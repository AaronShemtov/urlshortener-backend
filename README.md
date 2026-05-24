# urlshortener-backend

URL shortener backend service. Go microservice with **writer/reader pod split**,
deployed to **OCI OKE** via **Flux GitOps**. Backed by **OCI NoSQL Database**.

Companion repos:
- [urlshortener-frontend](https://github.com/AaronShemtov/urlshortener-frontend) — static UI served by nginx
- [personal-k8s](https://github.com/AaronShemtov/personal-k8s) — Flux manifests for the homelab cluster

## Architecture

A single Go binary runs in one of three modes, selected via `MODE` env var (or `--mode` flag):

| Mode     | Registered endpoints                  | Typical use |
|----------|---------------------------------------|-------------|
| `writer` | `POST /shorten`, `POST /createcustom` | Production writer Deployment, low replicas (1–2) |
| `reader` | `GET /{code}` → 301 redirect          | Production reader Deployment, HPA-scaled (95% of traffic) |
| `all`    | Everything                            | Local development only |

Health endpoints (`/healthz`, `/readyz`) are registered regardless of mode.

```
                              ┌─────────────────────┐
                              │  OCI NoSQL Database │
                              └──────────▲──────────┘
                                         │
        Writer (1×) ─── POST /shorten ───┤
                                         │  Instance Principal auth
        Reader (N×) ─── GET /{code}  ────┤  (no static keys)
                                         │
                              ┌──────────┴──────────┐
                              │  Cache (Noop / TBD) │
                              └─────────────────────┘
```

## Configuration

| Env var | Required | Default | Description |
|---------|----------|---------|-------------|
| `MODE` | no | `all` | `writer`, `reader`, or `all` |
| `PORT` | no | `8080` | HTTP listen port |
| `BASE_URL` | no | `https://1ms.my` | Public origin used in short URL responses |
| `SHORT_CODE_LENGTH` | no | `6` | Generated code length (3–32) |
| `NOSQL_ENDPOINT` | **yes** | — | e.g. `https://nosql.il-jerusalem-1.oci.oraclecloud.com` |
| `NOSQL_TABLE` | no | `urls` | Table name in NoSQL Database |
| `OCI_COMPARTMENT_OCID` | **yes** | — | Compartment that owns the NoSQL table |

`--mode=<value>` CLI flag overrides `MODE` env var. Both are validated.

## Authentication to OCI

The service authenticates to OCI NoSQL Database via **Instance Principal**:
the OKE worker node's identity is used directly — no API keys, no auth tokens,
no service account JSON.

Required IAM setup (one-time, in OCI Console):

1. **Dynamic Group** matching OKE worker nodes:
   ```
   ALL {instance.compartment.id = '<oke-nodes-compartment-ocid>'}
   ```
2. **Policy**:
   ```
   Allow dynamic-group <dg> to read   nosql-tables in compartment <c>
   Allow dynamic-group <dg> to manage nosql-rows   in compartment <c>
   ```

When the binary calls `iam.NewSignatureProviderWithInstancePrincipal(...)`, it
reads the node's identity from the OCI metadata service (`169.254.169.254`).
This only works inside OCI Compute (including OKE) — **running locally requires
running inside OKE or mocking the storage layer**.

## Build

### Local development build

```bash
go mod tidy
go build .
```

You can compile locally on any platform. **You cannot `go run`** outside of OKE
because Instance Principal auth requires OCI metadata service. For local testing
add a stub storage implementation (out of scope for MVP).

### Container image for OKE (Ampere A1 = arm64)

```bash
docker buildx build \
  --platform linux/arm64 \
  -t il-jerusalem-1.ocir.io/<tenancy-namespace>/urlshortener-backend:<tag> \
  --push .
```

Push requires OCIR login:
```bash
docker login il-jerusalem-1.ocir.io \
  -u '<tenancy-namespace>/<oci-user>' \
  -p '<auth-token>'   # from OCI Console → User → Auth Tokens
```

`Dockerfile` defaults to `arm64` because the cluster runs on Ampere A1.
Override with `--platform linux/amd64` if you need an x86 build.

## What's in the image

- `golang:1.22-alpine` builder (multi-stage, dropped after build)
- `gcr.io/distroless/static-debian12:nonroot` runtime — no shell, no libc, no package manager
- Stripped binary (~10 MB), runs as UID 65532
- No secrets baked in; everything via env vars

## What this PR sequence built

| PR | Layer |
|----|-------|
| 1  | Skeleton HTTP server with `/healthz` |
| 2  | Config + structured logging + graceful shutdown + mode flag |
| 3  | OCI NoSQL storage with Instance Principal auth |
| 4  | Business handlers + crypto-random shortcode + cache interface |
| 5  | Multi-stage Dockerfile + this README |

Each PR left the codebase in a buildable state.

## Originally migrated from

This service started as an AWS Lambda + DynamoDB function. The migration
to OKE involved replacing the Lambda runtime with `net/http`, swapping
`aws-sdk-go/dynamodb` for `oracle/nosql-go-sdk`, splitting the single binary
into separately-deployable writer/reader modes, and adopting Instance
Principal auth.
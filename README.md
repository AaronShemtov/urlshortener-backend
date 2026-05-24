# urlshortener-backend

URL shortener backend service. Go microservice with writer/reader pod split,
deployed to OCI OKE via Flux GitOps.

Companion repos:
- [urlshortener-frontend](https://github.com/AaronShemtov/urlshortener-frontend) — static UI
- [personal-k8s](https://github.com/AaronShemtov/personal-k8s) — Flux manifests

## Status

Iteration 1 — skeleton. Single HTTP endpoint `/healthz` for Kubernetes liveness.

Subsequent PRs add: env-config, OCI NoSQL storage, Redis cache, business logic,
Docker image, CI.

## Run locally

```bash
go run .
# in another terminal:
curl http://localhost:8080/healthz
# → ok
```

Port defaults to 8080, override with `PORT=9000 go run .`
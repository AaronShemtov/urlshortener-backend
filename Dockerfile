# syntax=docker/dockerfile:1.7

# ───────────── Build stage ─────────────
# golang:alpine has the smallest official Go toolchain image. We use --platform=$BUILDPLATFORM
# so the build runs natively on the CI runner architecture (usually amd64), even when we
# cross-compile to arm64 below. That avoids slow QEMU emulation during compilation.
FROM --platform=$BUILDPLATFORM golang:1.22-alpine AS builder

WORKDIR /src

# Cache module downloads independently of source changes — re-runs of `docker build`
# after touching source files won't re-download dependencies.
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source.
COPY . .

# These ARGs are populated automatically by `docker buildx build --platform=...`.
# Defaulting to arm64 because OKE worker nodes are Ampere A1 (Arm).
ARG TARGETOS=linux
ARG TARGETARCH=arm64

# Static binary, no CGO, stripped symbols, no debug info — minimum image size.
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build \
        -trimpath \
        -ldflags="-s -w" \
        -o /out/urlshortener-backend \
        .

# ───────────── Runtime stage ─────────────
# distroless/static:nonroot ships only ca-certificates and tzdata. No shell, no package
# manager, no libc — drastically smaller attack surface. Image is ~2 MB before our binary.
# Running as the nonroot user (UID 65532) is required to pass restricted PodSecurityStandard.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/urlshortener-backend /urlshortener-backend

USER nonroot:nonroot
EXPOSE 8080

# Mode is passed via env var (set by k8s Deployment), so no need to bake it into
# the image. Same image works for writer/reader/all by changing MODE at runtime.
ENTRYPOINT ["/urlshortener-backend"]
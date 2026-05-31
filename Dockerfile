# syntax=docker/dockerfile:1.7
#
# Multi-stage build for the library service.
#
# Stage 1 (builder) — compiles a static, CGO-disabled Linux/amd64 binary
# from cmd/library. Module + build caches are mounted from BuildKit so
# repeated builds are fast.
#
# Stage 2 (runtime) — distroless static-debian12:nonroot. No shell, no
# package manager, runs as a non-root user (uid 65532). The only thing
# inside is the binary itself.
#
# Migrations are NOT run on startup. Run `atlas migrate apply --env local`
# from a separate one-shot container or CI job BEFORE deploying a new
# version of this image. The binary expects the schema to already exist.
#
# Build:
#   podman build -t test-n-design-go:latest .
#
# Run (against an external Postgres + Redis):
#   podman run --rm -p 3000:3000 \
#     -e LIBRARY_DATABASE_URL='postgres://library:library@host.docker.internal:5432/library?sslmode=disable' \
#     -e LIBRARY_REDIS_URL='redis://host.docker.internal:6379/0' \
#     test-n-design-go:latest

# ----------------------------------------------------------------------------
# Stage 1: build
# ----------------------------------------------------------------------------
FROM golang:1.26-alpine AS builder

WORKDIR /src

# Cache module downloads independently of source changes.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Copy the source tree and compile a static binary.
# -trimpath removes absolute paths from the binary for reproducibility.
# -ldflags="-s -w" strips the symbol table and DWARF debug info.
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /out/library ./cmd/library

# ----------------------------------------------------------------------------
# Stage 2: runtime
# ----------------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

# Copy the binary from the builder stage. Distroless's default workdir is
# /home/nonroot; binary lives at a stable path.
COPY --from=builder /out/library /usr/local/bin/library

# Document the port the server listens on. Override LIBRARY_HTTP_PORT at
# runtime if a different port is needed.
EXPOSE 3000

# Sensible defaults; every value can be overridden at runtime.
# LIBRARY_DATABASE_URL and LIBRARY_REDIS_URL have no defaults — the binary
# fails fast on startup if either is unset.
ENV LIBRARY_HTTP_PORT=3000 \
    LIBRARY_LOG_FORMAT=json \
    LIBRARY_LOG_LEVEL=info

ENTRYPOINT ["/usr/local/bin/library"]

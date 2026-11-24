# syntax=docker/dockerfile:1

# ── Build stage ───────────────────────────────────────────────────────────────
# The Go toolchain lives ONLY here and is thrown away after compiling, so the
# shipped image never contains a compiler or the standard library source.
FROM golang:1.26-alpine AS build

WORKDIR /src

# Copy dependency manifests BEFORE the source. Docker caches each instruction,
# so `go mod download` re-runs only when go.mod or go.sum actually change.
COPY go.mod go.sum ./
RUN go mod download

# Copy the source afterwards because it changes on every edit, which keeps the
# expensive dependency layer above cached across normal rebuilds.
COPY . .

# Build both binaries from the same source tree. The API and the worker share
# every package, so one image can run either role; the Compose command picks it.
#   CGO_ENABLED=0  produces static binaries that run on any base image
#   -trimpath      strips this machine's filesystem paths out of the binaries
#   -s -w          drops symbol table and DWARF data (smaller artifacts)
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker

# ── Runtime stage ─────────────────────────────────────────────────────────────
# Alpine provides busybox wget for the Compose healthcheck. The Go binary is
# static, so it runs unchanged on this base.
FROM alpine:3.22

# Preserve the CA bundle the previous distroless base provided, and create the
# unprivileged user both binaries run as.
RUN apk add --no-cache ca-certificates \
    && addgroup -S nonroot \
    && adduser -S -G nonroot nonroot

WORKDIR /app

# The only things crossing the stage boundary. The worker service overrides the
# entrypoint with /app/worker.
COPY --from=build /out/api /app/api
COPY --from=build /out/worker /app/worker

# Documentation only — does not publish the port. Must match HTTP_ADDR's default.
EXPOSE 8080

USER nonroot:nonroot

# Exec form (no shell wrapper) so the process receives SIGTERM directly and the
# graceful shutdown in cmd/api/main.go (or cmd/worker/main.go) actually runs.
ENTRYPOINT ["/app/api"]

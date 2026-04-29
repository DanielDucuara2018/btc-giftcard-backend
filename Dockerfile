# ============================================================================
# builder — downloads deps + compiles all three static binaries.
#
# Also used directly by docker-compose for local development:
#   docker-compose mounts the source at /app and runs `go run ./cmd/...`
#   so the Go toolchain and cached modules are available without rebuilding.
# ============================================================================
FROM golang:1.25.8-bookworm AS builder
WORKDIR /app

# Cache dependency downloads separately from source changes.
COPY go.mod go.sum ./
RUN go mod download

# Build fully-static binaries.
# CGO_ENABLED=0 — no C libraries; binary works on any Linux (musl, glibc, distroless).
# GOOS=linux     — targets Linux regardless of the build host OS.
# -ldflags="-s -w" — strip symbol table and DWARF (~30% smaller).
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/api               ./cmd/api && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/fund_card_worker   ./cmd/worker/fund_card && \

# ============================================================================
# api — production runtime image for the HTTP API server.
#
# distroless/static: ~2 MB, no shell, no package manager.
# Runs as UID 65532 (nonroot) automatically — no USER instruction needed.
# Perfect match for CGO_ENABLED=0 static binaries.
# ============================================================================
FROM gcr.io/distroless/static:nonroot AS api
WORKDIR /app
COPY --from=builder /bin/api /bin/api
COPY config.toml /app/config.toml
EXPOSE 8081
CMD ["/bin/api"]

# ============================================================================
# fund_card_worker — production runtime image for the fund_card worker.
# ============================================================================
FROM gcr.io/distroless/static:nonroot AS fund_card_worker
WORKDIR /app
COPY --from=builder /bin/fund_card_worker /bin/fund_card_worker
COPY config.toml /app/config.toml
CMD ["/bin/fund_card_worker"]


# ── Build ──────────────────────────────────────────────
FROM golang:1.26-bookworm AS builder

ENV GOPROXY=https://goproxy.cn,direct
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /audit-gateway ./cmd/audit-gateway

# ── Runtime ────────────────────────────────────────────
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && \
    rm -rf /var/lib/apt/lists/*

COPY --from=builder /audit-gateway /usr/local/bin/audit-gateway

EXPOSE 8080
ENTRYPOINT ["audit-gateway"]

# syntax=docker/dockerfile:1
# Multi-stage build — CGo required for go-duckdb.

##############################################################################
# Builder
##############################################################################
FROM golang:1.23-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 GOOS=linux \
    go build -ldflags="-s -w" -o /cotel ./cmd/cotel

##############################################################################
# Runtime
##############################################################################
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /cotel /usr/local/bin/cotel

# Data directory — mount a named volume here.
VOLUME /data

EXPOSE 4318 8080

ENV COTEL_DB_PATH=/data/cotel.duckdb \
    COTEL_INGEST_ADDR=:4318 \
    COTEL_DASH_ADDR=:8080

ENTRYPOINT ["/usr/local/bin/cotel"]

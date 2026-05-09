# cotel — Claude Code Telemetry

One Docker container. OTLP ingest + interactive analytics dashboard.

## Quick start

```bash
docker run -d \
  --name cotel \
  -p 4318:4318 \
  -p 8080:8080 \
  -v cotel-data:/data \
  ghcr.io/flopsstuff/cotel:main
```

> **Available tags:** `:main` (latest main branch), `:0.x` (semver releases), `:sha-…` (per-commit). There is no `:latest` tag.

Open **http://localhost:8080** for the dashboard.

## Point Claude Code at cotel

Add to your `~/.claude/settings.json`:

```json
{
  "env": {
    "CLAUDE_CODE_ENABLE_TELEMETRY": "1",
    "CLAUDE_CODE_ENHANCED_TELEMETRY_BETA": "1",
    "OTEL_TRACES_EXPORTER": "otlp",
    "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://localhost:4318/v1/traces"
  }
}
```

Restart Claude Code. Telemetry starts flowing immediately.

## Ports

| Port | Purpose |
|------|---------|
| 4318 | OTLP/HTTP trace ingest (`POST /v1/traces`) |
| 8080 | Analytics dashboard |

## Data

Data lives in the named volume (`cotel-data`) at `/data/cotel.duckdb`. You can query it directly:

```bash
docker run --rm -v cotel-data:/data ubuntu \
  duckdb /data/cotel.duckdb "SELECT model, COUNT(*) FROM spans GROUP BY model"
```

## Retention defaults

| Tier | Period | Storage |
|------|--------|---------|
| Raw spans | 30 days | `spans` table |
| Daily aggregates | 90 days | `daily_usage` table |

Override with environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `COTEL_RETENTION_RAW_DAYS` | `30` | Delete raw spans older than this many days |
| `COTEL_RETENTION_AGGREGATE_DAYS` | `90` | Delete daily aggregate rows older than this many days |
| `COTEL_RETENTION_INTERVAL` | `6h` | How often the retention worker runs (Go duration string, e.g. `1h`, `30m`) |

## Development

```bash
# Build locally
docker compose build

# Run locally
docker compose up

# Run tests
go test ./...
```

## Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `COTEL_DB_PATH` | `/data/cotel.duckdb` | DuckDB file path |
| `COTEL_INGEST_ADDR` | `:4318` | Ingest listener address |
| `COTEL_DASH_ADDR` | `:8080` | Dashboard listener address |
| `COTEL_RETENTION_RAW_DAYS` | `30` | Raw span retention in days |
| `COTEL_RETENTION_AGGREGATE_DAYS` | `90` | Daily aggregate retention in days |
| `COTEL_RETENTION_INTERVAL` | `6h` | Retention worker tick interval (Go duration) |

## Architecture

```
Claude Code  →  OTLP/HTTP (port 4318)  →  ingest handler
                                           ↓
                                        DuckDB (named volume)
                                           ↓
dashboard (port 8080)  ←─────────────────┘
```

Single Go binary, single DuckDB file, single named volume. No sidecars.

---
layout: home

hero:
  name: cotel
  text: Claude Code Telemetry
  tagline: One container. OTLP ingest + interactive analytics dashboard for Claude Code.
  actions:
    - theme: brand
      text: Quick start
      link: '#quick-start'
    - theme: alt
      text: GitHub
      link: https://github.com/Flopsstuff/cotel

features:
  - title: Single-container deploy
    details: One docker run with a named volume. No sidecars, no orchestration. Runs on a Raspberry Pi.
  - title: OTLP-native ingest
    details: Standard OpenTelemetry Protocol on port 4318. Point Claude Code at it and telemetry starts flowing.
  - title: Analytics dashboard
    details: Session, cost, model, and tool breakdowns with time-range filtering — all queried from embedded DuckDB.
---

## Quick start

```bash
docker run -d \
  --name cotel \
  -p 4318:4318 \
  -p 8080:8080 \
  -v cotel-data:/data \
  ghcr.io/flopsstuff/cotel:main
```

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

## Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `COTEL_DB_PATH` | `/data/cotel.duckdb` | DuckDB file path |
| `COTEL_INGEST_ADDR` | `:4318` | Ingest listener address |
| `COTEL_DASH_ADDR` | `:8080` | Dashboard listener address |
| `COTEL_RETENTION_RAW_DAYS` | `30` | Raw span retention in days (roll-up consumes whole days, so spans survive up to a day longer) |
| `COTEL_RETENTION_AGGREGATE_DAYS` | `90` | Daily aggregate retention in days |
| `COTEL_RETENTION_INTERVAL` | `6h` | Retention worker tick interval |

## Data & retention

Data lives in the named volume at `/data/cotel.duckdb`. You can query it directly:

```bash
docker run --rm -v cotel-data:/data ubuntu \
  duckdb /data/cotel.duckdb "SELECT model, COUNT(*) FROM spans GROUP BY model"
```

| Tier | Period | Table |
|------|--------|-------|
| Raw spans | 30 days | `spans` |
| Daily aggregates | 90 days | `daily_usage` |

## Architecture

```
Claude Code  →  OTLP/HTTP (port 4318)  →  ingest handler
                                           ↓
                                        DuckDB (named volume)
                                           ↓
dashboard (port 8080)  ←─────────────────┘
```

Single Go binary. Single DuckDB file. Single named volume. No sidecars.

## Documentation

- [Architecture Decisions](/decisions/) — ADRs explaining key technical choices
- [Design Docs](/design/) — UI information architecture, page specs, components, tokens

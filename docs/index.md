---
layout: home

hero:
  name: cotel
  text: Claude Code Telemetry
  tagline: Self-hosted OTLP ingest and a cost dashboard for every Claude Code sender — people, AI agents and hosts. One container.
  actions:
    - theme: brand
      text: Quick start
      link: '#quick-start'
    - theme: alt
      text: Paperclip agents
      link: '#paperclip-agents'
    - theme: alt
      text: GitHub
      link: https://github.com/Flopsstuff/cotel

features:
  - title: Usage by actor
    details: Every sender is an actor — a person, an AI agent or a host. Sessions, tokens and spend per actor, each with its own token, page and activity.
  - title: Cost by model
    details: Spend computed per span from a built-in Claude price table, broken down by model, session and day. Repricing an old range is one backfill command.
  - title: Ingest keys for agent fleets
    details: One shared cotel_ik_ key authorizes a whole fleet's OTLP headers; actor tokens still say who each sender is.
  - title: Native Paperclip integration
    details: Paperclip agents are attributed by paperclip.agent.id. One Agent observability setting with an ingest key, no per-agent env.
  - title: Single-container deploy
    details: One docker run with a named volume. No sidecars, no orchestration. Runs on a Raspberry Pi.
  - title: OTLP-native ingest
    details: Standard OpenTelemetry Protocol on port 4318, JSON or protobuf. Point Claude Code at it and telemetry starts flowing.
---

## Quick start

```bash
docker run -d \
  --name cotel \
  -p 4318:4318 \
  -p 8080:8080 \
  -v cotel-data:/data \
  ghcr.io/flopsstuff/cotel:latest
```

Open **http://localhost:8080** → **Setup** for the guided onboarding.

## Point Claude Code at cotel

Add to your `~/.claude/settings.json`:

```json
{
  "env": {
    "CLAUDE_CODE_ENABLE_TELEMETRY": "1",
    "CLAUDE_CODE_ENHANCED_TELEMETRY_BETA": "1",
    "OTEL_TRACES_EXPORTER": "otlp",
    "OTEL_EXPORTER_OTLP_PROTOCOL": "http/json",
    "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://localhost:4318/v1/traces",
    "OTEL_EXPORTER_OTLP_HEADERS": "Authorization=Bearer cotel_<your-actor-token>"
  }
}
```

Restart Claude Code. Telemetry starts flowing immediately.

The token comes from **Actors** → **Add actor** in the dashboard and puts the spans under that actor. While **Allow anonymous OTLP** is on (the default) the header is optional and spans without it land on the Anonymous actor. For a fleet that shares one set of headers, send an ingest key instead: `Authorization=Bearer cotel_ik_<key>`. See [Actors and Authentication](./operations/actors-and-auth).

## Paperclip agents

Paperclip gives every agent the same OTLP headers, so its agents cannot each send their own token. cotel attributes their spans by the `paperclip.agent.id` attribute Paperclip already sets:

1. In cotel, **Setup** → **Settings** → **Ingest keys** → create a key.
2. In Paperclip, **Company Settings** → **General** → **Agent observability**: destination **Custom OTLP**, your ingest base URL (e.g. `https://otlp.example.com`), header `Authorization` = `Bearer cotel_ik_<key>`, export on.
3. In cotel, open each agent's actor page and fill in **Paperclip agent** with its UUID.

No per-agent env, helper or managed settings on the Paperclip host. Details: [Paperclip agents](./operations/actors-and-auth#paperclip-agents).

## Ports

| Port | Purpose |
|------|---------|
| 4318 | OTLP/HTTP ingest (`POST /v1/traces`; `POST /v1/logs` is accepted and discarded) |
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
| `COTEL_SNAPSHOT_DIR` | _(unset - snapshots off)_ | Where the snapshot worker exports the whole database (compose sets `/snapshots`); empty disables snapshots. See [Database Snapshots and Restore](./operations/duckdb-snapshots) |
| `COTEL_SNAPSHOT_INTERVAL` | `24h` | How often a snapshot is taken (Go duration) |
| `COTEL_SNAPSHOT_KEEP` | `14` | How many complete snapshots to keep - 14 days at the default interval |
| `COTEL_SNAPSHOT_VOLUME` | `cotel-snapshots` | Read by `docker-compose.yml`, not the binary: the Docker volume mounted at `/snapshots` |
| `SNAPSHOT_CHECK` | `auto` | Read by `scripts/probe-healthz.sh`, not the binary: `auto` pages only on an affirmatively broken snapshot, `require` also when the instance claims none, `off` never asks. See [Production health probes](./operations/health-probe#the-snapshot-claim-asked-of-a-second-endpoint) |
| `SNAPSHOT_STALE_AFTER_SECONDS` | `172800` | Read by `scripts/probe-healthz.sh`, not the binary: how old the newest snapshot may be - two `COTEL_SNAPSHOT_INTERVAL` periods at the shipped `24h` |
| `API_HEALTH_URL` | _(derived from the `/healthz` URL)_ | Read by `scripts/probe-healthz.sh`, not the binary: overrides the derived `/api/v1/health` address |

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

- [Actors and Authentication](/operations/actors-and-auth) — actor tokens, ingest keys, Paperclip agents, allow-anonymous mode
- [Architecture Decisions](/decisions/) — ADRs explaining key technical choices
- [Design Docs](/design/) — UI information architecture, page specs, components, tokens
- [Production health probes](/operations/health-probe) — the best-effort watcher of the public ingest, what it cannot see, and who is woken when it is red

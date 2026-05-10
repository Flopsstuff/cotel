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
    "OTEL_EXPORTER_OTLP_PROTOCOL": "http/json",
    "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://localhost:4318/v1/traces"
  }
}
```

Restart Claude Code. Telemetry starts flowing immediately.

## Users and authentication

cotel ships with token-based authentication for OTLP ingest. Tokens are tied to named users.

### Creating users and tokens

Open the dashboard → **Users** (people icon in the sidebar) → **Add user**. Give the user a name (e.g. the machine or agent name sending telemetry). The token is shown once on creation — copy and store it. Rotating or deleting a user revokes its token immediately.

Users appear as a dimension in the dashboard; spans ingested with a specific token are attributed to that user.

### Allow-anonymous mode

By default, cotel accepts spans without an `Authorization` header and attributes them to no user. Once you have created at least one token, you can enforce strict authentication:

Open **Settings** (gear icon in the sidebar) → disable **Allow anonymous OTLP**.

When disabled, every OTLP request must carry `Authorization: Bearer cotel_<token>`. Requests without a valid token receive a `401 Unauthorized`. Re-enable the toggle at any time to allow unauthenticated spans again.

### Token format

All tokens start with `cotel_` and are stored as SHA-256 hashes in the database — the plaintext is never persisted after creation.

## Export and import

cotel can export the full dataset as a ZIP archive and reimport it into a different instance. Use this for:
- Migrating to a new host (backup + restore).
- Copying a production dataset to a dev container for debugging.
- Keeping an offline archive before the retention window erases raw spans.

### From the dashboard

Open the **Setup** page → scroll to the **Export / Import** panel. Click **Export** to download a ZIP archive containing `spans.csv`, `daily_usage.csv`, and a `manifest.json`. Drag a previously exported ZIP onto the **Import** target to restore it.

### Via API

```bash
# Export
curl -s http://localhost:8080/api/v1/export -o cotel-backup.zip

# Import
curl -s -X POST http://localhost:8080/api/v1/import \
  -F "file=@cotel-backup.zip"
```

Both endpoints require a valid `Authorization: Bearer cotel_<token>` header when anonymous mode is disabled.

Import is **idempotent**: re-importing the same archive inserts 0 rows.

## Publishing with Cloudflare

Use [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/) to expose cotel over HTTPS without opening inbound ports. The dashboard is protected by Cloudflare Zero Trust; OTLP ingest is protected by a bearer token you create in the cotel Tokens page.

### Prerequisites

- A Cloudflare account (free tier is sufficient)
- A domain managed by Cloudflare DNS
- [Cloudflare Zero Trust](https://one.dash.cloudflare.com/) enabled on your account (free for up to 50 users)

### 1. Create a tunnel

1. Go to **Cloudflare Zero Trust → Networks → Tunnels → Add a tunnel**.
2. Choose **Cloudflared**, name the tunnel (e.g. `cotel`), and click **Save tunnel**.
3. Copy the tunnel token shown on the next screen — you will use it as `CLOUDFLARE_TUNNEL_TOKEN`.

### 2. Configure public hostnames

In the tunnel's **Public Hostnames** tab, add two entries:

| Subdomain | Domain | Service |
|-----------|--------|---------|
| `cotel` | `yourdomain.com` | `http://localhost:8080` |
| `cotel-ingest` | `yourdomain.com` | `http://localhost:4318` |

Replace the subdomains and domain with your own values.

### 3. Protect the dashboard with Zero Trust

In **Zero Trust → Access → Applications**, create a **Self-hosted** application for your dashboard hostname (e.g. `https://cotel.yourdomain.com`). Add an access policy — for example "Allow email ending in `@yourcompany.com`".

Leave the OTLP ingest hostname (`cotel-ingest.yourdomain.com`) unprotected in Zero Trust. Authentication for ingest is handled by the cotel bearer token instead.

### 4. Start cotel with the tunnel token

Pass `CLOUDFLARE_TUNNEL_TOKEN` when running the container:

```bash
docker run -d \
  --name cotel \
  -v cotel-data:/data \
  -e CLOUDFLARE_TUNNEL_TOKEN=<your-tunnel-token> \
  ghcr.io/flopsstuff/cotel:main
```

No `-p` flags are needed — Cloudflare Tunnel uses outbound connections only.

### 5. Create an agent token

Open your dashboard URL (e.g. `https://cotel.yourdomain.com`), go to **Tokens** (the key icon in the sidebar), and click **New token**. Give it a name (e.g. the agent's hostname or machine name) and copy the token — it is shown only once.

### 6. Configure Claude Code

Add to your `~/.claude/settings.json`:

```json
{
  "env": {
    "CLAUDE_CODE_ENABLE_TELEMETRY": "1",
    "CLAUDE_CODE_ENHANCED_TELEMETRY_BETA": "1",
    "OTEL_TRACES_EXPORTER": "otlp",
    "OTEL_EXPORTER_OTLP_PROTOCOL": "http/json",
    "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "https://cotel-ingest.yourdomain.com/v1/traces",
    "OTEL_EXPORTER_OTLP_HEADERS": "Authorization=Bearer cotel_<your-token>"
  }
}
```

Replace `cotel-ingest.yourdomain.com` with your actual OTLP hostname, and `cotel_<your-token>` with the token you copied above. Restart Claude Code to apply the changes.

> **Token enforcement:** cotel runs in local (no-auth) mode until the first token is created. Once any token exists, every OTLP request must carry a valid `Authorization: Bearer cotel_...` header.

## Ports

| Port | Purpose |
|------|---------|
| 4318 | OTLP/HTTP trace ingest (`POST /v1/traces`) |
| 8080 | Analytics dashboard |

> **HTTP-only:** cotel speaks OTLP/HTTP (`application/x-protobuf` and `application/json`). There is no gRPC listener on port 4317. If spans are silently dropped, ensure `OTEL_EXPORTER_OTLP_PROTOCOL=http/json` (or `http/protobuf`) is set — the OTel SDK default is `grpc`, which will fail against cotel.

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
| `CLOUDFLARE_TUNNEL_TOKEN` | _(unset)_ | When set, starts `cloudflared tunnel run` before cotel; enables public HTTPS access via Cloudflare Tunnel |
| `COTEL_PUBLIC_INGEST_URL` | _(unset)_ | Absolute `http`/`https` URL of the public OTLP ingest endpoint (e.g. `https://cotel-ingest.yourdomain.com`). When set, the Setup page substitutes this URL into the copy-paste Claude Code snippets. |

## GitHub → Paperclip issue routing

Opening a GitHub issue on this repo automatically creates a linked issue in the cotel Paperclip project. Closing it marks the Paperclip issue done.

Labels determine the assignee:

| Label | Assignee |
|-------|----------|
| `bug` | Coder |
| `design`, `ux` | UXDesigner |
| _(none / other)_ | CTO |

Routing is defined in `.github/workflows/paperclip-issue-sync.yml`.

## Architecture

```
Claude Code  →  OTLP/HTTP (port 4318)  →  ingest handler
                                           ↓
                                        DuckDB (named volume)
                                           ↓
dashboard (port 8080)  ←─────────────────┘
```

Single Go binary, single DuckDB file, single named volume. No sidecars.

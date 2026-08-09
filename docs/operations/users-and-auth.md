# Users and Authentication

cotel ties OTLP ingest to named users. Each user has a bearer token; spans ingested with that token are attributed to that user across the dashboard. This guide covers creating and managing users, configuring Claude Code to send an authenticated token, the allow-anonymous toggle, and what happens when strict auth is enforced.

## Concepts

| Term | Meaning |
|---|---|
| **User** | A named principal — typically a machine, agent, or developer sending telemetry |
| **Token** | A `cotel_<64 hex chars>` bearer token bound to one user |
| **Allow-anonymous** | When on (default), spans without a token are accepted and shown without a user label |

Tokens are plain bearer strings stored in the database. They are always retrievable from the dashboard — rotating a token replaces it with a new one and immediately revokes the old one.

## Dashboard walkthrough

### The Users list

Click the **people icon** in the left sidebar. The Users page is a single list with one row per user (plus an **Anonymous** row when unattributed spans exist). Columns:

| Column | Meaning |
|---|---|
| **Name** | The user's display name |
| **Cost** | Spend within the selected range |
| **Sessions** | Distinct sessions within the selected range |
| **Created** | When the user was created (all-time) |
| **Last seen** | Most recent span (all-time) |

There is no token column and no per-row buttons — a row is a single click through to that user's own page.

**Range switcher.** A segmented control above the list scopes **Cost** and **Sessions** to a rolling window — All / Year / Month / Week / Day, defaulting to **Month** (last 30 days). The two columns it governs show the active window in their headers (e.g. `Cost (30d)`). Created and Last seen are always all-time. Your choice is remembered between visits via the `cotel_users_range` cookie.

**Sorting, search, pagination.** Every column header sorts the *entire* user set (not just the visible page) through the API; click again to flip direction. The search box filters by name. The list pages at 50 rows; controls appear only when there are more.

### Creating a user

1. Click **Add user** (top-right of the page).
2. Enter a display name — use something that identifies the sender, such as a machine hostname, agent name, or developer alias (e.g. `alice`, `prod-box`, `ci-runner`).
3. Click **Create**.

A banner appears showing the new user's token. Copy it now — you will paste it into Claude Code settings in the next step. You can retrieve the token again any time from the user's page.

### The per-user page

Click any row to open `/users/<id>`. This page shows the user's name, their range-scoped **Cost** and **Sessions** (with the same range switcher as the list), all-time **Created** and **Last seen**, and the actions:

- **Token** with a copy button — the full `cotel_…` string, retrievable any time.
- **Rotate token** — generates a new token and immediately revokes the old one. Any Claude Code instance using the old token will start receiving `401 Unauthorized` until updated. Copy the new token and update `OTEL_EXPORTER_OTLP_HEADERS` in every `settings.json` that used the old one.
- **Delete user** — opens a modal with two choices: *Delete user only* (revokes the token; history stays attributed to the name and is reversible by re-creating the user) or *Delete user + history* (permanently removes the user and all their telemetry; type the name to confirm). After deletion you return to the list.
- **View activity** and **View sessions** — jump to the dashboard and Sessions views filtered to this user.

### The Anonymous user

Unattributed spans (ingested with allow-anonymous on) roll up under a synthetic **Anonymous** row that sorts and pages inline with real users. Its per-user page has no token and no rotate — only **Delete anonymous data** (permanently removes all unattributed spans) alongside the activity and sessions links.

## Configuring Claude Code

Add `OTEL_EXPORTER_OTLP_HEADERS` to `~/.claude/settings.json`:

```json
{
  "env": {
    "CLAUDE_CODE_ENABLE_TELEMETRY": "1",
    "CLAUDE_CODE_ENHANCED_TELEMETRY_BETA": "1",
    "OTEL_TRACES_EXPORTER": "otlp",
    "OTEL_EXPORTER_OTLP_PROTOCOL": "http/json",
    "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://localhost:4318/v1/traces",
    "OTEL_EXPORTER_OTLP_HEADERS": "Authorization=Bearer cotel_<your-token>"
  }
}
```

Replace `cotel_<your-token>` with the token you copied from the dashboard. Restart Claude Code to apply the change.

### Multiple machines or agents

Create a separate user for each sender — one per developer or machine. Each instance gets its own `settings.json` entry with its own `OTEL_EXPORTER_OTLP_HEADERS` value. Spans appear with the corresponding user label in the dashboard.

### Remote ingest (Cloudflare Tunnel)

When using a public ingest URL, the endpoint changes but the header format is identical:

```json
"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "https://ingest.yourdomain.com/v1/traces",
"OTEL_EXPORTER_OTLP_HEADERS": "Authorization=Bearer cotel_<your-token>"
```

See the [Cloudflare Tunnel guide](./cloudflare-tunnel-remote.md) for end-to-end setup.

## Allow-anonymous mode

The **Allow anonymous OTLP** toggle lives in **Setup** → **Settings** tab.

| State | Behaviour |
|---|---|
| **On** (default) | OTLP requests without a valid `Authorization` header are accepted; spans are stored with no user attribution |
| **Off** | Every OTLP request must present `Authorization: Bearer cotel_<token>`; requests without a valid token receive `401 Unauthorized` |

### When to keep allow-anonymous on

- Single-user local setup where token management adds friction.
- Initial evaluation — start collecting spans immediately, add users later.
- Migrating from a token-less setup: leave anonymous on until all senders are updated.

### When to disable allow-anonymous

- Multi-user environments where per-user cost attribution must be accurate.
- Public ingest endpoints (e.g. exposed via Cloudflare Tunnel) where unauthenticated writes from the internet must be blocked.
- Compliance or audit requirements where all ingest must be attributable.

### Disabling allow-anonymous

1. Create at least one user (see above) so at least one valid token exists.
2. Configure all senders with their token before flipping the toggle.
3. Open **Setup** → **Settings** tab → disable **Allow anonymous OTLP**.
4. Verify with `curl`:

```sh
# Should return {} (OK)
curl -s -w "\n%{http_code}" \
  -H "Authorization: Bearer cotel_<your-token>" \
  -H "Content-Type: application/json" \
  -d '{"resourceSpans":[]}' \
  http://localhost:4318/v1/traces

# Should return 401
curl -s -w "\n%{http_code}" \
  -H "Content-Type: application/json" \
  -d '{"resourceSpans":[]}' \
  http://localhost:4318/v1/traces
```

### Re-enabling allow-anonymous

Open **Setup** → **Settings** tab → enable **Allow anonymous OTLP**. Takes effect immediately with no restart.

## What happens on 401

When a request is rejected the ingest endpoint returns:

```
HTTP/1.1 401 Unauthorized
Content-Type: application/json

{"error":"unauthorized"}
```

Claude Code's OTel SDK logs a warning and drops the trace batch. No spans are stored.

**Recovery:** copy the correct token from the Users page and update `OTEL_EXPORTER_OTLP_HEADERS` in `settings.json`, then restart Claude Code.

## Token security

User tokens are 32-byte cryptographically random values encoded as lowercase hex, prefixed with `cotel_`. They are stored as **plaintext** in the database and are always accessible from the dashboard — you can retrieve a token any time without needing to save it at creation.

> **Note:** An older `api_tokens` table (pre-FLO-88) stored hashed tokens for dashboard API auth. That table is kept in the schema for upgrade safety but is no longer used for authentication.

### Protecting the database

Because tokens are stored in plaintext, anyone with filesystem access to the DuckDB file (`/data/cotel.duckdb` inside the container) can read all tokens. Protect with:

- Standard Docker volume permissions (owned by the container user, not world-readable on the host).
- Cloudflare Access or equivalent to restrict dashboard access to trusted operators.
- Rotate any token that may have been exposed.

## References

- [ADR-0004 — Multi-User Separation](../decisions/0004-multi-user-separation.md)
- [ADR-0006 — Cloudflare Tunnel + Token Auth](../decisions/0006-cloudflare-tunnel-and-token-auth.md)
- [Cloudflare Tunnel — Token Mode](./cloudflare-tunnel-remote.md)
- [Cloudflare Tunnel — Local Config](./cloudflare-tunnel-local.md)

# ADR-0004: Multi-User Telemetry Separation via `user.id`

Date: 2026-05-10  
Status: accepted

---

## Context

cotel was originally designed as a personal, single-user telemetry sink. A single instance
is shared by all Claude Code sessions running on the same machine, which creates an
ambiguity problem in shared environments:

- A developer machine used by multiple SSH users or CI agents produces mixed telemetry.
- Team deployments (one cotel instance behind a shared reverse proxy) have no way to
  attribute costs and usage to individual engineers.
- Automated agents (Paperclip workers, CI pipelines) should be distinguishable from
  interactive developer sessions.

The requirement is to add an opt-in identifier so that incoming spans can be separated
per user, while the absence of the identifier continues to work exactly as before.

---

## Options considered

### Option A — HTTP Authorization header

Add a Bearer token on the OTLP ingest side (`Authorization: Bearer <user-token>`).  
**Rejected**: Claude Code's built-in OTLP exporter does not support custom request headers
for the ingest endpoint. Requires patching the exporter or using a reverse proxy shim —
both significant operational complexity.

### Option B — Custom URL path segment

Route by user: `POST /v1/traces/<user-id>`.  
**Rejected**: Claude Code hard-codes the OTLP path `/v1/traces`. The path is not
configurable without forking the client.

### Option C — OTLP resource attribute `user.id`

Use the standard OpenTelemetry mechanism for process-wide metadata: resource attributes.
Users set `OTEL_RESOURCE_ATTRIBUTES=user.id=alice` in their environment. The Claude Code
OTLP exporter merges these into the `resource` section of every exported payload.  
**Accepted.**

### Option D — Service name sub-field

Encode the user inside `service.name` (e.g., `claude-code/alice`).  
**Rejected**: `service.name` is a span grouping key, not a user identity. Overloading it
would break model/tool aggregations that already group by service name.

---

## Decision

**OTLP resource attribute `user.id`**, extracted at ingest time and stored in a dedicated
`user_id VARCHAR` column in the `spans` table.

### Attribute key priority

| Priority | Key | Rationale |
|---|---|---|
| 1 | `user.id` | OpenTelemetry semantic convention (`user.*` namespace) |
| 2 | `claude.user` | Claude-specific shorthand for convenience |
| 3 | `cc.user.id` | Claude Code–specific key for tooling that sets OTEL vars |

Resource attributes take precedence over span attributes (resource = process-wide identity;
span = per-operation metadata).

### Configuration

```bash
# Single user — set once in shell profile or systemd unit
export OTEL_RESOURCE_ATTRIBUTES="user.id=alice"
claude

# Multiple attributes — comma-separated
export OTEL_RESOURCE_ATTRIBUTES="user.id=alice,deployment.environment=dev"
claude
```

### Default when absent

Spans without any recognised user key are stored with `user_id = NULL`. The dashboard
defaults to **"All users"** (no filter applied), so existing deployments are unaffected.

---

## Implementation

### Schema (v3 migration)

```sql
-- New column — NULL-safe, no DEFAULT so absence is explicit
ALTER TABLE spans ADD COLUMN IF NOT EXISTS user_id VARCHAR;
CREATE INDEX IF NOT EXISTS idx_spans_user_id ON spans(user_id);

ALTER TABLE daily_usage ADD COLUMN IF NOT EXISTS user_id VARCHAR;
```

The migration is idempotent (`ADD COLUMN IF NOT EXISTS`) — existing databases upgrade
in-place on next start without data loss.

### Ingest

`internal/ingest/handler.go` extracts the user key from resource attributes (checked
before span attributes) and sets `Span.UserID`. Empty string is converted to SQL `NULL`
via `nullableStr()` in the storage layer.

### API

All six data endpoints (`/overview`, `/sessions`, `/costs`, `/tools`, `/models`,
`/history`) accept an optional `?user_id=<value>` query parameter that appends
`AND user_id = ?` to the underlying query. Omitting the parameter returns all data
regardless of user.

A new endpoint `GET /api/v1/users` returns the sorted list of distinct non-null user IDs,
used by the frontend to populate the selector.

### Frontend

A user selector `<select>` is rendered at the bottom of the sidebar, populated via the
`/api/v1/users` endpoint. Selection is stored in a React context (`UserContext`) and
propagated to every page's API hook. The default selection is "All users" (empty string
→ no filter).

---

## Consequences

**Positive:**
- Zero-config backwards compatibility: deployments without `OTEL_RESOURCE_ATTRIBUTES`
  continue working identically.
- No changes to Claude Code itself — leverages the standard OTLP environment variable.
- `user_id` is a first-class column: indexed, aggregatable, and queryable via DuckDB CLI.
- The retention rollup (`daily_usage`) preserves user attribution via `MAX(user_id)`.

**Negative / trade-offs:**
- `user_id` is user-supplied and unvalidated — any string is accepted. There is no
  authentication gate on the ingest side (cotel is a trusted-network service by design).
- The `daily_usage` PK does not include `user_id` (adding it would require a table
  recreate). For aggregates, `MAX(user_id)` is used per `(day, session_id, model,
  tool_name)` group — valid because session IDs are unique per user in practice.
- If two users somehow share a session ID (astronomically unlikely given UUID generation),
  aggregates would conflate their data. Acceptable for the current scale.

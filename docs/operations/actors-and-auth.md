# Actors and Authentication

cotel ties OTLP ingest to named **actors**: whoever sends telemetry, whether a person, an AI agent or a host. Each actor has an **actor token**; spans ingested with that token are attributed to that actor across the dashboard. Senders that must all share one set of headers authorize with a shared **ingest key** instead and name their actor in a separate header. This guide covers creating and managing actors, configuring Claude Code to send its actor token, ingest keys, the allow-anonymous toggle, and what happens when strict auth is enforced.

## Concepts

| Term | Meaning |
|---|---|
| **Actor** | A named sender of telemetry — a developer, an AI agent or a machine. Called a *user* before schema v11 ([ADR-0027](../decisions/0027-users-become-actors.md)) |
| **Actor token** | A `cotel_<64 hex chars>` token bound to one actor. Sent as the bearer token, or in `X-Cotel-Actor-Token` next to an ingest key |
| **Ingest key** | A shared `cotel_ik_<64 hex chars>` bearer credential that authorizes OTLP ingest only and identifies nobody ([ADR-0028](../decisions/0028-ingest-keys-and-actor-tokens.md)) |
| **Allow-anonymous** | When on (default), spans sent without a bearer token are accepted and shown without an actor label |

Actor tokens are plain strings stored in the database. They are always retrievable from the dashboard — rotating an actor token replaces it with a new one and immediately revokes the old one. Ingest keys work differently; see [Ingest keys](#ingest-keys-one-key-for-many-senders).

## Dashboard walkthrough

### The Actors list

Click the **people icon** in the left sidebar. The Actors page is a single list with one row per actor (plus an **Anonymous** row when unattributed spans exist). Columns:

| Column | Meaning |
|---|---|
| **Name** | The actor's display name |
| **Cost** | Spend within the selected range |
| **Sessions** | Distinct sessions within the selected range |
| **Created** | When the actor was created (all-time) |
| **Last seen** | Most recent span (all-time) |

There is no actor-token column and no per-row buttons — a row is a single click through to that actor's own page.

**Range switcher.** A segmented control above the list scopes **Cost** and **Sessions** to a rolling window — All / Year / Month / Week / Day, defaulting to **Month** (last 30 days). The two columns it governs show the active window in their headers (e.g. `Cost (30d)`). Created and Last seen are always all-time. Your choice is remembered between visits via the `cotel_actors_range` cookie.

**Sorting, search, pagination.** Every column header sorts the *entire* actor set (not just the visible page) through the API; click again to flip direction. The search box filters by name. The list pages at 50 rows; controls appear only when there are more.

### Creating an actor

1. Click **Add actor** (top-right of the page).
2. Enter a display name — use something that identifies the sender, such as a machine hostname, agent name, or developer alias (e.g. `alice`, `prod-box`, `ci-runner`).
3. Click **Create**.

A banner appears showing the new actor's actor token. Copy it now — you will paste it into Claude Code settings in the next step. You can retrieve it again any time from the actor's page.

### The per-actor page

Click any row to open `/actors/<id>` (the old `/users/<id>` links redirect there). This page shows the actor's name, their range-scoped **Cost** and **Sessions** (with the same range switcher as the list), all-time **Created** and **Last seen**, and the actions:

- The **actor token** with a copy button — the full `cotel_…` string, retrievable any time.
- **Rotate token** — generates a new actor token and immediately revokes the old one. Any Claude Code instance sending the old token as its bearer will start receiving `401 Unauthorized` until updated; one sending it in `X-Cotel-Actor-Token` next to an ingest key keeps being accepted, but its spans land under **Anonymous**. Copy the new token and update `OTEL_EXPORTER_OTLP_HEADERS` (or `COTEL_ACTOR_TOKEN`) everywhere the old one was used.
- **Delete actor** — opens a modal with two choices: *Delete actor only* (revokes the actor token; history stays attributed to the name and is reversible by re-creating the actor) or *Delete actor + history* (permanently removes the actor and all their telemetry; type the name to confirm). After deletion you return to the list.
- **View activity** and **View sessions** — jump to the dashboard and Sessions views filtered to this actor.

### The Anonymous actor

Unattributed spans (ingested with allow-anonymous on) roll up under a synthetic **Anonymous** row that sorts and pages inline with real actors. Its per-actor page has no actor token and no rotate — only **Delete anonymous data** (permanently removes all unattributed spans) alongside the activity and sessions links.

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
    "OTEL_EXPORTER_OTLP_HEADERS": "Authorization=Bearer cotel_<your-actor-token>"
  }
}
```

Replace `cotel_<your-actor-token>` with the actor token you copied from the dashboard. Restart Claude Code to apply the change.

### Multiple machines or agents

Create a separate actor for each sender — one per developer or machine. Each instance gets its own `settings.json` entry with its own `OTEL_EXPORTER_OTLP_HEADERS` value. Spans appear with the corresponding actor label in the dashboard. When the senders cannot have headers of their own — every agent run by Paperclip gets the same ones — use an [ingest key](#ingest-keys-one-key-for-many-senders) instead.

### Remote ingest (Cloudflare Tunnel)

When using a public ingest URL, the endpoint changes but the header format is identical:

```json
"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "https://ingest.yourdomain.com/v1/traces",
"OTEL_EXPORTER_OTLP_HEADERS": "Authorization=Bearer cotel_<your-actor-token>"
```

See the [Cloudflare Tunnel guide](./cloudflare-tunnel-remote.md) for end-to-end setup.

## Ingest keys (one key for many senders)

An **ingest key** is a shared credential for OTLP ingest. It answers *may this request write spans?* and nothing else; which actor the spans belong to comes from the sender's actor token, sent in a second header, `X-Cotel-Actor-Token` ([ADR-0028](../decisions/0028-ingest-keys-and-actor-tokens.md)).

Use one when many senders must share one set of headers. Paperclip is the case it was built for: before every agent run it removes all `OTEL_*` and `CLAUDE_CODE_*TELEMETRY*` variables from the agent's env and applies one company-wide OTLP destination with one set of headers, so a per-agent `Authorization` header cannot be set through it. Senders that can each have their own headers need no ingest key; sending the actor token as the bearer keeps working.

| | Actor token | Ingest key |
|---|---|---|
| Format | `cotel_<64 hex>` | `cotel_ik_<64 hex>` |
| Identifies | One actor | Nobody |
| Authorizes | OTLP ingest | OTLP ingest only (`/v1/traces`, `/v1/logs`) — never export/import or the dashboard |
| Sent as | `Authorization: Bearer …`, or `X-Cotel-Actor-Token: …` next to an ingest key | `Authorization: Bearer …` |
| Stored | Plaintext, retrievable any time | Plaintext, retrievable any time |

### Creating and revoking an ingest key

Open **Setup** → **Settings** tab → **Ingest keys**.

- **Create:** give the key a name that says who uses it (e.g. `paperclip`) and click **Generate key**; the name is required. The new key is shown with a ready-to-paste snippet. The list shows each key's name, the start of the key with a **Copy** button for the full value, and its creation time.
- **Revoke:** deletes the key. Every sender using it gets `401 Unauthorized` on its next request. To replace a key without a gap, create the new one, update the shared headers, then revoke the old one.

There is no last-used time: ingest does not write to the database to record which key a request used.

### How ingest decides

| Request | Result |
|---|---|
| `Authorization: Bearer <ingest key>` + `X-Cotel-Actor-Token: <valid actor token>` | Accepted, attributed to that actor |
| Valid ingest key, actor token missing, empty or unknown | Accepted as **Anonymous** — even with allow-anonymous off, because the key authorized the request |
| Unknown `cotel_ik_` key | `401` |
| `Authorization: Bearer <actor token>` | Accepted, attributed to that actor; `X-Cotel-Actor-Token` is ignored |
| Any other unknown `cotel_` bearer | `401` |
| No bearer (a bearer not starting with `cotel_` counts as none) | [Allow-anonymous](#allow-anonymous-mode) decides; `X-Cotel-Actor-Token` alone never identifies a request |

A wrong actor token is not an error. A typo, a rotated token or a deleted actor sends the spans to **Anonymous** without telling the sender, so check the [Actors page](#the-actors-list) after wiring up a new sender.

`POST /v1/logs` accepts the same credentials and discards the body (`200 {}`). It exists so that a sender exporting logs as well as traces, as Paperclip does, does not get `404`; cotel stores spans only.

### Configuring a sender

Each sender needs three things:

1. **The ingest key** in its OTLP headers: `Authorization=Bearer cotel_ik_<key>` in `OTEL_EXPORTER_OTLP_HEADERS` or `OTEL_EXPORTER_OTLP_TRACES_HEADERS`. With Paperclip this is the company-wide header set; Paperclip puts it in the per-signal header vars.
2. **Its own actor token** in the env var `COTEL_ACTOR_TOKEN`, in that sender's own process env (for a Paperclip agent, its adapter env). Do not put it in the `env` block of a shared `settings.json`: that block applies to every launch and overrides the inherited env, so every sender would claim the same actor.
3. **The headers helper.** Claude Code does not send env vars as headers, so it cannot send `COTEL_ACTOR_TOKEN` by itself. It does run an `otelHeadersHelper` command and sends the headers it prints. `scripts/otel-headers-helper.py` (Python 3.9+, stdlib only) prints the complete header set: `OTEL_EXPORTER_OTLP_HEADERS`, overlaid with `OTEL_EXPORTER_OTLP_TRACES_HEADERS`, plus `X-Cotel-Actor-Token` from `COTEL_ACTOR_TOKEN`. Point the `settings.json` that the sender's Claude Code reads at it:

```json
{
  "otelHeadersHelper": "/path/to/cotel/scripts/otel-headers-helper.py"
}
```

Things to know about the helper:

- Claude Code runs it at startup and again every 29 minutes (`CLAUDE_CODE_OTEL_HEADERS_HELPER_DEBOUNCE_MS`), and only for the `http/protobuf` and `http/json` protocols. It reads the env Claude Code was started with, so a changed `COTEL_ACTOR_TOKEN` takes effect from the next Claude Code run.
- If a helper fails or prints anything but a JSON object, Claude Code's exports fail until it works again. This one always prints one JSON object and exits 0; on an unexpected error it prints what it had built, or `{}`.
- It repeats the headers from the env rather than printing only `X-Cotel-Actor-Token`, because how Claude Code merges a helper's output with the static `OTEL_EXPORTER_OTLP_*HEADERS` vars is not documented. If the output replaces them, a helper that printed only the actor header would drop the ingest key.
- The file must be executable and `python3` must be on the sender's `PATH`. Run it by hand with the sender's env to see what it will send; its tests are `scripts/otel-headers-helper_test.sh`.

### Verifying an ingest key

```sh
# Should return {} (OK): the key is accepted
curl -s -w "\n%{http_code}" \
  -H "Authorization: Bearer cotel_ik_<your-ingest-key>" \
  -H "X-Cotel-Actor-Token: cotel_<the-actor-token>" \
  -H "Content-Type: application/json" \
  -d '{"resourceSpans":[]}' \
  http://localhost:4318/v1/traces

# Should return 401: the key is not one cotel issued
curl -s -w "\n%{http_code}" \
  -H "Authorization: Bearer cotel_ik_0000000000000000000000000000000000000000000000000000000000000000" \
  -H "Content-Type: application/json" \
  -d '{"resourceSpans":[]}' \
  http://localhost:4318/v1/traces
```

An empty `resourceSpans` stores nothing, so the first command proves the key is accepted but does not show attribution; that shows up on the Actors page once the sender's real spans arrive.

## Allow-anonymous mode

The **Allow anonymous OTLP** toggle lives in **Setup** → **Settings** tab.

| State | Behaviour |
|---|---|
| **On** (default) | OTLP requests without an `Authorization` header are accepted; spans are stored with no actor attribution |
| **Off** | Every OTLP request must present `Authorization: Bearer` with an actor token or an ingest key; requests without one receive `401 Unauthorized` |

In both states an unknown `cotel_…` bearer gets `401`, and a request with a valid ingest key is accepted (as Anonymous if it names no known actor).

### When to keep allow-anonymous on

- A single-person local setup where token management adds friction.
- Initial evaluation — start collecting spans immediately, add actors later.
- Migrating from a token-less setup: leave anonymous on until all senders are updated.

### When to disable allow-anonymous

- Multi-actor environments where per-actor cost attribution must be accurate.
- Public ingest endpoints (e.g. exposed via Cloudflare Tunnel) where unauthenticated writes from the internet must be blocked.
- Compliance or audit requirements where all ingest must be attributable.

### Disabling allow-anonymous

1. Create at least one actor (see above) so at least one valid actor token exists.
2. Configure all senders with their actor token (or an ingest key) before flipping the toggle.
3. Open **Setup** → **Settings** tab → disable **Allow anonymous OTLP**.
4. Verify with `curl`:

```sh
# Should return {} (OK)
curl -s -w "\n%{http_code}" \
  -H "Authorization: Bearer cotel_<your-actor-token>" \
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

**Recovery:** copy the correct actor token from the Actors page and update `OTEL_EXPORTER_OTLP_HEADERS` in `settings.json`, then restart Claude Code. A sender using an ingest key gets `401` only when the key itself is wrong or revoked: create a new key and update the shared headers.

## Token security

Actor tokens are 32-byte cryptographically random values encoded as lowercase hex, prefixed with `cotel_`. They are stored as **plaintext** in the database and are always accessible from the dashboard — you can retrieve a token any time without needing to save it at creation.

> **Note:** An older `api_tokens` table (pre-FLO-88) stored hashed tokens for dashboard API auth. That table is kept in the schema for upgrade safety but is no longer used for authentication.

### Protecting the database

Because actor tokens are stored in plaintext, anyone with filesystem access to the DuckDB file (`/data/cotel.duckdb` inside the container) can read all actor tokens. Protect with:

- Standard Docker volume permissions (owned by the container user, not world-readable on the host).
- Cloudflare Access or equivalent to restrict dashboard access to trusted operators.
- Rotate any actor token that may have been exposed.

Ingest keys are stored the same way, in plaintext, so the same protections apply to them.

## References

- [ADR-0004 — Multi-User Separation](../decisions/0004-multi-user-separation.md)
- [ADR-0027 — Users Become Actors](../decisions/0027-users-become-actors.md)
- [ADR-0028 — Ingest Keys Authorize, Actor Tokens Identify](../decisions/0028-ingest-keys-and-actor-tokens.md)
- [ADR-0006 — Cloudflare Tunnel + Token Auth](../decisions/0006-cloudflare-tunnel-and-token-auth.md)
- [Cloudflare Tunnel — Token Mode](./cloudflare-tunnel-remote.md)
- [Cloudflare Tunnel — Local Config](./cloudflare-tunnel-local.md)

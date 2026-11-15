# ADR 0028 — Ingest keys authorize, actor tokens identify

**Date:** 2026-11-15
**Status:** Accepted
**Deciders:** Flop (board)

---

## Context

Since ADR-0008 every sender proves two things with one header:
`Authorization: Bearer <actor token>` both lets the request in and names the
actor its spans belong to. Each Paperclip agent carried its own token in its own
adapter env, set by `scripts/agent-set-cotel-token.sh`.

Paperclip now manages Claude Code's telemetry env itself. Before every agent run
it removes every `OTEL_*` and `CLAUDE_CODE_*TELEMETRY*` variable from the
agent's env and applies **one** company-wide OTLP destination with **one** set
of headers (its "Agent observability" setting): http/protobuf, traces to
`{base}/v1/traces`, logs to `{base}/v1/logs`, the headers in
`OTEL_EXPORTER_OTLP_TRACES_HEADERS` and `OTEL_EXPORTER_OTLP_LOGS_HEADERS` with
percent-encoded values, and the resource attributes `paperclip.agent.id` and
`paperclip.company.id`. With that setting empty it sets
`CLAUDE_CODE_ENABLE_TELEMETRY=0`.

That is why agent telemetry stopped on 2026-11-07: the per-agent headers were
stripped and telemetry was switched off. Configuring the setting brings
telemetry back, but only with headers that are identical for every agent. A
per-agent `Authorization` header cannot be expressed through Paperclip, so with
one header doing both jobs the choice is between no agent telemetry and every
agent merged into one actor.

## Decision

Split authorization from identification.

- An **ingest key** is a new shared credential, `cotel_ik_<64 hex>`. It is
  created and revoked in the dashboard (**Setup → Settings → Ingest keys**) and
  travels as `Authorization: Bearer <ingest key>`. Many senders can share one.
- The **actor token** is the existing per-actor `cotel_<64 hex>` token,
  unchanged and not reissued. Alongside an ingest key it travels in
  `X-Cotel-Actor-Token`. Agents keep it in the env var `COTEL_ACTOR_TOKEN`,
  which Paperclip's stripping does not touch.

### How ingest decides

| Request | Result |
|---|---|
| `Authorization: Bearer <ingest key>` + `X-Cotel-Actor-Token: <valid actor token>` | Accepted, attributed to that actor |
| Valid ingest key, actor token missing, empty or unknown | Accepted as **Anonymous**, even with allow-anonymous off |
| Invalid `cotel_ik_` key | `401` |
| `Authorization: Bearer <actor token>` (legacy) | Accepted, attributed to that actor; `X-Cotel-Actor-Token` is ignored |
| Any other unknown `cotel_` bearer | `401` |
| No bearer (a bearer not starting with `cotel_` counts as none) | Allow-anonymous decides, as before; `X-Cotel-Actor-Token` alone never identifies |

A request with a valid ingest key and no usable actor token is stored as
Anonymous even when allow-anonymous is off. The toggle exists to keep out
requests nobody authorized; this one was authorized, and refusing it would drop
real telemetry over a missing label. The actor token alone is never enough:
it is an identity, not a credential, so `X-Cotel-Actor-Token` without a bearer
falls through to allow-anonymous like any request without one.

### Storage and scope

- Schema v12 adds `ingest_keys (id, name, token_hash UNIQUE, prefix,
  created_at)`. The key is stored **hashed**; only a 12-character display prefix
  (e.g. `cotel_ik_ab1`) is kept in clear. The full value is shown once, when the
  key is created, and cannot be retrieved afterwards. Revoking a key deletes its
  row.
- An ingest key authorizes **OTLP ingest only** (`/v1/traces`, `/v1/logs`). It
  never opens export/import or the dashboard API.
- Management is in the dashboard API: `GET`, `POST /api/v1/ingest-keys` and
  `DELETE /api/v1/ingest-keys/{id}`.
- `POST /v1/logs` exists only so Paperclip's log exporter does not get `404`. It
  accepts the body and discards it (`200 {}`). cotel stores spans, not logs.

### Getting the actor header out of Claude Code

Claude Code does not turn arbitrary env vars into headers. It does run an
`otelHeadersHelper` from `settings.json`: a command that prints a JSON object of
HTTP headers, run at startup and every 29 minutes
(`CLAUDE_CODE_OTEL_HEADERS_HELPER_DEBOUNCE_MS`), for http/protobuf and
http/json only. If the helper fails or prints invalid JSON, exports fail until it
works again.

`scripts/otel-headers-helper.py` is that helper. It rebuilds the headers from
`OTEL_EXPORTER_OTLP_HEADERS` overlaid with `OTEL_EXPORTER_OTLP_TRACES_HEADERS`,
adds `X-Cotel-Actor-Token` from `COTEL_ACTOR_TOKEN`, and always prints one JSON
object and exits 0. It emits the **complete** header set, not just the actor
header, because how Claude Code merges the helper's output with the static
`OTEL_EXPORTER_OTLP_*HEADERS` vars is not documented: if the helper's output
replaced them, a helper that printed only `X-Cotel-Actor-Token` would silently
drop the ingest key and every export would get `401`.

## Alternatives considered

- **Patch our Paperclip fork to stop stripping the env.** Rejected: it fights
  upstream behaviour on purpose, and the patch would have to survive the weekly
  rebase indefinitely.
- **Map the `paperclip.agent.id` resource attribute to actors.** Viable, but
  Paperclip-only and a second identity scheme to keep in sync with the actors
  table. It may be added later as a fallback for requests without an actor
  token.
- **One shared actor for all agents.** Works today with no code change, and
  loses per-agent attribution, which is what ADR-0008 exists for.

## Consequences

- **The ingest key is readable by agent code.** It sits in the agent's env, so
  anything the agent runs can read it. That is why its scope is ingest-only: the
  worst a leaked key does is write spans.
- **Revoking a key cuts every sender using it** at once. Rotating a shared key
  means creating the new one, updating the shared configuration, then revoking
  the old one.
- **Unknown actor tokens land in Anonymous silently.** A typo in
  `COTEL_ACTOR_TOKEN`, a rotated token or a deleted actor gives no error to the
  sender; its spans appear under Anonymous. Check the Actors page after wiring a
  new sender.
- **No last-used timestamp.** Ingest does not write to the database per request
  to record key use, so there is no way to see which keys are still in use other
  than revoking one and watching what stops.
- Senders using a bearer actor token directly keep working unchanged.

# ADR 0029 — Paperclip agents are identified by `paperclip.agent.id`

**Date:** 2026-11-15
**Status:** Accepted
**Deciders:** Flop (board)
**Amends:** [ADR-0028](./0028-ingest-keys-and-actor-tokens.md) for Paperclip agents

---

## Context

ADR-0028 split authorization from identity: a shared ingest key authorizes, and
each sender's actor token, sent as `X-Cotel-Actor-Token`, identifies it. For
Paperclip agents the token was meant to travel as the env var
`COTEL_ACTOR_TOKEN`, turned into a header by Claude Code's `otelHeadersHelper`.
On production that did not work, for reasons on Paperclip's and Claude Code's
side that cotel cannot change:

- **Paperclip runs agents through the Claude Agent SDK with
  `--setting-sources=project,local`**, so `~/.claude/settings.json` - where the
  helper was registered - is never read by an agent. The agent's process did
  carry the right `COTEL_ACTOR_TOKEN`; nothing sent it.
- **Registering the helper in managed settings breaks export.** When
  `otelHeadersHelper` comes from managed settings, Claude Code drops
  `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` from every lower-trust source ("lower-trust
  scopes cannot redirect the traces signal"), which is where Paperclip puts the
  endpoint. Traces from the whole host stopped until the file was removed.
- **Paperclip header values are literal.** The company destination's headers are
  one secret for every agent; `$COTEL_ACTOR_TOKEN` is sent as that literal text.
- **Agents cannot set their own env.** Since Paperclip #15283 an agent key gets
  `403` on any env change to a `*_local` agent, its own included; only the board
  can.
- The feature covers Claude Code and Codex only; per-host Claude Code settings
  would never have reached Codex or the other harnesses anyway.

What Paperclip does provide is identity: it sets `OTEL_RESOURCE_ATTRIBUTES` for
every agent run, so each span carries `paperclip.agent.id` (the agent's UUID).

## Decision

**cotel maps Paperclip agent ids to actors.** Each actor has an optional
`paperclip_agent_id` (schema v13, set on the actor page or with
`PATCH /api/v1/actors/{id}`). On ingest:

1. An actor token - `Authorization: Bearer <actor token>`, or
   `X-Cotel-Actor-Token` next to an ingest key - names the actor, as before.
2. Otherwise, **if and only if an ingest key authorized the request**, the span
   is attributed to the actor whose `paperclip_agent_id` equals the span's
   `paperclip.agent.id` - read from the resource attributes, falling back to the
   span's own attributes.
3. Otherwise the span is Anonymous.

The attribute is honoured only behind an ingest key: an unauthenticated sender
(with `allow_anonymous` on) could otherwise claim any agent's identity by
setting an attribute. An agent maps to at most one actor; assigning an id that
another actor holds moves it. There is no `UNIQUE` constraint on the column
(DuckDB has raised false duplicate-key errors on updates of such rows); storage
keeps one actor per id in a transaction. Lookups are memoized per request, so a
batch from one agent costs one query.

ADR-0028 is unchanged for everything else: ingest keys, actor tokens, the header
and the helper still serve people and machines whose Claude Code reads user
settings (a developer's laptop, a host's interactive shell).

## Consequences

- Paperclip's company observability needs only the ingest key in
  `Authorization`. No helper, managed settings or per-agent env is needed on the
  Paperclip host, and remote agents (another machine) are covered the same way.
- Each agent's id is entered once per actor. A new Paperclip agent stays
  Anonymous until its actor is mapped - visible on the Actors page.
- The `COTEL_ACTOR_TOKEN` already written into the agents' Paperclip env is
  harmless and unused.
- Spans ingested before an agent was mapped stay Anonymous; the mapping is not
  applied retroactively.

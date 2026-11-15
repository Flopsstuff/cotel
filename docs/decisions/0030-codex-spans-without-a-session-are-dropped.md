# ADR 0030 — Codex spans without a session are dropped at ingest

**Date:** 2026-11-15
**Status:** Accepted
**Deciders:** Flop (board)

---

## Context

Codex (`service.name` `codex-app-server`) exports every span its Rust tracing
layer opens, not a curated set like Claude Code's. On 2026-11-15 two short
Paperclip ping runs of one Codex agent sent 31,373 spans, 78% of that day's
traffic. All of production then held about 102,000 raw spans, covering 30 days.

| Spans | Count | What they are |
|---|---|---|
| `receiving`, `handle_responses` | 18,946 | A pair per streamed response event, about 225 events per model request |
| `append_items`, `persist_rollout_items` | 4,788 | Writes to Codex's own rollout file |
| `fs.get_metadata`, `fs.read_file` | 2,677 | Filesystem calls |
| `realtime_conversation.running_state` | 1,294 | A state poll |
| tool routing, MCP, plugins, `auth`, config | the rest | Set-up repeated before each step |

Only 207 of them (0.7%) feed anything cotel shows. These are the turn span
(`session_task.turn`, the model and token totals) and the tool-call spans. They
are also exactly the spans that carry the conversation id, which the Codex
adapter reads as the session. Every page that shows a span (Sessions, Tools,
Costs, Models, the session timeline) shows only spans with a session. The
other 99.3% were stored and never shown. They stayed for the 30-day raw
retention and went into every daily snapshot. Volume grows with the number of
model requests, about 750 spans each, so one real task would add more spans
than all other senders send in a month.

Codex itself cannot be told to send less. Its binary has no span filter by
name or level. `OTEL_TRACES_SAMPLER` drops whole traces, turn spans included,
and Paperclip strips `OTEL_*` from agents' environment anyway (ADR-0028).

## Decision

**cotel drops at ingest every Codex span that has no session** after the Codex
adapter has run. The adapter takes the session from `conversation.id`, or from
`thread.id` when that is a UUID string. Dropped spans are not stored. The
request still succeeds, because the drop is a choice and not an error. The
ingest log reports how many spans were dropped, at most once a minute.

The rule keys on the session, not on a list of span names. Codex adds internal
spans in new versions, and a list of names would let each new one through
until someone noticed. A span that the dashboard could show always has a
session, so the rule cannot drop one of those.

Only Codex is filtered. Claude Code and Grok CLI spans are all stored. Grok
sends about one fiftieth of Codex's volume.

`cotel --purge-dropped` (dry run) and `--purge-dropped-apply` apply the same
rule to spans already stored, using the ingest code itself (`ingest.WouldDrop`).
Only spans without a session are candidates.

## Consequences

- Codex volume falls by about 99%. Sessions, tokens, cost and tool calls are
  unchanged.
- An error status on a dropped span is lost. Such errors never affected a
  session's status, because that status reads only spans in the session, so
  nothing visible changes.
- The raw export has fewer Codex spans than were sent. Anyone who needs the
  full Codex trace must send it to a tracing backend, not to cotel.
- A Codex version that stops sending the conversation id would have every span
  dropped. The ingest log would show it as a jump in dropped spans, and Codex
  sessions would stop appearing.
- `DELETE` does not shrink a DuckDB file. Space freed by the purge is reused
  for new spans. The file itself gets smaller only when it is rebuilt from a
  snapshot ([Database snapshots](../operations/duckdb-snapshots.md)).

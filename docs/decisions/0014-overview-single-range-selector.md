# ADR 0014 — Overview: one range selector every panel obeys

**Date:** 2026-08-20
**Status:** Accepted
**Deciders:** Daedalus (CTO)

---

## Context

The Overview page is the dashboard's front door: five KPIs and a stack of
sections that each summarise one resource and link to its full page. Today those
panels do not agree on what "now" means.

| Panel | Endpoint | Window it actually shows |
|---|---|---|
| KPIs | `GET /overview` | last 30 days, hardcoded server-side |
| Sessions | `GET /sessions` | all time — no time filter exists |
| History | `GET /history` | last 30 days, from/to computed in the page |
| Costs | `GET /costs` | last 30 days, from/to defaulted server-side |
| Tools | `GET /tools` | `range=all`, passed by the page |
| Models | `GET /models` | all time — no time filter exists |

Three different windows on one screen, and only the KPI labels say which — as a
literal `(30d)` baked into the string. A reader comparing the Sessions count KPI
against the Models table below it is comparing 30 days against all time, with
nothing on the page to say so.

`range` already exists as a contract. ADR-0011 introduced it for the Users list
and ADR-0012 extended it to Tools: five rolling-window keys
(`all|year|month|week|day`), `month` the default, unrecognised values falling
back rather than 400ing, and the window answered from a **union of raw `spans`
and rolled-up `daily_usage`** split at the earliest surviving raw day. That union
is not decoration. The retention worker (ADR-0009) rolls spans older than
`RawDays` (default 30) into `daily_usage` and deletes them, so any endpoint that
queries `spans` alone answers `year` and `all` with the same number it answers
`month` — a confident wrong total, which ADR-0011 rejected as worse than having
no switcher at all.

The board asked for a single range selector in the Overview header that every
figure on the page obeys. That request is unsatisfiable without deciding how far
the existing `range` contract reaches.

## Options considered

1. **Translate in the frontend only.** `/costs` and `/history` already take
   `from`/`to`; the page could derive them from the selected range and leave the
   API alone. Rejected on two counts. It cannot scope `/overview`, `/sessions`
   or `/models` at all — they have no time parameter to translate into. And it
   would answer long ranges from `spans` alone, reintroducing the exact defect
   ADR-0011 exists to prevent.

2. **One fat `GET /overview?range=…` returning every panel.** One request, one
   window, trivially consistent within the page. Rejected: it is a second
   contract for numbers the per-resource endpoints already own, and each section
   links to a full page served by those endpoints. Two independent
   implementations of "cost in the last 30 days" will diverge, and the place it
   shows up is a summary that disagrees with the page it links to.

3. **Extend the existing `range` contract to the remaining read endpoints
   (chosen).** One parameter, one meaning, one union, everywhere the dashboard
   reads. The Overview then holds no time logic of its own — it picks a key and
   passes it down.

## Decision

`GET /api/v1/overview`, `/sessions`, `/costs` and `/models` accept `range` with
the same five keys, the same rolling-window semantics, and the same
fallback-don't-400 rule as `/users` and `/tools`. Each response echoes the
`range` it actually used.

**Defaults preserve today's behaviour rather than converging on one value.**
`/overview` and `/costs` default to `month`, which is the 30-day window they
already apply. `/sessions` and `/models` default to `all`, because they have no
time filter today and a `month` default would silently truncate every existing
caller. A page that wants a window asks for one; no caller's meaning changes
under it.

**Explicit bounds beat the range key.** `/costs` keeps `from`/`to`. When a
request carries both, `from`/`to` wins and `range` is ignored — the narrower,
more specific statement is the one the caller meant.

**Long ranges are answered from the union, not from `spans`.** Every metric that
recomposes from additive parts — cost, token totals, span and session counts,
distinct users, per-model and per-tool totals — sums across `spans` ∪
`daily_usage` at the raw-floor split defined in ADR-0011.

**A panel that cannot honour the range says so.** The Sessions *list* is the one
that cannot: `daily_usage` aggregates a session's day, cost and counts, but not
the start time, model and status a session row shows, so rows for rolled-up days
cannot be reconstructed. It stays raw-only, clamps the range to raw coverage,
and returns `covered_since` (RFC3339, `null` when the range is fully covered) so
the UI states the shortfall in one line. This is the rule ADR-0012 set for the
Bash breakdown: the constraint is displayed, not absorbed. The *session count*
KPI is unaffected — `daily_usage` carries `session_id`, so counting distinct
sessions across the union is exact.

The selected range persists in its own cookie, `cotel_overview_range`, per the
per-page rule in `useRangeCookie`: changing the range on Overview must not move
the Users or Tools page under a reader who switches tabs.

## Consequences

- The Overview stops being five windows stacked vertically. Every number on it
  answers the same question, and the KPI labels carry the range suffix
  (`RANGE_SUFFIX`) instead of a hardcoded `(30d)`.
- `/sessions` and `/models` gain a parameter and change no existing behaviour.
  Callers that pass no `range` see exactly what they see today.
- `/sessions` grows `covered_since`, so a client can tell "no sessions in this
  window" apart from "the window reaches past raw retention". It self-retires
  the same way `duration_stats_since` does not: raw coverage is a standing
  property of retention, so this field is permanent, not transitional.
- Six of the dashboard's read endpoints now share one time contract. The next
  one is a parameter, not a design.
- `?user_id=` scoping on Overview is orthogonal and stays. It composes with
  `range` on every endpoint above.
- The union costs a second scan over `daily_usage` on every Overview load. The
  table is one row per (day, session, model, tool) and capped at
  `AggregateDays` (90); the Users list already pays this on its default range.

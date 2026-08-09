# ADR 0009 — `daily_usage` roll-up normalises empty/NULL keys to an `unknown` sentinel

**Date:** 2026-08-09
**Status:** Accepted
**Deciders:** Daedalus (CTO), Wayland (implementer)

---

## Context

The retention worker rolls raw `spans` into `daily_usage`, a daily aggregate
table keyed by:

```
PRIMARY KEY (day, session_id, model, tool_name)
```

In DuckDB, primary-key columns are implicitly `NOT NULL`. But in `spans` those
same columns are nullable, and are frequently absent:

- `model` — non-model spans (e.g. a `Bash` tool call) have no model; some
  historical / imported rows have `model = NULL`.
- `tool_name` — non-tool spans (API requests, session spans) have no tool.
- `session_id` — anonymous or malformed telemetry can arrive without one.

The ingest path stores missing attributes as the empty string `''`, while
imported / older data can contain genuine SQL `NULL`. Both are "no value".

The roll-up query previously selected these columns straight through:

```sql
SELECT …, session_id, model, tool_name, … FROM spans … GROUP BY …
```

When any grouped key was `NULL`, the whole `INSERT … SELECT` aborted with:

```
Constraint Error: NOT NULL constraint failed: daily_usage.model
```

The retention worker caught the error, logged it at an ordinary level, and moved
on — so the roll-up (and everything downstream: aggregates, purge) silently
stopped working. Observed in production on robmini, 2026-08-09 (FLO-553).

## Options considered

1. **Normalise at ingest** — write a sentinel into raw spans when the attribute
   is missing. Rejected: it rewrites the meaning of raw spans (a `Bash` span
   genuinely has no model), pollutes the dashboard's raw-span views, and would
   force the cost-recalculation work (FLO-552) to special-case the sentinel in
   raw data.
2. **Make the PK columns nullable** — drop them from the PK or allow NULL.
   Rejected: it is a schema/identity change to a table that already round-trips
   through export/import, and `NULL` grouping keys are awkward (`NULL != NULL`),
   which would fragment aggregates.
3. **Normalise at the aggregation boundary (chosen)** — coalesce empty/NULL keys
   to a visible sentinel in the roll-up query only.

## Decision

Normalise at the aggregation boundary. The roll-up maps both `NULL` and `''` to
the sentinel string **`unknown`** for the three PK columns:

```sql
COALESCE(NULLIF(session_id, ''), 'unknown') AS session_id,
COALESCE(NULLIF(model, ''), 'unknown')      AS model,
COALESCE(NULLIF(tool_name, ''), 'unknown')  AS tool_name,
…
GROUP BY 1, 2, 3, 4   -- group on the normalised expressions
```

`GROUP BY` references the SELECT-list positions so `''` and `NULL` collapse into
a single `unknown` bucket instead of two. The sentinel is the exported constant
`storage.UnknownSentinel`.

Raw `spans` are left untouched: they keep their original `''` / `NULL`. The
sentinel exists only in `daily_usage`, which is read solely by export,
user-deletion, and the roll-up itself — no dashboard/API query reads it — so
there is no user-visible change to the live dashboard.

### Shared representation with FLO-552

FLO-552 (cost recalculation) and this change agree on one definition of "a span
without a model": a raw span where `model IS NULL OR model = ''`. FLO-552 reports
such spans as **uncovered** (never priced as zero); this roll-up buckets them as
`unknown` so their volume stays countable. The two are consistent because both
key off the raw-span predicate, not off each other's output.

### Observability

The worker's per-cycle outcome is persisted (settings keys
`retention_last_status` / `retention_last_error` / `retention_last_run_at`) and
surfaced on `GET /api/v1/health` as a `retention` object; a failed cycle flips
top-level health to `degraded` and is logged at `ERROR` level. A silent
roll-up failure like FLO-553 is now observable.

## Consequences

- The roll-up no longer aborts on empty/NULL key columns; all three NOT NULL PK
  columns are covered, not just `model`.
- `daily_usage` may now contain rows with `session_id`, `model`, or `tool_name`
  equal to `'unknown'`. Consumers that previously saw `''` for absent
  `tool_name` (non-tool spans) now see `'unknown'`. Export/import carries the
  sentinel verbatim.
- No schema change: the table definition and PK are unchanged.

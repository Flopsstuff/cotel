# ADR 0012 — Tools list: time-ranged stats, server-side sort and pagination

**Date:** 2026-08-09
**Status:** Accepted
**Deciders:** Daedalus (CTO)

---

## Context

The Tools page repeats the Users page's shape, and therefore its defects
(ADR-0011). It is three blocks: a "Top 10 Tools by Call Count" bar chart, an
"All Tools" table, and a "Bash Command Breakdown" table.

1. **The chart duplicates the table.** It plots `calls` — a column the table
   below it already has — for ten rows out of however many exist, with no time
   scoping. It is a picture of one column of the next block.
2. **There is no time range.** `GET /api/v1/tools` and `/api/v1/bash-commands`
   aggregate over all of `spans`, unconditionally. "Which tools do I lean on"
   is a question about a period, and the page cannot express one.
3. **All-time silently means "since the retention floor".** The retention
   worker (ADR-0009) deletes raw spans older than `RawDays` (default 30). The
   Tools numbers are labelled as totals but are a rolling 30-day window that
   nobody chose and nothing states.
4. **Sorting is client-side.** Correct today only because the page hands
   `DataTable` the complete list. The moment pagination is added on top of that
   — the board's request — sorting reorders the visible page and the page
   becomes wrong in exactly the way ADR-0011 describes.

Unlike the Users case, the aggregate table is a partial answer here.
`daily_usage` carries `tool_name` in its primary key, so `span_count` and
`total_cost_usd` survive roll-up per tool. `duration_ms` and `status_code` do
not: no column holds them, so average duration and error rate are unrecoverable
for any day the roll-up has already consumed.

## Options considered

### Long ranges for duration / error rate

1. **Offer only ranges raw spans cover (day/week/month).** Rejected: a Tools
   page whose range switcher stops where the Users page's continues is an
   inconsistency users read as a bug, and it concedes the question permanently.
2. **Report duration and error rate over the raw-covered slice and say
   nothing.** Rejected — this is defect 3 again with a different label.
3. **Add the two missing sums to the roll-up (chosen).** `total_duration_ms`
   and `fail_count` are additive over days, so a weighted average and a rate
   recompose exactly from them. Two nullable columns, following the v7 → v8
   cache-token migration precedent already in `schema.sql`.

### Bash command breakdown over long ranges

Rejected outright: giving `daily_usage` a command dimension. Its primary key is
`(day, session_id, model, tool_name)` — bounded vocabularies. A command string
is unbounded, so a `command` PK column makes the aggregate table grow with
distinct commands per day, in an embedded single-file DuckDB. That is a storage
one-way door bought for one panel.

## Decision

### Drop the "Top 10 Tools by Call Count" chart

The page becomes the tool list plus the bash breakdown.

### `GET /api/v1/tools` gains the ADR-0011 query contract

| Param   | Values | Default | Meaning |
|---------|--------|---------|---------|
| `range` | `all` \| `year` \| `month` \| `week` \| `day` | `month` | Window for every metric |
| `q`     | string | — | Case-insensitive substring match on tool name |
| `sort`  | `name` \| `calls` \| `avg_duration_ms` \| `fail_count` \| `fail_rate` | `calls` | Sort key |
| `order` | `asc` \| `desc` | `desc` | Sort direction |
| `page`  | integer ≥ 1 | `1` | 1-based page index |
| `limit` | 0…500 | `0` | Rows per page; `0` means unpaginated |

Same semantics as the users list, deliberately: rolling windows anchored at
request time, unknown values falling back to the default rather than 400
(*trust the boundary*), `limit=0` meaning unpaginated so pagination ships off
and turning it on later is a UI parameter rather than an API migration. The
existing `user_id` filter is unchanged. The response keeps its `items` array
with an unchanged item shape and adds the echo fields `total`, `page`, `limit`,
`range`, `sort`, `order` (*schema and public interfaces are forever*).

`/api/v1/bash-commands` gains the same parameters except `range`, which it
accepts and clamps — see below.

### Schema version 9 — `daily_usage` gains `total_duration_ms`, `fail_count`

```sql
ALTER TABLE daily_usage ADD COLUMN IF NOT EXISTS total_duration_ms DOUBLE;
ALTER TABLE daily_usage ADD COLUMN IF NOT EXISTS fail_count        BIGINT;
```

Additive, nullable, no default, behind the version guard (ADR-0010). Rows
rolled up before this migration stay NULL — honestly unknown, not a fabricated
zero, exactly as v7 → v8 handled the cache-token columns. The roll-up populates
them with `SUM(duration_ms)` and `COUNT(*) FILTER (WHERE status_code = 2)`, and
accumulates them on conflict like every other sum.

### Union rule, and what NULL does to it

The raw/aggregate split is ADR-0011's, unchanged:

```
raw_floor := (SELECT MIN(start_time) FROM spans)

raw part:  FROM spans        WHERE tool_name IS NOT NULL AND tool_name <> ''
                               AND (since IS NULL OR start_time >= since)
agg part:  FROM daily_usage  WHERE tool_name <> 'unknown'
                               AND day < CAST(raw_floor AS DATE)
                               AND (since IS NULL OR day >= CAST(since AS DATE))
```

The aggregate part must exclude the `unknown` sentinel. The roll-up maps a NULL
or empty `tool_name` to `'unknown'` (ADR-0009) while the raw query filters those
spans out entirely; without the exclusion, a phantom tool named "unknown"
appears in the list only for ranges old enough to be rolled up.

Metrics recompose from additive parts across the union:

```
calls            = SUM(span_count)
avg_duration_ms  = SUM(total_duration_ms) / SUM(span_count)   -- over covered rows only
fail_rate        = 100.0 * SUM(fail_count) / SUM(span_count)  -- over covered rows only
```

Aggregate rows predating v9 contribute to `calls` and cost but are excluded from
the duration and failure denominators — an average over the part of the range
that has the data, never a zero standing in for a missing sum. The response
carries `duration_stats_since` (RFC3339, or null when the whole range is
covered) so the UI can state the shortfall in one line instead of the number
quietly meaning something else. The field self-retires: `AggregateDays` is 90,
so no pre-v9 row survives more than 90 days past this migration and the value
becomes permanently null.

### Bash breakdown stays raw-only and says so

It is computed from `spans` alone. It accepts `range` and clamps it to raw
coverage, and the block states the window it actually covers whenever the
selected range reaches further back than raw spans go. Same rule as above: the
constraint is displayed, not absorbed.

## Consequences

- One schema migration (v9), additive and nullable, so downgrade-compatible:
  an older binary ignores two columns it does not know about.
- `GET /api/v1/models` has the identical shape and the identical defect, and the
  v9 columns make the same fix available to it. Out of scope here; worth doing
  next so the three list pages stop diverging.
- The `avg_duration_ms` / `fail_rate` sort keys sort over a metric that may
  cover less than the requested range during the ≤90-day transition. Sorting by
  a partially-covered metric is still a consistent total order over the result
  set; it is the value's meaning, not the ordering, that the note qualifies.
- Pagination remains off by default across the dashboard. The list pages share
  one contract, so the eventual switch is one decision, not three.

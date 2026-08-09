# ADR 0011 — Users list: time-ranged stats, server-side sort and pagination

**Date:** 2026-08-09
**Status:** Accepted
**Deciders:** Daedalus (CTO)

---

## Context

The Users page was two blocks: a "Cost by user (top 10)" bar chart and an
"All users" table. Four problems had accumulated:

1. **The chart duplicated the table.** It showed one number — all-time cost —
   that the table already had a column for, could not be scoped to a time range,
   and truncated at ten users.
2. **Stats were all-time only.** "Which users spent what *this month*" — the
   question the page exists to answer — could not be asked.
3. **Sorting was per-page.** The shared `DataTable` sorts the array it is
   handed. The Users page slices to a page *before* handing rows over, so
   sorting reordered the visible page only. With 25 rows/page and 200 users,
   "sort by cost desc" showed the most expensive user *of page 1*. Any
   pagination scheme built on client-side sorting is wrong by construction.
4. **Tokens were listed in the table.** A per-user secret rendered in every row
   of a wide table, next to a destructive delete button, on a page whose primary
   job is comparison.

There is also a data-availability constraint that any time range longer than a
month runs into. The retention worker (ADR-0009, `internal/storage/retention.go`)
rolls raw spans older than `RawDays` (default 30) into `daily_usage` and deletes
them; `daily_usage` rows are kept `AggregateDays` (default 90). So a `year` or
`all` range **cannot** be answered from `spans` alone — the raw rows are gone.

The roll-up cutoff is snapped back to UTC midnight, so a UTC calendar day is
either fully rolled up or not rolled up at all — never split across the two
tables. That property is what makes a clean union possible.

## Options considered

### Range-scoped stats

1. **Query `spans` only.** Simplest. Rejected: once retention has run, `year`
   and `all` silently return the same number as `month`. A range switcher whose
   options are indistinguishable is worse than no switcher — it reports a
   confident wrong total.
2. **Query `daily_usage` only.** Rejected in the other direction: recent
   activity is not rolled up yet, so the last 30 days — the default range —
   would be empty.
3. **Union both, split on a day boundary (chosen).** Raw spans answer for every
   day they still cover; `daily_usage` answers for strictly earlier days.

### Sorting and pagination

1. **Sort client-side over the full list, paginate client-side.** Correct today
   (tens of users), and the change is small. Rejected: it makes the API contract
   the thing that has to change later, exactly when the dataset is too big to
   fetch whole — a migration under pressure.
2. **Sort and paginate in SQL (chosen).** The order is defined over the whole
   result set, so page 2 continues page 1. Pagination stays *off* by default
   (`limit=0`), so today's UI is unchanged in behaviour and the switch to paged
   mode later is a UI parameter, not an API change.

## Decision

### `GET /api/v1/users` gains query parameters

| Param   | Values | Default | Meaning |
|---------|--------|---------|---------|
| `range` | `all` \| `year` \| `month` \| `week` \| `day` | `month` | Window for `cost` and `sessions` |
| `q`     | string | — | Case-insensitive substring match on user name |
| `sort`  | `name` \| `created_at` \| `last_seen` \| `cost` \| `sessions` | `cost` | Sort key |
| `order` | `asc` \| `desc` | `desc` | Sort direction |
| `page`  | integer ≥ 1 | `1` | 1-based page index |
| `limit` | 0…500 | `0` | Rows per page; `0` means unpaginated |

Unknown or malformed values fall back to the default rather than erroring —
this is a dashboard query string, validated at the edge and then trusted
(*trust the boundary*).

Ranges are rolling windows anchored at request time: `day` = 24 h, `week` = 7 d,
`month` = 30 d, `year` = 365 d, `all` = no lower bound. Rolling, not calendar:
the switcher answers "how much lately", and a calendar month resets to a
near-empty page on the 1st.

**Only `cost` and `sessions` are range-scoped.** `created_at` and `last_seen`
stay all-time — "last seen" filtered by a range would just restate the range.

The response keeps its existing `users` array with an unchanged item shape, and
adds the echo fields `total`, `page`, `limit`, `range`, `sort`, `order`.
Existing consumers that read `users` keep working (*schema and public interfaces
are forever*).

### The synthetic anonymous row is produced in SQL

Unattributed spans (`user_id IS NULL`) surface as the pseudo-user
`__anonymous__`. It was appended in Go after the query returned; it is now
produced inside the statement so it sorts, filters, and paginates with everyone
else. A row that jumps to the bottom of every sort is a bug users report as
"sorting is broken".

### Union rule for range stats

```
raw_floor := (SELECT MIN(start_time) FROM spans)     -- NULL when no raw spans

raw part:  FROM spans        WHERE since IS NULL OR start_time >= since
agg part:  FROM daily_usage  WHERE day < CAST(raw_floor AS DATE)
                               AND (since IS NULL OR day >= CAST(since AS DATE))
```

The two parts are disjoint and leave no gap. The roll-up consumes whole UTC days
and deletes the spans it aggregates, so a day that still has any raw span was
never rolled up: raw covers `[raw_floor_day, today]`, aggregates cover everything
strictly earlier. When `raw_floor` is `NULL` (no raw spans at all) the aggregate
part is unbounded above.

`<` rather than `<=` also picks the safer side of the one case that breaks the
invariant — a backfill or import writing spans dated to an already-rolled-up day.
That day is then briefly counted from raw only (low) instead of from both (double)
until the next roll-up folds the new spans into the existing aggregate.

Cost is `SUM` over the union; sessions is `COUNT(DISTINCT session_id)` over the
union, so a session straddling the boundary counts once.

### New `GET /api/v1/users/{id}`

Returns a single user, including the token. The list may now be a page, so "find
the user in the list response" is no longer a valid way for a detail view to
resolve one. `__anonymous__` is accepted and returns the synthetic row.

### Token and destructive actions move off the list

The list shows name, cost, sessions, created, last seen — comparison columns
only. The token, its copy button, rotate, and delete live on the user's own page
at `/users/{id}`, reached by clicking a row. One row click, one destination;
no per-row action cluster to mis-click.

## Consequences

- The default cost figure on the Users page changes meaning from all-time to
  last 30 days. This is the point of the change, but it is a visible number
  moving: the range control is rendered next to the columns it governs, and the
  selected range is persisted in a cookie (`cotel_users_range`) so the page does
  not silently reset to a different window between visits.
- Range accuracy is bounded by retention: with defaults, `year` and `all` can
  only see back `AggregateDays` (90). Operators who want a longer history must
  raise `AggregateDays`; the number is honest about what is stored, not padded.
- `daily_usage` becomes a dashboard read path for the first time. Its
  `unknown` sentinel (ADR-0009) means rolled-up spans that had no `session_id`
  collapse into a single bucket, so a session count over the aggregate tail can
  be low by the number of distinct session-less spans. Raw-window counts are
  unaffected.
- `daily_usage` correctness is now a dashboard concern, not just an export one.
  The whole-day cutoff and accumulating `ON CONFLICT` that the roll-up gained
  are what this union relies on; changing either breaks the users list quietly,
  in a table nobody looks at until a total looks wrong.
- `DataTable` gains an optional controlled-sort mode. Pages that do not pass
  `sort`/`onSortChange` keep sorting client-side exactly as before.

# JSON API reference

The dashboard is a client of the same public JSON API you can call yourself.
Everything below is mounted under `/api/v1/` on the dashboard port (`:8080` by
default) and returns `application/json`.

## The list-query contract

`GET /users`, `GET /tools` and `GET /bash-commands` share one set of query
parameters. They behave identically across the three endpoints, so learning it
once is enough ([ADR-0011](../decisions/0011-users-list-ranged-stats-and-server-side-sort.md),
[ADR-0012](../decisions/0012-tools-list-ranged-stats-and-server-side-sort.md)).

| Param | Values | Default | Meaning |
|---|---|---|---|
| `range` | `all` \| `year` \| `month` \| `week` \| `day` | `month` | Rolling window every metric is scoped to |
| `q` | string | — | Case-insensitive substring match on the row's name |
| `sort` | see per-endpoint table | see below | Sort key |
| `order` | `asc` \| `desc` | `desc` | Sort direction |
| `page` | integer ≥ 1 | `1` | 1-based page index |
| `limit` | 0…500 | `0` | Rows per page; `0` means unpaginated |

Three rules hold everywhere:

- **Ranges roll from request time**, they are not calendar-aligned. `week` means
  the last 7 × 24 hours, not "this week".
- **Unrecognised values fall back to the default** rather than returning `400`.
  `?range=decade&sort=nonsense` is a valid request that answers with the
  defaults, so a stale bookmark never becomes an error page.
- **Sorting and paging happen in SQL**, over the whole matching set. Page 2 is
  the second page of one global ordering, never a re-sort of a slice. `total`
  counts matching rows *before* paging, so it is stable across pages.

Every response echoes `total`, `page`, `limit`, `range`, `sort` and `order` back,
so a client can render its controls from the response alone.

## `range` on the summary endpoints

`GET /overview`, `GET /sessions`, `GET /costs`, `GET /models` and `GET /history`
accept the same `range` parameter, with the same five keys, the same
rolling-window semantics and the same fall-back-don't-`400` rule
([ADR-0014](../decisions/0014-overview-single-range-selector.md)). Each echoes
back the `range` it used. All five also accept `user_id`, which composes with
`range` rather than overriding it.

**Defaults preserve each endpoint's previous behaviour rather than converging on
one value:**

| Endpoint | `range` default | Why |
|---|---|---|
| `GET /overview` | `month` | The 30-day window it always applied |
| `GET /costs` | `month` | The 30-day window `from`/`to` already defaulted to |
| `GET /history` | `month` | Same — the window a bare request already answered |
| `GET /sessions` | `all` | Had no time filter; a `month` default would truncate existing callers |
| `GET /models` | `all` | Same |

Every additive figure — cost, token totals, span and session counts, distinct
users, per-model and per-tool totals — is answered from the union of raw `spans`
and the `daily_usage` roll-up, split at the earliest surviving raw day. `year`
and `all` therefore keep answering after retention has deleted the raw spans,
instead of silently repeating the `month` figure.

Two consequences are worth knowing:

- **`GET /costs` and `GET /history`: explicit bounds beat the range key.** When a
  request carries `from` and/or `to`, those win and `range` is ignored — the
  narrower, more specific statement is the one the caller meant. The response
  then echoes `"range": null`. On `/costs`, `top_sessions[].first_seen` degrades
  to the aggregate's day at midnight UTC for rolled-up sessions, since
  `daily_usage` keeps no intra-day timestamp; it is a lower bound on the real
  start, never a later one.
- **`GET /sessions` clamps and says so.** A session row needs a start time, model
  and status, none of which the roll-up carries, so the list is computed from raw
  spans alone. A range reaching past the raw floor is clamped, and the response
  reports `covered_since`: the RFC3339 instant the list actually starts from, or
  `null` when the selected range is fully covered. The *session count* on
  `/overview` is unaffected — `daily_usage` carries `session_id`, so counting
  distinct sessions across the union is exact.

`GET /overview`'s `users_count` counts the distinct principals active in the
range, with all unattributed spans counting as the single `__anonymous__`
principal the users list shows.

## `GET /history`

Activity over time, bucketed at the requested `granularity`.

| Param | Values | Default | Meaning |
|---|---|---|---|
| `granularity` | `10m` \| `hour` \| `4h` \| `day` \| `week` \| `month` | `day` | Bucket width for `buckets` and `by_model`; an unrecognised value falls back to `day` |
| `range` | `all` \| `year` \| `month` \| `week` \| `day` | `month` | Rolling window, as above |
| `from`, `to` | `YYYY-MM-DD` | — | Explicit bounds; when either is present they win and `range` echoes `null` |
| `user_id` | user id \| `__anonymous__` | — | Scopes every figure to one principal |

```json
{
  "granularity": "day",
  "from": "2026-07-21",
  "to": null,
  "range": "month",
  "buckets": [
    { "bucket": "2026-08-19", "sessions": 12, "spans": 940, "cost_usd": 8.21, "input_tokens": 1201, "output_tokens": 4402 }
  ],
  "by_model": [
    { "bucket": "2026-08-19", "model": "claude-opus-5", "cost_usd": 7.90, "spans": 612 }
  ],
  "heatmap": [
    { "date": "2026-08-19", "hour": 14, "count": 61, "cost_usd": 0.94 }
  ],
  "covered_since": null,
  "heatmap_covered_since": null
}
```

`from` and `to` echo the resolved bounds, and are `null` for a side the window
does not bound — `range=all` reports `"from": null`, and any range-scoped request
reports `"to": null` because the window runs to request time.

`bucket` is a UTC wall-clock label: `YYYY-MM-DD` at `day` and coarser,
`YYYY-MM-DD HH:MM` at the sub-day widths, floored to the width (`4h` to
`00:00`/`04:00`/…, `10m` to `:00`/`:10`/…). It is UTC on any host —
`CAST(start_time AS TIMESTAMP)` renders the stored `TIMESTAMPTZ` in UTC whatever
the server's timezone is — so a client can reconstruct a bucket label for an
instant without asking what the server thinks midnight is.

**Which parts span the roll-up.** At `day`, `week` and `month` granularity,
`buckets` and `by_model` are answered from the `spans` ∪ `daily_usage` union at
the raw-floor split, so `year` and `all` keep charting after retention has
deleted the raw spans. Two fields report where that stops:

- **`covered_since`** clamps `buckets` and `by_model`. It is always `null` at
  `day`, `week` and `month`. At the sub-day widths — **`10m`, `hour`, `4h`** — it
  names the raw floor when the window reaches past it: `daily_usage` buckets
  whole UTC days and cannot produce a sub-day bucket, so those series are
  raw-only rather than day-shaped data under a sub-day label.
- **`heatmap_covered_since`** clamps `heatmap`, which resolves hour of day at
  every granularity and is therefore always raw-only.

Both are RFC3339, and `null` when the selected window is fully covered by raw
spans.

## `GET /tools`

One row per tool, with call volume, average duration and error rate.

| Sort key | Orders by |
|---|---|
| `name` | Tool name |
| `calls` (default) | Call count |
| `avg_duration_ms` | Mean duration per call |
| `fail_count` | Number of failed calls |
| `fail_rate` | Percentage of calls that failed |

Also accepts `user_id` to scope every figure to one user; the value
`__anonymous__` selects spans ingested without a user.

```json
{
  "items": [
    { "name": "Bash", "calls": 942, "avg_duration_ms": 903.4, "fail_count": 11, "fail_rate": 1.17 }
  ],
  "total": 5,
  "page": 1,
  "limit": 0,
  "range": "month",
  "sort": "calls",
  "order": "desc",
  "duration_stats_since": null
}
```

**`duration_stats_since`** is the one field that needs explaining. Tool figures
come from raw spans for recent days and from the `daily_usage` roll-up for older
ones. Rows rolled up before schema v9 carry no duration or failure sums — they
predate the columns. Those rows still count toward `calls`, but they are left out
of the duration and error denominators rather than contributing a zero that would
silently drag the average down.

When that applies, `duration_stats_since` is the RFC3339 instant from which
`avg_duration_ms` and `fail_rate` are actually backed by data. It is `null` when
the whole selected range is covered. Aggregates are purged after 90 days, so the
field permanently becomes `null` within 90 days of the v9 upgrade.

## `GET /bash-commands`

Per-command breakdown of `Bash` tool calls. The command text is read from the
`command` span attribute, falling back to `command` inside a JSON-encoded
`tool_input`.

**Claude Code does not send either attribute.** Its tool spans carry `tool_name`,
`tool_use_id` and `duration_ms` only — which tool ran, not what it ran — so
against Claude Code telemetry this endpoint returns an empty `items` list and the
dashboard's Bash commands section stays empty. The endpoint is kept for OTLP
producers that do send a `command` attribute; cotel's ingest is generic.

| Sort key | Orders by |
|---|---|
| `command` | Command text |
| `calls` (default) | Call count |
| `avg_duration_ms` | Mean duration per call |
| `fail_count` | Number of failed calls |
| `fail_rate` | Percentage of calls that failed |

`q` matches on the command text. `user_id` works as above.

This endpoint is computed from raw spans alone. A command string is unbounded, so
the roll-up deliberately carries no command dimension — giving it one would grow
the aggregate table with every distinct command per day. It therefore accepts
`range` but **clamps it to the raw-span window**, and reports the result in
`covered_since`: the RFC3339 instant this breakdown actually starts from, or
`null` when the selected range is fully covered by raw spans.

```json
{
  "items": [
    { "command": "git status", "calls": 84, "avg_duration_ms": 120.5, "fail_count": 0, "fail_rate": 0 }
  ],
  "total": 12,
  "page": 1,
  "limit": 0,
  "range": "month",
  "sort": "calls",
  "order": "desc",
  "covered_since": "2026-07-11T00:00:00Z"
}
```

## `GET /users`

One row per user, plus a synthetic `__anonymous__` row when unattributed spans
exist. `cost` and `sessions` are range-scoped; `created_at` and `last_seen` are
always all-time.

| Sort key | Orders by |
|---|---|
| `name` | Display name |
| `cost` (default) | Spend in the selected range |
| `sessions` | Distinct sessions in the selected range |
| `created_at` | When the user was created |
| `last_seen` | Most recent span |

Its rows are returned under `users` rather than `items`. `GET /users/{id}`
returns one user in the same shape and accepts `range`. See
[Users and Authentication](./users-and-auth.md) for the management endpoints
(create, rotate, delete).

## References

- [ADR-0011 — Users list: ranged stats and server-side sort](../decisions/0011-users-list-ranged-stats-and-server-side-sort.md)
- [ADR-0012 — Tools list: ranged stats and server-side sort](../decisions/0012-tools-list-ranged-stats-and-server-side-sort.md)
- [ADR-0014 — Overview: one range selector every panel obeys](../decisions/0014-overview-single-range-selector.md)
- [ADR-0009 — daily_usage unknown sentinel](../decisions/0009-daily-usage-unknown-sentinel.md)

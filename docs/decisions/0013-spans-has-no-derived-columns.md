# ADR 0013 — `spans` carries no derived columns: drop `duration_ms`

**Date:** 2026-08-14
**Status:** Accepted
**Deciders:** Daedalus (CTO); diagnosis by Wayland

---

## Context

`spans.duration_ms` is declared as a VIRTUAL generated column in the middle of
the table:

```sql
start_time      TIMESTAMPTZ NOT NULL,
end_time        TIMESTAMPTZ NOT NULL,
duration_ms     DOUBLE GENERATED ALWAYS AS (
                    epoch_ms(end_time) - epoch_ms(start_time)
                ),
service_name    VARCHAR,
...
```

A virtual column takes a logical slot but no storage slot, so every column
declared after it has logical index = physical index + 1. A column is then
answered wrongly by a bare `WHERE col = <constant>` exactly when its logical
index collides with the *physical* index of an indexed column: the scan probes
that unrelated ART index for the constant, finds nothing, and returns zero rows.
Not an error — an empty answer.

Today `service_name` (logical 7) collides with `session_id` (physical 7) and
`tool_name` (logical 10) with `user_id` (physical 10). That is how
`/api/v1/bash-commands` shipped permanently empty behind a `WHERE tool_name =
'Bash'` that reads correctly to any reviewer.

The trap is our table shape, not an engine version. It reproduces on DuckDB
1.1.3, 1.4.1 and 1.5.5, and 1.4+ additionally pushes `COALESCE` into the scan,
which invalidates the `COALESCE(col, '') = ?` workaround the code currently
relies on. The collision set also moves silently whenever anyone adds, reorders
or indexes a column in `spans`.

The guard test landed alongside this analysis
(`internal/storage/pushdown_test.go`) derives the affected set from the live
schema and fails in both directions, so a moved collision turns into a red
build. It detects the trap; it does not remove it.

## Options considered

**A. Do nothing; keep the guard test and the `COALESCE` workaround.**
Survivable only while nobody changes `spans`' columns or indexes and we never
upgrade the engine — and 1.4+ already pushes `COALESCE` down, so the workaround
is on borrowed time. Rejected: every future query against a VARCHAR column is a
correctness landmine that reads fine in review.

**B. Reorder the generated column to last in `CREATE TABLE`.**
Fixes fresh databases only. DuckDB rejects `ALTER TABLE … ADD COLUMN … GENERATED
ALWAYS AS` (*"Adding generated columns after table creation is not supported
yet"*), so an existing database cannot be reordered in place without a full
table rewrite. `STORED` is rejected outright on every engine tested. Rejected.

**C. Make `duration_ms` a plain stored `DOUBLE` appended last**, backfilled once
and populated by `InsertSpan`. Every query site stays byte-identical. Rejected —
see below.

**D. Drop `duration_ms` from `spans` entirely and compute the duration at the
four query sites that need it.** Chosen.

## Decision

Drop `duration_ms` from `spans`. Compute
`epoch_ms(end_time) - epoch_ms(start_time)` in the queries that need it, aliased
back to `duration_ms` so response shapes and sort keys do not move.

**Why D over C.** Option C is the smaller diff by line count, but it buys that
with a worse migration and a new invariant to maintain:

- **The migration moves no row data.** D drops a column; C needs DROP + ADD plus
  a full-table backfill `UPDATE`. In this project a merge to `main` is a
  production deploy, onto a database whose cold start is already the sensitive
  path, so a migration with no rows to rewrite is worth more than a few saved
  lines. D is not the single statement it first looks like: DuckDB refuses to
  `ALTER` a table an index depends on, so the migration drops the four secondary
  indexes first and lets the existing `CREATE INDEX IF NOT EXISTS` block rebuild
  them. Measured at 138 ms on a 108 MB database of 34 706 spans; the whole v10
  upgrade added 0.4 s to a 9.5 s cold start already dominated by WAL replay.
- **C composes badly with [ADR-0010](./0010-schema-version-guard).** That guard
  re-applies the whole of `schema.sql` on any change to the file, including a
  comment-only one — deliberately, to fail toward doing too much. Neither option
  is a one-time cost under that guard, but they degrade very differently: C's
  DROP + ADD + backfill drops the real column and rewrites the whole table on
  *every future schema edit*, where D re-pays only the index rebuild above.
  Holding C to a single application means a bespoke one-shot guard in Go, i.e.
  new machinery around a column we do not need to store. D's statements are all
  `IF EXISTS` / `IF NOT EXISTS` and match the migration style already in the
  file.
- **A stored column can drift; a computed one cannot.** C makes `duration_ms`
  writable, so it becomes possible for it to disagree with `start_time` /
  `end_time`. D keeps one source of truth.
- **The public interface does not read the column anyway.** The export CSV is a
  versioned interface ([ADR-0005](./0005-export-import-format)) and carries
  `duration_ms` at a fixed position — but `writeSpansCSV` already derives it in
  Go (`s.EndTime.Sub(s.StartTime)`) and the export query never selects the
  column. The precedent for deriving duration outside storage is already in the
  codebase, on the one path where the format is frozen.

The cost of D is four SQL call sites instead of zero: the session spans
projection and the tools and bash-commands aggregates in `internal/api`, and the
roll-up sum in `internal/storage/retention.go`. That is a known, bounded, one-off
edit, against a landmine that is neither.

**The rule this establishes:** `spans` carries no derived columns. Anything
computable from other columns is computed where it is read, or — where it must
be persisted for retention, as in `daily_usage.total_duration_ms` — stored in an
aggregate table, never as a generated column beside the raw data.

## Consequences

- `WHERE col = <constant>` becomes correct on `spans` again. The `COALESCE`
  workaround in `handleBashCommands` and the `pushdownBrokenSpanCols` list in the
  guard test both go away; the guard test itself stays, as the detector for any
  future reintroduction.
- Schema version bumps 9 → 10 under the ADR-0010 guard.
- No backfill, no table rewrite, no row data moved on the deploy that introduces
  it — only the four secondary indexes are dropped and rebuilt. The dropped
  values are recomputed on read from columns that are `NOT NULL`, so nothing is
  lost, and re-adding the column later would be a plain additive migration.
- **No downgrade path.** An older binary still opens a v10 database — its
  `CREATE TABLE IF NOT EXISTS` cannot bring the column back — but every query
  naming `duration_ms` then errors. Rolling the binary back means restoring the
  pre-upgrade database with it.
- The export ZIP/CSV format is unchanged: same columns, same positions, same
  values, `format_version` untouched.
- Reads that previously projected a stored value now evaluate two `epoch_ms`
  calls per row on timestamps the scan has already loaded. Negligible against the
  scan itself, and the aggregate paths (`tools`, `bash-commands`) read the same
  rows either way.
- Anyone adding a convenience column to `spans` must not reach for `GENERATED
  ALWAYS AS`. The guard test enforces the outcome; this ADR records the reason.

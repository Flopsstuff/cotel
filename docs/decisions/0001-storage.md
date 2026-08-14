# ADR-0001: Storage Engine — DuckDB

Date: 2026-05-09  
Status: accepted

---

## Context

cotel is a single-container telemetry sink for Claude Code. It must:

- Accept burst OTLP writes on port 4318 (session ends, tool calls).
- Serve an analytics dashboard (session / model / tool / cost breakdowns) with p95 < 1 s on a year of data.
- Live inside **one container, one named volume** — no sidecar processes.
- Support a credible retention and down-sampling story before data grows painful.

The storage decision is one-way: migrating billions of rows later is expensive. Slow down here.

---

## Options considered

| Option | Deployment | Analytics perf | Down-sampling | Notes |
|---|---|---|---|---|
| **DuckDB** | Embedded in process | Excellent (vectorised columnar) | SQL window functions + scheduled jobs | One writer at a time — acceptable for single-container ingest queue |
| SQLite | Embedded | Poor on aggregations | Manual, painful | Row-oriented; full-table scans on large time ranges |
| ClickHouse local mode | Embedded binary | Excellent | Native TTL + materialised views | ~300 MB overhead; designed for distributed — overkill here |
| Postgres + TimescaleDB | Separate process | Good | Continuous aggregates | Requires two processes → violates one-container constraint |

---

## Decision

**DuckDB**, embedded in the Go ingest process, writing to a single file at `/data/cotel.duckdb` (inside the named volume).

Reasons:

1. **Boring tech wins.** DuckDB is a stable embedded OLAP database with a mature Go driver (`go-duckdb`). A future maintainer can query it directly with the `duckdb` CLI.
2. **One container, one volume.** No network socket, no sidecar. The file lives in the named volume at a single well-known path.
3. **Analytics performance.** Columnar vectorised execution keeps dashboard queries < 1 s on tens of millions of spans.
4. **Down-sampling is SQL.** Retention worker is a scheduled SQL job (no external scheduler needed — ticker in Go). Raw → daily aggregate roll-up is idiomatic DuckDB.
5. **Write concurrency.** Claude Code bursts writes at session end. A single goroutine ingest queue + DuckDB's one-writer model is sufficient; queue depth stays low because writes are cheap.

---

## Consequences

- DuckDB Go driver requires CGo → Docker multi-stage build must include a C toolchain in the builder stage. Final image carries `libduckdb.so` (≈ 50 MB).
- One writer at a time: the ingest handler queues writes through a channel; concurrent dashboard reads use DuckDB's read-only connection pool.
- Schema changes require migrations (versioned `.sql` files). Schema is frozen at the OTLP boundary — column renames break Claude Code integrations.
- Down-sampling job is a Go ticker inside the same process. If the container restarts mid-roll-up, the job re-runs idempotently (window functions on raw data).

---

## Known engine trap: virtual generated columns and secondary indexes

A table that mixes a VIRTUAL generated column with secondary indexes makes
DuckDB answer some constant-equality predicates — `WHERE tool_name = 'Bash'` —
with **zero rows** instead of the matching ones: a silent wrong result, not an
error and not a slowdown.

A virtual generated column takes a logical slot but no storage slot, so every
column declared after it has logical index = physical index + 1. A column is
answered wrongly exactly when its logical index collides with the *physical*
index of an indexed column: the scan concludes an index covers the predicate,
probes that unrelated ART index for the constant, and finds nothing.

`spans` used to declare `duration_ms` this way, which put `service_name`
(logical 7) on `session_id` (physical 7) and `tool_name` (logical 10) on
`user_id` (physical 10) — that is how `/api/v1/bash-commands` shipped
permanently empty. Schema version 10 drops the column and computes the duration
where it is read, so logical == physical everywhere and bare equality is correct
again. ADR-0013 records that decision and the rule it establishes: `spans`
carries no derived columns.

What remains for anyone touching this schema:

- **The affected set is a property of the layout, not of any one column.**
  Adding, reordering, or indexing a column moves the collisions.
  `TestSpansEqualityUnderFilterPushdown` in `internal/storage` derives the set
  from the live schema on every run and fails in both directions, so a
  reintroduced hole is a red build rather than an empty dashboard panel.
- **`COALESCE(col, '') = …` keeps a predicate out of the pushdown**, which is
  the escape hatch if one is ever needed again. It is not durable: DuckDB 1.4+
  pushes `COALESCE` down too, so the same guard test also asserts it still
  returns the truth.
- **DuckDB refuses to `ALTER` a table an index depends on**, so a spans column
  change means dropping the four secondary indexes first and letting the
  `CREATE INDEX IF NOT EXISTS` block recreate them. Measured at 138 ms on a
  108 MB production copy (34 706 spans).

Verified present on DuckDB 1.1.3 (bundled), 1.4.1 and 1.5.5, so upgrading the
engine does not resolve it, and `STORED` is rejected outright. Avoiding
generated columns on indexed tables is the only durable fix.

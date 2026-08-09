# ADR 0010 — Guard schema migrations behind a recorded version

**Date:** 2026-08-09
**Status:** Accepted
**Deciders:** Daedalus (CTO), Wayland (implementer)

---

## Context

`storage.Open` applied the entire embedded `schema.sql` on every process
start:

```go
ddl, _ := schemaFS.ReadFile("schema.sql")
rw.Exec(string(ddl))   // 10 CREATE/INDEX IF NOT EXISTS + ALTER … IF NOT EXISTS + a DISTINCT backfill
```

Every statement is idempotent, but re-running the lot on each boot is wasteful:
the user backfill (`INSERT INTO users SELECT DISTINCT user_id FROM spans`) scans
the whole `spans` table, and the DDL grows with every migration.

The database already records applied migrations in `schema_version` (one row per
version). Nothing read it back — the file was replayed blind.

## Decision

Skip `schema.sql` entirely only when the database records **both** the target
version and the exact hash of the embedded file. Otherwise apply the full,
idempotent file and record both markers.

- The **target** version is parsed from `schema.sql` itself — the highest
  `INSERT INTO schema_version (version) VALUES (N)` — not a hand-maintained Go
  constant. Adding a migration means adding its version row (the existing
  convention).
- The **hash** (`sha256` of `schema.sql`, stored in `settings` under
  `schema_sql_sha256`) is the safety net. Migrations here live in one mutable
  file rather than one file per version, so "edit the file, forget the version
  row" — or write a version row the parser doesn't match — is a live failure
  mode. Version-only, that silently skips the new migration on every existing
  database forever (works locally on a fresh DB, fails only in prod). Any edit
  to the file changes its hash, so the guard falls through to a re-apply
  instead: it fails toward doing too much idempotent work, never toward skipping.
- **Fresh database** (no `schema_version` table) and **pre-marker database**
  (tables exist, marker absent) both fail the version read and fall through to a
  full, idempotent apply — which records the version rows and the hash, so the
  next start takes the fast path.
- A **stale** database (recorded version behind the file, or a mismatched hash)
  also applies the full file, running any new migration exactly once.

The schema stays a one-way door: migrations are additive only and version
numbers are never reused.

## The measurement that reframes the win

The parent work (FLO-556) attributed a ~3-minute production startup to
"WAL replay + schema migration" and this guard targeted the migration half.
Measuring `storage.Open` against a copy of the production database
(108 MB + 6.2 MB WAL, `spans = 39 987`, `schema_version = 8`) shows the
migration was never the cost:

| Path | Duration |
|------|----------|
| Real production start (container log) | **3m1.7s** |
| Raw DuckDB open + WAL replay, no `schema.sql` (Pi copy) | **5m37s** |
| Raw open on the same copy **after** the WAL is folded in | **9ms** |
| Full unguarded `Open` (open + all DDL) on the WAL-free copy | **174ms** |
| Guarded `Open` on the WAL-free copy | **9ms** |

The whole of `schema.sql` costs ~165 ms; the guard removes it. The multi-minute
startup is **DuckDB replaying the write-ahead log on open** — which every path
must do before it can even read the schema version, so the guard cannot touch
it. A WAL-free open with all four ART indexes present is 9 ms, so the cost is
replay itself, not index loading.

The real lever for startup latency is keeping the WAL small at restart
(e.g. `CHECKPOINT` on graceful shutdown; cotel currently traps no signal, so a
deploy's `SIGTERM` kills it with a live WAL to replay). That is out of scope
here and tracked separately.

## Consequences

- An unchanged restart runs two cheap reads (`SELECT max(version)` plus the hash
  lookup) instead of the full DDL (~165 ms → ~9 ms). Correct hygiene; negligible
  against the WAL-replay cost.
- Migrations now run once, on the deploy that introduces them, instead of every
  boot — so a future expensive migration cannot silently tax every restart.
- Any edit to `schema.sql`, including a comment-only one, changes the hash and
  triggers one idempotent re-apply on the next boot. That is the intended
  trade-off: a spurious ~165 ms re-run is cheaper than the failure mode it rules
  out (a real migration silently skipped).
- `schema.sql` remains the single source of truth for the target version; the
  `-- Schema version: N` header is documentation, kept honest by a test that
  asserts it equals the highest inserted version.

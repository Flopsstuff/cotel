# ADR 0023 - Snapshots: `EXPORT DATABASE` to Parquet, on a timer, from inside cotel

**Date:** 2026-10-04
**Status:** Accepted
**Deciders:** Daedalus (CTO)

---

## Context

Production has no backup of the live database. The 2026-10-04 recovery left three
volumes behind, which look like redundancy and are not: two of them hold the same
damaged file (`sha256` `7eb82bbe...` on both) and the third is production itself.
See [What a recovery leaves behind](../operations/duckdb-recovery#what-a-recovery-leaves-behind-and-when-to-delete-it).

The gap has a date on it. The damaged original's newest span is 2026-09-27, and raw
spans are purged at `COTEL_RETENTION_RAW_DAYS` (30), so from roughly **2026-10-27**
re-repairing it recovers nothing retention would have kept anyway. After that there
is no recovery point for production at all.

Two properties of the running system shape every option:

1. **DuckDB has one writer, and the live process holds the file lock.** An external
   process cannot open `/data/cotel.duckdb`, not even read-only, while cotel runs.
   So a snapshot is either taken by cotel itself, or taken while cotel is stopped.
2. **cotel serves everything through a single connection.** `storage.Open` sets
   `rw.SetMaxOpenConns(1)` because DuckDB has one writer, and `DB.ReadOnly()` hands
   the dashboard that same pool so reads always see WAL-buffered writes. Whatever a
   snapshot does on that connection, it does with ingest and the dashboard queued
   behind it. **The decisive number is therefore how long the statement holds the
   connection, not how big its output is.**

---

## Options considered

| Option | Who takes it | Ingest cost | Format | Verdict |
|---|---|---|---|---|
| **1. `EXPORT DATABASE` on a timer inside cotel** | cotel | **0.15-0.3 s per run, measured** | Parquet + `schema.sql`, portable | **Chosen** |
| 2. Graceful stop, copy the volume | an operator, by hand | full stop for the length of a 152 MB copy | native DuckDB file, version-coupled | Rejected |
| 3. External pull over `/api/v1/export` | a sidecar or cron in compose | same connection, strictly more work | ZIP/CSV, **incomplete** | Rejected |

---

## Measurements

Host: **robmini** (Mac mini M1, 8 cores, macOS 26.6) - the machine production runs
on. Subject: a probe copy of the live production database, taken 2026-10-04 with
`docker cp cotel-cotel-1:/data/cotel.duckdb` (plus its `.wal`) into a throwaway
Docker volume: **152,580,096 B**, 65,184 spans, 3,048 `daily_usage` rows, 17 users,
`schema_version` 10.

Every row below was produced by the production image's own binary,
`cotel --db-query "<sql>"`, which opens the file with `access_mode=read_only` (the
engine is DuckDB 1.5.6, the one the production binary links). That the exports ran
at all under a read-only handle is itself the evidence for one acceptance point:
**the snapshot never writes to the live database file.**

| Operation | wall clock, incl. ~0.4 s of container start and file open | output |
|---|---|---|
| baseline, `SELECT 1` | 0.42 / 0.36 / 0.36 s | - |
| `EXPORT DATABASE (FORMAT PARQUET, COMPRESSION ZSTD)` | 0.69 / 0.58 / 0.51 s | **4,399,961 B** (4.2 MiB) |
| `EXPORT DATABASE (FORMAT PARQUET)` (snappy) | 0.55 s | 8,629,574 B |
| `EXPORT DATABASE (FORMAT CSV)` | 0.77 s | 71,012,385 B |
| `IMPORT DATABASE` into a fresh file, DuckDB 1.5.6 CLI | 0.96 / 0.74 s | a 36.2 MB database file |

Subtracting the baseline, the Parquet+ZSTD export statement costs **0.15-0.3 s** and
**4.2 MiB**: 1/35th of the live file, for a full copy of every table. That is the
measurement the recommendation in the ticket hinged on, and it holds - there is no
"blocks ingest for a noticeable time" case to flip the decision to option 3.

The cost is bounded going forward, not just today: raw spans live 30 days by
retention, and production writes ~2.2 k spans/day, so the exported volume is capped
by the retention window rather than growing with the service's age.

### The restore was verified, not assumed

`IMPORT DATABASE` from the snapshot above into an empty volume, then the **cotel
binary** (not the CLI that wrote it) opened the result:

| Check | Source database | Restored from snapshot |
|---|---|---|
| `count(*) FROM spans` | 65,184 | **65,184** |
| `count(*) FROM daily_usage` | 3,048 | **3,048** |
| `count(*) FROM users` | 17 | **17** |
| `max(start_time) FROM spans` | `2026-10-04 17:53:06.637+00` | **identical** |
| `max(version) FROM schema_version` | 10 | **10** |
| `count(*) FROM duckdb_indexes()` | 4 | **4** |
| opens with `cotel --db-query` | yes | **yes** |

Two things worth naming in that table. The four secondary ART indexes are **rebuilt
from the data** by the import rather than copied, which is precisely the failure the
2026-09 incident was: a damaged index structure in a file whose rows were all
readable. A Parquet snapshot cannot carry that damage across; a byte copy of the
volume carries it faithfully. And the restored file is 36.2 MB where the live one is
152.6 MB, so a restore also compacts away the free space a year of retention churn
left behind.

---

## Decision

cotel takes its own snapshots, with `EXPORT DATABASE ... (FORMAT PARQUET, COMPRESSION
ZSTD)`, on a timer, into a second named volume. No new processes, no new ports, no
token: the deploy stays `docker compose up -d`.

**Mechanism.** A `RunSnapshotWorker` goroutine alongside `RunRetentionWorker`: run
once at startup, then every `COTEL_SNAPSHOT_INTERVAL`. It runs on the same single
connection as everything else, which is also why it cannot race the WAL checkpoint:
the two are serialised by construction, not by a lock we have to get right.

**Layout.** One directory per snapshot, named for its UTC instant:

```
/snapshots/2026-10-04T18-00-00Z/
    schema.sql  load.sql
    spans.parquet  daily_usage.parquet  users.parquet
    api_tokens.parquet  settings.parquet  schema_version.parquet
    snapshot.json          <- written last; its presence means "complete"
```

`snapshot.json` carries the instant, the duration, the row count per table and the
`schema_version`, so a restore can be checked against what the snapshot claimed to
hold. It is written after the export returns, and nothing else creates it, so a
directory without it is a failed or half-written run: prunable, never restorable.
This is also why the export writes straight into its final directory instead of a
temporary one that gets renamed - DuckDB bakes **absolute paths** into `load.sql`
(`COPY spans FROM '/snapshots/.../spans.parquet'`), so renaming the directory after
the fact breaks the import.

**Depth over density.** `COTEL_SNAPSHOT_INTERVAL=6h`, `COTEL_SNAPSHOT_KEEP=56`: 14
days of history in 56 snapshots, about 235 MB. The September failure went unnoticed
for six days, so the thing worth buying is reach back past the moment someone
notices, not a tighter recovery point. Disk does not constrain the choice (93 GB
free on the host); the pruning rule does: keep the N newest complete snapshots,
delete the rest, and never delete the last one standing.

**Health.** The worker records its outcome the way the retention worker does
(`settings` keys surfaced on `/api/v1/health`), because a backup that has been
failing quietly for a month is worse than a known absent one.

**Restore.** `cotel --db-import <dir>` opens the target database read-write and runs
`IMPORT DATABASE`, so a restore needs nothing but the image that is already on the
host. Today the alternative is the DuckDB CLI with its version matched by hand,
which is step 4 of the recovery page and the step most able to destroy the file;
`docs/operations/` gets the volume-level procedure built on the flag instead.

---

## Why not the other two

**Option 2, graceful stop plus volume copy.** It is the one option that cannot be
shipped: it needs hands on the host, so there is nothing to merge and nothing that
runs when nobody is watching - which is exactly how production arrived at no backup
at all. It also stops ingest for the length of a 152 MB copy, and the artifact it
produces is a native DuckDB file: version-coupled, and a faithful copy of whatever
corruption the source already carries. The 2026-10-04 recovery produced two such
copies, byte-identical, and neither opens. That is the measured precedent for this
option, not a hypothetical.

**Option 3, external pull over `/api/v1/export`.** Rejected on three counts, the
first of which is fatal on its own:

1. **It is not a backup of the database.** The ZIP carries `spans.csv` and
   `daily_usage.csv` and nothing else ([ADR-0005](./0005-export-import-format)).
   `users`, `api_tokens`, `settings` and `schema_version` are not in it, so an
   instance restored from it rejects every agent's ingest token and has no record of
   its own schema version. `EXPORT DATABASE` writes all six tables; that is verified
   in the file listing above.
2. **It buys no isolation from the writer.** `ExportSpans` runs on the same single
   `rw` connection, and it materialises every span into Go memory before encoding
   CSV into a ZIP, so it holds the connection *longer* than the engine-side export
   while delivering less.
3. **It adds moving parts to the deploy.** An ingest token (board-only to mint), a
   sidecar or cron entry in `docker-compose.yml`, and a `period=day|week|month`
   argument that has to be chosen per run and silently returns 404 for a period with
   no data.

---

## Consequences

- **Three new env vars**, documented in `README.md` and `docs/index.md`:
  `COTEL_SNAPSHOT_DIR` (empty = disabled, so a dev checkout does not start writing
  snapshots into its container filesystem; compose sets `/snapshots`),
  `COTEL_SNAPSHOT_INTERVAL` (`6h`), `COTEL_SNAPSHOT_KEEP` (`56`).
- **A second volume in `docker-compose.yml`**, `cotel-snapshots` mounted at
  `/snapshots`, name overridable the way `COTEL_DATA_VOLUME` already is. It must be
  mounted at the same path on restore, because of the absolute paths in `load.sql`.
- **`docker volume prune` is now dangerous in a new way.** The snapshots volume is
  only "in use" while the container exists; a prune on a stopped deploy takes the
  backups with it. This is a note for `docs/operations/`, not something the code can
  prevent.
- **Every snapshot carries the ingest tokens.** `users.token` is plaintext by
  design, and a snapshot that omitted it would not be restorable to a working
  instance. So the snapshots volume holds the same secrets as the data volume and
  has to be treated the same way, which is a second reason an off-host copy is
  its own decision rather than an obvious next step.
- **Host loss is still uncovered.** Snapshots land on the same disk as production, so
  they insure against file-level corruption, a bad migration and accidental
  deletion - the failures cotel has actually had - and not against the disk or the
  machine going away. An off-host copy is a separate decision with its own
  dependencies (where to put it, what credential it uses); it is deliberately not in
  this one.
- **A snapshot of a silently damaged database is still a good snapshot**, because the
  export reads rows and the import rebuilds indexes. The converse is the trap option
  2 fell into.
- **The snapshot format is a one-way door only in one direction**: Parquet plus a
  plain-text `schema.sql` is readable by any DuckDB build and by anything that reads
  Parquet, so a snapshot outlives the engine version that wrote it. That is the
  property the native-file copy does not have, and the reason this ADR exists rather
  than a cron line in a README.

# Database Snapshots and Restore

cotel exports its whole database to Parquet on a timer and keeps the newest N
exports in a second volume. This page is how you check that it is happening, how
you restore from one, and what the snapshots do *not* protect against.

The decision behind the mechanism, with the measurements it was chosen on, is
[ADR-0023](../decisions/0023-production-database-snapshots). The recovery
procedure for a database that will not open at all is a different page:
[Recovering a DuckDB File That Will Not Open](./duckdb-recovery).

## What the mechanism does

A worker inside cotel runs `EXPORT DATABASE ... (FORMAT PARQUET, COMPRESSION
ZSTD)` once at startup and then every `COTEL_SNAPSHOT_INTERVAL`, writing one
directory per snapshot into `COTEL_SNAPSHOT_DIR`:

```
/snapshots/2026-10-06T12-00-00Z/
    schema.sql  load.sql
    spans.parquet  daily_usage.parquet  users.parquet
    api_tokens.parquet  settings.parquet  schema_version.parquet
    snapshot.json
```

Three properties are worth knowing before you rely on it:

- **`snapshot.json` is written last, and nothing else writes it.** A directory
  without it is a failed or interrupted run: prunable, never restorable. It
  records the instant, the export duration, the schema version and the row count
  per table, read back out of the Parquet files themselves - so a restore can be
  checked against what the snapshot claims to hold rather than trusted.
- **A snapshot is only taken when the newest complete one is older than the
  interval.** A restart - or a crash loop - therefore cannot spend the retained
  window on snapshots minutes apart.
- **The paths inside `load.sql` are absolute.** DuckDB bakes the export
  directory's path into it, so a snapshot cannot be moved or renamed and still
  be imported. It must be visible at the path it was written to - `/snapshots`
  in the shipped compose file.

Pruning keeps the `COTEL_SNAPSHOT_KEEP` newest complete snapshots, deletes
incomplete ones, and only ever runs after a successful export, so the last
snapshot standing can never be pruned away. Directories whose name is not a
snapshot instant are left alone, so the volume is safe to share with anything
else you keep there.

## Is it working?

`/api/v1/health` carries the worker's own report:

```bash
curl -s localhost:8080/api/v1/health | jq '{status, snapshot}'
{
  "status": "ok",
  "snapshot": {
    "status": "ok",
    "last_run_at": "2026-10-06T12:00:04Z",
    "last_dir": "/snapshots/2026-10-06T12-00-00Z"
  }
}
```

`snapshot.status` is `error` after a failed cycle, which also flips the
top-level `status` to `degraded` - the same treatment a failing retention
roll-up gets, and for the same reason: a backup that has been failing quietly
for a month is worse than a known absent one. `unknown` means the worker has not
run yet, or snapshots are disabled (`COTEL_SNAPSHOT_DIR` empty, which is the
default outside the compose file).

To see what is actually on disk:

```bash
docker run --rm -v cotel-snapshots:/snapshots debian:bookworm-slim \
  sh -c 'ls -1 /snapshots && du -sh /snapshots'
docker run --rm -v cotel-snapshots:/snapshots debian:bookworm-slim \
  cat /snapshots/2026-10-06T12-00-00Z/snapshot.json
```

## Who is watching it

Nobody has to remember to run the command above: the scheduled health probe
asks the same endpoint. The LAN half of
[the 10-minute tick](./health-probe) reads `/healthz` first and, when that is
green, asks `/api/v1/health` and classifies the `snapshot` object. A red verdict
pages the same way a dead process does — two consecutive failures, the
`[cotel-health-probe]` marker, an issue whose assignment wakes an agent.

The claim is deliberately **not** on `/healthz`. That endpoint is the container
liveness contract: `cotel --healthcheck`, the Docker `HEALTHCHECK` and
`scripts/wait-for-healthy.sh` all read it, so folding a failed backup into
`ok: false` would mark a working container unhealthy and fail the next deploy.
A missing restore point is an alerting fact, not a liveness fact, so it lives on
the alert path.

### What is red

| `snapshot` in `/api/v1/health` | Verdict | Why |
|---|---|---|
| `status: "error"` | **red** (probe exit 6) | The worker ran and failed. `last_error` is quoted into the alert |
| `status: "ok"`, `last_run_at` older than `SNAPSHOT_STALE_AFTER_SECONDS` | **red** | The worker is not completing cycles any more; the newest restore point is aging out |
| `status: "ok"`, `last_run_at` absent or unparseable | **red** | The claim contradicts itself |
| `status: "unknown"` | silent by default | Means *both* "has not run yet" and "snapshots are disabled", which is the shipped default outside compose — red here would cry wolf on every local instance |
| no `snapshot` field, or `/api/v1/health` not readable | silent by default | An older binary, or a vantage point that cannot see the endpoint |

The last two rows flip to red under `SNAPSHOT_CHECK=require`, which is the
caller asserting "snapshots are expected on this instance". Default is `auto`:
page only on affirmative evidence that the backup is broken. `SNAPSHOT_CHECK=off`
stops the probe asking at all.

### The threshold

`SNAPSHOT_STALE_AFTER_SECONDS` defaults to **43200 s (12 h)** — two
`COTEL_SNAPSHOT_INTERVAL` periods at the shipped `6h`. Two and not one because
a cycle is scheduled *from* the last one, so a single interval leaves no room
for the export itself and a tick landing just before the next run would be red
every time. If you change `COTEL_SNAPSHOT_INTERVAL`, change this with it: the
probe reads the server's answer, not the server's configuration, and cannot
know the interval on its own.

### Checking it by hand

```bash
# the question the probe asks, on the LAN (the dashboard host is behind Access)
curl -s http://robmini.local:8080/api/v1/health | jq '{status, snapshot}'

# the probe's own verdict, classification and all
HEALTHZ_URL=http://robmini.local:8080/healthz scripts/probe-healthz.sh

# the same, demanding that snapshots be configured here
SNAPSHOT_CHECK=require HEALTHZ_URL=http://robmini.local:8080/healthz \
  scripts/probe-healthz.sh

# on the Pi: both halves of the scheduled tick, paging nothing
~/ops/cotel-healthz.sh --probe-only
```

The probe takes one URL — `/healthz` — and derives the `/api/v1/health` address
from it, so there is no second address to keep in step on the host. A green line
names the snapshot it found:

```
probe-healthz: OK — HTTP 200 ingest age 2s (threshold 21600s) url=http://robmini.local:8080/healthz | snapshot last run 2026-10-06T17:39:54Z (age 909s, threshold 43200s) dir=/snapshots/2026-10-06T17-39-54Z
```

## Restoring from a snapshot

`cotel --db-import <dir>` creates the database at `COTEL_DB_PATH` from a
snapshot directory and verifies every table against the snapshot's manifest. It
needs nothing but the image already on the host: no DuckDB CLI, no version
matching by hand (which is the step of the recovery procedure most able to
destroy a file).

Two rules the flag enforces rather than documents: the target database file must
be **empty or absent** (the snapshot's `schema.sql` issues plain `CREATE TABLE`,
so importing over populated tables would fail halfway), and the snapshot must
carry its `snapshot.json`.

**Step 1 - make a probe volume and do the restore there, never into the live
volume.** Creating a probe volume is
[step 3 of the recovery page](./duckdb-recovery#step-3-do-every-experiment-on-a-probe-copy);
for a restore it only has to be empty:

```bash
docker volume create cotel-data-restore-probe
```

**Step 2 - import.** `--entrypoint` is not optional: the image's entrypoint
starts the server and would swallow the flag, leaving a *running cotel* writing
to the volume. The snapshots volume must be mounted at `/snapshots`, the path in
`load.sql`:

```bash
docker run --rm --entrypoint /usr/local/bin/cotel \
  -v cotel-snapshots:/snapshots \
  -v cotel-data-restore-probe:/data \
  ghcr.io/flopsstuff/cotel:latest --db-import /snapshots/2026-10-06T12-00-00Z
# db-import: restored /data/cotel.duckdb from snapshot /snapshots/2026-10-06T12-00-00Z
#   (taken 2026-10-06T12:00:00Z, schema_version 10,
#    api_tokens=3 daily_usage=3048 settings=7 schema_version=10 spans=65184 users=17)
```

A row count that does not match the manifest fails the import with the table and
both numbers named. Nothing is written to the live database or to the snapshot at
any point.

An import that fails partway leaves a half-populated file behind, and the
refusal above means retrying into it fails too. Throw the probe volume away and
make a new one rather than trying to clean it up:

```bash
docker volume rm cotel-data-restore-probe && docker volume create cotel-data-restore-probe
```

**Step 3 - verify with the same binary that will serve it.**

```bash
docker run --rm --entrypoint /usr/local/bin/cotel -v cotel-data-restore-probe:/data \
  ghcr.io/flopsstuff/cotel:latest --db-query "SELECT count(*) FROM spans"
docker run --rm --entrypoint /usr/local/bin/cotel -v cotel-data-restore-probe:/data \
  ghcr.io/flopsstuff/cotel:latest --db-query "SELECT max(start_time) FROM spans"
docker run --rm --entrypoint /usr/local/bin/cotel -v cotel-data-restore-probe:/data \
  ghcr.io/flopsstuff/cotel:latest --db-query "SELECT count(*) FROM duckdb_indexes()"
```

The secondary indexes are **rebuilt from the data** by the import rather than
copied, which is why a snapshot of a database with a damaged index still
restores to a healthy one. Expect the restored file to be considerably smaller
than the live one, too: the import compacts away the free space retention churn
leaves behind.

**Step 4 - promote.** Point the deploy at the restored volume instead of
overwriting the one you are restoring from; Docker has no `volume rename`, and
the volume you would overwrite is also your evidence:

```bash
docker compose down
COTEL_DATA_VOLUME=cotel-data-restore-probe docker compose up -d
```

Make that variable permanent in the deploy's `.env` before you walk away - an
untracked `COTEL_DATA_VOLUME` on the command line is forgotten by the next
`docker compose up -d`, which then silently brings the old volume back.

## A snapshot carries the same secrets as the database

`users.token` holds ingest tokens in plaintext by design
([Users and Authentication](./users-and-auth)), so every snapshot carries a copy
of every agent's token. Treat the snapshots volume exactly like the data volume:
it is not a file to hand around, and an off-host copy of it is an off-host copy
of the credentials.

## What snapshots do not cover

- **Host loss.** Snapshots land on the same disk as production. They insure
  against file-level corruption, a bad migration and accidental deletion - the
  failures cotel has actually had - and not against the disk or the machine
  going away. An off-host copy is a separate decision.
- **`docker volume prune`.** The snapshots volume is only "in use" while the
  container exists, so a prune on a stopped deploy takes the backups with it.
  This is the one new way the volume layout can bite; no code can prevent it.
- **Everything newer than the last snapshot.** With the shipped `6h` interval
  the worst-case loss is six hours of spans.

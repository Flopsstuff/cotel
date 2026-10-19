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

**Nobody is, as of 2026-10-14.** `scripts/probe-healthz.sh` reads `/healthz`
first and, when that is green, asks `/api/v1/health` and classifies the
`snapshot` object — a red verdict pages under the `[cotel-health-probe]`
marker, as an issue whose assignment wakes an agent. That still works, from any
machine on the LAN, but **no schedule runs it**: the 10-minute timer that did
was retired by board order, and the one scheduled watcher cotel has left asks
the public ingest URL only. See
[Production health probes](./health-probe#what-this-watcher-cannot-see). Until
something schedules this probe again, a stopped snapshot worker is found by
running the command above by hand.

That is a standing price, not a pending task:
[ADR-0025](../decisions/0025-best-effort-ingest-watcher-in-actions.md) records why
the only job that could watch this on a schedule is still dispatch-only, and
what has to be granted before it is not.

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

`ghcr.io/flopsstuff/cotel:latest` is the registry's published release and a real
multi-arch index (`linux/amd64` + `linux/arm64`) — see
[ADR-0003](../decisions/0003-release-policy) — so it is safe to `docker run` on
an arm64 host (Pi, Apple Silicon) straight from a `docker pull`; you do not need
`docker compose build` just to get a binary that can import. If pull still
reports `no matching manifest for linux/arm64`, you are on a release older than
that change — build once with `docker compose build`, which tags its result
`cotel-local:latest`, and use that name in the commands below.

Those two names are never the same artifact: `cotel-local:latest` is what this
host built, `ghcr.io/flopsstuff/cotel:latest` is what the registry published,
and the production deploy runs the former. For a restore either will do — the
import only needs a cotel binary for this platform — but when it matters which
one you asked, the name now tells you.

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

**Step 3 - verify with the same binary that will serve it.** Which name that is
depends on the host: `cotel-local:latest` where the image is built locally, as
production does, and `ghcr.io/flopsstuff/cotel:latest` where it is pulled. The
distinction matters at this step and not at step 2 - what this step catches is a
file the *serving* binary cannot open, so asking any other binary answers a
different question.

```bash
docker run --rm --entrypoint /usr/local/bin/cotel -v cotel-data-restore-probe:/data \
  cotel-local:latest --db-query "SELECT count(*) FROM spans"
docker run --rm --entrypoint /usr/local/bin/cotel -v cotel-data-restore-probe:/data \
  cotel-local:latest --db-query "SELECT max(start_time) FROM spans"
docker run --rm --entrypoint /usr/local/bin/cotel -v cotel-data-restore-probe:/data \
  cotel-local:latest --db-query "SELECT count(*) FROM duckdb_indexes()"
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

## The off-host mirror

Snapshots land on the same disk as production, so a second copy lives on
another machine: the Pi pulls the complete snapshots it does not have yet and
keeps them. **Nothing schedules that pull**, so the mirror is exactly as fresh
as the last time someone ran the command below. The decision, the transport it
was chosen on and the risk that is knowingly *not* covered are
[ADR-0024](../decisions/0024-off-host-snapshot-copy-pi-pull).

```bash
scripts/pull-snapshots.sh                        # one tick: pull what is missing, prune
scripts/pull-snapshots.sh --status               # is the mirror current?
scripts/pull-snapshots.sh --status --max-lag 24h # same, as an exit code: 0 fresh, 5 stale
scripts/pull-snapshots.sh --dry-run              # what a tick would do; writes nothing
```

The bytes come out through a throwaway `--rm` container with the volume mounted
`:ro`, because a Docker named volume on macOS has no host-side path for `rsync`
to read - it is inside Docker Desktop's VM. Production is never touched.

### Where it lives, and why there

`SNAPSHOT_MIRROR_DIR`, default `$HOME/cotel-snapshots`, mode 700. Deliberately
**not** under `~/projects`: that checkout is shared, and a copy inside a
repository is a copy that can be committed. Deliberately **not** under
`~/ops/reports/`, which is swept on a 31-day retention rule that has nothing to
do with this data. There is **no** schedule: the systemd timer that would have
run this in the owner's `~/ops` tree was declined, so the pull happens only when
someone runs the command above.

The mirror carries the same secrets as the data volume: `users.token` is
plaintext by design, so every snapshot in it holds a copy of every agent's
ingest token. Treat the directory like the database itself - mode 700, never
inside a repository, not a thing to hand around.

### Reading `--status`

This is what answers "is the mirror current" without trusting that the timer
ran:

```
pull-snapshots: status
  mirror       /home/flop/cotel-snapshots — 25 complete snapshots, 118M on disk, keep 56
  local newest 2026-10-12T17-39-54Z (age 1h37m)
  source       robmini:cotel-snapshots — newest 2026-10-12T17-39-54Z (age 1h37m), 25 complete
  lag          0s — the mirror holds the newest source snapshot
  to pull      0 source snapshot(s) not here yet
```

`lag` is the gap between the newest snapshot here and the newest one at the
source. With no schedule it grows by one snapshot every `6h` until someone runs
a tick, so treat it as the mirror's age rather than as a health signal, and read
it before relying on the copy. Nothing else will tell you: there is no unit, so
no alert.

Two numbers bound what that costs, and they are not the same number:

- **What is lost if robmini dies right now** is everything since the newest
  instant in the mirror - that is `lag`, and it is the time since the last tick.
  Measured on 2026-10-19: **5 d 18 h**, 23 instants, with the source worker
  healthy the whole time.
- **What can never be mirrored** is any instant the source pruned before a tick
  reached it. The source keeps `COTEL_SNAPSHOT_KEEP=56` at `6h`, so a gap of
  **more than 14 days** between ticks leaves a permanent hole in the mirror's
  history - unlike `lag`, running a tick afterwards does not repair it.

### A lag threshold as an exit code

`--max-lag <duration>` makes `--status` answer with a code instead of a number
to interpret: `0` when the mirror is within the threshold, `5` when it is not
(and when the mirror holds no complete snapshot at all). It accepts `90s`,
`30m`, `24h`, `7d` or bare seconds, and it still prints the whole block.

```
$ scripts/pull-snapshots.sh --status --max-lag 24h
...
  lag          6d0h behind the source
  threshold    24h — lag within this is a pass
pull-snapshots: FAILED — the mirror is 6d0h behind the source, over the 24h threshold; run a tick
$ echo $?
5
```

This adds no watcher - there is still nothing on a schedule here. What it adds
is that the check is one command with a verdict, so a person can read it at a
glance and whatever eventually runs on a schedule needs no parsing.

### Exit codes

A tick's non-zero exit reaches only whoever ran it - nothing watches this pull -
so the codes are meant to be read:

| | |
|---|---|
| `0` | Tick completed, including "nothing to do" |
| `1` | Source unreachable - ssh or `docker run` failed |
| `2` | The remote listing was empty, or was not snapshot directories at all |
| `3` | A pulled snapshot failed verification; the partial was deleted and nothing landed |
| `4` | The mirror directory is missing and uncreatable, or not writable |
| `5` | `--status --max-lag` only: the mirror is staler than the threshold |
| `64` | Invalid invocation or configuration - not a tick outcome |

Every pulled directory is verified before it lands: per-file `sha256` against
the same bytes read back out of the source volume, plus `snapshot.json` parsing
and agreeing with the instant it was pulled under. Extraction goes into
`.partial-<instant>` and is renamed to `<instant>` only after that passes, so an
interrupted pull cannot leave a directory a later tick reads as present.

### The mirror does not mirror deletions

Local pruning is `SNAPSHOT_MIRROR_KEEP` newest complete snapshots, never below
one. A local snapshot is **never** deleted because the source stopped having it.
This is the single reason the implementation is not `rsync --delete`:
propagating a source deletion would faithfully reproduce `docker volume prune`
on a stopped deploy - the accident the next section names as the new one - into
the copy that exists to survive it. A test holds the rule, and it must not be
"simplified" later.

### Restoring from the mirror

The mirror's directories *are* the snapshot directories, named after their
instants, so this is [the restore procedure above](#restoring-from-a-snapshot)
with the mirror mounted where the volume would be:

```bash
docker run --rm --entrypoint /usr/local/bin/cotel \
  -v "$HOME/cotel-snapshots:/snapshots:ro" \
  -v cotel-data-restore-probe:/data \
  ghcr.io/flopsstuff/cotel:latest --db-import /snapshots/2026-10-12T17-39-54Z
```

`/snapshots` is not a convention here but a requirement: `load.sql` holds
absolute `/snapshots/<instant>/...` paths, which is also why each mirrored
directory keeps the source's name exactly.

**That command now runs on the Pi too.** `--db-import` needs a cotel binary for
the host's platform, and the published image stopped being amd64-only: `docker
pull ghcr.io/flopsstuff/cotel:latest` on the Pi resolves the `linux/arm64` entry
of the index and the binary inside it executes natively. What has not changed is
the proof the mirror itself offers on every tick - byte-identity, `sha256` per
file plus the manifest - which is what the status line reports; an actual
restore from the mirror on the Pi is now possible but is not something the pull
does for you. Restorability of the artifact itself is still established at the
source, with the drill in
[ADR-0023](../decisions/0023-production-database-snapshots).

## What snapshots do not cover

- **Host loss.** Snapshots land on the same disk as production. They insure
  against file-level corruption, a bad migration and accidental deletion - the
  failures cotel has actually had - and not against the disk or the machine
  going away. The off-host copy is decided -
  [ADR-0024](../decisions/0024-off-host-snapshot-copy-pi-pull) - and the pull
  that implements it is [the off-host mirror](#the-off-host-mirror): a pull to
  the Pi over the existing LAN ssh path, with premises loss accepted as
  uncovered because it is another machine in the same building. That pull is
  **unscheduled** - a mirror is only as current as its last tick, and here the
  last tick was a person, so read `--status` instead of assuming. Measured once,
  on 2026-10-19: **5d18h and 23 instants behind**, while the worker on the
  source was healthy the whole time. A green snapshot verdict says the worker
  runs, not that an off-host copy of its output exists.
- **`docker volume prune`.** The snapshots volume is only "in use" while the
  container exists, so a prune on a stopped deploy takes the backups with it.
  This is the one new way the volume layout can bite; no code can prevent it.
- **Everything newer than the last snapshot.** With the shipped `6h` interval
  the worst-case loss is six hours of spans.

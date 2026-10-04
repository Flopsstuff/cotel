# Recovering a DuckDB File That Will Not Open

cotel keeps everything in a single embedded DuckDB file on the `/data` volume. If that file's internal index structures get damaged, `storage.Open` aborts inside `libduckdb` — a C++ `abort()` that no Go code can catch — and the container restart-loops forever. Ingest stops; nothing else reports it.

This page is the recovery procedure. It was written from an incident in which production refused spans for six days and was then recovered with no data loss.

---

## Symptoms

| Signal | What you see |
|---|---|
| Container state | Restart loop; `docker ps -a` shows **`Exited (134)`** (SIGABRT) or **`Exited (3)`** (checkpoint failed after the database opened) |
| Log | Ends immediately after `opening db /data/cotel.duckdb` — **no** `db ready` line — or shows `db ready` then `startup checkpoint FAILED` and never `ready: serving live traffic` |
| Log (the abort) | `INTERNAL Error: Invalid node type for GetAllocatorIdx: 0`, or another `InternalException` out of `duckdb_open_ext` |
| Volume | A `cotel.duckdb.wal` file is present and never shrinks |
| Volume | `cotel.duckdb.checkpoint-failed` is present (see [Marker file](#the-checkpoint-failure-marker)) |
| Ingest | OTLP clients get `503` with `Retry-After` from the readiness gate, forever |

## Read these three facts before you touch anything

1. **A failed open does not mutate the file.** During the incident the `sha256sum` of `cotel.duckdb` was identical after 336 crash-restart cycles. The restart loop is not making it worse, so there is no reason to rush, and no reason to pull the power.
2. **Never `kill -9` / `docker kill` cotel.** A hard kill is what leaves a WAL behind, and an unreplayable WAL is the half of this failure that turns "slow start" into "never starts". Always stop gracefully (see step 1).
3. **`FORCE CHECKPOINT` succeeding proves nothing.** On the damaged file, `FORCE CHECKPOINT` completed in 0 ms. The defect only surfaces when an index is *mutated* — a rebuild, or the checkpoint that folds index changes. Do not conclude "the file is fine" from a clean checkpoint, a clean `SELECT count(*)`, or a readable dashboard.

---

## Step 1 — Stop cotel gracefully

```bash
docker compose stop cotel        # sends SIGTERM, honours the stop grace period
# or, outside compose:
docker stop -t 30 cotel
```

`docker compose stop` / `docker stop` send SIGTERM; the entrypoint forwards it to cotel, which folds the WAL and exits. If the container is in a restart loop, stop it anyway — this also prevents the loop from restarting under you mid-recovery:

```bash
docker update --restart=no cotel     # remember to restore 'unless-stopped' at the end
docker stop -t 30 cotel
```

## Step 2 — Back up the volume, with hashes on both sides

Make a second named volume. Never a copy inside the same volume: you want the original byte-for-byte untouched.

```bash
STAMP=$(date +%Y%m%d)
VOL=cotel_cotel-data                 # adjust: `docker volume ls` / compose project prefix

docker run --rm -v "$VOL":/data:ro alpine sha256sum /data/cotel.duckdb /data/cotel.duckdb.wal

docker volume create "cotel-data-backup-$STAMP"
docker run --rm -v "$VOL":/src:ro -v "cotel-data-backup-$STAMP":/dst \
  alpine sh -c 'cp -a /src/. /dst/'

docker run --rm -v "cotel-data-backup-$STAMP":/data:ro alpine sha256sum /data/cotel.duckdb /data/cotel.duckdb.wal
```

The two `sha256sum` outputs must match. Record them in the incident ticket — they are what lets you prove later that no repair step touched the original.

## Step 3 — Do every experiment on a probe copy

The original volume is forensics from here on: **read-only mounts only** (`:ro`). All repair attempts run on a throwaway probe copy, which you can recreate from the backup as often as you need.

```bash
docker volume create cotel-data-probe
docker run --rm -v "cotel-data-backup-$STAMP":/src:ro -v cotel-data-probe:/dst \
  alpine sh -c 'rm -rf /dst/* && cp -a /src/. /dst/'
```

## Step 4 — Match the DuckDB CLI to the version linked into cotel

**This is the step that can destroy the file.** DuckDB's storage format is version-coupled; opening the database with a *newer* CLI silently upgrades it, and the cotel binary will then refuse to open it at all. Determine the version, do not assume it.

Ask the binary itself — it reports the version of the `libduckdb` statically linked into it, which is the only answer that matters:

```bash
docker run --rm --entrypoint /usr/local/bin/cotel -v cotel-data-probe:/data \
  ghcr.io/flopsstuff/cotel:latest --db-query "SELECT version()"
# v1.1.3
```

`--entrypoint` is not optional here: the image's entrypoint script starts the server and passes extra arguments through, so without it the flag is swallowed and you get a *running cotel* writing to the volume instead of a one-shot query.

`--db-query` opens the database **read-only**, so it is safe to run against the probe copy. If the file is too damaged to open even read-only, read the pin out of the source tree instead:

```bash
go list -m -f '{{.Dir}}' github.com/marcboeker/go-duckdb   # the version in go.mod
grep DUCKDB_BRANCH "$(go list -m -f '{{.Dir}}' github.com/marcboeker/go-duckdb)/Makefile"
# DUCKDB_BRANCH=v1.1.3
```

Do **not** try to read it out of the bundled static library: `strings deps/linux_arm64/libduckdb.a | grep '^v1\.'` lists `v1.0.0 v1.1.0 v1.1.1 v1.1.2 v1.1.3` — those are storage-compatibility markers, not the build version, and picking the wrong one puts you right back in the trap this step exists to avoid.

Then install exactly that CLI version:

```bash
DUCKDB_VER=v1.1.3
ARCH=aarch64                      # x86_64 hosts: amd64
docker run --rm -it -v cotel-data-probe:/data debian:bookworm-slim sh -c "
  apt-get -qq update && apt-get -qq install -y --no-install-recommends curl unzip ca-certificates &&
  curl -fsSL https://github.com/duckdb/duckdb/releases/download/\$DUCKDB_VER/duckdb_cli-linux-\$ARCH.zip -o /tmp/d.zip &&
  unzip -oq /tmp/d.zip -d /usr/local/bin &&
  duckdb --version && exec bash"
```

Confirm `duckdb --version` prints the same version as `SELECT version()` above before you run a single statement against the file.

## Step 5 — Rebuild the secondary indexes

The four secondary indexes on `spans` are plain ART indexes and carry no data of their own — dropping and recreating them rebuilds the damaged structure from the table. Inside the CLI container from step 4:

```sql
-- on the PROBE copy only
DROP INDEX IF EXISTS idx_spans_session_id;
DROP INDEX IF EXISTS idx_spans_start_time;
DROP INDEX IF EXISTS idx_spans_name;
DROP INDEX IF EXISTS idx_spans_user_id;

CREATE INDEX idx_spans_session_id ON spans(session_id);
CREATE INDEX idx_spans_start_time ON spans(start_time);
CREATE INDEX idx_spans_name       ON spans(name);
CREATE INDEX idx_spans_user_id    ON spans(user_id);

FORCE CHECKPOINT;
```

These are the same index definitions as `internal/storage/schema.sql`; if that file has moved on, take them from there rather than from this page.

`FORCE CHECKPOINT` at the end is what matters: it folds the rebuilt indexes into the main file and is the step that fails loudly if the damage is deeper than the indexes.

## Step 6 — Verify on the probe copy

Still in the CLI container:

```sql
SELECT count(*) FROM spans;                                   -- compare with the pre-incident count
SELECT max(start_time), max(ingested_at) FROM spans;          -- newest data still there
SELECT index_name FROM duckdb_indexes() ORDER BY index_name;  -- all four back
```

Then exit the CLI and confirm the file opens with the cotel binary — the round trip is the real test, because a version-mismatched CLI write would show up here and nowhere earlier:

```bash
docker run --rm --entrypoint /usr/local/bin/cotel -v cotel-data-probe:/data \
  ghcr.io/flopsstuff/cotel:latest --db-query "SELECT count(*) FROM spans"
```

No `cotel.duckdb.wal` should remain in the probe volume after the CLI exits cleanly.

Verify with `--db-query`, not by starting the server: the retention worker runs **once at startup**, so a server start against the probe copy rolls raw spans older than `COTEL_RETENTION_RAW_DAYS` into `daily_usage` and purges them. A span count that drops after you start cotel on a probe copy is retention doing its job, not recovery losing data.

## Step 7 — Promote the repaired copy

Keep the original volume. Point cotel at the repaired data by copying the probe copy into a fresh volume and re-pointing the service, or by restoring into the original volume *only after* the backup from step 2 is verified and recorded.

```bash
docker volume create cotel-data-repaired
docker run --rm -v cotel-data-probe:/src:ro -v cotel-data-repaired:/dst \
  alpine sh -c 'cp -a /src/. /dst/'
# then switch the service's volume to cotel-data-repaired and start it
docker compose up -d cotel
docker update --restart=unless-stopped cotel   # if you disabled it in step 1
```

The recovery is not finished when the service is healthy: it leaves three volumes
behind, one of which is a named trap. See [What a recovery leaves
behind](#what-a-recovery-leaves-behind-and-when-to-delete-it).

### Acceptance

| Check | Expected |
|---|---|
| `docker logs cotel \| grep 'db ready'` | one line, with the open duration |
| `curl -sf localhost:8080/healthz` | `"ok":true` with the expected `"spans":N` |
| `docker exec cotel ls /data` | **no** `cotel.duckdb.wal`, **no** `cotel.duckdb.checkpoint-failed` |
| POST a span, then re-check `/healthz` | `spans` increases and `newest_span_age_seconds` drops to single digits |
| `docker compose stop cotel` → `docker logs --tail 5 cotel` | `checkpoint complete in …; exiting` |
| after that stop: `docker run --rm -v "$VOL":/data:ro alpine ls /data` | no `.wal` file left behind |

The last two are the ones that prove the file is actually healthy, because they are the index-mutating path — not `SELECT count(*)`, and not a `FORCE CHECKPOINT` on a quiet database.

---

## The checkpoint-failure marker

cotel folds the WAL on SIGTERM so the next start does not have to replay it, and again immediately after schema apply so a migration that corrupts an ART index fails the deploy instead of serving traffic. That fold is the earliest honest warning that the database is damaged, so its outcomes are kept apart:

| When | Outcome | Log | Exit code | Marker file |
|---|---|---|---|---|
| Shutdown | succeeded | `checkpoint complete in …; exiting` | 0 | removed if present |
| Shutdown | 8 s deadline | `checkpoint on shutdown timed out …, WAL left for replay on next start` | 0 | not written |
| Shutdown | failed | `checkpoint on shutdown FAILED …, the WAL left behind may not be replayable` | **3** | `<db>.checkpoint-failed` written |
| Startup | succeeded | `startup checkpoint complete in …` | continues | removed if present |
| Startup | 8 s deadline | `startup checkpoint timed out …, continuing` | continues | not written |
| Startup | failed | `startup checkpoint FAILED …` | **3** | `<db>.checkpoint-failed` written |

A deadline is benign: the WAL is whole and the next open replays it (or, at startup, the process continues). A *failed* fold is not — it fails because the database is damaged, and the WAL it leaves can abort the next open inside `libduckdb`. So that case exits non-zero — `docker ps -a` shows `Exited (3)`, and:

```bash
docker inspect --format '{{.State.ExitCode}}' cotel    # 3
```

— and writes a marker next to the database file, which outlives the container's logs. A failed *startup* checkpoint never opens the gates, so the container never reports healthy.

On the next start, cotel logs a warning if the marker is there, immediately before it opens the database — if the open then aborts in C++, that warning is the only line connecting the crash to the checkpoint that caused it. The marker is advisory: cotel still tries to open the database, because refusing to start on a stale marker would turn a healthy file into an outage.

**If you find the marker, start at step 1 of this page.** Clear it only by letting cotel complete a clean fold (a successful start checkpoint, or a clean shutdown), so it never describes anything but the most recent failed checkpoint.

---

## What a recovery leaves behind, and when to delete it

A recovery ends with three volumes where there was one, and nothing removes any of
them on its own. Worse, the live one is now the compose default, so the other two
stop appearing in any deploy path and quietly become unexplained disk. The names
below are from the 2026-10-04 production recovery; the roles generalise.

| Volume | Role | Size | Delete when |
|---|---|---|---|
| `cotel-data-repaired-20261004` | live production, mounted at `/data`, the compose default | 146 MB | never, it is prod |
| `cotel_cotel-data` | the damaged original, untouched: the only artifact that reproduces the `abort()` | 147 MB | upstream [duckdb#25360](https://github.com/duckdb/duckdb/issues/25360) is resolved *and* cotel no longer links a DuckDB that can hit it |
| `cotel-data-backup-20261004` | byte-identical copy of the damaged original, from step 2 | 147 MB | immediately, see below |

Disk is not what forces any of this: the host had 94 GB free. The reason to decide
now is that in six months nobody will remember which of the two leftovers is safe
to remove, and the safe-looking one is the dangerous one.

### The step-2 "backup" is not a fallback

After the repair, verify what the snapshot actually holds:

```bash
for v in cotel_cotel-data "cotel-data-backup-$STAMP"; do
  docker run --rm -v "$v":/data:ro alpine sha256sum /data/cotel.duckdb /data/cotel.duckdb.wal
done
```

On 2026-10-04 both volumes returned the same two hashes (`7eb82bbe…` for the
database, `33e13061…` for the WAL). That is the expected result and it is the point:
the step-2 snapshot is taken *before* the repair, so it preserves the state that
does not open. Restoring it reproduces the outage. In particular it cannot insure
against a defect in the repaired copy that surfaces weeks later, which is the risk
people assume a volume named `…-backup-…` covers.

So it buys exactly one thing the original already provides, and it offers a name
that invites a tired operator to restore a crashing database into production.
Delete it and keep the original, which carries the same bytes under an honest name.
Docker has no `volume rename`, so there is no middle option.

```bash
docker volume rm "cotel-data-backup-$STAMP"
```

Its real job ends the moment step 6 passes: until then it is what lets you rebuild
the probe copy without ever mounting the original. Deleting it after the promotion
in step 7 is the last step of the recovery, not a later cleanup.

### The damaged original has two separate lifetimes

As a **data fallback** it expires on its own. Its newest span was 2026-09-27 and raw
spans are purged at `COTEL_RETENTION_RAW_DAYS` (default 30), so from roughly
2026-10-27 re-repairing it recovers nothing retention would have kept anyway.

As a **bug-repro artifact** it lives as long as the upstream defect. It is the only
file in existence that aborts DuckDB 1.1.3 inside `duckdb_open_ext`, and it cannot
be regenerated: the schema change that damaged the index ran once, against data that
no longer exists. duckdb#25360 is open and had no upstream reply a month after
filing, so a request for a reproduction is still plausible.

Therefore: **hold `cotel_cotel-data` indefinitely**, mounted read-only only, and
re-examine at both of these moments rather than on a calendar:

- the upstream issue changes state, or
- the DuckDB version linked into cotel moves, which is also when the repro becomes
  the natural acceptance test for the bump.

### There is no backup of the live database

Three volumes look like redundancy and are not. Two of them are the same damaged
file and the third is production itself; once the leftovers are gone, the live
database has no snapshot anywhere. That gap predates this incident and is tracked
separately. Do not read the table above as a backup policy.

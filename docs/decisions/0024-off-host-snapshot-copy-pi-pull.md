# ADR 0024 - The off-host copy is a Pi-side pull, and it cannot be an rsync of the volume

**Date:** 2026-10-12
**Status:** Accepted; the schedule was declined, and its absence is an accepted price as
of 2026-10-19 - see **The price of no schedule**
**Deciders:** Prospero (CEO, risk model and mechanism), Daedalus (CTO, transport and implementation)

---

## Context

[ADR-0023](./0023-production-database-snapshots) closes file-level corruption, a bad
migration and accidental deletion, and says so: snapshots land on the same disk as
production, "an off-host copy is a separate decision with its own dependencies (where
to put it, what credential it uses); it is deliberately not in this one."

Those dependencies were the reason it was deferred, and both of them dissolve if the
copy goes to the Pi: no new account, no new secret, no spend. The one question left
was the risk model, and it is not a technical one. The CEO answered it on 2026-10-12:

> **"другая машина, то же здание"** is accepted for cotel snapshots. Off-site is not
> required. Revisit when cotel starts holding data whose loss is materially expensive
> (billing-grade usage, records the company leans on) - at which point it is a board
> matter again, because an external credential comes back with it.

So the Pi is the destination, and this ADR is only about what actually reaches it and
how anyone knows it did.

### What the volume turned out to be

The decision was described as "rsync-pull of the snapshots volume over the existing
ssh path". **That shape is not implementable, and the reason is worth recording rather
than working around quietly.** Snapshots live in the Docker named volume
`cotel-snapshots`, and robmini's `DockerRootDir` is `/var/lib/docker` - a path that
**does not exist on that host**, because it is inside Docker Desktop's Linux VM:

```
$ ssh robmini "docker info -f '{{.DockerRootDir}}'; ls -ld /var/lib/docker"
/var/lib/docker
ls: /var/lib/docker: No such file or directory
```

There is no host-side directory for `rsync` to read, so every option below has to
bring the bytes out through a container.

### Measured, 2026-10-12

| | |
|---|---|
| snapshots on the volume | **25**, 118 MB, one per 6 h |
| one snapshot | 4.8 MB, 9 files |
| at full `COTEL_SNAPSHOT_KEEP=56` | ~270 MB |
| free on the Pi's NVMe | 304 GB |
| robmini host tooling | `rsync`, `tar`, `docker`; macOS 26.6 |
| runtime image contents | `debian:bookworm-slim` + `ca-certificates`: has `tar`, **no** `rsync` |

Capacity is irrelevant at this size, three orders of magnitude over. The constraint is
the transport.

---

## Options considered

| Option | Verdict |
|---|---|
| 1. `rsync` a host path | **Impossible.** No host path exists for a named volume on macOS |
| 2. `rsync --rsync-path=` into a throwaway container | Needs `rsync` inside an image (the runtime image has none) plus remote-shell wiring to make the container the rsync peer. Clever, and the thing a future maintainer debugs at 2am |
| 3. **Per-snapshot `tar` stream over the existing ssh path, driven by a list diff** | **Chosen** |
| 4. Push from robmini | Rejected: a schedule and a credential on the deploy host, and a host that is gone cannot push |

Option 3 is boring because the data makes it boring. A snapshot directory is
**immutable once complete** and says so itself - `snapshot.json` is written last and
nothing else writes it - so the sync rule is a set difference, not a delta algorithm:
pull the directories that are not here yet, skip the ones that are, ignore the ones
with no manifest at the source. There is nothing for an incremental protocol to earn
on a 4.8 MB directory that will never be rewritten.

Option 4 fails the same test ADR-0022 applied to the health probe: the watcher belongs
on the machine that survives the failure being watched. A push from robmini is a backup
that stops exactly when it is needed.

### The transport, exercised before this was decided

```
ssh robmini "docker run --rm -v cotel-snapshots:/snapshots:ro debian:bookworm-slim \
  tar -cf - -C /snapshots <instant>" | tar -xf - -C <mirror>
```

One directory, **1.16 s wall clock**, and all **9 of 9 files sha256-identical** to the
source read back out of the volume. Read-only in two senses: the mount is `:ro`, and
the production container is not touched at all - the stream comes from a throwaway
container of the image cotel's own runtime is built on, which is already on that host.

---

## Decision

**A Pi-side pull on a systemd timer, hourly, using option 3's transport.**

> **Not scheduled (2026-10-13).** The timer was never installed. It would have had to
> live in the owner's own `~/ops` tree rather than in a company repository, that write
> is sanctioned per case, and the request was declined. So nothing on this machine runs
> the pull on its own: the mirror is refreshed only when someone runs
> `scripts/pull-snapshots.sh`, and its freshness is unknown until `--status` is read.
> Everything else below shipped and is live - the transport, the mirror, the
> verification and the pruning rule.
>
> **The absence of a schedule is now the accepted price, not a pending request
> (2026-10-19).** See **The price of no schedule** below for what it costs, measured.

- **The script lives in this repo** (`scripts/pull-snapshots.sh`), and the timer in
  `~/ops` materializes it from `origin/main` at each tick. That is
  [ADR-0022](./0022-health-probe-scheduler-outside-github)'s pattern, unchanged and for
  the same reasons: the code path stays reviewed here and reaches the schedule with no
  sync step.
- **Hourly was the intended cadence**, not every 6 h. A tick that finds nothing new costs one ssh round trip, so
  the cadence is a free variable; matching it to the snapshot interval would put the
  mirror's worst case at nearly two intervals behind for no saving.
- **The mirror lives at `~/cotel-snapshots/`, mode 700** - outside `~/projects`,
  because that checkout is shared by every agent and a copy inside a repo is a copy
  that can be committed, and outside `~/ops/reports/`, which is swept on a 31-day
  retention rule that has nothing to do with this data.
- **The mirror does not mirror deletions.** It prunes on its own count
  (`COTEL_SNAPSHOT_MIRROR_KEEP`, default 56, never below one), and it never deletes a
  local snapshot because the source stopped having it. A sync that propagated source
  deletions would faithfully reproduce the one accident ADR-0023 named as newly
  dangerous - `docker volume prune` on a stopped deploy - into the copy that exists to
  survive it. This is the single reason the implementation is not `rsync --delete`, and
  it must not be "simplified" later.
- **Verification is per-file sha256 against the source, plus the manifest**, checked on
  every pulled directory; a directory that fails either is removed and the tick fails.

---

## The price of no schedule (2026-10-19)

The schedule was asked for twice and declined twice at the gate that owns `~/ops` - the
second time against a full description of the four files it would have added and with a
day of measured lag on the table. That is a decision, not a pending question, so the
cost of paying it belongs in this ADR rather than in a third request.

**What it cost in the first week.** Measured 2026-10-19: the mirror was **5 d 18 h and 23
instants** behind, with the snapshot worker on the source healthy for every one of those
instants. The copy was six days old because six days had passed since a person last ran
the command, which is exactly the mechanism and not an incident.

**Two costs, and they are different numbers.**

| | |
|---|---|
| **Recovery point**, if robmini is lost right now | everything since the mirror's newest instant - i.e. `lag`, the time since the last manual tick. Unbounded in principle; 5 d 18 h observed |
| **History that can never be mirrored** | any instant the source pruned before a tick reached it. At `COTEL_SNAPSHOT_KEEP=56` and `6h` that is **14 days** of reach, so a gap longer than that between ticks leaves a permanent hole - and unlike `lag`, a later tick does not repair it |

The second one is the one worth watching, because it is the only irreversible half. Two
levers move it without touching `~/ops`, and neither is taken here: raising
`COTEL_SNAPSHOT_KEEP` at the source widens the window a manual rhythm has to beat, and
a Paperclip routine on this host could tick the pull at the cost of an agent run per
tick. If the observed rhythm ever exceeds 14 days, take the first one.

**What is not claimed.** None of this is detection. Nothing on this host runs on a
schedule for this mirror, so nothing can notice the lag growing; the only signal remains
`--status`, read by a person. The one thing that improved is that the reading is now a
verdict rather than a duration to interpret: `--status --max-lag <duration>` exits `5`
when the mirror is outside the threshold (and when it holds no snapshot at all), so the
check is a single command for a person and needs no parsing from whatever eventually runs
on a schedule. **A threshold with no runner is not a watcher**, and this does not pretend
otherwise - the green half of the earlier watcher taught exactly that lesson.

**Revisit when:** a measured gap between ticks approaches 14 days, or the data stops
being operational telemetry (the same condition that reopens off-site, under Consequences
below). Either one makes the schedule a question again rather than a settled price.

---

## Consequences

- **Premises loss stays uncovered**, by decision and not by omission, with the revisit
  condition quoted above. The Pi and robmini are in the same building.
- **The Pi holds a second copy of every agent's ingest token.** `users.token` is
  plaintext by design, and a snapshot that omitted it would not restore to a working
  instance, so the mirror carries the same secrets as the data volume and is treated
  the same way: 700, and never inside a repository.
- **The mirror cannot be restored on the Pi today, and the proof available there is
  byte-identity rather than a restore.** `--db-import` needs the cotel binary, and the
  published image is **amd64 only** - `docker manifest inspect` lists one platform, and
  production's own arm64 image was built locally on robmini by compose. So the Pi can
  prove the copy is the copy (sha256 + `snapshot.json` row counts), while the restore
  procedure in
  [Database Snapshots and Restore](../operations/duckdb-snapshots#restoring-from-a-snapshot)
  runs wherever a cotel binary for that platform exists. Restorability of the artifact
  itself is established at the source, with the drill in ADR-0023. Publishing an arm64
  manifest would make the mirror independently verifiable on the Pi; that is a
  packaging decision with its own ticket, not a prerequisite for having the copy.
- **No footprint in `~/ops` after all.** The extra script and timer this company would
  have maintained there was named as the honest cost of the option and accepted by the
  CEO, then declined at the gate that actually owns it: `~/ops` is the owner's
  maintenance tree, not a company repo, and that write is sanctioned per case - the same
  gate `cotel-healthz.timer` passed and this one did not.
- **Nothing is loud, because there is no unit.** The design put a failing pull on the
  `OnFailure=` path the other ops units use, so a broken tick would have announced
  itself; with the timer declined, that path does not exist and the only signal is
  `--status` read by a human. The weaker half of this was already the accepted residual
  [ADR-0022](./0022-health-probe-scheduler-outside-github) accepted for the probe's own
  schedule, and the reason the mirror's newest instant is worth reading in the runbook
  rather than assumed.
- **The source keeps its own watcher.** The 10-minute tick already pages when snapshots
  stop happening on robmini; this ADR adds no claim to `/api/v1/health`, because the
  mirror is not something production can observe.
  **No longer true as of 2026-10-14:** that tick was retired, and the watcher that
  replaced it ([ADR-0025](./0025-best-effort-ingest-watcher-in-actions)) asks the public
  ingest only and never reaches `/api/v1/health`. A snapshot worker that stops is now
  found by running the probe by hand. The decision below stands; only this consequence
  changed.

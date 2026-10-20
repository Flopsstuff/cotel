# ADR 0024 - The off-host copy is a Pi-side pull, and it cannot be an rsync of the volume

**Date:** 2026-10-12
**Status:** **Rescinded by the owner, 2026-10-19.** The mirror is deleted, there is no
off-host copy of cotel's snapshots, and the tooling is retired - see
**Rescinded by the owner**. Everything above that section is the record of what was
built and why; none of it is current posture.
**Deciders:** Prospero (CEO, risk model and mechanism), Daedalus (CTO, transport and
implementation), board owner (rescission)

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

> **This decision is no longer in force (2026-10-19).** The timer was declined on
> 2026-10-13 and never installed, so the pull only ever ran when a person ran it; the
> mirror itself was then rescinded by the owner and deleted. Read the rest of this
> section as what was built, not as what exists. The current posture is in
> **Rescinded by the owner** below.

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
  (`SNAPSHOT_MIRROR_KEEP`, default 56, never below one), and it never deletes a
  local snapshot because the source stopped having it. A sync that propagated source
  deletions would faithfully reproduce the one accident ADR-0023 named as newly
  dangerous - `docker volume prune` on a stopped deploy - into the copy that exists to
  survive it. This is the single reason the implementation is not `rsync --delete`, and
  it must not be "simplified" later.
- **Verification is per-file sha256 against the source, plus the manifest**, checked on
  every pulled directory; a directory that fails either is removed and the tick fails.

---

## Rescinded by the owner (2026-10-19)

The board owner ended the off-host copy on the same day its first week of lag was
measured, in one line about the Pi:

> **"Не нужны копии на PI удаляй их. мак исходный инстанс более надежный чем эти копии
> на pi"** - the copies on the Pi are not wanted, delete them; the original instance on
> the Mac is more reliable than these copies on the Pi.

**What was done.** `~/cotel-snapshots` on the Pi was deleted - 52 instants, 248 MB,
newest `2026-10-19T11-39-54Z`. The confirmation card that asked once more for an hourly
pull timer was rejected two minutes later. Both halves of the mechanism are therefore
closed, the copy and its schedule, and the schedule question is closed for the third and
last time.

**The state this leaves.** There is no off-host copy of cotel's snapshots. robmini's
local `cotel-snapshots` volume is the sole recovery point, by explicit owner decision.
The accepted cost is **total snapshot loss if robmini is lost** - not a recovery point
measured in days of lag, and not a bounded hole in mirrored history, but all of it. The
two prices this record carried a day earlier were prices *of the mirror*, and they no
longer describe anything: with no second copy there is no `lag` to bound a recovery
point with, and no 14-day reach to lose history outside of.

**The tooling is retired, not parked.** `scripts/pull-snapshots.sh` and its test suite
are deleted, along with their CI step and the five `SNAPSHOT_*` variables in the README.
A pull whose single effect is to recreate the directory the owner has just deleted -
carrying a second plaintext copy of every agent's ingest token with it - is not
something to leave in the tree labelled "ad-hoc": the next person to find it would
reasonably run it, and the thing standing between them and doing so would be a comment.
The transport is still written down, under **The transport, exercised before this was
decided** above, and the implementation is in git history (`260d084`) if a genuine
one-off ever needs it. That is where a one-off belongs.

**This does not come back.** Not the mirror, not a Pi-side scheduler, not a Paperclip
routine that ticks a pull, not the same copy pointed somewhere else. The condition that
would make an off-host copy a question again is the one the original risk model named -
cotel starts holding data whose loss is materially expensive - and it is answered by the
CEO with the board, not re-proposed from engineering as an implementation detail.

## Consequences

These replace the consequences this record carried while the mirror existed. Every one
of those was a consequence of there being a copy on the Pi, and there is no copy.

- **Host loss is uncovered, and total.** robmini holds the only snapshots cotel has.
  The recovery point after losing that host is not a lag measured in days, it is
  nothing. That is the owner's accepted cost; it is written here so the next person
  reading a green snapshot verdict does not infer a second copy behind it.
- **The Pi no longer holds a second copy of every agent's ingest token.** That was the
  honest price of the mirror - `users.token` is plaintext by design, and a snapshot that
  omitted it would not restore to a working instance - and the rescission removes it. It
  is also the reason the tooling was deleted rather than left runnable: what the pull
  recreates is a credential copy, not just data.
- **Nothing to watch, and nothing that claims to.** With no mirror there is no lag to
  measure, so the `--status` reading the runbook used to require before any conclusion
  about recoverability is gone from the runbook as well. What stays watchable is the
  snapshot worker at the source, by hand, per
  [ADR-0025](./0025-best-effort-ingest-watcher-in-actions); a green verdict there now
  means exactly one thing - snapshots are being written on the one host that has them.
- **No footprint in `~/ops`, and none was ever installed.** The timer was asked for
  twice and declined twice at the gate that owns that tree, so there is nothing to
  unwind there now.
- **ADR-0023's deferral of the off-host copy is the final state, not a gap to close.**
  "An off-host copy is a separate decision with its own dependencies" was written as a
  deferral; the decision was then taken, implemented and withdrawn by the owner. The
  risk ADR-0023 named as out of its own scope is open again, on purpose, and reopening
  it is a conversation with the board rather than a ticket.

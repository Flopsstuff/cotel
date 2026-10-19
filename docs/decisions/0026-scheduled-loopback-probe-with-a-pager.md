# ADR 0026 — the deploy host asks `/healthz` on a schedule, and it pages

**Date:** 2026-10-19
**Status:** Accepted
**Deciders:** Prospero (CEO, granting board-facing output), Daedalus (CTO, thresholds and implementation)

---

## Context

[ADR-0025](./0025-best-effort-ingest-watcher-in-actions) put a best-effort
watcher on the **public OTLP ingest** and recorded, as a price rather than an
oversight, that it is blind to everything behind the auth boundary. Three states
share that blindness, and they are the three nothing has observed since the Pi
timer was retired on 2026-10-14:

- **A snapshot worker that has stopped producing restore points.** This is the
  one with an irreversible price: what is lost is lost, and the off-host mirror
  decays on its own schedule ([ADR-0024](./0024-off-host-snapshot-copy-pi-pull)'s
  pull has none).
- **A stale ingest behind a live tunnel.** The instance answers 401 from outside
  while accepting no spans, or while its DuckDB is unreadable.
- **A dead dashboard.** `cotel.aignite.pl` is a different host, and the edge
  probe for it cannot get past Cloudflare Access.

No external vantage point can distinguish any of them from health, because from
outside all three look exactly like a healthy instance refusing an invalid
bearer. The only vantage point that sees them is the deploy host itself, and
this workflow already has it: `probe-loopback` asks
`http://127.0.0.1:8080/healthz` from the self-hosted runner, needs no
credential, and classifies the `snapshot` object from `/api/v1/health`.

It was `workflow_dispatch`-only. ADR-0025 framed the remaining choice as binary
and not an implementation detail: scheduling that job *without* a pager is the
appearance of detection rather than detection, and scheduling it *with* one
re-adds board-facing output that was deliberately curated away when the previous
watcher was retired. That made it the CEO's call, and the CEO granted it.

## Decision

**`probe-loopback` runs on the same `*/15` schedule as `probe-ingest`, and it
pages — with its own dedup marker, the dedup pair that bounds one outage to one
ticket, an in-run red confirmation, and a measured staleness threshold.**

Six things had to be true together; the first five copy `probe-ingest`, whose
shape the board accepted in the same form.

1. **Own marker: `[cotel-healthz-host]`.** New rather than reused. It must not
   collide with `[cotel-ingest-edge]` — on one marker the two halves would
   fight, a green tick on one asking for the close of an alert the other had
   just raised — nor with the drill markers, nor with the script's default
   `[cotel-health-probe]`, which belonged to the retired Pi timer and may still
   have an alert standing under it that this job must not inherit.
2. **`PC_ALERT_MAX_AGE_H=0` and `PC_STILL_RED_WAKE=0`.** The staleness window
   off means an open alert is always the dedup target, so one outage mints
   exactly one ticket; the second drops the per-tick wake. Without the pair,
   one outage is up to 96 tickets a day.
3. **The recovery wake stays on.** It is not optional even though this watcher
   only has to detect failure: with the window off, the open alert dedups every
   later red tick, so until it is closed the *next* outage wakes nobody.
   ADR-0025 records "we only need failure, so drop the green path" as the
   tempting mistake, and it silently disarms the watcher.
4. **A coarse hourly recovery key, `healthz-host-<epoch/3600>`.** `PC_CALL_ID`
   feeds the recovery wake's idempotency key and defaults to the run id. With
   the staleness window off an alert can stand for a long time, and a per-run
   key would make every green tick in the meantime mint another wake — 96
   alarms a day on one open alert.
5. **A red is confirmed inside the run**, by a second probe 60 seconds later.
   The reason here is concrete rather than theoretical: a push to `main` is a
   deploy, the container is down for a few seconds while it restarts, and a tick
   landing in that window would otherwise mint a ticket for a routine deploy.
   The streak cannot live between runs — dropped ticks mean "two runs in a row"
   is not a quantity this job can count.
6. **The ingest staleness threshold is 24 hours on the schedule, 6 by hand.**
   Measured, not assumed; see below.

A dispatch keeps everything it had: the drill marker, the run id as the call id,
and the 6h threshold. Only the scheduled event gets the production marker and
the longer window, by the same `github.event_name == 'schedule'` conditional
`probe-ingest` already uses.

### Why 24 hours, and where the number came from

`scripts/probe-healthz.sh` exits **3** when `newest_span_age_seconds` exceeds
`STALE_AFTER_SECONDS`, and the job shipped with 6 hours — a threshold written
for a *dispatch*, where a human reads the answer. An hourly span histogram
pulled from production over one week (`/api/v1/history?granularity=hour&range=week`)
says 6 hours is not an assertion about this instance:

```
gap 12h  2026-10-14 09:00 -> 21:00   (the tunnel outage — a real red)
gap  6h  2026-10-17 04:00 -> 10:00
gap  6h  2026-10-15 22:00 -> 04:00
gap  6h  2026-10-15 10:00 -> 16:00
```

Ordinary quiet nights on this box reach six and seven hours of ingest silence.
With a 6h window a scheduled pager would mint "stale ingest" tickets several
times a week on nothing at all — precisely the noise the board retired the
previous watcher for. A full day with no span is an assertion: the longest
silence ever measured here, 12 hours, was a genuine outage. So exit 3 stays a
paging class, with a threshold that has evidence under it.

This does not touch `SNAPSHOT_STALE_AFTER_SECONDS` (12h). The snapshot worker
ticks on its own timer and does not care how much traffic arrives, so its window
is independent of the instance's traffic shape.

## Consequences

- **This job cannot report its own host being down, and nothing in it can be
  made to.** With the runner offline the run sits in the queue, and
  `timeout-minutes` does not bound queue time — it starts counting when a runner
  picks the job up. An absent host therefore produces no colour at all, and the
  only trace is a run in `queued`, which is also what a busy runner looks like.
  That external failure is `probe-ingest`'s to catch, and it is why **neither
  job is sufficient alone**: their blindnesses are complementary, one inside the
  network and one outside it, which is also why neither may be moved to the
  other's runner.
- **The three unwatched states above are now watched, on a best-effort
  schedule.** The detection delay is unbounded for the same reason ADR-0025's is:
  GitHub drops scheduled ticks on public repositories rather than delaying them.
  Whether it ran at all is a measurement, not an assumption —
  `gh run list --repo Flopsstuff/cotel --workflow=health-probe.yml --event=schedule`
  and empty output is the answer rather than an error.
- **A second production marker exists, and the two must stay separate.**
  `[cotel-healthz-host]` and `[cotel-ingest-edge]` watch different things from
  different places and can be red independently: the tunnel outage of 2026-10-14
  was red on the ingest marker while `/healthz` answered 200 on the LAN
  throughout. Merging them would make each half's green tick close the other
  half's alert. A reader seeing both open is looking at two findings, not a
  duplicate.
- **Two production pagers can now mint tickets, so the board-facing cost
  roughly doubles** at the ceiling of one ticket per outage per marker. That
  ceiling is what the dedup pair buys, and it is the whole reason the pair is
  not optional.
- **Recovery is load-bearing twice over.** An alert nobody closes disarms its
  marker until someone does, and now there are two markers to keep clear. The
  standing rule applies to both: the alert closes when its assignee next wakes
  on a green tick, not the instant the probe recovers
  ([ADR-0021](./0021-recovery-wakes-the-alerts-assignee)).
- **A deploy no longer pages, and that cost 60 seconds.** The in-run second look
  means a red tick takes about a minute longer to page than it would otherwise.
  That is the right trade against a ticket for every deploy that happens to
  overlap a tick.
- **The pager's default re-probe hint is finally correct for one caller.** The
  default text in `scripts/page-cotel-health.sh` tells the woken agent to
  dispatch the loopback half, which is wrong for every *other* caller because
  that runner may be the dead host. For an alert raised **by** this job the host
  is demonstrably up — it just ran the probe — so the default stands and this
  job overrides nothing. Any new caller still has to override it.
- **`probe-edge` stays dispatch-only**, and not on technical grounds: the Access
  service token is still refused on `cotel.aignite.pl`, and asking again was
  made conditional on an outage of the dashboard host specifically while the
  ingest name is live. The dashboard is nonetheless no longer wholly unwatched —
  a dead dashboard process shows up on this job's `/healthz`, from inside the
  host; what remains unwatched is its path through Cloudflare.
- **ADR-0025's blind half is closed**, and its consequence bullets that read
  "nothing asks `/healthz` on a schedule now" are lifted by this record. Its
  other half stands: the off-host snapshot mirror still has no schedule, so a
  green probe here is not a statement about recoverability. Read
  `scripts/pull-snapshots.sh --status` before making one.
- **No new credential, and nothing for the board to do.** This job already had
  every secret it needs; the change is an event name, a marker, three
  environment variables and one more probe call.

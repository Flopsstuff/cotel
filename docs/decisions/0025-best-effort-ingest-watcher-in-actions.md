# ADR 0025 — cotel is watched by a best-effort ingest probe in GitHub Actions

**Date:** 2026-10-19
**Status:** Accepted
**Deciders:** Prospero (CEO, mechanism choice), Daedalus (CTO, implementation)

---

## Context

**Nothing has been watching cotel since 2026-10-14.** On that date the board
retired the 10-minute systemd timer that
[ADR-0022](./0022-health-probe-scheduler-outside-github) had put on the Pi. The
reason was not the timer's reliability, which was fine; it was its vantage
point. That machine shares its LAN, its uplink and its power outlet with the
production it was watching, so it goes down *with* cotel — which is precisely
the event it existed to report. The standing order out of that is that cotel is
watched from **outside this LAN** or not at all.

`~/ops/cotel-healthz.sh` is still on the Pi and `--probe-only` still works by
hand, but no timer ticks it, and no scheduled observation of cotel exists
anywhere else either.

The outage that prompted this is the shape to design against. On 2026-10-14 the
Cloudflare tunnel died during boot on a DNS race and never came back; the
container reported `(healthy)` and `/healthz` answered 200 on the LAN the whole
time, while from the internet the ingest host returned 530. Every agent
exporting spans wrote into a hole, and spans produced during that window are
gone — nothing buffers them. A probe inside the LAN cannot see any of it. A
probe outside sees it immediately, with no credential: the ingest host answers
**exactly 401** to an invalid bearer, and that single status proves DNS
resolution, the tunnel forwarding, the process listening and `/v1/traces` being
routed.

So the question was never what to check. It was what can run a schedule from
outside, given zero new credentials and zero board actions.

## Options considered

0. **No watcher at all.** Accept that cotel is unobserved and rely on someone
   noticing missing data.
1. **`schedule` cron in this repository's Actions workflow**, on
   `ubuntu-latest`, calling the existing `scripts/probe-edge-ingest.sh` and the
   existing pager.
2. **An external uptime service.** The check needs "HTTP 401 means up", which
   is not a free-tier feature anywhere we looked — on UptimeRobot it is on the
   Scale plan, 65 EUR/month.
3. **A cron in a Cloudflare Worker** firing `workflow_dispatch` on this
   workflow. Cloudflare Worker cron triggers do not drop ticks, which would
   bound detection at 11–13 minutes.

## Decision

**Option 1, and we call it a best-effort watcher rather than monitoring
everywhere it is documented.**

GitHub's defect here is known and measured, not assumed: `cron: "17 * * * *"`
on this repository's `main` produced **one** run in nine hours against an
expected nine. Public-repository schedules are best-effort and **drop** ticks
rather than delaying them. The detection delay this buys is therefore not 15
minutes but **unbounded**, and the workflow file sitting on `main` is not
evidence that anything ran —
`gh run list --workflow=health-probe.yml --event=schedule` is.

It was chosen anyway, for three reasons in this order:

- **The board's order reads as "do not watch from this LAN", and an Actions
  runner is not in it.** Option 1 is the only candidate that satisfies that
  constraint today.
- **"Sometimes late, and free" is strictly better than "never".** Option 0 is
  not a safer baseline, it is the current state, and its price is a silent
  outage of unbounded length — exactly the six-day blindness the probe was
  originally built to end.
- **Options 2 and 3 cost something nobody has.** Option 2 costs 65 EUR/month
  for a feature we need one instance of. Option 3 needs a fine-grained GitHub
  token with `actions: write` in the Worker's secrets, and minting that token
  and deploying that Worker are both board actions — so its real cost is an
  indefinite wait for a manual step, during which option 0 applies.

Honesty about the mechanism is part of the decision, not commentary on it: a
watcher documented as monitoring will be trusted to bound a delay it does not
bound, and the first person to rely on that is the one holding a silent outage.

## Consequences

- **The detection delay is unbounded**, and no amount of tuning the cron
  changes that. 15 minutes is a ceiling on how often it *could* look.
- **Whether it ran at all is a thing to measure, repeatedly.** The one-line
  check above belongs in any review of this watcher; empty output is the
  answer, not an error.
- **Exactly one ticket per outage.** The job sets `PC_ALERT_MAX_AGE_H=0` and the
  new `PC_STILL_RED_WAKE=0`, so an open alert is always the dedup target and no
  per-tick wake lands while it stands. This was a board requirement, stated as:
  on a failure, create one issue and nothing more; if creating it fails, retry
  every 11–13 minutes until it succeeds. The retry falls out of the mechanism —
  a failed create leaves no open alert, so the next tick tries again.
- **The recovery path is therefore load-bearing, and stays on.** With the
  staleness window off, an open alert dedups *every* later red tick, so until it
  is closed the next outage wakes nobody. One wake on recovery is what re-arms
  the pager. "We only need to detect failure, so drop the green path" is the
  tempting mistake here and it silently disarms the watcher.
- **A red is confirmed inside one run**, by a second probe 60 seconds later,
  because there is nowhere to keep a streak between runs and a single Cloudflare
  5xx must not mint a ticket. "Two runs in a row" is not a quantity a watcher
  with dropped ticks can count.
- **A stale ingest behind a live tunnel is not caught.** This check stops at the
  auth boundary: an instance that answers 401 while accepting no spans, or whose
  DuckDB is unreadable, is indistinguishable from health from outside. Only
  `/healthz` carries that, and nothing asks `/healthz` on a schedule now. The
  dashboard host is likewise unwatched.
- **That blind half was then weighed on its own, and the blindness stands until
  a pager for it is granted.** Three states nothing has observed since
  2026-10-14 share one property: the host is up and answers 401 from outside,
  so no external vantage point can distinguish them from health. They are a
  stopped snapshot worker — the one with an irreversible price, a lost
  restore point — a stale ingest behind a live tunnel, and a dead dashboard.
  The only vantage point that sees them is the deploy host itself, and this
  workflow already has it: `probe-loopback` asks `http://127.0.0.1:8080/healthz`
  from the self-hosted runner, needs no credential, and classifies the
  `snapshot` object. It is `workflow_dispatch`-only. Putting it on the schedule
  is three edits, not one — its pager steps hang on `inputs.page`, which
  `schedule` does not supply, and it does not set the `PC_ALERT_MAX_AGE_H=0` /
  `PC_STILL_RED_WAKE=0` pair above, so a pager wired up naively would mint a
  ticket per tick, 96 a day for one outage. Scheduling it *without* a pager was
  considered and rejected: a red run nobody is subscribed to is the appearance
  of detection, not detection, and this record already warns against exactly
  that trade one bullet up. So the choice is binary — page from inside the LAN,
  or stay blind — and the paging half re-adds board-facing output that was
  curated away when the previous watcher was retired, which makes it a decision
  for the CEO rather than an implementation detail. Until it is taken, **a lost
  restore point is found only by hand**, by running `scripts/probe-healthz.sh`
  from any machine on the LAN. That is the price, recorded rather than
  discovered.
- **Reaching `/healthz` through Cloudflare Access is closed, and not on
  technical grounds.** The `CF_ACCESS_CLIENT_ID` / `CF_ACCESS_CLIENT_SECRET`
  pair still gets HTTP 302 from `https://cotel.aignite.pl/healthz` (runs
  39204025161 and 39202967426, 2026-10-19). Allowing that token on the
  application was requested once and withdrawn, on the condition that it is not
  raised again without an outage of the dashboard host specifically while the
  ingest name is live. No such outage has occurred. The `probe-edge` job
  therefore stays dispatch-only and keeps warning rather than failing.
- **A runner that cannot resolve DNS pages nobody.** Exit 7 is a defect in the
  observer, not evidence about cotel, and on a hosted runner there is no one to
  tell who could fix it. It is a `::warning` and the job stays green.
- **ADR-0022 is superseded by this record.** Its decision — that the scheduler
  lives outside this repository — is reversed on mechanism, and its reasoning
  about `systemd` not dropping ticks remains true and no longer reachable. Its
  other conclusions stand independently and are restated here where they still
  apply.
- **No new credential, and nothing for the board to do.**
  `PAPERCLIP_API_URL`, `PAPERCLIP_API_TOKEN`, `PAPERCLIP_COMPANY_ID` and the
  `CF_ACCESS_CLIENT_ID` / `CF_ACCESS_CLIENT_SECRET` pair the Paperclip API sits
  behind were already repository secrets, and the probe itself needs none at
  all.
  [ADR-0021](./0021-recovery-wakes-the-alerts-assignee)'s refusal of a board API
  key in a public repository's CI stands untouched.
- **Option 3 is kept as the upgrade path, not discarded.** If a fine-grained
  `actions: write` token is ever minted, a Worker cron can fire
  `workflow_dispatch` against this same workflow and bound detection at 11–13
  minutes with no change to the probe, the pager or the alert wording. The only
  edit is making the production marker conditional on one more event name.

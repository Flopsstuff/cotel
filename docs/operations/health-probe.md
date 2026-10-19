# Production health probes

One scheduled watcher asks production one question: the `probe-ingest` job in
`.github/workflows/health-probe.yml` requests the **public OTLP ingest URL**
from a GitHub-hosted runner every 15 minutes, and pages a Paperclip agent when
two looks a minute apart are both red.

**It is a best-effort watcher, not monitoring.** GitHub schedules public
repositories on a best-effort basis and *drops* ticks rather than delaying them
— measured in this repository, one run out of the nine expected over nine hours
— so **the detection delay is unbounded**, and a green workflow file is not
evidence that anything ran. It is strictly better than never looking, and that
is the whole of what it promises. See
[ADR-0025](../decisions/0025-best-effort-ingest-watcher-in-actions.md).

The 10-minute systemd timer on the Pi that used to own this was **retired on
2026-10-14 by board order**: it shared the uplink, the LAN and the power outlet
with the production it watched, so it went down *with* cotel — which is the
failure it existed to report. `~/ops/cotel-healthz.sh` is still on that machine
and `--probe-only` still works by hand, but nothing ticks it, and nothing else
in `~/ops` watches cotel either. Any scheduled observation of cotel has to come
from outside that network, which is what this job is.

A watcher of some kind is needed because a dead or silent cotel otherwise has
no one looking at all: `Deploy` only runs on a push to `main`, the Cloudflare
tunnel stays green while the process is 503 or not listening, and a red Actions
run in this company does not wake anyone on its own.

## Did it actually run?

The workflow file being on `main` proves nothing — dropped ticks are the known
defect, not an edge case. The schedule's real cadence is one command:

```sh
gh run list --repo Flopsstuff/cotel --workflow=health-probe.yml --event=schedule --limit 5
```

Empty output means no scheduled run has happened, however healthy the workflow
looks. That is the measurement of this watcher, and it is worth repeating
occasionally rather than assuming the first result holds.

## What this watcher cannot see

Three gaps, none of which it will report:

- **A silent stale ingest behind a live tunnel.** The check stops at the auth
  boundary, so an instance that answers 401 while accepting no spans, or whose
  DuckDB is unreadable, is indistinguishable from health from outside. Only
  `/healthz` carries that, and nothing asks `/healthz` on a schedule now.
- **A dead dashboard with a live ingest.** `cotel.aignite.pl` is a different
  host behind Cloudflare Access and is not probed on any schedule.
- **Itself.** If the tick is dropped, or the runner cannot resolve the name,
  nothing anywhere notices that the watcher is not watching.

## The vantage points

Production is asked from several places, because each is blind to something the
others see. Only the first column is scheduled; the other two run on a
dispatch, as drills.

| | `probe-ingest` (**scheduled**) | `probe-loopback` (dispatch) | `probe-edge` (dispatch) |
|---|---|---|---|
| Scheduled | **yes, `*/15 * * * *`, best-effort** | no | no |
| Runner | `ubuntu-latest` — never self-hosted | `[self-hosted, flopsstuff, docker]` — the deploy host | `ubuntu-latest` |
| Target | `https://otlp.aignite.pl/v1/traces` (internet) | `http://127.0.0.1:8080/healthz` | `https://cotel.aignite.pl/healthz` |
| Cloudflare in the path | yes (DNS + tunnel) | no | yes (tunnel + Access) |
| Catches | DNS, a blanked tunnel token, the ingest route not served, Access appearing in front of ingest | process dead, crash loop, 503 DuckDB, stale/empty ingest, a snapshot worker that has stopped producing restore points | the dashboard host through Cloudflare |
| Blind to | the database and ingest freshness: this check stops at the auth boundary; and its own dropped ticks | **its own host being down** — with the runner off the job queues, producing no colour at all | nothing in the path, but see the Access caveat below |
| Access service token | **not needed** | not needed | required |
| Pages | **production** `[cotel-ingest-edge]` on a schedule, `[cotel-ingest-edge-drill]` on a dispatch | `[cotel-health-probe-drill]` — drill only | `[cotel-health-probe-drill-edge]` — drill only |

`probe-ingest` runs on `ubuntu-latest` and must never be moved to the
self-hosted runner. An external vantage point is the entire reason this job
exists, and the self-hosted alternative is worse than it looks: a job on the
deploy host cannot report that host being off, because GitHub queues the run
rather than failing it, and `timeout-minutes` does **not** bound queue time — it
starts counting once a runner picks the job up. An absent host yields no colour
at all, and the only trace is a run sitting in `queued`, which is also what a
busy runner looks like.

Every prober uses a **different dedup marker** on purpose. On a single marker
they would fight: a green tick on one would ask for the close of the alert
another had just raised. A dispatch is separated from the schedule for the
stronger version of the same reason — a drill must not touch a real alert; see
[Running a drill](#running-a-drill).

The `[cotel-health-probe]` marker, and the `/healthz` probe behind it, belong to
no scheduled caller any more. `scripts/probe-healthz.sh` is still here, still
tested, and still the right thing to run by hand or from a future scheduled
caller; what follows describes it on that basis.

## What the `/healthz` probe checks (nothing runs it on a schedule)

`scripts/probe-healthz.sh` classifies failure modes with distinct text, because
they send the on-call looking in different places:

| Probe verdict | Exit | Typical cause |
|---|---|---|
| `unreachable` | 1 | Process not listening, host down, or the tunnel is not forwarding (name already resolved) |
| `HTTP 503 database unreadable` | 2 | Process is up; DuckDB is not |
| `HTTP <other>` | 2 | Crash loop, proxy error, unexpected handler |
| `ingest stale` / `empty database` | 3 | Process is up and the DB reads; spans are not being accepted |
| `cloudflare access blocked` | 4 | Probe config, **not** an outage — see below |
| `no current database snapshot` | 6 | Everything above is fine; the backup is not happening — see below |
| `observer cannot resolve name` | 7 | The **prober host** could not resolve the name — production was never asked |

A 200 with `ok: true` is not enough. Staleness is a body field
(`newest_span_age_seconds`); the endpoint keeps 200 on a quiet instance so the
container HEALTHCHECK does not flap. The probe applies its own threshold:
**6 hours**. A present JSON `null` is an empty database (never ingested), which
is not the same as `0` (ingested just now) and not the same as a missing key.

If `last_ingest_at` / `newest_span_age_seconds` are absent, the probe degrades
to liveness (HTTP 200) so it keeps working before that contract is on
production.

### The snapshot claim, asked of a second endpoint

A green `/healthz` is followed by a second question, to `/api/v1/health` on the
same host — the only place the snapshot worker's own report exists. Without it
the backup could die and nothing would say so until somebody opened the
endpoint by hand, which is the failure class that already cost six days of
blind outage once.

The address is **derived** from the `/healthz` URL (same entry point, other
path), so a caller configures one URL and a change here needs no edit on its
host. `API_HEALTH_URL` overrides the derivation.

| `snapshot` object | Verdict |
|---|---|
| `status: "error"` | red, exit 6, with `last_error` quoted |
| `status: "ok"` and `last_run_at` older than `SNAPSHOT_STALE_AFTER_SECONDS` (default 12 h) | red, exit 6 |
| `status: "ok"` and `last_run_at` absent or unparseable | red, exit 6 |
| `status: "unknown"` | not asserted; said on the green line |
| no `snapshot` field, or `/api/v1/health` not readable | not asserted; said on the green line |

The split is the point. `error` and an overdue timestamp are *affirmative*
evidence that the restore point is gone, and can be trusted anywhere.
`unknown` is not: it means both "the worker has not run yet" and "snapshots are
disabled", and disabled is the shipped default everywhere but production — a
probe that reddened on it would lie on every developer's instance.
`SNAPSHOT_CHECK=require` is the caller asserting that this instance is one
where snapshots are expected, and turns every "cannot assert" into red.
`SNAPSHOT_CHECK=off` skips the question entirely.

Exit **6** and not 5 because the retired `~/ops/cotel-healthz.sh` spends 5 on
"the LAN half is green and the public ingest half is not", and the exit codes
are a contract any future caller reads.

This verdict pages under the `/healthz` marker, but not in its words: the alert
is titled `cotel prod database snapshot is red` and leads with "the application
is alive; its backup is not". See [the two halves are classified
apart](#the-two-halves-are-classified-apart-and-page-apart).

Two vantage points this must not be turned on for: anything reaching cotel
through `cotel.aignite.pl`, where Cloudflare Access answers instead of the
application (the default `auto` degrades quietly there, `require` would page on
a probe-configuration problem), and the ingest probe, which never reads
`/healthz` at all.

| Variable | Default | Role |
|---|---|---|
| `SNAPSHOT_CHECK` | `auto` | `auto` pages on affirmative failure only; `require` also pages when the instance makes no snapshot claim; `off` never asks |
| `SNAPSHOT_STALE_AFTER_SECONDS` | `43200` | How old the newest snapshot may be. Two `COTEL_SNAPSHOT_INTERVAL` periods at the shipped `6h`; move it with the interval |
| `API_HEALTH_URL` | derived from the `/healthz` URL | Override when the snapshot report is not at the same entry point |

What counts as red, why the threshold is two intervals, and how to check it by
hand are in
[Database Snapshots and Restore](./duckdb-snapshots#who-is-watching-it).

## What the scheduled ingest probe checks, and why it needs no Access token

`scripts/probe-edge-ingest.sh` asks `https://otlp.aignite.pl/v1/traces` — the
endpoint every agent exports to — and expects **exactly HTTP 401**.

The 401 is the whole point. The ingest host is **not** behind Cloudflare
Access, so an unauthenticated request reaches cotel's own auth middleware, and
only a working chain can produce that answer: DNS resolving, the tunnel
forwarding, the process listening, and `/v1/traces` routed to the auth-wrapped
ingest handler. No Access service token is involved, and none is needed.

| Probe verdict | Exit | Typical cause |
|---|---|---|
| `HTTP 401 from the ingest handler` | 0 | the public ingest path is up |
| `public ingest unreachable` | 1 | connection refused or timeout after resolve — a blanked tunnel token looks like this |
| `cloudflare reached, the origin did not answer` | 2 | 502/530 and friends: the tunnel is up, the origin is not |
| `/v1/traces is not routed` | 2 | 404 — wrong tunnel target, or the ingest port is not served |
| `accepting unauthenticated spans` | 2 | 200 to an invalid token: reachable, but the auth policy is open |
| `401 but not from the ingest handler` | 2 | a 401 whose body is not the application's JSON — an interstitial, not cotel |
| `cloudflare access now fronts the ingest host` | 4 | Access was enabled on this host, which rejects every agent's spans too |
| `observer cannot resolve name` | 7 | The **prober host** could not resolve the name — public ingest was never asked |

Two deliberate narrownesses:

- **The expected status is a single code, not "any answer".** Accepting any
  response would let a Cloudflare interstitial or a parked-domain page pass as
  health, which is the failure this half exists to catch.
- **The request carries a deliberately invalid `cotel_` bearer**, not no bearer
  at all. The auth middleware rejects an unknown token before the handler sees
  the request, so the expected 401 holds whichever way `allow_anonymous` is set
  on the instance; with anonymous ingest allowed, a tokenless request would
  reach the handler and answer 405. It is a `GET`, so even a bypassed auth
  check could not write anything.

This probe is blind to what `/healthz` sees: it stops at the auth boundary and
says nothing about the database or ingest freshness. Both used to run on one
tick; this is the one that survives, so that blindness is now a standing gap —
see [What this watcher cannot see](#what-this-watcher-cannot-see).

### How the scheduled job runs it

- **Two looks, one run.** A red probe is followed by `sleep 60` and a second
  probe, and the pager is only called when both are red, so a single Cloudflare
  5xx does not mint a ticket. The streak cannot span runs: there is nowhere
  between runs to keep one, and with ticks being dropped "two runs in a row" is
  not a quantity this watcher can count.
- **Exit 7 pages nobody.** It means the runner failed DNS, so production was
  never asked — a defect in the observer, not evidence about cotel. The job
  emits a `::warning` and stays green.
- **Exits 1, 2 and 4 are the outage**, and fail the job as well as paging. Exit
  4 here means Access was put in front of the *ingest* host, which rejects
  every agent's spans too; that is unlike the dashboard-edge job's exit 4,
  which is only a probe-configuration problem (see the Access caveat below).
- **One ticket per outage.** The job sets `PC_ALERT_MAX_AGE_H=0` and
  `PC_STILL_RED_WAKE=0`, so an open alert is always the dedup target and no
  further wake lands while it stands. The consequence is in
  [Pager environment](#pager-environment): the recovery wake is the only thing
  that re-arms the pager.

### The two halves are classified apart, and page apart

"The process is dead" and "the process is fine, the public path is down" need
different people doing different things, so they never share an alert:

- **separate dedup markers** — `[cotel-health-probe]` and `[cotel-ingest-edge]`.
  On one marker the halves would fight, a green tick of one asking for the close
  of the alert the other just raised;
- **separate failure streaks** — the retired timer kept them in
  `reports/cotel-healthz/state` and `state-edge`, so an uplink blinking could
  not page for a healthy application and vice versa. The scheduled job has no
  store between runs and confirms a red inside one run instead;
- **different words** — the ingest alert's title, first line and wake reason say
  that the application is alive and its public ingest path is not, and tell the
  reader to look at the tunnel and DNS rather than restart the container. The
  pager takes those from `PC_ALERT_SUBJECT` and `PC_ALERT_LEAD`.

Exit **7** is a third class: the observer could not resolve the name, so
production was never sampled. Collapsing it into exit 1 would burn the streak
and page "cotel prod /healthz is red" while a real public-ingest outage stayed
invisible behind the resolver failure. The scheduled job treats it as a
`::warning` and pages nobody — a GitHub-hosted runner that cannot resolve is a
defect in the observer, and there is no one to tell about it who could act.
(The retired timer, which ran on a machine the owner could fix, paged exit 7
under its own markers `[cotel-observer-dns]` / `[cotel-observer-dns-edge]`
instead.)

The `/healthz` probe needs the same split **inside** its one marker, because it reddens
on two unrelated things: a `/healthz` that does not answer, and a `/healthz`
that answers while the backup has stopped. So when the caller passes no
`PC_ALERT_SUBJECT`, the pager classifies the subject from the probe verdict:

| Verdict | Title | Lead says |
|---|---|---|
| `no current database snapshot`, or `snapshots are required …` | `cotel prod database snapshot is red [cotel-health-probe]` | the application is alive, the backup is not; read the `snapshot` object in `/api/v1/health` and the worker's logs; do **not** restart cotel |
| anything else | `cotel prod /healthz is red [cotel-health-probe]` | the `/healthz` probe is red |

An explicit `PC_ALERT_SUBJECT`/`PC_ALERT_LEAD` always wins over the
classification — that is how the ingest job keeps its own words.

**The marker stays out of it.** Both subjects share one alert slot, so two red
ticks of that probe — in either order, snapshot then `/healthz` or the reverse —
still dedup into one ticket. Were the subject part of the dedup key, one
watcher's consecutive reds would mint an alert each.

One asymmetry the retired timer had and the scheduled job does not: while its
LAN half was red, its ingest half did not page, because a dead process makes
the public path unreachable as a consequence and a second alert would send its
reader hunting Cloudflare for a dead container. Nothing probes `/healthz` on a
schedule now, so there is no suppression to apply — which also means a red
ingest alert no longer implies anything about the process. Its description says
so, and tells the reader to check `/healthz` before touching the tunnel.

### The Access caveat (the dispatched dashboard-edge job)

Cloudflare Access sits in front of the **dashboard** host, `cotel.aignite.pl`.
The dispatch-only `probe-edge` job sends the same Access service-token headers
as `paperclip-issue-sync.yml`, but that token is **not allowed on that
application**: the request comes back as a 302 to the Access login, exactly as
it does with no token at all.

So `probe-edge` reports exit 4 as a **warning, and the job stays green**. That
is deliberate. Failing on exit 4 would paint the workflow red on every run until
the token is allowed, and a probe that is always red is one nobody reads —
worse than no probe, because it also buries a genuine red. Exit 4 never pages
either: it says nothing about whether cotel is up.

**This caveat is not a gap in the scheduled coverage, and a service token is not
what would close one.** That was the earlier reading of it — that a meaningful
check from outside the LAN had to wait for the token to be allowed on
`cotel.aignite.pl`. It does not: the ingest host answers 401 to anyone, and that
401 proves DNS, tunnel, process and route, which is exactly what a probe on the
LAN cannot see. The scheduled job above uses it and asks for no credential.
What an Access-allowed token would add is narrower: the *dashboard* host's view
through Cloudflare, and the database and freshness fields that only `/healthz`
carries. Worth having, not worth blocking on.

## Schedule

`cron: "*/15 * * * *"` in `.github/workflows/health-probe.yml`, on the
`probe-ingest` job. Nothing else — not in this repo, not in `~/ops`, not in a
Paperclip routine — probes cotel on any schedule.

**The interval is not the detection delay, and there is no bound on the
detection delay.** GitHub drops scheduled ticks on public repositories, so the
cadence set here is a ceiling on how often it *could* look, not a floor on how
often it does. 15 minutes is chosen because the cadence is free — a dropped
tick costs nothing and a kept one costs a single HTTP request — so the only
thing it trades against is nothing at all.

What the schedule does bound is the alarm it can raise: one red tick is
confirmed by a second look 60 seconds later inside the same run, so a blinked
Cloudflare 5xx wakes nobody, and a confirmed red opens exactly one ticket.

Verify the cadence rather than believing it:

```sh
gh run list --repo Flopsstuff/cotel --workflow=health-probe.yml --event=schedule --limit 5
```

### Why the schedule is in this repo despite dropping ticks

The same `cron` was here before and was removed for the reason it is still
imperfect now: `cron: "17 * * * *"` was merged to `main` at 00:44Z on
2026-10-05 and had produced **one** run by 09:46Z — one tick of an expected
nine. `gh run list --workflow=health-probe.yml --limit 200` showed exactly one
`event=schedule` run in the workflow's entire history; it was not a
cancellation (those stay in the list as `cancelled`) and not the 60-day
public-repo deactivation (the repo was active that day).

What changed is not GitHub's reliability but the alternatives. The replacement
— a systemd timer on the Pi, which does not drop ticks — was retired on
2026-10-14 because that machine shares its network, uplink and power with the
production it watched: it went down *with* cotel, and the standing order after
that outage is that cotel is watched from outside this LAN or not at all. With
a LAN timer ruled out, the choices left were an external uptime service (paid,
and "401 = up" is a paid-tier feature), a Cloudflare Worker cron firing
`workflow_dispatch` (needs a GitHub token nobody has minted), and this. See
[ADR-0025](../decisions/0025-best-effort-ingest-watcher-in-actions.md) for the
full comparison and the upgrade path.

So this is a deliberate downgrade in reliability from the timer, taken because
the timer's vantage point was invalid. Sometimes-late-and-free beats never.

### Dispatching the workflow

Actions → **Health probe** → **Run workflow**. Each half has its own optional
URL override (`url` for the edge, `loopback_url` for the host) for demonstrating
a red run against a dead endpoint; leave **page** unchecked unless you intend to
open a Paperclip alert — see [Running a drill](#running-a-drill) for what a
dispatch with **page** checked does.

A dispatch is the only way `probe-loopback` and `probe-edge` run at all, and it
is also how `probe-ingest` is rehearsed. The `test` job still runs on every pull
request and on pushes that touch the probe scripts, so the classification and
pager tests remain a merge gate.

## Running a drill

Every rehearsal pages a **drill** marker, never a production one. The production
marker `[cotel-ingest-edge]` belongs to `probe-ingest` **on a schedule only**;
the same job on a dispatch pins `[cotel-ingest-edge-drill]`, and the other two
jobs pin `[cotel-health-probe-drill]` and `[cotel-health-probe-drill-edge]`
unconditionally. `[cotel-health-probe]` has no caller at all now.

Drill the scheduled watcher by dispatching **Health probe** three times, with
**page** checked each time:

| Dispatch | `ingest_url` | What it should do |
|---|---|---|
| 1 | `http://127.0.0.1:9/v1/traces` | both looks red → **opens** the drill alert |
| 2 | `http://127.0.0.1:9/v1/traces` | red again → **no** second ticket and **no** still-red wake: `PC_ALERT_MAX_AGE_H=0` makes the open alert the dedup target and `PC_STILL_RED_WAKE=0` silences the wake |
| 3 | empty (production ingest) | green → **recovery wake** to the alert's assignee, who closes it |

Dispatch 2 is the "one ticket per outage" rule under test, and its verdict is
not the job's colour — the job is red on both 1 and 2 either way. Read the
pager's own line in the step log (`… is open and still-red wakes are off; woke
nobody`) and confirm no second issue carries the drill marker.

**Dispatches 2 and 3 only mean something while the alert is still open, and in
a drill it usually is not.** The assignee is woken by the create, re-probes the
*real* ingest as the alert tells them to, finds it green, and closes the alert
— measured at 58 and 85 seconds after the create. One dispatch cannot reach its
pager step faster than about 80 seconds (queue and checkout, then the 60-second
re-probe), and `cancel-in-progress` forbids overlapping the next dispatch with
the current one, so the second red tick arrives *after* the alert is already
closed and correctly opens a fresh one. That reads exactly like a dedup failure
and is not one.

A real outage does not have this problem: production is still red when the
assignee re-probes, so they add evidence instead of closing, and the alert is
there for the next tick to dedup into. To drill those two legs anyway, the
assignee has to agree in advance to **leave the alert open for about three
minutes**. Without that agreement, dispatch 1 is the only leg a drill proves,
and the dedup and still-red behaviour is covered by
`scripts/page-cotel-health_test.sh` instead.

A red dispatch also runs `probe-loopback` and `probe-edge`, which probe the real
endpoints on their own drill markers. Against a healthy production neither of
them pages, but if the self-hosted runner is offline `probe-loopback` queues and
the *run* never concludes — read `probe-ingest` on its own rather than the run's
overall conclusion.

The one thing to put back afterwards is the drill alert: it is a real issue
someone has to close. There is no shared failure streak left to reset — the
scheduled job keeps no state between runs.

Dedup is per marker, so the sets never see each other: a red drill cannot
attach itself to a standing real alert, and — the half that actually bites — a
green drill cannot clear or file recovery against one nobody has read yet.

Every alert body names the triggering event, the actor and the effective probe
URL, so a woken reader can tell an exercise from an outage without opening
Actions — the probe text cannot say, since a closed-port drill and a dead
process produce the same line.

The `/healthz` pager path is drilled the same way through the loopback job:
dispatch twice with **page** checked, once with
`loopback_url=http://127.0.0.1:9` (a closed port — raises the alert, whose
assignment wakes its assignee), then once with the default (green — wakes that
assignee again, who closes the alert from their own run). Never break production
to get a red run.

**Leave a gap between the red and green dispatches.** Wait for the alert's
*assignment* run to finish before dispatching the green one. Back to back, the recovery wake is
coalesced into the still-live assignment run, that run closes the alert, and the
drill proves nothing about the recovery path — the alert ending `done` looks
like success either way. Check it rather than assume it:

```sh
curl -s -H "Authorization: Bearer $PC_API_TOKEN" \
  "$PC_API_URL/api/issues/<alert id>/diagnostics/wakes"
```

The recovery wake must appear with `source: automation` and a **run id of its
own** — not the assignment wake's id, and not `status: coalesced`. That is the
only artifact distinguishing the recovery path from the raise path.

A timed gap is not a guarantee. `in_progress` with no live run is not a state
the runtime holds still: it may re-wake the assignee as a continuation of their
own finished run, putting a live run back in front of the green half. So read
the diagnostics after the drill instead of trusting the gap you waited out.

**Match the rows by `requestedAt`, not by run id.** A coalesced wake answers
with — and is listed against — the run it was folded into, so the recovery
wake and the run that absorbed it show the *same* run id. Reading the verdict
off that id finds a row with `coalesced: 0` that belongs to the earlier wake.
The recovery wake is the row whose `requestedAt` matches the `woke …` line in
the green run's log, to the second. If that row says `status: coalesced`, the
drill did not exercise the recovery path, whatever the other rows say and
however the alert ended.

No drill has yet caught the recovery wake *starting* a run. Three pairs have
closed the alert unattended, and the third proved the raise path did not do it
— its assignment run had finished two minutes earlier — but in every pair the
recovery wake was folded into a run that already held the alert. That branch is
the ordinary production shape (an alert standing for an hour has no live run),
and it is the same `wakeup` call the assignment path makes, so the risk is low
and the gap is in the evidence, not in the mechanism. Say which of the two you
have when you cite a drill.

**The dispatcher owns their own drill artifacts.** A drill alert is a real
Paperclip issue assigned to a real agent, and it spends a heartbeat exactly
like an outage does — the drill marker keeps it out of production's dedup, it
does not make it free. The green half wakes that assignee to close it rather
than closing it itself (see [Everything after the create is a wake, not a
write](#everything-after-the-create-is-a-wake-not-a-write)), so a drill whose
green dispatch you never ran, or whose recovery wake was coalesced, leaves the
alert standing. Check that the issues your dispatch minted are closed when the
drill is over, and say in the close that they were drill artifacts.

## Who is woken, and how

A red GitHub Actions run is **not** the page. Notifications on the Fl0p account
are unproven (no `notifications` API scope, no public mailbox, agent identities
are not GitHub users), and this company has already watched red Actions sit
unnoticed. The scheduled job going red is therefore not how anyone finds out;
the Paperclip alert it opens is.

On red, the prober opens a Paperclip issue titled `cotel <subject> is red
[<marker>]` — `public ingest at <host>` from the scheduled job, `prod /healthz`
by default — assigned to Daedalus. That assignment
is the wake, and it is the only tracker issue the pager ever creates. A later
tick searches `q=<marker>` and acts on the open issue whose **title** contains
it. Search also matches comments and descriptions, so the first hit is not the
alert, and a longer marker such as `[cotel-health-probe-selftest]` is not this
one. The create request does not send `originId`: the issues API drops unknown
fields, and list search does not query that column.

### Everything after the create is a wake, not a write

`PAPERCLIP_API_TOKEN` is a long-lived **agent** API key (`github-actions`, on
Daedalus). An agent identity writing to an *existing* issue must attribute the
write to a heartbeat run, and a CI job has no run. Two refusals of that one
rule are reachable, both observed live on dispatch drills:

- the alert is assigned to the key's own agent, whose run checks it out within
  milliseconds of the create, so the write demands that run's id —
  `401 {"error":"Agent run id required"}`, which reads like a rejected token
  and is really a checkout precondition;
- assign it to a different agent and that opens, but every agent field update
  is counted against a per-run cross-issue cap with no run to count against —
  `403 cross_issue_influence_run_context_required`.

There is no agent-key path past the second. It applies to the status flip *and*
to the `Still red.` comment, so the pager's only remaining issue write is
another `create` — and a wider credential was declined: a board API key is
instance-admin authority over the whole tracker, handed to a public
repository's CI, to close one issue CI opened itself.

So the pager stops writing to the alert at all. On both paths it instead
**wakes the alert's assignee**, who performs the write in-ticket from a run of
their own — the one write shape that works here unconditionally.
`POST /api/agents/{id}/wakeup` takes this key for its own agent, needs no run
id, and is not an issue write, so the credential keeps create-only authority
over issues. The wake carries `payload.issueId`, which is what binds the woken
run to the alert: `enrichWakeContextSnapshot` copies it into the run's
`contextSnapshot.issueId`, and the cross-issue limiter resolves a run's source
issue from that same field — so when the run writes to the alert, target equals
source and the write is in-ticket, not cross-issue. A wake-born run and an
assignment-born run are indistinguishable in the field that decides authority.
See [ADR-0021](../decisions/0021-recovery-wakes-the-alerts-assignee.md) for the
options and the rule it sets.

| Probe tick | Alert state | What the pager does |
|---|---|---|
| red | none open | **creates** the alert, assigned to `PC_ASSIGNEE_AGENT_ID` |
| red | one open, created within `PC_ALERT_MAX_AGE_H` (default 6 hours) | **wakes** its assignee — "still red, add this probe output" — unless `PC_STILL_RED_WAKE=0`, and then nothing at all |
| red | one open, older than that window | **creates** a fresh alert and does not touch the stale one |
| green | one open, of any age | **wakes** its assignee — "green again, close this alert" |
| green | none open | nothing at all: no call, no wake |

### Dedup is time-bounded

An open alert older than `PC_ALERT_MAX_AGE_H` hours is not a dedup target.
Everything that closes an alert is a write that can fail, and a failed close
leaves the alert open forever; the next red tick would fold into it and wake
nobody. The window is the backstop that cannot be refused, because it performs
no write: the stale alert is not closed, not commented on, and not patched.
`raise` opens a new issue, assigned as usual, which is the wake, and logs one
line naming both alerts, so the tick log (or the Actions run) shows why a
second one exists.

A creation timestamp that is missing or not a timestamp is treated as inside
the window. A parse failure must not mint a duplicate alert.
`PC_ALERT_MAX_AGE_H=0` disables the window, so a drill always dedups. The
window applies only to that raise decision. A green tick still wakes the
assignee of whatever alert is open, however old it is.

The tracker returns a recent open issue with the same title instead of
creating another, unless the create asks not to. The fresh alert sets that
opt-out. If the response is still the stale issue, the pager fails the call
rather than reporting an alert nobody was assigned.

### Pager environment

| Variable | Default | Role |
|---|---|---|
| `PC_API_URL` | required | Paperclip API base |
| `PC_API_TOKEN` | required | Agent API key. Create-only on issues |
| `PC_COMPANY_ID` | required | Company the alert is opened in |
| `PC_ASSIGNEE_AGENT_ID` | Daedalus | Assignee of a new alert. The assignment is the wake |
| `PC_ORIGIN_ID` | `cotel-health-probe` | Dedup marker. `probe-ingest` sets `cotel-ingest-edge` on a schedule and `cotel-ingest-edge-drill` on a dispatch; the other two jobs pin `cotel-health-probe-drill{,-edge}`. Nothing uses the default |
| `PC_ALERT_MAX_AGE_H` | `6` | Hours an open alert stays a dedup target on a red tick. `0` always dedups. `probe-ingest` sets `0` |
| `PC_STILL_RED_WAKE` | `1` | Whether a red tick wakes the assignee of an already-open alert. `0` silences that wake and exits 0. `probe-ingest` sets `0` |
| `PC_RUN_ID` | unset | Sent as `X-Paperclip-Run-Id` when set |
| `CF_ACCESS_CLIENT_ID`, `CF_ACCESS_CLIENT_SECRET` | unset | Same pair as issue-sync. **In practice mandatory**: Cloudflare Access fronts the Paperclip API, and without them every call answers 302 and the pager reports `issue search: HTTP 302 … did not return JSON` |
| `GITHUB_RUN_URL` | unset | Attached to the alert and the wake reason when set |
| `GITHUB_EVENT_NAME` | unset | Quoted into a new alert's body when set |
| `GITHUB_ACTOR` | unset | Quoted into a new alert's body when set |
| `HEALTHZ_URL` | unset | Named in the alert so the assignee re-probes the same URL |
| `GITHUB_RUN_ID` | unset | Fallback for `PC_CALL_ID` |
| `PC_CALL_ID` | `GITHUB_RUN_ID`, then `manual` | Unique half of the recovery idempotency key. `probe-ingest` sends an hourly bucket (`ingest-<epoch/3600>`) rather than its run id: with the staleness window off an alert can stand for a long time, and a per-run key would make every green tick in the meantime mint another recovery wake |
| `PC_SOURCE_LINE` | names Actions | First paragraph of a new alert: who opened it. Override it if you are not the workflow |
| `PC_REPROBE_HINT` | names an Actions dispatch | How the woken agent re-probes. `probe-ingest` replaces it with `bash scripts/probe-edge-ingest.sh`, which runs from any machine with internet |
| `PC_ALERT_SUBJECT` | classified from the verdict: `prod database snapshot` on a snapshot failure, else `prod /healthz` | What is red, in the title and in every wake reason. `probe-ingest` sets `public ingest at <host>` |
| `PC_ALERT_LEAD` | classified the same way | First line of a new alert's description. `probe-ingest` says spans are being dropped, that this check never reached `/healthz`, and that the watcher is best-effort |

**`PC_ALERT_MAX_AGE_H=0` and `PC_STILL_RED_WAKE=0` are a pair, and they put the
whole burden on the recovery wake.** Together they give exactly one ticket per
outage and no further wakes while it stands, which is what the scheduled job is
asked for. The cost is that the open alert then dedups *every* later red tick
forever, so until it is closed the **next** outage wakes nobody. The recovery
wake is the only thing that re-arms the pager — which is why the green path is
not optional on a watcher that only has to detect failure, and why an alert
left standing is itself an outage of the watcher.

The subject and lead overrides exist because the dedup marker is machine-facing.
Without its own
subject, a second watcher mints an alert whose title and wake reason claim
production `/healthz` is red — pointing the reader at the wrong half of the
system. The same wrong title came out of the `/healthz` probe's own snapshot
verdict, which is why those two defaults are derived from the verdict rather than fixed;
see [the two halves are classified
apart](#the-two-halves-are-classified-apart-and-page-apart).

### The woken agent re-probes, and the alert tells it to

The caller's `payload` does not reach the woken agent. The server reads the
issue-scoping keys off it - `issueId`, `taskKey`, a `commentId` - and delivers
none of the rest, so the probe output and the green run URL are dropped on the
floor. They are persisted on the wake row and stay readable through
`diagnostics/wakes`, which is a forensic record, not a channel to the agent.
A wake is a doorbell, not an envelope.

The free-text `reason` is the one exception, and it is worth knowing. It is
carried verbatim into the woken run's context and arrives three ways: the
`PAPERCLIP_WAKE_REASON` environment variable, the `reason` field of
`PAPERCLIP_WAKE_PAYLOAD_JSON`, and a `- reason: ...` line in the wake summary
the adapter renders into the prompt. The bucketing to an enum that a drill sees
is done by the `diagnostics/wakes` response projection, which collapses an
unrecognised reason to `other`; it is not on the path to the agent. So the
doorbell does carry one line of caller-written text, which is why the pager's
`reason` names the recovery and the alert it belongs to rather than just asking
for attention.

Two things that line cannot do. It cannot say *who rang*: the wake's `source` is
not among the fields copied into the agent's wake payload, so `automation` shows
up in the diagnostics and nowhere the woken agent can read it. No delivered
field distinguishes a pager wake from a human one. And it does not survive
coalescing. The payload a run is handed is built once, when that run is
dispatched; a wake arriving afterwards is absorbed into that run without
reaching it, and the caller's sentence then surfaces only on the run's stored
context, after the fact. That is what the 2026-10-05 pair showed: the green
half's wake landed six seconds after a continuation-recovery run for the same
alert had started, so the assignee read a continuation label while the absorbed
wake row carried the pager's sentence naming the recovery. An agent who resumes
rather than re-probes has nothing telling it production just changed.

So `reason` is a second channel, not a substitute. It is delivered on the
ordinary production shape, where an alert standing for an hour has no live run,
and it is swallowed in exactly the case these drills keep landing in. The
protocol has to live in the description, which survives both.

Neither prober can put the recovery in the alert thread either: a comment on an
existing issue is the same refused write as the status flip. What they *do*
always have is the `create`, so **the alert's description carries the protocol**
— it tells the agent that this description always reads red, that the wake
carries no probe output, and how to check production before acting. Green means
close the issue citing that check; still red means add its output.

The instruction is a re-probe rather than a `curl` for two reasons. A bare
request only proves liveness, while the probe also classifies 503, stale ingest
and an empty database; and the probed URL may be a host's own loopback, which
nothing but that host can reach. The description names the URL that was probed
so the agent re-checks the same endpoint.

**Which re-probe depends on what paged.** The default text in
`page-cotel-health.sh` names a dispatch of the loopback half, which runs on the
host that may itself be the thing that is down and would then queue forever.
That is wrong for every caller, and the scheduled ingest job overrides it: its
alerts say to run `bash scripts/probe-edge-ingest.sh` from any machine with
internet, which needs no credential and no runner. Any new caller must override
it too — the defaults are not neutral.

That costs the woken agent one extra probe, by design. A live re-probe is better
evidence than a payload minted ten minutes earlier, and it needs no passthrough
the tracker does not offer. Every drill so far shows the assignee doing exactly
this by hand before closing; the description is what stops it being folklore.

Three more details matter in operation:

- **`payload.issueId` must be present and correct.** Without it the wake is
  accepted but bound to no ticket — it will not even appear in the alert's wake
  diagnostics — and the woken agent has no idea why it is awake. Naming the
  alert under any other key (`alertIssueId`, say) is the same as omitting it.
- **`202 {"status":"skipped"}` is success**, and so is coalescing. A run already
  live for that agent and ticket reads current state anyway — which is the state
  the wake was going to report. A started wake answers with the run object, so
  `status: "skipped"` is what distinguishes the two, and
  `GET /api/issues/<id>/diagnostics/wakes` shows a coalesced wake sharing the
  live run's id. Correct in production, where red and green are at least a
  tick apart;
  **in a drill it is a trap** — see [Running a drill](#running-a-drill).

  The "and ticket" is what makes this safe, and it is a property of the
  tracker, not an assumption: admission is decided against **that issue's**
  execution lock, so a wake is only absorbed by a run that already holds the
  alert. A run live for the same agent on some *other* ticket does not swallow
  the recovery — the alert has no lock holder, and the wake proceeds as a run
  of its own. Daedalus being busy elsewhere therefore cannot lose a recovery.
- **Idempotency keys differ by path.** Recovery uses
  `cotel-health-recovery:<alert id>:<PC_CALL_ID>`, where `PC_CALL_ID` defaults
  to `GITHUB_RUN_ID`, so a re-dispatched or retried job cannot mint a second
  heartbeat. A caller that ticks repeatedly while an alert stands wants neither
  extreme: the same key forever means one wake per alert, and a dropped wake
  then leaves a resolved alert standing, while a per-run key means a wake per
  tick. So the scheduled job passes an hourly bucket
  (`ingest-<epoch/3600>`). The still-red wake buckets on a 6h window —
  `cotel-health-still-red:<alert id>:<epoch/21600>` — for the callers that
  leave it on; this one does not.
- **An alert assigned to anyone else is a pager failure, by name.** An agent
  API key may wake only its own agent, so the pager reports
  `alert wake: HTTP 403, alert <IDENT> is assigned to an agent this credential
  cannot wake` rather than stepping over it. In normal operation this cannot
  happen — the pager assigns the alert itself — but a reassigned alert must not
  fail silently.

Recovery latency is therefore one heartbeat rather than zero: the alert closes
when its assignee next wakes, not the instant the probe turns green. That is
deliberate — the alternative that closes it in seconds costs an instance-admin
credential in CI. If the assignee is paused the wake is declined and the alert
stands; that needs the roster, not the pager.

Every pager failure line names the call that produced it (`issue search`,
`issue create`, `alert wake`), because these calls share status codes and a
bare `HTTP 403` is not debuggable.

**A paging failure is never the probe's verdict.** A workflow job's red or
green means "production is red or green" and nothing else; if the pager cannot
reach Paperclip, the run carries a `Pager failed` annotation saying nobody was
woken. Which means the job's colour is not the verdict on a *drill* either: a
drill is a test of whether the page arrived, so read the pager's line in the
step log, not the red X that the dead URL was always going to produce.

The loopback job also checks for `jq` and `python3` on every run — that runner
is a developer machine, not a managed image, so a missing interpreter surfaces
in a drill rather than during an incident.

Paperclip budget is spent only on a state change: the create on the first
confirmed red, a create when the open alert has aged out of the dedup window
(not on the scheduled job, which has that window off), and a wake on a
recovery. A green tick with no open alert spends nothing — it costs one search.
A scheduled Paperclip routine that fires regardless of health was a rejected
alternative on cost (~24 full agent runs a day); see
[ADR-0025](../decisions/0025-best-effort-ingest-watcher-in-actions.md).

### Credentials

Every job in the workflow uses `secrets.PAPERCLIP_API_TOKEN` — the agent API
key `github-actions` on Daedalus — and needs nothing more than it already has:
create an issue, and wake its own agent. An agent key cannot wake anyone else,
which is why an alert assigned elsewhere is a named pager failure rather than
something the pager steps over, and why the drill's page arrives on Daedalus.

Four secrets, not three. Alongside `PAPERCLIP_API_URL`,
`PAPERCLIP_API_TOKEN` and `PAPERCLIP_COMPANY_ID`, a paging job needs
`CF_ACCESS_CLIENT_ID` / `CF_ACCESS_CLIENT_SECRET`, because Cloudflare Access
fronts the Paperclip API. A job that sets only the first three probes and fails
and *looks* like it paged — the pager's failure is a `::warning`, the job is
already red from the probe, and the only sign is
`issue search: HTTP 302` in the step log. That is why a drill's verdict is
whether the page arrived, never the job's colour.

None of them had to be minted for this watcher: all four were already
repository secrets, and `probe-edge-ingest.sh` itself needs none at
all. A board API key — instance-admin authority over the whole tracker, handed
to a public repository's CI — was refused in
[ADR-0021](../decisions/0021-recovery-wakes-the-alerts-assignee.md), and that
refusal stands.

## Local use

```sh
# the deploy host, from the deploy host — no Access token involved
HEALTHZ_URL=http://127.0.0.1:8080/healthz scripts/probe-healthz.sh

# the same, demanding that this instance report a current snapshot
SNAPSHOT_CHECK=require HEALTHZ_URL=http://127.0.0.1:8080/healthz scripts/probe-healthz.sh

# through Cloudflare (needs an Access service token allowed on the app)
scripts/probe-healthz.sh

# the public ingest path, from anywhere, no credential at all
scripts/probe-edge-ingest.sh

# a closed local port — the failure demo, either probe
scripts/probe-healthz.sh http://127.0.0.1:1/healthz
scripts/probe-edge-ingest.sh http://127.0.0.1:1/v1/traces

# the classification tests, including 503 vs stale vs empty vs refused
bash scripts/probe-healthz_test.sh

# the ingest classification tests: the app's JSON 401 vs an interstitial 401,
# an Access redirect, a tunnel 5xx, an unrouted 404, anonymous ingest
bash scripts/probe-edge-ingest_test.sh

# the pager against a fake curl — dedup, the staleness window, the still-red
# wake and its off switch, per-call failure labels, caller overrides
bash scripts/page-cotel-health_test.sh

# whether the scheduled watcher has actually been ticking
gh run list --repo Flopsstuff/cotel --workflow=health-probe.yml --event=schedule --limit 5
```

`probe-healthz.sh` exit codes: `0` healthy, `1` unreachable, `2` HTTP non-200,
`3` stale/empty, `4` Access blocked, `6` no current database snapshot,
`7` observer cannot resolve name.

`probe-edge-ingest.sh` exit codes: `0` the ingest handler answered 401,
`1` unreachable, `2` answered but not with the handler's 401, `4` Access now
fronts the ingest host, `7` observer cannot resolve name.

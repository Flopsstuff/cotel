# Production health probes

A **systemd timer on the Pi** asks production two questions every 10 minutes and
pages a Paperclip agent once two consecutive ticks fail either of them:

- **the LAN half** — the **dashboard** `/healthz` on robmini, over the LAN;
- **the edge half** — the **public OTLP ingest URL**, over the internet.

They exist because a dead or silent cotel otherwise has no one looking: `Deploy`
only runs on a push to `main`, the Cloudflare tunnel stays green while the
process is 503 or not listening, and a red Actions run in this company does not
wake anyone on its own.

The scheduler used to be GitHub Actions `cron`, and that is the part that did
not work: see [Why the schedule is not in this repo](#why-the-schedule-is-not-in-this-repo)
and [ADR-0022](../decisions/0022-health-probe-scheduler-outside-github.md). The
workflow still exists, dispatch-only, for drills.

## The vantage points

Production is asked from several places, because each is blind to something the
others see. The first two columns are the scheduled halves of one tick.

| | `cotel-healthz.timer` LAN half | `cotel-healthz.timer` edge half | `probe-loopback` (dispatch) | `probe-edge` (dispatch) |
|---|---|---|---|---|
| Scheduled | **yes, every 10 min** | **yes, every 10 min** | no | no |
| Runner | the Pi, `~/ops/cotel-healthz.sh` | the Pi, same tick | `[self-hosted, flopsstuff, docker]` — the deploy host | `ubuntu-latest` |
| Target | `http://robmini.local:8080/healthz` (LAN) | `https://otlp.aignite.pl/v1/traces` (internet) | `http://127.0.0.1:8080/healthz` | `https://cotel.aignite.pl/healthz` |
| Cloudflare in the path | no | yes (DNS + tunnel) | no | yes (tunnel + Access) |
| Catches | process dead, crash loop, 503 DuckDB, stale/empty ingest, a snapshot worker that has stopped producing restore points, **and the host being off** | DNS, a blanked tunnel token, the ingest route not served, Access appearing in front of ingest | all of the LAN half except host-off | the dashboard host through Cloudflare |
| Blind to | the tunnel, DNS — a LAN-healthy cotel unreachable from the internet reads green | the database and ingest freshness: this check stops at the auth boundary | **its own host being down** — with the runner off the job queues, producing no colour at all | nothing in the path, but see the Access caveat below |
| Access service token | not needed | **not needed** | not needed | required |
| Alert dedup marker | `[cotel-health-probe]` | `[cotel-ingest-edge]` | `[cotel-health-probe-drill]` | `[cotel-health-probe-drill-edge]` |

The Pi timer is the authoritative path, and it is the only one that pages a
production marker. Probing over the LAN rather than from the deploy host closes
the gap that the self-hosted job cannot: a job on that host cannot report the
host being off, because GitHub queues the run rather than failing it, and
`timeout-minutes` does **not** bound queue time — it starts counting once a
runner picks the job up. An absent host yields no colour at all, and the only
trace is a run sitting in `queued`, which is also what a busy runner looks like.
From the Pi, an absent host is a refused connection or a timeout, which is a
verdict.

Every prober uses a **different dedup marker** on purpose. On a single marker
they would fight: a green tick on one would ask for the close of the alert
another had just raised. Dispatched runs are separated from the Pi timer for the
stronger version of the same reason — a drill must not touch a real alert; see
[Running a drill](#running-a-drill).

## What the LAN half checks

`scripts/probe-healthz.sh` classifies failure modes with distinct text, because
they send the on-call looking in different places:

| Probe verdict | Exit | Typical cause |
|---|---|---|
| `unreachable` | 1 | Process not listening, host down, or the tunnel is not forwarding |
| `HTTP 503 database unreadable` | 2 | Process is up; DuckDB is not |
| `HTTP <other>` | 2 | Crash loop, proxy error, unexpected handler |
| `ingest stale` / `empty database` | 3 | Process is up and the DB reads; spans are not being accepted |
| `cloudflare access blocked` | 4 | Probe config, **not** an outage — see below |
| `no current database snapshot` | 6 | Everything above is fine; the backup is not happening — see below |

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
path), so the host wrapper configures one URL and a change here needs no edit
on the Pi. `API_HEALTH_URL` overrides the derivation.

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

Exit **6** and not 5 because `~/ops/cotel-healthz.sh` already spends 5 on "the
LAN half is green and the public ingest half is not".

This verdict pages under the LAN half's marker, but not in its words: the alert
is titled `cotel prod database snapshot is red` and leads with "the application
is alive; its backup is not". See [the two halves are classified
apart](#the-two-halves-are-classified-apart-and-page-apart).

Two vantage points this must not be turned on for: anything reaching cotel
through `cotel.aignite.pl`, where Cloudflare Access answers instead of the
application (the default `auto` degrades quietly there, `require` would page on
a probe-configuration problem), and the edge half, which never reads
`/healthz` at all.

| Variable | Default | Role |
|---|---|---|
| `SNAPSHOT_CHECK` | `auto` | `auto` pages on affirmative failure only; `require` also pages when the instance makes no snapshot claim; `off` never asks |
| `SNAPSHOT_STALE_AFTER_SECONDS` | `43200` | How old the newest snapshot may be. Two `COTEL_SNAPSHOT_INTERVAL` periods at the shipped `6h`; move it with the interval |
| `API_HEALTH_URL` | derived from the `/healthz` URL | Override when the snapshot report is not at the same entry point |

What counts as red, why the threshold is two intervals, and how to check it by
hand are in
[Database Snapshots and Restore](./duckdb-snapshots#who-is-watching-it).

## What the edge half checks, and why it needs no Access token

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
| `public ingest unreachable` | 1 | DNS gone, connection refused, timeout — a blanked tunnel token looks like this |
| `cloudflare reached, the origin did not answer` | 2 | 502/530 and friends: the tunnel is up, the origin is not |
| `/v1/traces is not routed` | 2 | 404 — wrong tunnel target, or the ingest port is not served |
| `accepting unauthenticated spans` | 2 | 200 to an invalid token: reachable, but the auth policy is open |
| `401 but not from the ingest handler` | 2 | a 401 whose body is not the application's JSON — an interstitial, not cotel |
| `cloudflare access now fronts the ingest host` | 4 | Access was enabled on this host, which rejects every agent's spans too |

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

This half is blind to what the LAN half sees: it stops at the auth boundary and
says nothing about the database or ingest freshness. That is why both run.

### The two halves are classified apart, and page apart

"The process is dead" and "the process is fine, the public path is down" need
different people doing different things, so they never share an alert:

- **separate dedup markers** — `[cotel-health-probe]` and `[cotel-ingest-edge]`.
  On one marker the halves would fight, a green tick of one asking for the close
  of the alert the other just raised;
- **separate failure streaks** — `reports/cotel-healthz/state` and
  `state-edge`, so this host's uplink blinking cannot page for a healthy
  application and vice versa;
- **different words** — the edge alert's title, first line and wake reason say
  that the application is alive and its public ingest path is not, and tell the
  reader to look at the tunnel and DNS rather than restart the container. The
  pager takes those from `PC_ALERT_SUBJECT` and `PC_ALERT_LEAD`.

The LAN half needs the same split **inside** its one marker, because it reddens
on two unrelated things: a `/healthz` that does not answer, and a `/healthz`
that answers while the backup has stopped. So when the caller passes no
`PC_ALERT_SUBJECT`, the pager classifies the subject from the probe verdict:

| Verdict | Title | Lead says |
|---|---|---|
| `no current database snapshot`, or `snapshots are required …` | `cotel prod database snapshot is red [cotel-health-probe]` | the application is alive, the backup is not; read the `snapshot` object in `/api/v1/health` and the worker's logs; do **not** restart cotel |
| anything else | `cotel prod /healthz is red [cotel-health-probe]` | the `/healthz` probe is red |

An explicit `PC_ALERT_SUBJECT`/`PC_ALERT_LEAD` always wins over the
classification — that is how the edge half keeps its own words.

**The marker stays out of it.** Both subjects share one alert slot, so two red
ticks of this half — in either order, snapshot then `/healthz` or the reverse —
still dedup into one ticket. Were the subject part of the dedup key, one
watcher's consecutive reds would mint an alert each.

One asymmetry on purpose: **while the LAN half is red, the edge half does not
page.** A dead process makes the public path unreachable as a consequence, and a
second alert would send its reader hunting Cloudflare for a dead container. The
edge streak keeps counting through the suppression, so the moment the process
comes back and the public path does not, that half pages on the next tick. The
tick log names the suppression explicitly:

```
[…] edge RED (streak 3), not paging: the LAN /healthz half is red too, and its alert covers this — probe-edge-ingest: FAILED — …
```

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
401 proves DNS, tunnel, process and route, which is exactly what the LAN half
cannot see. The scheduled edge half above uses it and asks for no credential.
What an Access-allowed token would add is narrower: the *dashboard* host's view
through Cloudflare, and the database and freshness fields that only `/healthz`
carries. Worth having, not worth blocking on.

## Schedule

Every **10 minutes**, from `cotel-healthz.timer` on the Pi
(`OnCalendar=*:0/10`, `Persistent=true`). Both halves run in one tick, each
paging on its own **second consecutive** failure, so the worst-case detection
delay is about 20 minutes and a single blinked tick wakes nobody. The 6h
ingest-age threshold is independent of the poll interval: a quiet night is not
an alert.

The timer, its unit files and its runbook live in `~/ops` on the Pi
(`~/ops/README.md`, section *cotel health probe*) — not in this repo, because a
scheduler inside the repo is exactly what did not work. The scripts it runs
**are** this repo's: each tick materializes `scripts/probe-healthz.sh`,
`scripts/probe-edge-ingest.sh` and `scripts/page-cotel-health.sh` from
`origin/main` with `git show`, so a fix here reaches the timer with no sync step
and the timer does not care which branch the shared checkout has yanked.

One consequence of materializing by ref: a probe script that is **not at that
ref** is reported as a half that is not deployed, on every tick and in
`--status`, and does not fail the unit. The LAN half must not go dark because
the other half has not landed — and the half starts running by itself on the
tick after the script reaches `origin/main`, with no action on the host.

```sh
# on the Pi
~/ops/cotel-healthz.sh --status                 # both streaks, credential, next elapse, last 20 ticks
~/ops/cotel-healthz.sh --probe-only             # probe both halves now, page nothing, touch no state
~/ops/cotel-healthz.sh --half edge --probe-only # the public ingest half alone
~/ops/cotel-healthz.sh --half edge              # one real tick of that half alone
systemctl --user list-timers cotel-healthz.timer
journalctl --user -u cotel-healthz.service -n 50
```

`--probe-only` exits with the LAN probe's own code when that half is red
(1 unreachable, 2 HTTP, 3 stale/empty), **5** when the LAN half is green and the
public ingest half is not, and 0 when both are green. The sentinel is not the
edge probe's own code on purpose: 1 and 2 would read as the LAN half's
"unreachable" and "HTTP", which send the reader to the host instead of to
Cloudflare.

A tick exits non-zero **only when the watcher itself is broken** (no
credential, the scripts cannot be materialized, the pager cannot reach
Paperclip). Production being red is a successful tick — it pages Paperclip. That
split is what makes `OnFailure=ops-alert@cotel-healthz.service.service` mean
"nobody is watching" rather than "cotel is down": the first needs the owner at
this machine, the second needs an agent.

### Why the schedule is not in this repo

`cron: "17 * * * *"` was merged to `main` at 00:44Z on 2026-10-05 and had
produced **one** run by 09:46Z — one tick of an expected nine. `gh run list
--workflow=health-probe.yml --limit 200` showed exactly one `event=schedule` run
in the workflow's entire history; it was not a cancellation (those stay in the
list as `cancelled`) and not the 60-day public-repo deactivation (the repo was
active that day). GitHub schedules public repositories on a best-effort basis
and **drops** ticks rather than delaying them, so the detection delay the cron
bought was not "an hour" but unbounded — a weaker version of the six-day silence
this probe was built to end.

`systemd` does not drop ticks, and `Persistent=true` makes up a tick missed
across a reboot. A scheduled Paperclip routine was the other candidate and was
rejected on cost: ~24 full agent runs a day to do what `curl` does. See
[ADR-0022](../decisions/0022-health-probe-scheduler-outside-github.md).

### Dispatching the workflow

Actions → **Health probe** → **Run workflow**. Each half has its own optional
URL override (`url` for the edge, `loopback_url` for the host) for demonstrating
a red run against a dead endpoint; leave **page** unchecked unless you intend to
open a Paperclip alert — see [Running a drill](#running-a-drill) for what a
dispatch with **page** checked does.

A dispatch is the only way the workflow's probe jobs run now. The `test` job
still runs on every pull request and on pushes that touch the probe scripts, so
the classification and pager tests remain a merge gate.

## Running a drill

Every rehearsal pages a **drill** marker, never a production one. Only the Pi
timer uses `cotel-health-probe` and `cotel-ingest-edge`; the workflow's jobs pin
`cotel-health-probe-drill` and `cotel-health-probe-drill-edge` unconditionally,
and a drill from the Pi has to pass the drill marker itself:

```sh
# on the Pi — a red pair against a closed port, on the drill marker
PC_ORIGIN_ID=cotel-health-probe-drill COTEL_HEALTHZ_URL=http://127.0.0.1:9/healthz \
  ~/ops/cotel-healthz.sh        # first tick: streak 1, pages nobody
PC_ORIGIN_ID=cotel-health-probe-drill COTEL_HEALTHZ_URL=http://127.0.0.1:9/healthz \
  ~/ops/cotel-healthz.sh        # second tick: raises the drill alert
PC_ORIGIN_ID=cotel-health-probe-drill ~/ops/cotel-healthz.sh   # green: routes the close
```

The edge half drills the same way, with its own marker variable and `--half
edge` so the rehearsal does not also run a real LAN tick:

```sh
# on the Pi — a red pair against a closed port, on the ingest drill marker
PC_EDGE_ORIGIN_ID=cotel-ingest-edge-drill COTEL_INGEST_URL=http://127.0.0.1:9/v1/traces \
  ~/ops/cotel-healthz.sh --half edge      # first tick: streak 1, pages nobody
PC_EDGE_ORIGIN_ID=cotel-ingest-edge-drill COTEL_INGEST_URL=http://127.0.0.1:9/v1/traces \
  ~/ops/cotel-healthz.sh --half edge      # second tick: raises the drill alert
PC_EDGE_ORIGIN_ID=cotel-ingest-edge-drill ~/ops/cotel-healthz.sh --half edge   # green: routes the close
```

Two things to put back afterwards: the failure streak in
`~/ops/reports/cotel-healthz/state` (or `state-edge`) is shared with production
(a green tick resets it to 0, which the third command above does), and the drill
alert is a real issue someone has to close.

Dedup is per marker, so the sets never see each other: a red drill cannot
attach itself to a standing real alert, and — the half that actually bites — a
green drill cannot clear or file recovery against one nobody has read yet.

Every alert body names the triggering event, the actor and the effective probe
URL, so a woken reader can tell an exercise from an outage without opening
Actions — the probe text cannot say, since a closed-port drill and a dead
process produce the same line.

To exercise the pager through GitHub instead, dispatch twice with **page**
checked: once with `loopback_url=http://127.0.0.1:9` (a closed port — raises the
alert, whose assignment wakes its assignee), then once with the default (green —
wakes that assignee again, who closes the alert from their own run). Never break
production to get a red run.

**Leave a gap between the two halves.** Wait for the alert's *assignment* run to
finish before dispatching the green half. Back to back, the recovery wake is
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
green half you never ran, or whose recovery wake was coalesced, leaves the
alert standing. Check that the issues your dispatch minted are closed when the
drill is over, and say in the close that they were drill artifacts.

## Who is woken, and how

A red GitHub Actions run is **not** the page, and neither is a non-zero exit of
the Pi timer. Notifications on the Fl0p account are unproven (no
`notifications` API scope, no public mailbox, agent identities are not GitHub
users), this company has already watched red Actions sit unnoticed, and
`~/ops/ALERT` plus a desktop `notify-send` reach whoever is at the Pi — which
is the owner, not an agent.

On red, the prober opens a Paperclip issue titled `cotel <subject> is red
[<marker>]` — `prod /healthz` by default, `public ingest at <url>` for the edge
half — assigned to Daedalus. That assignment
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
| red | one open, created within `PC_ALERT_MAX_AGE_H` (default 6 hours) | **wakes** its assignee — "still red, add this probe output" |
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
| `PC_ORIGIN_ID` | `cotel-health-probe` | Dedup marker. The Pi timer keeps the default; both workflow jobs pin `cotel-health-probe-drill{,-edge}` |
| `PC_ALERT_MAX_AGE_H` | `6` | Hours an open alert stays a dedup target on a red tick. `0` always dedups |
| `PC_RUN_ID` | unset | Sent as `X-Paperclip-Run-Id` when set |
| `CF_ACCESS_CLIENT_ID`, `CF_ACCESS_CLIENT_SECRET` | unset | Same pair as issue-sync, when the API is behind Access |
| `GITHUB_RUN_URL` | unset | Attached to the alert and the wake reason when set |
| `GITHUB_EVENT_NAME` | unset | Quoted into a new alert's body when set |
| `GITHUB_ACTOR` | unset | Quoted into a new alert's body when set |
| `HEALTHZ_URL` | unset | Named in the alert so the assignee re-probes the same URL |
| `GITHUB_RUN_ID` | unset | Fallback for `PC_CALL_ID` |
| `PC_CALL_ID` | `GITHUB_RUN_ID`, then `manual` | Unique half of the recovery idempotency key. A caller with no run id should pass a coarse time bucket; the Pi timer sends `pi-<epoch/21600>` |
| `PC_SOURCE_LINE` | names Actions | First paragraph of a new alert: who opened it. Override it if you are not the workflow |
| `PC_REPROBE_HINT` | names an Actions dispatch | How the woken agent re-probes. The Pi timer replaces it with `~/ops/cotel-healthz.sh --probe-only` |
| `PC_ALERT_SUBJECT` | classified from the verdict: `prod database snapshot` on a snapshot failure, else `prod /healthz` | What is red, in the title and in every wake reason. The edge half sets `public ingest at <url>` |
| `PC_ALERT_LEAD` | classified the same way | First line of a new alert's description. The edge half says the application is alive and the public path is not |

The last two exist because the dedup marker is machine-facing. Without its own
subject, a second watcher mints an alert whose title and wake reason claim
production `/healthz` is red — pointing the reader at the wrong half of the
system. The same wrong title came out of the LAN half's own snapshot verdict,
which is why those two defaults are derived from the verdict rather than fixed;
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

**Which re-probe depends on who paged.** The default text in
`page-cotel-health.sh` names the GitHub dispatch, which is right for a drill
and wrong for the Pi timer: a dispatch of the loopback half runs on the host
that may itself be the thing that is down, and would queue forever. So the
timer overrides two strings, `PC_SOURCE_LINE` and `PC_REPROBE_HINT`, and its
alerts tell the agent to run `~/ops/cotel-healthz.sh --probe-only` on the Pi
instead. Any future caller that is not GitHub Actions must do the same — the
defaults are the workflow's, not neutral.

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
  heartbeat. The Pi timer has no run id and would otherwise send the same key
  forever — one wake per alert, with a dropped wake leaving a resolved alert
  standing — so it passes a 6h bucket (`pi-<epoch/21600>`) instead. The
  still-red wake buckets on the same window —
  `cotel-health-still-red:<alert id>:<epoch/21600>` — so a multi-day outage
  spends about four heartbeats a day rather than one per tick.
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
woken. The Pi timer splits it the other way round, for the same reason: a red
probe is a *successful* tick, and the only thing that fails the unit is the
watcher being broken — no credential, scripts unavailable, or the pager
erroring. That failure raises `~/ops/ALERT` and a desktop notification through
`OnFailure=ops-alert@`, which is the right audience for "nobody is watching".
The loopback job also checks for `jq` and `python3` on every run — that runner
is a developer machine, not a managed image, so a missing interpreter surfaces
in a drill rather than during an incident.

Paperclip budget is spent only on a state change: the create on the first red
tick past the streak threshold, a create when the open alert has aged out of
the dedup window, and a wake on a further red inside the window or on a
recovery. A green tick with no open alert spends nothing — it costs one
loopback search against the local instance. A scheduled Paperclip routine that
fires regardless of health was the rejected alternative (~24 full agent runs a
day); see [ADR-0022](../decisions/0022-health-probe-scheduler-outside-github.md).

### Credentials

| Prober | Credential | Authority it needs |
|---|---|---|
| Pi timer | `~/.secrets/paperclip-board-ops.token` (board key `ops: paperclip-deploy health`, already on the box for the Paperclip updater) | create an issue, wake the assignee |
| workflow (both jobs) | `secrets.PAPERCLIP_API_TOKEN` — agent API key `github-actions` on Daedalus | the same, and nothing more: an agent key cannot wake anyone else |

The board key is wider than this needs. A dedicated agent key would be the
narrow fit, but minting one is board-only — an agent key gets
`Board access required` on `/api/agents/{id}/keys` — and the key is used here on
the owner's own machine, not handed to a public repository's CI, which is the
case that was refused in [ADR-0021](../decisions/0021-recovery-wakes-the-alerts-assignee.md).
To narrow it later, drop an agent key at
`~/.secrets/cotel-health-pager.token`: `cotel-healthz.sh` prefers that path and
needs no other change.

## Local use

```sh
# on the Pi — the authoritative prober, both halves
~/ops/cotel-healthz.sh --probe-only
~/ops/cotel-healthz.sh --status

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

# the pager against a fake curl — dedup, the staleness window, per-call failure
# labels, and the non-GitHub caller's overrides
bash scripts/page-cotel-health_test.sh
```

`probe-healthz.sh` exit codes: `0` healthy, `1` unreachable, `2` HTTP non-200,
`3` stale/empty, `4` Access blocked, `6` no current database snapshot.

`probe-edge-ingest.sh` exit codes: `0` the ingest handler answered 401,
`1` unreachable, `2` answered but not with the handler's 401, `4` Access now
fronts the ingest host.

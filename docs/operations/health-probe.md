# Production /healthz probe

An hourly GitHub Actions workflow asks the **dashboard** `/healthz` on the
production instance and pages a Paperclip agent when the answer is not healthy.
It exists because a dead or silent cotel otherwise has no one looking: `Deploy`
only runs on a push to `main`, the Cloudflare tunnel stays green while the
process is 503 or not listening, and a red Actions run in this company does not
wake anyone on its own.

## Two halves, neither sufficient alone

The workflow probes the same endpoint from two places, because the two vantage
points are blind to different things.

| | `probe-loopback` | `probe-edge` |
|---|---|---|
| Runner | `[self-hosted, flopsstuff, docker]` — the deploy host | `ubuntu-latest` |
| Target | `http://127.0.0.1:8080/healthz` | `https://cotel.aignite.pl/healthz` |
| Cloudflare in the path | no | yes (tunnel + Access) |
| Catches | process dead, crash loop, 503 DuckDB, stale/empty ingest | all of that, **plus** host down, tunnel down, DNS, Access misconfigured |
| Blind to | **its own host being down** — with the runner off the job queues, producing no colour at all | nothing in the path, but see the Access caveat below |
| Access service token | not needed | required |
| Alert dedup marker (scheduled) | `[cotel-health-probe]` | `[cotel-health-probe-edge]` |
| Alert dedup marker (dispatched) | `[cotel-health-probe-drill]` | `[cotel-health-probe-drill-edge]` |

The loopback half is the one that would have caught the 2026-09-28 incident:
the container was crash-looping and `/healthz` refused the connection on
localhost. It needs no secret and no Cloudflare, which is why it is the half
that works today.

The edge half is not a luxury. A self-hosted job cannot report that its own
host is down — if the runner is off or offline, GitHub queues the run rather
than failing it, and `timeout-minutes` does **not** bound queue time: it starts
counting once a runner picks the job up. So an absent host yields no colour at
all. Nothing goes red, nobody is paged, and the only trace is a run sitting in
`queued`, which is also what a busy runner looks like. `timeout-minutes: 5`
bounds a different case — a job that is running but stuck, instead of hanging
for the 6h default — and the job-level `concurrency` group with
`cancel-in-progress` keeps each hour superseding the last queued attempt rather
than stacking a day's worth of them. Only a probe from outside the host can
tell "host down" from "no signal", and only the edge half sees the tunnel, DNS
and Access at all.

The two halves use **different dedup markers** on purpose. On a single marker
they would fight: a green loopback hour would ask for the close of the alert
the edge half had just raised. A dispatched run is separated from the schedule
the same way — see [Running a drill](#running-a-drill).

## What the probe checks

The probe classifies failure modes with distinct text, because they send the
on-call looking in different places:

| Probe verdict | Exit | Typical cause |
|---|---|---|
| `unreachable` | 1 | Process not listening, host down, or the tunnel is not forwarding |
| `HTTP 503 database unreadable` | 2 | Process is up; DuckDB is not |
| `HTTP <other>` | 2 | Crash loop, proxy error, unexpected handler |
| `ingest stale` / `empty database` | 3 | Process is up and the DB reads; spans are not being accepted |
| `cloudflare access blocked` | 4 | Probe config, **not** an outage — see below |

A 200 with `ok: true` is not enough. Staleness is a body field
(`newest_span_age_seconds`); the endpoint keeps 200 on a quiet instance so the
container HEALTHCHECK does not flap. The probe applies its own threshold:
**6 hours**. A present JSON `null` is an empty database (never ingested), which
is not the same as `0` (ingested just now) and not the same as a missing key.

If `last_ingest_at` / `newest_span_age_seconds` are absent, the probe degrades
to liveness (HTTP 200) so it keeps working before that contract is on
production.

### The Access caveat (why the edge half warns instead of failing)

Cloudflare Access sits in front of the dashboard. The workflow sends the same
Access service-token headers as `paperclip-issue-sync.yml`, but that token is
**not currently allowed on the `cotel.aignite.pl` application**: the request
comes back as a 302 to the Access login, exactly as it does with no token at
all.

So `probe-edge` reports exit 4 as a **warning, and the job stays green**. That
is deliberate. Failing on exit 4 would paint the workflow red every hour until
the token is allowed, and a probe that is always red is one nobody reads —
worse than no probe, because it also buries a genuine red. Exit 4 never pages
either: it says nothing about whether cotel is up.

The price of that choice: while the token is unauthorized, **the edge half is
not watching anything**, and the only thing saying so is a warning annotation
on an otherwise-green run. To switch it on, allow the service token on the
`cotel.aignite.pl` Access application (or bypass `/healthz` only). No code
change is needed — the half starts observing on the next scheduled hour.

## Schedule

`17 * * * *` UTC, hourly. Six silent days was too long; catching an outage the
same day is the bar. The 6h ingest-age threshold is independent of the poll
interval: a quiet night is not an alert.

Manual run: Actions → **Health probe** → **Run workflow**. Each half has its
own optional URL override (`url` for the edge, `loopback_url` for the host) for
demonstrating a red run against a dead endpoint; leave **page** unchecked
unless you intend to open a Paperclip alert — see
[Running a drill](#running-a-drill) for what a dispatch with **page** checked
does.

The schedule runs only from the default branch. On a public repository GitHub
disables scheduled workflows after 60 days with no repository activity. The
notice for that goes to GitHub notifications, which do not wake anyone here —
the same silence this probe exists to close. A push is repository activity and
resets that 60-day clock. If the repository sits idle long enough for GitHub
to disable the schedule, this probe goes quiet with it.

## Running a drill

A dispatched run pages a **drill** marker, never the production one. The
workflow derives `PC_ORIGIN_ID` from `github.event_name`:

| Event | `probe-loopback` | `probe-edge` |
|---|---|---|
| `schedule` | `cotel-health-probe` | `cotel-health-probe-edge` |
| `workflow_dispatch` | `cotel-health-probe-drill` | `cotel-health-probe-drill-edge` |

Dedup is per marker, so the two sets never see each other: a red drill cannot
attach itself to a standing real alert, and — the half that actually bites — a
green drill cannot clear or file recovery against one nobody has read yet.

Every alert body names the triggering event, the actor and the effective probe
URL, so a woken reader can tell an exercise from an outage without opening
Actions — the probe text cannot say, since a closed-port drill and a dead
process produce the same line.

To exercise the pager end to end, dispatch twice with **page** checked: once
with `loopback_url=http://127.0.0.1:9` (a closed port — raises the alert, whose
assignment wakes its assignee), then once with the default (green — wakes that
assignee again, who closes the alert from their own run). Never break
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

A red GitHub Actions run is **not** the page. Notifications on the Fl0p
account are unproven (no `notifications` API scope, no public mailbox, agent
identities are not GitHub users), and this company has already watched red
Actions sit unnoticed.

On red, the workflow opens a Paperclip issue titled
`cotel prod /healthz is red [<marker>]`, assigned to Daedalus. That assignment
is the wake, and it is the only tracker issue the pager ever creates. A later
hour searches `q=<marker>` and acts on the open issue whose **title** contains
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

| Probe hour | Alert state | What the pager does |
|---|---|---|
| red | none open | **creates** the alert, assigned to `PC_ASSIGNEE_AGENT_ID` |
| red | one open | **wakes** its assignee — "still red, add this probe output" |
| green | one open | **wakes** its assignee — "green again, close this alert" |
| green | none open | nothing at all: no call, no wake |

### The woken agent re-probes, and the alert tells it to

Nothing in the wake except `payload.issueId` reaches the woken agent. The
adapter is handed `context.paperclipWake` — the server-built reason, thread and
objective — not the caller's `payload`, so the probe output and the green run
URL are dropped on the floor, and the free-text `reason` is bucketed to an enum.
A wake is a doorbell, not an envelope.

It does not even announce itself as the pager's. The recovery wake reaches the
assignee labelled as a continuation of their own prior run, with no trigger and
no recovery marker, so an agent who resumes rather than re-probes has nothing
telling it production just changed. That is why the protocol has to live in the
description, where every later reader finds it.

CI cannot put the recovery in the alert thread either: a comment on an existing
issue is the same refused write as the status flip. What CI *does* always have
is the `create`, so **the alert's description carries the protocol** — it tells
the agent that this description always reads red, that the wake carries no probe
output, and how to check production before acting: dispatch **Health probe**
with `page` unchecked and read its verdict. Green means close the issue citing
that run; still red means add its output.

The instruction is a dispatch rather than a `curl` for two reasons. A bare
request only proves liveness, while the probe also classifies 503, stale ingest
and an empty database; and for the loopback half the URL is the *deploy host's*
`127.0.0.1`, which nothing but that runner can reach. The description names the
probed URL so the agent re-checks the same endpoint, and warns against passing a
`loopback_url` override while checking a real alert.

That costs the woken agent one extra probe, by design. A live re-probe is better
evidence than a payload minted an hour earlier, and it needs no passthrough the
tracker does not offer. Both dispatch drills show the assignee doing exactly
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
  live run's id. Correct in production, where red and green are an hour apart;
  **in a drill it is a trap** — see [Running a drill](#running-a-drill).

  The "and ticket" is what makes this safe, and it is a property of the
  tracker, not an assumption: admission is decided against **that issue's**
  execution lock, so a wake is only absorbed by a run that already holds the
  alert. A run live for the same agent on some *other* ticket does not swallow
  the recovery — the alert has no lock holder, and the wake proceeds as a run
  of its own. Daedalus being busy elsewhere therefore cannot lose a recovery.
- **Idempotency keys differ by path.** Recovery uses
  `cotel-health-recovery:<alert id>:<GITHUB_RUN_ID>`, so a re-dispatched or
  retried job cannot mint a second heartbeat. The still-red wake buckets on a
  coarse window instead — `cotel-health-still-red:<alert id>:<epoch/21600>` —
  so a multi-day outage spends about four heartbeats a day rather than
  twenty-four.
- **An alert assigned to anyone else is a pager failure, by name.** The API
  allows self-wake only, so the pager reports
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

**A paging failure is a warning, never the job's verdict.** Each job's red or
green means "production is red or green" and nothing else; if the pager itself
cannot reach Paperclip, the run carries a `Pager failed` annotation saying
nobody was woken. The loopback job also checks for `jq` and `python3` on every
run — that runner is a developer machine, not a managed image, so a missing
interpreter should surface on a green hour rather than during an incident.

Paperclip budget is spent only on a state change: the create on the first red
hour, and a wake on a further red or on a recovery. A green hour with no open
alert spends nothing. A scheduled Paperclip routine that fires every hour
regardless of health is the more expensive alternative (24 heartbeats a day).
It is not enabled.

## Local use

```sh
# the deploy host, from the deploy host — no Access token involved
HEALTHZ_URL=http://127.0.0.1:8080/healthz scripts/probe-healthz.sh

# through Cloudflare (needs an Access service token allowed on the app)
scripts/probe-healthz.sh

# a closed local port — the failure demo
scripts/probe-healthz.sh http://127.0.0.1:1/healthz

# the classification tests, including 503 vs stale vs empty vs refused
bash scripts/probe-healthz_test.sh

# the pager against a fake curl — dedup and per-call failure labels
bash scripts/page-cotel-health_test.sh
```

Exit codes: `0` healthy, `1` unreachable, `2` HTTP non-200, `3` stale/empty,
`4` Access blocked.

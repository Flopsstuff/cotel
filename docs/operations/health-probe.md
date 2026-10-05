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
| Alert dedup marker | `[cotel-health-probe]` | `[cotel-health-probe-edge]` |

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
they would fight: a green loopback hour would mark the alert the edge half had
just raised as done.

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
unless you intend to open a Paperclip alert.

To exercise the pager end to end, dispatch twice with **page** checked: once
with `loopback_url=http://127.0.0.1:9` (a closed port — raises the alert, whose
assignment wakes its assignee), then once with the default (green — wakes that
assignee again, who closes the alert from their own run). Never break
production to get a red run. A dispatched drill writes to the **production**
alert marker, so do not run one while a genuine alert is standing.

The schedule runs only from the default branch. On a public repository GitHub
disables scheduled workflows after 60 days with no repository activity. The
notice for that goes to GitHub notifications, which do not wake anyone here —
the same silence this probe exists to close. A push is repository activity and
resets that 60-day clock. If the repository sits idle long enough for GitHub
to disable the schedule, this probe goes quiet with it.

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
over issues. See
[ADR-0019](../decisions/0019-ci-never-mutates-an-issue.md) for the options and
the rule it sets.

| Probe hour | Alert state | What the pager does |
|---|---|---|
| red | none open | **creates** the alert, assigned to `PC_ASSIGNEE_AGENT_ID` |
| red | one open | **wakes** its assignee — "still red, add this probe output" |
| green | one open | **wakes** its assignee — "green again, close this alert" |
| green | none open | nothing at all: no call, no wake |

The wake carries the alert's identifier and id, the probe output and the green
run's URL in its `payload`, so the woken run needs to re-derive nothing. Three
details matter in operation:

- **`202 {"status":"skipped"}` is success.** It means a run is already live for
  that agent, and a live run reads current state — which is the state the wake
  was going to tell it about.
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

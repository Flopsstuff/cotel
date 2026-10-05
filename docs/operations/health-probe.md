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
is the wake. A later red hour searches `q=<marker>` and comments on the open
issue whose **title** contains it. Search also matches comments and
descriptions, so the first hit is not the alert, and a longer marker such as
`[cotel-health-probe-selftest]` is not this one. The create request does not
send `originId`: the issues API drops unknown fields, and list search does not
query that column.

A green hour marks that same issue done. If the assignee still has the alert
checked out, the status change comes back as a run-ownership conflict. The
probe leaves the issue open and does not fail the job, because that assignee
is already awake. The next green hour closes it once the checkout is released.

**A paging failure is a warning, never the job's verdict.** Each job's red or
green means "production is red or green" and nothing else; if the pager itself
cannot reach Paperclip, the run carries a `Pager failed` annotation saying
nobody was woken. The loopback job also checks for `jq` and `python3` on every
run — that runner is a developer machine, not a managed image, so a missing
interpreter should surface on a green hour rather than during an incident.

Paperclip budget is spent only when a probe is red (create or comment) or
when it recovers. A green hour with no open alert does not write. A scheduled
Paperclip routine that fires every hour regardless of health is the more
expensive alternative (24 heartbeats a day). It is not enabled.

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
```

Exit codes: `0` healthy, `1` unreachable, `2` HTTP non-200, `3` stale/empty,
`4` Access blocked.

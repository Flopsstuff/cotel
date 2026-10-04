# Production /healthz probe

An hourly GitHub Actions workflow asks the **dashboard** `/healthz` on the
production instance and pages a Paperclip agent when the answer is not healthy.
It exists because a dead or silent cotel otherwise has no one looking: `Deploy`
only runs on a push to `main`, the Cloudflare tunnel stays green while the
process is 503 or not listening, and a red Actions run in this company does not
wake anyone on its own.

## What it checks

Target: `https://cotel.aignite.pl/healthz` (dashboard port, behind the tunnel).
Not the ingest hostname, not `/`, not Cloudflare's tunnel "healthy" flag.

The probe classifies four failure modes with distinct text, because they send
the on-call looking in different places:

| Probe verdict | Typical cause |
|---|---|
| `unreachable` | Process not listening, host down, or the tunnel is not forwarding |
| `HTTP 503 database unreadable` | Process is up; DuckDB is not |
| `HTTP <other>` | Crash loop, Access/proxy error, unexpected handler |
| `ingest stale` / `empty database` | Process is up and the DB reads; spans are not being accepted |

A 200 with `ok: true` is not enough. Staleness is a body field
(`newest_span_age_seconds`); the endpoint keeps 200 on a quiet instance so the
container HEALTHCHECK does not flap. The probe applies its own threshold:
**6 hours**. A present JSON `null` is an empty database (never ingested), which
is not the same as `0` (ingested just now) and not the same as a missing key.

If `last_ingest_at` / `newest_span_age_seconds` are absent, the probe degrades
to liveness (HTTP 200) so it keeps working before that contract is on
production.

Cloudflare Access sits in front of the dashboard. The workflow sends the same
Access service-token headers as `paperclip-issue-sync.yml`. A login redirect is
reported as `cloudflare access blocked` — that is a probe-config failure, not
"cotel is down". The token must be allowed on the `cotel.aignite.pl` Access
application; if a run comes back `access blocked`, add it there (or bypass
`/healthz` only).

## Schedule

`17 * * * *` UTC, hourly. Six silent days was too long; catching an outage the
same day is the bar. The 6h ingest-age threshold is independent of the poll
interval: a quiet night is not an alert.

Manual run: Actions → **Health probe** → **Run workflow**. Optional URL
override is for demonstrating a red run against a dead endpoint; leave **page**
unchecked unless you intend to open a Paperclip alert.

## Who is woken, and how

A red GitHub Actions run is **not** the page. Notifications on the Fl0p
account are unproven (no `notifications` API scope, no public mailbox, agent
identities are not GitHub users), and this company has already watched red
Actions sit unnoticed.

On red, the workflow creates (or comments on) a Paperclip issue with
`originId: cotel-health-probe`, assigned to Daedalus. That assignment is the
wake — the same path `paperclip-issue-sync.yml` already uses for GitHub issue
intake. Paperclip budget is spent only when the probe is red or when it
recovers (the standing issue is marked done). Green hourly ticks do not create
issues.

A scheduled Paperclip *routine* that fires every hour regardless of health is
the more expensive alternative (24 execution issues a day, each a heartbeat).
It is not enabled.

## Local use

```sh
# production (needs Access service token env if the dashboard policy requires it)
scripts/probe-healthz.sh

# a closed local port — the failure demo
scripts/probe-healthz.sh http://127.0.0.1:1/healthz

# the classification tests, including 503 vs stale vs empty vs refused
bash scripts/probe-healthz_test.sh
```

Exit codes: `0` healthy, `1` unreachable, `2` HTTP non-200, `3` stale/empty,
`4` Access blocked.

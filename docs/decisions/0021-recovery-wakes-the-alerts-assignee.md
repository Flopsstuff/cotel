# ADR 0021 — Recovery wakes the alert's assignee, and dedup is time-bounded

**Date:** 2026-10-05
**Status:** Accepted
**Supersedes:** [ADR-0019](./0019-ci-never-mutates-an-issue), [ADR-0020](./0020-recovery-arrives-as-a-new-issue)
**Deciders:** Daedalus (CTO)

---

## Context

This is the third record on one question — how a green hour closes the alert a
red hour opened — and it exists because the second one was decided on a
measurement of a defect rather than of the design. The question itself has not
moved: the CI credential is a long-lived **agent** API key, a CI job holds no
heartbeat run, and the tracker refuses an agent-identity write to an existing
issue that has no run to attribute it to. `create` is the one tracker write CI
can make unaided. [ADR-0019](./0019-ci-never-mutates-an-issue) sets out the two
gates and declines a board key in CI; both still stand.

[ADR-0020](./0020-recovery-arrives-as-a-new-issue) reversed ADR-0019 on a single
claim:

> A bare agent wake cannot carry a write, because the run it produces has no
> ticket. […] the endpoint's request schema has no field that could bind one —
> only `source`, `triggerDetail`, `reason`, `failedRunId`, `payload`,
> `idempotencyKey`, `forceFreshSession` and `debug`. `payload` is inert data the
> woken agent can read; it does not give the run a ticket.

**That claim is false, and the evidence behind it was taken against a typo.**
The first implementation of the wake named the field `alertIssueId`, which
nothing on the server reads. A run born from that wake is indeed bound to
nothing — scratch directory `paperclip-run-unassigned-<run-id>`, no task, every
write to the alert cross-issue and refused. ADR-0020 generalised a misspelled
key into a property of the endpoint.

The field the server reads is `payload.issueId`. The tracker promotes it into
the run's own context before the run row is created, and the authority check
reads exactly the promoted field:

- `enrichWakeContextSnapshot` copies `payload.issueId` (or `payload.taskId`)
  into the new run's `contextSnapshot.issueId` **and** `.taskId`.
- The cross-issue limiter resolves a run's source issue from
  `contextSnapshot.issueId` / `.taskId`, and when the target equals the source
  it does not count the write and does not refuse it — the write is in-ticket.
- Nothing on that path branches on the wake's `source`. The ordinary
  assignment wake — the one that already produces task-bound runs every day —
  reaches `heartbeat.wakeup` with the same `payload.issueId`, through the same
  enrichment. A wake-born run and an assignment-born run are the same row in
  the field that decides authority.

Observed on the live instance after the field was corrected: the recovery wake
appears in the alert's own wake diagnostics as `source: automation`, which it
could only do by resolving to that task.

So ADR-0019's comparison was right after all. Both designs end in a write; the
wake's is **in-ticket**, which is the shape that works unconditionally, and the
notice's is **cross-issue**, contended on the target's checkout. ADR-0020 cited
one notice run closing an alert successfully; the other live notice run did
not — its cross-ticket `PATCH` took a `409` and the alert closed only because
its own run was still live. That is the contention ADR-0019 predicted.

**One finding of ADR-0020 survives intact and is adopted here: the drill
methodology was unsound.** Every drill dispatched red and green seconds apart,
so the alert's own assignment run was still live and the recovery wake was
*coalesced into it* rather than spawning a run of its own. The drill therefore
proved the alert closes, but not that the recovery path closed it. Under either
design that is the hour that matters, and no drill had exercised it.

A second limit, found by being the woken agent: **the wake carries no evidence.**
`payload` scopes the run to the ticket but does not reach the agent — the
adapter is handed the server-built wake payload (reason, thread, objective), the
free-text `reason` is bucketed to an enum, and caller fields are dropped. The
woken agent sees the alert, whose description is the **red** text, and nothing
that says production recovered. ADR-0019 claimed the opposite ("the woken run
needs nothing it has to re-derive") and was wrong.

## Decision

**Recovery wakes the alert's assignee, who closes it in-ticket. The alert's own
description tells that agent how to tell red from green. Dedup is time-bounded
so no write is load-bearing.**

1. **The close is an in-ticket write by the assignee.** On a green hour with an
   alert open, the pager `POST`s `/api/agents/<alert assignee>/wakeup` with
   `payload.issueId` set to the alert's id — the field that binds the run to the
   ticket. The woken run closes the alert from a run that owns it. No second
   issue, no second marker, no cross-issue write. A `202 {"status":"skipped"}`
   is success: a run for that agent and ticket is already live and will see it.
2. **The evidence travels in the alert, not in the wake.** Because the payload
   reaches nobody, the alert's description — written by CI at `create`, the one
   write CI always has — carries the protocol: *if you are woken on this issue,
   re-probe production yourself before acting; this description always reads
   red, because the pager cannot edit it. Green means the outage is over; close
   this issue, citing the probe.* A live re-probe is better evidence than a
   payload minted an hour earlier, and it needs no passthrough the tracker does
   not offer.
3. **A second red hour wakes, it does not comment.** The `Still red.` comment is
   refused by the same gate and has never worked. It becomes the same wake,
   bucketed on a coarse window so a multi-day outage spends about four
   heartbeats a day rather than twenty-four.
4. **Dedup is time-bounded (`PC_ALERT_MAX_AGE_H`, default 6h).** `raise` treats
   an older open alert as stale and opens a fresh one. This was ADR-0019's
   option 3, shelved, and ADR-0020's item 2. It is kept because it is the only
   part of the recovery story that performs no write and therefore cannot be
   refused: if every mechanism above fails, a stale alert still cannot swallow
   the next outage.

The rule for the credential, restated and now accurate:

> The tracker credential in CI holds **create-only** authority over issues.
> Anything that must mutate an existing issue is performed by an agent, from a
> run bound to that issue. CI's reach is to *start* such a run — by creating an
> issue, or by waking an agent with `payload.issueId` naming one — never to
> perform the write itself.

Mechanics the implementation must honour:

- Each probe half keeps its own alert marker; the halves must not resolve each
  other's alerts.
- A green hour with no open alert writes nothing and wakes nobody.
- Send an `idempotencyKey` on every wake, derived from the alert id and the
  green run id (recovery) or a coarse time bucket (still red), so a retried or
  re-dispatched job cannot mint a second heartbeat.
- If the alert is assigned to an agent this credential cannot wake — the
  endpoint permits self-wake only — report it by name as a pager failure rather
  than stepping over it.
- A pager failure stays a **warning**, never the job's verdict. A red job means
  production is red and nothing else.
- The loopback half runs on the macOS runner, whose `/usr/bin/env bash` is 3.2 —
  no `mapfile`/`readarray`, no `${x,,}`.
- **The drill must leave a gap.** Red, then wait for the alert's assignment run
  to finish, *then* green. A back-to-back pair coalesces the recovery wake into
  the still-live assignment run and proves nothing about the path it is there to
  test. The recovery wake must appear in the alert's wake diagnostics with a run
  id of its own.

## Consequences

- **One issue per incident, and the receipt lands in the alert thread** — where
  the next reader of the incident looks. This is what ADR-0019 wanted and
  ADR-0020 gave up to buy a write it believed was impossible.
- **Recovery latency is one heartbeat**, as under either design: the alert
  closes when the woken assignee next runs, not the instant the probe turns
  green.
- **The woken agent pays one extra probe.** It re-derives green instead of
  reading it, by design — see decision 2.
- **A paused assignee still gets nothing.** Waking a paused agent starts no run.
  No design on this credential fixes that; it needs the roster, not the pager.
- **A stale alert is possible but harmless.** If nothing ever closes it, the
  bounded window retires it from dedup and the next red opens a fresh one.
- **Three records on one decision is the real cost here.** The lesson is not
  about this endpoint: twice, a drill result was read as a property of the
  design when it was a property of the attempt, and an ADR was written at the
  speed of the drill. A refusal observed once is a hypothesis; the record is
  worth writing when the mechanism behind it has been read, not only hit.
- If the tracker ever grows a first-class way for an external, runless identity
  to resolve an issue it created — a scoped alert token, or an idempotent
  "resolve what I opened" endpoint — it supersedes this ADR, removes the
  heartbeat of latency, and is narrower authority than a board key.

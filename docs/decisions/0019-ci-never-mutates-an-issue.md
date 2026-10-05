# ADR 0019 — CI never mutates an issue: recovery wakes the alert's assignee

**Date:** 2026-10-05
**Status:** Superseded by [ADR-0020](./0020-recovery-arrives-as-a-new-issue)
**Deciders:** Daedalus (CTO)

---

> **Superseded.** Option 5 below rests on the claim that the agent woken by
> `POST /api/agents/{id}/wakeup` closes the alert with an *in-ticket* write. It
> does not: that endpoint produces a run bound to no task, so the write is
> cross-issue with no run to attribute it to and is refused `403`. The option
> this record rejected — recovery as a new issue — is the one whose write works.
> See [ADR-0020](./0020-recovery-arrives-as-a-new-issue). The analysis of the two
> gates, and the refusal of a board API key in CI (option 1), still stand.

## Context

The hourly `/healthz` probe pages the tracker by **creating an issue** assigned
to an agent; that assignment is the wake. See
[Production /healthz probe](../operations/health-probe). Raising works. Closing
the alert when production recovers does not, and the gap is not a bug in this
repository.

The credential in `PAPERCLIP_API_TOKEN` is a long-lived **agent** API key. In
the tracker, an agent identity that mutates an existing issue must attribute the
write to a heartbeat run, and a CI job has no run. Two independent gates enforce
it, both observed live on dispatch drills:

1. The alert is assigned to the key's own agent, whose run checks it out within
   milliseconds of the create, so the write demands that run's id —
   `401 Agent run id required`.
2. Assign the alert to a different agent and the first gate opens, but every
   agent field update is counted against a per-run cross-issue cap, and there is
   no run to count against — `403 cross_issue_influence_run_context_required`.

There is no agent-key path past the second gate. The same gates block the
`Still red.` dedup comment, so after the first red hour the only tracker
*write* the pager can still make is another `create`.

Leaving it there has a cost beyond an untidy board: dedup attaches the next red
hour to whatever alert is already open, so an alert nobody closed **swallows the
next real outage**. That is the exact silence this probe was built to end — six
days of dead production in September 2026 with nobody looking.

## Options considered

### 1. Put a board API key in the CI secret

Board actors are exempt from the run-context gate, so `status: done` would go
through with no run id and no code change at all. This is the option the
investigation surfaced, and it is declined.

A board key is instance-admin authority over the **whole** tracker instance —
every company, every agent, every issue — handed to a CI job whose entire need
is to close one issue it opened itself. This repository is public, its workflows
run on a self-hosted runner, and any future workflow or collaborator in it
inherits whatever that secret can do. The blast radius is wildly out of
proportion to the task, and the only revocation story is a human noticing and
rotating it. Least authority is not a formality here: the alerting path exists
to be trusted unattended.

### 2. Poll from inside the tracker

A scheduled routine, or a monitor on the open alert, wakes an agent who holds a
run and can close it. Correct, and needs no new credential — but it spends a
heartbeat per poll whether or not anything changed, and the probe's whole budget
argument is that it writes only on a state change. A routine firing hourly is 24
heartbeats a day to notice one transition.

### 3. Expire the alert instead of closing it

A time-bounded dedup window — ignore an alert older than N hours, so a later red
raises a fresh one. Cheap, and it removes the swallowing. But it is a mitigation,
not a recovery: nothing ever records that production came back, and the stale
alert stays open forever. Worth having as a backstop; not an answer.

### 4. Recovery arrives as a new issue

`create` is the one tracker write a runless agent key *can* make. So on a green
hour with an alert still open, the pager creates a second, small issue assigned
to that alert's assignee, naming the alert and asking for it to be closed.

Rejected, for a reason that only shows up when you ask *which write finally
closes the alert*: it is a **cross-issue** write. The agent woken by the notice
holds a run scoped to the notice and must then reach into a different issue —
the shape that is counted against the per-run cross-issue cap, and the shape
that also has to win the alert's checkout. This is the most failure-prone write
in this tracker, and the design would have put the last step of the recovery
path on it. It also mints a second issue per incident, a second dedup marker,
and a second thing to close.

### 5. Wake the alert's assignee

`POST /api/agents/{id}/wakeup` accepts an agent API key **for its own agent**
(`403 Agent can only invoke itself` otherwise), requires no run id, and is not
an issue write — so the credential keeps create-only authority over issues. On a
green hour with an alert still open, the pager wakes the agent it authenticates
as, carrying the alert's identifier, the probe output and the green run's URL.

That agent then finds the alert in its own inbox, assigned to itself, and closes
it from a run of its own: an **in-ticket** write by the assignee, the one write
shape that works here unconditionally. If the alert's checkout is held by a dead
run, a fresh wake is also exactly what releases it.

## Decision

**Option 5.** CI never mutates an existing issue and never creates a second
issue to carry a recovery. The pager's `resolve` action stops trying to `PATCH`
the alert and instead wakes the alert's assignee, which routes the close through
a heartbeat run that is allowed to perform it, in the ticket where it belongs.

The general rule this sets for every CI job in this repository:

> The tracker credential in CI holds **create-only** authority over issues, and
> may wake only the agent it authenticates as. Anything that has to mutate an
> existing issue is routed to that agent's run, never by widening the credential
> and never by minting another issue.

Mechanics that the implementation must honour:

- On a green hour, wake only if an alert is **still open**. A green hour with no
  open alert writes nothing and wakes nobody.
- The same substitution applies to the red path. A red hour that finds an alert
  already open cannot comment on it — the `Still red.` comment is refused by the
  same gate — so it wakes the assignee instead. To keep a long outage from
  spending a heartbeat an hour, bucket that wake's `idempotencyKey` on a coarse
  time window rather than on the run.
- The wake targets the open alert's `assigneeAgentId`. The credential can only
  wake its own agent, so an alert assigned to anyone else is a condition the
  pager must report by name as a pager failure, not step over quietly.
- The wake payload carries the alert's identifier and id, the probe output and
  the green run URL, so the woken run can act without re-deriving any of it.
- Send an `idempotencyKey` derived from the alert id and the green run id, so a
  re-dispatched or retried job does not mint a second heartbeat.
- `202 skipped` is success: a run is already live for that agent, and a live run
  reads current state, which is green.
- Each probe half keeps its own alert marker; the loopback and edge halves must
  not resolve each other's alerts.
- A pager failure stays a **warning**, never the job's verdict. A red job means
  production is red and nothing else.

## Consequences

- **No new secret, and no new authority anywhere.** The CI credential keeps
  exactly what it has today, plus a self-wake it could already perform.
- **Recovery latency is one heartbeat**, not zero. An alert closes when its
  assignee next wakes rather than the instant the probe turns green. Accepted
  deliberately: the alternative that closes it in seconds costs an
  instance-admin credential in CI.
- **The receipt that production came back lands in the alert thread**, as the
  closing comment with the green run's URL on it — where someone reading the
  incident looks anyway, rather than in a separate ticket.
- **No extra issue, no second marker, no second dedup rule.** The whole recovery
  path is one API call in the probe script, and the red path's dead comment call
  collapses into the same one.
- **A paused assignee still gets nothing.** The wake is declined and the alert
  stands. Assigning an issue to a paused agent wakes nobody either, so no design
  on this credential fixes that; it needs the roster, not the pager.
- **The swallowing risk narrows but does not vanish.** If the assignee never
  wakes, the alert still stands and still dedups later reds. Option 3 remains
  available as a backstop on top of this, and is cheap.
- If the tracker ever grows a first-class way for an external, runless identity
  to resolve an issue it created — a scoped alert token, or an idempotent
  "resolve what I opened" endpoint — that supersedes this ADR. It would be
  narrower authority than a board key and would remove the heartbeat of latency.

## Changed from the first draft

An earlier revision of this record, accepted and merged hours before this one,
chose option 4. It was reversed before any code was written, on the cross-issue
argument above: the notice design is more machinery and puts the decisive write
on the least reliable path. That revision also carried a duplicate ADR number,
which is why this file was renumbered to 0019 separately.

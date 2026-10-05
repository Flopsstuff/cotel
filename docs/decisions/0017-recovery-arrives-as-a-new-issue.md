# ADR 0017 — CI never closes an alert: recovery arrives as a new issue

**Date:** 2026-10-05
**Status:** Accepted
**Deciders:** Daedalus (CTO)

---

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
`Still red.` dedup comment, so after the first red hour the only call the pager
can still make is another `create`.

Leaving it there has a cost beyond an untidy board: dedup attaches the next red
hour to whatever alert is already open, so an alert nobody closed **swallows the
next real outage**. That is the exact silence this probe was built to end — six
days of dead production in September 2026 with nobody looking.

## Options considered

### 1. Put a board API key in the CI secret

Board actors are exempt from the run-context gate, so `status: done` would go
through with no run id and no code change at all. This is the option the
investigation surfaced, and it is the one being declined.

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

`create` is the one tracker write a runless agent key *can* make — proven by the
raise path it already uses every red hour. So on a green hour with an alert
still open, the pager creates a second, small issue assigned to that alert's
assignee, naming the alert and asking for it to be closed. The assignee is woken
by the assignment, holds a run, and closes both.

## Decision

**Option 4.** CI never mutates an existing issue. The pager's `resolve` action
stops trying to `PATCH` the alert and instead creates a recovery notice, which
routes the close through an agent heartbeat that is allowed to perform it.

The general rule this sets for every CI job in this repository:

> The tracker credential in CI holds **create-only** authority. Anything that
> has to mutate an existing issue is routed to an agent run by creating an
> issue, never by widening the credential.

Mechanics that the implementation must honour:

- The recovery notice carries its **own** dedup marker, so that a recovery
  notice is never mistaken for an open alert and a second green hour does not
  mint a duplicate. Alert lookup matches the bracketed marker as an exact
  substring of the title, so the recovery marker must not contain the alert
  marker — `[cotel-health-recovery]` is safe, `[cotel-health-probe-recovery]`
  is not.
- Each probe half keeps its own pair of markers; the loopback and edge halves
  must not close each other's alerts.
- A green hour with no open alert still writes nothing.

## Consequences

- **No new secret, and no new authority anywhere.** The CI credential keeps
  exactly what it has today: create issues in one company.
- **Recovery latency becomes one heartbeat**, not zero. An alert closes when its
  assignee next wakes rather than the instant the probe turns green. Accepted
  deliberately: the alternative that closes it in seconds costs an instance-admin
  credential in CI.
- **The recovery notice is itself the receipt** that production came back, with
  the green run's URL on it — a record the `PATCH` approach would have left only
  as a status flip.
- **One extra issue per outage.** Only on the red→green transition, so the
  volume is one per incident, not one per hour.
- **The swallowing risk narrows but does not vanish.** If the assignee never
  wakes, the alert still stands and still dedups later reds. Option 3 remains
  available as a backstop on top of this, and is cheap.
- If the tracker ever grows a first-class way for an external, runless identity
  to resolve an issue it created — a scoped alert token, or an idempotent
  "resolve what I opened" endpoint — that supersedes this ADR. It would be
  narrower authority than a board key and would remove the heartbeat of latency.

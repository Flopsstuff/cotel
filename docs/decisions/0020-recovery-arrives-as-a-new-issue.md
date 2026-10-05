# ADR 0020 — Recovery arrives as a new issue, and dedup is time-bounded

**Date:** 2026-10-05
**Status:** Superseded by [ADR-0021](./0021-recovery-wakes-the-alerts-assignee)
**Supersedes:** [ADR-0019](./0019-ci-never-mutates-an-issue)
**Deciders:** Daedalus (CTO)

---

> **Superseded.** The premise below — that `POST /api/agents/{id}/wakeup`
> produces a run bound to no task — is false. The field that binds one is
> `payload.issueId`, which the tracker promotes into the run's own context
> before the run exists; the drill that showed an unbound run had misspelled it
> `alertIssueId`. The wake's write is in-ticket after all, and the notice's is
> the contended cross-issue one. This record's finding about the *drill
> methodology* — red and green dispatched seconds apart coalesce the recovery
> wake into the still-live assignment run, so the path under test never ran —
> is correct and is carried into
> [ADR-0021](./0021-recovery-wakes-the-alerts-assignee), along with the
> time-bounded dedup window. The refusal of a board API key in CI still stands.

## Context

[ADR-0019](./0019-ci-never-mutates-an-issue) decided that on a green hour the
pager would **wake the alert's assignee**, who would then close the alert
"from a run of their own: an **in-ticket** write by the assignee, the one write
shape that works here unconditionally." It rejected the alternative of creating
a recovery notice precisely because that option's closing write would be
*cross-issue*, and cross-issue is the most failure-prone write in the tracker.

The premise is false, and the first live exercise of the wake path disproved it.

A run spawned by `POST /api/agents/{id}/wakeup` is **not bound to any task**.
There is no `PAPERCLIP_TASK_ID` in its environment, its scratch directory is
named `paperclip-run-unassigned-<run-id>`, and the endpoint's request schema has
no field that could bind one — only `source`, `triggerDetail`, `reason`,
`failedRunId`, `payload`, `idempotencyKey`, `forceFreshSession` and `debug`.
`payload` is inert data the woken agent can read; it does not give the run a
ticket.

So the woken run's write to the alert is not an in-ticket write. It is a
cross-issue write with no run to attribute it to — gate 2 of the two that
ADR-0019 itself documents:

```
POST /api/issues/<alert>/comments  ->  403
{"error":"Cross-issue writes need a run to attribute them to (Heartbeat run
 context). Every agent comment and task update is attributed to a heartbeat run
 so the cross-issue cap can be counted and the audit trail can name who acted
 for whom. This request arrived without a valid run, so it could not ..."}
```

`PATCH` of the alert's status fails the same way. So does a `PATCH` of an issue
the woken run **created itself** moments earlier. The one tracker write such a
run can make is `create` — exactly the authority the CI credential already has.

ADR-0019 therefore compared the two options backwards. Both end in a
cross-issue write; the difference is that the notice's write is *contended* and
succeeds once it wins the target's checkout, while the wake's write is
*unconditionally refused*, because there is no run to count against and never
will be. Observed on the other side: an alert received a cross-issue closing
comment seven minutes after its own assignment run had finished, from a
different agent's task-bound run. The shape ADR-0019 called least reliable
works; the shape it chose does not.

The defect was hidden by how the path was drilled. Every drill dispatched the
red and the green halves seconds apart, so the alert's **own** assignment run —
a genuinely task-bound run, from the raise path — was still live and closed the
alert itself. The wake contributed only a second, write-incapable run. On a real
incident that assignment run ends hours earlier with the alert legitimately
still open, and the recovery wake is the only path left. The pager would have
looked green in every drill and failed on the one hour it exists for.

The same collapse applies to ADR-0019's red path: the `still red` wake also asks
an unassigned run to update an existing alert, and is refused identically.

## Decision

**Recovery arrives as a new issue, and dedup is time-bounded.** Two things must
happen when production recovers — a receipt that it came back, and the assurance
that a standing alert cannot swallow the next real outage. Neither may depend on
a write that can be refused.

1. **Receipt — a `create`.** On a green hour with an alert still open, the pager
   creates a small recovery notice assigned to the alert's assignee, naming the
   alert's identifier and id and carrying the green run's URL and probe output.
   This is ADR-0019's option 4, reinstated. The notice's assignment spawns a
   task-bound run, which *can* close the alert cross-issue — so the alert still
   normally closes, one heartbeat later, but nothing is lost if that write loses
   the checkout.
2. **No swallowing — script logic, zero writes.** `raise` treats an open alert
   older than a bounded window (`PC_ALERT_MAX_AGE_H`, default 6h) as stale and
   opens a fresh alert instead of deduping into it. This was ADR-0019's option 3,
   shelved as a backstop; it becomes load-bearing, because it is the only part of
   the recovery story that cannot fail.
3. **The red path writes nothing after the first alert.** A red hour that finds
   an open alert inside the window does not wake and does not comment — the per
   hour trail already exists in the GitHub run list. The `still red` wake is
   removed rather than repaired.

The rule ADR-0019 set for the credential still holds, and is narrowed:

> The tracker credential in CI holds **create-only** authority over issues.
> Anything that must mutate an existing issue is carried by an issue it creates,
> whose assignment gives some agent a task-bound run that is allowed to perform
> the write. A bare agent wake cannot carry a write, because the run it produces
> has no ticket.

ADR-0019's option 1 — a board API key in CI — remains declined, unchanged, for
the blast-radius reason given there: instance-admin authority over the whole
tracker, in a public repository's CI, to close one issue CI opened itself.

Mechanics the implementation must honour:

- The notice carries its **own** marker, distinct from either alert marker, so
  notices never dedup against alerts or against each other across incidents.
- Each probe half keeps its own alert marker; the halves must not resolve each
  other's alerts.
- A green hour with no open alert writes nothing and creates nothing.
- Send an `idempotencyKey` on the notice create, derived from the alert id and
  the green run id, so a retried or re-dispatched job does not mint two.
- If the open alert is assigned to an agent this credential cannot act for, the
  pager reports that by name as a pager failure rather than stepping over it.
- A pager failure stays a **warning**, never the job's verdict. A red job means
  production is red and nothing else.
- The loopback half runs on the macOS runner, whose `/usr/bin/env bash` is 3.2 —
  no `mapfile`/`readarray`, no `${x,,}`.

## Consequences

- **The decisive write is no longer load-bearing.** The receipt is a `create`,
  which cannot be refused. Closing the alert is best-effort on top of that, and
  the swallowing risk is handled by logic that performs no write at all. This is
  the lens ADR-0019 invoked and misapplied: do not put a correctness requirement
  on the least reliable path.
- **Cost: one extra issue per incident**, and a second marker and dedup rule —
  the machinery ADR-0019 declined. It buys a recovery path that works, which the
  cheaper design did not.
- **Recovery latency stays one heartbeat.** The alert closes when the notice's
  assignee next wakes, not the instant the probe turns green.
- **The receipt is in its own ticket, not the alert thread.** Worse for someone
  reading the incident than ADR-0019's intent, and the reason that ADR preferred
  the wake. It is the price of the receipt existing at all; the notice names the
  alert, and the closing comment that the notice's run makes still lands on the
  alert whenever that write wins.
- **A stale alert is now possible but harmless** — if nothing ever closes it, the
  bounded window retires it from dedup and the next red opens a fresh one.
- **A paused assignee still gets nothing.** Assigning an issue to a paused agent
  wakes nobody, so the notice goes unread. No design on this credential fixes
  that; it needs the roster, not the pager.
- If the tracker ever grows a first-class way for an external, runless identity
  to resolve an issue it created — a scoped alert token, or an idempotent
  "resolve what I opened" endpoint — that supersedes this ADR, removes the extra
  issue and the heartbeat of latency, and is narrower authority than a board key.

## Note on ADR-0019

ADR-0019 was accepted and merged hours before this record, and itself reversed an
earlier revision that had chosen the notice design. This ADR restores that
choice, on evidence rather than on the reasoning either revision used: the wake's
write was tried against the live API and refused, and the notice's write was
observed to succeed. No implementation of ADR-0019's wake path reached `main`.

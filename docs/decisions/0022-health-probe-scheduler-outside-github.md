# ADR 0022 — The health probe's scheduler lives outside this repo

**Date:** 2026-10-05
**Status:** Accepted
**Deciders:** Prospero (CEO, mechanism choice), Daedalus (CTO, implementation)

---

## Context

The probe and the pager work. Both halves were exercised live: a red probe
creates the alert, the assignment wakes its assignee, and the recovery routes
through a wake that the assignee acts on
([ADR-0021](./0021-recovery-wakes-the-alerts-assignee)). What did not work was
the leg nobody had measured — the schedule.

`cron: "17 * * * *"` landed on `main` at 00:44Z on 2026-10-05. By 09:46Z it had
produced **one** run instead of about nine, and `gh run list
--workflow=health-probe.yml --limit 200` showed exactly one `event=schedule` run
in the workflow's whole history. Two innocent explanations were checked and
excluded: a cancelled run would still be listed as `cancelled`, and the 60-day
deactivation of schedules on idle public repositories had not fired (the
repository was active that day). GitHub schedules public repositories on a
best-effort basis and **drops** ticks rather than delaying them.

That matters more here than it would elsewhere. This probe exists because
production sat dead for six days with nobody looking; a watcher whose detection
delay is unbounded is a quieter version of the same silence, and nothing watches
whether the watcher ran.

A second gap compounded it. The loopback half runs on a self-hosted runner on
the deploy host itself, so when that host is off the job does not go red — it
queues, and `timeout-minutes` does not bound queue time. The failure mode most
worth catching produced no colour at all.

## Options considered

1. **A systemd timer on the Pi** (`~/ops`), probing `robmini` over the LAN and
   calling this repo's existing pager.
2. **A scheduled Paperclip routine** — a reliable scheduler that wakes an agent
   by construction.
3. **Leave the GitHub schedule as it is** and accept best-effort detection.

## Decision

**Option 1.** `cotel-healthz.timer` on the Pi, every 10 minutes, paging on the
second consecutive failure. It owns the production alert marker
`[cotel-health-probe]`. The GitHub workflow keeps `workflow_dispatch` and its
pull-request tests but no longer schedules anything; both of its probe jobs page
drill markers only.

Reasons, in the order they decided it:

- **systemd does not drop ticks**, and `Persistent=true` makes up a tick missed
  across a reboot. That is the entire defect being fixed.
- **Probing from the Pi over the LAN catches the host being off**, which the job
  on that host structurally cannot report. One prober now covers both failure
  classes: dead process (non-200, refused) and dead host (timeout, refused).
- **It costs nothing recurring.** Option 2 was rejected on exactly that: ~24
  full agent runs a day to perform a check that is one `curl`, right after this
  company cut idle heartbeat burn by moving the interval from 1h to 6h. A probe
  should be cheap enough that its cadence is a free variable — at 10 minutes
  this one spends a loopback HTTP request and nothing else while production is
  healthy.
- **Option 3 was rejected** because unbounded detection delay is the thing the
  parent ticket exists to remove.

Consequences accepted with it:

- **The scheduler is not in this repo, and cannot be.** The repo documents where
  it is (`docs/operations/health-probe.md` → `~/ops/README.md`) and the timer
  materializes this repo's scripts from `origin/main` at each tick, so the code
  path stays reviewed here and reaches the timer with no sync step. A reader of
  this repo alone cannot see the schedule; a reader of either runbook can.
- **One machine, one prober.** If the Pi is off, nothing probes — the same shape
  as before, with a different single point. The Pi is where the Paperclip
  instance and every agent already run, so an off Pi has no one to wake anyway.
- **Edge coverage is now unscheduled.** Tunnel, DNS and Cloudflare Access are
  outside the LAN and therefore outside what the Pi can see. This is a real loss
  only on paper: the edge probe cannot observe `/healthz` at all today, because
  its Access service token is not allowed on the application, so it returns exit
  4 and watches a login redirect. Allowing that token is the prerequisite for an
  edge schedule being worth anything, and it is a board action, not a code
  change.
- **The pager's defaults are GitHub's.** The alert description tells the woken
  agent how to re-probe, and the default text names a workflow dispatch — which
  for a Pi-raised alert would point at a runner on the possibly-dead host. The
  pager therefore takes `PC_SOURCE_LINE` and `PC_REPROBE_HINT` overrides, and
  the timer sets them to the local command. Any future non-Actions caller must
  do the same.
- **The credential is wider than ideal.** The timer uses the board ops key
  already on that machine, because minting a narrow agent key is board-only.
  Dropping an agent key at `~/.secrets/cotel-health-pager.token` narrows it with
  no code change. ADR-0021's refusal of a board key stands where it was aimed —
  a public repository's CI — and does not extend to the owner's own host.

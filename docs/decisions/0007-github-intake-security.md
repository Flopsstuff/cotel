# ADR 0007 — GitHub Issue Intake Security Hardening

**Date:** 2026-05-10  
**Status:** Accepted  
**Deciders:** Daedalus (CTO)

---

## Context

The `paperclip-issue-sync.yml` GitHub Actions workflow automatically creates a Paperclip task from every opened GitHub issue and assigns it directly to an internal agent (CTO, Coder, or UX Designer) based on labels. This poses two problems:

1. **Prompt injection risk.** Any external contributor—or adversary—can craft a GitHub issue whose body is read verbatim into an AI agent's task context. Malicious issue bodies can attempt to hijack agent behaviour, exfiltrate information, or issue destructive instructions.

2. **Information disclosure.** The README publicly documents the routing table (label → internal agent), exposing internal team structure and the fact that agents act directly on GitHub input.

Additionally, the feature has no on/off switch, so disabling it requires editing and committing the workflow file.

---

## Options Considered

**A. Remove the feature entirely**  
Eliminates the risk. Loses the convenience of GitHub-sourced issue intake. Rejected because the intake pipeline has real value once it is safe.

**B. Keep current routing, add input sanitisation in the workflow**  
Shell-level sanitisation is brittle and easy to bypass. Cannot reason about intent. Rejected.

**C. Route all intake through Lyra (Gemini agent) as safety screener and triage decomposer**  
Every new GitHub issue lands first in Lyra's queue. Lyra evaluates the content for harmful intent, then—if safe—decomposes it into well-scoped Paperclip subtasks and assigns them to the right agents. The workflow creates only one kind of issue (an intake stub) and knows nothing about internal routing.

**Chosen: Option C.**

---

## Decision

### 1. Feature gate

Add `PAPERCLIP_INTAKE_ENABLED` as a GitHub Actions **variable** (not a secret; it is not sensitive). The workflow skips all Paperclip API calls when the variable is absent or not exactly `"true"`. This makes the feature trivially disableable without touching workflow code.

### 2. Lyra as intake gate

The workflow creates a single Paperclip intake issue assigned to **Lyra** with:

- Title prefix `[INTAKE]` so Lyra can identify intake issues.
- Full context in the description: GitHub issue title, body, URL, repo slug, issue number, labels.
- `status: "todo"`, `priority: "high"`, project/goal set.

Lyra's responsibilities on receipt of an intake issue:

1. **Safety screen.** Evaluate the body for:
   - Prompt injection attempts (instructions targeting the agent, requests to override behaviour, attempts to exfiltrate data).
   - Social engineering targeting internal agents or processes.
   - Spam, abuse, or intentional service disruption.

2. **Rejection path.** If any harmful signal is found:
   - Cancel the Paperclip intake issue with a comment explaining the rejection reason (not the detection logic).
   - Do not create any agent subtasks.

3. **Acceptance path.** If the issue is legitimate:
   - Classify the issue type: bug, feature request, UX, documentation, question, etc.
   - Create one or more well-scoped Paperclip subtasks under the intake issue as parent, assigned to the appropriate agents (Wayland for implementation, Iris for UX/design, Daedalus for architecture).
   - Close the intake issue as done.

### 3. Remove public routing documentation

The README section "GitHub → Paperclip issue routing" (routing table, agent names, workflow filename) is removed. This is internal infrastructure. No user-visible behaviour changes.

---

## Consequences

**Positive:**
- Prompt injection is blocked before any operational agent sees the content.
- Internal agent roster is not exposed publicly.
- Feature can be disabled in seconds via the Actions variable UI.
- Lyra's semantic triage (intent-based) is richer than the old label-based routing.
- Intake issues are visible in the Paperclip board, making the pipeline observable.

**Negative:**
- Every legitimate GitHub issue now takes one Lyra wake before any work begins (latency: minutes rather than seconds; acceptable for async intake).
- Lyra's judgment may miss edge cases in either direction (false reject, false accept); review of cancelled intake issues is recommended periodically.
- Lyra must have Paperclip API access configured and the paperclip skill installed.

**Risks:**
- If Lyra's adapter is down, intake issues pile up in `todo` state; they do not auto-fail. Monitor for stale intake issues.
- Lyra can be fooled by sophisticated adversarial input. This hardens the surface, it does not make it impenetrable. Future: add rate-limiting per GitHub user at the workflow level.

---

## Implementation checklist

- [ ] Remove README section (Wayland / CTO) — see [FLO-126](/FLO/issues/FLO-126)
- [ ] Update workflow: add feature gate, route to Lyra (Wayland) — see [FLO-126](/FLO/issues/FLO-126)
- [ ] Define Lyra intake screening instructions (Lyra) — see [FLO-127](/FLO/issues/FLO-127)
- [ ] Set `PAPERCLIP_INTAKE_ENABLED=true` in repo Actions variables once workflow is deployed

# ADR 0008 — Per-agent telemetry identity must not live in shared settings.json env

**Date:** 2026-06-23
**Status:** Accepted
**Deciders:** Daedalus (CTO)

---

## Context

FlopBut's agents and the board's interactive sessions all run Claude Code with
OpenTelemetry export to cotel. cotel attributes every span to a user by the
bearer token carried in `OTEL_EXPORTER_OTLP_HEADERS`. Distinct per-agent cost and
session attribution therefore requires each agent to export under its **own**
cotel token.

Per-agent tokens were being injected correctly by Paperclip into each agent's
process env (verified: distinct, non-default `cotel_…` tokens in
`/proc/<pid>/environ`), yet **all** agent telemetry merged into a single default
bucket (`cotel_654b…`). Root cause (FLO-486, from FLO-485): the global
`~/.claude/settings.json` `env` block hardcoded a default
`OTEL_EXPORTER_OTLP_HEADERS`.

### Precedence finding (confirmed empirically)

Claude Code reads `settings.json` `env` at startup and uses those values for its
own OTLP exporter, **overriding** the inherited per-agent process env. Observed
from a live agent's tool subprocess: the shared flags (`CLAUDE_CODE_ENABLE_TELEMETRY`,
`CLAUDE_CODE_ENHANCED_TELEMETRY_BETA`) propagate from `settings.json`, while the
`OTEL_*` vars are consumed internally by Claude Code (not even leaked to
subprocess env) — i.e. `settings.json` is Claude Code's source of truth for the
exporter. Combined with the field evidence (distinct environ tokens still landing
in the default bucket), this confirms: a hardcoded identity header in the shared
`settings.json` clobbers every agent's own token.

This is the same class of variable as `GIT_AUTHOR_*` / `GIT_COMMITTER_*` — it
encodes **identity**, and identity must be per-agent, not shared.

## Decision

1. **Shared, identity-independent OTEL config stays in the global
   `~/.claude/settings.json` `env`** — every launch reads it:
   `CLAUDE_CODE_ENABLE_TELEMETRY`, `CLAUDE_CODE_ENHANCED_TELEMETRY_BETA`,
   `OTEL_TRACES_EXPORTER`, `OTEL_EXPORTER_OTLP_PROTOCOL`,
   `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`.

2. **The per-agent identity header `OTEL_EXPORTER_OTLP_HEADERS` is removed from
   the shared `settings.json`** and supplied per-agent via Paperclip
   `adapterConfig.env`, applied by each agent through a **self-apply** script
   (`scripts/agent-set-cotel-token.sh`), mirroring the `GIT_*` scheme
   ([ADR-driving FLO-209/FLO-210], `agent-set-git-identity.sh`). Self-apply is
   required because a cross-agent env PATCH cannot preserve another agent's secret
   env and would wipe it.

3. **The board's interactive (non-Paperclip) sessions get the default token from
   a guarded shell export** in `~/.bashrc`:
   `export OTEL_EXPORTER_OTLP_HEADERS="${OTEL_EXPORTER_OTLP_HEADERS:-Authorization=Bearer cotel_…}"`.
   The `:-` guard makes it set-only-if-unset, so it can never clobber a
   per-agent value, and `.bashrc`'s non-interactive guard means Paperclip-spawned
   agents (non-interactive) never reach it.

### General rule (durable)

> Per-agent **identity** variables — the cotel `OTEL_EXPORTER_OTLP_HEADERS`, and
> `GIT_AUTHOR_*` / `GIT_COMMITTER_*` — must **never** be hardcoded in the shared
> global `~/.claude/settings.json` `env` block, because that block is applied at
> Claude Code startup and overrides the inherited per-agent process env. Only
> shared, identity-independent configuration belongs there.

## Rollout (sequenced to avoid regression)

The declobber and the provisioning must land in this order, because removing the
default before every agent is provisioned would push un-provisioned agents to
anonymous spans:

1. **Mint** one cotel user/token per agent in the cotel dashboard (Users → Add
   user), via board/CTO dashboard access. Produce an agent→token map.
2. **Self-apply**: each agent runs `scripts/agent-set-cotel-token.sh <token>`
   once in a heartbeat to merge its header into its own `adapterConfig.env`. Takes
   effect on its next run.
3. **Declobber flip** (final, gated on all agents provisioned): remove
   `OTEL_EXPORTER_OTLP_HEADERS` from `~/.claude/settings.json`; add the guarded
   default export to `~/.bashrc`.
4. **Verify** in the cotel Users view: distinct agents appear under distinct
   tokens (not all under `cotel_654b…`); the board's interactive sessions remain
   tracked under the default user.

Tracked as a child rollout issue of FLO-486.

## Consequences

- **Correct attribution.** After rollout, per-agent cost/session views are real;
  the default token becomes the board's personal interactive token.
- **One-time per-agent step.** Each agent must self-apply once (same operational
  shape as the `GIT_*` rollout). New agents need a minted token + self-apply as
  part of onboarding.
- **Token minting needs dashboard access.** cotel runs remotely behind Cloudflare
  Access; tokens are minted by whoever holds dashboard access, not by the agents
  themselves. The self-apply script takes the token as input rather than minting.
- **Reversible** (decision lens: reversibility). Re-adding the header to
  `settings.json` restores the old behaviour; this is a two-way door.

## Decision lenses

- **Schema & public interfaces are forever** — identity attribution is a contract
  consumers (the cotel Users/Costs views) depend on; encode it once, consistently.
- **Trust the boundary** — identity is injected at the process boundary
  (Paperclip env / shell export), not re-derived or overridden downstream.
- **Reversibility** — the change is a two-way door; the flip is gated on
  provisioning so it never regresses live attribution.

## References

- FLO-486 (this), FLO-485 (root-cause investigation)
- `docs/operations/agent-identity.md` — the per-agent identity scheme (git + cotel)
- `docs/operations/users-and-auth.md` — cotel users & tokens
- ADR-0004 — Multi-User Separation
- `scripts/agent-set-cotel-token.sh`, `scripts/agent-set-git-identity.sh`

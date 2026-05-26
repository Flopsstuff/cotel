# Agent identity in git & GitHub

How FlopBut's local agents are distinguished in git history and on GitHub.

> Decision: [FLO-209](/FLO/issues/FLO-209) — env-aliases + commit trailers + PR
> attribution. We do **not** create separate GitHub accounts. Every push and PR
> happens under the board's token; agents are distinguished at the commit-author,
> trailer, and PR-metadata level.

## Canonical identities

Slug is the lowercased name. The email is a label for git history only — it is
intentionally **not** a mailbox and **not** linked to any GitHub account. The
domain `agents.flopbut.local` is deliberately non-routable so it can never
mis-link to a real GitHub user.

| Agent    | NAME       | Email                          |
|----------|------------|--------------------------------|
| Prospero | `Prospero` | `prospero@agents.flopbut.local` |
| Daedalus | `Daedalus` | `daedalus@agents.flopbut.local` |
| Wayland  | `Wayland`  | `wayland@agents.flopbut.local`  |
| Aldric   | `Aldric`   | `aldric@agents.flopbut.local`   |
| Soren    | `Soren`    | `soren@agents.flopbut.local`    |
| Lyra     | `Lyra`     | `lyra@agents.flopbut.local`     |
| Iris     | `Iris`     | `iris@agents.flopbut.local`     |
| Orion    | `Orion`    | `orion@agents.flopbut.local`    |

If the board later wants a real domain, change the values everywhere — the
scheme is unchanged.

## 1. Env aliases (author/committer identity)

Each local agent's Paperclip `adapterConfig.env` carries four variables:

```json
"env": {
  "GIT_AUTHOR_NAME":     { "type": "plain", "value": "Wayland" },
  "GIT_AUTHOR_EMAIL":    { "type": "plain", "value": "wayland@agents.flopbut.local" },
  "GIT_COMMITTER_NAME":  { "type": "plain", "value": "Wayland" },
  "GIT_COMMITTER_EMAIL": { "type": "plain", "value": "wayland@agents.flopbut.local" }
}
```

`GIT_*` env vars override `git config` in **every** repository, so no per-repo
setup is needed. New values take effect on the agent's **next** run (the current
process inherited its env at spawn).

### Rolling it out — agents self-apply

Each agent runs this once, inside a heartbeat:

```bash
scripts/agent-set-git-identity.sh
```

It reads the agent's own name from `/api/agents/me`, derives the email, and
**merges** the four `GIT_*` vars into its own `adapterConfig.env`. Idempotent.

> **Why self-apply, not a central rollout?** A `PATCH` to `adapterConfig`
> *replaces* the whole `env` object, and the API redacts other agents' configs
> and rejects cross-agent edits unless the caller holds `agents:create` (most
> agents, including the CTO, do not). Even an authorized caller cannot read
> another agent's *secret* env (e.g. an adapter API key) to merge it back, so a
> blind cross-agent write would silently wipe secrets. An agent editing itself
> can read its full env and merge safely — so self-apply is both the only
> permitted path and the only safe one.

## 2. Commit trailers

In addition to the mandatory skill trailer:

```
Co-Authored-By: Paperclip <noreply@paperclip.ing>
```

each commit gets a second co-author line for the authoring agent:

```
Co-authored-by: Wayland <wayland@agents.flopbut.local>
```

The robust way to add this is the **`prepare-commit-msg` hook** in
`scripts/git-hooks/`, which reads `$GIT_AUTHOR_NAME` / `$GIT_AUTHOR_EMAIL` and
appends the trailer idempotently (it never duplicates). Install it per repo:

```bash
scripts/install-agent-git-hooks.sh            # current repo
scripts/install-agent-git-hooks.sh /path/repo # another checkout
```

This points `core.hooksPath` at `scripts/git-hooks`. The hook is a no-op when no
agent identity is in the env, so it is safe for human contributors too.

## 3. PR attribution

Because PRs are always opened under the board's token, the authoring agent is
recorded with:

- an **`Agent: <Name>`** line in the PR body, and
- an **`agent:<slug>`** label (e.g. `agent:wayland`).

Use the wrapper, which sets both from `$GIT_AUTHOR_NAME` and creates the label
idempotently:

```bash
scripts/agent-pr.sh --title "feat: ..." --body "Closes FLO-NNN" --base main
```

Or do it by hand:

```bash
gh label create "agent:wayland" --color BFD4F2 -f
gh pr create --title "..." --body $'...\n\nAgent: Wayland' --label agent:wayland
```

## Scope

Repositories in scope: `cotel`, `ksef-docs`, and any new repo under
`~/projects/`. Run `scripts/install-agent-git-hooks.sh` once per repo (or per
worktree) to enable the trailer hook there.

`pi_local` (Orion) is in `error`/trial status; its env aliases are applied last
or skipped until the adapter is healthy.

# Agent identity in git & GitHub

How FlopBut's local agents are distinguished in git history and on GitHub.

> Decision: [FLO-209](/FLO/issues/FLO-209) — env-aliases + commit trailers + PR
> attribution. We do **not** create separate GitHub accounts. Every push and PR
> happens under the board's token; agents are distinguished at the commit-author,
> trailer, and PR-metadata level.

## Canonical identities

Slug is the lowercased name. The email is a label for git history only — it is
intentionally **not** a mailbox and **not** linked to any GitHub account, so it
never mis-links to a real GitHub user. The domain was `agents.flopbut.local`
until 2026-11-21; commits made before that keep the old addresses and history
is not rewritten.

| Agent     | NAME        | Email                            |
|-----------|-------------|----------------------------------|
| Prospero  | `Prospero`  | `prospero@agents.flopbut.pl`  |
| Daedalus  | `Daedalus`  | `daedalus@agents.flopbut.pl`  |
| Wayland   | `Wayland`   | `wayland@agents.flopbut.pl`   |
| Aldric    | `Aldric`    | `aldric@agents.flopbut.pl`    |
| Soren     | `Soren`     | `soren@agents.flopbut.pl`     |
| Lyra      | `Lyra`      | `lyra@agents.flopbut.pl`      |
| Iris      | `Iris`      | `iris@agents.flopbut.pl`      |
| Orion     | `Orion`     | `orion@agents.flopbut.pl`     |
| Robert    | `Robert`    | `robert@agents.flopbut.pl`    |
| Pygmalion | `Pygmalion` | `pygmalion@agents.flopbut.pl` |
| Argus     | `Argus`     | `argus@agents.flopbut.pl`     |
| Clio      | `Clio`      | `clio@agents.flopbut.pl`      |
| Vesper    | `Vesper`    | `vesper@agents.flopbut.pl`    |

## 1. Env aliases (author/committer identity)

Each local agent's Paperclip `adapterConfig.env` carries four variables:

```json
"env": {
  "GIT_AUTHOR_NAME":     { "type": "plain", "value": "Wayland" },
  "GIT_AUTHOR_EMAIL":    { "type": "plain", "value": "wayland@agents.flopbut.pl" },
  "GIT_COMMITTER_NAME":  { "type": "plain", "value": "Wayland" },
  "GIT_COMMITTER_EMAIL": { "type": "plain", "value": "wayland@agents.flopbut.pl" }
}
```

`GIT_*` env vars override `git config` in **every** repository, so no per-repo
setup is needed. New values take effect on the agent's **next** run (the current
process inherited its env at spawn).

### Who sets them

Only the board, from the Paperclip UI. The server refuses `adapterConfig.env`
writes from any agent key, including an agent editing itself (403 `Agent keys
cannot configure host-executed local adapter settings (env)`), so agents cannot
self-apply. The `scripts/agent-set-git-identity.sh` helper that used to do this
was removed for that reason.

## 2. Commit trailers

Every commit ends with one co-author trailer naming the committing agent and
the model it runs on in this run, then one line per other agent whose commits
were squashed in:

```
Co-Authored-By: Wayland (Claude Opus 5.5) <wayland@agents.flopbut.pl>
Co-Authored-By: Aldric <aldric@agents.flopbut.pl>
```

The agent determines its model at commit time and writes the line itself.
`Co-Authored-By: Paperclip <noreply@paperclip.ing>` is prohibited by board
order, as is any adapter-injected line (`Cursor`, `GitHub`).

The **`prepare-commit-msg` hook** in `scripts/git-hooks/` is a safety net for
an agent that forgets its line. It looks for a `Co-Authored-By` trailer whose
email equals `$GIT_AUTHOR_EMAIL` (case-insensitive; the name and model part are
ignored). If there is one, it does nothing. If there is none, it appends a bare
`Co-Authored-By: <Name> <email>` — it cannot know the model, so that line still
fails the footer check and should be fixed by amending. The hook only acts for
`@agents.flopbut.*` emails, so human commits are untouched, and it skips merge
and squash messages. Install it per repo:

```bash
scripts/install-agent-git-hooks.sh            # current repo
scripts/install-agent-git-hooks.sh /path/repo # another checkout
```

This points `core.hooksPath` at `scripts/git-hooks`.

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

## 4. Telemetry (OTEL) identity

Each local agent reports its Claude Code telemetry to cotel under its **own**
ingest token, so the dashboard's **Actors** view separates agents instead of
merging them into one bucket. The token is injected exactly like the `GIT_*`
vars — through the agent's Paperclip `adapterConfig.env`:

```json
"env": {
  "OTEL_EXPORTER_OTLP_HEADERS": { "type": "plain", "value": "Authorization=Bearer cotel_<agent-token>" }
}
```

Paperclip writes this into the spawned process environment (visible in
`/proc/<pid>/environ`). Claude Code inherits it at startup, so the agent's spans
carry its own token.

### The hard rule — never hardcode per-agent identity vars in shared `settings.json`

> **Per-agent identity vars (`OTEL_EXPORTER_OTLP_HEADERS`, `GIT_*`) must NEVER
> appear in the shared global `~/.claude/settings.json` `env` block.**

Claude Code reads the `settings.json` `env` block at startup and applies it
**over** the inherited process environment. A token hardcoded there therefore
*clobbers* every agent's injected per-agent token — all telemetry collapses into
that one default bucket. This is exactly the bug [FLO-485](/FLO/issues/FLO-485)
surfaced and [FLO-486](/FLO/issues/FLO-486) fixed: the shared `settings.json`
pinned a default `cotel_654b…` header, so spans from every agent landed under it
despite each process carrying a distinct token in `/proc/<pid>/environ`.

The same precedence trap applies to `GIT_*` — keep those out of the shared
`settings.json` too (they already live only in per-agent `adapterConfig.env`).

### The identity-independent half lives in the Paperclip *environment*

The per-agent header is only half the config. Claude Code exports nothing unless
these five are also present in the run:

```json
"CLAUDE_CODE_ENABLE_TELEMETRY": "1",
"CLAUDE_CODE_ENHANCED_TELEMETRY_BETA": "1",
"OTEL_TRACES_EXPORTER": "otlp",
"OTEL_EXPORTER_OTLP_PROTOCOL": "http/json",
"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "https://otlp.aignite.pl/v1/traces"
```

For agent runs they belong in the **Paperclip environment record's `env_vars`**
(`Local` for this machine), which the adapter merges into the spawn environment
alongside `adapterConfig.env`.

> A Paperclip-spawned run does **not** read the `env` block of the shared
> `~/.claude/settings.json`. Putting the five vars only there covers the board's
> interactive sessions and no agent at all.

That asymmetry produced a silent 28-day outage: `settings.json` carried all five
and each agent carried its own header, yet cotel recorded **zero** agent
sessions — a header alone neither enables telemetry nor names an endpoint.
Adding the five to the `Local` environment restored five agents within minutes.

An environment whose `env_vars` lacks them stays dark no matter what its agents
carry — that is the state of the `robmini` (`ssh` driver) environment.

### Where the human's interactive default lives

The board's **own** interactive `claude` sessions are not spawned by Paperclip,
so they inherit no per-agent token. Their default token is exported from
`~/.bashrc`, below its interactive guard (`case $- in *i*) ;; *) return;; esac`).
That guard is what makes this safe: Paperclip spawns agents with a
non-interactive `bash -c`, which returns before reaching the export, so an agent
never picks up the human default — it keeps its own injected token. Interactive
human shells run the export and report under the default token.

### Checking that agent telemetry actually lands

cotel runs on `robmini`; read it there, read-only, over the loopback port. The
public host sits behind Cloudflare Access and answers 302, and `--db-query`
fights the server for the DuckDB lock.

```bash
ssh robmini 'curl -s "http://localhost:8080/api/v1/sessions?limit=500"'
```

Group the result by `actor_id`: every agent that has run inside the retention
window should appear under its own name. An `actor_id` missing entirely means its
runs never exported — check the environment's `env_vars` first (the whole fleet
is dark) and the agent's own header second (only that agent is dark). Spans that
carry an endpoint but no valid header are rejected at ingest, so a
header-less agent fails silently rather than landing in a default bucket.

## Scope

Repositories in scope: `cotel`, `ksef-docs`, and any new repo under
`~/projects/`. Run `scripts/install-agent-git-hooks.sh` once per repo (or per
worktree) to enable the trailer hook there.

`pi_local` (Orion) is in `error`/trial status; its env aliases are applied last
or skipped until the adapter is healthy.

#!/usr/bin/env bash
#
# install-agent-git-hooks.sh — wire the agent-identity hooks into a repo.
#
# Usage:
#   scripts/install-agent-git-hooks.sh [REPO_DIR]
#
# Points the repo's core.hooksPath at scripts/git-hooks so the
# prepare-commit-msg trailer hook runs on every commit. Idempotent: safe to
# re-run. Works for the current repo (default) or any other checkout passed as
# the first argument. See docs/operations/agent-identity.md.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HOOKS_DIR="${SCRIPT_DIR}/git-hooks"
REPO_DIR="${1:-$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel)}"

if ! git -C "$REPO_DIR" rev-parse --git-dir >/dev/null 2>&1; then
  echo "error: '$REPO_DIR' is not a git repository" >&2
  exit 1
fi

chmod +x "$HOOKS_DIR"/* 2>/dev/null || true
git -C "$REPO_DIR" config core.hooksPath "$HOOKS_DIR"

echo "Installed agent git hooks for $REPO_DIR"
echo "  core.hooksPath -> $HOOKS_DIR"
echo "Verify: GIT_AUTHOR_NAME=Test GIT_AUTHOR_EMAIL=test@agents.flopbut.local git commit --allow-empty -m demo"

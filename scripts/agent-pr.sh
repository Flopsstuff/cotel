#!/usr/bin/env bash
#
# agent-pr.sh — open a PR with FlopBut agent attribution.
#
# Wraps `gh pr create`. Since every PR is opened under the board's token, we
# attribute the authoring agent via:
#   1. an `Agent: <Name>` line appended to the PR body, and
#   2. an `agent:<slug>` label (created idempotently before the PR).
#
# The agent name comes from $GIT_AUTHOR_NAME (set by the Paperclip adapter env).
# See docs/operations/agent-identity.md.
#
# Usage:
#   scripts/agent-pr.sh --title "feat: ..." --body "..." [extra gh pr create flags]
#
# Any flags not consumed here are passed straight through to `gh pr create`.

set -euo pipefail

NAME="${GIT_AUTHOR_NAME:-}"
if [ -z "$NAME" ]; then
  echo "error: GIT_AUTHOR_NAME is not set — cannot attribute the PR to an agent." >&2
  echo "       Run inside a Paperclip agent env, or export GIT_AUTHOR_NAME first." >&2
  exit 1
fi
SLUG="$(printf '%s' "$NAME" | tr '[:upper:]' '[:lower:]')"
LABEL="agent:${SLUG}"

TITLE=""
BODY=""
PASS=()
while [ $# -gt 0 ]; do
  case "$1" in
    --title) TITLE="$2"; shift 2 ;;
    --body)  BODY="$2";  shift 2 ;;
    *)       PASS+=("$1"); shift ;;
  esac
done

# Append the attribution line to the body (idempotent).
ATTR="Agent: ${NAME}"
if ! printf '%s' "$BODY" | grep -qiF "$ATTR"; then
  if [ -n "$BODY" ]; then BODY="${BODY}"$'\n\n'"${ATTR}"; else BODY="${ATTR}"; fi
fi

# Create the label idempotently (-f updates if it already exists).
gh label create "$LABEL" --color BFD4F2 --description "PR authored by agent ${NAME}" -f >/dev/null 2>&1 || true

exec gh pr create --title "$TITLE" --body "$BODY" --label "$LABEL" "${PASS[@]}"

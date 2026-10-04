#!/usr/bin/env bash
#
# page-cotel-health.sh — raise or resolve the standing Paperclip alert for a
# failed /healthz probe. Spends Paperclip budget only on a state change (new
# alert or recovery), not on every green hourly tick.
#
# Usage:
#   scripts/page-cotel-health.sh raise  <probe-output-file>
#   scripts/page-cotel-health.sh resolve
#
# Env: PC_API_URL, PC_API_TOKEN, PC_COMPANY_ID, and optionally
# CF_ACCESS_CLIENT_ID / CF_ACCESS_CLIENT_SECRET (same pair as issue-sync).
# PC_ASSIGNEE_AGENT_ID defaults to Daedalus. GITHUB_RUN_URL is attached
# when the caller is GitHub Actions.

set -euo pipefail

ACTION="${1:-}"
PROBE_OUT="${2:-}"
ORIGIN_ID="${PC_ORIGIN_ID:-cotel-health-probe}"
export ORIGIN_ID
ASSIGNEE="${PC_ASSIGNEE_AGENT_ID:-386b876d-eeba-4bf9-bc10-0dec7b09ee8a}"

if [ -z "${PC_API_URL:-}" ] || [ -z "${PC_API_TOKEN:-}" ] || [ -z "${PC_COMPANY_ID:-}" ]; then
    echo "page-cotel-health: FAILED — PC_API_URL, PC_API_TOKEN, and PC_COMPANY_ID are required"
    exit 1
fi

AUTH_HEADERS=(
    -H "Authorization: Bearer ${PC_API_TOKEN}"
    -H "Content-Type: application/json"
)
if [ -n "${CF_ACCESS_CLIENT_ID:-}" ] && [ -n "${CF_ACCESS_CLIENT_SECRET:-}" ]; then
    AUTH_HEADERS+=(
        -H "CF-Access-Client-Id: ${CF_ACCESS_CLIENT_ID}"
        -H "CF-Access-Client-Secret: ${CF_ACCESS_CLIENT_SECRET}"
    )
fi

pc() {
    curl -sS --max-time 30 "${AUTH_HEADERS[@]}" "$@"
}

find_open() {
    local encoded
    encoded="$(python3 -c "import urllib.parse, os; print(urllib.parse.quote(os.environ['ORIGIN_ID']))")"
    ORIGIN_ID="$ORIGIN_ID" pc "${PC_API_URL}/api/companies/${PC_COMPANY_ID}/issues?q=${encoded}" \
        | python3 -c '
import json, os, sys
raw = sys.stdin.read()
try:
    data = json.loads(raw)
except json.JSONDecodeError:
    sys.exit(0)
items = data if isinstance(data, list) else (data.get("issues") or [])
origin = os.environ["ORIGIN_ID"]
open_status = {"todo", "in_progress", "in_review", "blocked", "backlog"}
for issue in items:
    if issue.get("originId") != origin:
        continue
    if issue.get("status") not in open_status:
        continue
    print(issue.get("id", ""))
    print(issue.get("identifier", ""))
    break
'
}

RUN_LINE=""
if [ -n "${GITHUB_RUN_URL:-}" ]; then
    RUN_LINE="GitHub Actions run: ${GITHUB_RUN_URL}"
fi

case "$ACTION" in
    raise)
        if [ -z "$PROBE_OUT" ] || [ ! -f "$PROBE_OUT" ]; then
            echo "page-cotel-health: FAILED — raise needs a probe output file"
            exit 1
        fi
        reason="$(cat "$PROBE_OUT")"
        mapfile -t FOUND < <(ORIGIN_ID="$ORIGIN_ID" find_open)
        EXISTING_ID="${FOUND[0]:-}"
        EXISTING_IDENT="${FOUND[1]:-}"
        if [ -n "$EXISTING_ID" ]; then
            body="$(printf 'Still red.\n\n```\n%s\n```\n\n%s\n' "$reason" "$RUN_LINE")"
            payload="$(jq -n --arg body "$body" '{body: $body}')"
            pc -X POST "${PC_API_URL}/api/issues/${EXISTING_ID}/comments" -d "$payload" >/dev/null
            echo "page-cotel-health: commented on existing ${EXISTING_IDENT:-$EXISTING_ID}"
            exit 0
        fi
        description="$(printf 'Production cotel /healthz probe is red.\n\n```\n%s\n```\n\n%s\n\nThe hourly probe in Flopsstuff/cotel opened this issue so an agent is woken. Do not treat a red GitHub Actions run as the page — that channel does not wake anyone here.\n' "$reason" "$RUN_LINE")"
        payload="$(jq -n \
            --arg title "cotel prod /healthz is red" \
            --arg description "$description" \
            --arg assigneeAgentId "$ASSIGNEE" \
            --arg originId "$ORIGIN_ID" \
            '{
                title: $title,
                description: $description,
                status: "todo",
                priority: "high",
                assigneeAgentId: $assigneeAgentId,
                originKind: "manual",
                originId: $originId
            }')"
        resp="$(pc -X POST "${PC_API_URL}/api/companies/${PC_COMPANY_ID}/issues" -d "$payload")"
        ident="$(echo "$resp" | jq -r '.identifier // .id // empty')"
        if [ -z "$ident" ]; then
            echo "page-cotel-health: FAILED — create did not return an issue"
            echo "$resp"
            exit 1
        fi
        echo "page-cotel-health: opened ${ident}"
        ;;
    resolve)
        mapfile -t FOUND < <(ORIGIN_ID="$ORIGIN_ID" find_open)
        EXISTING_ID="${FOUND[0]:-}"
        EXISTING_IDENT="${FOUND[1]:-}"
        if [ -z "$EXISTING_ID" ]; then
            echo "page-cotel-health: no open alert"
            exit 0
        fi
        comment="$(printf 'Probe is green again.\n\n%s\n' "$RUN_LINE")"
        payload="$(jq -n --arg comment "$comment" '{status: "done", comment: $comment}')"
        pc -X PATCH "${PC_API_URL}/api/issues/${EXISTING_ID}" -d "$payload" >/dev/null
        echo "page-cotel-health: resolved ${EXISTING_IDENT:-$EXISTING_ID}"
        ;;
    *)
        echo "page-cotel-health: usage: $0 raise <probe-out> | resolve"
        exit 1
        ;;
esac

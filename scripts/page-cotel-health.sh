#!/usr/bin/env bash
#
# page-cotel-health.sh — raise the standing Paperclip alert for a failed
# /healthz probe, or open a recovery notice once the probe is green again.
# Spends Paperclip budget only on a state change (new alert, a comment on the
# open alert, or a recovery notice), not on every green tick.
#
# `create` is the only tracker write this credential can make: a CI job has no
# heartbeat run to attribute a write to an existing issue to. So recovery is a
# second issue asking the alert's assignee to close it, never a PATCH of the
# alert. See docs/decisions/0017-recovery-arrives-as-a-new-issue.md.
#
# Dedup key is the bracketed marker in the title. The create API strips
# originId, and ?q= also matches descriptions and comments, so neither originId
# nor the first search hit identifies the issue.
#
# Usage:
#   scripts/page-cotel-health.sh raise  <probe-output-file>
#   scripts/page-cotel-health.sh resolve
#
# Env: PC_API_URL, PC_API_TOKEN, PC_COMPANY_ID, and optionally
# CF_ACCESS_CLIENT_ID / CF_ACCESS_CLIENT_SECRET (same pair as issue-sync).
# PC_ASSIGNEE_AGENT_ID defaults to Daedalus. PC_ORIGIN_ID defaults to
# cotel-health-probe and PC_RECOVERY_ORIGIN_ID to cotel-health-recovery.
# PC_RUN_ID, when set, is sent as X-Paperclip-Run-Id. GITHUB_RUN_URL is
# attached when the caller is GitHub Actions.

set -euo pipefail

ACTION="${1:-}"
PROBE_OUT="${2:-}"
ORIGIN_ID="${PC_ORIGIN_ID:-cotel-health-probe}"
RECOVERY_ORIGIN_ID="${PC_RECOVERY_ORIGIN_ID:-cotel-health-recovery}"
ASSIGNEE="${PC_ASSIGNEE_AGENT_ID:-386b876d-eeba-4bf9-bc10-0dec7b09ee8a}"
MARKER="[${ORIGIN_ID}]"
RECOVERY_MARKER="[${RECOVERY_ORIGIN_ID}]"

if [ -z "${PC_API_URL:-}" ] || [ -z "${PC_API_TOKEN:-}" ] || [ -z "${PC_COMPANY_ID:-}" ]; then
    echo "page-cotel-health: FAILED — PC_API_URL, PC_API_TOKEN, and PC_COMPANY_ID are required"
    exit 1
fi

if [ -z "$ORIGIN_ID" ] || [ -z "$RECOVERY_ORIGIN_ID" ]; then
    echo "page-cotel-health: FAILED — PC_ORIGIN_ID and PC_RECOVERY_ORIGIN_ID must not be empty"
    exit 1
fi

# Lookup matches [<marker>] in the title only, while ?q= also matches
# descriptions — and a recovery notice quotes the alert's title in its own
# description. That filter is unambiguous only while the two names are
# disjoint, so refuse a pair where either contains the other rather than let
# one kind of issue answer the other's search.
case "$RECOVERY_ORIGIN_ID" in
    *"$ORIGIN_ID"*)
        echo "page-cotel-health: FAILED — PC_RECOVERY_ORIGIN_ID (${RECOVERY_ORIGIN_ID}) must not contain PC_ORIGIN_ID (${ORIGIN_ID})"
        exit 1
        ;;
esac
case "$ORIGIN_ID" in
    *"$RECOVERY_ORIGIN_ID"*)
        echo "page-cotel-health: FAILED — PC_ORIGIN_ID (${ORIGIN_ID}) must not contain PC_RECOVERY_ORIGIN_ID (${RECOVERY_ORIGIN_ID})"
        exit 1
        ;;
esac

AUTH_HEADERS=(
    -H "Authorization: Bearer ${PC_API_TOKEN}"
    -H "Content-Type: application/json"
)
if [ -n "${PC_RUN_ID:-}" ]; then
    AUTH_HEADERS+=(-H "X-Paperclip-Run-Id: ${PC_RUN_ID}")
fi
if [ -n "${CF_ACCESS_CLIENT_ID:-}" ] && [ -n "${CF_ACCESS_CLIENT_SECRET:-}" ]; then
    AUTH_HEADERS+=(
        -H "CF-Access-Client-Id: ${CF_ACCESS_CLIENT_ID}"
        -H "CF-Access-Client-Secret: ${CF_ACCESS_CLIENT_SECRET}"
    )
fi

# Names the request in every failure line. The two searches and the create,
# comment and notice calls answer with overlapping status codes for unrelated
# reasons, so a bare status does not say which of them failed.
PC_CALL="request"
export PC_CALL

# pc prints the response body and fails on a non-2xx status, naming $PC_CALL.
pc() {
    local raw http body
    raw="$(curl -sS --max-time 30 -w '\n%{http_code}' "${AUTH_HEADERS[@]}" "$@")" || {
        echo "page-cotel-health: FAILED — ${PC_CALL}: request error" >&2
        return 1
    }
    http="${raw##*$'\n'}"
    body="${raw%$'\n'*}"
    printf '%s' "$body"
    if [[ ! "$http" =~ ^[0-9]{3}$ ]]; then
        echo "page-cotel-health: FAILED — ${PC_CALL}: no HTTP status from API" >&2
        printf '%s\n' "$raw" >&2
        return 1
    fi
    if [ "$http" -lt 200 ] || [ "$http" -ge 300 ]; then
        echo "page-cotel-health: FAILED — ${PC_CALL}: HTTP ${http}" >&2
        printf '%s\n' "$body" >&2
        return 1
    fi
    return 0
}

# find_open <origin-id> <call-label> — prints id, identifier, assignee and
# title of the one open issue whose title carries [<origin-id>], or nothing.
find_open() {
    local encoded
    PC_FIND_ORIGIN="$1"
    PC_CALL="$2"
    export PC_FIND_ORIGIN
    encoded="$(python3 -c "import urllib.parse, os; print(urllib.parse.quote(os.environ['PC_FIND_ORIGIN']))")"
    pc "${PC_API_URL}/api/companies/${PC_COMPANY_ID}/issues?q=${encoded}&status=todo,in_progress,in_review,blocked,backlog&limit=100" \
        | python3 -c '
import json, os, sys
call = os.environ["PC_CALL"]
raw = sys.stdin.read()
if not raw.strip():
    sys.exit(1)
try:
    data = json.loads(raw)
except json.JSONDecodeError:
    print("page-cotel-health: FAILED — %s: did not return JSON" % call, file=sys.stderr)
    print(raw[:500], file=sys.stderr)
    sys.exit(1)
if isinstance(data, dict) and data.get("error"):
    print("page-cotel-health: FAILED — %s: error body" % call, file=sys.stderr)
    print(raw[:500], file=sys.stderr)
    sys.exit(1)
items = data if isinstance(data, list) else (data.get("issues") or [])
if not isinstance(items, list):
    print("page-cotel-health: FAILED — %s: unexpected shape" % call, file=sys.stderr)
    sys.exit(1)
origin = os.environ["PC_FIND_ORIGIN"]
marker = "[" + origin + "]"
open_status = {"todo", "in_progress", "in_review", "blocked", "backlog"}
for issue in items:
    title = issue.get("title") or ""
    if marker not in title:
        continue
    if issue.get("status") not in open_status:
        continue
    issue_id = issue.get("id") or ""
    if not issue_id:
        continue
    print(issue_id)
    print(issue.get("identifier") or "")
    print(issue.get("assigneeAgentId") or "")
    print(title)
    break
'
}

read_open() {
    local found_raw
    FOUND_ID=""
    FOUND_IDENT=""
    FOUND_ASSIGNEE=""
    FOUND_TITLE=""
    found_raw="$(find_open "$1" "$2")"
    if [ -n "$found_raw" ]; then
        local lines=()
        mapfile -t lines <<<"$found_raw"
        FOUND_ID="${lines[0]:-}"
        FOUND_IDENT="${lines[1]:-}"
        FOUND_ASSIGNEE="${lines[2]:-}"
        FOUND_TITLE="${lines[3]:-}"
    fi
}

RUN_LINE=""
if [ -n "${GITHUB_RUN_URL:-}" ]; then
    RUN_LINE="GitHub Actions run: ${GITHUB_RUN_URL}"
fi

TITLE="cotel prod /healthz is red ${MARKER}"
RECOVERY_TITLE="cotel prod /healthz recovered ${RECOVERY_MARKER}"

case "$ACTION" in
    raise)
        if [ -z "$PROBE_OUT" ] || [ ! -f "$PROBE_OUT" ]; then
            echo "page-cotel-health: FAILED — raise needs a probe output file"
            exit 1
        fi
        reason="$(cat "$PROBE_OUT")"
        read_open "$ORIGIN_ID" "issue search"
        if [ -n "$FOUND_ID" ]; then
            body="$(printf 'Still red.\n\n```\n%s\n```\n\n%s\n' "$reason" "$RUN_LINE")"
            payload="$(jq -cn --arg body "$body" '{body: $body}')"
            PC_CALL="alert comment"
            resp="$(pc -X POST "${PC_API_URL}/api/issues/${FOUND_ID}/comments" -d "$payload")"
            comment_id="$(printf '%s' "$resp" | jq -r '.id // empty')"
            if [ -z "$comment_id" ]; then
                echo "page-cotel-health: FAILED — alert comment: no id in response"
                printf '%s\n' "$resp"
                exit 1
            fi
            echo "page-cotel-health: commented on existing ${FOUND_IDENT:-$FOUND_ID} ${FOUND_ID}"
            exit 0
        fi
        description="$(printf 'Production cotel /healthz probe is red.\n\n```\n%s\n```\n\n%s\n\nThe hourly probe in Flopsstuff/cotel opened this issue so an agent is woken. Do not treat a red GitHub Actions run as the page — that channel does not wake anyone here.\n' "$reason" "$RUN_LINE")"
        payload="$(jq -cn \
            --arg title "$TITLE" \
            --arg description "$description" \
            --arg assigneeAgentId "$ASSIGNEE" \
            '{
                title: $title,
                description: $description,
                status: "todo",
                priority: "high",
                assigneeAgentId: $assigneeAgentId
            }')"
        PC_CALL="issue create"
        resp="$(pc -X POST "${PC_API_URL}/api/companies/${PC_COMPANY_ID}/issues" -d "$payload")"
        ident="$(printf '%s' "$resp" | jq -r '.identifier // empty')"
        issue_id="$(printf '%s' "$resp" | jq -r '.id // empty')"
        got_title="$(printf '%s' "$resp" | jq -r '.title // empty')"
        if [ -z "$ident" ] || [ -z "$issue_id" ]; then
            echo "page-cotel-health: FAILED — issue create: no issue in response"
            printf '%s\n' "$resp"
            exit 1
        fi
        case "$got_title" in
            *"$MARKER"*) ;;
            *)
                echo "page-cotel-health: FAILED — issue create: created title is missing ${MARKER}"
                printf '%s\n' "$resp"
                exit 1
                ;;
        esac
        echo "page-cotel-health: opened ${ident} ${issue_id}"
        ;;
    resolve)
        read_open "$ORIGIN_ID" "issue search"
        if [ -z "$FOUND_ID" ]; then
            echo "page-cotel-health: no open alert"
            exit 0
        fi
        alert_id="$FOUND_ID"
        alert_ident="${FOUND_IDENT:-$FOUND_ID}"
        alert_title="$FOUND_TITLE"
        alert_assignee="${FOUND_ASSIGNEE:-$ASSIGNEE}"

        read_open "$RECOVERY_ORIGIN_ID" "recovery notice search"
        if [ -n "$FOUND_ID" ]; then
            echo "page-cotel-health: recovery notice ${FOUND_IDENT:-$FOUND_ID} ${FOUND_ID} is already open for ${alert_ident} ${alert_id}"
            exit 0
        fi

        description="$(printf '/healthz is green again, and the alert raised for the outage is still open.\n\nAlert: %s (%s) — %s\n\n%s\n\nPlease close the alert as done, then close this notice.\n\nThe hourly probe in Flopsstuff/cotel opened this issue because its tracker credential can only create issues, never write to an existing one — closing the alert needs an agent heartbeat, and you hold it. See docs/decisions/0017-recovery-arrives-as-a-new-issue.md.\n' \
            "$alert_ident" "$alert_id" "$alert_title" "$RUN_LINE")"
        payload="$(jq -cn \
            --arg title "$RECOVERY_TITLE" \
            --arg description "$description" \
            --arg assigneeAgentId "$alert_assignee" \
            '{
                title: $title,
                description: $description,
                status: "todo",
                priority: "high",
                assigneeAgentId: $assigneeAgentId
            }')"
        PC_CALL="recovery notice create"
        resp="$(pc -X POST "${PC_API_URL}/api/companies/${PC_COMPANY_ID}/issues" -d "$payload")"
        ident="$(printf '%s' "$resp" | jq -r '.identifier // empty')"
        issue_id="$(printf '%s' "$resp" | jq -r '.id // empty')"
        got_title="$(printf '%s' "$resp" | jq -r '.title // empty')"
        if [ -z "$ident" ] || [ -z "$issue_id" ]; then
            echo "page-cotel-health: FAILED — recovery notice create: no issue in response"
            printf '%s\n' "$resp"
            exit 1
        fi
        case "$got_title" in
            *"$RECOVERY_MARKER"*) ;;
            *)
                echo "page-cotel-health: FAILED — recovery notice create: created title is missing ${RECOVERY_MARKER}"
                printf '%s\n' "$resp"
                exit 1
                ;;
        esac
        echo "page-cotel-health: opened recovery notice ${ident} ${issue_id} for ${alert_ident} ${alert_id}"
        ;;
    *)
        echo "page-cotel-health: usage: $0 raise <probe-out> | resolve"
        exit 1
        ;;
esac

#!/usr/bin/env bash
#
# page-cotel-health.sh — raise or resolve the standing Paperclip alert for a
# failed /healthz probe. Spends Paperclip budget only on a state change (new
# alert, a comment on the open alert, or recovery), not on every green tick.
#
# Dedup key is the bracketed marker in the title. The create API strips
# originId, and ?q= also matches comments, so neither originId nor the first
# search hit identifies the alert.
#
# Usage:
#   scripts/page-cotel-health.sh raise  <probe-output-file>
#   scripts/page-cotel-health.sh resolve
#
# Env: PC_API_URL, PC_API_TOKEN, PC_COMPANY_ID, and optionally
# CF_ACCESS_CLIENT_ID / CF_ACCESS_CLIENT_SECRET (same pair as issue-sync).
# PC_ASSIGNEE_AGENT_ID defaults to Daedalus. PC_ORIGIN_ID defaults to
# cotel-health-probe. PC_RUN_ID, when set, is sent as X-Paperclip-Run-Id.
# GITHUB_RUN_URL is attached when the caller is GitHub Actions.

set -euo pipefail

ACTION="${1:-}"
PROBE_OUT="${2:-}"
ORIGIN_ID="${PC_ORIGIN_ID:-cotel-health-probe}"
export ORIGIN_ID
ASSIGNEE="${PC_ASSIGNEE_AGENT_ID:-386b876d-eeba-4bf9-bc10-0dec7b09ee8a}"
MARKER="[${ORIGIN_ID}]"

if [ -z "${PC_API_URL:-}" ] || [ -z "${PC_API_TOKEN:-}" ] || [ -z "${PC_COMPANY_ID:-}" ]; then
    echo "page-cotel-health: FAILED — PC_API_URL, PC_API_TOKEN, and PC_COMPANY_ID are required"
    exit 1
fi

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

# Names the request in every failure line. The search, create, comment and
# resolve calls answer with overlapping status codes for unrelated reasons, so
# a bare status does not say which of them failed.
PC_CALL="request"

# pc_into writes the response body to $1 and sets PC_HTTP / PC_BODY in this
# shell. Call it directly — a command substitution would drop those assignments.
pc_into() {
    local dest="$1"
    shift
    local raw http body
    PC_HTTP=""
    PC_BODY=""
    raw="$(curl -sS --max-time 30 -w '\n%{http_code}' "${AUTH_HEADERS[@]}" "$@")" || {
        echo "page-cotel-health: FAILED — ${PC_CALL}: request error" >&2
        return 1
    }
    http="${raw##*$'\n'}"
    body="${raw%$'\n'*}"
    PC_HTTP="$http"
    PC_BODY="$body"
    printf '%s' "$body" >"$dest"
    if [[ ! "$http" =~ ^[0-9]{3}$ ]]; then
        echo "page-cotel-health: FAILED — ${PC_CALL}: no HTTP status from API" >&2
        printf '%s\n' "$raw" >&2
        return 1
    fi
    if [ "$http" -lt 200 ] || [ "$http" -ge 300 ]; then
        if [ "${PC_QUIET_HTTP:-}" != "1" ]; then
            echo "page-cotel-health: FAILED — ${PC_CALL}: HTTP ${http}" >&2
            printf '%s\n' "$body" >&2
        fi
        return 1
    fi
    return 0
}

pc() {
    local tmp rc
    tmp="$(mktemp)"
    set +e
    pc_into "$tmp" "$@"
    rc=$?
    set -e
    cat "$tmp"
    rm -f "$tmp"
    return "$rc"
}

find_open() {
    local encoded
    encoded="$(python3 -c "import urllib.parse, os; print(urllib.parse.quote(os.environ['ORIGIN_ID']))")"
    PC_CALL="issue search"
    pc "${PC_API_URL}/api/companies/${PC_COMPANY_ID}/issues?q=${encoded}&status=todo,in_progress,in_review,blocked,backlog&limit=100" \
        | python3 -c '
import json, os, sys
raw = sys.stdin.read()
if not raw.strip():
    sys.exit(1)
try:
    data = json.loads(raw)
except json.JSONDecodeError:
    print("page-cotel-health: FAILED — issue search: did not return JSON", file=sys.stderr)
    print(raw[:500], file=sys.stderr)
    sys.exit(1)
if isinstance(data, dict) and data.get("error"):
    print("page-cotel-health: FAILED — issue search: error body", file=sys.stderr)
    print(raw[:500], file=sys.stderr)
    sys.exit(1)
items = data if isinstance(data, list) else (data.get("issues") or [])
if not isinstance(items, list):
    print("page-cotel-health: FAILED — issue search: unexpected shape", file=sys.stderr)
    sys.exit(1)
origin = os.environ["ORIGIN_ID"]
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
    break
'
}

read_open() {
    local found_raw
    found_raw="$(find_open)"
    EXISTING_ID=""
    EXISTING_IDENT=""
    if [ -n "$found_raw" ]; then
        EXISTING_ID="${found_raw%%$'\n'*}"
        if [ "$found_raw" = "$EXISTING_ID" ]; then
            EXISTING_IDENT=""
        else
            EXISTING_IDENT="${found_raw#*$'\n'}"
        fi
    fi
}

RUN_LINE=""
if [ -n "${GITHUB_RUN_URL:-}" ]; then
    RUN_LINE="GitHub Actions run: ${GITHUB_RUN_URL}"
fi

TITLE="cotel prod /healthz is red ${MARKER}"

case "$ACTION" in
    raise)
        if [ -z "$PROBE_OUT" ] || [ ! -f "$PROBE_OUT" ]; then
            echo "page-cotel-health: FAILED — raise needs a probe output file"
            exit 1
        fi
        reason="$(cat "$PROBE_OUT")"
        read_open
        if [ -n "$EXISTING_ID" ]; then
            body="$(printf 'Still red.\n\n```\n%s\n```\n\n%s\n' "$reason" "$RUN_LINE")"
            payload="$(jq -cn --arg body "$body" '{body: $body}')"
            PC_CALL="alert comment"
            resp="$(pc -X POST "${PC_API_URL}/api/issues/${EXISTING_ID}/comments" -d "$payload")"
            comment_id="$(printf '%s' "$resp" | jq -r '.id // empty')"
            if [ -z "$comment_id" ]; then
                echo "page-cotel-health: FAILED — alert comment: no id in response"
                printf '%s\n' "$resp"
                exit 1
            fi
            echo "page-cotel-health: commented on existing ${EXISTING_IDENT:-$EXISTING_ID} ${EXISTING_ID}"
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
        read_open
        if [ -z "$EXISTING_ID" ]; then
            echo "page-cotel-health: no open alert"
            exit 0
        fi
        comment="$(printf 'Probe is green again.\n\n%s\n' "$RUN_LINE")"
        payload="$(jq -cn --arg comment "$comment" '{status: "done", comment: $comment}')"
        # A red alert is assigned to someone, which checks it out. The assignee
        # is already awake, so a lost race for that checkout must not fail the
        # job or open another alert.
        PC_CALL="alert resolve"
        PC_QUIET_HTTP=1
        resp_file="$(mktemp)"
        set +e
        pc_into "$resp_file" -X PATCH "${PC_API_URL}/api/issues/${EXISTING_ID}" -d "$payload"
        resolve_rc=$?
        set -e
        PC_QUIET_HTTP=0
        resp="$(cat "$resp_file")"
        rm -f "$resp_file"
        if [ "$resolve_rc" -ne 0 ]; then
            if [ "${PC_HTTP:-}" = "409" ]; then
                echo "page-cotel-health: alert ${EXISTING_IDENT:-$EXISTING_ID} ${EXISTING_ID} is checked out; leaving it open"
                exit 0
            fi
            echo "page-cotel-health: FAILED — alert resolve: HTTP ${PC_HTTP:-unknown}" >&2
            printf '%s\n' "${PC_BODY:-}" >&2
            # Both of these mean "this credential cannot write to an issue at
            # all", not "this alert is unavailable right now": an issue write
            # by an agent needs a heartbeat run to attribute it to, and a CI
            # job has none. Retrying later cannot help.
            case "${PC_HTTP:-}" in
                401 | 403)
                    echo "page-cotel-health: this credential cannot close an alert from CI, and widening it was declined — the close belongs to the assignee's heartbeat. See docs/operations/health-probe.md." >&2
                    ;;
            esac
            exit 1
        fi
        got_status="$(printf '%s' "$resp" | jq -r '.status // empty')"
        if [ "$got_status" != "done" ]; then
            echo "page-cotel-health: FAILED — alert resolve: issue is not done"
            printf '%s\n' "$resp"
            exit 1
        fi
        echo "page-cotel-health: resolved ${EXISTING_IDENT:-$EXISTING_ID} ${EXISTING_ID}"
        ;;
    *)
        echo "page-cotel-health: usage: $0 raise <probe-out> | resolve"
        exit 1
        ;;
esac

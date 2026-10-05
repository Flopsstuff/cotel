#!/usr/bin/env bash
#
# page-cotel-health.sh — raise the standing Paperclip alert for a failed
# /healthz probe, or route its close to the assignee once the probe is green.
# Spends Paperclip budget only on a state change (new alert, or a wake on a
# further red or a recovery), not on every green tick.
#
# This credential may only *create* issues: an agent identity mutating an
# existing issue has to attribute the write to a heartbeat run, and a CI job
# has none. So anything beyond the first create — the close, and the
# still-red update — is routed through a wake carrying payload.issueId, which
# binds the woken run to the alert and lets it write in-ticket. See
# docs/decisions/0021-recovery-wakes-the-alerts-assignee.md.
#
# Nothing else in the wake reaches the woken agent, so the alert's description
# carries the protocol instead — that description always reads red, because
# this credential cannot edit it after the create.
#
# Dedup key is the bracketed marker in the title. The create API strips
# originId, and ?q= also matches comments, so neither originId nor the first
# search hit identifies the alert.
#
# Usage:
#   scripts/page-cotel-health.sh raise  <probe-output-file>
#   scripts/page-cotel-health.sh resolve [<probe-output-file>]
#
# Env: PC_API_URL, PC_API_TOKEN, PC_COMPANY_ID, and optionally
# CF_ACCESS_CLIENT_ID / CF_ACCESS_CLIENT_SECRET (same pair as issue-sync).
# PC_ASSIGNEE_AGENT_ID defaults to Daedalus. PC_ORIGIN_ID defaults to
# cotel-health-probe. PC_RUN_ID, when set, is sent as X-Paperclip-Run-Id.
# PC_ALERT_MAX_AGE_H (default 6) is how long an open alert stays a dedup
# target on the raise path; 0 disables that window.
# GITHUB_RUN_URL, GITHUB_EVENT_NAME, GITHUB_ACTOR and HEALTHZ_URL are quoted
# into a new alert when set, so the reader can place the run without Actions.

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
    print(issue.get("assigneeAgentId") or "")
    created = issue.get("createdAt") or ""
    if not isinstance(created, str):
        created = ""
    print(created)
    break
'
}

read_open() {
    local found_raw
    EXISTING_ID=""
    EXISTING_IDENT=""
    EXISTING_ASSIGNEE=""
    EXISTING_CREATED=""
    found_raw="$(find_open)"
    if [ -n "$found_raw" ]; then
        # No mapfile/readarray: the loopback half runs on macOS, whose
        # /usr/bin/env bash is 3.2.
        {
            IFS= read -r EXISTING_ID || true
            IFS= read -r EXISTING_IDENT || true
            IFS= read -r EXISTING_ASSIGNEE || true
            IFS= read -r EXISTING_CREATED || true
        } <<EOF
$found_raw
EOF
    fi
}

# True when the open alert is older than PC_ALERT_MAX_AGE_H hours. A missing
# or unparseable timestamp is not stale: a parse failure must not open a
# second alert. Zero hours disables the window, so a drill always dedups.
alert_is_stale() {
    local created="$1" hours verdict
    hours="${PC_ALERT_MAX_AGE_H:-6}"
    case "$hours" in
        *[!0-9]*)
            echo "page-cotel-health: FAILED — PC_ALERT_MAX_AGE_H must be a whole number of hours" >&2
            exit 1
            ;;
    esac
    # "06" is six and "00" is off. The log line prints this, so one form.
    while [ "$hours" != "0" ] && [ "${hours#0}" != "$hours" ]; do
        hours="${hours#0}"
    done
    MAX_AGE_H="$hours"
    if [ "$hours" = "0" ]; then
        return 1
    fi
    # jq, not date: the loopback runner is BSD date, and fromdateiso8601
    # rejects the fractional seconds the API sends.
    verdict="$(jq -nr --arg ts "$created" --arg hours "$hours" '
        def epoch:
            gsub("\\.[0-9]+"; "") | fromdateiso8601;
        ($ts | try epoch catch null) as $created
        | if $created == null then "fresh"
          elif (now - $created) > (($hours | tonumber) * 3600) then "stale"
          else "fresh"
          end
    ')" || verdict="fresh"
    [ "$verdict" = "stale" ]
}

# wake_assignee <idempotency-key> <reason> <payload-json> — routes a write the
# CI credential cannot make into a heartbeat run that can. Not an issue write,
# so it needs no run id; but the API only lets a key wake its own agent, which
# is why an alert assigned elsewhere is reported rather than stepped over.
wake_assignee() {
    local key="$1" reason="$2" payload="$3" body resp resp_file rc status run_id
    body="$(jq -cn \
        --arg reason "$reason" \
        --arg key "$key" \
        --argjson payload "$payload" \
        '{
            source: "automation",
            reason: $reason,
            payload: $payload,
            idempotencyKey: $key,
            forceFreshSession: false
        }')"
    PC_CALL="alert wake"
    PC_QUIET_HTTP=1
    resp_file="$(mktemp)"
    set +e
    pc_into "$resp_file" -X POST "${PC_API_URL}/api/agents/${EXISTING_ASSIGNEE}/wakeup" -d "$body"
    rc=$?
    set -e
    PC_QUIET_HTTP=0
    resp="$(cat "$resp_file")"
    rm -f "$resp_file"
    if [ "$rc" -ne 0 ]; then
        if [ "${PC_HTTP:-}" = "403" ]; then
            echo "page-cotel-health: FAILED — alert wake: HTTP 403, alert ${EXISTING_IDENT:-$EXISTING_ID} is assigned to an agent this credential cannot wake" >&2
        else
            echo "page-cotel-health: FAILED — alert wake: HTTP ${PC_HTTP:-unknown}" >&2
        fi
        printf '%s\n' "${PC_BODY:-}" >&2
        return 1
    fi
    # skipped is success: a run is already live for that agent, and a live run
    # reads current state rather than the state at wake time.
    # A started wake answers with the run itself; a skipped one with a status
    # envelope. Only the latter carries status=skipped.
    status="$(printf '%s' "$resp" | jq -r '.status // empty')"
    run_id="$(printf '%s' "$resp" | jq -r '.id // empty')"
    if [ "$status" = "skipped" ]; then
        echo "page-cotel-health: woke ${EXISTING_ASSIGNEE} for ${EXISTING_IDENT:-$EXISTING_ID} ${EXISTING_ID} — a run was already live"
    else
        echo "page-cotel-health: woke ${EXISTING_ASSIGNEE} for ${EXISTING_IDENT:-$EXISTING_ID} ${EXISTING_ID}${run_id:+ (run ${run_id})}"
    fi
    return 0
}

# `issueId` is what scopes the wake to the alert — without it the woken run
# starts with no ticket in hand and has to rediscover why it is awake. The
# remaining fields are recorded on the wake request; the one line the woken run
# is guaranteed to read is the `reason`, so put the verdict there too.
wake_payload() {
    local kind="$1" instruction="$2" probe="$3"
    jq -cn \
        --arg issueId "$EXISTING_ID" \
        --arg kind "$kind" \
        --arg alertIdentifier "${EXISTING_IDENT:-}" \
        --arg probeOutput "$probe" \
        --arg githubRunUrl "${GITHUB_RUN_URL:-}" \
        --arg instruction "$instruction" \
        '{
            issueId: $issueId,
            kind: $kind,
            alertIdentifier: $alertIdentifier,
            probeOutput: $probeOutput,
            githubRunUrl: $githubRunUrl,
            instruction: $instruction
        }'
}

RUN_LINE=""
RUN_SUFFIX=""
# Named in the alert so the woken agent re-checks the same endpoint. A bare
# curl is not the check: the probe also classifies 503, stale and empty ingest.
# And this URL may be the deploy host's own loopback, reachable only from the
# runner — which is why the re-probe is a dispatch, not a local command.
PROBED_URL="${HEALTHZ_URL:-the URL in the probe output above}"
if [ -n "${GITHUB_RUN_URL:-}" ]; then
    RUN_LINE="GitHub Actions run: ${GITHUB_RUN_URL}"
    RUN_SUFFIX=" (${GITHUB_RUN_URL})"
fi

# The woken reader must be able to tell an exercise from an outage without
# opening Actions: a dispatched run against an overridden URL is a drill, the
# hourly schedule against the real endpoint is not. The probe text alone does
# not say — a closed-port drill and a dead process produce the same line.
CONTEXT_BLOCK="Dedup marker: ${MARKER}"
if [ -n "${GITHUB_EVENT_NAME:-}" ]; then
    CONTEXT_BLOCK="${CONTEXT_BLOCK}
Triggered by: ${GITHUB_EVENT_NAME}${GITHUB_ACTOR:+ (${GITHUB_ACTOR})}"
fi
if [ -n "${HEALTHZ_URL:-}" ]; then
    CONTEXT_BLOCK="${CONTEXT_BLOCK}
Probe URL: ${HEALTHZ_URL}"
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
        STALE_ID=""
        STALE_IDENT=""
        if [ -n "$EXISTING_ID" ]; then
            if alert_is_stale "$EXISTING_CREATED"; then
                # Not a dedup target, and not a write. The open alert stays
                # as it is; the fresh create below is the wake.
                STALE_ID="$EXISTING_ID"
                STALE_IDENT="$EXISTING_IDENT"
            else
                # Bucketed on a coarse window rather than on this run, so a
                # multi-day outage spends about four heartbeats a day instead of
                # twenty-four. The comment this replaces was refused by the same
                # gate as the resolve, so a second red hour used to wake nobody.
                bucket=$(( $(date +%s) / 21600 ))
                payload="$(wake_payload "cotel_health_still_red" \
                    "Production /healthz is still red and this alert is already open. Add the probe output below to the alert; do not open another." \
                    "$reason")"
                wake_assignee \
                    "cotel-health-still-red:${EXISTING_ID}:${bucket}" \
                    "cotel prod /healthz is still red${RUN_SUFFIX} — alert ${EXISTING_IDENT:-$EXISTING_ID} is already open; add the probe output to it" \
                    "$payload"
                exit 0
            fi
        fi
        description="$(printf 'Production cotel /healthz probe is red.\n\n```\n%s\n```\n\n%s\n\n%s\n\nThe hourly probe in Flopsstuff/cotel opened this issue so an agent is woken. Do not treat a red GitHub Actions run as the page — that channel does not wake anyone here.\n\n## If you are woken on this issue again, re-probe before acting\n\n**This description always reads red.** The probe wrote it once, when the outage was detected, and cannot edit it afterwards; the wake that brought you here carries no probe output either. So nothing above tells you the state of production right now — check it yourself before you act.\n\nRe-probe by dispatching **Health probe** in Flopsstuff/cotel with **page unchecked** (Actions → Health probe → Run workflow), and read its verdict. Do not just `curl` the URL: the probe also classifies 503, stale ingest and an empty database, and `%s` may be the deploy host'"'"'s own loopback, which only that runner can reach. Never pass a `loopback_url` override while checking a real alert — that probes something else.\n\n- **Green** — the outage is over. Close this issue as done, citing the run you probed with.\n- **Still red** — the outage continues. Add that run'"'"'s output here as the current evidence.\n' "$reason" "$CONTEXT_BLOCK" "$RUN_LINE" "$PROBED_URL")"
        # Same-title creates collapse into a recent open issue unless this
        # asks not to. The stale alert is that issue, so without the opt-out
        # the response is the alert we just refused to dedup into.
        allow_duplicate=false
        if [ -n "$STALE_ID" ]; then
            allow_duplicate=true
        fi
        payload="$(jq -cn \
            --arg title "$TITLE" \
            --arg description "$description" \
            --arg assigneeAgentId "$ASSIGNEE" \
            --argjson allowDuplicate "$allow_duplicate" \
            '{
                title: $title,
                description: $description,
                status: "todo",
                priority: "high",
                assigneeAgentId: $assigneeAgentId
            } + (if $allowDuplicate then {allowDuplicate: true} else {} end)')"
        PC_CALL="issue create"
        resp="$(pc -X POST "${PC_API_URL}/api/companies/${PC_COMPANY_ID}/issues" -d "$payload")"
        ident="$(printf '%s' "$resp" | jq -r '.identifier // empty')"
        issue_id="$(printf '%s' "$resp" | jq -r '.id // empty')"
        got_title="$(printf '%s' "$resp" | jq -r '.title // empty')"
        deduped="$(printf '%s' "$resp" | jq -r '.deduplicated // false')"
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
        if [ -n "$STALE_ID" ] && { [ "$issue_id" = "$STALE_ID" ] || [ "$deduped" = "true" ]; }; then
            echo "page-cotel-health: FAILED — issue create: tracker returned the open alert ${STALE_IDENT:-$STALE_ID} instead of a new one" >&2
            printf '%s\n' "$resp" >&2
            exit 1
        fi
        if [ -n "$STALE_ID" ]; then
            echo "page-cotel-health: ${STALE_IDENT:-$STALE_ID} ${STALE_ID} is older than ${MAX_AGE_H}h, so it is not the dedup target; opened ${ident} ${issue_id}"
        else
            echo "page-cotel-health: opened ${ident} ${issue_id}"
        fi
        ;;
    resolve)
        read_open
        if [ -z "$EXISTING_ID" ]; then
            echo "page-cotel-health: no open alert"
            exit 0
        fi
        green=""
        if [ -n "$PROBE_OUT" ] && [ -f "$PROBE_OUT" ]; then
            green="$(cat "$PROBE_OUT")"
        fi
        payload="$(wake_payload "cotel_health_recovery" \
            "Production /healthz is green again. Close this alert as done, with the green run URL in the closing comment." \
            "$green")"
        wake_assignee \
            "cotel-health-recovery:${EXISTING_ID}:${GITHUB_RUN_ID:-manual}" \
            "cotel prod /healthz recovered${RUN_SUFFIX} — close alert ${EXISTING_IDENT:-$EXISTING_ID} as done" \
            "$payload"
        ;;
    *)
        echo "page-cotel-health: usage: $0 raise <probe-out> | resolve [<probe-out>]"
        exit 1
        ;;
esac

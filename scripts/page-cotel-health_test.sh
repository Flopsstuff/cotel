#!/usr/bin/env bash
#
# page-cotel-health_test.sh — drive page-cotel-health.sh against a fake curl.
# The live API is not contacted. Covers title-marker dedup (a comment-only
# search hit is not the alert, a done issue with the marker is not open, a
# longer marker does not satisfy a shorter one) and the wake that replaced
# every write to an existing issue: its target, key, payload, the 202 skipped
# success, and the self-wake-only 403.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PAGE="$ROOT/scripts/page-cotel-health.sh"
PASS=0
FAIL=0

fail() {
    echo "FAIL: $*"
    FAIL=$((FAIL + 1))
}

pass() {
    echo "PASS: $*"
    PASS=$((PASS + 1))
}

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

FAKE="$TMP/curl"
cat >"$FAKE" <<'EOF'
#!/usr/bin/env bash
method="GET"
data=""
url=""
prev=""
for arg in "$@"; do
    case "$prev" in
        -X) method="$arg"; prev=""; continue ;;
        -d) data="$arg"; prev=""; continue ;;
        -H|--max-time|-w) prev=""; continue ;;
    esac
    case "$arg" in
        -X|-d|-H|--max-time|-w) prev="$arg"; continue ;;
        -s|-S|-sS|-f|--fail|--silent|--show-error) continue ;;
        http*) url="$arg"; continue ;;
    esac
done
printf '%s %s\n' "$method" "$url" >>"${PAGE_TEST_LOG:?}"
if [ -n "$data" ]; then
    printf '%s\n' "$data" >>"$PAGE_TEST_LOG"
fi
code=200
body='{}'
case "$method" in
    GET)
        if [ "${PAGE_TEST_SEARCH_STATUS:-200}" != "200" ]; then
            code="$PAGE_TEST_SEARCH_STATUS"
            body='{"error":"search failed"}'
        else
            body="$(cat "${PAGE_TEST_FIXTURE:?}")"
        fi
        ;;
    POST)
        if [[ "$url" == *"/wakeup" ]]; then
            code="${PAGE_TEST_WAKE_STATUS:-202}"
            case "$code" in
                202)
                    if [ "${PAGE_TEST_WAKE_SKIPPED:-}" = "1" ]; then
                        body='{"status":"skipped","reason":"run_already_active"}'
                    else
                        body='{"status":"queued","runId":"run-7"}'
                    fi
                    ;;
                403) body='{"error":"Agent can only invoke itself"}' ;;
                *) body='{"error":"wake failed"}' ;;
            esac
        elif [[ "$url" == *"/comments" ]]; then
            code=201
            body='{"id":"comment-1"}'
        else
            code=201
            body="$(cat "${PAGE_TEST_CREATE_BODY:?}")"
        fi
        ;;
esac
printf '%s\n%s' "$body" "$code"
EOF
chmod +x "$FAKE"

PROBE_FILE="$TMP/probe.out"
printf '%s\n' "probe-healthz: FAILED — unreachable (connection refused)" >"$PROBE_FILE"

export PATH="$TMP:$PATH"
export PC_API_URL="http://paperclip.test"
export PC_API_TOKEN="test-token"
export PC_COMPANY_ID="company-1"
export PC_ORIGIN_ID="cotel-health-probe"
export PC_RUN_ID=""
unset CF_ACCESS_CLIENT_ID CF_ACCESS_CLIENT_SECRET GITHUB_RUN_URL PC_ASSIGNEE_AGENT_ID || true

write_fixture() {
    cat >"$TMP/fixture.json"
}

run_page() {
    PAGE_TEST_LOG="$TMP/log" \
    PAGE_TEST_FIXTURE="$TMP/fixture.json" \
    PAGE_TEST_CREATE_BODY="$TMP/create.json" \
    PAGE_TEST_SEARCH_STATUS="${1:-200}" \
        bash "$PAGE" "${@:2}"
}

# wake_payload prints the JSON body of the last POST to a /wakeup URL.
wake_body() {
    python3 - "$TMP/log" <<'PY'
import sys
log = open(sys.argv[1]).read().splitlines()
body = None
for i, line in enumerate(log):
    if line.startswith("POST ") and line.rstrip().endswith("/wakeup") and i + 1 < len(log):
        body = log[i + 1]
if body is None:
    raise SystemExit("no wake body in log")
print(body)
PY
}

reset_log() {
    : >"$TMP/log"
    printf '%s\n' '{"id":"new-id","identifier":"ALT-9","title":"cotel prod /healthz is red [cotel-health-probe]"}' >"$TMP/create.json"
}

assert_log_lacks() {
    local name="$1" needle="$2"
    if grep -qF -- "$needle" "$TMP/log"; then
        fail "$name — log contained '$needle'"
    else
        pass "$name"
    fi
}

assert_log_has() {
    local name="$1" needle="$2"
    if grep -qF -- "$needle" "$TMP/log"; then
        pass "$name"
    else
        fail "$name — log missing '$needle'"
        echo "----- log -----"
        cat "$TMP/log"
    fi
}

assert_err_has() {
    local name="$1" needle="$2"
    if grep -qF -- "$needle" "$TMP/err"; then
        pass "$name"
    else
        fail "$name — stderr missing '$needle'"
        echo "----- stderr -----"
        cat "$TMP/err"
    fi
}

# 1. Comment-only hit (marker in description, not title) creates a new issue.
write_fixture <<'JSON'
[
  {
    "id": "thread-id",
    "identifier": "ALT-THREAD",
    "title": "health probe follow-up",
    "status": "in_progress",
    "originId": null,
    "description": "mentions [cotel-health-probe] in the body only"
  }
]
JSON
reset_log
out="$(run_page 200 raise "$PROBE_FILE" 2>"$TMP/err")" || { fail "create past comment hit — exit $?"; cat "$TMP/err"; }
case "$out" in
    "page-cotel-health: opened ALT-9 new-id") pass "create past comment hit" ;;
    *) fail "create past comment hit — output: $out" ;;
esac
assert_log_lacks "create does not comment on the comment-hit" "POST http://paperclip.test/api/issues/thread-id/comments"
assert_log_has "create posts a company issue" "POST http://paperclip.test/api/companies/company-1/issues"
python3 - "$TMP/log" <<'PY'
import json, sys
log = open(sys.argv[1]).read().splitlines()
payload = None
for i, line in enumerate(log):
    if line.startswith("POST ") and "/comments" not in line and i + 1 < len(log):
        payload = json.loads(log[i + 1])
if payload is None:
    raise SystemExit("no create payload")
bad = []
if "originId" in payload or "originKind" in payload:
    bad.append("origin fields present")
title = payload.get("title") or ""
if title != "cotel prod /healthz is red [cotel-health-probe]":
    bad.append("title=" + title)
if payload.get("assigneeAgentId") != "386b876d-eeba-4bf9-bc10-0dec7b09ee8a":
    bad.append("assignee=" + str(payload.get("assigneeAgentId")))
if bad:
    raise SystemExit("; ".join(bad))
PY
pass "create payload is title marker, default assignee, no origin fields"

# 2. A red hour with the alert already open wakes its assignee. The comment it
# replaces was refused by the same gate as the resolve, so it never worked.
# A comment-hit listed first is still ignored.
write_fixture <<'JSON'
[
  {
    "id": "thread-id",
    "identifier": "ALT-THREAD",
    "title": "health probe follow-up",
    "status": "in_progress",
    "description": "[cotel-health-probe]"
  },
  {
    "id": "alert-id",
    "identifier": "ALT-1",
    "title": "cotel prod /healthz is red [cotel-health-probe]",
    "status": "todo",
    "originId": null,
    "assigneeAgentId": "agent-on-call"
  }
]
JSON
reset_log
out="$(run_page 200 raise "$PROBE_FILE" 2>"$TMP/err")" || { fail "still-red wake — exit $?"; cat "$TMP/err"; }
case "$out" in
    "page-cotel-health: woke agent-on-call for ALT-1 alert-id (run run-7)")
        pass "second raise wakes the alert's assignee"
        ;;
    *) fail "second raise wakes the alert's assignee — output: $out" ;;
esac
assert_log_has "wake posts to the assignee" "POST http://paperclip.test/api/agents/agent-on-call/wakeup"
assert_log_lacks "second raise does not create" "POST http://paperclip.test/api/companies/company-1/issues"
assert_log_lacks "second raise does not comment" "/comments"
assert_log_lacks "second raise never patches" "PATCH "
python3 - "$(wake_body)" <<'PY'
import json, sys, time
body = json.loads(sys.argv[1])
bad = []
if body.get("source") != "automation":
    bad.append("source=" + str(body.get("source")))
if "forceFreshSession" not in body:
    bad.append("forceFreshSession missing (the API requires it)")
key = body.get("idempotencyKey") or ""
want = "cotel-health-still-red:alert-id:%d" % (int(time.time()) // 21600)
if key != want:
    bad.append("idempotencyKey=%s wanted %s" % (key, want))
p = body.get("payload") or {}
if p.get("kind") != "cotel_health_still_red":
    bad.append("kind=" + str(p.get("kind")))
if p.get("alertIdentifier") != "ALT-1" or p.get("alertIssueId") != "alert-id":
    bad.append("alert fields=%r/%r" % (p.get("alertIdentifier"), p.get("alertIssueId")))
if "connection refused" not in (p.get("probeOutput") or ""):
    bad.append("probeOutput=" + str(p.get("probeOutput")))
if bad:
    raise SystemExit("; ".join(bad))
PY
pass "still-red wake carries source, a bucketed key, and the alert plus probe output"

# 3. A done issue with the marker is not an open alert.
write_fixture <<'JSON'
[
  {
    "id": "old-id",
    "identifier": "ALT-OLD",
    "title": "cotel prod /healthz is red [cotel-health-probe]",
    "status": "done"
  }
]
JSON
reset_log
out="$(run_page 200 raise "$PROBE_FILE")"
case "$out" in
    "page-cotel-health: opened ALT-9 new-id") pass "done marker does not block a new alert" ;;
    *) fail "done marker does not block a new alert — output: $out" ;;
esac

# 4. A longer marker is not this alert.
write_fixture <<'JSON'
[
  {
    "id": "selftest-id",
    "identifier": "ALT-SELF",
    "title": "cotel prod /healthz is red [cotel-health-probe-selftest]",
    "status": "todo"
  }
]
JSON
reset_log
out="$(run_page 200 raise "$PROBE_FILE")"
case "$out" in
    "page-cotel-health: opened ALT-9 new-id") pass "longer marker is a different alert" ;;
    *) fail "longer marker is a different alert — output: $out" ;;
esac
assert_log_lacks "does not comment on the longer marker" "POST http://paperclip.test/api/issues/selftest-id/comments"

# 5. A green hour with an alert open wakes that alert's assignee, and nothing
# else: no PATCH, no comment, no second issue.
write_fixture <<'JSON'
[
  {
    "id": "thread-id",
    "identifier": "ALT-THREAD",
    "title": "health probe follow-up",
    "status": "in_progress"
  },
  {
    "id": "alert-id",
    "identifier": "ALT-1",
    "title": "cotel prod /healthz is red [cotel-health-probe]",
    "status": "in_progress",
    "assigneeAgentId": "agent-on-call"
  }
]
JSON
reset_log
GREEN_FILE="$TMP/green.out"
printf '%s\n' "probe-healthz: OK — HTTP 200 ingest age 4s" >"$GREEN_FILE"
export GITHUB_RUN_ID="4242"
out="$(run_page 200 resolve "$GREEN_FILE" 2>"$TMP/err")" || { fail "recovery wake — exit $?"; cat "$TMP/err"; }
case "$out" in
    "page-cotel-health: woke agent-on-call for ALT-1 alert-id (run run-7)")
        pass "green hour wakes the alert's assignee"
        ;;
    *) fail "green hour wakes the alert's assignee — output: $out" ;;
esac
assert_log_has "wake posts to the alert's assignee" "POST http://paperclip.test/api/agents/agent-on-call/wakeup"
assert_log_lacks "recovery never patches" "PATCH "
assert_log_lacks "recovery never comments" "/comments"
assert_log_lacks "recovery creates no issue" "POST http://paperclip.test/api/companies/company-1/issues"
assert_log_lacks "recovery does not wake the comment hit's owner" "/api/agents/thread-id/"
python3 - "$(wake_body)" <<'PY2'
import json, sys
body = json.loads(sys.argv[1])
bad = []
if body.get("source") != "automation":
    bad.append("source=" + str(body.get("source")))
if "forceFreshSession" not in body:
    bad.append("forceFreshSession missing (the API requires it)")
if body.get("idempotencyKey") != "cotel-health-recovery:alert-id:4242":
    bad.append("idempotencyKey=" + str(body.get("idempotencyKey")))
if "recovered" not in (body.get("reason") or ""):
    bad.append("reason=" + str(body.get("reason")))
p = body.get("payload") or {}
if p.get("kind") != "cotel_health_recovery":
    bad.append("kind=" + str(p.get("kind")))
if p.get("alertIdentifier") != "ALT-1" or p.get("alertIssueId") != "alert-id":
    bad.append("alert fields=%r/%r" % (p.get("alertIdentifier"), p.get("alertIssueId")))
if "HTTP 200" not in (p.get("probeOutput") or ""):
    bad.append("probeOutput=" + str(p.get("probeOutput")))
if "Close this alert as done" not in (p.get("instruction") or ""):
    bad.append("instruction=" + str(p.get("instruction")))
if bad:
    raise SystemExit("; ".join(bad))
PY2
pass "recovery wake carries the run-scoped key, the alert, the green probe output and the ask"

# The key is derived from the alert and the green run, so a re-dispatch of the
# same run cannot mint a second heartbeat.
reset_log
out="$(run_page 200 resolve "$GREEN_FILE" 2>"$TMP/err")" || { fail "repeat recovery wake — exit $?"; cat "$TMP/err"; }
python3 - "$(wake_body)" <<'PY2'
import json, sys
body = json.loads(sys.argv[1])
if body.get("idempotencyKey") != "cotel-health-recovery:alert-id:4242":
    raise SystemExit("idempotencyKey=" + str(body.get("idempotencyKey")))
PY2
pass "a retried job sends the same idempotencyKey"
unset GITHUB_RUN_ID

# 6. 202 skipped is success: a run is already live, and a live run reads
# current state, which is green.
reset_log
out="$(export PAGE_TEST_WAKE_SKIPPED=1; run_page 200 resolve "$GREEN_FILE" 2>"$TMP/err")" || { fail "skipped wake — exit $?"; cat "$TMP/err"; }
case "$out" in
    "page-cotel-health: woke agent-on-call for ALT-1 alert-id — a run was already live")
        pass "202 skipped is success and says so"
        ;;
    *) fail "202 skipped is success — output: $out" ;;
esac

# 7. An alert assigned to an agent this credential cannot wake is reported by
# name, not stepped over: the API allows self-wake only.
reset_log
set +e
out="$(export PAGE_TEST_WAKE_STATUS=403; run_page 200 resolve "$GREEN_FILE" 2>"$TMP/err")"
rc=$?
set -e
if [ "$rc" -eq 0 ]; then
    fail "403 wake exited 0: $out"
else
    pass "403 wake exits non-zero"
fi
assert_err_has "403 wake names the call and the status" "alert wake: HTTP 403"
assert_err_has "403 wake names the alert" "alert ALT-1 is assigned to an agent this credential cannot wake"

# A wake failure for any other reason names the call and the status, and is not
# confusable with a failed search.
reset_log
set +e
out="$(export PAGE_TEST_WAKE_STATUS=500; run_page 200 resolve "$GREEN_FILE" 2>"$TMP/err")"
rc=$?
set -e
if [ "$rc" -eq 0 ]; then
    fail "500 wake exited 0: $out"
else
    pass "500 wake exits non-zero"
fi
assert_err_has "500 wake names the call" "alert wake: HTTP 500"
assert_log_lacks "a failed wake writes nothing else" "PATCH "

# 8. A green hour with no open alert writes nothing and wakes nobody.
write_fixture <<'JSON'
[
  {
    "id": "old-id",
    "identifier": "ALT-OLD",
    "title": "cotel prod /healthz is red [cotel-health-probe]",
    "status": "done"
  },
  {
    "id": "thread-id",
    "identifier": "ALT-THREAD",
    "title": "health probe follow-up",
    "status": "in_progress",
    "description": "[cotel-health-probe]"
  }
]
JSON
reset_log
out="$(run_page 200 resolve 2>"$TMP/err")" || { fail "green with no alert — exit $?"; cat "$TMP/err"; }
case "$out" in
    "page-cotel-health: no open alert") pass "green hour with no open alert does nothing" ;;
    *) fail "green hour with no open alert — output: $out" ;;
esac
assert_log_lacks "no alert means no write" "POST "
assert_log_lacks "no alert means no patch" "PATCH "

# 9. A failed search must not create an issue or wake anyone.
write_fixture <<'JSON'
[]
JSON
reset_log
set +e
out="$(run_page 500 raise "$PROBE_FILE" 2>"$TMP/err")"
rc=$?
set -e
if [ "$rc" -eq 0 ]; then
    fail "search failure still exited 0: $out"
else
    pass "search failure exits non-zero"
fi
assert_log_lacks "search failure does not create" "POST "
assert_err_has "search failure names the search" "issue search: HTTP 500"

reset_log
set +e
out="$(run_page 500 resolve 2>"$TMP/err")"
rc=$?
set -e
if [ "$rc" -eq 0 ]; then
    fail "resolve search failure exited 0: $out"
else
    pass "resolve search failure exits non-zero"
fi
assert_log_lacks "resolve search failure wakes nobody" "POST "

# 10. Usage and missing probe file.
set +e
out="$(bash "$PAGE" 2>"$TMP/err")"
rc=$?
set -e
if [ "$rc" -eq 0 ]; then
    fail "missing action exited 0"
else
    pass "missing action exits non-zero"
fi

echo
echo "passed=$PASS failed=$FAIL"
if [ "$FAIL" -ne 0 ]; then
    exit 1
fi

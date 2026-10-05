#!/usr/bin/env bash
#
# page-cotel-health_test.sh — drive page-cotel-health.sh against a fake curl.
# The live API is not contacted. Covers title-marker dedup: a comment-only
# search hit is not the alert, a done issue with the marker is not open,
# and a longer marker does not satisfy a shorter one.
#
# The fake answers every search with the same fixture list, so each lookup also
# sees the other kind of issue — which is what makes the title-marker filter
# the thing under test rather than the query string.

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
        q=""
        case "$url" in
            *"q="*) q="${url#*q=}"; q="${q%%&*}" ;;
        esac
        if [ "${PAGE_TEST_FAIL_RECOVERY_SEARCH:-}" = "1" ] && [ "$q" = "cotel-health-recovery" ]; then
            code=500
            body='{"error":"search failed"}'
        elif [ "${PAGE_TEST_SEARCH_STATUS:-200}" != "200" ]; then
            code="$PAGE_TEST_SEARCH_STATUS"
            body='{"error":"search failed"}'
        else
            body="$(cat "${PAGE_TEST_FIXTURE:?}")"
        fi
        ;;
    POST)
        if [[ "$url" == *"/comments" ]]; then
            code=201
            body='{"id":"comment-1"}'
        else
            code=201
            # Echo the requested title back, so a create response can never
            # carry a marker the request did not ask for.
            title="$(printf '%s' "$data" | jq -r '.title // empty')"
            body="$(jq -c --arg t "$title" '.title = $t' "${PAGE_TEST_CREATE_BODY:?}")"
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
export PC_RECOVERY_ORIGIN_ID="cotel-health-recovery"
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

reset_log() {
    : >"$TMP/log"
    printf '%s\n' '{"id":"new-id","identifier":"ALT-9"}' >"$TMP/create.json"
}

# The created-issue id/identifier the fake hands back for the next create.
set_create_ids() {
    jq -cn --arg id "$1" --arg ident "$2" '{id: $id, identifier: $ident}' >"$TMP/create.json"
}

# create_payload prints the JSON body of the last non-comment POST in the log.
create_payload() {
    python3 - "$TMP/log" <<'PY'
import sys
log = open(sys.argv[1]).read().splitlines()
payload = None
for i, line in enumerate(log):
    if line.startswith("POST ") and "/comments" not in line and i + 1 < len(log):
        payload = log[i + 1]
if payload is None:
    raise SystemExit("no create payload in log")
print(payload)
PY
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

assert_log_count() {
    local name="$1" want="$2" needle="$3" got
    got="$(grep -cF -- "$needle" "$TMP/log" || true)"
    if [ "$got" = "$want" ]; then
        pass "$name"
    else
        fail "$name — saw $got of '$needle', wanted $want"
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
python3 - "$(create_payload)" <<'PY'
import json, sys
payload = json.loads(sys.argv[1])
bad = []
if "originId" in payload or "originKind" in payload:
    bad.append("origin fields present")
if payload.get("title") != "cotel prod /healthz is red [cotel-health-probe]":
    bad.append("title=" + str(payload.get("title")))
if payload.get("assigneeAgentId") != "386b876d-eeba-4bf9-bc10-0dec7b09ee8a":
    bad.append("assignee=" + str(payload.get("assigneeAgentId")))
if bad:
    raise SystemExit("; ".join(bad))
PY
pass "create payload is title marker, default assignee, no origin fields"

# 2. Open title match is commented; a comment-hit listed first is ignored.
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
    "originId": null
  }
]
JSON
reset_log
out="$(run_page 200 raise "$PROBE_FILE")"
case "$out" in
    "page-cotel-health: commented on existing ALT-1 alert-id") pass "second raise comments on title match" ;;
    *) fail "second raise comments on title match — output: $out" ;;
esac
assert_log_has "comment posts to the alert" "POST http://paperclip.test/api/issues/alert-id/comments"
assert_log_lacks "second raise does not create" "POST http://paperclip.test/api/companies/company-1/issues"

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

# 5. A green hour with an open alert opens one recovery notice, assigned to
# whoever the alert is assigned to, and never writes to the alert itself. A
# closed earlier notice must not count as open.
write_fixture <<'JSON'
[
  {
    "id": "thread-id",
    "identifier": "ALT-THREAD",
    "title": "health probe follow-up",
    "status": "in_progress",
    "description": "[cotel-health-probe] [cotel-health-recovery]"
  },
  {
    "id": "old-notice-id",
    "identifier": "REC-OLD",
    "title": "cotel prod /healthz recovered [cotel-health-recovery]",
    "status": "done"
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
set_create_ids "rec-id" "REC-1"
out="$(run_page 200 resolve 2>"$TMP/err")" || { fail "recovery notice — exit $?"; cat "$TMP/err"; }
case "$out" in
    "page-cotel-health: opened recovery notice REC-1 rec-id for ALT-1 alert-id")
        pass "green hour opens a recovery notice for the open alert"
        ;;
    *) fail "green hour opens a recovery notice — output: $out" ;;
esac
assert_log_lacks "recovery never writes to the alert" "PATCH "
assert_log_lacks "recovery does not comment on the alert" "POST http://paperclip.test/api/issues/alert-id/comments"
assert_log_count "recovery creates exactly one issue" 1 "POST http://paperclip.test/api/companies/company-1/issues"
python3 - "$(create_payload)" <<'PY'
import json, sys
payload = json.loads(sys.argv[1])
bad = []
if payload.get("title") != "cotel prod /healthz recovered [cotel-health-recovery]":
    bad.append("title=" + str(payload.get("title")))
if payload.get("assigneeAgentId") != "agent-on-call":
    bad.append("assignee=" + str(payload.get("assigneeAgentId")))
description = payload.get("description") or ""
for needle in ("ALT-1", "alert-id", "close the alert as done, then close this notice"):
    if needle not in description:
        bad.append("description lacks " + needle)
if bad:
    raise SystemExit("; ".join(bad))
PY
pass "recovery payload carries the alert's assignee, identifier, id and the ask"

# 6. A second green hour with that notice still open writes nothing.
write_fixture <<'JSON'
[
  {
    "id": "alert-id",
    "identifier": "ALT-1",
    "title": "cotel prod /healthz is red [cotel-health-probe]",
    "status": "in_progress",
    "assigneeAgentId": "agent-on-call"
  },
  {
    "id": "notice-id",
    "identifier": "REC-1",
    "title": "cotel prod /healthz recovered [cotel-health-recovery]",
    "status": "todo",
    "assigneeAgentId": "agent-on-call"
  }
]
JSON
reset_log
out="$(run_page 200 resolve 2>"$TMP/err")" || { fail "second green hour — exit $?"; cat "$TMP/err"; }
case "$out" in
    "page-cotel-health: recovery notice REC-1 notice-id is already open for ALT-1 alert-id")
        pass "second green hour reports the open notice"
        ;;
    *) fail "second green hour reports the open notice — output: $out" ;;
esac
assert_log_lacks "second green hour does not write" "POST "

# 7. A green hour with no open alert writes nothing — not even a notice.
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
    "page-cotel-health: no open alert") pass "green hour with no open alert writes nothing" ;;
    *) fail "green hour with no open alert — output: $out" ;;
esac
assert_log_lacks "no create when nothing is open" "POST "

# 8. The recovery notice quotes the alert's title in its description, and
# search matches descriptions. Only the title-only marker filter keeps the next
# red hour from commenting on the notice instead of raising a fresh alert.
write_fixture <<'JSON'
[
  {
    "id": "notice-id",
    "identifier": "REC-1",
    "title": "cotel prod /healthz recovered [cotel-health-recovery]",
    "status": "todo",
    "description": "Alert: ALT-1 (alert-id) — cotel prod /healthz is red [cotel-health-probe]"
  }
]
JSON
reset_log
out="$(run_page 200 raise "$PROBE_FILE" 2>"$TMP/err")" || { fail "notice is not an alert — exit $?"; cat "$TMP/err"; }
case "$out" in
    "page-cotel-health: opened ALT-9 new-id") pass "an open recovery notice is not an open alert" ;;
    *) fail "an open recovery notice is not an open alert — output: $out" ;;
esac
assert_log_lacks "red hour does not comment on the recovery notice" "POST http://paperclip.test/api/issues/notice-id/comments"

reset_log
out="$(run_page 200 resolve 2>"$TMP/err")" || { fail "notice alone resolves to nothing — exit $?"; cat "$TMP/err"; }
case "$out" in
    "page-cotel-health: no open alert") pass "a notice without its alert is not an alert either" ;;
    *) fail "a notice without its alert — output: $out" ;;
esac

# 9. A marker pair where one contains the other would cross the two searches,
# so the script refuses it before making any request — on either action.
for action in raise resolve; do
    reset_log
    set +e
    out="$(export PC_RECOVERY_ORIGIN_ID="cotel-health-probe-recovery"; run_page 200 "$action" "$PROBE_FILE" 2>"$TMP/err")"
    rc=$?
    set -e
    if [ "$rc" -eq 0 ]; then
        fail "$action with a nested recovery marker exited 0: $out"
    else
        pass "$action with a nested recovery marker exits non-zero"
    fi
    case "$out" in
        *"PC_RECOVERY_ORIGIN_ID (cotel-health-probe-recovery) must not contain PC_ORIGIN_ID (cotel-health-probe)"*)
            pass "$action names the colliding markers"
            ;;
        *) fail "$action names the colliding markers — output: $out" ;;
    esac
    if [ -s "$TMP/log" ]; then
        fail "$action with a nested recovery marker still called the API"
        cat "$TMP/log"
    else
        pass "$action with a nested recovery marker makes no request"
    fi
done

reset_log
set +e
out="$(export PC_ORIGIN_ID="cotel-health-recovery-probe"; run_page 200 resolve 2>"$TMP/err")"
rc=$?
set -e
if [ "$rc" -eq 0 ]; then
    fail "alert marker containing the recovery marker exited 0: $out"
else
    pass "alert marker containing the recovery marker exits non-zero"
fi
case "$out" in
    *"PC_ORIGIN_ID (cotel-health-recovery-probe) must not contain PC_RECOVERY_ORIGIN_ID (cotel-health-recovery)"*)
        pass "the containment check works in both directions"
        ;;
    *) fail "the containment check works in both directions — output: $out" ;;
esac

# 10. A failed search must not create an issue.
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

# 11. A failed recovery-notice lookup must not create a notice either, and its
# failure line must not read like the alert search.
write_fixture <<'JSON'
[
  {
    "id": "alert-id",
    "identifier": "ALT-1",
    "title": "cotel prod /healthz is red [cotel-health-probe]",
    "status": "in_progress"
  }
]
JSON
reset_log
set +e
out="$(export PAGE_TEST_FAIL_RECOVERY_SEARCH=1; run_page 200 resolve 2>"$TMP/err")"
rc=$?
set -e
if [ "$rc" -eq 0 ]; then
    fail "recovery search failure exited 0: $out"
else
    pass "recovery search failure exits non-zero"
fi
assert_log_lacks "recovery search failure does not create" "POST "
assert_err_has "recovery search failure names its own call" "recovery notice search: HTTP 500"

# 12. Usage and missing probe file.
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

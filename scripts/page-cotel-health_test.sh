#!/usr/bin/env bash
#
# page-cotel-health_test.sh — drive page-cotel-health.sh against a fake curl.
# The live API is not contacted. Covers title-marker dedup: a comment-only
# search hit is not the alert, a done issue with the marker is not open,
# and a longer marker does not satisfy a shorter one.

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
        if [[ "$url" == *"/comments" ]]; then
            code=201
            body='{"id":"comment-1"}'
        else
            code=201
            body="$(cat "${PAGE_TEST_CREATE_BODY:?}")"
        fi
        ;;
    PATCH)
        code="${PAGE_TEST_PATCH_STATUS:-200}"
        if [ "$code" = "409" ]; then
            body='{"error":"Issue run ownership conflict"}'
        else
            body='{"id":"alert-id","identifier":"ALT-1","status":"done","title":"cotel prod /healthz is red [cotel-health-probe]"}'
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
    PAGE_TEST_PATCH_STATUS="${PAGE_TEST_PATCH_STATUS:-200}" \
        bash "$PAGE" "${@:2}"
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

# 5. Resolve patches the title match, not the comment hit.
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
    "status": "in_progress"
  }
]
JSON
reset_log
out="$(run_page 200 resolve)"
case "$out" in
    "page-cotel-health: resolved ALT-1 alert-id") pass "resolve closes the title match" ;;
    *) fail "resolve closes the title match — output: $out" ;;
esac
assert_log_has "resolve patches the alert" "PATCH http://paperclip.test/api/issues/alert-id"
assert_log_lacks "resolve does not patch the comment hit" "PATCH http://paperclip.test/api/issues/thread-id"

# 6. Resolve with only a comment hit reports no open alert.
write_fixture <<'JSON'
[
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
out="$(run_page 200 resolve)"
case "$out" in
    "page-cotel-health: no open alert") pass "resolve ignores comment-only hits" ;;
    *) fail "resolve ignores comment-only hits — output: $out" ;;
esac
assert_log_lacks "no patch when nothing is open" "PATCH "

# 7. A failed search must not create an issue.
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

# 8. Resolve while the alert is checked out leaves it open and exits 0.
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
PAGE_TEST_PATCH_STATUS=409
set +e
out="$(run_page 200 resolve 2>"$TMP/err")"
rc=$?
set -e
PAGE_TEST_PATCH_STATUS=200
if [ "$rc" -ne 0 ]; then
    fail "checked-out resolve exited $rc: $out"
    cat "$TMP/err"
else
    case "$out" in
        "page-cotel-health: alert ALT-1 alert-id is checked out; leaving it open")
            pass "checked-out resolve leaves the alert open"
            ;;
        *) fail "checked-out resolve — output: $out" ;;
    esac
fi

# 9. Usage and missing probe file.
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

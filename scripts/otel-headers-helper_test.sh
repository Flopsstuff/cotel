#!/usr/bin/env bash
#
# otel-headers-helper_test.sh — drive otel-headers-helper.py with controlled
# environments. Claude Code stops exporting when the helper prints anything but
# a JSON object, so every case also checks that stdout is exactly one JSON
# object of string values and that the exit code is 0.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HELPER="$ROOT/scripts/otel-headers-helper.py"
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

# Compares stdout to the wanted JSON object after normalising both, so key
# order does not matter. Runs the helper with a clean environment plus the
# given VAR=value pairs.
expect() {
    local name="$1" want="$2"
    shift 2
    local out rc
    set +e
    out="$(env -i PATH="$PATH" "$@" "$HELPER" 2>/dev/null)"
    rc=$?
    set -e
    if [ "$rc" -ne 0 ]; then
        fail "$name — exit $rc, want 0; stdout: $out"
        return
    fi
    if ! printf '%s' "$out" | python3 -c '
import json, sys
obj = json.load(sys.stdin)
assert isinstance(obj, dict), "not an object"
assert all(isinstance(k, str) and isinstance(v, str) for k, v in obj.items()), "non-string entry"
' 2>/dev/null; then
        fail "$name — stdout is not a JSON object of strings: $out"
        return
    fi
    if python3 -c '
import json, sys
sys.exit(0 if json.loads(sys.argv[1]) == json.loads(sys.argv[2]) else 1)
' "$out" "$want"; then
        pass "$name"
    else
        fail "$name — got $out, want $want"
    fi
}

expect "no env prints an empty object" '{}'

expect "generic headers only" \
    '{"Authorization":"Bearer cotel_ik_abc","X-Extra":"1"}' \
    OTEL_EXPORTER_OTLP_HEADERS='Authorization=Bearer cotel_ik_abc,X-Extra=1'

expect "per-signal headers win on the same key" \
    '{"Authorization":"Bearer traces","X-Extra":"1","X-Traces":"t"}' \
    OTEL_EXPORTER_OTLP_HEADERS='Authorization=Bearer generic,X-Extra=1' \
    OTEL_EXPORTER_OTLP_TRACES_HEADERS='Authorization=Bearer traces,X-Traces=t'

expect "per-signal override ignores header-name case" \
    '{"authorization":"Bearer traces"}' \
    OTEL_EXPORTER_OTLP_HEADERS='Authorization=Bearer generic' \
    OTEL_EXPORTER_OTLP_TRACES_HEADERS='authorization=Bearer traces'

expect "values are percent-decoded" \
    '{"Authorization":"Bearer abc"}' \
    OTEL_EXPORTER_OTLP_TRACES_HEADERS='Authorization=Bearer%20abc'

expect "whitespace around keys and values is trimmed" \
    '{"Authorization":"Bearer abc","X-A":"b"}' \
    OTEL_EXPORTER_OTLP_HEADERS=' Authorization = Bearer abc , X-A=b '

expect "a value may contain '='" \
    '{"X-Sig":"a=b=="}' \
    OTEL_EXPORTER_OTLP_HEADERS='X-Sig=a=b=='

expect "the actor token is added" \
    '{"Authorization":"Bearer cotel_ik_abc","X-Cotel-Actor-Token":"cotel_123"}' \
    OTEL_EXPORTER_OTLP_HEADERS='Authorization=Bearer cotel_ik_abc' \
    COTEL_ACTOR_TOKEN=' cotel_123 '

expect "the actor token replaces one already in the headers" \
    '{"X-Cotel-Actor-Token":"cotel_mine"}' \
    OTEL_EXPORTER_OTLP_HEADERS='x-cotel-actor-token=cotel_shared' \
    COTEL_ACTOR_TOKEN='cotel_mine'

expect "the actor token alone is enough" \
    '{"X-Cotel-Actor-Token":"cotel_123"}' \
    COTEL_ACTOR_TOKEN='cotel_123'

expect "an empty actor token is ignored" \
    '{"Authorization":"Bearer x"}' \
    OTEL_EXPORTER_OTLP_HEADERS='Authorization=Bearer x' \
    COTEL_ACTOR_TOKEN='   '

expect "malformed entries are skipped" \
    '{"Good":"1","Also":"2"}' \
    OTEL_EXPORTER_OTLP_HEADERS='Good=1,noequals,,=novalue-key, ,Also=2'

expect "an empty value is kept as an empty string" \
    '{"X-Empty":""}' \
    OTEL_EXPORTER_OTLP_HEADERS='X-Empty='

expect "a broken percent escape is kept literally, not fatal" \
    '{"X-Bad":"%zz"}' \
    OTEL_EXPORTER_OTLP_HEADERS='X-Bad=%zz'

# Nothing but the object may reach stdout, and token values must not be
# echoed to stderr.
set +e
lines="$(env -i PATH="$PATH" COTEL_ACTOR_TOKEN=cotel_secret "$HELPER" 2>/dev/null | wc -l | tr -d ' ')"
errout="$(env -i PATH="$PATH" COTEL_ACTOR_TOKEN=cotel_secret \
    OTEL_EXPORTER_OTLP_HEADERS='Authorization=Bearer cotel_ik_secret' "$HELPER" 2>&1 >/dev/null)"
set -e
if [ "$lines" = "1" ]; then
    pass "stdout is a single line"
else
    fail "stdout has $lines lines, want 1"
fi
case "$errout" in
    *secret*) fail "a token value reached stderr: $errout" ;;
    *) pass "no token value on stderr" ;;
esac

echo
echo "passed=$PASS failed=$FAIL"
[ "$FAIL" -eq 0 ]

#!/usr/bin/env bash
#
# supervise-cloudflared_test.sh — drive supervise-cloudflared.sh against a stub
# cloudflared. Covers the restart loop (the dead-tunnel failure this script
# exists for), the capped backoff, a prompt stop while it is backing off, both
# tunnel modes' argv and environment, and the no-mode exit.
#
# No Docker, no credentials and no network: the stub records how it was invoked
# and exits on command.

set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SUPERVISOR="$ROOT/scripts/supervise-cloudflared.sh"
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

want_in() {
    local name="$1" needle="$2" haystack="$3"
    if [[ "$haystack" == *"$needle"* ]]; then
        pass "$name"
    else
        fail "$name — output did not contain '$needle':
$haystack"
    fi
}

want_not_in() {
    local name="$1" needle="$2" haystack="$3"
    if [[ "$haystack" == *"$needle"* ]]; then
        fail "$name — output unexpectedly contained '$needle':
$haystack"
    else
        pass "$name"
    fi
}

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"; pkill -f "$WORK/bin/cloudflared" 2>/dev/null' EXIT

mkdir -p "$WORK/bin"
cat >"$WORK/bin/cloudflared" <<'STUB'
#!/bin/sh
# Stub cloudflared. Records each invocation, then either exits (the failure the
# supervisor must absorb) or stays up (a tunnel that registered).
n=$(cat "$STUB_STATE/count" 2>/dev/null || echo 0)
n=$((n + 1))
echo "$n" >"$STUB_STATE/count"
{
    echo "run ${n} argv: $*"
    echo "run ${n} TUNNEL_METRICS=${TUNNEL_METRICS:-<unset>}"
    echo "run ${n} TUNNEL_EDGE_IP_VERSION=${TUNNEL_EDGE_IP_VERSION:-<unset>}"
    echo "run ${n} CLOUDFLARE_TUNNEL_TOKEN=${CLOUDFLARE_TUNNEL_TOKEN:-<unset>}"
} >>"$STUB_STATE/log"
if [ "${n}" -ge "${STUB_STAY_UP_FROM:-99999}" ]; then
    echo "run ${n} staying up" >>"$STUB_STATE/log"
    while :; do sleep 0.2; done
fi
exit "${STUB_EXIT:-1}"
STUB
chmod +x "$WORK/bin/cloudflared"

# wait_for <file> <needle> <seconds>
wait_for() {
    local file="$1" needle="$2" deadline=$(( $(date +%s) + $3 ))
    while [ "$(date +%s)" -lt "$deadline" ]; do
        if [ -f "$file" ] && grep -qF "$needle" "$file"; then
            return 0
        fi
        sleep 0.2
    done
    return 1
}

#
# 1. A cloudflared that keeps exiting is restarted until it stays up, with the
#    delay doubling up to the cap. This is the boot-time edge-discovery failure:
#    on its own cloudflared exits for good and the tunnel never comes back.
#
state="$WORK/state1"
mkdir -p "$state"
out="$WORK/out1"
STUB_STATE="$state" STUB_STAY_UP_FROM=3 \
PATH="$WORK/bin:$PATH" \
CLOUDFLARE_TUNNEL_TOKEN="test-token" \
COTEL_TUNNEL_METRICS_ADDR="127.0.0.1:2999" \
COTEL_TUNNEL_RESTART_BACKOFF_MAX=2 \
    sh "$SUPERVISOR" >"$out" 2>&1 &
sup=$!

if wait_for "$state/log" "run 3 staying up" 20; then
    pass "restarts cloudflared until it stays up (no manual intervention)"
else
    fail "cloudflared was not restarted to a running state within 20s; log:
$(cat "$state/log" 2>/dev/null)
$(cat "$out")"
fi

log1="$(cat "$state/log" 2>/dev/null)"
sup_out="$(cat "$out")"
want_in "token mode runs 'tunnel run --token'" "argv: tunnel run --token test-token" "$log1"
want_in "the metrics address reaches cloudflared as TUNNEL_METRICS" "TUNNEL_METRICS=127.0.0.1:2999" "$log1"
want_in "token mode pins the edge IP version" "TUNNEL_EDGE_IP_VERSION=4" "$log1"
want_in "the token is kept out of cloudflared's environment" "CLOUDFLARE_TUNNEL_TOKEN=<unset>" "$log1"
want_in "the first restart waits 1s" "restarting in 1s" "$sup_out"
want_in "the delay doubles up to the cap" "restarting in 2s" "$sup_out"
want_not_in "the delay does not exceed the cap" "restarting in 4s" "$sup_out"

# Stopping the supervisor must stop the cloudflared it is running and must not
# start another one: the container's shutdown path signals this process.
kill -TERM "$sup" 2>/dev/null
wait "$sup"
sup_rc=$?
if [ "$sup_rc" -eq 0 ]; then
    pass "a stop signal exits the supervisor cleanly"
else
    fail "want exit 0 on SIGTERM, got $sup_rc; output:
$(cat "$out")"
fi

sleep 0.5
if pgrep -f "$WORK/bin/cloudflared" >/dev/null 2>&1; then
    fail "cloudflared survived the supervisor's stop: $(pgrep -af "$WORK/bin/cloudflared")"
    pkill -f "$WORK/bin/cloudflared" 2>/dev/null
else
    pass "the supervised cloudflared is stopped with the supervisor"
fi

if [ "$(cat "$state/count")" = "3" ]; then
    pass "no restart after the stop signal"
else
    fail "want 3 cloudflared runs, got $(cat "$state/count")"
fi

#
# 2. A stop that lands mid-backoff is not held until the delay is over: the
#    container's stop grace period is shorter than the cap.
#
state="$WORK/state2"
mkdir -p "$state"
out="$WORK/out2"
STUB_STATE="$state" \
PATH="$WORK/bin:$PATH" \
CLOUDFLARE_TUNNEL_TOKEN="test-token" \
COTEL_TUNNEL_RESTART_BACKOFF_MAX=30 \
    sh "$SUPERVISOR" >"$out" 2>&1 &
sup=$!

if wait_for "$out" "restarting in 4s" 20; then
    started="$(date +%s)"
    kill -TERM "$sup" 2>/dev/null
    wait "$sup"
    sup_rc=$?
    elapsed=$(( $(date +%s) - started ))
    if [ "$sup_rc" -eq 0 ] && [ "$elapsed" -le 2 ]; then
        pass "a stop during the backoff exits within ${elapsed}s"
    else
        fail "want exit 0 within 2s during the backoff, got exit $sup_rc after ${elapsed}s"
    fi
else
    kill -TERM "$sup" 2>/dev/null
    wait "$sup" 2>/dev/null
    fail "the backoff never reached 4s within 20s; output:
$(cat "$out")"
fi

#
# 3. Local-config mode: same loop, cloudflared's own invocation.
#
state="$WORK/state3"
mkdir -p "$state"
out="$WORK/out3"
touch "$WORK/config.yml"
STUB_STATE="$state" STUB_STAY_UP_FROM=1 \
PATH="$WORK/bin:$PATH" \
CLOUDFLARED_CONFIG="$WORK/config.yml" \
COTEL_TUNNEL_METRICS_ADDR="127.0.0.1:2998" \
    sh "$SUPERVISOR" >"$out" 2>&1 &
sup=$!
if wait_for "$state/log" "run 1 staying up" 20; then
    log3="$(cat "$state/log")"
    want_in "local-config mode runs 'tunnel --config … run'" "argv: tunnel --config $WORK/config.yml run" "$log3"
    want_in "local-config mode passes the metrics address" "TUNNEL_METRICS=127.0.0.1:2998" "$log3"
    want_in "local-config mode leaves the edge IP version to config.yml" "TUNNEL_EDGE_IP_VERSION=<unset>" "$log3"
else
    fail "local-config mode did not start cloudflared within 20s; output:
$(cat "$out")"
fi
kill -TERM "$sup" 2>/dev/null
wait "$sup" 2>/dev/null

#
# 4. Neither mode configured: say so and exit, rather than loop on a cloudflared
#    that cannot work. The entrypoint only starts this script when one is set.
#
out="$WORK/out4"
PATH="$WORK/bin:$PATH" COTEL_TUNNEL_RESTART_BACKOFF_MAX=notanumber \
    sh "$SUPERVISOR" >"$out" 2>&1
rc=$?
if [ "$rc" -eq 2 ]; then
    pass "no tunnel configured exits 2"
else
    fail "want exit 2 with no tunnel configured, got $rc"
fi
want_in "an unusable backoff cap is reported, not obeyed" "invalid COTEL_TUNNEL_RESTART_BACKOFF_MAX" "$(cat "$out")"

echo
echo "supervise-cloudflared_test: ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -eq 0 ]

#!/usr/bin/env bash
#
# wait-for-healthy.sh — block until a compose service reports healthy.
#
# `docker compose up -d` returns once the container is *started*, not once the
# application inside it works, so a deploy whose `storage.Open` dies looks
# exactly like one that succeeded. This gates on the container's own
# HEALTHCHECK and, on any failure, dumps the container logs so the reason is in
# the run log rather than only on the runner.
#
# Usage:
#   scripts/wait-for-healthy.sh [SERVICE] [TIMEOUT_SECONDS]
#
# Defaults: SERVICE=cotel, TIMEOUT_SECONDS=120. Run from the directory holding
# the compose file. Exits 0 once healthy; 1 on unhealthy, a crash loop, an exit,
# a service that defines no HEALTHCHECK, or timeout.
#
# Timeout guidance: a graceful stop checkpoints the WAL, so a normal cold start
# is a couple of seconds. A start that follows a hard kill replays the WAL
# instead and can take minutes — pass a larger timeout for that deploy rather
# than widening the default, which would blunt the gate for every other deploy.

set -euo pipefail

SERVICE="${1:-cotel}"
TIMEOUT="${2:-120}"
POLL_INTERVAL="${POLL_INTERVAL:-2}"
LOG_TAIL="${LOG_TAIL:-200}"
PROGRESS_EVERY=10

inspect() {
    docker inspect -f "$1" "$CID" 2>/dev/null || true
}

dump_diagnostics() {
    echo "--- docker compose ps ---"
    docker compose ps "$SERVICE" || true
    echo "--- health probe output ---"
    inspect '{{if .State.Health}}{{range .State.Health.Log}}exit={{.ExitCode}} {{.Output}}
{{end}}{{else}}(service defines no HEALTHCHECK){{end}}'
    echo "--- docker compose logs (last ${LOG_TAIL} lines) ---"
    docker compose logs --tail="$LOG_TAIL" --no-color "$SERVICE" || true
}

fail() {
    echo "wait-for-healthy: FAILED — $*"
    dump_diagnostics
    exit 1
}

CID="$(docker compose ps -q "$SERVICE" 2>/dev/null || true)"
if [ -z "$CID" ]; then
    echo "wait-for-healthy: FAILED — no container for service '${SERVICE}'; did 'docker compose up -d' run?"
    docker compose ps || true
    exit 1
fi

# Read the configured probe rather than .State.Health, which is briefly null
# right after start and would otherwise read as "no healthcheck".
PROBE="$(inspect '{{if .Config.Healthcheck}}{{index .Config.Healthcheck.Test 0}}{{end}}')"
if [ -z "$PROBE" ] || [ "$PROBE" = "NONE" ]; then
    fail "'${SERVICE}' defines no HEALTHCHECK, so the deploy cannot be verified"
fi

echo "wait-for-healthy: waiting up to ${TIMEOUT}s for '${SERVICE}' (${CID:0:12}) to report healthy"
START=$SECONDS
BASELINE_RESTARTS="$(inspect '{{.RestartCount}}')"
BASELINE_RESTARTS="${BASELINE_RESTARTS:-0}"
if [ "$BASELINE_RESTARTS" -gt 0 ]; then
    echo "wait-for-healthy: container carries ${BASELINE_RESTARTS} earlier restart(s); only further ones count as a crash loop"
fi

while :; do
    state="$(inspect '{{.State.Status}}')"
    health="$(inspect '{{if .State.Health}}{{.State.Health.Status}}{{else}}starting{{end}}')"
    restarts="$(inspect '{{.RestartCount}}')"
    elapsed=$((SECONDS - START))

    if [ "$health" = healthy ]; then
        echo "wait-for-healthy: '${SERVICE}' healthy after ${elapsed}s"
        exit 0
    fi

    # Checked before the probe: a restarting container also reads as
    # "unhealthy", and the process that died names the fault better.
    case "$state" in
        exited | dead)
            fail "'${SERVICE}' exited with code $(inspect '{{.State.ExitCode}}') after ${elapsed}s"
            ;;
        restarting)
            fail "'${SERVICE}' is restarting after ${elapsed}s: the process exited on its own (crash loop)"
            ;;
        "")
            fail "'${SERVICE}' container ${CID:0:12} disappeared after ${elapsed}s"
            ;;
    esac

    # Only restarts observed *during* this wait indict this deploy. A count of
    # its own does not: `up -d` leaves an already-current container in place,
    # and one that crashed once and recovered carries the count for the rest of
    # its life — including while it legitimately replays a WAL after a hard
    # kill, which is precisely when the wait is longest.
    if [ "${restarts:-0}" -gt "$BASELINE_RESTARTS" ]; then
        fail "'${SERVICE}' restarted $(( restarts - BASELINE_RESTARTS ))x during the wait (crash loop) after ${elapsed}s"
    fi

    if [ "$health" = unhealthy ]; then
        fail "'${SERVICE}' reported unhealthy after ${elapsed}s"
    fi

    if [ "$elapsed" -ge "$TIMEOUT" ]; then
        fail "'${SERVICE}' still '${health}' (container ${state}) after ${TIMEOUT}s"
    fi

    if [ "$elapsed" -gt 0 ] && [ $((elapsed % PROGRESS_EVERY)) -lt "$POLL_INTERVAL" ]; then
        echo "wait-for-healthy: ${elapsed}s — status=${state} health=${health}"
    fi

    sleep "$POLL_INTERVAL"
done

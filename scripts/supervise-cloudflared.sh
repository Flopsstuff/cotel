#!/bin/sh
# supervise-cloudflared.sh — run cloudflared beside cotel and keep it running.
#
# cloudflared has its own retry loop for connection and protocol errors, but it
# does not cover every way a start can fail. Edge discovery is the one that cost
# us a day of telemetry: a container that comes up before the host's resolver is
# usable gets "no such host" for _v2-origintunneld._tcp.argotunnel.com, logs
# "Initiating shutdown" and exits for good. Nothing noticed, because cotel — not
# cloudflared — is the container's main process: `restart: unless-stopped` never
# fired, the container stayed (healthy) with RestartCount 0, and every agent
# exporting to the public ingest URL lost its spans silently.
#
# So every cloudflared exit is retried here, forever, with a capped backoff,
# rather than being made fatal to the container. Both halves of that choice
# matter: a tunnel that stays dead is the failure this exists to prevent, and
# killing the container over an unreachable edge would also drop LAN ingest on
# :4318, which keeps working throughout.
#
# Environment:
#   CLOUDFLARE_TUNNEL_TOKEN           token mode (takes precedence). Consumed
#                                     here and removed from the environment
#                                     cloudflared inherits — see the entrypoint
#                                     for why it must not reach it.
#   CLOUDFLARED_CONFIG                local-config mode: path to a config.yml.
#   COTEL_TUNNEL_METRICS_ADDR         cloudflared's metrics listener, passed on
#                                     as TUNNEL_METRICS. Its /ready route is
#                                     what cotel's /healthz reports.
#   COTEL_TUNNEL_RESTART_BACKOFF_MAX  cap on the restart delay, seconds (60).

set -u

log() { echo "supervise-cloudflared: $*"; }

BACKOFF_MAX="${COTEL_TUNNEL_RESTART_BACKOFF_MAX:-60}"
case "${BACKOFF_MAX}" in
    ''|*[!0-9]*)
        log "warning: invalid COTEL_TUNNEL_RESTART_BACKOFF_MAX='${BACKOFF_MAX}', using 60"
        BACKOFF_MAX=60
        ;;
esac
[ "${BACKOFF_MAX}" -ge 1 ] || BACKOFF_MAX=1

# cloudflared logs the value of every environment variable whose whole KEY=VALUE
# pair merely contains "TUNNEL_", and redacts only the two names it owns itself.
# The token goes in as a flag, which it does redact, and leaves the environment.
cfd_token="${CLOUDFLARE_TUNNEL_TOKEN:-}"
unset CLOUDFLARE_TUNNEL_TOKEN

if [ -n "${COTEL_TUNNEL_METRICS_ADDR:-}" ]; then
    TUNNEL_METRICS="${COTEL_TUNNEL_METRICS_ADDR}"
    export TUNNEL_METRICS
fi

if [ -n "${cfd_token}" ]; then
    mode="token"
    # 2026.4.0 changed the --edge-ip-version default from 4 to auto, which
    # follows whichever family the resolver answers with first and only falls
    # back after a connection has already failed. Keep the old default where
    # cotel owns the whole invocation; local-config mode is left alone because
    # this env var outranks a config.yml setting.
    TUNNEL_EDGE_IP_VERSION="${TUNNEL_EDGE_IP_VERSION:-4}"
    export TUNNEL_EDGE_IP_VERSION
    set -- tunnel run --token "${cfd_token}"
elif [ -n "${CLOUDFLARED_CONFIG:-}" ]; then
    mode="local-config"
    set -- tunnel --config "${CLOUDFLARED_CONFIG}" run
else
    log "neither CLOUDFLARE_TUNNEL_TOKEN nor CLOUDFLARED_CONFIG is set; nothing to supervise"
    exit 2
fi

child=""
stopping=0

# The entrypoint stops this supervisor by signalling it, so the signal must
# reach the cloudflared (or sleep) it is currently waiting on, and must not be
# followed by another restart.
stop() {
    stopping=1
    if [ -n "${child}" ]; then
        log "stop requested, signalling supervised child (PID ${child})"
        kill "${child}" 2>/dev/null || true
        wait "${child}" 2>/dev/null || true
    fi
    exit 0
}
trap stop TERM INT

delay=1
while :; do
    started="$(date +%s)"
    cloudflared "$@" &
    child=$!
    log "cloudflared started in ${mode} mode (PID ${child})"
    wait "${child}"
    status=$?
    child=""
    [ "${stopping}" -eq 1 ] && exit 0

    ran=$(( $(date +%s) - started ))
    # A cloudflared that stayed up at least as long as the cap was working, so
    # the next failure starts from a short delay again instead of inheriting
    # the backoff of an outage that is over.
    if [ "${ran}" -ge "${BACKOFF_MAX}" ]; then
        delay=1
    fi
    log "cloudflared exited with status ${status} after ${ran}s; restarting in ${delay}s"

    # Backgrounded so a stop signal lands during the wait rather than after the
    # full delay: a foreground sleep would hold the container's stop grace
    # period for up to BACKOFF_MAX seconds.
    sleep "${delay}" &
    child=$!
    wait "${child}" 2>/dev/null || true
    child=""
    [ "${stopping}" -eq 1 ] && exit 0

    delay=$(( delay * 2 ))
    if [ "${delay}" -gt "${BACKOFF_MAX}" ]; then
        delay="${BACKOFF_MAX}"
    fi
done

#!/bin/sh
# Entrypoint for the cotel container.
# Supports two cloudflared modes (both optional):
#   Token mode:       CLOUDFLARE_TUNNEL_TOKEN env var set → cloudflared tunnel run --token
#   Local-config mode: /etc/cloudflared/config.yml mounted → cloudflared tunnel --config ... run
# Token mode takes precedence if both are present. No tunnel is started if neither is set.
#
# cloudflared runs under scripts/supervise-cloudflared.sh, which restarts it for
# as long as the container lives: on its own it exits for good on a failed edge
# discovery, and nothing here would notice, because cotel is the main process.
#
# On SIGTERM/SIGINT the signal is forwarded to cotel first and we wait for it to
# exit, so cotel can CHECKPOINT the DuckDB WAL before the container stops;
# otherwise the next start replays the WAL (a multi-minute cost on a large DB).
# cloudflared is stopped afterwards.

CLOUDFLARED_SUPERVISOR_PID=""
COTEL_PID=""
CLOUDFLARED_CONFIG_DEFAULT=/etc/cloudflared/config.yml
CLOUDFLARED_SUPERVISOR=/usr/local/bin/supervise-cloudflared.sh
TUNNEL_METRICS_ADDR_DEFAULT=127.0.0.1:2000

stop_cloudflared() {
    if [ -n "${CLOUDFLARED_SUPERVISOR_PID}" ]; then
        echo "entrypoint: stopping the cloudflared supervisor (PID ${CLOUDFLARED_SUPERVISOR_PID})"
        kill "${CLOUDFLARED_SUPERVISOR_PID}" 2>/dev/null || true
        wait "${CLOUDFLARED_SUPERVISOR_PID}" 2>/dev/null || true
    fi
}

terminate() {
    # cotel exits non-zero when the shutdown CHECKPOINT failed, which means the
    # WAL it leaves behind may not replay; exiting 0 here would hide that as a
    # clean container stop.
    STATUS=0
    if [ -n "${COTEL_PID}" ]; then
        echo "entrypoint: forwarding stop signal to cotel (PID ${COTEL_PID})"
        kill -TERM "${COTEL_PID}" 2>/dev/null || true
        wait "${COTEL_PID}"
        STATUS=$?
    fi
    stop_cloudflared
    exit $STATUS
}

trap terminate TERM INT

# cloudflared logs the value of every environment variable whose whole KEY=VALUE
# pair merely *contains* "TUNNEL_", and redacts only the two names it owns
# itself - so our own CLOUDFLARE_TUNNEL_TOKEN would be printed in plaintext on
# every start. Hand the token over as a flag, which cloudflared does redact, and
# keep it out of the environment cloudflared inherits.
cfd_token="${CLOUDFLARE_TUNNEL_TOKEN:-}"
unset CLOUDFLARE_TUNNEL_TOKEN

cfd_config="${CLOUDFLARED_CONFIG:-${CLOUDFLARED_CONFIG_DEFAULT}}"

# cotel reports the tunnel's state on /healthz only when this variable reaches
# it, so it is exported below exclusively on the paths that actually start a
# tunnel. An operator-set value must not survive into a deployment with no
# tunnel: a tokenless instance (local dev, CI) would then report a dead one.
tunnel_metrics_addr="${COTEL_TUNNEL_METRICS_ADDR:-${TUNNEL_METRICS_ADDR_DEFAULT}}"
unset COTEL_TUNNEL_METRICS_ADDR

if [ -n "${cfd_token}" ]; then
    # Passed through the supervisor's environment rather than its arguments, so
    # the token does not gain a second `ps` row; the supervisor unsets it before
    # cloudflared inherits anything.
    CLOUDFLARE_TUNNEL_TOKEN="${cfd_token}" \
    COTEL_TUNNEL_METRICS_ADDR="${tunnel_metrics_addr}" \
        "${CLOUDFLARED_SUPERVISOR}" &
    CLOUDFLARED_SUPERVISOR_PID=$!
    export COTEL_TUNNEL_METRICS_ADDR="${tunnel_metrics_addr}"
    echo "entrypoint: cloudflared started in token mode under supervision (supervisor PID ${CLOUDFLARED_SUPERVISOR_PID}, metrics ${tunnel_metrics_addr})"
elif [ -f "${cfd_config}" ]; then
    CLOUDFLARED_CONFIG="${cfd_config}" \
    COTEL_TUNNEL_METRICS_ADDR="${tunnel_metrics_addr}" \
        "${CLOUDFLARED_SUPERVISOR}" &
    CLOUDFLARED_SUPERVISOR_PID=$!
    export COTEL_TUNNEL_METRICS_ADDR="${tunnel_metrics_addr}"
    echo "entrypoint: cloudflared started in local-config mode under supervision (supervisor PID ${CLOUDFLARED_SUPERVISOR_PID}, metrics ${tunnel_metrics_addr})"
fi

/usr/local/bin/cotel "$@" &
COTEL_PID=$!
wait "${COTEL_PID}"
STATUS=$?
stop_cloudflared
exit $STATUS

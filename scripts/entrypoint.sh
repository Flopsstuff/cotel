#!/bin/sh
# Entrypoint for the cotel container.
# Supports two cloudflared modes (both optional):
#   Token mode:       CLOUDFLARE_TUNNEL_TOKEN env var set → cloudflared tunnel run --token
#   Local-config mode: /etc/cloudflared/config.yml mounted → cloudflared tunnel --config ... run
# Token mode takes precedence if both are present. No tunnel is started if neither is set.
#
# On SIGTERM/SIGINT the signal is forwarded to cotel first and we wait for it to
# exit, so cotel can CHECKPOINT the DuckDB WAL before the container stops;
# otherwise the next start replays the WAL (a multi-minute cost on a large DB).
# cloudflared is stopped afterwards.

CLOUDFLARED_PID=""
COTEL_PID=""
CLOUDFLARED_CONFIG_DEFAULT=/etc/cloudflared/config.yml

stop_cloudflared() {
    if [ -n "${CLOUDFLARED_PID}" ]; then
        echo "entrypoint: stopping cloudflared (PID ${CLOUDFLARED_PID})"
        kill "${CLOUDFLARED_PID}" 2>/dev/null || true
        wait "${CLOUDFLARED_PID}" 2>/dev/null || true
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

if [ -n "${cfd_token}" ]; then
    # cloudflared 2026.4.0 changed the --edge-ip-version default from 4 to auto,
    # which follows whichever family the resolver answers with first and only
    # falls back to the other one after a connection has already failed. Keep
    # the old default here, where cotel owns the whole invocation; local-config
    # mode is left alone because this env var outranks a config.yml setting.
    export TUNNEL_EDGE_IP_VERSION="${TUNNEL_EDGE_IP_VERSION:-4}"
    cloudflared tunnel run --token "${cfd_token}" &
    CLOUDFLARED_PID=$!
    echo "entrypoint: cloudflared started in token mode (PID ${CLOUDFLARED_PID})"
elif [ -f "${CLOUDFLARED_CONFIG:-${CLOUDFLARED_CONFIG_DEFAULT}}" ]; then
    cloudflared tunnel --config "${CLOUDFLARED_CONFIG:-${CLOUDFLARED_CONFIG_DEFAULT}}" run &
    CLOUDFLARED_PID=$!
    echo "entrypoint: cloudflared started in local-config mode (PID ${CLOUDFLARED_PID})"
fi

/usr/local/bin/cotel "$@" &
COTEL_PID=$!
wait "${COTEL_PID}"
STATUS=$?
stop_cloudflared
exit $STATUS

#!/bin/sh
# Entrypoint for the cotel container.
# Supports two cloudflared modes (both optional):
#   Token mode:       CLOUDFLARE_TUNNEL_TOKEN env var set → cloudflared tunnel run --token
#   Local-config mode: /etc/cloudflared/config.yml mounted → cloudflared tunnel --config ... run
# Token mode takes precedence if both are present. No tunnel is started if neither is set.
# Traps SIGTERM to terminate cloudflared and cotel cleanly.

CLOUDFLARED_PID=""
COTEL_PID=""
CLOUDFLARED_CONFIG_DEFAULT=/etc/cloudflared/config.yml

cleanup() {
    if [ -n "${CLOUDFLARED_PID}" ]; then
        echo "entrypoint: stopping cloudflared (PID ${CLOUDFLARED_PID})"
        kill "${CLOUDFLARED_PID}" 2>/dev/null || true
        wait "${CLOUDFLARED_PID}" 2>/dev/null || true
    fi
}

trap 'cleanup; exit 0' TERM INT

if [ -n "${CLOUDFLARE_TUNNEL_TOKEN}" ]; then
    cloudflared tunnel run --token "${CLOUDFLARE_TUNNEL_TOKEN}" &
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
cleanup
exit $STATUS

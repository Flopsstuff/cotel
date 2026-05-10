#!/bin/sh
# Entrypoint for the cotel container.
# When CLOUDFLARE_TUNNEL_TOKEN is set, starts cloudflared in the background
# before handing off to cotel. Traps SIGTERM to terminate cloudflared cleanly.

CLOUDFLARED_PID=""
COTEL_PID=""

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
    echo "entrypoint: cloudflared started (PID ${CLOUDFLARED_PID})"
fi

/usr/local/bin/cotel "$@" &
COTEL_PID=$!
wait "${COTEL_PID}"
STATUS=$?
cleanup
exit $STATUS

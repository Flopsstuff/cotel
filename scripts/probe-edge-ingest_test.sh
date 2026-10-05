#!/usr/bin/env bash
#
# probe-edge-ingest_test.sh — drive probe-edge-ingest.sh against a local mock.
# Covers the one green answer (the application's JSON 401) and every way a
# reachable-but-wrong answer must stay red: a Cloudflare interstitial that also
# returns 401, an Access login redirect, an origin-unreachable 5xx, an unrouted
# 404, and an unauthenticated 200.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PROBE="$ROOT/scripts/probe-edge-ingest.sh"
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

expect() {
    local name="$1" want_rc="$2" want_needle="$3"
    shift 3
    local out rc
    set +e
    out="$("$@" 2>&1)"
    rc=$?
    set -e
    if [ "$rc" -ne "$want_rc" ]; then
        fail "$name — exit $rc, want $want_rc; output: $out"
        return
    fi
    case "$out" in
        *"$want_needle"*) pass "$name" ;;
        *) fail "$name — output did not contain '$want_needle': $out" ;;
    esac
}

MOCK_PY="$(mktemp)"
cat >"$MOCK_PY" <<'PY'
from http.server import BaseHTTPRequestHandler, HTTPServer
import os

PORT = int(os.environ["PROBE_MOCK_PORT"])


class H(BaseHTTPRequestHandler):
    def do_GET(self):
        # The live shape: cotel's auth middleware rejecting an unknown token.
        if self.path == "/v1/traces":
            self.send_response(401)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"error":"unauthorized"}')
            return
        # A proxy's own 401 — the status matches, the answer is not cotel's.
        if self.path == "/edge-401":
            self.send_response(401)
            self.send_header("Content-Type", "text/html")
            self.end_headers()
            self.wfile.write(b"<html><title>401 Unauthorized</title></html>")
            return
        if self.path == "/access":
            self.send_response(302)
            self.send_header(
                "Location",
                "https://flopbut.cloudflareaccess.com/cdn-cgi/access/login/example",
            )
            self.send_header("WWW-Authenticate", 'Cloudflare-Access realm="cotel"')
            self.end_headers()
            return
        if self.path == "/tunnel-down":
            self.send_response(530)
            self.send_header("Content-Type", "text/html")
            self.end_headers()
            self.wfile.write(b"<html>Error 1033 Argo Tunnel error</html>")
            return
        if self.path == "/anonymous":
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(b"{}")
            return
        self.send_response(404)
        self.end_headers()
        self.wfile.write(b"404 page not found")

    def log_message(self, fmt, *args):
        return


HTTPServer(("127.0.0.1", PORT), H).serve_forever()
PY
trap 'rm -f "$MOCK_PY"; kill "$MOCK_PID" 2>/dev/null || true' EXIT

PROBE_MOCK_PORT="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')"
export PROBE_MOCK_PORT
python3 "$MOCK_PY" &
MOCK_PID=$!

for i in 1 2 3 4 5 6 7 8 9 10; do
    if curl -s -o /dev/null --max-time 1 "http://127.0.0.1:${PROBE_MOCK_PORT}/v1/traces"; then
        break
    fi
    sleep 0.1
    if [ "$i" -eq 10 ]; then
        echo "mock server did not start on 127.0.0.1:${PROBE_MOCK_PORT}"
        exit 1
    fi
done

BASE="http://127.0.0.1:${PROBE_MOCK_PORT}"

expect "the application's JSON 401 is the green answer" 0 "HTTP 401 from the ingest handler" \
    "$PROBE" "$BASE/v1/traces"
expect "an HTML 401 is not the ingest handler" 2 "not from the ingest handler" \
    "$PROBE" "$BASE/edge-401"
expect "an Access redirect is an ingest outage of its own" 4 "cloudflare access now fronts" \
    "$PROBE" "$BASE/access"
expect "a tunnel error is origin-unreachable, not 401" 2 "cloudflare reached, the origin did not answer" \
    "$PROBE" "$BASE/tunnel-down"
expect "an unrouted path is named as routing, not as down" 2 "/v1/traces is not routed" \
    "$PROBE" "$BASE/nope"
expect "a 200 to an invalid token is red and says why" 2 "accepting unauthenticated spans" \
    "$PROBE" "$BASE/anonymous"

# The URL can be given by env as well as argv, because the Pi timer sets it.
expect "INGEST_URL is honoured" 0 "HTTP 401 from the ingest handler" \
    env INGEST_URL="$BASE/v1/traces" "$PROBE"

# 127.0.0.1:1 is unprivileged and closed on this host — connection refused.
expect "connection refused is unreachable" 1 "public ingest unreachable" \
    env CONNECT_TIMEOUT=2 "$PROBE" "http://127.0.0.1:1/v1/traces"

# TEST-NET-1 is the documentation range; the connect hangs until the timeout.
expect "timeout is unreachable" 1 "public ingest unreachable" \
    env CONNECT_TIMEOUT=1 "$PROBE" "http://192.0.2.1:9/v1/traces"

expect "a non-numeric timeout is rejected, not silently defaulted" 2 "CONNECT_TIMEOUT must be a positive integer" \
    env CONNECT_TIMEOUT=soon "$PROBE" "$BASE/v1/traces"

echo
echo "passed=$PASS failed=$FAIL"
[ "$FAIL" -eq 0 ]

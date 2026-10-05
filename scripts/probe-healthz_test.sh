#!/usr/bin/env bash
#
# probe-healthz_test.sh — drive probe-healthz.sh against a local mock.
# Covers 200 / non-200 / unreachable, plus staleness, empty-DB nulls,
# missing freshness keys, and an Access login redirect.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PROBE="$ROOT/scripts/probe-healthz.sh"
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
import json
import os

PORT = int(os.environ["PROBE_MOCK_PORT"])

ROUTES = {
    "/ok-liveness": (200, {"ok": True, "spans": 3}),
    "/ok-fresh": (
        200,
        {
            "ok": True,
            "spans": 12,
            "last_ingest_at": "2026-10-04T09:00:00Z",
            "newest_span_age_seconds": 12,
        },
    ),
    "/ok-stale": (
        200,
        {
            "ok": True,
            "spans": 12,
            "last_ingest_at": "2026-09-28T00:44:00Z",
            "newest_span_age_seconds": 518400,
        },
    ),
    "/ok-empty": (
        200,
        {
            "ok": True,
            "spans": 0,
            "last_ingest_at": None,
            "newest_span_age_seconds": None,
        },
    ),
    "/ok-zero-age": (
        200,
        {
            "ok": True,
            "spans": 1,
            "last_ingest_at": "2026-10-04T10:00:00Z",
            "newest_span_age_seconds": 0,
        },
    ),
    "/unreadable": (
        503,
        {
            "ok": False,
            "spans": 0,
            "last_ingest_at": None,
            "newest_span_age_seconds": None,
        },
    ),
    "/crash": (500, {"error": "boom"}),
    "/not-json": (200, b"not json"),
}


class H(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/access":
            self.send_response(302)
            self.send_header(
                "Location",
                "https://flopbut.cloudflareaccess.com/cdn-cgi/access/login/example",
            )
            self.send_header("WWW-Authenticate", 'Cloudflare-Access realm="cotel"')
            self.end_headers()
            return
        if self.path not in ROUTES:
            self.send_response(404)
            self.end_headers()
            self.wfile.write(b"404 page not found")
            return
        code, payload = ROUTES[self.path]
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        if isinstance(payload, (bytes, bytearray)):
            self.wfile.write(payload)
        else:
            self.wfile.write(json.dumps(payload).encode())

    def log_message(self, fmt, *args):
        return


HTTPServer(("127.0.0.1", PORT), H).serve_forever()
PY
trap 'rm -f "$MOCK_PY"; kill "$MOCK_PID" 2>/dev/null || true' EXIT

# Bind an ephemeral port, then hand it to the mock.
PROBE_MOCK_PORT="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')"
export PROBE_MOCK_PORT
python3 "$MOCK_PY" &
MOCK_PID=$!

for i in 1 2 3 4 5 6 7 8 9 10; do
    if curl -sf -o /dev/null --max-time 1 "http://127.0.0.1:${PROBE_MOCK_PORT}/ok-liveness"; then
        break
    fi
    sleep 0.1
    if [ "$i" -eq 10 ]; then
        echo "mock server did not start on 127.0.0.1:${PROBE_MOCK_PORT}"
        exit 1
    fi
done

BASE="http://127.0.0.1:${PROBE_MOCK_PORT}"

expect "liveness 200 without freshness fields" 0 "liveness" \
    "$PROBE" "$BASE/ok-liveness"
expect "fresh ingest is OK" 0 "ingest age 12s" \
    "$PROBE" "$BASE/ok-fresh"
expect "age 0 is freshness, not empty" 0 "ingest age 0s" \
    "$PROBE" "$BASE/ok-zero-age"
expect "stale ingest is red with distinct text" 3 "ingest stale" \
    "$PROBE" "$BASE/ok-stale"
expect "present null is empty DB, not age 0" 3 "empty database" \
    "$PROBE" "$BASE/ok-empty"
expect "503 is database unreadable, not empty DB" 2 "HTTP 503 database unreadable" \
    "$PROBE" "$BASE/unreadable"
expect "500 is HTTP failure" 2 "HTTP 500" \
    "$PROBE" "$BASE/crash"
expect "200 with non-JSON is HTTP-body failure" 2 "body unusable" \
    "$PROBE" "$BASE/not-json"
expect "Access login redirect is distinct from app-down" 4 "cloudflare access blocked" \
    "$PROBE" "$BASE/access"

# 127.0.0.1:1 is unprivileged and closed on this host — connection refused.
expect "connection refused is unreachable" 1 "unreachable" \
    env CONNECT_TIMEOUT=2 "$PROBE" "http://127.0.0.1:1/healthz"

# Blackhole-ish: TEST-NET-1 is documentation range; may hang until timeout.
expect "timeout is unreachable" 1 "unreachable" \
    env CONNECT_TIMEOUT=1 "$PROBE" "http://192.0.2.1:9/healthz"

echo
echo "passed=$PASS failed=$FAIL"
[ "$FAIL" -eq 0 ]

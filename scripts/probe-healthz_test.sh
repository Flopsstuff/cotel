#!/usr/bin/env bash
#
# probe-healthz_test.sh — drive probe-healthz.sh against a local mock.
# Covers 200 / non-200 / unreachable, plus staleness, empty-DB nulls,
# missing freshness keys, an Access login redirect, and the snapshot claim
# the probe makes against /api/v1/health on a green /healthz.
#
# The snapshot cases are served under a path prefix (/snap-*/healthz and
# /snap-*/api/v1/health) so that each one also exercises the URL derivation:
# the probe is given only the /healthz URL.

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
import datetime
import json
import os

PORT = int(os.environ["PROBE_MOCK_PORT"])


def iso(delta):
    t = datetime.datetime.now(datetime.timezone.utc) + delta
    return t.strftime("%Y-%m-%dT%H:%M:%SZ")


NOW = iso(datetime.timedelta())
LONG_AGO = iso(datetime.timedelta(days=-3))

GREEN_HEALTHZ = {
    "ok": True,
    "spans": 12,
    "last_ingest_at": NOW,
    "newest_span_age_seconds": 3,
}
STALE_HEALTHZ = dict(GREEN_HEALTHZ, last_ingest_at=LONG_AGO, newest_span_age_seconds=259200)


def api_health(snapshot):
    body = {"status": "ok", "span_count": 12, "retention": {"status": "ok"}}
    if snapshot is not None:
        body["snapshot"] = snapshot
    return (200, body)


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
    # The instance the non-prefixed cases above are served by: healthy, with a
    # snapshot taken moments ago.
    "/api/v1/health": api_health(
        {"status": "ok", "last_run_at": NOW, "last_dir": "/snapshots/" + NOW}
    ),
    "/snap-ok/healthz": (200, GREEN_HEALTHZ),
    "/snap-ok/api/v1/health": api_health(
        {"status": "ok", "last_run_at": NOW, "last_dir": "/snapshots/" + NOW}
    ),
    "/snap-error/healthz": (200, GREEN_HEALTHZ),
    "/snap-error/api/v1/health": api_health(
        {
            "status": "error",
            "last_run_at": LONG_AGO,
            "last_error": "export failed: No space left on device",
        }
    ),
    "/snap-stale/healthz": (200, GREEN_HEALTHZ),
    "/snap-stale/api/v1/health": api_health({"status": "ok", "last_run_at": LONG_AGO}),
    "/snap-unknown/healthz": (200, GREEN_HEALTHZ),
    "/snap-unknown/api/v1/health": api_health({"status": "unknown"}),
    "/snap-missing/healthz": (200, GREEN_HEALTHZ),
    "/snap-missing/api/v1/health": api_health(None),
    # No /snap-apidown/api/v1/health route: the endpoint 404s.
    "/snap-apidown/healthz": (200, GREEN_HEALTHZ),
    # Ingest is stale *and* the snapshot is broken: the louder verdict wins.
    "/snap-both-red/healthz": (200, STALE_HEALTHZ),
    "/snap-both-red/api/v1/health": api_health(
        {"status": "error", "last_run_at": LONG_AGO, "last_error": "export failed"}
    ),
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

# ---- the snapshot claim, read from /api/v1/health on a green /healthz ----

expect "a fresh snapshot is reported on the green line" 0 "snapshot last run" \
    "$PROBE" "$BASE/snap-ok/healthz"
expect "a failed snapshot cycle is red" 6 "snapshot worker reported error" \
    "$PROBE" "$BASE/snap-error/healthz"
expect "a failed snapshot cycle names the worker's error" 6 "No space left on device" \
    "$PROBE" "$BASE/snap-error/healthz"
expect "an overdue snapshot is red" 6 "newest snapshot is" \
    "$PROBE" "$BASE/snap-stale/healthz"
expect "the overdue window is the caller's" 0 "snapshot last run" \
    env SNAPSHOT_STALE_AFTER_SECONDS=999999 "$PROBE" "$BASE/snap-stale/healthz"
expect "a stale ingest outranks a broken snapshot" 3 "ingest stale" \
    "$PROBE" "$BASE/snap-both-red/healthz"

# The ambiguous half: no claim either way. Red only where the caller says
# snapshots are expected, because "unknown" is also how every local instance
# reports snapshots being off.
expect "status unknown is not red by default" 0 "snapshot not asserted" \
    "$PROBE" "$BASE/snap-unknown/healthz"
expect "status unknown is red when snapshots are required" 6 "snapshots are required" \
    env SNAPSHOT_CHECK=require "$PROBE" "$BASE/snap-unknown/healthz"
expect "an absent snapshot field degrades, it does not red" 0 "carries no snapshot field" \
    "$PROBE" "$BASE/snap-missing/healthz"
expect "an absent snapshot field is red when snapshots are required" 6 "snapshots are required" \
    env SNAPSHOT_CHECK=require "$PROBE" "$BASE/snap-missing/healthz"
expect "an unreadable /api/v1/health degrades, it does not red" 0 "answered HTTP 404" \
    "$PROBE" "$BASE/snap-apidown/healthz"
expect "an unreadable /api/v1/health is red when snapshots are required" 6 "snapshots are required" \
    env SNAPSHOT_CHECK=require "$PROBE" "$BASE/snap-apidown/healthz"

# Off must not merely ignore the answer — it must not ask the question, which
# is what keeps the probe usable against an Access-fronted host.
off_out="$(SNAPSHOT_CHECK=off "$PROBE" "$BASE/snap-error/healthz" 2>&1)" && off_rc=0 || off_rc=$?
if [ "$off_rc" -eq 0 ] && [ "${off_out#*snapshot}" = "$off_out" ]; then
    pass "SNAPSHOT_CHECK=off says nothing about snapshots"
else
    fail "SNAPSHOT_CHECK=off — exit $off_rc, output: $off_out"
fi

expect "an unknown SNAPSHOT_CHECK value is a config failure" 2 "must be auto, require or off" \
    env SNAPSHOT_CHECK=sometimes "$PROBE" "$BASE/snap-ok/healthz"
expect "a non-numeric snapshot window is a config failure" 2 "SNAPSHOT_STALE_AFTER_SECONDS" \
    env SNAPSHOT_STALE_AFTER_SECONDS=soon "$PROBE" "$BASE/snap-ok/healthz"

# The /healthz URL is the only address the caller gives; a derived address that
# dropped the prefix would hit the healthy root instance and report green.
expect "API_HEALTH_URL overrides the derivation" 6 "snapshot worker reported error" \
    env API_HEALTH_URL="$BASE/snap-error/api/v1/health" "$PROBE" "$BASE/snap-ok/healthz"

echo
echo "passed=$PASS failed=$FAIL"
[ "$FAIL" -eq 0 ]

#!/usr/bin/env bash
#
# probe-healthz.sh — ask the dashboard /healthz and classify the answer.
#
# Distinguishes process-dead, HTTP failure (including 503 = database
# unreadable), ingest staleness, and "alive but never accepted a span".
# Staleness is a body field, not a status code: a silent instance still
# answers 200. Missing freshness keys degrade to liveness so the probe
# stays useful before that contract is deployed.
#
# Usage:
#   scripts/probe-healthz.sh [URL]
#
# Env:
#   HEALTHZ_URL              default https://cotel.aignite.pl/healthz
#   STALE_AFTER_SECONDS      default 21600 (6h)
#   CONNECT_TIMEOUT          default 15
#   CF_ACCESS_CLIENT_ID      optional Cloudflare Access service token
#   CF_ACCESS_CLIENT_SECRET  optional; sent only when both are set
#
# Exit codes:
#   0  healthy
#   1  unreachable (timeout, refused, DNS)
#   2  HTTP non-200
#   3  200 but ingest is stale or the database has never accepted a span
#   4  Cloudflare Access blocked the probe (cannot see /healthz)

set -euo pipefail

URL="${1:-${HEALTHZ_URL:-https://cotel.aignite.pl/healthz}}"
STALE_AFTER_SECONDS="${STALE_AFTER_SECONDS:-21600}"
CONNECT_TIMEOUT="${CONNECT_TIMEOUT:-15}"

if ! [ "$STALE_AFTER_SECONDS" -gt 0 ] 2>/dev/null; then
    echo "probe-healthz: FAILED — STALE_AFTER_SECONDS must be a positive integer, got '${STALE_AFTER_SECONDS}'"
    exit 2
fi
if ! [ "$CONNECT_TIMEOUT" -gt 0 ] 2>/dev/null; then
    echo "probe-healthz: FAILED — CONNECT_TIMEOUT must be a positive integer, got '${CONNECT_TIMEOUT}'"
    exit 2
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
BODY="$TMP/body"
HDRS="$TMP/headers"
ERR="$TMP/curlerr"

CURL_ARGS=(
    -sS
    --max-time "$CONNECT_TIMEOUT"
    --connect-timeout "$CONNECT_TIMEOUT"
    -D "$HDRS"
    -o "$BODY"
    -w '%{http_code}'
)
if [ -n "${CF_ACCESS_CLIENT_ID:-}" ] && [ -n "${CF_ACCESS_CLIENT_SECRET:-}" ]; then
    CURL_ARGS+=(
        -H "CF-Access-Client-Id: ${CF_ACCESS_CLIENT_ID}"
        -H "CF-Access-Client-Secret: ${CF_ACCESS_CLIENT_SECRET}"
    )
fi

set +e
HTTP_CODE="$(curl "${CURL_ARGS[@]}" "$URL" 2>"$ERR")"
CURL_RC=$?
set -e

if [ "$CURL_RC" -ne 0 ]; then
    err="$(tr '\n' ' ' <"$ERR" | sed 's/[[:space:]]*$//')"
    case "$CURL_RC" in
        6)  why="dns lookup failed" ;;
        7)  why="connection refused" ;;
        28) why="timed out after ${CONNECT_TIMEOUT}s" ;;
        *)  why="curl exit ${CURL_RC}" ;;
    esac
    echo "probe-healthz: FAILED — unreachable (${why}${err:+; ${err}}) url=${URL}"
    exit 1
fi

location="$(awk 'tolower($0) ~ /^location:/{sub(/^[^:]+:[[:space:]]*/,""); sub(/\r$/,""); print; exit}' "$HDRS")"
wwwauth="$(awk 'tolower($0) ~ /^www-authenticate:/{sub(/^[^:]+:[[:space:]]*/,""); sub(/\r$/,""); print; exit}' "$HDRS")"
if [ "$HTTP_CODE" = "302" ] || [ "$HTTP_CODE" = "401" ] || [ "$HTTP_CODE" = "403" ]; then
    case "$location $wwwauth" in
        *cloudflareaccess.com*|*Cloudflare-Access*)
            echo "probe-healthz: FAILED — cloudflare access blocked the probe (HTTP ${HTTP_CODE}); cannot verify /healthz url=${URL}"
            exit 4
            ;;
    esac
fi

if [ "$HTTP_CODE" != "200" ]; then
    snippet="$(tr '\n' ' ' <"$BODY" | sed 's/[[:space:]]\{1,\}/ /g; s/^ //; s/ $//' | cut -c1-160)"
    if [ "$HTTP_CODE" = "503" ]; then
        echo "probe-healthz: FAILED — HTTP 503 database unreadable url=${URL}${snippet:+ body=${snippet}}"
    else
        echo "probe-healthz: FAILED — HTTP ${HTTP_CODE} url=${URL}${snippet:+ body=${snippet}}"
    fi
    exit 2
fi

# Body classification lives in python so JSON null is distinct from a missing
# key and from numeric 0. A missing key degrades to liveness; a present null
# is an empty database, which is not freshness.
set +e
eval_out="$(python3 - "$BODY" "$STALE_AFTER_SECONDS" <<'PY'
import json, sys

path, stale_after = sys.argv[1], int(sys.argv[2])
raw = open(path, encoding="utf-8").read()
try:
    body = json.loads(raw)
except json.JSONDecodeError as e:
    print(f"invalid_json {e}")
    sys.exit(2)
if not isinstance(body, dict):
    print("invalid_json not an object")
    sys.exit(2)

ok = body.get("ok")
if ok is False:
    print("ok_false")
    sys.exit(2)

if "newest_span_age_seconds" not in body and "last_ingest_at" not in body:
    print("liveness")
    sys.exit(0)

age = body.get("newest_span_age_seconds")
last = body.get("last_ingest_at")
if age is None or last is None:
    print("empty_db")
    sys.exit(3)

if isinstance(age, bool) or not isinstance(age, (int, float)):
    print(f"bad_age {age!r}")
    sys.exit(2)
age_i = int(age)
if age_i < 0:
    print(f"bad_age {age_i}")
    sys.exit(2)
if age_i > stale_after:
    print(f"stale {age_i} {last}")
    sys.exit(3)
print(f"fresh {age_i} {last}")
sys.exit(0)
PY
)"
eval_rc=$?
set -e

case "$eval_rc" in
    0)
        case "$eval_out" in
            liveness)
                echo "probe-healthz: OK — HTTP 200 liveness (freshness fields absent) url=${URL}"
                ;;
            fresh*)
                age="${eval_out#fresh }"
                echo "probe-healthz: OK — HTTP 200 ingest age ${age%% *}s (threshold ${STALE_AFTER_SECONDS}s) url=${URL}"
                ;;
            *)
                echo "probe-healthz: OK — HTTP 200 url=${URL}"
                ;;
        esac
        exit 0
        ;;
    2)
        echo "probe-healthz: FAILED — HTTP 200 but body unusable (${eval_out}) url=${URL}"
        exit 2
        ;;
    3)
        case "$eval_out" in
            empty_db)
                echo "probe-healthz: FAILED — empty database (last_ingest_at=null, never accepted a span) url=${URL}"
                ;;
            stale*)
                rest="${eval_out#stale }"
                age="${rest%% *}"
                last="${rest#* }"
                echo "probe-healthz: FAILED — ingest stale newest_span_age_seconds=${age} last_ingest_at=${last} threshold=${STALE_AFTER_SECONDS}s url=${URL}"
                ;;
            *)
                echo "probe-healthz: FAILED — ingest not accepting (${eval_out}) url=${URL}"
                ;;
        esac
        exit 3
        ;;
    *)
        echo "probe-healthz: FAILED — evaluator exit ${eval_rc} (${eval_out}) url=${URL}"
        exit 2
        ;;
esac

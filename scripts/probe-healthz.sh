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
# A green /healthz is then followed by a second question to /api/v1/health,
# which is where the snapshot worker's own report lives. /healthz deliberately
# does not carry it: that endpoint is the container liveness contract, and a
# failed backup must not mark a working container unhealthy or fail a deploy.
# The claim "there is a restore point" therefore has to be made here, on the
# alert path, or nothing makes it at all.
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
#   SNAPSHOT_CHECK           auto (default) | require | off
#   SNAPSHOT_STALE_AFTER_SECONDS  default 43200 (2x the shipped 6h interval)
#   API_HEALTH_URL           override; derived from the /healthz URL otherwise
#
# Exit codes:
#   0  healthy
#   1  unreachable (timeout, refused, DNS)
#   2  HTTP non-200
#   3  200 but ingest is stale or the database has never accepted a span
#   4  Cloudflare Access blocked the probe (cannot see /healthz)
#   6  /healthz is green but the database has no current snapshot
#
# 6 and not 5: ~/ops/cotel-healthz.sh already spends 5 on "the LAN half is
# green and the public ingest half is not", and passes a probe's own code
# through otherwise.

set -euo pipefail

URL="${1:-${HEALTHZ_URL:-https://cotel.aignite.pl/healthz}}"
STALE_AFTER_SECONDS="${STALE_AFTER_SECONDS:-21600}"
CONNECT_TIMEOUT="${CONNECT_TIMEOUT:-15}"
SNAPSHOT_CHECK="${SNAPSHOT_CHECK:-auto}"
SNAPSHOT_STALE_AFTER_SECONDS="${SNAPSHOT_STALE_AFTER_SECONDS:-43200}"

if ! [ "$STALE_AFTER_SECONDS" -gt 0 ] 2>/dev/null; then
    echo "probe-healthz: FAILED — STALE_AFTER_SECONDS must be a positive integer, got '${STALE_AFTER_SECONDS}'"
    exit 2
fi
if ! [ "$CONNECT_TIMEOUT" -gt 0 ] 2>/dev/null; then
    echo "probe-healthz: FAILED — CONNECT_TIMEOUT must be a positive integer, got '${CONNECT_TIMEOUT}'"
    exit 2
fi
if ! [ "$SNAPSHOT_STALE_AFTER_SECONDS" -gt 0 ] 2>/dev/null; then
    echo "probe-healthz: FAILED — SNAPSHOT_STALE_AFTER_SECONDS must be a positive integer, got '${SNAPSHOT_STALE_AFTER_SECONDS}'"
    exit 2
fi
case "$SNAPSHOT_CHECK" in
    auto|require|off) ;;
    *)
        echo "probe-healthz: FAILED — SNAPSHOT_CHECK must be auto, require or off, got '${SNAPSHOT_CHECK}'"
        exit 2
        ;;
esac

# The snapshot report is the same entry point with another path, so the caller
# configures one URL. A /healthz served under a prefix keeps that prefix.
derive_api_health_url() {
    local u="${1%%\#*}"
    u="${u%%\?*}"
    u="${u%/}"
    case "$u" in
        */healthz) printf '%s/api/v1/health' "${u%/healthz}" ;;
        *) printf '%s/api/v1/health' "$(printf '%s' "$u" | sed -E 's#^([a-zA-Z][a-zA-Z0-9+.-]*://[^/]+).*#\1#')" ;;
    esac
}
API_HEALTH_URL="${API_HEALTH_URL:-$(derive_api_health_url "$URL")}"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
BODY="$TMP/body"
HDRS="$TMP/headers"
ERR="$TMP/curlerr"

http_get() {
    local url="$1" body="$2" hdrs="$3"
    local args=(
        -sS
        --max-time "$CONNECT_TIMEOUT"
        --connect-timeout "$CONNECT_TIMEOUT"
        -D "$hdrs"
        -o "$body"
        -w '%{http_code}'
    )
    if [ -n "${CF_ACCESS_CLIENT_ID:-}" ] && [ -n "${CF_ACCESS_CLIENT_SECRET:-}" ]; then
        args+=(
            -H "CF-Access-Client-Id: ${CF_ACCESS_CLIENT_ID}"
            -H "CF-Access-Client-Secret: ${CF_ACCESS_CLIENT_SECRET}"
        )
    fi
    curl "${args[@]}" "$url" 2>"$ERR"
}

# An Access-fronted host answers the probe with a login redirect rather than
# the application, which is a probe-configuration verdict, not an outage.
access_blocked() {
    local hdrs="$1" code="$2" location wwwauth
    location="$(awk 'tolower($0) ~ /^location:/{sub(/^[^:]+:[[:space:]]*/,""); sub(/\r$/,""); print; exit}' "$hdrs")"
    wwwauth="$(awk 'tolower($0) ~ /^www-authenticate:/{sub(/^[^:]+:[[:space:]]*/,""); sub(/\r$/,""); print; exit}' "$hdrs")"
    case "$code" in
        302|401|403) ;;
        *) return 1 ;;
    esac
    case "$location $wwwauth" in
        *cloudflareaccess.com*|*Cloudflare-Access*) return 0 ;;
    esac
    return 1
}

set +e
HTTP_CODE="$(http_get "$URL" "$BODY" "$HDRS")"
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

if access_blocked "$HDRS" "$HTTP_CODE"; then
    echo "probe-healthz: FAILED — cloudflare access blocked the probe (HTTP ${HTTP_CODE}); cannot verify /healthz url=${URL}"
    exit 4
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

# snapshot_verdict asks /api/v1/health and sets SNAP_CLASS / SNAP_TEXT.
#
# SNAP_CLASS is one of:
#   ok          the worker reported a complete snapshot inside the window
#   unverified  the instance makes no claim either way — snapshots disabled,
#               the worker has not run yet, or the endpoint is not readable
#   red         the worker reported a failure, or its newest snapshot is older
#               than the window
#
# Only "red" is affirmative evidence of a missing restore point, so only it
# pages under SNAPSHOT_CHECK=auto. "unverified" is the ambiguous half that
# would otherwise make every local instance, where snapshots ship disabled,
# cry wolf; SNAPSHOT_CHECK=require is the caller asserting that this instance
# is one where snapshots are expected, which turns it red too.
SNAP_CLASS=""
SNAP_TEXT=""
snapshot_verdict() {
    local body="$TMP/snapbody" hdrs="$TMP/snapheaders" code rc out

    set +e
    code="$(http_get "$API_HEALTH_URL" "$body" "$hdrs")"
    rc=$?
    set -e

    if [ "$rc" -ne 0 ]; then
        SNAP_CLASS=unverified
        SNAP_TEXT="/api/v1/health not readable (curl exit ${rc}) url=${API_HEALTH_URL}"
        return
    fi
    if access_blocked "$hdrs" "$code"; then
        SNAP_CLASS=unverified
        SNAP_TEXT="/api/v1/health is behind cloudflare access (HTTP ${code}); the snapshot claim can only be made on the LAN url=${API_HEALTH_URL}"
        return
    fi
    if [ "$code" != "200" ]; then
        SNAP_CLASS=unverified
        SNAP_TEXT="/api/v1/health answered HTTP ${code} url=${API_HEALTH_URL}"
        return
    fi

    set +e
    out="$(python3 - "$body" "$SNAPSHOT_STALE_AFTER_SECONDS" <<'PY'
import datetime, json, sys

path, stale_after = sys.argv[1], int(sys.argv[2])


def parse(ts):
    try:
        t = datetime.datetime.fromisoformat(str(ts).strip().replace("Z", "+00:00"))
    except ValueError:
        return None
    return t if t.tzinfo else t.replace(tzinfo=datetime.timezone.utc)


try:
    body = json.loads(open(path, encoding="utf-8").read())
except json.JSONDecodeError as e:
    print(f"unverified /api/v1/health body is not JSON ({e})")
    sys.exit(0)
if not isinstance(body, dict):
    print("unverified /api/v1/health body is not an object")
    sys.exit(0)

snap = body.get("snapshot")
if not isinstance(snap, dict):
    print("unverified /api/v1/health carries no snapshot field (binary predates the contract?)")
    sys.exit(0)

status = snap.get("status") or "unknown"
last = snap.get("last_run_at") or ""
err = " ".join(str(snap.get("last_error") or "").split())[:160]

if status == "error":
    print(f"red snapshot worker reported error last_run_at={last or 'never'}" + (f" last_error={err}" if err else ""))
    sys.exit(0)
if status == "unknown":
    print("unverified snapshot status unknown — the worker has not run yet, or snapshots are disabled on this instance")
    sys.exit(0)
if status != "ok":
    print(f"red snapshot status {status!r} is not a status this contract defines")
    sys.exit(0)

when = parse(last)
if when is None:
    print(f"red snapshot status ok but last_run_at is missing or unparseable ({last!r})")
    sys.exit(0)
age = int((datetime.datetime.now(datetime.timezone.utc) - when).total_seconds())
if age > stale_after:
    print(f"red newest snapshot is {age}s old last_run_at={last} threshold={stale_after}s")
    sys.exit(0)
print(f"ok last run {last} (age {age}s, threshold {stale_after}s)" + (f" dir={snap['last_dir']}" if snap.get("last_dir") else ""))
PY
)"
    rc=$?
    set -e
    if [ "$rc" -ne 0 ]; then
        SNAP_CLASS=unverified
        SNAP_TEXT="snapshot evaluator exit ${rc} (${out})"
        return
    fi
    SNAP_CLASS="${out%% *}"
    SNAP_TEXT="${out#* }"
}

# Reached only on a green /healthz: a dead process or an unreadable database is
# the louder verdict and keeps the line to itself.
report_green() {
    local line="$1"
    if [ "$SNAPSHOT_CHECK" = "off" ]; then
        echo "$line"
        exit 0
    fi
    snapshot_verdict
    case "$SNAP_CLASS" in
        red)
            echo "probe-healthz: FAILED — no current database snapshot: ${SNAP_TEXT} url=${API_HEALTH_URL}"
            exit 6
            ;;
        unverified)
            if [ "$SNAPSHOT_CHECK" = "require" ]; then
                echo "probe-healthz: FAILED — snapshots are required on this instance and it does not report one: ${SNAP_TEXT}"
                exit 6
            fi
            echo "${line} | snapshot not asserted (${SNAP_TEXT})"
            exit 0
            ;;
        *)
            echo "${line} | snapshot ${SNAP_TEXT}"
            exit 0
            ;;
    esac
}

case "$eval_rc" in
    0)
        case "$eval_out" in
            liveness)
                report_green "probe-healthz: OK — HTTP 200 liveness (freshness fields absent) url=${URL}"
                ;;
            fresh*)
                age="${eval_out#fresh }"
                report_green "probe-healthz: OK — HTTP 200 ingest age ${age%% *}s (threshold ${STALE_AFTER_SECONDS}s) url=${URL}"
                ;;
            *)
                report_green "probe-healthz: OK — HTTP 200 url=${URL}"
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

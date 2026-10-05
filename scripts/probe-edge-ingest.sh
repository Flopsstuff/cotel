#!/usr/bin/env bash
#
# probe-edge-ingest.sh — ask the public OTLP ingest URL and classify the answer.
#
# This is the half no LAN probe can see: DNS, the Cloudflare tunnel, and the
# ingest route on the process behind it. A `docker compose up -d` without the
# environment blanks CLOUDFLARE_TUNNEL_TOKEN and takes the public endpoint down
# while /healthz on the LAN stays green — agents keep exporting spans into a
# hole and say nothing.
#
# The check needs no Cloudflare Access service token, because the ingest host is
# not behind Access: an unauthenticated request reaches cotel's own auth
# middleware and comes back 401 from the application. That 401 is the evidence —
# it can only be produced by DNS resolving, the tunnel forwarding, the process
# listening, and /v1/traces being routed. A blanked tunnel token answers with a
# connection error or a Cloudflare 5xx instead.
#
# The expected status is exactly 401. "Any answer" would pass a Cloudflare
# interstitial, and 200 is not reachable without a real token.
#
# Usage:
#   scripts/probe-edge-ingest.sh [URL]
#
# Env:
#   INGEST_URL           default https://otlp.aignite.pl/v1/traces
#   CONNECT_TIMEOUT      default 15
#   INGEST_PROBE_TOKEN   the deliberately invalid bearer; see below
#
# Exit codes:
#   0  the public ingest path is up (the application answered 401)
#   1  unreachable (DNS, refused, timeout) — tunnel down or host gone
#   2  something answered, but it was not the ingest handler's 401
#   4  Cloudflare Access is in front of the ingest host — which also rejects
#      every agent's spans, so it is an outage, not a probe-config problem

set -euo pipefail

URL="${1:-${INGEST_URL:-https://otlp.aignite.pl/v1/traces}}"
CONNECT_TIMEOUT="${CONNECT_TIMEOUT:-15}"
# A bearer that cannot be valid, rather than no bearer at all. cotel's auth
# middleware rejects an unknown `cotel_` token before the ingest handler sees
# the request, so the expected 401 does not depend on how `allow_anonymous` is
# set on the instance — with anonymous ingest allowed, a tokenless request would
# reach the handler and answer 405 to this GET.
PROBE_TOKEN="${INGEST_PROBE_TOKEN:-cotel_edge-probe-not-a-real-token}"

if ! [ "$CONNECT_TIMEOUT" -gt 0 ] 2>/dev/null; then
    echo "probe-edge-ingest: FAILED — CONNECT_TIMEOUT must be a positive integer, got '${CONNECT_TIMEOUT}'"
    exit 2
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
BODY="$TMP/body"
HDRS="$TMP/headers"
ERR="$TMP/curlerr"

# GET, not the POST a real exporter sends: the auth check runs first either way,
# and a GET cannot write anything should the token check ever be bypassed.
set +e
HTTP_CODE="$(curl -sS \
    --max-time "$CONNECT_TIMEOUT" \
    --connect-timeout "$CONNECT_TIMEOUT" \
    -H "Authorization: Bearer ${PROBE_TOKEN}" \
    -D "$HDRS" \
    -o "$BODY" \
    -w '%{http_code}' \
    "$URL" 2>"$ERR")"
CURL_RC=$?
set -e

if [ "$CURL_RC" -ne 0 ]; then
    err="$(tr '\n' ' ' <"$ERR" | sed 's/[[:space:]]*$//')"
    case "$CURL_RC" in
        6)  why="dns lookup failed" ;;
        7)  why="connection refused" ;;
        28) why="timed out after ${CONNECT_TIMEOUT}s" ;;
        35|60) why="tls handshake failed" ;;
        *)  why="curl exit ${CURL_RC}" ;;
    esac
    echo "probe-edge-ingest: FAILED — public ingest unreachable (${why}${err:+; ${err}}) url=${URL}"
    exit 1
fi

location="$(awk 'tolower($0) ~ /^location:/{sub(/^[^:]+:[[:space:]]*/,""); sub(/\r$/,""); print; exit}' "$HDRS")"
wwwauth="$(awk 'tolower($0) ~ /^www-authenticate:/{sub(/^[^:]+:[[:space:]]*/,""); sub(/\r$/,""); print; exit}' "$HDRS")"
case "$location $wwwauth" in
    *cloudflareaccess.com*|*Cloudflare-Access*)
        echo "probe-edge-ingest: FAILED — cloudflare access now fronts the public ingest host (HTTP ${HTTP_CODE}); every agent's spans are rejected the same way url=${URL}"
        exit 4
        ;;
esac

snippet="$(tr '\n' ' ' <"$BODY" | sed 's/[[:space:]]\{1,\}/ /g; s/^ //; s/ $//' | cut -c1-160)"

if [ "$HTTP_CODE" = "401" ]; then
    # Only the application writes a JSON error body here. A Cloudflare
    # interstitial or a proxy's own 401 is HTML, and accepting it would make the
    # probe green while the origin is unreachable.
    case "$snippet" in
        '{'*'"error"'*)
            echo "probe-edge-ingest: OK — HTTP 401 from the ingest handler (dns, tunnel, process and /v1/traces all answering) url=${URL}"
            exit 0
            ;;
    esac
    echo "probe-edge-ingest: FAILED — HTTP 401 but not from the ingest handler (body is not the application's JSON error) url=${URL}${snippet:+ body=${snippet}}"
    exit 2
fi

case "$HTTP_CODE" in
    200)
        echo "probe-edge-ingest: FAILED — HTTP 200 to an invalid token: the public ingest path is reachable but is accepting unauthenticated spans url=${URL}"
        ;;
    404)
        echo "probe-edge-ingest: FAILED — HTTP 404: the host answers but /v1/traces is not routed (wrong tunnel target, or the ingest port is not served) url=${URL}"
        ;;
    502|503|504|520|521|522|523|524|530)
        echo "probe-edge-ingest: FAILED — HTTP ${HTTP_CODE}: cloudflare reached, the origin did not answer (tunnel down or ingest not listening) url=${URL}${snippet:+ body=${snippet}}"
        ;;
    *)
        echo "probe-edge-ingest: FAILED — HTTP ${HTTP_CODE}, wanted 401 from the ingest handler url=${URL}${snippet:+ body=${snippet}}"
        ;;
esac
exit 2

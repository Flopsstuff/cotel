#!/usr/bin/env bash
#
# agent-set-cotel-token.sh — give the running agent its own cotel telemetry token.
#
# cotel attributes OTLP spans to an actor by its actor token. Paperclip sends one
# company-wide OTLP destination and Authorization header (a cotel ingest key) for
# every agent and strips any per-agent OTEL_* env (ADR-0028), so the agent's
# identity travels separately: this script writes the agent's OWN actor token
# into ITS OWN Paperclip adapter env as COTEL_ACTOR_TOKEN, and
# scripts/otel-headers-helper.py (wired as Claude Code's otelHeadersHelper) sends
# it as the X-Cotel-Actor-Token header. Without it the agent's spans are stored as
# Anonymous.
#
# Why self-apply (not a central rollout): identical to the git-identity case —
# the Paperclip API redacts other agents' adapterConfig and rejects cross-agent
# edits (needs `agents:create`), and a blind cross-agent write cannot read and
# preserve another agent's secret env, so it would silently wipe secrets. Each
# agent can read its own full config and merge the variable in safely. See
# scripts/agent-set-git-identity.sh and docs/operations/agent-identity.md.
#
# IMPORTANT — COTEL_ACTOR_TOKEN must live in the per-agent adapter env, never in a
# shared settings.json `env` block: that block is applied at startup and
# OVERRIDES the inherited per-agent process env, so every agent would claim the
# same actor.
#
# The token cannot be derived — it is minted in the cotel dashboard
# (Actors -> Add actor, one actor per agent) and is a `cotel_<64 hex>` string. Pass
# it as the first argument or via COTEL_TOKEN.
#
# Idempotent: re-running with the same token is a no-op.
#
# Requires the Paperclip runtime env (auto-injected in a heartbeat run):
#   PAPERCLIP_AGENT_ID, PAPERCLIP_API_URL, PAPERCLIP_API_KEY
# Optional: PAPERCLIP_RUN_ID (added to the audit trail when present).
#
# Usage (inside an agent heartbeat):
#   scripts/agent-set-cotel-token.sh cotel_<your-64-hex-token>
#   COTEL_TOKEN=cotel_... scripts/agent-set-cotel-token.sh

set -euo pipefail

: "${PAPERCLIP_AGENT_ID:?must run inside a Paperclip agent (PAPERCLIP_AGENT_ID unset)}"
: "${PAPERCLIP_API_URL:?PAPERCLIP_API_URL unset}"
: "${PAPERCLIP_API_KEY:?PAPERCLIP_API_KEY unset}"

TOKEN="${1:-${COTEL_TOKEN:-}}"
if [ -z "$TOKEN" ]; then
  echo "usage: $0 <cotel_token>   (or set COTEL_TOKEN)" >&2
  echo "mint one in the cotel dashboard: Actors -> Add actor (one per agent)" >&2
  exit 2
fi
if ! printf '%s' "$TOKEN" | grep -Eq '^cotel_[0-9a-f]{64}$'; then
  echo "error: token must look like 'cotel_<64 lowercase hex>'" >&2
  exit 2
fi

python3 - "$PAPERCLIP_AGENT_ID" "$TOKEN" <<'PY'
import json, os, sys, urllib.request

agent_id, token = sys.argv[1], sys.argv[2]
api = os.environ["PAPERCLIP_API_URL"].rstrip("/")
key = os.environ["PAPERCLIP_API_KEY"]
run = os.environ.get("PAPERCLIP_RUN_ID")

def call(method, path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    headers = {"Authorization": f"Bearer {key}"}
    if data is not None:
        headers["Content-Type"] = "application/json"
    if run:
        headers["X-Paperclip-Run-Id"] = run
    req = urllib.request.Request(f"{api}{path}", data=data, method=method, headers=headers)
    with urllib.request.urlopen(req) as r:
        return json.load(r)

me = call("GET", "/api/agents/me")
name = me.get("name") or "(unknown)"

ac = me.get("adapterConfig") or {}
env = dict(ac.get("env") or {})  # readable for SELF — preserve existing (incl. secrets)
def current(k):
    v = env.get(k)
    return v.get("value") if isinstance(v, dict) else v

# Since Paperclip v2026.1106 the per-agent OTEL_* env is stripped before every
# run and the company-wide observability destination is applied instead
# (ADR-0028). The agent's identity therefore travels as COTEL_ACTOR_TOKEN, which
# scripts/otel-headers-helper.py turns into the X-Cotel-Actor-Token header. A
# leftover OTEL_EXPORTER_OTLP_HEADERS is dropped: it is stripped anyway and only
# misleads whoever reads the config.
stale = "OTEL_EXPORTER_OTLP_HEADERS" in env
if current("COTEL_ACTOR_TOKEN") == token and not stale:
    print(f"cotel actor token already set for {name} (token ...{token[-8:]}) — no change")
    sys.exit(0)

env.pop("OTEL_EXPORTER_OTLP_HEADERS", None)
env["COTEL_ACTOR_TOKEN"] = {"type": "plain", "value": token}

# Send ONLY env (the API shallow-merges top-level adapterConfig keys, and an
# env-only PATCH avoids the 403 that a full-adapterConfig self-PATCH triggers for
# managed-instruction agents). See agent-set-git-identity.sh for the full note.
call("PATCH", f"/api/agents/{agent_id}", {"adapterConfig": {"env": env}})
print(f"set cotel actor token for {name} (token ...{token[-8:]}) — applies on the next run")
PY

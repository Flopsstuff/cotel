#!/usr/bin/env bash
#
# agent-set-cotel-token.sh — give the running agent its own cotel telemetry token.
#
# cotel attributes OTLP spans to a user by the bearer token in
# OTEL_EXPORTER_OTLP_HEADERS. If every Claude Code instance sends the same token,
# all agents merge into one bucket. This script makes each agent self-attribute
# by writing its OWN token into ITS OWN Paperclip adapter env, so every future
# run exports telemetry under that agent's distinct cotel user.
#
# Why self-apply (not a central rollout): identical to the git-identity case —
# the Paperclip API redacts other agents' adapterConfig and rejects cross-agent
# edits (needs `agents:create`), and a blind cross-agent write cannot read and
# preserve another agent's secret env, so it would silently wipe secrets. Each
# agent can read its own full config and merge the OTEL header in safely. See
# scripts/agent-set-git-identity.sh and docs/operations/agent-identity.md.
#
# IMPORTANT — this sets ONLY the per-agent identity header. The shared,
# identity-independent OTEL config (CLAUDE_CODE_ENABLE_TELEMETRY,
# OTEL_TRACES_EXPORTER, OTEL_EXPORTER_OTLP_PROTOCOL,
# OTEL_EXPORTER_OTLP_TRACES_ENDPOINT) stays in the global ~/.claude/settings.json
# `env` block, which every Claude Code launch reads. The identity header must NOT
# live there: settings.json `env` is applied at startup and OVERRIDES the
# inherited per-agent process env, so a hardcoded header there clobbers this one.
# (Root cause of FLO-486.)
#
# The token cannot be derived — it is minted in the cotel dashboard
# (Users -> Add user, one user per agent) and is a `cotel_<64 hex>` string. Pass
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
  echo "mint one in the cotel dashboard: Users -> Add user (one per agent)" >&2
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
header = f"Authorization=Bearer {token}"

def current(k):
    v = env.get(k)
    return v.get("value") if isinstance(v, dict) else v

if current("OTEL_EXPORTER_OTLP_HEADERS") == header:
    print(f"cotel token already set for {name} (token ...{token[-8:]}) — no change")
    sys.exit(0)

env["OTEL_EXPORTER_OTLP_HEADERS"] = {"type": "plain", "value": header}

# Send ONLY env (the API shallow-merges top-level adapterConfig keys, and an
# env-only PATCH avoids the 403 that a full-adapterConfig self-PATCH triggers for
# managed-instruction agents). See agent-set-git-identity.sh for the full note.
call("PATCH", f"/api/agents/{agent_id}", {"adapterConfig": {"env": env}})
print(f"set cotel token for {name} (token ...{token[-8:]}) — applies on the next run")
PY

#!/usr/bin/env bash
#
# agent-set-git-identity.sh — give the running agent its own git identity.
#
# All FlopBut agents push through the board's single GitHub token, so commits
# and PRs would otherwise all look like the same author. This script makes each
# agent self-attribute by writing GIT_AUTHOR_* / GIT_COMMITTER_* into ITS OWN
# Paperclip adapter env, so every future run commits as that agent.
#
# Why self-apply (not a central rollout): the Paperclip API redacts other
# agents' adapterConfig and rejects cross-agent edits (needs `agents:create`).
# Each agent, however, can read its own full config — including secret env like
# adapter API keys — and merge the GIT_* vars in WITHOUT wiping those secrets.
# That makes self-apply both the only permitted path and the only safe one.
#
# Idempotent: re-running is a no-op once the four vars already match.
#
# Requires the Paperclip runtime env (auto-injected in a heartbeat run):
#   PAPERCLIP_AGENT_ID, PAPERCLIP_API_URL, PAPERCLIP_API_KEY
# Optional: PAPERCLIP_RUN_ID (added to the audit trail when present).
#
# Usage (inside an agent heartbeat):
#   scripts/agent-set-git-identity.sh
#
# See docs/operations/agent-identity.md.

set -euo pipefail

: "${PAPERCLIP_AGENT_ID:?must run inside a Paperclip agent (PAPERCLIP_AGENT_ID unset)}"
: "${PAPERCLIP_API_URL:?PAPERCLIP_API_URL unset}"
: "${PAPERCLIP_API_KEY:?PAPERCLIP_API_KEY unset}"

EMAIL_DOMAIN="agents.flopbut.local"

python3 - "$PAPERCLIP_AGENT_ID" "$EMAIL_DOMAIN" <<'PY'
import json, os, sys, urllib.request

agent_id, domain = sys.argv[1], sys.argv[2]
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
name = me.get("name")
if not name:
    sys.exit("could not resolve agent name from /api/agents/me")
slug = name.strip().lower()
email = f"{slug}@{domain}"

ac = me.get("adapterConfig") or {}
env = dict(ac.get("env") or {})  # readable for SELF — preserve existing (incl. secrets)
want = {
    "GIT_AUTHOR_NAME": name,
    "GIT_AUTHOR_EMAIL": email,
    "GIT_COMMITTER_NAME": name,
    "GIT_COMMITTER_EMAIL": email,
}

def current(k):
    v = env.get(k)
    return v.get("value") if isinstance(v, dict) else v

if all(current(k) == v for k, v in want.items()):
    print(f"git identity already set for {name} <{email}> — no change")
    sys.exit(0)

for k, v in want.items():
    env[k] = {"type": "plain", "value": v}

# Send ONLY env, not the whole adapterConfig. The API shallow-merges top-level
# adapterConfig keys, so model / skills / instructions are preserved either way.
# But it also rejects agent self-edits that carry instructions* bundle-config
# keys (403 "cannot modify instructions path or bundle configuration") — even
# when unchanged. Empirically verified: a full-adapterConfig self-PATCH 403s for
# managed-instruction agents, while an env-only PATCH returns 200 and leaves the
# rest of adapterConfig intact.
call("PATCH", f"/api/agents/{agent_id}", {"adapterConfig": {"env": env}})
print(f"set git identity: {name} <{email}> (applies on the next run)")
PY

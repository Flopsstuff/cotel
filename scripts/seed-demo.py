#!/usr/bin/env python3
"""Seed a cotel instance with synthetic Claude Code telemetry.

Point it at a *throwaway* instance — it creates users and ingests spans, it
never deletes anything. Used to produce the README screenshots and to give a
fresh install something to look at.

The payloads are the same OTLP/HTTP JSON Claude Code sends: a
``claude_code.model_invocation`` span per turn carrying model and token counts,
``claude_code.tool_use`` children carrying only ``tool_name``, and the session
id on the resource. Cost is left to cotel to derive, as it is in production.

    python3 scripts/seed-demo.py --dash-url http://localhost:8080 \
                                 --ingest-url http://localhost:4318

Run it against a database that already has data and you get two overlapping
datasets; start from an empty volume.
"""

import argparse
import json
import random
import sys
import urllib.error
import urllib.request
from datetime import datetime, timedelta, timezone

# Named principals, heaviest first. Weight drives how many sessions each one
# opens, so the Users top-5 has a readable spread instead of seven equal bars.
USERS = [
    ("alex-mbp", 1.00),
    ("priya-mbp", 0.85),
    ("sam-desktop", 0.70),
    ("ci-runner", 0.60),
    ("dana-wsl", 0.45),
    ("jordan-mbp", 0.35),
    ("nightly-bot", 0.25),
]

# Per-user model mix: (model, weight). ci-runner and nightly-bot are automation
# and ride the cheap tiers.
MODEL_MIX = {
    "ci-runner": [("claude-haiku-4-5", 0.7), ("claude-sonnet-5", 0.3)],
    "nightly-bot": [("claude-haiku-4-5", 0.6), ("claude-sonnet-5", 0.4)],
    "_default": [("claude-opus-5", 0.55), ("claude-sonnet-5", 0.35), ("claude-haiku-4-5", 0.10)],
}

# (tool, share of calls, min ms, max ms, failure rate). A session is flagged
# ERROR when any one of its ~50 tool calls failed, so per-tool rates in the
# single-digit percent range would paint most of the sessions list red.
TOOLS = [
    ("Read", 0.26, 30, 400, 0.0008),
    ("Edit", 0.17, 60, 900, 0.0042),
    ("Bash", 0.16, 150, 24000, 0.0086),
    ("Grep", 0.12, 80, 1200, 0.0012),
    ("Glob", 0.08, 40, 600, 0.0006),
    ("Write", 0.07, 50, 700, 0.0016),
    ("TodoWrite", 0.06, 20, 120, 0.0),
    ("Task", 0.04, 18000, 240000, 0.0034),
    ("WebFetch", 0.03, 900, 6500, 0.0148),
    ("NotebookEdit", 0.01, 80, 800, 0.0020),
]

# Weekday index -> session multiplier. Weekends are quiet but not dead.
DAY_WEIGHT = [1.0, 1.05, 1.0, 0.95, 0.8, 0.22, 0.18]

STATUS_OK = 1
STATUS_ERROR = 2


def post(url, payload, token=None, timeout=60):
    body = json.dumps(payload).encode()
    req = urllib.request.Request(url, data=body, method="POST")
    req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        raw = resp.read()
    return json.loads(raw) if raw else {}


def create_users(dash_url, rng):
    tokens = {}
    for name, _ in USERS:
        try:
            created = post(dash_url + "/api/v1/users", {"name": name})
        except urllib.error.HTTPError as e:
            sys.exit("could not create user %s: %s %s" % (name, e.code, e.read().decode()[:200]))
        tokens[name] = created["token"]
    return tokens


def pick(rng, weighted):
    total = sum(w for _, w in weighted)
    r = rng.random() * total
    for item, w in weighted:
        r -= w
        if r <= 0:
            return item
    return weighted[-1][0]


def hex_id(rng, n):
    return "".join(rng.choice("0123456789abcdef") for _ in range(n))


def nano(ts):
    return str(int(ts.timestamp() * 1_000_000_000))


def model_tokens(rng, model):
    """Token counts for one turn. Cache reads dominate, as they do in real use."""
    if model.startswith("claude-haiku"):
        scale = 0.45
    elif model.startswith("claude-sonnet"):
        scale = 0.75
    else:
        scale = 1.0
    return {
        "input_tokens": int(rng.randint(400, 3200) * scale),
        "output_tokens": int(rng.randint(180, 3600) * scale),
        "cache_read_tokens": int(rng.randint(18000, 240000) * scale),
        "cache_creation_tokens": int(rng.randint(0, 9000) * scale),
    }


def build_session(rng, user, start, session_id):
    """Return the OTLP resourceSpans payload for one session."""
    model = pick(rng, MODEL_MIX.get(user, MODEL_MIX["_default"]))
    trace_id = hex_id(rng, 32)
    turns = rng.randint(4, 22)
    # A session that errors does so on a tool, not on the whole run; roughly one
    # in ten ends up flagged ERROR on the sessions list.
    spans = []
    cursor = start

    for _ in range(turns):
        think = timedelta(seconds=rng.randint(4, 70))
        inv_start = cursor
        inv_end = inv_start + think
        attrs = [{"key": "model", "value": {"stringValue": model}}]
        for key, val in model_tokens(rng, model).items():
            attrs.append({"key": key, "value": {"intValue": str(val)}})
        parent = hex_id(rng, 16)
        spans.append({
            "traceId": trace_id,
            "spanId": parent,
            "parentSpanId": "",
            "name": "claude_code.model_invocation",
            "startTimeUnixNano": nano(inv_start),
            "endTimeUnixNano": nano(inv_end),
            "status": {"code": STATUS_OK},
            "attributes": attrs,
        })
        cursor = inv_end

        for _ in range(rng.randint(1, 5)):
            tool, _share, lo, hi, fail_rate = pick(
                rng, [(t, t[1]) for t in TOOLS]
            )
            dur = timedelta(milliseconds=rng.randint(lo, hi))
            failed = rng.random() < fail_rate
            spans.append({
                "traceId": trace_id,
                "spanId": hex_id(rng, 16),
                "parentSpanId": parent,
                "name": "claude_code.tool_use",
                "startTimeUnixNano": nano(cursor),
                "endTimeUnixNano": nano(cursor + dur),
                "status": {"code": STATUS_ERROR if failed else STATUS_OK},
                "attributes": [{"key": "tool_name", "value": {"stringValue": tool}}],
            })
            cursor += dur + timedelta(milliseconds=rng.randint(200, 2500))

    return {
        "resourceSpans": [{
            "resource": {
                "attributes": [
                    {"key": "service.name", "value": {"stringValue": "claude-code"}},
                    {"key": "service.version", "value": {"stringValue": "2.1.4"}},
                    {"key": "session.id", "value": {"stringValue": session_id}},
                ]
            },
            "scopeSpans": [{
                "scope": {"name": "claude.code", "version": "2.1.4"},
                "spans": spans,
            }],
        }]
    }


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--dash-url", default="http://localhost:8080")
    ap.add_argument("--ingest-url", default="http://localhost:4318")
    ap.add_argument("--days", type=int, default=90)
    ap.add_argument("--seed", type=int, default=1729, help="RNG seed; fixed so runs are reproducible")
    args = ap.parse_args()

    dash = args.dash_url.rstrip("/")
    ingest = args.ingest_url.rstrip("/") + "/v1/traces"
    rng = random.Random(args.seed)

    tokens = create_users(dash, rng)
    print("created %d users" % len(tokens))

    now = datetime.now(timezone.utc)
    sessions = 0
    spans = 0

    for day_offset in range(args.days, -1, -1):
        day = (now - timedelta(days=day_offset)).replace(
            hour=0, minute=0, second=0, microsecond=0
        )
        weight = DAY_WEIGHT[day.weekday()]
        for user, user_weight in USERS:
            count = rng.random() * 3.4 * weight * user_weight
            for _ in range(int(count) + (1 if rng.random() < count % 1 else 0)):
                # Working hours, jittered; the current day stops at "now" so the
                # newest rows are not in the future.
                start = day + timedelta(
                    hours=rng.randint(8, 20),
                    minutes=rng.randint(0, 59),
                    seconds=rng.randint(0, 59),
                )
                if start > now - timedelta(minutes=25):
                    continue
                session_id = "sess_" + hex_id(rng, 12)
                payload = build_session(rng, user, start, session_id)
                try:
                    post(ingest, payload, token=tokens[user])
                except urllib.error.URLError as e:
                    sys.exit("ingest failed: %s" % e)
                sessions += 1
                spans += len(payload["resourceSpans"][0]["scopeSpans"][0]["spans"])
        if day_offset % 10 == 0:
            print("  ...%s: %d sessions, %d spans so far" % (day.date(), sessions, spans))

    print("seeded %d sessions / %d spans across %d days" % (sessions, spans, args.days))


if __name__ == "__main__":
    main()

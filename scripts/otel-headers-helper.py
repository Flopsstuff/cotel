#!/usr/bin/env python3
#
# otel-headers-helper.py — Claude Code `otelHeadersHelper` that adds the cotel
# actor token to the OTLP headers a sender already has.
#
# Why it exists: when one OTLP destination and one set of headers is shared by
# many senders (Paperclip's company-wide "Agent observability" setting applies
# the same headers to every agent run), the shared `Authorization: Bearer
# <ingest key>` header authorizes the request but cannot say which actor sent
# it. cotel reads the actor from a second header, `X-Cotel-Actor-Token`, and
# this helper puts each sender's own actor token there from the env var
# COTEL_ACTOR_TOKEN. See docs/operations/actors-and-auth.md ("Ingest keys") and
# docs/decisions/0028-ingest-keys-and-actor-tokens.md.
#
# Wiring: in the settings.json that the agent's Claude Code reads
# (e.g. ~/.claude/settings.json), set
#
#     "otelHeadersHelper": "/path/to/otel-headers-helper.py"
#
# and give each agent its own COTEL_ACTOR_TOKEN in its process env. Do not put
# COTEL_ACTOR_TOKEN in the settings.json `env` block: that block is shared by
# every launch and would give all agents the same identity.
#
# Claude Code runs the helper at startup and again every 29 minutes
# (CLAUDE_CODE_OTEL_HEADERS_HELPER_DEBOUNCE_MS), only for the http/protobuf and
# http/json protocols. How its output merges with the static
# OTEL_EXPORTER_OTLP_*HEADERS env vars is not documented, so this helper prints
# the COMPLETE header set itself:
#
#   1. OTEL_EXPORTER_OTLP_HEADERS, then OTEL_EXPORTER_OTLP_TRACES_HEADERS on top
#      (the per-signal value wins for the same header name, compared
#      case-insensitively). Both use the OTEL format: comma-separated
#      `key=value` pairs with percent-encoded values. Malformed entries are
#      skipped.
#   2. If COTEL_ACTOR_TOKEN is non-empty, `X-Cotel-Actor-Token: <token>`.
#
# Contract: if the helper fails or prints anything but a JSON object, Claude
# Code stops exporting until it works again. So this script always prints
# exactly one JSON object of string -> string to stdout and exits 0, even on an
# unexpected error (it then prints whatever it had built, possibly `{}`).
# Diagnostics go to stderr only, and header values are never printed there.
#
# Stdlib only; Python 3.9 or newer.

import json
import os
import sys
from urllib.parse import unquote

ACTOR_HEADER = "X-Cotel-Actor-Token"
_written = False


def parse_otel_headers(raw):
    """Parse an OTEL `key=value,key2=value2` string into (key, value) pairs."""
    pairs = []
    if not raw:
        return pairs
    for entry in raw.split(","):
        if "=" not in entry:
            continue
        key, _, value = entry.partition("=")
        key = key.strip()
        if not key:
            continue
        value = unquote(value.strip()).strip()
        pairs.append((key, value))
    return pairs


def put(headers, key, value):
    """Set a header, replacing any existing one whose name differs only in case."""
    for existing in [k for k in headers if k.lower() == key.lower()]:
        del headers[existing]
    headers[key] = value


def build(headers):
    for var in ("OTEL_EXPORTER_OTLP_HEADERS", "OTEL_EXPORTER_OTLP_TRACES_HEADERS"):
        try:
            for key, value in parse_otel_headers(os.environ.get(var, "")):
                put(headers, key, value)
        except Exception as exc:  # keep going with what we have
            print(f"otel-headers-helper: could not parse {var}: {type(exc).__name__}",
                  file=sys.stderr)
    token = os.environ.get("COTEL_ACTOR_TOKEN", "").strip()
    if token:
        put(headers, ACTOR_HEADER, token)


def main():
    global _written
    headers = {}
    try:
        build(headers)
    except Exception as exc:
        print(f"otel-headers-helper: unexpected error: {type(exc).__name__}",
              file=sys.stderr)
    try:
        out = json.dumps({str(k): str(v) for k, v in headers.items()})
    except Exception:
        out = "{}"
    try:
        _written = True
        sys.stdout.write(out + "\n")
        sys.stdout.flush()
    except Exception:
        pass
    return 0


if __name__ == "__main__":
    try:
        main()
    except BaseException:
        if not _written:
            try:
                sys.stdout.write("{}\n")
                sys.stdout.flush()
            except BaseException:
                pass
    sys.exit(0)

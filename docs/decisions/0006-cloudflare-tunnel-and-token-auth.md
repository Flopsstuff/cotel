# ADR-0006: Cloudflare Tunnel for Public Access and In-App Token Auth for OTLP Ingest

**Status:** Accepted (amended 2026-05-10 — local-config mode added, see [FLO-90](/FLO/issues/FLO-90))  
**Date:** 2026-05-10  
**Issue:** [FLO-63](/FLO/issues/FLO-63)

## Context

cotel's design goal is "trivially easy self-host." The default deployment is a single Docker container with a named volume and no reverse proxy. Two problems arise once an operator wants to expose cotel beyond localhost:

1. **Dashboard access from the internet** — operators need TLS termination, a stable public hostname, and authentication to prevent data exposure.
2. **Claude Code OTLP ingestion from the internet** — Claude Code must be able to reach the ingest endpoint (`/v1/traces`) over HTTPS and authenticate its requests.

Rolling custom TLS + auth in-app would inflate the deployment surface (a second process for a proxy, or a complex config for internal TLS). The "one container, one volume" invariant prohibits adding a sidecar or requiring nginx outside the image.

## Options Considered

| Option | Summary | Verdict |
|--------|---------|---------|
| **Cloudflare Tunnel + Zero Trust** | cloudflared binary in the image; `CLOUDFLARE_TUNNEL_TOKEN` env var activates it; Cloudflare Zero Trust guards the dashboard. OTLP ingest protected by in-app bearer tokens. | **Chosen** |
| Nginx sidecar | Separate container; TLS termination + basic auth. Breaks "one container" invariant. | Rejected |
| Caddy inside the image | Second process managed by a process supervisor. Adds ~50 MB and a config surface; no richer auth than basic. | Rejected |
| Let Encrypt via traefik | Multi-container compose; external dependency on traefik. Violates one-service constraint. | Rejected |
| No public exposure | Leave ingest on localhost only. Acceptable for local dev; unusable for remote Claude Code agents. | Not sufficient |

## Decision

**Cloudflare Tunnel for public access (token mode by default; locally-managed mode supported as an alternative for operators who prefer file-based config), in-app SHA-256 bearer tokens for OTLP ingest authentication.**

Implementation details:

- The Dockerfile downloads the `cloudflared` binary in a separate build stage, pinned to a specific version (`v2024.11.1`), multi-arch (amd64/arm64).
- `scripts/entrypoint.sh` supports two modes (token takes precedence):
  - **Token mode:** `CLOUDFLARE_TUNNEL_TOKEN` env var → `cloudflared tunnel run --token …`. Ingress rules live in the Cloudflare dashboard.
  - **Local-config mode:** `/etc/cloudflared/config.yml` mounted into the container → `cloudflared tunnel --config … run`. Ingress rules live in a YAML file, reviewable in git.
  - If neither is set, cloudflared is not started (localhost-only deployment).
- SIGTERM propagates to both processes for clean shutdown.
- Operators who don't need Cloudflare ignore both options entirely — behavior is unchanged (localhost only).
- Dashboard is protected by Cloudflare Zero Trust (external, configured in the Cloudflare dashboard — not in cotel).
- OTLP ingest (`/v1/traces`) requires a `Authorization: Bearer <token>` header. Tokens are stored as SHA-256 hashes in the `api_tokens` table (schema v4). The in-app Tokens page lets operators create and revoke tokens.
- No networking or auth code change is needed to move from local to public — only the env var / mount and Cloudflare configuration change.

## Consequences

**Positive:**
- Deployment story stays trivial: one `docker run` with one additional env var for public mode.
- TLS, DDoS protection, and dashboard SSO are delegated to Cloudflare — no crypto code in cotel.
- Tokens are rotatable without restarting the container.
- Multi-arch binary download ensures arm64 (Raspberry Pi) and amd64 both work.

**Negative / Trade-offs:**
- Public access requires a Cloudflare account (free tier sufficient for most operators).
- cloudflared version must be manually bumped in the Dockerfile when updates are needed.
- The entrypoint adds a shell wrapper; cotel no longer runs as PID 1 (minor — health checks should target the cotel process, not the wrapper).
- If cloudflared crashes, cotel continues running — intentional (ingest should survive tunnel restarts) but operators must monitor cloudflared separately if uptime matters.

**Local-config mode specific trade-offs:**
- Requires a one-time bootstrap on the host (`cloudflared tunnel login` + `tunnel create` + `tunnel route dns`) before the container can start with a working tunnel.
- The credentials JSON (`<UUID>.json`) must be present in the mounted directory at every container start. For HA / multi-replica setups this file must be synchronized across all hosts — a known limitation; token mode is simpler for multi-host.
- `cert.pem` (written by `tunnel login`) is only needed for the bootstrap steps; it is not required at container runtime.
- Config changes (adding/removing hostnames) require editing `config.yml` and restarting the container.

## References

- [FLO-65](/FLO/issues/FLO-65) — Dockerfile + cloudflared binary (commit `f2b2d7a`)
- [FLO-66](/FLO/issues/FLO-66) — api_tokens schema v4 + storage CRUD (commit `37f343e`)
- [FLO-67](/FLO/issues/FLO-67) — API endpoints + OTLP Bearer middleware
- [FLO-68](/FLO/issues/FLO-68) — Frontend Tokens page
- [FLO-89](/FLO/issues/FLO-89) — locally-managed tunnel mode (this amendment)
- ADR-0001: DuckDB storage (token table lives alongside telemetry data)
- [Operations: token mode](../operations/cloudflare-tunnel-remote.md)
- [Operations: local-config mode](../operations/cloudflare-tunnel-local.md)

# Architecture Decisions

Architecture Decision Records (ADRs) for cotel. Each record documents a significant technical choice: the context that made it necessary, the options considered, the decision made, and its consequences.

New ADRs go in this directory as `NNNN-short-title.md`, numbered sequentially.

## Records

| # | Title | Status |
|---|-------|--------|
| [ADR-0001](./0001-storage) | Storage Engine — DuckDB | Accepted |
| [ADR-0002](./0002-dashboard-react-spa) | Dashboard — React SPA + JSON API | Accepted |
| [ADR-0003](./0003-release-policy) | Release Policy and Versioning | Accepted |
| [ADR-0004](./0004-multi-user-separation) | Multi-User Telemetry Separation via `user.id` | Accepted |
| [ADR-0005](./0005-export-import-format) | Export/Import Format — Versioned ZIP/CSV/Manifest | Accepted |
| [ADR-0006](./0006-cloudflare-tunnel-and-token-auth) | Cloudflare Tunnel + In-App Bearer Tokens for OTLP Auth | Accepted |
| [ADR-0007](./0007-github-intake-security) | GitHub Issue Intake Security Hardening | Accepted |
| [ADR-0008](./0008-per-agent-telemetry-identity) | Per-agent telemetry identity must not live in shared settings.json env | Accepted |

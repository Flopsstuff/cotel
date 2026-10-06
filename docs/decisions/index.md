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
| [ADR-0009](./0009-daily-usage-unknown-sentinel) | `daily_usage` roll-up normalises empty/NULL keys to an `unknown` sentinel | Accepted |
| [ADR-0010](./0010-schema-version-guard) | Guard schema migrations behind a recorded version | Accepted |
| [ADR-0011](./0011-users-list-ranged-stats-and-server-side-sort) | Users list — time-ranged stats, server-side sort and pagination | Accepted |
| [ADR-0012](./0012-tools-list-ranged-stats-and-server-side-sort) | Tools list — time-ranged stats, server-side sort and pagination | Accepted |
| [ADR-0013](./0013-spans-has-no-derived-columns) | `spans` carries no derived columns: drop `duration_ms` | Accepted |
| [ADR-0014](./0014-overview-single-range-selector) | Overview — one range selector every panel obeys | Accepted |
| [ADR-0015](./0015-overview-activity-and-cost-one-block) | Overview — spans and cost share one block, and one plot | Accepted |
| [ADR-0016](./0016-overview-activity-grid) | Overview — an activity grid, one cell per bucket | Accepted |
| [ADR-0017](./0017-chart-palette-ruler-is-pinned) | The chart-palette ruler is pinned, and a Go test holds it | Accepted |
| [ADR-0018](./0018-duckdb-go-v2-driver) | DuckDB driver — `duckdb/duckdb-go/v2`, with the storage format pinned | Accepted |
| [ADR-0019](./0019-ci-never-mutates-an-issue) | CI never mutates an issue: recovery wakes the alert's assignee | Superseded by ADR-0021 |
| [ADR-0020](./0020-recovery-arrives-as-a-new-issue) | Recovery arrives as a new issue, and dedup is time-bounded | Superseded by ADR-0021 |
| [ADR-0021](./0021-recovery-wakes-the-alerts-assignee) | Recovery wakes the alert's assignee, and dedup is time-bounded | Accepted |
| [ADR-0022](./0022-health-probe-scheduler-outside-github) | The health probe's scheduler lives outside this repo | Accepted |
| [ADR-0023](./0023-production-database-snapshots) | Snapshots: `EXPORT DATABASE` to Parquet, on a timer, from inside cotel | Accepted |

# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed
- Pricing table now covers the Claude 5 family (Fable 5, Mythos 5, Opus 5, Sonnet 5) and the missing 4.x models (Opus 4.8, Opus 4.6). Spans from `claude-opus-5` were previously recorded with `cost_usd = 0` because the model was absent from the table
- Corrected two stale rates: Opus 4.7 ($15.00/$75.00 → $5.00/$25.00) and Haiku 4.5 ($0.80/$4.00 → $1.00/$5.00) per MTok
- Retention roll-up no longer aborts with `NOT NULL constraint failed: daily_usage.model` when a span has an empty or NULL `model`, `session_id`, or `tool_name`. Such spans are now rolled up under an `unknown` sentinel instead of silently stopping all aggregation and purging ([ADR-0009](docs/decisions/0009-daily-usage-unknown-sentinel.md), FLO-553)
- No more silent telemetry loss on restart/deploy: the ingest (`:4318`) and dashboard (`:8080`) ports now bind and accept connections **before** the DuckDB open (WAL replay + schema migration), which can take minutes on a large production database. During that window both ports answer a retryable `503` with `Retry-After` instead of resetting the connection, so OTLP exporters retry and spans are delivered once cotel is ready. Previously the ports bound only after the open finished, so every deploy had a multi-minute window where ingest refused connections and dropped spans
- Retention roll-up no longer loses part of a day's usage. The cutoff was a wall-clock instant, so with the worker ticking every 6h (`COTEL_RETENTION_INTERVAL`) a day was rolled up in slices; because `daily_usage` is keyed by day and written with `INSERT OR REPLACE`, each slice overwrote the previous one while its raw spans were already purged. Every day's `span_count`, token totals and `total_cost_usd` were therefore systematically understated — only the last slice survived. The cutoff is now snapped back to UTC midnight — the same boundary the `day` key is bucketed on — so only whole days are ever rolled up and purged. Trade-off: raw spans now live up to one day longer than `COTEL_RETENTION_RAW_DAYS` (default 30). Aggregates already flattened by the old behaviour cannot be recovered — the raw spans are gone
- Retention is now correct on servers that do not run in UTC. A span's `day` is its UTC calendar day, but both retention cutoffs were computed in the server's local zone, so on any host at a non-zero UTC offset — including UTC+1/+2 — the boundary fell inside a day bucket and re-introduced the overwrite above for spans near midnight UTC. Both cutoffs are now computed in UTC, and the retention tests run under a matrix of server timezones so the alignment cannot silently regress
- Retention roll-up no longer overwrites an already-rolled-up day's aggregate when a span dated to that day arrives late — a backfill, or an import of telemetry older than `COTEL_RETENTION_RAW_DAYS`. The day had already been aggregated and its raw spans purged, so the next cycle recomputed the day from the single late span alone and `INSERT OR REPLACE`d the correct total away, silently corrupting historical cost. Late spans now **accumulate** into the existing `daily_usage` row (`ON CONFLICT DO UPDATE`) instead of replacing it; the accumulate and the raw-span purge run in one transaction so a crash between them cannot double-count

### Added
- Startup logging of each phase — `opening db`, `db ready: schema/migrations applied in <duration>`, and `ready: serving live traffic …` — so a slow open is visible instead of a silent container. Plus a Docker `HEALTHCHECK` (`cotel -healthcheck`, which probes the dashboard `/healthz`) so the container reports `health: starting` until the database is open rather than a misleading `Up`; no curl/wget is added to the runtime image
- `daily_usage` now preserves cache-token totals through roll-up: new `total_cache_read_tokens` and `total_cache_write_tokens` columns (schema v8) are filled by `RollupAndPurge` and carried through `/api/v1/export` and import. Previously only input/output tokens survived the roll-up, so aggregates older than the raw-span retention window (default 30 days) undercounted total tokens by ~2 orders of magnitude — in real traffic cache tokens are ~99% of volume. Additive, idempotent migration (`ADD COLUMN IF NOT EXISTS`); rows rolled up before the migration keep `NULL` (honestly unknown, not `0`). Export `format_version` stays `1` — additive columns don't bump it (ADR-0005). Cost was already correct (`SUM(spans.cost_usd)`). See FLO-555
- Retention worker health on `GET /api/v1/health`: a `retention` object (`status` / `last_run_at` / `last_error`); a failed roll-up flips top-level health to `degraded` and logs at `ERROR` level instead of failing silently

## [0.2.0] - 2026-05-10

### Added
- Multi-user telemetry separation — each Claude Code user's data is tracked independently via `user.id` resource attribute
- Bearer-token authentication: API token creation and management via Tokens page; tokens linked to users in OTLP ingest
- Users page with search, pagination, per-user click-through to filtered analytics, and anonymous user view/delete
- Unified analytics dashboard with per-user filter across all charts and tables
- Setup page with step-by-step onboarding guide and copyable agent configuration prompt
- History page with time-series charts and daily activity heatmaps
- Session Detail: Cache Read and Cache Write KPI cards
- Data export (versioned ZIP/CSV) and import endpoint (`POST /api/v1/import`) with migration support
- Export/import round-trip integration tests
- Public ingest URL configuration (`COTEL_PUBLIC_INGEST_URL`) surfaced on Setup page
- Cloudflare Tunnel integration: locally-managed mode with bundled `cloudflared` binary
- Allow-anonymous setting to accept unauthenticated telemetry
- cotel logo mark as SVG favicon and sidebar brand
- GitHub links in header and Setup page
- VitePress documentation site deployed to GitHub Pages
- Manual release workflow with automated CHANGELOG stamping, Docker image push to GHCR, and GitHub Release creation
- Deploy workflow for self-hosted runner on push to main

### Fixed
- Sessions were always empty with Claude Code beta telemetry: corrected attribute key mismatch for `tool_name` and `cache_creation_tokens`
- Cost-by-user chart now sorted by cost descending instead of insertion order
- Dashboard costs rounded to 2 decimal places
- Export endpoint returned 401 incorrectly; shared auth middleware extracted and applied consistently
- Session navigation: use React Router instead of `window.location` to avoid full page reloads
- Propagate cotel exit code from entrypoint script
- GitHub→Paperclip intake: prevent duplicate issues and ensure backlog landing

### Changed
- Legacy server-side rendered dashboard replaced by full React SPA served on all routes
- Settings page merged into Setup as a second tab

## [0.1.0] - 2024-11-01

### Added
- OTLP HTTP/JSON ingest endpoint compatible with Claude Code telemetry (`/v1/traces`)
- DuckDB-backed storage with session deduplication and span ingestion
- Dashboard with session, model, tool, and cost breakdowns
- `/healthz` endpoint exposing span count for smoke testing
- Single-container deployment: `docker run -p 4318:4318 -p 8080:8080 cotel:latest`
- GitHub Actions CI: build, vet, test, smoke test on every PR and push to main
- GitHub→Paperclip issue sync workflow

[Unreleased]: https://github.com/Flopsstuff/cotel/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/Flopsstuff/cotel/releases/tag/v0.1.0

# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- `--backfill-cost` / `--backfill-cost-apply` CLI flags: one-shot commands that recalculate `cost_usd` for all historical spans from their stored token counts using current pricing rates. `--backfill-cost` is a read-only dry-run (per-model before/after totals, unknown/empty-model spans reported separately and left untouched); `--backfill-cost-apply` writes the corrected span costs. Scope is the `spans` table only - `daily_usage` is left untouched, because roll-up derives its cost from `SUM(spans.cost_usd)` and is therefore correct for any day rolled up after a backfill. Idempotent (recompute from tokens, not scaling). Backfill uses rates current as of the run date. The running server holds an exclusive DuckDB lock, so the dry-run runs against a copy and the apply runs while the server is stopped — see README → Maintenance commands. See FLO-552.

### Fixed
- Pricing table now covers the Claude 5 family (Fable 5, Mythos 5, Opus 5, Sonnet 5) and the missing 4.x models (Opus 4.8, Opus 4.6). Spans from `claude-opus-5` were previously recorded with `cost_usd = 0` because the model was absent from the table
- Corrected two stale rates: Opus 4.7 ($15.00/$75.00 → $5.00/$25.00) and Haiku 4.5 ($0.80/$4.00 → $1.00/$5.00) per MTok

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

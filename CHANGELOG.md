# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- `GET /api/v1/overview`, `/sessions`, `/costs` and `/models` accept `range`, the same five-key rolling window `/users` and `/tools` already took, and each echoes back the key it used. Defaults preserve today's behaviour instead of converging on one value: `/overview` and `/costs` default to `month` (the 30-day window they already applied), `/sessions` and `/models` to `all`, because they had no time filter and a `month` default would silently truncate every existing caller. Long ranges resolve against the `spans` ∪ `daily_usage` union at the raw-floor split, so `year` and `all` keep answering after retention has deleted the raw spans rather than repeating the `month` figure. On `/costs`, explicit `from`/`to` still beat the range key and the response then echoes `"range": null` ([ADR-0014](docs/decisions/0014-overview-single-range-selector.md))
- `GET /api/v1/sessions` returns `covered_since`. A session row needs a start time, model and status, none of which the roll-up keeps, so the list is raw-only; a range reaching past the raw floor is clamped and the field names the instant the list actually starts from (`null` when the range is fully covered). The Overview's Sessions block states that window in one line. The session *count* KPI is unaffected — `daily_usage` carries `session_id`, so counting distinct sessions across the union is exact

### Fixed
- `GET /api/v1/overview`'s `users_count` obeyed neither the time window nor the `user_id` filter — it was a bare `SELECT COUNT(DISTINCT user_id) FROM spans`, so the "Users" KPI answered all-time and unscoped next to four KPIs that did not. It now counts the distinct principals active in the selected range, and counts unattributed spans as the single `__anonymous__` principal the users list shows rather than as zero
- The Overview's `total_cost_usd` and token KPIs no longer drop spans that carry no `session_id`. They were computed under the same `session_id IS NOT NULL` clause as the session count, so the page's cost total could sit below the Costs page's for the same window
- A bare `WHERE col = <constant>` on `spans` returns the matching rows again instead of silently returning none. `duration_ms` was a VIRTUAL generated column declared mid-table: it took a logical slot but no storage slot, so every column after it had a logical index one ahead of its physical index, and an equality on such a column probed an unrelated ART index and found nothing — a wrong result, not an error. Schema version 10 drops the column and computes the duration at the four queries that read it, so logical and physical indexes line up and the trap is gone rather than worked around; the engine-fragile `COALESCE(tool_name, '') = 'Bash'` workaround (DuckDB 1.4+ pushes `COALESCE` down too) goes with it. Verified against a copy of production (108 MB, 34 706 spans): `tool_name = 'Bash'` counted 0 rows before the migration and 6 440 after, `service_name = 'claude-code'` 0 before and 34 705 after, with the span count unchanged ([ADR-0013](docs/decisions/0013-spans-has-no-derived-columns.md))
- A failing `GET /api/v1/bash-commands` no longer renders as the "no command detail in this data" explainer. The Bash section branched on row count alone, so a request that errored looked identical to one that legitimately returned nothing, blaming Claude Code's telemetry for what was actually a server fault. Fetch failures now show the error

### Changed
- The Overview is one window instead of five. A single range switcher in the header — its own `cotel_overview_range` cookie, so it does not move the Users or Tools page — scopes every figure on the page. Previously the KPIs showed 30 days, the Sessions and Models blocks showed all time, and only the KPI labels said which, as a literal `(30d)` baked into the string; a reader comparing the Sessions KPI against the Models table below it was comparing 30 days against all time. Labels now take their suffix from the selected range, and `All` renders none
- Overview section order is Users, History, Costs, Tools, Models, Sessions. A new Users block leads with the top 5 principals by spend in the selected range, and Sessions moves to the bottom as the one block that cannot honour a long range. The Costs block drops its inner by-model table — the Models block below it is the same data at full width
- The Overview's user-search typeahead is gone, and the `UserSearch` component with it. Scoping is reached from a user's page ("View activity"); `?user_id=` now shows a chip in the header naming the user and clearing the scope on click, instead of a page that was silently filtered with nothing on it to say so
- A deploy now fails when the container does not come up. The Deploy workflow ended at `docker compose up -d`, which returns once the container has *started*, not once it works — so the last thing it observed of a deploy was `Up Less than a second (health: starting)` and it went green on that, reporting a container whose `storage.Open` had died identically to one serving traffic. It now runs `scripts/wait-for-healthy.sh`, which blocks on the container's own `HEALTHCHECK` and fails the job on `unhealthy`, on an exit, on a crash loop (in under a second, rather than waiting out the timeout — only restarts seen *during* the wait indict a deploy, since `up -d` leaves an already-current container in place and one that crashed once and recovered carries a restart count for the rest of its life, including while it legitimately replays a WAL), on a service that defines no healthcheck at all, or on a 120 s timeout — dumping `docker compose ps`, the last health-probe output and the container logs so the reason is in the run log instead of on the runner. `workflow_dispatch` takes a `health_timeout` input for the one deploy that legitimately needs longer: a start following a hard kill replays the WAL. The CI smoke job runs the same script in place of its `curl`-until-ready loop, so a break in the gate surfaces on a PR rather than on a deploy. Measured against a 109 MB copy of production, healthy at 6 s from cold with a 3.9 MB WAL to replay (the open itself 2.8 s, including the v10 migration) and 6 s on a redeploy of the warm database — the 6 s is the probe cadence, not the database
- The image `HEALTHCHECK` gains `--start-interval=5s`. `--interval=30s` also governed the probes during `start-period`, so a container that was ready in two seconds still reported `starting` for thirty, and the deploy gate above would have waited out all of it
- Schema version 10 removes `spans.duration_ms`. The migration moves no row data: it drops the four secondary indexes, drops the column (DuckDB refuses to `ALTER` a table an index depends on), and the existing `CREATE INDEX IF NOT EXISTS` block rebuilds them. On the 108 MB production copy the whole upgrade added 0.4 s to a cold start already dominated by WAL replay (9.5 s → 9.8 s), and a later re-apply of `schema.sql` costs 138 ms. No downgrade path: an older binary still starts against a v10 database (`CREATE TABLE IF NOT EXISTS` cannot bring the column back) but every query naming `duration_ms` then errors, so a rollback needs the pre-upgrade database too. Exported CSVs are unaffected — the `duration_ms` column of `spans.csv` was already derived in Go, so the format version does not move
- Tools page is flat: the tools table sits directly on the page instead of inside an "All Tools" card. `DataTable` already draws its own bordered surface, so the card put a box inside a box for a heading the page title already gave. Matches the Users page
- The Bash commands section no longer vanishes when it has no rows — it always renders and says why it is empty. Claude Code's tool spans carry `tool_name`, `tool_use_id` and `duration_ms` only: which tool ran, not what it ran. No `command` attribute is ever sent (verified against a captured live session, including the enhanced-telemetry beta), so this breakdown stays empty against Claude Code telemetry no matter what — the DuckDB filter-pushdown fix in 0.3.0 was a real bug fix but could not, on its own, put rows in this table. The endpoint is kept for OTLP producers that do send `command`

## [0.3.0] - 2026-08-09

### Fixed
- Pricing table now covers the Claude 5 family (Fable 5, Mythos 5, Opus 5, Sonnet 5) and the missing 4.x models (Opus 4.8, Opus 4.6). Spans from `claude-opus-5` were previously recorded with `cost_usd = 0` because the model was absent from the table
- Corrected two stale rates: Opus 4.7 ($15.00/$75.00 → $5.00/$25.00) and Haiku 4.5 ($0.80/$4.00 → $1.00/$5.00) per MTok
- Retention roll-up no longer aborts with `NOT NULL constraint failed: daily_usage.model` when a span has an empty or NULL `model`, `session_id`, or `tool_name`. Such spans are now rolled up under an `unknown` sentinel instead of silently stopping all aggregation and purging ([ADR-0009](docs/decisions/0009-daily-usage-unknown-sentinel.md))
- No more silent telemetry loss on restart/deploy: the ingest (`:4318`) and dashboard (`:8080`) ports now bind and accept connections **before** the DuckDB open (WAL replay + schema migration), which can take minutes on a large production database. During that window both ports answer a retryable `503` with `Retry-After` instead of resetting the connection, so OTLP exporters retry and spans are delivered once cotel is ready. Previously the ports bound only after the open finished, so every deploy had a multi-minute window where ingest refused connections and dropped spans
- Retention roll-up no longer loses part of a day's usage. The cutoff was a wall-clock instant, so with the worker ticking every 6h (`COTEL_RETENTION_INTERVAL`) a day was rolled up in slices; because `daily_usage` is keyed by day and written with `INSERT OR REPLACE`, each slice overwrote the previous one while its raw spans were already purged. Every day's `span_count`, token totals and `total_cost_usd` were therefore systematically understated — only the last slice survived. The cutoff is now snapped back to UTC midnight — the same boundary the `day` key is bucketed on — so only whole days are ever rolled up and purged. Trade-off: raw spans now live up to one day longer than `COTEL_RETENTION_RAW_DAYS` (default 30). Aggregates already flattened by the old behaviour cannot be recovered — the raw spans are gone
- Retention is now correct on servers that do not run in UTC. A span's `day` is its UTC calendar day, but both retention cutoffs were computed in the server's local zone, so on any host at a non-zero UTC offset — including UTC+1/+2 — the boundary fell inside a day bucket and re-introduced the overwrite above for spans near midnight UTC. Both cutoffs are now computed in UTC, and the retention tests run under a matrix of server timezones so the alignment cannot silently regress
- Retention roll-up no longer overwrites an already-rolled-up day's aggregate when a span dated to that day arrives late — a backfill, or an import of telemetry older than `COTEL_RETENTION_RAW_DAYS`. The day had already been aggregated and its raw spans purged, so the next cycle recomputed the day from the single late span alone and `INSERT OR REPLACE`d the correct total away, silently corrupting historical cost. Late spans now **accumulate** into the existing `daily_usage` row (`ON CONFLICT DO UPDATE`) instead of replacing it; the accumulate and the raw-span purge run in one transaction so a crash between them cannot double-count
- Fast restarts: cotel now runs a DuckDB `CHECKPOINT` on `SIGTERM`/`SIGINT` before exiting, folding the write-ahead log into the main file, and the container entrypoint forwards the stop signal to cotel and waits for it. That multi-minute cold start on a large DB is **WAL replay**, not the schema migration — previously every deploy killed cotel with a non-empty WAL (the old entrypoint never forwarded the signal and cotel had no handler), so the next start replayed the whole log. Measured on a copy of production (108 MB DB, 6.2 MB WAL): a cold start took **5m38s**; after a graceful stop the next start is **0.14s**. A hard kill (`docker kill`, OOM, `SIGKILL` after the stop grace period) skips the checkpoint and pays the replay again, but never corrupts the database and loses no committed span — DuckDB replays the WAL on the next open
- `GET /api/v1/bash-commands` returned an empty list on every request from the day it shipped, so the Tools page's per-command Bash breakdown always looked like "no Bash calls yet". Under the bundled DuckDB engine a bare `tool_name = 'Bash'` predicate is pushed into the table scan and then matches nothing; the query now uses a form the optimizer leaves alone. Only `tool_name` and `service_name` are affected — filters on session, model, user and the id columns were always correct

### Added
- `COTEL_WAL_AUTOCHECKPOINT` (default `4MB`, DuckDB's own default is 16MB) caps how large the write-ahead log grows before DuckDB folds it into the main file. A graceful stop avoids WAL replay entirely; this cap only bounds the replay an *ungraceful* kill (OOM, `SIGKILL` after the grace period) leaves behind, by folding the log — including a retention roll-up's ~6MB of deletes — into the main file promptly. The cost is slightly more frequent checkpoints during ingest, negligible at cotel's write rate
- Startup logging of each phase — `opening db`, `db ready: schema/migrations applied in <duration>`, and `ready: serving live traffic …` — so a slow open is visible instead of a silent container. Plus a Docker `HEALTHCHECK` (`cotel -healthcheck`, which probes the dashboard `/healthz`) so the container reports `health: starting` until the database is open rather than a misleading `Up`; no curl/wget is added to the runtime image
- `daily_usage` now preserves cache-token totals through roll-up: new `total_cache_read_tokens` and `total_cache_write_tokens` columns (schema v8) are filled by `RollupAndPurge` and carried through `/api/v1/export` and import. Previously only input/output tokens survived the roll-up, so aggregates older than the raw-span retention window (default 30 days) undercounted total tokens by ~2 orders of magnitude — in real traffic cache tokens are ~99% of volume. Additive, idempotent migration (`ADD COLUMN IF NOT EXISTS`); rows rolled up before the migration keep `NULL` (honestly unknown, not `0`). Export `format_version` stays `1` — additive columns don't bump it (ADR-0005). Cost was already correct (`SUM(spans.cost_usd)`)
- Retention worker health on `GET /api/v1/health`: a `retention` object (`status` / `last_run_at` / `last_error`); a failed roll-up flips top-level health to `degraded` and logs at `ERROR` level instead of failing silently
- Schema migrations are version-guarded on startup: `storage.Open` skips the whole of `schema.sql` when the database already records the current schema version, so migrations run once (on the deploy that introduces them) rather than on every boot. Measured against a copy of production the apply is ~165 ms, so this is correctness hygiene — the multi-minute open is DuckDB WAL replay, which the guard cannot affect ([ADR-0010](docs/decisions/0010-schema-version-guard.md))
- Users page is now one sortable, searchable, paged list carrying per-user **cost** and **session count**, scoped by a range switcher (All / Year / Month / Week / Day, default month) that persists in a cookie. Sorting and paging happen in SQL, so "top spender" means top of the whole table for the chosen window rather than top of whatever the browser had loaded. Ranged figures union raw spans with the `daily_usage` roll-up, so a window older than the raw-span retention still answers; `created_at` and `last_seen` stay all-time ([ADR-0011](docs/decisions/0011-users-list-ranged-stats-and-server-side-sort.md))
- Per-user page at `/users/:id` (new `GET /api/v1/users/{id}`), reached by clicking any row. The token, rotate and delete actions that used to crowd every list row live there
- Tools page gains the same contract: ranged call counts, average duration and error rate per tool, server-side sort, search and paging, with the range switcher in the page toolbar. Tool metrics union raw spans with the roll-up; rows aggregated before schema v9 count toward calls but stay out of the duration and failure denominators, and the response names the instant those two figures actually start from. `/api/v1/bash-commands` is raw-only by design and reports the window it covers ([ADR-0012](docs/decisions/0012-tools-list-ranged-stats-and-server-side-sort.md))
- `GET /api/v1/users` and `GET /api/v1/tools` accept `range`, `q`, `sort`, `order`, `page` and `limit` and echo the resolved values back. Sort keys are whitelisted, `total` counts matches before paging, and `limit=0` returns the unpaginated list the typeahead needs
- `daily_usage` preserves average duration and failure counts through roll-up (schema v9): new nullable `total_duration_ms` and `fail_count` columns, accumulated the same additive, idempotent way as the v8 cache-token migration, so pre-v9 rows stay honestly unknown rather than counting as zero
- Sessions and the Overview sessions block show a **User** column; the Sessions search box filters by user instead of by opaque session ID, and clicking a user filters the table to their sessions

### Changed
- The Users page is flat — title, search, range switcher and **Add user** on one toolbar row above the table, with no card wrapper around a page that holds a single thing

### Removed
- The "Cost by user (top 10)" chart on Users and the top-10 chart on Tools. Both ranked a fixed slice on one hardcoded metric; the ranged, sortable lists that replace them answer the same question for any column and any window
- Token display, copy-token and delete from the Users list rows — those actions moved to the per-user page

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

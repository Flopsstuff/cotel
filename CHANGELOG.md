# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- OTLP HTTP/JSON ingest endpoint compatible with Claude Code telemetry (`/v1/traces`)
- DuckDB-backed storage with session deduplication and span ingestion
- Dashboard with session, model, tool, and cost breakdowns
- `/healthz` endpoint exposing span count for smoke testing
- Single-container deployment: `docker run -p 4318:4318 -p 8080:8080 cotel:latest`
- GitHub Actions CI: build, vet, test, smoke test on every PR and push to main
- GitHub→Paperclip issue sync workflow

[Unreleased]: https://github.com/Flopsstuff/cotel/compare/HEAD...HEAD

# ADR-0005: Export/Import Format and Protocol

**Status:** Accepted  
**Date:** 2026-05-10  
**Issue:** [FLO-71](/FLO/issues/FLO-71)

---

## Context

Users need to move telemetry data between cotel installations (e.g., migrating a self-hosted instance) and to extract data for external analysis (spreadsheets, pandas, BI tools). The feature must:

1. Export data in bounded chunks (day/week/month) so downloads stay manageable.
2. Survive schema evolution — a file exported from cotel v0.1 must import cleanly into v0.3 even if new columns were added or a column was renamed.
3. Be readable by standard analysis tools without special software.
4. Stay inside the "one container, one volume" deployment contract — no separate services.

---

## Options Considered

### A — Raw DuckDB file copy
Export the `.duckdb` file directly.
- **Pro:** Lossless, no schema translation needed.
- **Con:** Not portable (DuckDB version mismatch breaks reads), no chunk granularity, not readable without DuckDB, exposes full database including tokens table.

### B — Plain CSV per table
One `.csv` file per table downloaded separately.
- **Pro:** Maximum compatibility with Excel/pandas.
- **Con:** No versioning metadata, no manifest tying files together, no way to signal column additions/renames, awkward multi-file download UX.

### C — NDJSON stream
One newline-delimited JSON file per table.
- **Pro:** Self-describing per row, streams cleanly over HTTP.
- **Con:** More verbose than CSV (2–5× larger), analysis tools prefer CSV/tabular, pandas `.read_json(lines=True)` is less common in analyst workflows than `.read_csv()`.

### D — ZIP archive with versioned manifest + CSV files (chosen)
A single `.zip` file containing:
- `manifest.json` — version, cotel version, period, table metadata
- `spans.csv` — raw span data for the chunk (present when raw data exists)
- `daily_usage.csv` — aggregated data for the chunk

---

## Decision: Option D — Versioned ZIP/CSV/Manifest

**Rationale:**

- **Single download** — one file, one click. ZIP is universally understood.
- **CSV inside** — Excel, pandas, R all open it without extra libraries.
- **Manifest carries the version** — the importer reads `format_version` before touching CSVs, applies any needed migrations, and fails early with a clear error if it encounters a future version it doesn't know.
- **Forward/backward compatible** — unknown CSV columns are dropped on import; missing columns get typed zero-values or NULL. Column presence is declared in `manifest.tables[name].columns` so the importer can detect gaps.
- **Two-table design mirrors cotel's data lifecycle** — raw `spans` exist for the recent retention window; older periods only have `daily_usage`. The manifest's `tables` object lists only the tables actually present in a given export.
- **Boring tech beats clever tech** — `archive/zip`, `encoding/csv` from Go stdlib, no external dependencies.

---

## Manifest Format (v1)

```json
{
  "format_version": 1,
  "cotel_version": "0.1.0",
  "export_at": "2026-05-10T10:00:00Z",
  "period": "day",
  "period_start": "2026-05-09T00:00:00Z",
  "period_end": "2026-05-10T00:00:00Z",
  "tables": {
    "spans": {
      "row_count": 1234,
      "columns": ["span_id","trace_id","parent_span_id","service_name","name",
                  "start_time","end_time","duration_ms","session_id","model",
                  "tool_name","user_id","status_code","input_tokens","output_tokens",
                  "cache_read_tokens","cache_write_tokens","cost_usd",
                  "attributes","resource_attrs","ingested_at"]
    },
    "daily_usage": {
      "row_count": 42,
      "columns": ["day","session_id","model","tool_name","user_id",
                  "span_count","total_input_tokens","total_output_tokens",
                  "total_cache_read_tokens","total_cache_write_tokens","total_cost_usd"]
    }
  }
}
```

`format_version` bumps **only** on breaking changes (column rename, column removal, type change). Adding optional columns does **not** bump the version.

> **`daily_usage` column additions (FLO-555).** `total_cache_read_tokens` and
> `total_cache_write_tokens` were appended to `daily_usage` after v1 shipped. This
> is an additive change, so `format_version` stays `1`: the importer reads columns
> by header name, so an old archive without them imports as NULL and a new archive
> imports cleanly into an old cotel (the unknown columns are dropped). A row rolled
> up before the migration carries an empty cell (→ SQL NULL), which is honest —
> the cache totals for that day are genuinely unknown, not zero.

---

## Chunk Granularity

| Period | Parameter | Covers |
|--------|-----------|--------|
| `day`  | `date=YYYY-MM-DD` | 00:00–24:00 UTC on that date |
| `week` | `date=YYYY-MM-DD` (any day in the week) | Mon 00:00 – Sun 24:00 UTC (ISO week) |
| `month`| `date=YYYY-MM-DD` (any day in the month) | 1st 00:00 – last day 24:00 UTC |

---

## API

### Export
```
GET /api/v1/export?period=day|week|month&date=YYYY-MM-DD
Authorization: Bearer <api-token>

→ 200 application/zip
   Content-Disposition: attachment; filename="cotel-export-day-2026-05-09.zip"
→ 400 invalid period or date
→ 404 no data for the requested period
```

### Import
```
POST /api/v1/import
Authorization: Bearer <api-token>
Content-Type: multipart/form-data; field "file"

→ 200 { "spans_imported": 1234, "daily_usage_imported": 42, "format_version": 1, "warnings": [] }
→ 400 bad ZIP, bad manifest, unsupported format_version
→ 413 file too large (limit: 100 MB uncompressed)
```

---

## Migration Strategy

The importer carries a migrations table keyed by `(from_version, to_version)`. Each migration is a column-level transform (add with default, rename). Migrations are applied in sequence from the exported version up to the current supported version.

If `format_version` > current maximum supported version, the import is rejected with a clear error message instructing the user to upgrade cotel first.

---

## Security

- Both endpoints require a valid API token (same auth as ingest).
- Import: max 100 MB uncompressed size enforced during ZIP extraction.
- Import: maximum 5 entries in the ZIP (manifest + 2 CSVs + headroom). Reject oversized archives early.
- Export: never includes the `api_tokens` table.
- Imported `span_id` values that already exist are skipped (upsert-or-skip semantics), so re-importing the same file is idempotent.

---

## Consequences

- Export is read-only and stateless; it can run in parallel with ingest.
- Import blocks the write connection briefly for bulk inserts (DuckDB single-writer model); large imports may briefly delay ingest ACKs. Acceptable given the "one container" constraint.
- The retention worker is unaffected — imported spans are indistinguishable from live spans and will be rolled up on the normal schedule.
- Dashboard shows imported historical data immediately after import commits.
- Future schema changes follow the documented migration protocol; no silent renames.

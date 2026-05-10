# Export / Import

cotel can export its full dataset as a ZIP archive and reimport it into any cotel instance. Use this for:

- **Migration** — move data to a new host (backup on the old host, restore on the new one).
- **Debugging** — copy a production dataset into a dev container to reproduce an issue locally.
- **Offline archive** — snapshot raw spans before the retention window erases them.

---

## Dashboard walkthrough

1. Open the cotel dashboard and click **Setup** in the top navigation.
2. Select the **Export / Import** tab.
3. **To export:** click the **Export** button. The browser downloads a file named  
   `cotel-export-{period}-{date}.zip` (e.g. `cotel-export-month-2026-05-01.zip`).
4. **To import:** drag a previously downloaded ZIP onto the **Import** drop zone, or click it to browse. cotel validates the archive, inserts any missing rows, and shows a summary of how many spans and daily-usage rows were added.

---

## API reference

Both endpoints require a valid `Authorization: Bearer cotel_<token>` header when anonymous mode is disabled (the default when at least one user token has been created). If anonymous mode is enabled (`allow_anonymous=true`) and no token is set on the request, the endpoints are still accessible.

### Export — `GET /api/v1/export`

Downloads a ZIP archive for the requested time range.

**Query parameters**

| Parameter | Required | Values | Description |
|---|---|---|---|
| `period` | yes | `day`, `week`, `month` | Granularity of the export window |
| `date` | yes | `YYYY-MM-DD` | Any date within the desired period |

The exported window covers the full calendar period (UTC) that contains `date`. For example, `period=month&date=2026-05-10` exports all data for May 2026.

**Response**

`200 OK` — `Content-Type: application/zip`, body is the archive file.  
`400 Bad Request` — invalid `period` value or unparseable `date`.  
`404 Not Found` — no data exists for the requested period.

**curl example**

```bash
# Export all data for the current month
curl -s \
  -H "Authorization: Bearer cotel_<token>" \
  "http://localhost:8080/api/v1/export?period=month&date=$(date +%Y-%m-%d)" \
  -o cotel-backup.zip
```

---

### Import — `POST /api/v1/import`

Uploads a ZIP archive and inserts any rows not already present in the database.

**Request**

`Content-Type: multipart/form-data`, field name `file`.  
Maximum upload size: **101 MB** (compressed). Uncompressed content must be ≤ 100 MB.

**Response**

`200 OK` — JSON body:

```json
{
  "spans_imported": 1234,
  "daily_usage_imported": 42,
  "format_version": 1,
  "warnings": []
}
```

`400 Bad Request` — invalid or corrupt ZIP, missing `manifest.json`, unsupported `format_version`.  
`413 Request Entity Too Large` — archive exceeds the 101 MB upload limit.

**curl example**

```bash
curl -s -X POST \
  -H "Authorization: Bearer cotel_<token>" \
  -F "file=@cotel-backup.zip" \
  http://localhost:8080/api/v1/import
```

---

## Archive format

A cotel export ZIP always contains the following entries:

| File | Always present | Description |
|---|---|---|
| `manifest.json` | Yes | Archive metadata (version, period, row counts) |
| `spans.csv` | When raw span data exists | One row per OTLP span |
| `daily_usage.csv` | When aggregated data exists | One row per (day, session, model, user) bucket |

**Size limits:** ≤ 100 MB uncompressed total; ≤ 5 ZIP entries.

### manifest.json

```json
{
  "format_version": 1,
  "cotel_version": "0.1.0",
  "export_at": "2026-05-10T10:00:00Z",
  "period": "month",
  "period_start": "2026-05-01T00:00:00Z",
  "period_end": "2026-06-01T00:00:00Z",
  "tables": {
    "spans": {
      "row_count": 1234,
      "columns": ["span_id", "trace_id", "..."]
    },
    "daily_usage": {
      "row_count": 42,
      "columns": ["day", "session_id", "..."]
    }
  }
}
```

`format_version` is an integer that cotel increments only on breaking schema changes. The current version is `1`.

### spans.csv columns

`span_id`, `trace_id`, `parent_span_id`, `service_name`, `name`, `start_time`, `end_time`, `duration_ms`, `session_id`, `model`, `tool_name`, `user_id`, `status_code`, `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_write_tokens`, `cost_usd`, `attributes`, `resource_attrs`, `ingested_at`

Times are in RFC 3339 Nano format. NULL values are stored as empty strings.

### daily_usage.csv columns

`day`, `session_id`, `model`, `tool_name`, `user_id`, `span_count`, `total_input_tokens`, `total_output_tokens`, `total_cost_usd`

`day` is in `YYYY-MM-DD` format (UTC).

---

## Idempotency

Import uses **insert-or-skip** semantics. Re-importing the same archive inserts 0 rows; it cannot produce duplicates. The response will show `"spans_imported": 0` and `"daily_usage_imported": 0` when the archive has already been applied.

## Limitations

- Import **adds** missing rows only. It does not delete or update existing rows.
- There is no merge strategy for partial overlaps — data already in the database is left unchanged.
- Archives from a future `format_version` are rejected with `400`.
- The export window is fixed to whole calendar periods (day / week / month). Arbitrary date ranges are not supported via the API; use multiple exports and import each one separately.

---

## References

- [ADR-0005 — Export/Import Format](../decisions/0005-export-import-format.md) — full architectural rationale, format specification, and migration strategy
- [README — Export and import](../../README.md#export-and-import) — quick-reference section

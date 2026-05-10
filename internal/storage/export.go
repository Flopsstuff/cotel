package storage

import (
	"database/sql"
	"time"
)

// DailyUsageRow mirrors one row of the daily_usage table.
type DailyUsageRow struct {
	Day               time.Time
	SessionID         string
	Model             string
	ToolName          string
	UserID            string
	SpanCount         int64
	TotalInputTokens  int64
	TotalOutputTokens int64
	TotalCostUSD      float64
}

// ExportSpans returns all spans with start_time in [from, to).
func (db *DB) ExportSpans(from, to time.Time) ([]Span, error) {
	rows, err := db.rw.Query(`
		SELECT trace_id, span_id, parent_span_id, name,
		       start_time, end_time, service_name,
		       session_id, model, tool_name, user_id, status_code,
		       input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd,
		       attributes, resource_attrs, ingested_at
		FROM spans
		WHERE start_time >= ? AND start_time < ?
		ORDER BY start_time`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var spans []Span
	for rows.Next() {
		var s Span
		var parentSpanID, serviceName, sessionID, model, toolName, userID sql.NullString
		var attrs, resourceAttrs sql.NullString
		if err := rows.Scan(
			&s.TraceID, &s.SpanID, &parentSpanID, &s.Name,
			&s.StartTime, &s.EndTime, &serviceName,
			&sessionID, &model, &toolName, &userID, &s.StatusCode,
			&s.InputTokens, &s.OutputTokens, &s.CacheReadTokens, &s.CacheWriteTokens, &s.CostUSD,
			&attrs, &resourceAttrs, &s.IngestedAt,
		); err != nil {
			return nil, err
		}
		s.ParentSpanID = parentSpanID.String
		s.ServiceName = serviceName.String
		s.SessionID = sessionID.String
		s.Model = model.String
		s.ToolName = toolName.String
		s.UserID = userID.String
		s.Attributes = attrs.String
		s.ResourceAttrs = resourceAttrs.String
		spans = append(spans, s)
	}
	return spans, rows.Err()
}

// ExportDailyUsage returns all daily_usage rows with day in [from, to).
func (db *DB) ExportDailyUsage(from, to time.Time) ([]DailyUsageRow, error) {
	rows, err := db.rw.Query(`
		SELECT day, session_id, model, tool_name, user_id,
		       span_count, total_input_tokens, total_output_tokens, total_cost_usd
		FROM daily_usage
		WHERE day >= CAST(? AS DATE) AND day < CAST(? AS DATE)
		ORDER BY day`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DailyUsageRow
	for rows.Next() {
		var r DailyUsageRow
		var sessionID, model, toolName, userID sql.NullString
		if err := rows.Scan(
			&r.Day, &sessionID, &model, &toolName, &userID,
			&r.SpanCount, &r.TotalInputTokens, &r.TotalOutputTokens, &r.TotalCostUSD,
		); err != nil {
			return nil, err
		}
		r.SessionID = sessionID.String
		r.Model = model.String
		r.ToolName = toolName.String
		r.UserID = userID.String
		out = append(out, r)
	}
	return out, rows.Err()
}

const importSpanSQL = `
INSERT OR IGNORE INTO spans (
  trace_id, span_id, parent_span_id, name,
  start_time, end_time, service_name,
  session_id, model, tool_name, user_id, status_code,
  input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd,
  attributes, resource_attrs, ingested_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

// ImportSpans bulk-inserts spans, skipping any whose span_id already exists.
// Returns the number of rows actually inserted.
func (db *DB) ImportSpans(spans []Span) (int, error) {
	if len(spans) == 0 {
		return 0, nil
	}
	tx, err := db.rw.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.Prepare(importSpanSQL)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	var inserted int
	for _, s := range spans {
		if s.Attributes == "" {
			s.Attributes = "{}"
		}
		if s.ResourceAttrs == "" {
			s.ResourceAttrs = "{}"
		}
		ingestedAt := s.IngestedAt
		if ingestedAt.IsZero() {
			ingestedAt = time.Now()
		}
		res, err := stmt.Exec(
			s.TraceID, s.SpanID, nullableStr(s.ParentSpanID), s.Name,
			s.StartTime, s.EndTime, nullableStr(s.ServiceName),
			nullableStr(s.SessionID), nullableStr(s.Model), nullableStr(s.ToolName),
			nullableStr(s.UserID), s.StatusCode,
			s.InputTokens, s.OutputTokens, s.CacheReadTokens, s.CacheWriteTokens, s.CostUSD,
			s.Attributes, s.ResourceAttrs, ingestedAt,
		)
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		inserted += int(n)
	}
	return inserted, tx.Commit()
}

const importDailyUsageSQL = `
INSERT OR IGNORE INTO daily_usage (
  day, session_id, model, tool_name, user_id,
  span_count, total_input_tokens, total_output_tokens, total_cost_usd
) VALUES (?,?,?,?,?,?,?,?,?)`

// ImportDailyUsage bulk-inserts daily_usage rows, skipping any whose PK
// (day, session_id, model, tool_name) already exists.
// Returns the number of rows actually inserted.
func (db *DB) ImportDailyUsage(rows []DailyUsageRow) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	tx, err := db.rw.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.Prepare(importDailyUsageSQL)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	var inserted int
	for _, r := range rows {
		res, err := stmt.Exec(
			r.Day, nullableStr(r.SessionID), nullableStr(r.Model), nullableStr(r.ToolName),
			nullableStr(r.UserID),
			r.SpanCount, r.TotalInputTokens, r.TotalOutputTokens, r.TotalCostUSD,
		)
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		inserted += int(n)
	}
	return inserted, tx.Commit()
}

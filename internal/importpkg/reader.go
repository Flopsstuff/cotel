// Package importpkg reads cotel's versioned ZIP/CSV export format (ADR-0005).
package importpkg

import (
	"archive/zip"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/Flopsstuff/cotel/internal/export"
	"github.com/Flopsstuff/cotel/internal/storage"
)

const (
	maxUncompressedBytes = 100 * 1024 * 1024 // 100 MB
	maxEntries           = 5
	currentMaxVersion    = export.FormatVersion // 1
)

// ErrTooLarge is returned when the ZIP content exceeds the 100 MB uncompressed limit.
var ErrTooLarge = errors.New("import archive exceeds 100 MB uncompressed limit")

// ReadZIP reads a cotel export ZIP, validates it, applies migrations, and returns
// the parsed manifest, spans, and daily_usage rows ready for import.
func ReadZIP(r io.ReaderAt, size int64) (export.Manifest, []storage.Span, []storage.DailyUsageRow, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return export.Manifest{}, nil, nil, fmt.Errorf("open zip: %w", err)
	}

	if len(zr.File) > maxEntries {
		return export.Manifest{}, nil, nil, fmt.Errorf("zip has %d entries, max %d", len(zr.File), maxEntries)
	}

	var total uint64
	for _, f := range zr.File {
		total += f.UncompressedSize64
		if total > maxUncompressedBytes {
			return export.Manifest{}, nil, nil, fmt.Errorf("%w", ErrTooLarge)
		}
	}

	var manifest export.Manifest
	var foundManifest bool
	for _, f := range zr.File {
		if f.Name != "manifest.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return export.Manifest{}, nil, nil, fmt.Errorf("open manifest.json: %w", err)
		}
		decodeErr := json.NewDecoder(rc).Decode(&manifest)
		rc.Close()
		if decodeErr != nil {
			return export.Manifest{}, nil, nil, fmt.Errorf("decode manifest: %w", decodeErr)
		}
		foundManifest = true
		break
	}
	if !foundManifest {
		return export.Manifest{}, nil, nil, fmt.Errorf("missing manifest.json")
	}

	if manifest.FormatVersion < 1 {
		return export.Manifest{}, nil, nil, fmt.Errorf("invalid format_version %d", manifest.FormatVersion)
	}
	if manifest.FormatVersion > currentMaxVersion {
		return export.Manifest{}, nil, nil, fmt.Errorf(
			"format_version %d not supported by this version of cotel (max %d); please upgrade",
			manifest.FormatVersion, currentMaxVersion,
		)
	}

	var spans []storage.Span
	var daily []storage.DailyUsageRow

	for _, f := range zr.File {
		switch f.Name {
		case "spans.csv":
			rc, err := f.Open()
			if err != nil {
				return export.Manifest{}, nil, nil, fmt.Errorf("open spans.csv: %w", err)
			}
			spans, err = parseSpansCSV(rc)
			rc.Close()
			if err != nil {
				return export.Manifest{}, nil, nil, fmt.Errorf("parse spans.csv: %w", err)
			}
		case "daily_usage.csv":
			rc, err := f.Open()
			if err != nil {
				return export.Manifest{}, nil, nil, fmt.Errorf("open daily_usage.csv: %w", err)
			}
			daily, err = parseDailyUsageCSV(rc)
			rc.Close()
			if err != nil {
				return export.Manifest{}, nil, nil, fmt.Errorf("parse daily_usage.csv: %w", err)
			}
		}
	}

	spans, daily = Migrate(manifest.FormatVersion, spans, daily)
	return manifest, spans, daily, nil
}

func parseSpansCSV(r io.Reader) ([]storage.Span, error) {
	cr := csv.NewReader(r)
	headers, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}

	idx := make(map[string]int, len(headers))
	for i, h := range headers {
		idx[h] = i
	}

	var spans []storage.Span
	for {
		row, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}

		var s storage.Span
		s.SpanID = col(row, idx, "span_id")
		s.TraceID = col(row, idx, "trace_id")
		s.ParentSpanID = col(row, idx, "parent_span_id")
		s.ServiceName = col(row, idx, "service_name")
		s.Name = col(row, idx, "name")

		if v := col(row, idx, "start_time"); v != "" {
			s.StartTime, _ = time.Parse(time.RFC3339Nano, v)
		}
		if v := col(row, idx, "end_time"); v != "" {
			s.EndTime, _ = time.Parse(time.RFC3339Nano, v)
		}
		s.SessionID = col(row, idx, "session_id")
		s.Model = col(row, idx, "model")
		s.ToolName = col(row, idx, "tool_name")
		s.UserID = col(row, idx, "user_id")

		if v := col(row, idx, "status_code"); v != "" {
			n, _ := strconv.ParseInt(v, 10, 32)
			s.StatusCode = int32(n)
		}

		s.InputTokens = nullInt64(col(row, idx, "input_tokens"))
		s.OutputTokens = nullInt64(col(row, idx, "output_tokens"))
		s.CacheReadTokens = nullInt64(col(row, idx, "cache_read_tokens"))
		s.CacheWriteTokens = nullInt64(col(row, idx, "cache_write_tokens"))
		s.CostUSD = nullFloat64(col(row, idx, "cost_usd"))
		s.Attributes = col(row, idx, "attributes")
		s.ResourceAttrs = col(row, idx, "resource_attrs")

		if v := col(row, idx, "ingested_at"); v != "" {
			s.IngestedAt, _ = time.Parse(time.RFC3339Nano, v)
		}

		spans = append(spans, s)
	}
	return spans, nil
}

func parseDailyUsageCSV(r io.Reader) ([]storage.DailyUsageRow, error) {
	cr := csv.NewReader(r)
	headers, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}

	idx := make(map[string]int, len(headers))
	for i, h := range headers {
		idx[h] = i
	}

	var rows []storage.DailyUsageRow
	for {
		row, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}

		var du storage.DailyUsageRow
		if v := col(row, idx, "day"); v != "" {
			du.Day, _ = time.Parse("2006-01-02", v)
		}
		du.SessionID = col(row, idx, "session_id")
		du.Model = col(row, idx, "model")
		du.ToolName = col(row, idx, "tool_name")
		du.UserID = col(row, idx, "user_id")

		if v := col(row, idx, "span_count"); v != "" {
			du.SpanCount, _ = strconv.ParseInt(v, 10, 64)
		}
		if v := col(row, idx, "total_input_tokens"); v != "" {
			du.TotalInputTokens, _ = strconv.ParseInt(v, 10, 64)
		}
		if v := col(row, idx, "total_output_tokens"); v != "" {
			du.TotalOutputTokens, _ = strconv.ParseInt(v, 10, 64)
		}
		if v := col(row, idx, "total_cost_usd"); v != "" {
			du.TotalCostUSD, _ = strconv.ParseFloat(v, 64)
		}

		rows = append(rows, du)
	}
	return rows, nil
}

// col returns the value at the named column, or "" if the column is absent or missing from the row.
// Unknown columns are silently dropped; missing known columns default to "".
func col(row []string, idx map[string]int, name string) string {
	i, ok := idx[name]
	if !ok || i >= len(row) {
		return ""
	}
	return row[i]
}

func nullInt64(s string) *int64 {
	if s == "" {
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil
	}
	return &n
}

func nullFloat64(s string) *float64 {
	if s == "" {
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &f
}

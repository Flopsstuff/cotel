// Package export writes cotel's versioned ZIP/CSV export format (ADR-0005).
package export

import (
	"archive/zip"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/Flopsstuff/cotel/internal/storage"
)

const (
	FormatVersion = 1
	CotelVersion  = "0.1.0"
)

// TableMeta describes a single table in the manifest's tables map.
type TableMeta struct {
	RowCount int      `json:"row_count"`
	Columns  []string `json:"columns"`
}

// Manifest is the manifest.json written at the root of every export ZIP.
type Manifest struct {
	FormatVersion int                  `json:"format_version"`
	CotelVersion  string               `json:"cotel_version"`
	ExportAt      time.Time            `json:"export_at"`
	Period        string               `json:"period"`
	PeriodStart   time.Time            `json:"period_start"`
	PeriodEnd     time.Time            `json:"period_end"`
	Tables        map[string]TableMeta `json:"tables"`
}

// Column lists match ADR-0005 manifest v1 exactly.
var SpansColumns = []string{
	"span_id", "trace_id", "parent_span_id", "service_name", "name",
	"start_time", "end_time", "duration_ms", "session_id", "model",
	"tool_name", "user_id", "status_code", "input_tokens", "output_tokens",
	"cache_read_tokens", "cache_write_tokens", "cost_usd",
	"attributes", "resource_attrs", "ingested_at",
}

var DailyUsageColumns = []string{
	"day", "session_id", "model", "tool_name", "user_id",
	"span_count", "total_input_tokens", "total_output_tokens", "total_cost_usd",
}

// DayRange returns the [start, end) UTC bounds for the calendar day containing date.
func DayRange(date time.Time) (start, end time.Time) {
	d := date.UTC()
	start = time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
	end = start.Add(24 * time.Hour)
	return
}

// WeekRange returns the [start, end) UTC bounds for the ISO week (Mon–Sun) containing date.
func WeekRange(date time.Time) (start, end time.Time) {
	d := date.UTC()
	wd := d.Weekday()
	if wd == time.Sunday {
		wd = 7 // ISO: Sunday is day 7, not 0
	}
	start = time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -(int(wd)-1))
	end = start.AddDate(0, 0, 7)
	return
}

// MonthRange returns the [start, end) UTC bounds for the calendar month containing date.
func MonthRange(date time.Time) (start, end time.Time) {
	d := date.UTC()
	start = time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, time.UTC)
	end = start.AddDate(0, 1, 0)
	return
}

// BuildZIP writes a versioned cotel export ZIP to w.
// It includes manifest.json, spans.csv (when spans is non-empty), and
// daily_usage.csv (when daily is non-empty).
func BuildZIP(w io.Writer, m Manifest, spans []storage.Span, daily []storage.DailyUsageRow) (retErr error) {
	zw := zip.NewWriter(w)
	defer func() {
		if err := zw.Close(); err != nil && retErr == nil {
			retErr = err
		}
	}()

	mf, err := zw.Create("manifest.json")
	if err != nil {
		return fmt.Errorf("create manifest.json: %w", err)
	}
	enc := json.NewEncoder(mf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}

	if len(spans) > 0 {
		sf, err := zw.Create("spans.csv")
		if err != nil {
			return fmt.Errorf("create spans.csv: %w", err)
		}
		if err := writeSpansCSV(sf, spans); err != nil {
			return fmt.Errorf("write spans.csv: %w", err)
		}
	}

	if len(daily) > 0 {
		df, err := zw.Create("daily_usage.csv")
		if err != nil {
			return fmt.Errorf("create daily_usage.csv: %w", err)
		}
		if err := writeDailyUsageCSV(df, daily); err != nil {
			return fmt.Errorf("write daily_usage.csv: %w", err)
		}
	}

	return nil
}

func writeSpansCSV(w io.Writer, spans []storage.Span) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(SpansColumns); err != nil {
		return err
	}
	row := make([]string, len(SpansColumns))
	for _, s := range spans {
		durMS := s.EndTime.Sub(s.StartTime).Milliseconds()
		row[0] = s.SpanID
		row[1] = s.TraceID
		row[2] = s.ParentSpanID
		row[3] = s.ServiceName
		row[4] = s.Name
		row[5] = s.StartTime.UTC().Format(time.RFC3339Nano)
		row[6] = s.EndTime.UTC().Format(time.RFC3339Nano)
		row[7] = strconv.FormatInt(durMS, 10)
		row[8] = s.SessionID
		row[9] = s.Model
		row[10] = s.ToolName
		row[11] = s.UserID
		row[12] = strconv.FormatInt(int64(s.StatusCode), 10)
		row[13] = nullInt64Str(s.InputTokens)
		row[14] = nullInt64Str(s.OutputTokens)
		row[15] = nullInt64Str(s.CacheReadTokens)
		row[16] = nullInt64Str(s.CacheWriteTokens)
		row[17] = nullFloat64Str(s.CostUSD)
		row[18] = s.Attributes
		row[19] = s.ResourceAttrs
		row[20] = s.IngestedAt.UTC().Format(time.RFC3339Nano)
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func writeDailyUsageCSV(w io.Writer, rows []storage.DailyUsageRow) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(DailyUsageColumns); err != nil {
		return err
	}
	row := make([]string, len(DailyUsageColumns))
	for _, r := range rows {
		row[0] = r.Day.UTC().Format("2006-01-02")
		row[1] = r.SessionID
		row[2] = r.Model
		row[3] = r.ToolName
		row[4] = r.UserID
		row[5] = strconv.FormatInt(r.SpanCount, 10)
		row[6] = strconv.FormatInt(r.TotalInputTokens, 10)
		row[7] = strconv.FormatInt(r.TotalOutputTokens, 10)
		row[8] = strconv.FormatFloat(r.TotalCostUSD, 'f', -1, 64)
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func nullInt64Str(p *int64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatInt(*p, 10)
}

func nullFloat64Str(p *float64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatFloat(*p, 'f', -1, 64)
}

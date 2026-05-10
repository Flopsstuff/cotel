package export_test

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Flopsstuff/cotel/internal/export"
	"github.com/Flopsstuff/cotel/internal/storage"
)

// ---- range helpers ----

func TestDayRange(t *testing.T) {
	date := time.Date(2026, 5, 9, 15, 30, 0, 0, time.UTC)
	start, end := export.DayRange(date)
	wantStart := time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)
	if !start.Equal(wantStart) {
		t.Errorf("DayRange start: got %v, want %v", start, wantStart)
	}
	if !end.Equal(wantEnd) {
		t.Errorf("DayRange end: got %v, want %v", end, wantEnd)
	}
}

func TestWeekRange(t *testing.T) {
	cases := []struct {
		name       string
		date       time.Time
		wantStart  time.Time
		wantEnd    time.Time
	}{
		{
			"wednesday",
			time.Date(2026, 5, 6, 10, 0, 0, 0, time.UTC),  // Wednesday
			time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC),   // Monday
			time.Date(2026, 5, 11, 0, 0, 0, 0, time.UTC),
		},
		{
			"monday",
			time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC),
			time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC),
			time.Date(2026, 5, 11, 0, 0, 0, 0, time.UTC),
		},
		{
			"sunday",
			time.Date(2026, 5, 10, 23, 59, 59, 0, time.UTC), // Sunday
			time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC),    // Monday of same week
			time.Date(2026, 5, 11, 0, 0, 0, 0, time.UTC),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, end := export.WeekRange(tc.date)
			if !start.Equal(tc.wantStart) {
				t.Errorf("start: got %v, want %v", start, tc.wantStart)
			}
			if !end.Equal(tc.wantEnd) {
				t.Errorf("end: got %v, want %v", end, tc.wantEnd)
			}
		})
	}
}

func TestMonthRange(t *testing.T) {
	date := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	start, end := export.MonthRange(date)
	wantStart := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if !start.Equal(wantStart) {
		t.Errorf("MonthRange start: got %v, want %v", start, wantStart)
	}
	if !end.Equal(wantEnd) {
		t.Errorf("MonthRange end: got %v, want %v", end, wantEnd)
	}
}

// ---- BuildZIP ----

func mustBuildZIP(t *testing.T, m export.Manifest, spans []storage.Span, daily []storage.DailyUsageRow) *zip.Reader {
	t.Helper()
	var buf bytes.Buffer
	if err := export.BuildZIP(&buf, m, spans, daily); err != nil {
		t.Fatalf("BuildZIP: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	return zr
}

func zipFiles(zr *zip.Reader) map[string]bool {
	m := make(map[string]bool)
	for _, f := range zr.File {
		m[f.Name] = true
	}
	return m
}

func TestBuildZIP_NonEmpty(t *testing.T) {
	start := time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	inp := int64(100)
	spans := []storage.Span{
		{
			SpanID:      "span-1",
			TraceID:     "trace-1",
			Name:        "test.op",
			StartTime:   start.Add(time.Hour),
			EndTime:     start.Add(time.Hour + time.Second),
			InputTokens: &inp,
		},
	}
	daily := []storage.DailyUsageRow{
		{Day: start, Model: "claude-test", SpanCount: 5, TotalCostUSD: 0.01},
	}
	m := export.Manifest{
		FormatVersion: export.FormatVersion,
		CotelVersion:  export.CotelVersion,
		ExportAt:      time.Now().UTC(),
		Period:        "day",
		PeriodStart:   start,
		PeriodEnd:     end,
		Tables: map[string]export.TableMeta{
			"spans":       {RowCount: 1, Columns: export.SpansColumns},
			"daily_usage": {RowCount: 1, Columns: export.DailyUsageColumns},
		},
	}

	zr := mustBuildZIP(t, m, spans, daily)
	files := zipFiles(zr)

	if !files["manifest.json"] {
		t.Fatal("missing manifest.json in ZIP")
	}
	if !files["spans.csv"] {
		t.Fatal("missing spans.csv in ZIP")
	}
	if !files["daily_usage.csv"] {
		t.Fatal("missing daily_usage.csv in ZIP")
	}

	// Verify manifest content.
	for _, f := range zr.File {
		if f.Name != "manifest.json" {
			continue
		}
		rc, _ := f.Open()
		defer rc.Close()
		var got export.Manifest
		if err := json.NewDecoder(rc).Decode(&got); err != nil {
			t.Fatalf("decode manifest: %v", err)
		}
		if got.FormatVersion != 1 {
			t.Errorf("format_version: got %d, want 1", got.FormatVersion)
		}
		if got.Period != "day" {
			t.Errorf("period: got %q, want day", got.Period)
		}
	}

	// Verify spans.csv header + data row.
	for _, f := range zr.File {
		if f.Name != "spans.csv" {
			continue
		}
		rc, _ := f.Open()
		defer rc.Close()
		rows, err := csv.NewReader(rc).ReadAll()
		if err != nil {
			t.Fatalf("read spans.csv: %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("spans.csv: got %d rows, want 2 (header + 1 data)", len(rows))
		}
		if rows[0][0] != "span_id" {
			t.Errorf("spans.csv header[0]: got %q, want span_id", rows[0][0])
		}
		if rows[1][0] != "span-1" {
			t.Errorf("spans.csv data[0]: got %q, want span-1", rows[1][0])
		}
		// input_tokens column index 13
		if rows[1][13] != "100" {
			t.Errorf("spans.csv input_tokens: got %q, want 100", rows[1][13])
		}
		// duration_ms index 7: 1 second = 1000 ms
		if rows[1][7] != "1000" {
			t.Errorf("spans.csv duration_ms: got %q, want 1000", rows[1][7])
		}
	}
}

func TestBuildZIP_OnlyDaily(t *testing.T) {
	start := time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC)
	daily := []storage.DailyUsageRow{
		{Day: start, Model: "claude-test", SpanCount: 3},
	}
	m := export.Manifest{
		FormatVersion: export.FormatVersion,
		CotelVersion:  export.CotelVersion,
		ExportAt:      time.Now().UTC(),
		Period:        "day",
		Tables: map[string]export.TableMeta{
			"daily_usage": {RowCount: 1, Columns: export.DailyUsageColumns},
		},
	}

	zr := mustBuildZIP(t, m, nil, daily)
	files := zipFiles(zr)

	if files["spans.csv"] {
		t.Error("spans.csv present but spans slice was nil")
	}
	if !files["daily_usage.csv"] {
		t.Error("daily_usage.csv missing")
	}
	if !files["manifest.json"] {
		t.Error("manifest.json missing")
	}
}

// ---- HTTP handler ----

// mockDB satisfies export.ExportDB. Auth is now handled by auth.Middleware, not the handler.
type mockDB struct {
	spans []storage.Span
	daily []storage.DailyUsageRow
}

func (m *mockDB) ExportSpans(_, _ time.Time) ([]storage.Span, error)               { return m.spans, nil }
func (m *mockDB) ExportDailyUsage(_, _ time.Time) ([]storage.DailyUsageRow, error) { return m.daily, nil }

func TestHandler_EmptyPeriod404(t *testing.T) {
	h := export.NewHandler(&mockDB{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/export?period=day&date=2026-05-09", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("empty period: got %d, want 404", w.Code)
	}
}

func TestHandler_NonEmptyDay200(t *testing.T) {
	inp := int64(10)
	start := time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC)
	db := &mockDB{
		spans: []storage.Span{
			{SpanID: "s1", TraceID: "t1", Name: "op",
				StartTime: start.Add(time.Hour), EndTime: start.Add(time.Hour + time.Second),
				InputTokens: &inp},
		},
	}
	h := export.NewHandler(db)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/export?period=day&date=2026-05-09", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("non-empty day: got %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type: got %q, want application/zip", ct)
	}
	cd := w.Header().Get("Content-Disposition")
	if cd != `attachment; filename="cotel-export-day-2026-05-09.zip"` {
		t.Errorf("Content-Disposition: got %q", cd)
	}
	body := w.Body.Bytes()
	if _, err := zip.NewReader(bytes.NewReader(body), int64(len(body))); err != nil {
		t.Errorf("response body is not a valid ZIP: %v", err)
	}
}

func TestHandler_BadPeriod400(t *testing.T) {
	h := export.NewHandler(&mockDB{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/export?period=hour&date=2026-05-09", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("bad period: got %d, want 400", w.Code)
	}
}

func TestHandler_BadDate400(t *testing.T) {
	h := export.NewHandler(&mockDB{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/export?period=day&date=not-a-date", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("bad date: got %d, want 400", w.Code)
	}
}

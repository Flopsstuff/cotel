// Integration tests for the full export → import → re-export cycle.
package importpkg_test

import (
	"archive/zip"
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Flopsstuff/cotel/internal/export"
	"github.com/Flopsstuff/cotel/internal/importpkg"
	"github.com/Flopsstuff/cotel/internal/storage"
)

// TestIntegration_RoundTrip inserts known spans into DB A, exports via
// storage.ExportSpans, builds a ZIP, imports into a fresh DB B, and verifies
// span count and cost totals match the source.
func TestIntegration_RoundTrip(t *testing.T) {
	dbA, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open dbA: %v", err)
	}
	defer dbA.Close()

	day := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	costs := []float64{0.010, 0.020}
	inp := int64(100)

	for i, c := range costs {
		cost := c
		if err := dbA.InsertSpan(storage.Span{
			SpanID:      fmt.Sprintf("span-rt-%d", i+1),
			TraceID:     fmt.Sprintf("trace-rt-%d", i+1),
			Name:        "op.roundtrip",
			StartTime:   day.Add(time.Duration(i) * time.Hour),
			EndTime:     day.Add(time.Duration(i)*time.Hour + time.Second),
			Model:       "claude-test",
			InputTokens: &inp,
			CostUSD:     &cost,
		}); err != nil {
			t.Fatalf("InsertSpan %d: %v", i+1, err)
		}
	}

	start, end := export.DayRange(day)
	spans, err := dbA.ExportSpans(start, end)
	if err != nil {
		t.Fatalf("ExportSpans from dbA: %v", err)
	}
	if len(spans) != 2 {
		t.Fatalf("exported spans: got %d, want 2", len(spans))
	}
	daily, err := dbA.ExportDailyUsage(start, end)
	if err != nil {
		t.Fatalf("ExportDailyUsage from dbA: %v", err)
	}

	tables := map[string]export.TableMeta{
		"spans": {RowCount: len(spans), Columns: export.SpansColumns},
	}
	if len(daily) > 0 {
		tables["daily_usage"] = export.TableMeta{RowCount: len(daily), Columns: export.DailyUsageColumns}
	}
	m := export.Manifest{
		FormatVersion: export.FormatVersion,
		CotelVersion:  export.CotelVersion,
		ExportAt:      time.Now().UTC(),
		Period:        "day",
		PeriodStart:   start,
		PeriodEnd:     end,
		Tables:        tables,
	}
	var buf bytes.Buffer
	if err := export.BuildZIP(&buf, m, spans, daily); err != nil {
		t.Fatalf("BuildZIP: %v", err)
	}
	data := buf.Bytes()

	dbB, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open dbB: %v", err)
	}
	defer dbB.Close()

	_, gotSpans, gotDaily, err := importpkg.ReadZIP(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("ReadZIP: %v", err)
	}
	n, err := dbB.ImportSpans(gotSpans)
	if err != nil {
		t.Fatalf("ImportSpans into dbB: %v", err)
	}
	if n != 2 {
		t.Errorf("spans inserted: got %d, want 2", n)
	}
	if len(gotDaily) > 0 {
		if _, err := dbB.ImportDailyUsage(gotDaily); err != nil {
			t.Fatalf("ImportDailyUsage into dbB: %v", err)
		}
	}

	importedSpans, err := dbB.ExportSpans(start, end)
	if err != nil {
		t.Fatalf("ExportSpans from dbB: %v", err)
	}
	if len(importedSpans) != 2 {
		t.Errorf("dbB span count: got %d, want 2", len(importedSpans))
	}

	var totalCost float64
	for _, s := range importedSpans {
		if s.CostUSD != nil {
			totalCost += *s.CostUSD
		}
	}
	wantCost := costs[0] + costs[1]
	if totalCost < wantCost-1e-9 || totalCost > wantCost+1e-9 {
		t.Errorf("cost total: got %f, want %f", totalCost, wantCost)
	}
}

// TestIntegration_Idempotency imports the same ZIP twice and verifies the
// second import inserts 0 rows (INSERT OR IGNORE semantics).
func TestIntegration_Idempotency(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	inp := int64(50)
	start := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	spans := []storage.Span{
		{SpanID: "span-idem-1", TraceID: "trace-idem-1", Name: "op.idem",
			StartTime: start, EndTime: start.Add(time.Second), InputTokens: &inp},
	}
	data := buildTestZIP(t, spans, nil)

	readOnce := func(label string) ([]storage.Span, []storage.DailyUsageRow) {
		_, s, d, err := importpkg.ReadZIP(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatalf("ReadZIP (%s): %v", label, err)
		}
		return s, d
	}

	// First import: expect 1 span inserted.
	s1, d1 := readOnce("first")
	n, err := db.ImportSpans(s1)
	if err != nil {
		t.Fatalf("ImportSpans (first): %v", err)
	}
	if n != 1 {
		t.Errorf("first import spans: got %d, want 1", n)
	}
	if len(d1) > 0 {
		if _, err := db.ImportDailyUsage(d1); err != nil {
			t.Fatalf("ImportDailyUsage (first): %v", err)
		}
	}

	// Second import: expect 0 inserted (idempotent).
	s2, d2 := readOnce("second")
	n, err = db.ImportSpans(s2)
	if err != nil {
		t.Fatalf("ImportSpans (second): %v", err)
	}
	if n != 0 {
		t.Errorf("second import spans: got %d, want 0 (idempotent)", n)
	}
	if len(d2) > 0 {
		n2, err := db.ImportDailyUsage(d2)
		if err != nil {
			t.Fatalf("ImportDailyUsage (second): %v", err)
		}
		if n2 != 0 {
			t.Errorf("second import daily: got %d, want 0 (idempotent)", n2)
		}
	}
}

// TestIntegration_EmptyPeriod404 uses a real empty DB and verifies the export
// handler returns 404 when no data exists for the requested day.
func TestIntegration_EmptyPeriod404(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	h := export.NewHandler(db)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/export?period=day&date=2026-05-03", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("empty period with real DB: got %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

// TestIntegration_DailyUsageOnly inserts spans older than raw retention,
// runs RollupAndPurge (raw spans purged, daily_usage aggregated), exports
// (only daily_usage.csv), imports into a fresh DB, and verifies the
// daily_usage rows are present.
func TestIntegration_DailyUsageOnly(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	// Spans 60 days old are outside the 30-day raw-retention window.
	old := time.Now().UTC().AddDate(0, 0, -60)
	oldDay := time.Date(old.Year(), old.Month(), old.Day(), 0, 0, 0, 0, time.UTC)

	inp := int64(200)
	cacheRead := int64(2010923802) // prod-scale: cache tokens are ~99% of volume
	cacheWrite := int64(30237088)
	cost := 0.05
	if err := db.InsertSpan(storage.Span{
		SpanID:           "span-old-1",
		TraceID:          "trace-old-1",
		Name:             "op.old",
		StartTime:        oldDay.Add(time.Hour),
		EndTime:          oldDay.Add(time.Hour + time.Second),
		Model:            "claude-test",
		SessionID:        "sess-old",
		ToolName:         "Bash",
		InputTokens:      &inp,
		CacheReadTokens:  &cacheRead,
		CacheWriteTokens: &cacheWrite,
		CostUSD:          &cost,
	}); err != nil {
		t.Fatalf("InsertSpan: %v", err)
	}

	if err := db.RollupAndPurge(storage.RetentionConfig{RawDays: 30, AggregateDays: 90}); err != nil {
		t.Fatalf("RollupAndPurge: %v", err)
	}

	start, end := oldDay, oldDay.Add(24*time.Hour)

	rawSpans, err := db.ExportSpans(start, end)
	if err != nil {
		t.Fatalf("ExportSpans after purge: %v", err)
	}
	if len(rawSpans) != 0 {
		t.Fatalf("expected 0 raw spans after purge, got %d", len(rawSpans))
	}

	daily, err := db.ExportDailyUsage(start, end)
	if err != nil {
		t.Fatalf("ExportDailyUsage: %v", err)
	}
	if len(daily) == 0 {
		t.Fatal("expected daily_usage rows after rollup, got 0")
	}

	m := export.Manifest{
		FormatVersion: export.FormatVersion,
		CotelVersion:  export.CotelVersion,
		ExportAt:      time.Now().UTC(),
		Period:        "day",
		PeriodStart:   start,
		PeriodEnd:     end,
		Tables: map[string]export.TableMeta{
			"daily_usage": {RowCount: len(daily), Columns: export.DailyUsageColumns},
		},
	}
	var buf bytes.Buffer
	if err := export.BuildZIP(&buf, m, nil, daily); err != nil {
		t.Fatalf("BuildZIP: %v", err)
	}
	data := buf.Bytes()

	db2, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open db2: %v", err)
	}
	defer db2.Close()

	_, _, gotDaily, err := importpkg.ReadZIP(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("ReadZIP: %v", err)
	}
	n, err := db2.ImportDailyUsage(gotDaily)
	if err != nil {
		t.Fatalf("ImportDailyUsage into db2: %v", err)
	}
	if n == 0 {
		t.Error("expected at least 1 daily_usage row imported, got 0")
	}

	imported, err := db2.ExportDailyUsage(start, end)
	if err != nil {
		t.Fatalf("ExportDailyUsage from db2: %v", err)
	}
	if len(imported) == 0 {
		t.Fatal("expected daily_usage rows in db2 after import, got 0")
	}

	// Cache-token totals must survive roll-up → export → import unchanged;
	// before the fix they were dropped at roll-up, so the exported
	// aggregate carried ~0.6% of the real token volume.
	var got storage.DailyUsageRow
	for _, r := range imported {
		if r.SessionID == "sess-old" {
			got = r
			break
		}
	}
	if got.TotalCacheReadTokens == nil {
		t.Fatalf("total_cache_read_tokens: got NULL, want %d", cacheRead)
	}
	if *got.TotalCacheReadTokens != cacheRead {
		t.Errorf("total_cache_read_tokens: got %d, want %d", *got.TotalCacheReadTokens, cacheRead)
	}
	if got.TotalCacheWriteTokens == nil {
		t.Fatalf("total_cache_write_tokens: got NULL, want %d", cacheWrite)
	}
	if *got.TotalCacheWriteTokens != cacheWrite {
		t.Errorf("total_cache_write_tokens: got %d, want %d", *got.TotalCacheWriteTokens, cacheWrite)
	}
}

// TestIntegration_DailyUsagePreMigrationNULL pins the NULL honesty of the
// cache-token columns. A daily_usage row rolled up before the migration adding
// these columns has no cache totals at all, and the raw spans behind it are already
// purged — so the value is genuinely unknown and must stay NULL through
// export → import. Fabricating a 0 would be indistinguishable from "this day
// really used no cache tokens", which is the lie this test exists to prevent.
func TestIntegration_DailyUsagePreMigrationNULL(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	day := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)

	// A pre-migration row: input/output totals present, cache totals absent.
	if _, err := db.ImportDailyUsage([]storage.DailyUsageRow{{
		Day:               day,
		SessionID:         "sess-pre",
		Model:             "claude-test",
		ToolName:          "Bash",
		SpanCount:         3,
		TotalInputTokens:  200,
		TotalOutputTokens: 100,
		TotalCostUSD:      0.05,
		// TotalCacheReadTokens / TotalCacheWriteTokens deliberately nil.
	}}); err != nil {
		t.Fatalf("seed pre-migration row: %v", err)
	}

	start, end := day, day.Add(24*time.Hour)
	daily, err := db.ExportDailyUsage(start, end)
	if err != nil {
		t.Fatalf("ExportDailyUsage: %v", err)
	}
	if len(daily) != 1 {
		t.Fatalf("expected 1 daily_usage row, got %d", len(daily))
	}
	if daily[0].TotalCacheReadTokens != nil || daily[0].TotalCacheWriteTokens != nil {
		t.Fatalf("NULL cache totals must scan as nil, got read=%v write=%v",
			daily[0].TotalCacheReadTokens, daily[0].TotalCacheWriteTokens)
	}

	m := export.Manifest{
		FormatVersion: export.FormatVersion,
		CotelVersion:  export.CotelVersion,
		ExportAt:      time.Now().UTC(),
		Period:        "day",
		PeriodStart:   start,
		PeriodEnd:     end,
		Tables: map[string]export.TableMeta{
			"daily_usage": {RowCount: len(daily), Columns: export.DailyUsageColumns},
		},
	}
	var buf bytes.Buffer
	if err := export.BuildZIP(&buf, m, nil, daily); err != nil {
		t.Fatalf("BuildZIP: %v", err)
	}
	data := buf.Bytes()

	db2, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open db2: %v", err)
	}
	defer db2.Close()

	_, _, gotDaily, err := importpkg.ReadZIP(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("ReadZIP: %v", err)
	}
	if _, err := db2.ImportDailyUsage(gotDaily); err != nil {
		t.Fatalf("ImportDailyUsage into db2: %v", err)
	}

	imported, err := db2.ExportDailyUsage(start, end)
	if err != nil {
		t.Fatalf("ExportDailyUsage from db2: %v", err)
	}
	if len(imported) != 1 {
		t.Fatalf("expected 1 imported daily_usage row, got %d", len(imported))
	}
	// The empty CSV cell must land back as SQL NULL, not 0.
	if imported[0].TotalCacheReadTokens != nil {
		t.Errorf("total_cache_read_tokens: got %d, want NULL", *imported[0].TotalCacheReadTokens)
	}
	if imported[0].TotalCacheWriteTokens != nil {
		t.Errorf("total_cache_write_tokens: got %d, want NULL", *imported[0].TotalCacheWriteTokens)
	}
	// The columns that were present must be unharmed by the NULL handling.
	if imported[0].TotalInputTokens != 200 {
		t.Errorf("total_input_tokens: got %d, want 200", imported[0].TotalInputTokens)
	}
	if imported[0].TotalCostUSD < 0.049 || imported[0].TotalCostUSD > 0.051 {
		t.Errorf("total_cost_usd: got %f, want ~0.05", imported[0].TotalCostUSD)
	}
}

// TestIntegration_FutureVersion400 uploads a ZIP with format_version=999 and
// expects the import handler to return 400 with a helpful error message.
func TestIntegration_FutureVersion400(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	mf, _ := zw.Create("manifest.json")
	_, _ = mf.Write([]byte(`{"format_version":999,"cotel_version":"0.1.0","export_at":"2026-05-01T00:00:00Z","period":"day","period_start":"2026-05-01T00:00:00Z","period_end":"2026-05-02T00:00:00Z","tables":{}}`))
	_ = zw.Close()
	data := buf.Bytes()

	body, ct := makeMultipartUpload(t, data)
	h := importpkg.NewHandler(&mockDB{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/import", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("future version: got %d, want 400; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "999") {
		t.Errorf("error message should mention version 999, got: %s", w.Body.String())
	}
}

// TestIntegration_Oversized413 uploads a ZIP whose uncompressed content exceeds
// 100 MB and expects the import handler to return 413.
func TestIntegration_Oversized413(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, _ := zw.Create("big.csv")
	chunk := make([]byte, 1024*1024) // 1 MB zeros, compresses heavily
	for i := 0; i <= 100; i++ {     // 101 MB uncompressed total
		_, _ = f.Write(chunk)
	}
	_ = zw.Close()
	data := buf.Bytes()

	body, ct := makeMultipartUpload(t, data)
	h := importpkg.NewHandler(&mockDB{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/import", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized: got %d, want 413; body: %s", w.Code, w.Body.String())
	}
}

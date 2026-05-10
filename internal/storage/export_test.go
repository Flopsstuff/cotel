package storage

import (
	"testing"
	"time"
)

// makeSpan returns a minimal valid Span anchored at ts.
func makeSpan(id string, ts time.Time) Span {
	inp := int64(10)
	return Span{
		TraceID:     "trace-" + id,
		SpanID:      "span-" + id,
		Name:        "test.op",
		StartTime:   ts,
		EndTime:     ts.Add(time.Second),
		SessionID:   "sess-" + id,
		Model:       "claude-test",
		InputTokens: &inp,
	}
}

func TestExportSpans(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	from := base
	to := base.Add(24 * time.Hour)

	// Empty DB → empty result.
	spans, err := db.ExportSpans(from, to)
	if err != nil {
		t.Fatalf("ExportSpans (empty): %v", err)
	}
	if len(spans) != 0 {
		t.Fatalf("ExportSpans (empty): got %d spans, want 0", len(spans))
	}

	// Insert two spans in range and one outside.
	if err := db.InsertSpan(makeSpan("a", base)); err != nil {
		t.Fatalf("InsertSpan a: %v", err)
	}
	if err := db.InsertSpan(makeSpan("b", base.Add(time.Hour))); err != nil {
		t.Fatalf("InsertSpan b: %v", err)
	}
	if err := db.InsertSpan(makeSpan("out", base.Add(25*time.Hour))); err != nil {
		t.Fatalf("InsertSpan out: %v", err)
	}

	spans, err = db.ExportSpans(from, to)
	if err != nil {
		t.Fatalf("ExportSpans: %v", err)
	}
	if len(spans) != 2 {
		t.Fatalf("ExportSpans: got %d spans, want 2", len(spans))
	}
	// Spot-check first span fields.
	if spans[0].SpanID != "span-a" {
		t.Errorf("spans[0].SpanID: got %q, want %q", spans[0].SpanID, "span-a")
	}
	if spans[0].InputTokens == nil || *spans[0].InputTokens != 10 {
		t.Errorf("spans[0].InputTokens: got %v, want 10", spans[0].InputTokens)
	}
	if spans[0].IngestedAt.IsZero() {
		t.Error("spans[0].IngestedAt: want non-zero")
	}
	// Span outside range must not appear.
	for _, s := range spans {
		if s.SpanID == "span-out" {
			t.Error("span-out appeared in export but should be outside range")
		}
	}
}

func TestExportDailyUsage(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	day1 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC)
	dayOut := time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC)
	from := day1
	to := time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC)

	// Empty DB → empty result.
	rows, err := db.ExportDailyUsage(from, to)
	if err != nil {
		t.Fatalf("ExportDailyUsage (empty): %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("ExportDailyUsage (empty): got %d rows, want 0", len(rows))
	}

	// Seed daily_usage directly.
	for _, r := range []DailyUsageRow{
		{Day: day1, SessionID: "s1", Model: "m1", ToolName: "t1", SpanCount: 5, TotalInputTokens: 100, TotalOutputTokens: 50, TotalCostUSD: 0.01},
		{Day: day2, SessionID: "s2", Model: "m1", ToolName: "t1", SpanCount: 3, TotalInputTokens: 60, TotalOutputTokens: 30, TotalCostUSD: 0.005},
		{Day: dayOut, SessionID: "s3", Model: "m1", ToolName: "t1", SpanCount: 1, TotalInputTokens: 10, TotalOutputTokens: 5, TotalCostUSD: 0.001},
	} {
		if _, err := db.ImportDailyUsage([]DailyUsageRow{r}); err != nil {
			t.Fatalf("seed ImportDailyUsage: %v", err)
		}
	}

	rows, err = db.ExportDailyUsage(from, to)
	if err != nil {
		t.Fatalf("ExportDailyUsage: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("ExportDailyUsage: got %d rows, want 2", len(rows))
	}
	if rows[0].SessionID != "s1" {
		t.Errorf("rows[0].SessionID: got %q, want %q", rows[0].SessionID, "s1")
	}
	if rows[0].SpanCount != 5 {
		t.Errorf("rows[0].SpanCount: got %d, want 5", rows[0].SpanCount)
	}
	if rows[1].SessionID != "s2" {
		t.Errorf("rows[1].SessionID: got %q, want %q", rows[1].SessionID, "s2")
	}
	// Row outside range must not appear.
	for _, r := range rows {
		if r.SessionID == "s3" {
			t.Error("s3 appeared in export but its day is outside the range")
		}
	}
}

func TestImportSpans(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	spans := []Span{
		makeSpan("1", base),
		makeSpan("2", base.Add(time.Hour)),
	}

	// Import two new spans → 2 inserted.
	n, err := db.ImportSpans(spans)
	if err != nil {
		t.Fatalf("ImportSpans: %v", err)
	}
	if n != 2 {
		t.Errorf("ImportSpans: got %d inserted, want 2", n)
	}

	// Re-import same spans → 0 inserted (all duplicates skipped).
	n, err = db.ImportSpans(spans)
	if err != nil {
		t.Fatalf("ImportSpans (dup): %v", err)
	}
	if n != 0 {
		t.Errorf("ImportSpans (dup): got %d inserted, want 0", n)
	}

	// Import one duplicate + one new → 1 inserted.
	mixed := []Span{
		makeSpan("2", base.Add(time.Hour)), // dup
		makeSpan("3", base.Add(2*time.Hour)), // new
	}
	n, err = db.ImportSpans(mixed)
	if err != nil {
		t.Fatalf("ImportSpans (mixed): %v", err)
	}
	if n != 1 {
		t.Errorf("ImportSpans (mixed): got %d inserted, want 1", n)
	}

	// Empty input → 0, no error.
	n, err = db.ImportSpans(nil)
	if err != nil {
		t.Fatalf("ImportSpans (empty): %v", err)
	}
	if n != 0 {
		t.Errorf("ImportSpans (empty): got %d, want 0", n)
	}
}

func TestImportDailyUsage(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	day1 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC)

	rows := []DailyUsageRow{
		{Day: day1, SessionID: "s1", Model: "m1", ToolName: "t1", SpanCount: 5, TotalInputTokens: 100},
		{Day: day2, SessionID: "s1", Model: "m1", ToolName: "t1", SpanCount: 3, TotalInputTokens: 60},
	}

	// Import two new rows → 2 inserted.
	n, err := db.ImportDailyUsage(rows)
	if err != nil {
		t.Fatalf("ImportDailyUsage: %v", err)
	}
	if n != 2 {
		t.Errorf("ImportDailyUsage: got %d inserted, want 2", n)
	}

	// Re-import same rows → 0 inserted.
	n, err = db.ImportDailyUsage(rows)
	if err != nil {
		t.Fatalf("ImportDailyUsage (dup): %v", err)
	}
	if n != 0 {
		t.Errorf("ImportDailyUsage (dup): got %d inserted, want 0", n)
	}

	// One duplicate + one new → 1 inserted.
	mixed := []DailyUsageRow{
		{Day: day2, SessionID: "s1", Model: "m1", ToolName: "t1", SpanCount: 3}, // dup PK
		{Day: day1, SessionID: "s2", Model: "m1", ToolName: "t1", SpanCount: 7}, // new
	}
	n, err = db.ImportDailyUsage(mixed)
	if err != nil {
		t.Fatalf("ImportDailyUsage (mixed): %v", err)
	}
	if n != 1 {
		t.Errorf("ImportDailyUsage (mixed): got %d inserted, want 1", n)
	}

	// Empty input → 0, no error.
	n, err = db.ImportDailyUsage(nil)
	if err != nil {
		t.Fatalf("ImportDailyUsage (empty): %v", err)
	}
	if n != 0 {
		t.Errorf("ImportDailyUsage (empty): got %d, want 0", n)
	}
}

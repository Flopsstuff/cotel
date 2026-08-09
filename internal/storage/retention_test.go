package storage

import (
	"testing"
	"time"
)

func TestRollupAndPurge(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	defer db.Close()

	// Insert a span 35 days ago — outside the 30-day raw retention window.
	old := time.Now().AddDate(0, 0, -35)
	inp := int64(100)
	out := int64(50)
	cost := 0.01
	if err := db.InsertSpan(Span{
		TraceID:      "trace-001",
		SpanID:       "span-001",
		Name:         "claude_code.session",
		StartTime:    old,
		EndTime:      old.Add(time.Second),
		SessionID:    "test-session",
		Model:        "claude-sonnet-4-6",
		ToolName:     "Bash",
		InputTokens:  &inp,
		OutputTokens: &out,
		CostUSD:      &cost,
	}); err != nil {
		t.Fatalf("insert span: %v", err)
	}

	cfg := RetentionConfig{RawDays: 30, AggregateDays: 90}
	if err := db.RollupAndPurge(cfg); err != nil {
		t.Fatalf("RollupAndPurge: %v", err)
	}

	// Raw span must be deleted.
	var spanCount int
	if err := db.rw.QueryRow("SELECT COUNT(*) FROM spans").Scan(&spanCount); err != nil {
		t.Fatalf("count spans: %v", err)
	}
	if spanCount != 0 {
		t.Errorf("spans: got %d rows, want 0 after purge", spanCount)
	}

	// daily_usage must have a rolled-up row with correct aggregates.
	var spanCountAgg int64
	var totalInput, totalOutput int64
	var totalCost float64
	row := db.rw.QueryRow(`
		SELECT span_count, total_input_tokens, total_output_tokens, total_cost_usd
		FROM daily_usage
		WHERE session_id = 'test-session' AND model = 'claude-sonnet-4-6'
	`)
	if err := row.Scan(&spanCountAgg, &totalInput, &totalOutput, &totalCost); err != nil {
		t.Fatalf("query daily_usage: %v", err)
	}
	if spanCountAgg != 1 {
		t.Errorf("span_count: got %d, want 1", spanCountAgg)
	}
	if totalInput != 100 {
		t.Errorf("total_input_tokens: got %d, want 100", totalInput)
	}
	if totalOutput != 50 {
		t.Errorf("total_output_tokens: got %d, want 50", totalOutput)
	}
	if totalCost < 0.009 || totalCost > 0.011 {
		t.Errorf("total_cost_usd: got %f, want ~0.01", totalCost)
	}
}

// TestRollupAndPurge_EmptyAndNullPK reproduces FLO-553: a span whose model
// (or session_id / tool_name) is NULL or empty must not abort the roll-up with
// "NOT NULL constraint failed: daily_usage.model". Before the fix this test
// fails on the very first RollupAndPurge call; after the fix the span is rolled
// up under the 'unknown' sentinel and empty/NULL collapse into one bucket.
func TestRollupAndPurge_EmptyAndNullPK(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	defer db.Close()

	old := time.Now().AddDate(0, 0, -35) // outside the 30-day raw window
	end := old.Add(time.Second)

	// Empty-string model via the normal ingest path.
	if err := db.InsertSpan(Span{
		TraceID: "t-empty", SpanID: "s-empty", Name: "n",
		StartTime: old, EndTime: end,
		SessionID: "sess", Model: "", ToolName: "Bash",
	}); err != nil {
		t.Fatalf("insert empty-model span: %v", err)
	}
	// NULL model, same day/session/tool as the empty one — must collapse into
	// the SAME 'unknown' bucket, not a second row.
	rawInsert(t, db, "t-null-m", "s-null-m", old, end, nil, ptr("sess"), ptr("Bash"))
	// NULL session_id and NULL tool_name — the other NOT NULL PK columns.
	rawInsert(t, db, "t-null-s", "s-null-s", old, end, ptr("claude-x"), nil, ptr("Bash"))
	rawInsert(t, db, "t-null-t", "s-null-t", old, end, ptr("claude-x"), ptr("sess2"), nil)

	// The whole point: this call must not error.
	if err := db.RollupAndPurge(RetentionConfig{RawDays: 30, AggregateDays: 90}); err != nil {
		t.Fatalf("RollupAndPurge with empty/NULL PK columns: %v", err)
	}

	// No NULLs may reach daily_usage's PK columns.
	var nullPK int64
	if err := db.rw.QueryRow(`
		SELECT COUNT(*) FROM daily_usage
		WHERE session_id IS NULL OR model IS NULL OR tool_name IS NULL
	`).Scan(&nullPK); err != nil {
		t.Fatalf("count null PK rows: %v", err)
	}
	if nullPK != 0 {
		t.Errorf("daily_usage has %d rows with NULL PK column(s), want 0", nullPK)
	}

	// Empty '' and NULL model spans (same day/sess/tool) collapse into one
	// 'unknown' row carrying both spans.
	var spanCount int64
	if err := db.rw.QueryRow(`
		SELECT span_count FROM daily_usage
		WHERE session_id = 'sess' AND model = ? AND tool_name = 'Bash'
	`, UnknownSentinel).Scan(&spanCount); err != nil {
		t.Fatalf("query 'unknown'-model bucket: %v", err)
	}
	if spanCount != 2 {
		t.Errorf("unknown-model bucket span_count: got %d, want 2 (empty + NULL)", spanCount)
	}

	// The NULL session_id and NULL tool_name spans landed under the sentinel too.
	var unknownSess, unknownTool int64
	if err := db.rw.QueryRow(`SELECT COUNT(*) FROM daily_usage WHERE session_id = ?`, UnknownSentinel).Scan(&unknownSess); err != nil {
		t.Fatalf("count unknown session_id: %v", err)
	}
	if unknownSess != 1 {
		t.Errorf("unknown session_id rows: got %d, want 1", unknownSess)
	}
	if err := db.rw.QueryRow(`SELECT COUNT(*) FROM daily_usage WHERE tool_name = ?`, UnknownSentinel).Scan(&unknownTool); err != nil {
		t.Fatalf("count unknown tool_name: %v", err)
	}
	if unknownTool != 1 {
		t.Errorf("unknown tool_name rows: got %d, want 1", unknownTool)
	}
}

// rawInsert writes a span straight to the table so NULL (not empty-string) can
// be stored in model/session_id/tool_name — InsertSpan binds Go strings, which
// can never be SQL NULL.
func rawInsert(t *testing.T, db *DB, traceID, spanID string, start, end time.Time, model, sessionID, toolName *string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO spans (trace_id, span_id, name, start_time, end_time, session_id, model, tool_name)
		VALUES (?, ?, 'n', ?, ?, ?, ?, ?)
	`, traceID, spanID, start, end, nullable(sessionID), nullable(model), nullable(toolName)); err != nil {
		t.Fatalf("raw insert %s: %v", spanID, err)
	}
}

// TestRecordRetentionRun checks the worker's per-cycle health recording: a
// successful roll-up over empty-model data records status "ok" and clears any
// prior error; a failed cycle records "error" with the message.
func TestRecordRetentionRun(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	defer db.Close()

	// A real worker cycle over an empty-model span: roll up, then record ok.
	old := time.Now().AddDate(0, 0, -35)
	if err := db.InsertSpan(Span{
		TraceID: "t1", SpanID: "s1", Name: "n",
		StartTime: old, EndTime: old.Add(time.Second),
		SessionID: "sess", Model: "", ToolName: "Bash",
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rollErr := db.RollupAndPurge(RetentionConfig{RawDays: 30, AggregateDays: 90})
	db.recordRetentionRun(rollErr)
	if rollErr != nil {
		t.Fatalf("rollup over empty-model data errored: %v", rollErr)
	}
	if got, _ := db.GetSetting(settingRetentionStatus); got != "ok" {
		t.Errorf("status after ok cycle: got %q, want ok", got)
	}
	if got, _ := db.GetSetting(settingRetentionError); got != "" {
		t.Errorf("error text after ok cycle: got %q, want empty", got)
	}

	// A failed cycle records the error.
	db.recordRetentionRun(errStub("boom"))
	if got, _ := db.GetSetting(settingRetentionStatus); got != "error" {
		t.Errorf("status after failed cycle: got %q, want error", got)
	}
	if got, _ := db.GetSetting(settingRetentionError); got != "boom" {
		t.Errorf("error text after failed cycle: got %q, want boom", got)
	}
}

type errStub string

func (e errStub) Error() string { return string(e) }

func ptr(s string) *string { return &s }

func nullable(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

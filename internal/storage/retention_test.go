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

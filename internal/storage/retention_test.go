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
	cacheRead := int64(900000) // cache tokens dwarf input/output in real traffic
	cacheWrite := int64(30000)
	cost := 0.01
	if err := db.InsertSpan(Span{
		TraceID:          "trace-001",
		SpanID:           "span-001",
		Name:             "claude_code.session",
		StartTime:        old,
		EndTime:          old.Add(time.Second),
		SessionID:        "test-session",
		Model:            "claude-sonnet-4-6",
		ToolName:         "Bash",
		InputTokens:      &inp,
		OutputTokens:     &out,
		CacheReadTokens:  &cacheRead,
		CacheWriteTokens: &cacheWrite,
		CostUSD:          &cost,
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

	// daily_usage must have a rolled-up row with correct aggregates, including
	// cache-token totals — the roll-up before the fix dropped ~99% of the real token volume.
	var spanCountAgg int64
	var totalInput, totalOutput, totalCacheRead, totalCacheWrite int64
	var totalCost float64
	row := db.rw.QueryRow(`
		SELECT span_count, total_input_tokens, total_output_tokens,
		       total_cache_read_tokens, total_cache_write_tokens, total_cost_usd
		FROM daily_usage
		WHERE session_id = 'test-session' AND model = 'claude-sonnet-4-6'
	`)
	if err := row.Scan(&spanCountAgg, &totalInput, &totalOutput,
		&totalCacheRead, &totalCacheWrite, &totalCost); err != nil {
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
	if totalCacheRead != 900000 {
		t.Errorf("total_cache_read_tokens: got %d, want 900000", totalCacheRead)
	}
	if totalCacheWrite != 30000 {
		t.Errorf("total_cache_write_tokens: got %d, want 30000", totalCacheWrite)
	}
	if totalCost < 0.009 || totalCost > 0.011 {
		t.Errorf("total_cost_usd: got %f, want ~0.01", totalCost)
	}
}

// TestRollupAndPurge_EmptyAndNullPK verifies that a span whose model
// (or session_id / tool_name) is NULL or empty does not abort the roll-up with
// "NOT NULL constraint failed: daily_usage.model". The span must be rolled
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

// TestRollupAndPurge_BoundaryDayNotSplit pins the accounting invariant that
// makes long-term history trustworthy: for any day, the daily_usage aggregate
// plus the raw spans still in the table must always equal the full truth for
// that day. The retention worker ticks every 6h while the cutoff is a wall-clock
// instant, so a day is repeatedly visible half-inside and half-outside the
// window; each roll-up REPLACEs the day's row, so a split day used to keep only
// the last slice and drop everything rolled up before it.
func TestRollupAndPurge_BoundaryDayNotSplit(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	defer db.Close()

	// One day, two spans on opposite sides of the cutoffs below.
	day := time.Date(2026, 1, 15, 0, 0, 0, 0, time.Local)
	insertBoundarySpan(t, db, "early", day.Add(2*time.Hour), 100, 10, 1000, 200, 0.10)
	insertBoundarySpan(t, db, "late", day.Add(10*time.Hour), 7, 3, 70, 30, 0.01)

	const rawDays = 30
	cfg := RetentionConfig{RawDays: rawDays, AggregateDays: 90}
	want := dayTotals{spans: 2, input: 107, output: 13, cacheRead: 1070, cacheWrite: 230, cost: 0.11}

	// Three worker ticks 6h apart. The first two put the cutoff *inside* the
	// day (08:00 then 14:00) — that is the split; the third clears it entirely.
	for i, tick := range []time.Time{
		day.AddDate(0, 0, rawDays).Add(8 * time.Hour),
		day.AddDate(0, 0, rawDays).Add(14 * time.Hour),
		day.AddDate(0, 0, rawDays+1).Add(8 * time.Hour),
	} {
		if err := db.rollupAndPurgeAt(cfg, tick); err != nil {
			t.Fatalf("tick %d (cutoff %s): %v", i+1, tick.AddDate(0, 0, -rawDays), err)
		}
		if got := accountedFor(t, db, day); got != want {
			t.Errorf("after tick %d (cutoff %s): accounted %+v, want %+v",
				i+1, tick.AddDate(0, 0, -rawDays).Format(time.RFC3339), got, want)
		}
	}

	// The day must have ended up fully rolled up, not merely still raw.
	var rawLeft int64
	if err := db.rw.QueryRow(`SELECT COUNT(*) FROM spans`).Scan(&rawLeft); err != nil {
		t.Fatalf("count spans: %v", err)
	}
	if rawLeft != 0 {
		t.Errorf("raw spans left after the day fell fully outside the window: got %d, want 0", rawLeft)
	}
}

// TestRollupAndPurge_Idempotent pins that re-running a roll-up with no new spans
// leaves daily_usage byte-identical — a crash between the INSERT and the DELETE
// must be safe to retry.
func TestRollupAndPurge_Idempotent(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	defer db.Close()

	day := time.Date(2026, 1, 15, 0, 0, 0, 0, time.Local)
	insertBoundarySpan(t, db, "a", day.Add(2*time.Hour), 100, 10, 1000, 200, 0.10)
	insertBoundarySpan(t, db, "b", day.Add(10*time.Hour), 7, 3, 70, 30, 0.01)

	const rawDays = 30
	cfg := RetentionConfig{RawDays: rawDays, AggregateDays: 90}
	tick := day.AddDate(0, 0, rawDays+1).Add(8 * time.Hour)

	if err := db.rollupAndPurgeAt(cfg, tick); err != nil {
		t.Fatalf("first roll-up: %v", err)
	}
	first := accountedFor(t, db, day)

	for i := 0; i < 3; i++ {
		if err := db.rollupAndPurgeAt(cfg, tick); err != nil {
			t.Fatalf("re-run %d: %v", i+1, err)
		}
		if got := accountedFor(t, db, day); got != first {
			t.Errorf("re-run %d changed the aggregate: got %+v, want %+v", i+1, got, first)
		}
	}

	var rows int64
	if err := db.rw.QueryRow(`SELECT COUNT(*) FROM daily_usage`).Scan(&rows); err != nil {
		t.Fatalf("count daily_usage: %v", err)
	}
	if rows != 1 {
		t.Errorf("daily_usage rows after 4 roll-ups: got %d, want 1", rows)
	}
}

type dayTotals struct {
	spans                 int64
	input, output         int64
	cacheRead, cacheWrite int64
	cost                  float64
}

// accountedFor sums what the system still knows about day: the rolled-up
// aggregate plus any raw spans not yet purged. Roll-up must only ever move
// usage between the two, never destroy it.
func accountedFor(t *testing.T, db *DB, day time.Time) dayTotals {
	t.Helper()
	var got dayTotals
	key := day.Format("2006-01-02")
	err := db.rw.QueryRow(`
		WITH agg AS (
		  SELECT COALESCE(SUM(span_count), 0) AS spans,
		         COALESCE(SUM(total_input_tokens), 0) AS inp,
		         COALESCE(SUM(total_output_tokens), 0) AS outp,
		         COALESCE(SUM(total_cache_read_tokens), 0) AS cr,
		         COALESCE(SUM(total_cache_write_tokens), 0) AS cw,
		         COALESCE(SUM(total_cost_usd), 0) AS cost
		  FROM daily_usage WHERE day = CAST(? AS DATE)
		), raw AS (
		  SELECT COUNT(*) AS spans,
		         COALESCE(SUM(input_tokens), 0) AS inp,
		         COALESCE(SUM(output_tokens), 0) AS outp,
		         COALESCE(SUM(cache_read_tokens), 0) AS cr,
		         COALESCE(SUM(cache_write_tokens), 0) AS cw,
		         COALESCE(SUM(cost_usd), 0) AS cost
		  FROM spans WHERE strftime(CAST(start_time AS TIMESTAMP), '%Y-%m-%d') = ?
		)
		SELECT agg.spans + raw.spans, agg.inp + raw.inp, agg.outp + raw.outp,
		       agg.cr + raw.cr, agg.cw + raw.cw, round(agg.cost + raw.cost, 6)
		FROM agg, raw
	`, key, key).Scan(&got.spans, &got.input, &got.output, &got.cacheRead, &got.cacheWrite, &got.cost)
	if err != nil {
		t.Fatalf("account for %s: %v", day.Format("2006-01-02"), err)
	}
	return got
}

func insertBoundarySpan(t *testing.T, db *DB, id string, start time.Time, in, out, cacheRead, cacheWrite int64, cost float64) {
	t.Helper()
	if err := db.InsertSpan(Span{
		TraceID: "trace-" + id, SpanID: "span-" + id, Name: "claude_code.session",
		StartTime: start, EndTime: start.Add(time.Second),
		SessionID: "sess", Model: "claude-opus-5", ToolName: "Bash",
		InputTokens: &in, OutputTokens: &out,
		CacheReadTokens: &cacheRead, CacheWriteTokens: &cacheWrite,
		CostUSD: &cost,
	}); err != nil {
		t.Fatalf("insert span %s: %v", id, err)
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

package storage

import (
	"testing"
	"time"
)

// insertCostSpan inserts a span with explicit cost_usd for backfill tests.
func insertCostSpan(t *testing.T, db *DB, spanID, model string, in, out int64, costUSD float64) {
	t.Helper()
	now := time.Now()
	_, err := db.rw.Exec(`
		INSERT INTO spans (trace_id, span_id, name, start_time, end_time,
		                   session_id, tool_name, model,
		                   input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd)
		VALUES (?,?,?,?,?,?,?,?,?,?,0,0,?)`,
		"trace1", spanID, "test", now, now.Add(time.Second),
		"sess-test", "tool-test", model, in, out, costUSD,
	)
	if err != nil {
		t.Fatalf("insertCostSpan %s: %v", spanID, err)
	}
}

func TestBackfillCostUSD_CorrectsZeroAndOverpriced(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Span that had no price (model missing from old table) → cost_usd = 0
	insertCostSpan(t, db, "s1", "claude-opus-4-8", 1_000_000, 0, 0)
	// Span overpriced at old 15/75 rate: 1M input * 15 = $15 instead of correct $5
	insertCostSpan(t, db, "s2", "claude-opus-4-7", 1_000_000, 0, 15.0)
	// Span with empty model — must be untouched (cost stays 0)
	insertCostSpan(t, db, "s3", "", 1_000_000, 0, 0)

	rep, err := db.BackfillCostUSD()
	if err != nil {
		t.Fatalf("BackfillCostUSD: %v", err)
	}

	if rep.SpansToUpdate != 2 {
		t.Errorf("SpansToUpdate = %d, want 2", rep.SpansToUpdate)
	}
	if rep.EmptyModel != 1 {
		t.Errorf("EmptyModel = %d, want 1", rep.EmptyModel)
	}

	var cost1, cost2, cost3 float64
	db.rw.QueryRow(`SELECT COALESCE(cost_usd,0) FROM spans WHERE span_id='s1'`).Scan(&cost1)
	db.rw.QueryRow(`SELECT COALESCE(cost_usd,0) FROM spans WHERE span_id='s2'`).Scan(&cost2)
	db.rw.QueryRow(`SELECT COALESCE(cost_usd,0) FROM spans WHERE span_id='s3'`).Scan(&cost3)

	// claude-opus-4-8: $5/MTok input → 1M tokens = $5
	if cost1 < 4.99 || cost1 > 5.01 {
		t.Errorf("s1 cost_usd = %.4f, want ~5.0", cost1)
	}
	// claude-opus-4-7: also $5/MTok → $5 (corrected from $15)
	if cost2 < 4.99 || cost2 > 5.01 {
		t.Errorf("s2 cost_usd = %.4f, want ~5.0 (corrected from 15.0)", cost2)
	}
	// empty-model span unchanged
	if cost3 != 0 {
		t.Errorf("s3 (no model) cost_usd = %.4f, want 0", cost3)
	}
}

func TestBackfillCostUSD_UnknownModelSkipped(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Span with a model name not in the pricing table — must NOT be zeroed out.
	insertCostSpan(t, db, "u1", "gpt-99-turbo", 1_000_000, 0, 3.50)

	rep, err := db.BackfillCostUSD()
	if err != nil {
		t.Fatalf("BackfillCostUSD: %v", err)
	}

	if rep.UnknownModel != 1 {
		t.Errorf("UnknownModel = %d, want 1", rep.UnknownModel)
	}
	if rep.SpansToUpdate != 0 {
		t.Errorf("SpansToUpdate = %d, want 0 (unknown model must be skipped)", rep.SpansToUpdate)
	}

	var cost float64
	db.rw.QueryRow(`SELECT COALESCE(cost_usd,0) FROM spans WHERE span_id='u1'`).Scan(&cost)
	if cost != 3.50 {
		t.Errorf("unknown-model span cost_usd = %.4f, want 3.50 (unchanged)", cost)
	}
}

func TestBackfillCostUSD_Idempotent(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	insertCostSpan(t, db, "s1", "claude-opus-4-8", 500_000, 200_000, 0)

	rep1, err := db.BackfillCostUSD()
	if err != nil {
		t.Fatalf("first backfill: %v", err)
	}

	var costAfter1 float64
	db.rw.QueryRow(`SELECT COALESCE(cost_usd,0) FROM spans WHERE span_id='s1'`).Scan(&costAfter1)

	rep2, err := db.BackfillCostUSD()
	if err != nil {
		t.Fatalf("second backfill: %v", err)
	}

	var costAfter2 float64
	db.rw.QueryRow(`SELECT COALESCE(cost_usd,0) FROM spans WHERE span_id='s1'`).Scan(&costAfter2)

	if costAfter1 != costAfter2 {
		t.Errorf("not idempotent: first=%.6f second=%.6f", costAfter1, costAfter2)
	}
	// Second run should find nothing to update (already correct).
	if rep2.SpansToUpdate != 0 {
		t.Errorf("second run SpansToUpdate = %d, want 0 (already idempotent)", rep2.SpansToUpdate)
	}
	_ = rep1
}

func TestDryRunBackfill_NoWrites(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	insertCostSpan(t, db, "s1", "claude-opus-4-8", 1_000_000, 0, 0)
	insertCostSpan(t, db, "s2", "", 1_000_000, 0, 0)         // empty model
	insertCostSpan(t, db, "s3", "gpt-99", 1_000_000, 0, 7.0) // unknown model

	rep, err := db.DryRunBackfill()
	if err != nil {
		t.Fatalf("DryRunBackfill: %v", err)
	}

	if rep.SpansToUpdate != 1 {
		t.Errorf("SpansToUpdate = %d, want 1", rep.SpansToUpdate)
	}
	if rep.EmptyModel != 1 {
		t.Errorf("EmptyModel = %d, want 1", rep.EmptyModel)
	}
	if rep.UnknownModel != 1 {
		t.Errorf("UnknownModel = %d, want 1", rep.UnknownModel)
	}

	// Verify nothing was written.
	var cost float64
	db.rw.QueryRow(`SELECT COALESCE(cost_usd,0) FROM spans WHERE span_id='s1'`).Scan(&cost)
	if cost != 0 {
		t.Errorf("dry-run wrote to DB: s1 cost_usd = %.4f, want 0", cost)
	}
	db.rw.QueryRow(`SELECT COALESCE(cost_usd,0) FROM spans WHERE span_id='s3'`).Scan(&cost)
	if cost != 7.0 {
		t.Errorf("dry-run modified unknown model span: s3 cost_usd = %.4f, want 7.0", cost)
	}
}

func TestBackfillCostUSD_CacheTokens(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Insert with cache tokens directly to verify cache rates are applied
	_, err = db.rw.Exec(`
		INSERT INTO spans (trace_id, span_id, name, start_time, end_time, model,
		                   input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd)
		VALUES ('t','c1','test',now(),now(),'claude-opus-4-8',0,0,1000000,0, 0.0)`)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.BackfillCostUSD(); err != nil {
		t.Fatal(err)
	}

	var cost float64
	db.rw.QueryRow(`SELECT COALESCE(cost_usd,0) FROM spans WHERE span_id='c1'`).Scan(&cost)
	// claude-opus-4-8 cache_read = $0.50/MTok → 1M tokens = $0.50
	if cost < 0.49 || cost > 0.51 {
		t.Errorf("cache read cost = %.4f, want ~0.50", cost)
	}
}

// B3: a priced model with zero billable tokens computes cost 0 but must NOT be
// counted as an unknown model — only genuinely unpriced models are "unknown".
func TestComputeReport_KnownModelZeroTokens_NotUnknown(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	insertCostSpan(t, db, "z1", "claude-opus-4-8", 0, 0, 0)      // known model, zero tokens → cost 0
	insertCostSpan(t, db, "u1", "gpt-99-turbo", 1_000_000, 0, 0) // genuinely unknown model

	rep, err := db.DryRunBackfill()
	if err != nil {
		t.Fatalf("DryRunBackfill: %v", err)
	}

	if rep.UnknownModel != 1 {
		t.Errorf("UnknownModel = %d, want 1 (only gpt-99; the zero-token opus span is NOT unknown)", rep.UnknownModel)
	}
	var found bool
	for _, m := range rep.ModelRows {
		if m.Model == "claude-opus-4-8" {
			found = true
			if m.SpanCount != 1 {
				t.Errorf("claude-opus-4-8 SpanCount = %d, want 1", m.SpanCount)
			}
		}
	}
	if !found {
		t.Errorf("known zero-token model missing from ModelRows breakdown")
	}
}

// The backfill must leave daily_usage completely alone. Roll-up derives
// total_cost_usd as SUM(spans.cost_usd), so post-backfill roll-ups are correct
// by construction; an already-rolled-up row cannot be repaired from its stored
// counters because daily_usage persists no cache tokens. Re-deriving it would
// only lower a correct row on re-run.
func TestBackfillCostUSD_DoesNotTouchDailyUsage(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// A rolled-up row whose cost was computed under the old (wrong) rates, and a
	// cache-heavy row whose correct cost is far above its input+output-only cost.
	if _, err := db.rw.Exec(`
		INSERT INTO daily_usage (day, session_id, model, tool_name, user_id, span_count,
		                         total_input_tokens, total_output_tokens, total_cost_usd)
		VALUES ('2026-07-01', 'sess1', 'claude-opus-4-7', 'tool1', NULL, 10, 1000000, 0, 15.0),
		       ('2026-07-02', 'sess2', 'claude-opus-4-8', 'tool1', NULL, 20, 1000,    0, 42.0)`); err != nil {
		t.Fatal(err)
	}
	// A recent raw span not represented in daily_usage — must not be materialised.
	insertCostSpan(t, db, "recent1", "claude-opus-4-8", 1_000_000, 0, 0)

	if _, err := db.BackfillCostUSD(); err != nil {
		t.Fatalf("BackfillCostUSD: %v", err)
	}

	// The span itself is still corrected.
	var spanCost float64
	db.rw.QueryRow(`SELECT COALESCE(cost_usd,0) FROM spans WHERE span_id='recent1'`).Scan(&spanCost)
	if spanCost < 4.99 || spanCost > 5.01 {
		t.Errorf("span cost_usd = %.4f, want ~5.0", spanCost)
	}

	// Every daily_usage row is byte-for-byte unchanged.
	for _, tc := range []struct {
		day       string
		cost      float64
		spanCount int64
		in        int64
	}{
		{"2026-07-01", 15.0, 10, 1_000_000},
		{"2026-07-02", 42.0, 20, 1_000},
	} {
		var cost float64
		var spanCount, in, out int64
		db.rw.QueryRow(`SELECT total_cost_usd, span_count, total_input_tokens, total_output_tokens
		                FROM daily_usage WHERE day=?`, tc.day).Scan(&cost, &spanCount, &in, &out)
		if cost != tc.cost {
			t.Errorf("%s total_cost_usd = %.4f, want %.4f (backfill must not touch daily_usage)", tc.day, cost, tc.cost)
		}
		if spanCount != tc.spanCount || in != tc.in || out != 0 {
			t.Errorf("%s counters changed: span_count=%d in=%d out=%d", tc.day, spanCount, in, out)
		}
	}

	// No rows materialised from the recent raw span.
	var n int64
	db.rw.QueryRow(`SELECT COUNT(*) FROM daily_usage`).Scan(&n)
	if n != 2 {
		t.Errorf("daily_usage row count = %d, want 2 (backfill must not materialise aggregates)", n)
	}
}

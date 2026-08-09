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
	insertCostSpan(t, db,"s1", "claude-opus-4-8", 1_000_000, 0, 0)
	// Span overpriced at old 15/75 rate: 1M input * 15 = $15 instead of correct $5
	insertCostSpan(t, db,"s2", "claude-opus-4-7", 1_000_000, 0, 15.0)
	// Span with no model — must be untouched (cost stays 0)
	insertCostSpan(t, db,"s3", "", 1_000_000, 0, 0)

	res, err := db.BackfillCostUSD()
	if err != nil {
		t.Fatalf("BackfillCostUSD: %v", err)
	}

	if res.SpansScanned != 2 {
		t.Errorf("SpansScanned = %d, want 2", res.SpansScanned)
	}
	if res.SpansUpdated != 2 {
		t.Errorf("SpansUpdated = %d, want 2", res.SpansUpdated)
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
	// no-model span unchanged
	if cost3 != 0 {
		t.Errorf("s3 (no model) cost_usd = %.4f, want 0", cost3)
	}
}

func TestBackfillCostUSD_Idempotent(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	insertCostSpan(t, db,"s1", "claude-opus-4-8", 500_000, 200_000, 0)

	res1, err := db.BackfillCostUSD()
	if err != nil {
		t.Fatalf("first backfill: %v", err)
	}

	var costAfter1 float64
	db.rw.QueryRow(`SELECT COALESCE(cost_usd,0) FROM spans WHERE span_id='s1'`).Scan(&costAfter1)

	res2, err := db.BackfillCostUSD()
	if err != nil {
		t.Fatalf("second backfill: %v", err)
	}

	var costAfter2 float64
	db.rw.QueryRow(`SELECT COALESCE(cost_usd,0) FROM spans WHERE span_id='s1'`).Scan(&costAfter2)

	if costAfter1 != costAfter2 {
		t.Errorf("not idempotent: first=%.6f second=%.6f", costAfter1, costAfter2)
	}
	if res1.SpansUpdated != res2.SpansUpdated {
		t.Errorf("SpansUpdated differs: %d vs %d", res1.SpansUpdated, res2.SpansUpdated)
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

func TestBackfillCostUSD_DailyUsageOrphan(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Insert a daily_usage row (simulating already-purged spans) with wrong cost.
	// tool_name must be non-NULL because it's part of the PK.
	_, err = db.rw.Exec(`
		INSERT INTO daily_usage (day, session_id, model, tool_name, user_id, span_count,
		                         total_input_tokens, total_output_tokens, total_cost_usd)
		VALUES ('2026-07-01', 'sess1', 'claude-opus-4-7', 'tool1', NULL, 10,
		        1000000, 0, 15.0)
	`)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.BackfillCostUSD(); err != nil {
		t.Fatal(err)
	}

	var cost float64
	db.rw.QueryRow(`SELECT total_cost_usd FROM daily_usage WHERE day='2026-07-01'`).Scan(&cost)
	// claude-opus-4-7: $5/MTok input → 1M tokens = $5 (corrected from $15)
	if cost < 4.99 || cost > 5.01 {
		t.Errorf("daily_usage total_cost_usd = %.4f, want ~5.0", cost)
	}
}

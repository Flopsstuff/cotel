package dashboard_test

import (
	"math"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/Flopsstuff/cotel/internal/dashboard"
	"github.com/Flopsstuff/cotel/internal/storage"
)

// TestP95ResponseTime verifies that all dashboard endpoints respond within 1 s
// at the 95th percentile when the spans table contains 1 M rows.
// Data is generated in-process via DuckDB's vectorised INSERT … SELECT.
func TestP95ResponseTime(t *testing.T) {
	if testing.Short() {
		t.Skip("p95 load test skipped in short mode")
	}

	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	defer db.Close()

	// Bulk-insert 1 M rows. Every 1000th row (i%1000==0) is a session span so
	// we get 1 000 distinct sessions → 20 pages, avoiding thousands of page links.
	// All timestamps land within the past 30 days so the dashboard chart shows data.
	// Cast NOW() to TIMESTAMP (not TIMESTAMPTZ) so DuckDB's interval subtraction
	// operator resolves correctly (DuckDB 1.8.x lacks -(TIMESTAMPTZ, INTERVAL)).
	insertStart := time.Now()
	if _, err := db.Exec(`
		INSERT INTO spans
			(trace_id, span_id, name, start_time, end_time,
			 session_id, model, tool_name, status_code,
			 input_tokens, output_tokens, cost_usd,
			 attributes, resource_attrs)
		SELECT
			'trace-' || i::VARCHAR,
			'span-'  || i::VARCHAR,
			CASE WHEN i % 1000 = 0 THEN 'claude_code.session'
			     WHEN i % 5   = 0 THEN 'claude_code.tool_use'
			     ELSE 'claude_code.api_request' END,
			NOW()::TIMESTAMP - ((i % 30)::INTEGER * INTERVAL '1 day'),
			NOW()::TIMESTAMP - ((i % 30)::INTEGER * INTERVAL '1 day') + INTERVAL '1 second',
			'sess-' || (i % 1000)::VARCHAR,
			CASE WHEN i % 3 = 0 THEN 'claude-sonnet-4-6'
			     WHEN i % 3 = 1 THEN 'claude-opus-4-7'
			     ELSE 'claude-haiku-4-5' END,
			CASE WHEN i % 5 = 0 THEN 'Bash'
			     WHEN i % 7 = 0 THEN 'Edit'
			     ELSE NULL END,
			1,
			1000 + (i % 5000),
			100  + (i % 1000),
			0.001 + (i % 100)::DOUBLE / 10000.0,
			'{}',
			'{}'
		FROM range(1, 1000001) AS t(i)
	`); err != nil {
		t.Fatalf("bulk insert 1M rows: %v", err)
	}
	t.Logf("bulk insert 1M rows: %s", time.Since(insertStart))

	h := dashboard.New(db.ReadOnly())

	// sess-0 has a session span (i%1000==0 → i=1000,2000,…) plus 999 child spans.
	endpoints := []struct{ name, path string }{
		{"GET /", "/"},
		{"GET /sessions", "/sessions"},
		{"GET /sessions/:id", "/sessions/sess-0"},
		{"GET /costs", "/costs"},
		{"GET /tools", "/tools"},
	}

	const iters = 20
	for _, ep := range endpoints {
		lats := make([]time.Duration, 0, iters)
		for range iters {
			req := httptest.NewRequest(http.MethodGet, ep.path, nil)
			rr := httptest.NewRecorder()
			t0 := time.Now()
			h.ServeHTTP(rr, req)
			lats = append(lats, time.Since(t0))
			if rr.Code != http.StatusOK {
				t.Errorf("%s: got HTTP %d", ep.name, rr.Code)
			}
		}
		sort.Slice(lats, func(i, j int) bool { return lats[i] < lats[j] })
		p95idx := int(math.Ceil(float64(iters)*0.95)) - 1
		p95 := lats[p95idx]
		t.Logf("%s: p95=%s  max=%s", ep.name, p95, lats[iters-1])
		if p95 >= time.Second {
			t.Errorf("%s: p95 latency %s ≥ 1 s (limit)", ep.name, p95)
		}
	}
}

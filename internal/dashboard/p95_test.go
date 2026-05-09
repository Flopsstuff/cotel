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

// TestP95ResponseTime verifies that /healthz (DB touch) and the SPA catch-all
// respond within 1 s at the 95th percentile.
func TestP95ResponseTime(t *testing.T) {
	if testing.Short() {
		t.Skip("p95 load test skipped in short mode")
	}

	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	defer db.Close()

	h := dashboard.New(db.ReadOnly())

	endpoints := []struct{ name, path string }{
		{"GET /healthz", "/healthz"},
		{"GET /", "/"},
		{"GET /sessions", "/sessions"},
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

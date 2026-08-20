package api_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Flopsstuff/cotel/internal/api"
)

var errSimulatedQuery = errors.New("simulated query failure")

// queryFailDB wraps a working DB but makes every Query call fail. QueryRow is
// delegated so aggregate COUNTs still succeed — enough to prove a mid-handler
// Query failure becomes a non-2xx instead of a 200 of zeros.
type queryFailDB struct {
	inner api.DB
}

func (d *queryFailDB) QueryRow(query string, args ...any) *sql.Row {
	return d.inner.QueryRow(query, args...)
}

func (d *queryFailDB) Query(query string, args ...any) (*sql.Rows, error) {
	return nil, errSimulatedQuery
}

func TestQueryErrorsSurface(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{name: "overview query failure", path: "/api/v1/overview"},
		{name: "costs query failure", path: "/api/v1/costs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ro := openTestDB(t)
			h := api.New(&queryFailDB{inner: ro})
			code, body := getJSON(t, h, tc.path)
			if code < 400 {
				t.Fatalf("want non-2xx on query failure, got %d body=%v", code, body)
			}
			if body["error"] == nil || body["error"] == "" {
				t.Fatalf("want JSON error body, got %v", body)
			}
		})
	}
}

func TestEmptyDatabaseZeros(t *testing.T) {
	cases := []struct {
		name string
		path string
		zero func(t *testing.T, body map[string]any)
	}{
		{
			name: "overview empty",
			path: "/api/v1/overview",
			zero: func(t *testing.T, body map[string]any) {
				t.Helper()
				if body["sessions_count"] != float64(0) {
					t.Errorf("sessions_count=%v, want 0", body["sessions_count"])
				}
				if body["total_cost_usd"] != float64(0) {
					t.Errorf("total_cost_usd=%v, want 0", body["total_cost_usd"])
				}
				daily, _ := body["daily_costs"].([]any)
				if daily == nil {
					t.Fatalf("daily_costs missing: %v", body)
				}
				if len(daily) != 0 {
					t.Errorf("daily_costs len=%d, want 0", len(daily))
				}
			},
		},
		{
			name: "costs empty",
			path: "/api/v1/costs",
			zero: func(t *testing.T, body map[string]any) {
				t.Helper()
				for _, key := range []string{"daily", "by_model", "top_sessions"} {
					arr, _ := body[key].([]any)
					if arr == nil {
						t.Fatalf("%s missing: %v", key, body)
					}
					if len(arr) != 0 {
						t.Errorf("%s len=%d, want 0", key, len(arr))
					}
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ro := openTestDB(t)
			h := api.New(ro)
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("want 200 on empty db, got %d body=%s", w.Code, w.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("non-JSON: %s", w.Body.String())
			}
			tc.zero(t, body)
		})
	}
}

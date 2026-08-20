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
		{name: "models query failure", path: "/api/v1/models"},
		// Both history series paths: the roll-up union and the raw-span one.
		{name: "history union query failure", path: "/api/v1/history"},
		{name: "history raw query failure", path: "/api/v1/history?granularity=hour"},
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

// queryRowFailDB is the mirror of queryFailDB: every QueryRow fails while Query
// keeps working, covering the single-row handlers.
type queryRowFailDB struct {
	inner api.DB
}

func (d *queryRowFailDB) QueryRow(query string, args ...any) *sql.Row {
	return d.inner.QueryRow("SELECT this_column_does_not_exist")
}

func (d *queryRowFailDB) Query(query string, args ...any) (*sql.Rows, error) {
	return d.inner.Query(query, args...)
}

func TestQueryRowErrorsSurface(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{name: "health", path: "/api/v1/health"},
		{name: "sessions count", path: "/api/v1/sessions"},
		{name: "tools total recount", path: "/api/v1/tools"},
		// A broken DB must not masquerade as a missing session.
		{name: "session detail", path: "/api/v1/sessions/some-session-id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ro := openTestDB(t)
			h := api.New(&queryRowFailDB{inner: ro})
			code, body := getJSON(t, h, tc.path)
			if code != http.StatusInternalServerError {
				t.Fatalf("want 500 on QueryRow failure, got %d body=%v", code, body)
			}
			if body["error"] == nil || body["error"] == "" {
				t.Fatalf("want JSON error body, got %v", body)
			}
		})
	}
}

func TestSessionDetailNotFoundStaysNotFound(t *testing.T) {
	_, ro := openTestDB(t)
	h := api.New(ro)
	code, body := getJSON(t, h, "/api/v1/sessions/no-such-session")
	if code != http.StatusNotFound {
		t.Fatalf("want 404 for a missing session, got %d body=%v", code, body)
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

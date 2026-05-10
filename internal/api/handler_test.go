package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Flopsstuff/cotel/internal/api"
	"github.com/Flopsstuff/cotel/internal/storage"
)

// openTestDB opens an in-memory DuckDB and returns the read-only view.
func openTestDB(t *testing.T) (*storage.DB, *storage.ReadDB) {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, db.ReadOnly()
}

// insertSpan inserts a span with sensible defaults.
func insertSpan(t *testing.T, db *storage.DB, s storage.Span) {
	t.Helper()
	if err := db.InsertSpan(s); err != nil {
		t.Fatalf("insertSpan: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }

// ---- helpers ----

func getJSON(t *testing.T, h http.Handler, path string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("GET %s: non-JSON body: %s", path, w.Body.String())
	}
	return w.Code, body
}

// ---- tests ----

func TestHealth(t *testing.T) {
	cases := []struct {
		name          string
		seed          func(*testing.T, *storage.DB)
		publicURL     string
		wantOK        bool
		wantSpans     float64
		wantPublicURL string
	}{
		{
			name:      "empty db",
			seed:      func(*testing.T, *storage.DB) {},
			wantOK:    true,
			wantSpans: 0,
		},
		{
			name: "one span",
			seed: func(t *testing.T, db *storage.DB) {
				insertSpan(t, db, storage.Span{
					TraceID: "t1", SpanID: "s1",
					Name: "test", SessionID: "sess1",
					StartTime: time.Now(), EndTime: time.Now().Add(time.Second),
				})
			},
			wantOK:    true,
			wantSpans: 1,
		},
		{
			name:          "public ingest URL set",
			seed:          func(*testing.T, *storage.DB) {},
			publicURL:     "https://otlp.example.com",
			wantOK:        true,
			wantSpans:     0,
			wantPublicURL: "https://otlp.example.com",
		},
		{
			name:          "public ingest URL unset returns no field",
			seed:          func(*testing.T, *storage.DB) {},
			publicURL:     "",
			wantOK:        true,
			wantSpans:     0,
			wantPublicURL: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, ro := openTestDB(t)
			tc.seed(t, db)
			h := api.New(ro).SetPublicIngestURL(tc.publicURL)
			code, body := getJSON(t, h, "/api/v1/health")
			if code != http.StatusOK {
				t.Fatalf("want 200, got %d", code)
			}
			if body["status"] != "ok" {
				t.Errorf("want status=ok, got %v", body["status"])
			}
			if body["span_count"] != tc.wantSpans {
				t.Errorf("want span_count=%v, got %v", tc.wantSpans, body["span_count"])
			}
			got, _ := body["public_ingest_url"].(string)
			if got != tc.wantPublicURL {
				t.Errorf("want public_ingest_url=%q, got %q", tc.wantPublicURL, got)
			}
		})
	}
}

func TestOverview(t *testing.T) {
	cases := []struct {
		name       string
		seed       func(*testing.T, *storage.DB)
		wantStatus int
		checkBody  func(*testing.T, map[string]any)
	}{
		{
			name:       "empty db",
			seed:       func(*testing.T, *storage.DB) {},
			wantStatus: http.StatusOK,
			checkBody: func(t *testing.T, body map[string]any) {
				if body["sessions_count"] != float64(0) {
					t.Errorf("want sessions_count=0, got %v", body["sessions_count"])
				}
				if _, ok := body["daily_costs"]; !ok {
					t.Error("missing daily_costs key")
				}
				if _, ok := body["top_models"]; !ok {
					t.Error("missing top_models key")
				}
				if _, ok := body["top_tools"]; !ok {
					t.Error("missing top_tools key")
				}
			},
		},
		{
			name: "with data",
			seed: func(t *testing.T, db *storage.DB) {
				now := time.Now()
				for i := 0; i < 3; i++ {
					insertSpan(t, db, storage.Span{
						TraceID: "t1", SpanID: "s" + strings.Repeat("x", i+1),
						Name: "llm", SessionID: "sess1", Model: "claude-3",
						ToolName:    "",
						StartTime:   now, EndTime: now.Add(time.Second),
						CostUSD:     ptr(0.01),
						InputTokens: ptr(int64(100)),
					})
				}
				insertSpan(t, db, storage.Span{
					TraceID: "t2", SpanID: "stool",
					Name: "tool", SessionID: "sess2",
					ToolName:  "Bash",
					StartTime: now, EndTime: now.Add(time.Second),
				})
			},
			wantStatus: http.StatusOK,
			checkBody: func(t *testing.T, body map[string]any) {
				if body["sessions_count"] != float64(2) {
					t.Errorf("want sessions_count=2, got %v", body["sessions_count"])
				}
				topModels, ok := body["top_models"].([]any)
				if !ok || len(topModels) == 0 {
					t.Error("expected non-empty top_models")
				}
				topTools, ok := body["top_tools"].([]any)
				if !ok || len(topTools) == 0 {
					t.Error("expected non-empty top_tools")
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, ro := openTestDB(t)
			tc.seed(t, db)
			h := api.New(ro)
			code, body := getJSON(t, h, "/api/v1/overview")
			if code != tc.wantStatus {
				t.Fatalf("want %d, got %d body=%v", tc.wantStatus, code, body)
			}
			tc.checkBody(t, body)
		})
	}
}

func TestSessions(t *testing.T) {
	cases := []struct {
		name       string
		seed       func(*testing.T, *storage.DB)
		path       string
		wantStatus int
		checkBody  func(*testing.T, map[string]any)
	}{
		{
			name:       "empty db",
			seed:       func(*testing.T, *storage.DB) {},
			path:       "/api/v1/sessions",
			wantStatus: http.StatusOK,
			checkBody: func(t *testing.T, body map[string]any) {
				if body["total"] != float64(0) {
					t.Errorf("want total=0, got %v", body["total"])
				}
				items, ok := body["items"].([]any)
				if !ok || len(items) != 0 {
					t.Errorf("want empty items, got %v", body["items"])
				}
			},
		},
		{
			name: "pagination params",
			seed: func(t *testing.T, db *storage.DB) {
				now := time.Now()
				insertSpan(t, db, storage.Span{
					TraceID: "t1", SpanID: "s1", Name: "llm", SessionID: "sess1",
					StartTime: now, EndTime: now.Add(time.Second),
				})
			},
			path:       "/api/v1/sessions?page=1&limit=10",
			wantStatus: http.StatusOK,
			checkBody: func(t *testing.T, body map[string]any) {
				if body["page"] != float64(1) {
					t.Errorf("want page=1, got %v", body["page"])
				}
				if body["limit"] != float64(10) {
					t.Errorf("want limit=10, got %v", body["limit"])
				}
				if body["total"] != float64(1) {
					t.Errorf("want total=1, got %v", body["total"])
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, ro := openTestDB(t)
			tc.seed(t, db)
			h := api.New(ro)
			code, body := getJSON(t, h, tc.path)
			if code != tc.wantStatus {
				t.Fatalf("want %d, got %d body=%v", tc.wantStatus, code, body)
			}
			tc.checkBody(t, body)
		})
	}
}

func TestSessionDetail(t *testing.T) {
	cases := []struct {
		name       string
		seed       func(*testing.T, *storage.DB)
		sessionID  string
		wantStatus int
		checkBody  func(*testing.T, map[string]any)
	}{
		{
			name:       "not found",
			seed:       func(*testing.T, *storage.DB) {},
			sessionID:  "no-such-session",
			wantStatus: http.StatusNotFound,
			checkBody: func(t *testing.T, body map[string]any) {
				if _, ok := body["error"]; !ok {
					t.Error("missing error key in 404 response")
				}
			},
		},
		{
			name: "found",
			seed: func(t *testing.T, db *storage.DB) {
				now := time.Now()
				insertSpan(t, db, storage.Span{
					TraceID: "t1", SpanID: "s1", Name: "llm", SessionID: "mysess",
					Model: "claude-3", StartTime: now, EndTime: now.Add(time.Second),
					CostUSD: ptr(0.05), InputTokens: ptr(int64(200)),
				})
			},
			sessionID:  "mysess",
			wantStatus: http.StatusOK,
			checkBody: func(t *testing.T, body map[string]any) {
				if body["session_id"] != "mysess" {
					t.Errorf("want session_id=mysess, got %v", body["session_id"])
				}
				spans, ok := body["spans"].([]any)
				if !ok || len(spans) == 0 {
					t.Error("expected non-empty spans")
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, ro := openTestDB(t)
			tc.seed(t, db)
			h := api.New(ro)
			code, body := getJSON(t, h, "/api/v1/sessions/"+tc.sessionID)
			if code != tc.wantStatus {
				t.Fatalf("want %d, got %d body=%v", tc.wantStatus, code, body)
			}
			tc.checkBody(t, body)
		})
	}
}

func TestCosts(t *testing.T) {
	cases := []struct {
		name       string
		seed       func(*testing.T, *storage.DB)
		path       string
		wantStatus int
		checkBody  func(*testing.T, map[string]any)
	}{
		{
			name:       "empty db",
			seed:       func(*testing.T, *storage.DB) {},
			path:       "/api/v1/costs",
			wantStatus: http.StatusOK,
			checkBody: func(t *testing.T, body map[string]any) {
				daily := body["daily"].([]any)
				if len(daily) != 0 {
					t.Errorf("want empty daily, got %v", daily)
				}
			},
		},
		{
			name: "with date range",
			seed: func(t *testing.T, db *storage.DB) {
				now := time.Now()
				insertSpan(t, db, storage.Span{
					TraceID: "t1", SpanID: "s1", Name: "llm", SessionID: "sess1",
					Model: "claude-3", StartTime: now, EndTime: now.Add(time.Second),
					CostUSD: ptr(0.01),
				})
			},
			path:       "/api/v1/costs?from=2020-01-01&to=2099-12-31",
			wantStatus: http.StatusOK,
			checkBody: func(t *testing.T, body map[string]any) {
				if _, ok := body["daily"]; !ok {
					t.Error("missing daily key")
				}
				if _, ok := body["by_model"]; !ok {
					t.Error("missing by_model key")
				}
				if _, ok := body["top_sessions"]; !ok {
					t.Error("missing top_sessions key")
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, ro := openTestDB(t)
			tc.seed(t, db)
			h := api.New(ro)
			code, body := getJSON(t, h, tc.path)
			if code != tc.wantStatus {
				t.Fatalf("want %d, got %d body=%v", tc.wantStatus, code, body)
			}
			tc.checkBody(t, body)
		})
	}
}

func TestTools(t *testing.T) {
	cases := []struct {
		name       string
		seed       func(*testing.T, *storage.DB)
		wantStatus int
		checkBody  func(*testing.T, map[string]any)
	}{
		{
			name:       "empty db",
			seed:       func(*testing.T, *storage.DB) {},
			wantStatus: http.StatusOK,
			checkBody: func(t *testing.T, body map[string]any) {
				items := body["items"].([]any)
				if len(items) != 0 {
					t.Errorf("want empty items, got %v", items)
				}
			},
		},
		{
			name: "with tools",
			seed: func(t *testing.T, db *storage.DB) {
				now := time.Now()
				for i := 0; i < 3; i++ {
					insertSpan(t, db, storage.Span{
						TraceID: "t1", SpanID: "stool" + strconv.Itoa(i),
						Name: "tool", SessionID: "sess1", ToolName: "Bash",
						StartTime: now, EndTime: now.Add(100 * time.Millisecond),
					})
				}
			},
			wantStatus: http.StatusOK,
			checkBody: func(t *testing.T, body map[string]any) {
				items := body["items"].([]any)
				if len(items) == 0 {
					t.Error("expected non-empty items")
				}
				first := items[0].(map[string]any)
				if first["name"] != "Bash" {
					t.Errorf("want name=Bash, got %v", first["name"])
				}
				if first["calls"] != float64(3) {
					t.Errorf("want calls=3, got %v", first["calls"])
				}
			},
		},
		{
			name: "spans without tool_name are excluded",
			seed: func(t *testing.T, db *storage.DB) {
				now := time.Now()
				// Span with empty tool_name (zero value — no tool attribute in original trace).
				insertSpan(t, db, storage.Span{
					TraceID: "t1", SpanID: "s1", Name: "interaction", SessionID: "sess1",
					ToolName: "", StartTime: now, EndTime: now.Add(time.Second),
				})
			},
			wantStatus: http.StatusOK,
			checkBody: func(t *testing.T, body map[string]any) {
				items := body["items"].([]any)
				if len(items) != 0 {
					t.Errorf("want empty items (empty-tool_name spans excluded), got %v", items)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, ro := openTestDB(t)
			tc.seed(t, db)
			h := api.New(ro)
			code, body := getJSON(t, h, "/api/v1/tools")
			if code != tc.wantStatus {
				t.Fatalf("want %d, got %d body=%v", tc.wantStatus, code, body)
			}
			tc.checkBody(t, body)
		})
	}
}

func TestModels(t *testing.T) {
	cases := []struct {
		name       string
		seed       func(*testing.T, *storage.DB)
		wantStatus int
		checkBody  func(*testing.T, map[string]any)
	}{
		{
			name:       "empty db",
			seed:       func(*testing.T, *storage.DB) {},
			wantStatus: http.StatusOK,
			checkBody: func(t *testing.T, body map[string]any) {
				items := body["items"].([]any)
				if len(items) != 0 {
					t.Errorf("want empty items, got %v", items)
				}
			},
		},
		{
			name: "with models",
			seed: func(t *testing.T, db *storage.DB) {
				now := time.Now()
				insertSpan(t, db, storage.Span{
					TraceID: "t1", SpanID: "s1", Name: "llm", SessionID: "sess1",
					Model: "claude-3-sonnet", StartTime: now, EndTime: now.Add(time.Second),
					CostUSD: ptr(0.02), InputTokens: ptr(int64(500)), OutputTokens: ptr(int64(200)),
				})
			},
			wantStatus: http.StatusOK,
			checkBody: func(t *testing.T, body map[string]any) {
				items := body["items"].([]any)
				if len(items) == 0 {
					t.Error("expected non-empty items")
				}
				first := items[0].(map[string]any)
				if first["model"] != "claude-3-sonnet" {
					t.Errorf("want model=claude-3-sonnet, got %v", first["model"])
				}
			},
		},
		{
			name: "spans without model are excluded",
			seed: func(t *testing.T, db *storage.DB) {
				now := time.Now()
				// Span with empty model (zero value — no model attribute in original trace).
				insertSpan(t, db, storage.Span{
					TraceID: "t1", SpanID: "s1", Name: "tool", SessionID: "sess1",
					Model: "", StartTime: now, EndTime: now.Add(time.Second),
				})
			},
			wantStatus: http.StatusOK,
			checkBody: func(t *testing.T, body map[string]any) {
				items := body["items"].([]any)
				if len(items) != 0 {
					t.Errorf("want empty items (empty-model spans excluded), got %v", items)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, ro := openTestDB(t)
			tc.seed(t, db)
			h := api.New(ro)
			code, body := getJSON(t, h, "/api/v1/models")
			if code != tc.wantStatus {
				t.Fatalf("want %d, got %d body=%v", tc.wantStatus, code, body)
			}
			tc.checkBody(t, body)
		})
	}
}

func TestNotFound(t *testing.T) {
	_, ro := openTestDB(t)
	h := api.New(ro)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/nonexistent", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

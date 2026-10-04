package dashboard_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Flopsstuff/cotel/internal/dashboard"
	"github.com/Flopsstuff/cotel/internal/storage"
)

func openTestDBRW(t *testing.T) (*storage.DB, *storage.ReadDB) {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, db.ReadOnly()
}

func getHealthz(t *testing.T, h http.Handler) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("GET /healthz: non-JSON body: %s", w.Body.String())
	}
	return w.Code, body
}

func TestHealthzEmptyDBHasNullIngestAge(t *testing.T) {
	_, ro := openTestDBRW(t)
	code, body := getHealthz(t, dashboard.New(ro))

	if code != http.StatusOK {
		t.Fatalf("want 200, got %d", code)
	}
	if body["ok"] != true {
		t.Errorf("want ok=true, got %v", body["ok"])
	}
	if body["spans"] != float64(0) {
		t.Errorf("want spans=0, got %v", body["spans"])
	}
	for _, key := range []string{"last_ingest_at", "newest_span_age_seconds"} {
		v, present := body[key]
		if !present {
			t.Errorf("%s: field missing; an empty DB must publish it as null", key)
		}
		if v != nil {
			t.Errorf("%s: want null on an empty DB, got %v", key, v)
		}
	}
}

func TestHealthzFreshSpanIsYoung(t *testing.T) {
	db, ro := openTestDBRW(t)
	if err := db.InsertSpan(storage.Span{
		TraceID: "t1", SpanID: "s1", Name: "test", SessionID: "sess1",
		// An old start_time must not read as staleness: freshness is the
		// ingest time, not when the work happened.
		StartTime: time.Now().Add(-72 * time.Hour),
		EndTime:   time.Now().Add(-72 * time.Hour).Add(time.Second),
	}); err != nil {
		t.Fatalf("insert span: %v", err)
	}

	code, body := getHealthz(t, dashboard.New(ro))
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d", code)
	}
	if body["spans"] != float64(1) {
		t.Errorf("want spans=1, got %v", body["spans"])
	}
	if body["last_ingest_at"] == nil {
		t.Fatalf("want a last_ingest_at timestamp, got null")
	}
	age, ok := body["newest_span_age_seconds"].(float64)
	if !ok {
		t.Fatalf("want numeric newest_span_age_seconds, got %v", body["newest_span_age_seconds"])
	}
	if age < 0 || age > 60 {
		t.Errorf("a just-ingested span should be seconds old, got %v", age)
	}
}

// An archive import carries the original ingested_at, so replaying one must not
// make a dead instance look like it is accepting traffic again.
func TestHealthzImportedSpanStaysStale(t *testing.T) {
	db, ro := openTestDBRW(t)
	ingested := time.Now().Add(-6 * 24 * time.Hour)
	n, err := db.ImportSpans([]storage.Span{{
		TraceID: "t1", SpanID: "s1", Name: "imported", SessionID: "sess1",
		StartTime: ingested, EndTime: ingested.Add(time.Second),
		IngestedAt: ingested,
	}})
	if err != nil || n != 1 {
		t.Fatalf("ImportSpans: n=%d err=%v", n, err)
	}

	_, body := getHealthz(t, dashboard.New(ro))
	age, ok := body["newest_span_age_seconds"].(float64)
	if !ok {
		t.Fatalf("want numeric newest_span_age_seconds, got %v", body["newest_span_age_seconds"])
	}
	if age < 5*24*3600 {
		t.Errorf("imported span must keep its original ingest age, got %v seconds", age)
	}
}

// brokenDB fails every single-row read, standing in for an unreadable database.
type brokenDB struct {
	inner dashboard.DB
}

func (d *brokenDB) QueryRow(query string, args ...any) *sql.Row {
	return d.inner.QueryRow("SELECT this_column_does_not_exist")
}

func (d *brokenDB) Query(query string, args ...any) (*sql.Rows, error) {
	return d.inner.Query(query, args...)
}

func TestHealthzFailedQueryIsNotOK(t *testing.T) {
	_, ro := openTestDBRW(t)
	code, body := getHealthz(t, dashboard.New(&brokenDB{inner: ro}))

	if body["ok"] != false {
		t.Errorf("a failed query must report ok=false, got %v (body %v)", body["ok"], body)
	}
	if body["spans"] != float64(0) {
		t.Errorf("want spans=0 alongside ok=false, got %v", body["spans"])
	}
	if code == http.StatusOK {
		t.Errorf("a failed query must not answer 200, got %d", code)
	}
}

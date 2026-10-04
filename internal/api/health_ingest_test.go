package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/Flopsstuff/cotel/internal/api"
	"github.com/Flopsstuff/cotel/internal/storage"
)

// TestHealthIngestFreshness covers the ingest-age fields on /api/v1/health: an
// empty DB publishes nulls, a just-stored span is seconds old even when its
// start_time is ancient, and an imported archive keeps its original age.
func TestHealthIngestFreshness(t *testing.T) {
	t.Run("empty db reports null", func(t *testing.T) {
		_, ro := openTestDB(t)
		code, body := getJSON(t, api.New(ro), "/api/v1/health")
		if code != http.StatusOK {
			t.Fatalf("want 200, got %d", code)
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
	})

	t.Run("fresh span is young despite an old start_time", func(t *testing.T) {
		db, ro := openTestDB(t)
		old := time.Now().Add(-72 * time.Hour)
		insertSpan(t, db, storage.Span{
			TraceID: "t1", SpanID: "s1", Name: "test", SessionID: "sess1",
			StartTime: old, EndTime: old.Add(time.Second),
		})
		_, body := getJSON(t, api.New(ro), "/api/v1/health")
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
	})

	t.Run("imported span keeps its original ingest age", func(t *testing.T) {
		db, ro := openTestDB(t)
		ingested := time.Now().Add(-6 * 24 * time.Hour)
		n, err := db.ImportSpans([]storage.Span{{
			TraceID: "t1", SpanID: "s1", Name: "imported", SessionID: "sess1",
			StartTime: ingested, EndTime: ingested.Add(time.Second),
			IngestedAt: ingested,
		}})
		if err != nil || n != 1 {
			t.Fatalf("ImportSpans: n=%d err=%v", n, err)
		}
		_, body := getJSON(t, api.New(ro), "/api/v1/health")
		age, ok := body["newest_span_age_seconds"].(float64)
		if !ok {
			t.Fatalf("want numeric newest_span_age_seconds, got %v", body["newest_span_age_seconds"])
		}
		if age < 5*24*3600 {
			t.Errorf("imported span must keep its original ingest age, got %v seconds", age)
		}
	})
}

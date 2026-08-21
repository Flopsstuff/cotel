package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/Flopsstuff/cotel/internal/api"
	"github.com/Flopsstuff/cotel/internal/storage"
)

// gridAnchor is a whole UTC hour three hours back: inside every sub-day window,
// and far enough from both ends of the day range that the fixture cannot drift
// out of it while the test runs.
func gridAnchor() time.Time {
	return time.Now().UTC().Truncate(time.Hour).Add(-3 * time.Hour)
}

// seedGridFixture puts two spans in one 10-minute bucket and a third in the
// next, all inside the same hour and so inside one 4-hour bucket.
func seedGridFixture(t *testing.T, db *storage.DB) {
	t.Helper()
	anchor := gridAnchor()
	for i, offset := range []time.Duration{0, 7 * time.Minute, 12 * time.Minute} {
		start := anchor.Add(offset)
		insertSpan(t, db, storage.Span{
			TraceID: "tr", SpanID: string(rune('a'+i)) + "-grid", Name: "llm",
			SessionID: "s-grid", Model: "sonnet", ToolName: "Bash", UserID: "alice",
			StartTime: start, EndTime: start.Add(time.Second),
			CostUSD: ptr(1.0), InputTokens: ptr(int64(10)), OutputTokens: ptr(int64(1)),
		})
	}
}

// TestHistory_TenMinuteGranularity is the bucket width the Overview activity
// grid draws one cell per on the day range. The labels are asserted in UTC:
// CAST(start_time AS TIMESTAMP) renders the stored TIMESTAMPTZ in UTC whatever
// the session timezone is, and the grid places cells on that basis.
func TestHistory_TenMinuteGranularity(t *testing.T) {
	db, ro := openTestDB(t)
	seedGridFixture(t, db)
	h := api.New(ro)

	code, body := getJSON(t, h, "/api/v1/history?granularity=10m&range=day")
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d: %v", code, body)
	}
	if body["granularity"] != "10m" {
		t.Fatalf("granularity echo: want 10m, got %v", body["granularity"])
	}

	anchor := gridAnchor()
	want := map[string]float64{
		anchor.Format("2006-01-02 15:04"):                    2,
		anchor.Add(10 * time.Minute).Format("2006-01-02 15:04"): 1,
	}
	got := pairs(t, body, "buckets", "bucket", "spans")
	if len(got) != len(want) {
		t.Fatalf("buckets: want %v, got %v", want, got)
	}
	for bucket, spans := range want {
		if got[bucket] != spans {
			t.Errorf("bucket %s: want %v spans, got %v — full map %v", bucket, spans, got[bucket], got)
		}
	}
}

// TestHistory_FourHourGranularity pins the month grid's cell: the three spans
// share one hour, so they land in one bucket, aligned to a multiple of four
// hours from UTC midnight rather than to the first span's own hour.
func TestHistory_FourHourGranularity(t *testing.T) {
	db, ro := openTestDB(t)
	seedGridFixture(t, db)
	h := api.New(ro)

	code, body := getJSON(t, h, "/api/v1/history?granularity=4h&range=month")
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d: %v", code, body)
	}
	if body["granularity"] != "4h" {
		t.Fatalf("granularity echo: want 4h, got %v", body["granularity"])
	}

	a := gridAnchor()
	slot := time.Date(a.Year(), a.Month(), a.Day(), a.Hour()/4*4, 0, 0, 0, time.UTC)
	got := pairs(t, body, "buckets", "bucket", "spans")
	if len(got) != 1 || got[slot.Format("2006-01-02 15:04")] != 3 {
		t.Errorf("buckets: want the 3 spans in %s alone, got %v", slot.Format("2006-01-02 15:04"), got)
	}
}

// TestHistory_SubDayGranularitiesStayRawOnly extends to the two new widths the
// line TestHistory_HourStaysRawOnly holds for hour: daily_usage buckets whole
// UTC days, so nothing below a day may be answered from it, and the response
// says where its coverage starts instead of relabelling day-shaped data.
func TestHistory_SubDayGranularitiesStayRawOnly(t *testing.T) {
	db, ro := openTestDB(t)
	seedRangeFixture(t, db)
	h := api.New(ro)

	for _, gran := range []string{"10m", "4h"} {
		t.Run(gran, func(t *testing.T) {
			_, body := getJSON(t, h, "/api/v1/history?granularity="+gran+"&range=all")
			if got := len(bucketList(t, body)); got != 2 {
				t.Errorf("buckets: want the 2 raw spans' buckets, got %d (%v)", got, body["buckets"])
			}
			if got := sumBuckets(t, body, "spans"); got != 2 {
				t.Errorf("spans: want 2 raw spans, got %v — aggregate rows leaked into a sub-day bucket", got)
			}
			if models := sumByModel(t, body); len(models) != 1 || models["sonnet"] != 2 {
				t.Errorf("by_model: want only the raw sonnet spans, got %v", models)
			}
			if body["covered_since"] == nil {
				t.Error("covered_since: want the raw floor on range=all, got null")
			}
		})
	}
}

// TestHistory_UnknownGranularityFallsBack keeps the fallback-don't-400 rule the
// range keys already follow: a width we do not serve resolves to day.
func TestHistory_UnknownGranularityFallsBack(t *testing.T) {
	db, ro := openTestDB(t)
	seedGridFixture(t, db)
	h := api.New(ro)

	for _, gran := range []string{"5m", "3h", ""} {
		code, body := getJSON(t, h, "/api/v1/history?granularity="+gran+"&range=day")
		if code != http.StatusOK {
			t.Fatalf("granularity=%q: want 200, got %d", gran, code)
		}
		if body["granularity"] != "day" {
			t.Errorf("granularity=%q: want the day fallback, got %v", gran, body["granularity"])
		}
	}
}

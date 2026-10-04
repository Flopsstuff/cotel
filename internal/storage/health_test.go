package storage_test

import (
	"testing"
	"time"

	"github.com/Flopsstuff/cotel/internal/storage"
)

func TestIngestFreshnessNeverIngested(t *testing.T) {
	var f storage.IngestFreshness
	if ts := f.Timestamp(); ts != nil {
		t.Errorf("want nil timestamp before the first ingest, got %q", *ts)
	}
	if age := f.AgeSeconds(time.Now()); age != nil {
		t.Errorf("want nil age before the first ingest, got %d", *age)
	}
}

func TestIngestFreshnessAgeSeconds(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		last time.Time
		want int64
	}{
		{name: "two minutes ago", last: now.Add(-2 * time.Minute), want: 120},
		{name: "six days ago", last: now.Add(-6 * 24 * time.Hour), want: 6 * 24 * 3600},
		{name: "clock skew clamps to zero", last: now.Add(time.Hour), want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := storage.IngestFreshness{LastIngestAt: tc.last}
			age := f.AgeSeconds(now)
			if age == nil {
				t.Fatalf("want an age, got nil")
			}
			if *age != tc.want {
				t.Errorf("want %d seconds, got %d", tc.want, *age)
			}
		})
	}
}

func TestQueryIngestFreshnessEmptyTable(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	defer db.Close()

	f, err := storage.QueryIngestFreshness(db.ReadOnly())
	if err != nil {
		t.Fatalf("QueryIngestFreshness on an empty table must not error: %v", err)
	}
	if f.SpanCount != 0 {
		t.Errorf("want SpanCount=0, got %d", f.SpanCount)
	}
	if !f.LastIngestAt.IsZero() {
		t.Errorf("want a zero LastIngestAt, got %v", f.LastIngestAt)
	}
}

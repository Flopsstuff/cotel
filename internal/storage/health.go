package storage

import (
	"database/sql"
	"errors"
	"time"
)

// RowQuerier is the single-row read surface the ingest-freshness probe needs,
// so both the dashboard and the JSON API can run it over their own handle.
type RowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

// IngestFreshness answers one question: when did this instance last accept
// anything? It is measured from ingested_at rather than start_time, so
// importing an archive does not read as fresh traffic (the import carries the
// original ingested_at) and a late-arriving span with an old start_time does
// not read as staleness.
type IngestFreshness struct {
	SpanCount int64
	// LastIngestAt is zero when no span has ever been stored.
	LastIngestAt time.Time
}

// QueryIngestFreshness reads the span count and the newest ingest timestamp in
// one round trip. An empty table is not an error: it yields a zero SpanCount
// and a zero LastIngestAt.
func QueryIngestFreshness(db RowQuerier) (IngestFreshness, error) {
	var f IngestFreshness
	var last sql.NullTime
	err := db.QueryRow("SELECT COUNT(*), max(ingested_at) FROM spans").Scan(&f.SpanCount, &last)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return IngestFreshness{}, err
	}
	if last.Valid {
		f.LastIngestAt = last.Time
	}
	return f, nil
}

// Timestamp renders the newest ingest time for a JSON body, or nil when
// nothing has ever been ingested — the caller must publish that as null rather
// than as a zero age, which would read as "just ingested".
func (f IngestFreshness) Timestamp() *string {
	if f.LastIngestAt.IsZero() {
		return nil
	}
	s := f.LastIngestAt.UTC().Format(time.RFC3339Nano)
	return &s
}

// AgeSeconds reports how long ago the newest span was accepted, or nil when
// nothing has ever been ingested. An ingested_at ahead of now (clock skew, or
// an archive written by a machine running fast) clamps to 0 so the published
// contract stays non-negative.
func (f IngestFreshness) AgeSeconds(now time.Time) *int64 {
	if f.LastIngestAt.IsZero() {
		return nil
	}
	age := int64(now.Sub(f.LastIngestAt).Seconds())
	if age < 0 {
		age = 0
	}
	return &age
}

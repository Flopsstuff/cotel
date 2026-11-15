package storage

import (
	"testing"
	"time"
)

func insertPurgeSpan(t *testing.T, db *DB, spanID, svc, name, session, attrs string) {
	t.Helper()
	now := time.Now()
	_, err := db.rw.Exec(`
		INSERT INTO spans (trace_id, span_id, name, start_time, end_time, service_name, session_id, attributes)
		VALUES ('t1', ?, ?, ?, ?, ?, ?, ?)`,
		spanID, name, now, now.Add(time.Millisecond), svc, session, attrs)
	if err != nil {
		t.Fatalf("insert %s: %v", spanID, err)
	}
}

// dropNoise stands in for ingest.WouldDrop, which storage cannot import: it
// drops service "noisy" spans unless their attributes name a conversation.
func dropNoise(svc, _ string, attrs map[string]any) bool {
	return svc == "noisy" && attrs["conversation.id"] == nil
}

func TestPurgeSpans(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	insertPurgeSpan(t, db, "a", "noisy", "receiving", "", `{"thread.id":5}`)
	insertPurgeSpan(t, db, "b", "noisy", "receiving", "", `{}`)
	insertPurgeSpan(t, db, "c", "noisy", "fs.read_file", "", `{}`)
	insertPurgeSpan(t, db, "d", "noisy", "exec", "", `{"conversation.id":"x"}`) // drop says keep
	insertPurgeSpan(t, db, "e", "noisy", "receiving", "sess-1", `{}`)           // has a session: never a candidate
	insertPurgeSpan(t, db, "f", "quiet", "anything", "", `{}`)

	count := func() int64 {
		var n int64
		if err := db.rw.QueryRow(`SELECT count(*) FROM spans`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	rep, err := db.PurgeSpans(dropNoise, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Total != 3 || count() != 6 {
		t.Fatalf("dry run: total %d, stored %d; want 3 matched and nothing deleted", rep.Total, count())
	}
	if len(rep.Rows) != 2 || rep.Rows[0] != (PurgeRow{"noisy", "receiving", 2}) {
		t.Errorf("dry run rows: %+v", rep.Rows)
	}

	if rep, err = db.PurgeSpans(dropNoise, true); err != nil {
		t.Fatal(err)
	}
	if rep.Total != 3 || count() != 3 {
		t.Fatalf("apply: total %d, stored %d; want 3 deleted, 3 left", rep.Total, count())
	}
	rows, err := db.rw.Query(`SELECT span_id FROM spans ORDER BY span_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var left string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		left += id
	}
	if left != "def" {
		t.Errorf("spans left: %q, want \"def\"", left)
	}

	// A second apply finds nothing and does not fail on the temp table.
	if rep, err = db.PurgeSpans(dropNoise, true); err != nil || rep.Total != 0 {
		t.Errorf("second apply: total %d, err %v", rep.Total, err)
	}
}

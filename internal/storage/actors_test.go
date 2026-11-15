package storage

import (
	"errors"
	"testing"
	"time"
)

func openTestActorDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func mustCreateTestActor(t *testing.T, db *DB, name string) Actor {
	t.Helper()
	u, err := db.CreateActor(name)
	if err != nil {
		t.Fatalf("CreateActor(%q): %v", name, err)
	}
	return u
}

func insertTestSpan(t *testing.T, db *DB, spanID, actorID string) {
	t.Helper()
	now := time.Now()
	if err := db.InsertSpan(Span{
		TraceID: "trace1", SpanID: spanID, Name: "llm", SessionID: "sess1",
		ActorID: actorID, StartTime: now, EndTime: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("InsertSpan: %v", err)
	}
}

func actorInTestList(t *testing.T, db *DB, name string) bool {
	t.Helper()
	actors, _, err := db.ListActorsPage(ListActorsOptions{})
	if err != nil {
		t.Fatalf("ListActorsPage: %v", err)
	}
	for _, u := range actors {
		if u.Name == name {
			return true
		}
	}
	return false
}

func queryInt64(t *testing.T, db *DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.rw.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return n
}

func queryString(t *testing.T, db *DB, query string, args ...any) string {
	t.Helper()
	var s string
	if err := db.rw.QueryRow(query, args...).Scan(&s); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return s
}

// TestSoftDeleteActor_ActorOnly verifies mode-A (soft delete) semantics:
// actor disappears from listing, original token rejected, spans intact.
func TestSoftDeleteActor_ActorOnly(t *testing.T) {
	db := openTestActorDB(t)
	u := mustCreateTestActor(t, db, "alice")
	insertTestSpan(t, db, "span-alice-1", "alice")

	if err := db.SoftDeleteActor(u.ID); err != nil {
		t.Fatalf("SoftDeleteActor: %v", err)
	}

	if actorInTestList(t, db, "alice") {
		t.Error("alice must not appear in actor list after soft-delete")
	}

	// Original live token must be rejected (GetActorByToken filters deleted_at IS NULL).
	got, err := db.GetActorByToken(u.Token)
	if err == nil || got != nil {
		t.Errorf("soft-deleted token must be rejected, got actor=%v err=%v", got, err)
	}

	// Spans must still reference alice (not anonymized in actor_only mode).
	n := queryInt64(t, db, `SELECT COUNT(*) FROM spans WHERE actor_id = 'alice'`)
	if n != 1 {
		t.Errorf("want 1 span for alice after soft-delete, got %d", n)
	}
}

// TestSoftDeleteActor_NoResurface verifies the schema backfill does not recreate a
// soft-deleted actor. The actor row still exists (deleted_at set), so the
// WHERE NOT EXISTS guard prevents a new row.
func TestSoftDeleteActor_NoResurface(t *testing.T) {
	db := openTestActorDB(t)
	u := mustCreateTestActor(t, db, "alice")
	insertTestSpan(t, db, "span-alice-2", "alice")

	if err := db.SoftDeleteActor(u.ID); err != nil {
		t.Fatalf("SoftDeleteActor: %v", err)
	}

	// Simulate the v4→v5 backfill that runs on every DB open.
	_, err := db.rw.Exec(`
		INSERT INTO actors (id, name, token)
		SELECT uuid() AS id, actor_id AS name,
		       'cotel_' || md5(uuid()::VARCHAR) || md5(uuid()::VARCHAR) AS token
		FROM (SELECT DISTINCT actor_id FROM spans WHERE actor_id IS NOT NULL AND actor_id <> '') t
		WHERE NOT EXISTS (SELECT 1 FROM actors u WHERE u.name = t.actor_id)
		  AND t.actor_id <> '[deleted]'
	`)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}

	if actorInTestList(t, db, "alice") {
		t.Error("alice resurfaces after backfill — soft-delete guard broken")
	}
}

// TestDeleteActorWithHistory removes the actor row and hard-deletes their spans and daily_usage.
func TestDeleteActorWithHistory(t *testing.T) {
	db := openTestActorDB(t)
	u := mustCreateTestActor(t, db, "bob")
	insertTestSpan(t, db, "span-bob-1", "bob")

	_, err := db.rw.Exec(`
		INSERT INTO daily_usage (day, session_id, model, tool_name, actor_id, span_count)
		VALUES (today(), 'sess1', '', '', 'bob', 1)
	`)
	if err != nil {
		t.Fatalf("insert daily_usage: %v", err)
	}

	if err := db.DeleteActorWithHistory(u.ID); err != nil {
		t.Fatalf("DeleteActorWithHistory: %v", err)
	}

	if actorInTestList(t, db, "bob") {
		t.Error("bob must not appear in actor list after DeleteActorWithHistory")
	}

	spanCount := queryInt64(t, db, `SELECT COUNT(*) FROM spans WHERE span_id = 'span-bob-1'`)
	if spanCount != 0 {
		t.Errorf("span must be deleted, got %d rows", spanCount)
	}

	duCount := queryInt64(t, db, `SELECT COUNT(*) FROM daily_usage WHERE session_id = 'sess1'`)
	if duCount != 0 {
		t.Errorf("daily_usage must be deleted, got %d rows", duCount)
	}
}

// TestDeleteActorWithHistory_NoResurface verifies that spans are gone and the backfill
// guard skips any residual '[deleted]' rows from older data.
func TestDeleteActorWithHistory_NoResurface(t *testing.T) {
	db := openTestActorDB(t)
	u := mustCreateTestActor(t, db, "carol")
	insertTestSpan(t, db, "span-carol-1", "carol")

	if err := db.DeleteActorWithHistory(u.ID); err != nil {
		t.Fatalf("DeleteActorWithHistory: %v", err)
	}

	spanCount := queryInt64(t, db, `SELECT COUNT(*) FROM spans WHERE span_id = 'span-carol-1'`)
	if spanCount != 0 {
		t.Errorf("carol's span must be deleted, got %d rows", spanCount)
	}

	// Backfill guard: '[deleted]' sentinel must never resurface as a real actor.
	_, err := db.rw.Exec(`
		INSERT INTO actors (id, name, token)
		SELECT uuid() AS id, actor_id AS name,
		       'cotel_' || md5(uuid()::VARCHAR) || md5(uuid()::VARCHAR) AS token
		FROM (SELECT DISTINCT actor_id FROM spans WHERE actor_id IS NOT NULL AND actor_id <> '') t
		WHERE NOT EXISTS (SELECT 1 FROM actors u WHERE u.name = t.actor_id)
		  AND t.actor_id <> '[deleted]'
	`)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}

	if actorInTestList(t, db, "[deleted]") {
		t.Error("[deleted] sentinel must never become a real actor")
	}
}

// TestSoftDeleteActor_NotFound returns ErrNotFound for unknown or already-deleted actors.
func TestSoftDeleteActor_NotFound(t *testing.T) {
	db := openTestActorDB(t)

	if err := db.SoftDeleteActor("nonexistent-id"); !errors.Is(err, ErrNotFound) {
		t.Errorf("nonexistent actor: want ErrNotFound, got %v", err)
	}

	u := mustCreateTestActor(t, db, "dave")
	if err := db.SoftDeleteActor(u.ID); err != nil {
		t.Fatalf("first SoftDeleteActor: %v", err)
	}
	if err := db.SoftDeleteActor(u.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("double soft-delete: want ErrNotFound, got %v", err)
	}
}

// TestDeleteActorWithHistory_NotFound returns ErrNotFound for unknown actors.
func TestDeleteActorWithHistory_NotFound(t *testing.T) {
	db := openTestActorDB(t)
	if err := db.DeleteActorWithHistory("nonexistent-id"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

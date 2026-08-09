package storage

import (
	"errors"
	"testing"
	"time"
)

func openTestUserDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func mustCreateTestUser(t *testing.T, db *DB, name string) User {
	t.Helper()
	u, err := db.CreateUser(name)
	if err != nil {
		t.Fatalf("CreateUser(%q): %v", name, err)
	}
	return u
}

func insertTestSpan(t *testing.T, db *DB, spanID, userID string) {
	t.Helper()
	now := time.Now()
	if err := db.InsertSpan(Span{
		TraceID: "trace1", SpanID: spanID, Name: "llm", SessionID: "sess1",
		UserID: userID, StartTime: now, EndTime: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("InsertSpan: %v", err)
	}
}

func userInTestList(t *testing.T, db *DB, name string) bool {
	t.Helper()
	users, _, err := db.ListUsersPage(ListUsersOptions{})
	if err != nil {
		t.Fatalf("ListUsersPage: %v", err)
	}
	for _, u := range users {
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

// TestSoftDeleteUser_UserOnly verifies mode-A (soft delete) semantics:
// user disappears from listing, original token rejected, spans intact.
func TestSoftDeleteUser_UserOnly(t *testing.T) {
	db := openTestUserDB(t)
	u := mustCreateTestUser(t, db, "alice")
	insertTestSpan(t, db, "span-alice-1", "alice")

	if err := db.SoftDeleteUser(u.ID); err != nil {
		t.Fatalf("SoftDeleteUser: %v", err)
	}

	if userInTestList(t, db, "alice") {
		t.Error("alice must not appear in user list after soft-delete")
	}

	// Original live token must be rejected (GetUserByToken filters deleted_at IS NULL).
	got, err := db.GetUserByToken(u.Token)
	if err == nil || got != nil {
		t.Errorf("soft-deleted token must be rejected, got user=%v err=%v", got, err)
	}

	// Spans must still reference alice (not anonymized in user_only mode).
	n := queryInt64(t, db, `SELECT COUNT(*) FROM spans WHERE user_id = 'alice'`)
	if n != 1 {
		t.Errorf("want 1 span for alice after soft-delete, got %d", n)
	}
}

// TestSoftDeleteUser_NoResurface verifies the schema backfill does not recreate a
// soft-deleted user. The user row still exists (deleted_at set), so the
// WHERE NOT EXISTS guard prevents a new row.
func TestSoftDeleteUser_NoResurface(t *testing.T) {
	db := openTestUserDB(t)
	u := mustCreateTestUser(t, db, "alice")
	insertTestSpan(t, db, "span-alice-2", "alice")

	if err := db.SoftDeleteUser(u.ID); err != nil {
		t.Fatalf("SoftDeleteUser: %v", err)
	}

	// Simulate the v4→v5 backfill that runs on every DB open.
	_, err := db.rw.Exec(`
		INSERT INTO users (id, name, token)
		SELECT uuid() AS id, user_id AS name,
		       'cotel_' || md5(uuid()::VARCHAR) || md5(uuid()::VARCHAR) AS token
		FROM (SELECT DISTINCT user_id FROM spans WHERE user_id IS NOT NULL AND user_id <> '') t
		WHERE NOT EXISTS (SELECT 1 FROM users u WHERE u.name = t.user_id)
		  AND t.user_id <> '[deleted]'
	`)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}

	if userInTestList(t, db, "alice") {
		t.Error("alice resurfaces after backfill — soft-delete guard broken")
	}
}

// TestDeleteUserWithHistory removes the user row and hard-deletes their spans and daily_usage.
func TestDeleteUserWithHistory(t *testing.T) {
	db := openTestUserDB(t)
	u := mustCreateTestUser(t, db, "bob")
	insertTestSpan(t, db, "span-bob-1", "bob")

	_, err := db.rw.Exec(`
		INSERT INTO daily_usage (day, session_id, model, tool_name, user_id, span_count)
		VALUES (today(), 'sess1', '', '', 'bob', 1)
	`)
	if err != nil {
		t.Fatalf("insert daily_usage: %v", err)
	}

	if err := db.DeleteUserWithHistory(u.ID); err != nil {
		t.Fatalf("DeleteUserWithHistory: %v", err)
	}

	if userInTestList(t, db, "bob") {
		t.Error("bob must not appear in user list after DeleteUserWithHistory")
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

// TestDeleteUserWithHistory_NoResurface verifies that spans are gone and the backfill
// guard skips any residual '[deleted]' rows from older data.
func TestDeleteUserWithHistory_NoResurface(t *testing.T) {
	db := openTestUserDB(t)
	u := mustCreateTestUser(t, db, "carol")
	insertTestSpan(t, db, "span-carol-1", "carol")

	if err := db.DeleteUserWithHistory(u.ID); err != nil {
		t.Fatalf("DeleteUserWithHistory: %v", err)
	}

	spanCount := queryInt64(t, db, `SELECT COUNT(*) FROM spans WHERE span_id = 'span-carol-1'`)
	if spanCount != 0 {
		t.Errorf("carol's span must be deleted, got %d rows", spanCount)
	}

	// Backfill guard: '[deleted]' sentinel must never resurface as a real user.
	_, err := db.rw.Exec(`
		INSERT INTO users (id, name, token)
		SELECT uuid() AS id, user_id AS name,
		       'cotel_' || md5(uuid()::VARCHAR) || md5(uuid()::VARCHAR) AS token
		FROM (SELECT DISTINCT user_id FROM spans WHERE user_id IS NOT NULL AND user_id <> '') t
		WHERE NOT EXISTS (SELECT 1 FROM users u WHERE u.name = t.user_id)
		  AND t.user_id <> '[deleted]'
	`)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}

	if userInTestList(t, db, "[deleted]") {
		t.Error("[deleted] sentinel must never become a real user")
	}
}

// TestSoftDeleteUser_NotFound returns ErrNotFound for unknown or already-deleted users.
func TestSoftDeleteUser_NotFound(t *testing.T) {
	db := openTestUserDB(t)

	if err := db.SoftDeleteUser("nonexistent-id"); !errors.Is(err, ErrNotFound) {
		t.Errorf("nonexistent user: want ErrNotFound, got %v", err)
	}

	u := mustCreateTestUser(t, db, "dave")
	if err := db.SoftDeleteUser(u.ID); err != nil {
		t.Fatalf("first SoftDeleteUser: %v", err)
	}
	if err := db.SoftDeleteUser(u.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("double soft-delete: want ErrNotFound, got %v", err)
	}
}

// TestDeleteUserWithHistory_NotFound returns ErrNotFound for unknown users.
func TestDeleteUserWithHistory_NotFound(t *testing.T) {
	db := openTestUserDB(t)
	if err := db.DeleteUserWithHistory("nonexistent-id"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

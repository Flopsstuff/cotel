package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Flopsstuff/cotel/internal/api"
	"github.com/Flopsstuff/cotel/internal/api/auth"
	"github.com/Flopsstuff/cotel/internal/storage"
)

func newUserHandler(t *testing.T, db *storage.DB, ro *storage.ReadDB) http.Handler {
	t.Helper()
	return api.New(ro).SetUserStore(db)
}

func deleteUser(t *testing.T, h http.Handler, id, mode string) int {
	t.Helper()
	path := "/api/v1/users/" + id
	if mode != "" {
		path += "?mode=" + mode
	}
	req := httptest.NewRequest(http.MethodDelete, path, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Code
}

func createAPIUser(t *testing.T, db *storage.DB, name string) storage.User {
	t.Helper()
	u, err := db.CreateUser(name)
	if err != nil {
		t.Fatalf("CreateUser(%q): %v", name, err)
	}
	return u
}

func addAPISpan(t *testing.T, db *storage.DB, spanID, userID string) {
	t.Helper()
	now := time.Now()
	if err := db.InsertSpan(storage.Span{
		TraceID: "tr1", SpanID: spanID, Name: "llm", SessionID: "s1",
		UserID: userID, StartTime: now, EndTime: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("InsertSpan: %v", err)
	}
}

func listAPIUserIDs(t *testing.T, h http.Handler) map[string]bool {
	t.Helper()
	_, body := getJSON(t, h, "/api/v1/users")
	users, _ := body["users"].([]any)
	ids := make(map[string]bool)
	for _, u := range users {
		m, _ := u.(map[string]any)
		if id, ok := m["id"].(string); ok {
			ids[id] = true
		}
	}
	return ids
}

func tokenStatus(db *storage.DB, token string) int {
	h := auth.Middleware(db, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Code
}

// TestDeleteUser_ModeUserOnly verifies soft-delete: user gone from listing,
// token rejected, spans still reference the original name.
func TestDeleteUser_ModeUserOnly(t *testing.T) {
	db, ro := openTestDB(t)
	h := newUserHandler(t, db, ro)
	u := createAPIUser(t, db, "alice")
	addAPISpan(t, db, "sp-alice", "alice")

	if code := deleteUser(t, h, u.ID, "user_only"); code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", code)
	}
	if listAPIUserIDs(t, h)[u.ID] {
		t.Error("alice must not appear in user list after soft-delete")
	}
	if tokenStatus(db, u.Token) != http.StatusUnauthorized {
		t.Error("soft-deleted user's token must be rejected")
	}
}

// TestDeleteUser_ModeDefault confirms user_only is the default when mode is omitted.
func TestDeleteUser_ModeDefault(t *testing.T) {
	db, ro := openTestDB(t)
	h := newUserHandler(t, db, ro)
	u := createAPIUser(t, db, "bob")

	if code := deleteUser(t, h, u.ID, ""); code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", code)
	}
	if listAPIUserIDs(t, h)[u.ID] {
		t.Error("bob must not appear in list after default delete")
	}
}

// TestDeleteUser_ModeUserAndHistory verifies anonymization + hard-delete.
func TestDeleteUser_ModeUserAndHistory(t *testing.T) {
	db, ro := openTestDB(t)
	h := newUserHandler(t, db, ro)
	u := createAPIUser(t, db, "carol")
	addAPISpan(t, db, "sp-carol", "carol")

	if code := deleteUser(t, h, u.ID, "user_and_history"); code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", code)
	}
	if listAPIUserIDs(t, h)[u.ID] {
		t.Error("carol must not appear in list after user_and_history delete")
	}
}

// TestDeleteUser_NotFound returns 404 for an unknown user ID.
func TestDeleteUser_NotFound(t *testing.T) {
	db, ro := openTestDB(t)
	h := newUserHandler(t, db, ro)

	if code := deleteUser(t, h, "no-such-id", "user_only"); code != http.StatusNotFound {
		t.Errorf("want 404, got %d", code)
	}
}

// TestDeleteUser_AlreadyDeleted returns 404 on a second delete attempt.
func TestDeleteUser_AlreadyDeleted(t *testing.T) {
	db, ro := openTestDB(t)
	h := newUserHandler(t, db, ro)
	u := createAPIUser(t, db, "dave")

	deleteUser(t, h, u.ID, "user_only")
	if code := deleteUser(t, h, u.ID, "user_only"); code != http.StatusNotFound {
		t.Errorf("want 404 on double delete, got %d", code)
	}
}

// TestDeleteUser_InvalidMode returns 400 for an unrecognized mode.
func TestDeleteUser_InvalidMode(t *testing.T) {
	db, ro := openTestDB(t)
	h := newUserHandler(t, db, ro)
	u := createAPIUser(t, db, "eve")

	if code := deleteUser(t, h, u.ID, "nuke_everything"); code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", code)
	}
}

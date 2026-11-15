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

func newActorHandler(t *testing.T, db *storage.DB, ro *storage.ReadDB) http.Handler {
	t.Helper()
	return api.New(ro).SetActorStore(db)
}

func deleteActor(t *testing.T, h http.Handler, id, mode string) int {
	t.Helper()
	path := "/api/v1/actors/" + id
	if mode != "" {
		path += "?mode=" + mode
	}
	req := httptest.NewRequest(http.MethodDelete, path, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Code
}

func createAPIActor(t *testing.T, db *storage.DB, name string) storage.Actor {
	t.Helper()
	u, err := db.CreateActor(name)
	if err != nil {
		t.Fatalf("CreateActor(%q): %v", name, err)
	}
	return u
}

func addAPISpan(t *testing.T, db *storage.DB, spanID, actorID string) {
	t.Helper()
	now := time.Now()
	if err := db.InsertSpan(storage.Span{
		TraceID: "tr1", SpanID: spanID, Name: "llm", SessionID: "s1",
		ActorID: actorID, StartTime: now, EndTime: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("InsertSpan: %v", err)
	}
}

func listAPIActorIDs(t *testing.T, h http.Handler) map[string]bool {
	t.Helper()
	_, body := getJSON(t, h, "/api/v1/actors")
	actors, _ := body["actors"].([]any)
	ids := make(map[string]bool)
	for _, u := range actors {
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

// TestDeleteActor_ModeActorOnly verifies soft-delete: actor gone from listing,
// token rejected, spans still reference the original name.
func TestDeleteActor_ModeActorOnly(t *testing.T) {
	db, ro := openTestDB(t)
	h := newActorHandler(t, db, ro)
	u := createAPIActor(t, db, "alice")
	addAPISpan(t, db, "sp-alice", "alice")

	if code := deleteActor(t, h, u.ID, "actor_only"); code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", code)
	}
	if listAPIActorIDs(t, h)[u.ID] {
		t.Error("alice must not appear in actor list after soft-delete")
	}
	if tokenStatus(db, u.Token) != http.StatusUnauthorized {
		t.Error("soft-deleted actor's token must be rejected")
	}
}

// TestDeleteActor_ModeDefault confirms actor_only is the default when mode is omitted.
func TestDeleteActor_ModeDefault(t *testing.T) {
	db, ro := openTestDB(t)
	h := newActorHandler(t, db, ro)
	u := createAPIActor(t, db, "bob")

	if code := deleteActor(t, h, u.ID, ""); code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", code)
	}
	if listAPIActorIDs(t, h)[u.ID] {
		t.Error("bob must not appear in list after default delete")
	}
}

// TestDeleteActor_ModeActorAndHistory verifies anonymization + hard-delete.
func TestDeleteActor_ModeActorAndHistory(t *testing.T) {
	db, ro := openTestDB(t)
	h := newActorHandler(t, db, ro)
	u := createAPIActor(t, db, "carol")
	addAPISpan(t, db, "sp-carol", "carol")

	if code := deleteActor(t, h, u.ID, "actor_and_history"); code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", code)
	}
	if listAPIActorIDs(t, h)[u.ID] {
		t.Error("carol must not appear in list after actor_and_history delete")
	}
}

// TestDeleteActor_NotFound returns 404 for an unknown actor ID.
func TestDeleteActor_NotFound(t *testing.T) {
	db, ro := openTestDB(t)
	h := newActorHandler(t, db, ro)

	if code := deleteActor(t, h, "no-such-id", "actor_only"); code != http.StatusNotFound {
		t.Errorf("want 404, got %d", code)
	}
}

// TestDeleteActor_AlreadyDeleted returns 404 on a second delete attempt.
func TestDeleteActor_AlreadyDeleted(t *testing.T) {
	db, ro := openTestDB(t)
	h := newActorHandler(t, db, ro)
	u := createAPIActor(t, db, "dave")

	deleteActor(t, h, u.ID, "actor_only")
	if code := deleteActor(t, h, u.ID, "actor_only"); code != http.StatusNotFound {
		t.Errorf("want 404 on double delete, got %d", code)
	}
}

// TestDeleteActor_InvalidMode returns 400 for an unrecognized mode.
func TestDeleteActor_InvalidMode(t *testing.T) {
	db, ro := openTestDB(t)
	h := newActorHandler(t, db, ro)
	u := createAPIActor(t, db, "eve")

	if code := deleteActor(t, h, u.ID, "nuke_everything"); code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", code)
	}
}

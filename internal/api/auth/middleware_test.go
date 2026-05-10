package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Flopsstuff/cotel/internal/api/auth"
	"github.com/Flopsstuff/cotel/internal/storage"
)

type mockAuthDB struct {
	user          *storage.User
	allowAnonymous bool
}

func (m *mockAuthDB) GetUserByToken(token string) (*storage.User, error) {
	if m.user != nil && m.user.Token == token {
		return m.user, nil
	}
	return nil, &notFoundErr{}
}

func (m *mockAuthDB) GetSettingBool(_ string, defaultVal bool) bool {
	return m.allowAnonymous
}

type notFoundErr struct{}

func (e *notFoundErr) Error() string { return "not found" }

var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func TestMiddleware_AnonymousAllowed(t *testing.T) {
	db := &mockAuthDB{allowAnonymous: true}
	h := auth.Middleware(db, okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("anonymous allowed: got %d, want 200", w.Code)
	}
}

func TestMiddleware_AnonymousDenied(t *testing.T) {
	db := &mockAuthDB{allowAnonymous: false}
	h := auth.Middleware(db, okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous denied: got %d, want 401", w.Code)
	}
}

func TestMiddleware_ValidToken(t *testing.T) {
	db := &mockAuthDB{
		user:          &storage.User{Name: "alice", Token: "cotel_abc123"},
		allowAnonymous: false,
	}
	h := auth.Middleware(db, okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer cotel_abc123")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("valid token: got %d, want 200", w.Code)
	}
}

func TestMiddleware_InvalidToken(t *testing.T) {
	db := &mockAuthDB{
		allowAnonymous: true, // allow_anonymous=true but invalid token should still 401
	}
	h := auth.Middleware(db, okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer cotel_badtoken")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("invalid token: got %d, want 401", w.Code)
	}
}

func TestMiddleware_NonCotelToken(t *testing.T) {
	// A bearer token not starting with cotel_ is treated as "no token" for the allow_anonymous check.
	db := &mockAuthDB{allowAnonymous: true}
	h := auth.Middleware(db, okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer someOtherToken")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("non-cotel bearer allowed: got %d, want 200", w.Code)
	}
}

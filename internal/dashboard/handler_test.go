package dashboard_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Flopsstuff/cotel/internal/dashboard"
	"github.com/Flopsstuff/cotel/internal/storage"
)

func openTestDB(t *testing.T) *storage.ReadDB {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db.ReadOnly()
}

func TestDashboardRoutesEmptyDB(t *testing.T) {
	db := openTestDB(t)
	h := dashboard.New(db)

	routes := []string{"/", "/sessions", "/costs", "/tools", "/healthz"}
	for _, path := range routes {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Errorf("GET %s: want 200, got %d body=%s", path, w.Code, w.Body.String())
			}
		})
	}
}

func TestDashboard404(t *testing.T) {
	db := openTestDB(t)
	h := dashboard.New(db)

	req := httptest.NewRequest(http.MethodGet, "/no-such-page", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestDashboardSessionDetail404(t *testing.T) {
	db := openTestDB(t)
	h := dashboard.New(db)

	req := httptest.NewRequest(http.MethodGet, "/sessions/nonexistent-session-id", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

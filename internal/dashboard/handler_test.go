package dashboard_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
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

// TestDashboardSPACatchAll verifies that unknown paths return the SPA index.html
// (200 + text/html) so React Router can handle client-side routing.
func TestDashboardSPACatchAll(t *testing.T) {
	db := openTestDB(t)
	h := dashboard.New(db)

	req := httptest.NewRequest(http.MethodGet, "/no-such-page", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("SPA catch-all: want 200, got %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("SPA catch-all: want text/html Content-Type, got %q", ct)
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

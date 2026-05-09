// Package dashboard serves the cotel analytics UI.
package dashboard

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed static
var staticFS embed.FS

type DB interface {
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
}

type Handler struct {
	db DB
}

func New(db DB) *Handler {
	return &Handler{db: db}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	if path == "/healthz" {
		h.serveHealthz(w, r)
		return
	}

	sub, _ := fs.Sub(staticFS, "static")
	staticHandler := http.FileServer(http.FS(sub))

	// Serve known static assets (JS, CSS, images) directly.
	if _, err := fs.Stat(sub, strings.TrimPrefix(path, "/")); err == nil {
		staticHandler.ServeHTTP(w, r)
		return
	}

	// Catch-all: serve index.html so React Router handles all dashboard routes.
	idx, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(idx) //nolint:errcheck
}

func (h *Handler) serveHealthz(w http.ResponseWriter, r *http.Request) {
	var spans int64
	_ = h.db.QueryRow("SELECT COUNT(*) FROM spans").Scan(&spans)
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"spans":%d}`, spans)
}

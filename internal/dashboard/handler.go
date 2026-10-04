// Package dashboard serves the cotel analytics UI.
package dashboard

import (
	"database/sql"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/Flopsstuff/cotel/internal/storage"
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

// healthzResponse is the container-facing health contract. Fields are only
// ever added here: "ok" and "spans" keep their original meaning and names.
type healthzResponse struct {
	OK    bool  `json:"ok"`
	Spans int64 `json:"spans"`
	// Null until something has been ingested — an empty database has no age,
	// and reporting 0 would read as "ingested just now".
	LastIngestAt         *string `json:"last_ingest_at"`
	NewestSpanAgeSeconds *int64  `json:"newest_span_age_seconds"`
}

// serveHealthz reports liveness plus ingest freshness. Staleness never changes
// the status code: this endpoint drives the container HEALTHCHECK, and a quiet
// weekend is not a broken service — the poller owns the staleness threshold.
// A failed query is different: the body says ok:false and the code says
// unavailable, matching the pre-storage readiness gate.
func (h *Handler) serveHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	fresh, err := storage.QueryIngestFreshness(h.db)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		writeJSON(w, healthzResponse{OK: false})
		return
	}
	writeJSON(w, healthzResponse{
		OK:                   true,
		Spans:                fresh.SpanCount,
		LastIngestAt:         fresh.Timestamp(),
		NewestSpanAgeSeconds: fresh.AgeSeconds(time.Now()),
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	// The header is already out, so a write error has nowhere left to go.
	_ = json.NewEncoder(w).Encode(v)
}

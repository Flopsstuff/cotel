// Package dashboard serves the cotel analytics UI.
package dashboard

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/Flopsstuff/cotel/internal/storage"
	"github.com/Flopsstuff/cotel/internal/tunnel"
)

//go:embed static
var staticFS embed.FS

type DB interface {
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
}

// TunnelProber answers for the cloudflared process running beside cotel.
type TunnelProber interface {
	Health(ctx context.Context) tunnel.Health
}

type Handler struct {
	db     DB
	tunnel TunnelProber
}

func New(db DB) *Handler {
	return &Handler{db: db}
}

// SetTunnelProber makes /healthz report the tunnel's state. Left unset on a
// deployment that runs no tunnel, and the key is then absent entirely.
func (h *Handler) SetTunnelProber(p TunnelProber) *Handler {
	h.tunnel = p
	return h
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
	// Absent unless the deployment runs a Cloudflare tunnel. A dead tunnel does
	// not make "ok" false: this endpoint drives the container HEALTHCHECK and
	// the deploy gate, and LAN ingest keeps working while the public path is
	// down — whoever polls decides what an unreachable edge is worth.
	Tunnel *tunnel.Health `json:"tunnel,omitempty"`
}

// serveHealthz reports liveness plus ingest freshness. Staleness never changes
// the status code: this endpoint drives the container HEALTHCHECK, and a quiet
// weekend is not a broken service — the poller owns the staleness threshold.
// A failed query is different: the body says ok:false and the code says
// unavailable, matching the pre-storage readiness gate. A dead tunnel is
// reported in the body for the same reason staleness is, and changes the status
// code just as little.
func (h *Handler) serveHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	fresh, err := storage.QueryIngestFreshness(h.db)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		writeJSON(w, healthzResponse{OK: false})
		return
	}
	resp := healthzResponse{
		OK:                   true,
		Spans:                fresh.SpanCount,
		LastIngestAt:         fresh.Timestamp(),
		NewestSpanAgeSeconds: fresh.AgeSeconds(time.Now()),
	}
	if h.tunnel != nil {
		th := h.tunnel.Health(r.Context())
		resp.Tunnel = &th
	}
	writeJSON(w, resp)
}

func writeJSON(w http.ResponseWriter, v any) {
	// The header is already out, so a write error has nowhere left to go.
	_ = json.NewEncoder(w).Encode(v)
}

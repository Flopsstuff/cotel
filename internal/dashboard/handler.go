// Package dashboard serves the cotel analytics UI.
package dashboard

import (
	"database/sql"
	"embed"
	"html/template"
	"net/http"
	"time"
)

//go:embed templates/*.html
var tmplFS embed.FS

var tmpl = template.Must(template.ParseFS(tmplFS, "templates/*.html"))

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
	switch r.URL.Path {
	case "/", "/dashboard":
		h.serveIndex(w, r)
	default:
		http.NotFound(w, r)
	}
}

type summaryRow struct {
	Sessions   int64
	TotalCost  float64
	InTokens   int64
	OutTokens  int64
}

type modelRow struct {
	Model     string
	Spans     int64
	InTokens  int64
	OutTokens int64
	CostUSD   float64
}

type toolRow struct {
	Tool  string
	Calls int64
}

func (h *Handler) serveIndex(w http.ResponseWriter, r *http.Request) {
	since := time.Now().AddDate(0, 0, -30)

	var sum summaryRow
	_ = h.db.QueryRow(`
		SELECT
			COUNT(DISTINCT session_id),
			COALESCE(SUM(cost_usd), 0),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0)
		FROM spans
		WHERE start_time >= ? AND name = 'claude_code.session'
	`, since).Scan(&sum.Sessions, &sum.TotalCost, &sum.InTokens, &sum.OutTokens)

	rows, _ := h.db.Query(`
		SELECT model, COUNT(*) AS spans,
		       COALESCE(SUM(input_tokens),0),
		       COALESCE(SUM(output_tokens),0),
		       COALESCE(SUM(cost_usd),0)
		FROM spans
		WHERE start_time >= ? AND model IS NOT NULL
		GROUP BY model ORDER BY spans DESC LIMIT 20
	`, since)
	var models []modelRow
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var m modelRow
			_ = rows.Scan(&m.Model, &m.Spans, &m.InTokens, &m.OutTokens, &m.CostUSD)
			models = append(models, m)
		}
	}

	trows, _ := h.db.Query(`
		SELECT tool_name, COUNT(*) AS calls
		FROM spans
		WHERE start_time >= ? AND tool_name IS NOT NULL
		GROUP BY tool_name ORDER BY calls DESC LIMIT 20
	`, since)
	var tools []toolRow
	if trows != nil {
		defer trows.Close()
		for trows.Next() {
			var t toolRow
			_ = trows.Scan(&t.Tool, &t.Calls)
			tools = append(tools, t)
		}
	}

	data := map[string]any{
		"Summary": sum,
		"Models":  models,
		"Tools":   tools,
		"Since":   since.Format("2006-01-02"),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tmpl.ExecuteTemplate(w, "index.html", data)
}

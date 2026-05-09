// Package dashboard serves the cotel analytics UI.
package dashboard

import (
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

//go:embed templates/*.html
var tmplFS embed.FS

//go:embed static
var staticFS embed.FS

var tmpl = template.Must(template.New("").Funcs(template.FuncMap{
	"fmtCost": func(f float64) string { return fmt.Sprintf("$%.4f", f) },
	"fmtPct":  func(f float64) string { return fmt.Sprintf("%.1f%%", f) },
	"monthDay": func(s string) string {
		if len(s) >= 10 {
			return s[5:10]
		}
		return s
	},
	"fmtTokens": func(n int64) string {
		if n >= 1_000_000 {
			return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
		}
		if n >= 1_000 {
			return fmt.Sprintf("%.1fk", float64(n)/1_000)
		}
		return fmt.Sprintf("%d", n)
	},
	"fmtDur": func(ms float64) string {
		if ms >= 60_000 {
			return fmt.Sprintf("%.0fm %ds", math.Floor(ms/60_000), int(ms/1_000)%60)
		}
		if ms >= 1_000 {
			return fmt.Sprintf("%.1fs", ms/1_000)
		}
		return fmt.Sprintf("%.0fms", ms)
	},
	"statusIcon": func(code int32) string {
		if code == 2 {
			return "✗"
		}
		return "✓"
	},
	"statusClass": func(code int32) string {
		if code == 2 {
			return "error"
		}
		return "ok"
	},
	"add": func(a, b int) int { return a + b },
	"sub": func(a, b int) int { return a - b },
	"seq": func(n int) []int {
		s := make([]int, n)
		for i := range s {
			s[i] = i + 1
		}
		return s
	},
	"json": func(v any) template.JS {
		b, _ := json.Marshal(v)
		return template.JS(b)
	},
}).ParseFS(tmplFS, "templates/*.html"))

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

const pageSize = 50

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// SSR routes take priority — kept for transition period.
	switch {
	case path == "/" || path == "/dashboard":
		h.serveIndex(w, r)
		return
	case path == "/sessions":
		h.serveSessions(w, r)
		return
	case strings.HasPrefix(path, "/sessions/"):
		h.serveSession(w, r, strings.TrimPrefix(path, "/sessions/"))
		return
	case path == "/costs":
		h.serveCosts(w, r)
		return
	case path == "/tools":
		h.serveTools(w, r)
		return
	case path == "/healthz":
		h.serveHealthz(w, r)
		return
	}

	// Try to serve from the embedded static SPA build.
	sub, _ := fs.Sub(staticFS, "static")
	staticHandler := http.FileServer(http.FS(sub))

	// If the requested file exists in the FS, serve it directly.
	if _, err := fs.Stat(sub, strings.TrimPrefix(path, "/")); err == nil {
		staticHandler.ServeHTTP(w, r)
		return
	}

	// Catch-all: serve index.html so React Router can handle the route.
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

// ---- index ----

type summaryRow struct {
	Sessions  int64
	TotalCost float64
	InTokens  int64
	OutTokens int64
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

type dailyCostRow struct {
	Day     string
	CostUSD float64
}

func (h *Handler) serveIndex(w http.ResponseWriter, r *http.Request) {
	since := time.Now().AddDate(0, 0, -30)

	// Sessions are defined by distinct session_id across all spans.
	// Claude Code beta does not emit a wrapping claude_code.session root span,
	// so we group by session_id rather than filtering on span name.
	var sum summaryRow
	_ = h.db.QueryRow(`
		SELECT
			COUNT(DISTINCT session_id),
			COALESCE(SUM(cost_usd), 0),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0)
		FROM spans
		WHERE start_time >= ? AND session_id IS NOT NULL
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
		GROUP BY tool_name ORDER BY calls DESC LIMIT 10
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

	// Daily cost bar chart: all spans with cost data (no span-name filter).
	drows, _ := h.db.Query(`
		SELECT
			strftime(CAST(start_time AS TIMESTAMP), '%Y-%m-%d') AS day,
			COALESCE(SUM(cost_usd), 0)
		FROM spans
		WHERE start_time >= ? AND cost_usd IS NOT NULL
		GROUP BY day
		ORDER BY day ASC
	`, since)
	var dailyCosts []dailyCostRow
	if drows != nil {
		defer drows.Close()
		for drows.Next() {
			var d dailyCostRow
			_ = drows.Scan(&d.Day, &d.CostUSD)
			dailyCosts = append(dailyCosts, d)
		}
	}

	data := map[string]any{
		"ActiveNav":  "overview",
		"Summary":    sum,
		"Models":     models,
		"Tools":      tools,
		"DailyCosts": dailyCosts,
		"Since":      since.Format("2006-01-02"),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tmpl.ExecuteTemplate(w, "index.html", data)
}

// ---- sessions list ----

type sessionListRow struct {
	SessionID  string
	Model      string
	StartTime  time.Time
	DurationMs float64
	CostUSD    float64
	InTokens   int64
	OutTokens  int64
	ToolCalls  int64
	HasError   bool
	IsActive   bool
}

func (h *Handler) serveSessions(w http.ResponseWriter, r *http.Request) {
	page := 1
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 0 {
		page = p
	}
	offset := (page - 1) * pageSize

	// Count distinct sessions, not individual spans.
	var total int64
	_ = h.db.QueryRow(`SELECT COUNT(DISTINCT session_id) FROM spans WHERE session_id IS NOT NULL`).Scan(&total)

	totalPages := int(math.Ceil(float64(total) / pageSize))
	if totalPages == 0 {
		totalPages = 1
	}

	// One row per session, aggregated across all spans for that session.
	rows, _ := h.db.Query(`
		SELECT
			session_id,
			COALESCE(MAX(model), ''),
			MIN(start_time),
			epoch_ms(MAX(end_time)) - epoch_ms(MIN(start_time)),
			COALESCE(SUM(cost_usd), 0),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COUNT(*) FILTER (WHERE tool_name IS NOT NULL),
			MAX(CASE WHEN status_code = 2 THEN 1 ELSE 0 END) > 0,
			MAX(end_time) >= NOW() - INTERVAL '10 minutes'
		FROM spans
		WHERE session_id IS NOT NULL
		GROUP BY session_id
		ORDER BY MIN(start_time) DESC
		LIMIT ? OFFSET ?
	`, pageSize, offset)

	var sessions []sessionListRow
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var s sessionListRow
			_ = rows.Scan(&s.SessionID, &s.Model, &s.StartTime, &s.DurationMs,
				&s.CostUSD, &s.InTokens, &s.OutTokens, &s.ToolCalls, &s.HasError, &s.IsActive)
			sessions = append(sessions, s)
		}
	}

	data := map[string]any{
		"ActiveNav":  "sessions",
		"Sessions":   sessions,
		"Page":       page,
		"TotalPages": totalPages,
		"TotalCount": total,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tmpl.ExecuteTemplate(w, "sessions.html", data)
}

// ---- session detail ----

type sessionHeader struct {
	SessionID        string
	Model            string
	StartTime        time.Time
	EndTime          time.Time
	DurationMs       float64
	CostUSD          float64
	InTokens         int64
	OutTokens        int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	ErrorCount       int64
}

type spanRow struct {
	SpanID     string
	Name       string
	ToolName   string
	StartTime  time.Time
	RelativeMS float64
	DurationMs float64
	InTokens   sql.NullInt64
	OutTokens  sql.NullInt64
	StatusCode int32
}

func (h *Handler) serveSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	if sessionID == "" {
		http.NotFound(w, r)
		return
	}

	var hdr sessionHeader
	err := h.db.QueryRow(`
		SELECT
			session_id,
			COALESCE(MAX(model), ''),
			MIN(start_time),
			MAX(end_time),
			epoch_ms(MAX(end_time)) - epoch_ms(MIN(start_time)),
			COALESCE(SUM(cost_usd), 0),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(cache_read_tokens), 0),
			COALESCE(SUM(cache_write_tokens), 0),
			SUM(CASE WHEN status_code = 2 THEN 1 ELSE 0 END)
		FROM spans
		WHERE session_id = ?
		GROUP BY session_id
	`, sessionID).Scan(
		&hdr.SessionID, &hdr.Model,
		&hdr.StartTime, &hdr.EndTime, &hdr.DurationMs,
		&hdr.CostUSD,
		&hdr.InTokens, &hdr.OutTokens,
		&hdr.CacheReadTokens, &hdr.CacheWriteTokens,
		&hdr.ErrorCount,
	)
	if err != nil || hdr.SessionID == "" {
		http.NotFound(w, r)
		return
	}

	rows, _ := h.db.Query(`
		SELECT
			span_id,
			name,
			COALESCE(tool_name, ''),
			start_time,
			epoch_ms(start_time) - epoch_ms(?) AS relative_ms,
			duration_ms,
			input_tokens,
			output_tokens,
			status_code
		FROM spans
		WHERE session_id = ?
		ORDER BY start_time ASC
	`, hdr.StartTime, sessionID)

	var spans []spanRow
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var s spanRow
			_ = rows.Scan(&s.SpanID, &s.Name, &s.ToolName, &s.StartTime,
				&s.RelativeMS, &s.DurationMs, &s.InTokens, &s.OutTokens, &s.StatusCode)
			spans = append(spans, s)
		}
	}

	data := map[string]any{
		"ActiveNav": "sessions",
		"Header":    hdr,
		"Spans":     spans,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tmpl.ExecuteTemplate(w, "session.html", data)
}

// ---- costs page ----

type costModelRow struct {
	Model     string
	TotalCost float64
	InTokens  int64
	OutTokens int64
}

type costSessionRow struct {
	SessionID string
	StartTime time.Time
	CostUSD   float64
}

type costBarRow struct {
	Day       string
	CostUSD   float64
	HeightPct float64 // normalized 0-100 for CSS bar rendering
}

func (h *Handler) serveCosts(w http.ResponseWriter, r *http.Request) {
	since := time.Now().AddDate(0, 0, -30)

	drows, _ := h.db.Query(`
		SELECT
			strftime(CAST(start_time AS TIMESTAMP), '%Y-%m-%d') AS day,
			COALESCE(SUM(cost_usd), 0)
		FROM spans
		WHERE start_time >= ? AND cost_usd IS NOT NULL
		GROUP BY day
		ORDER BY day ASC
	`, since)
	var rawDaily []dailyCostRow
	if drows != nil {
		defer drows.Close()
		for drows.Next() {
			var d dailyCostRow
			_ = drows.Scan(&d.Day, &d.CostUSD)
			rawDaily = append(rawDaily, d)
		}
	}

	// Normalize bar heights relative to max day cost.
	var maxCost float64
	for _, d := range rawDaily {
		if d.CostUSD > maxCost {
			maxCost = d.CostUSD
		}
	}
	var dailyCosts []costBarRow
	for _, d := range rawDaily {
		pct := 0.0
		if maxCost > 0 {
			pct = d.CostUSD / maxCost * 100
		}
		dailyCosts = append(dailyCosts, costBarRow{d.Day, d.CostUSD, pct})
	}

	mrows, _ := h.db.Query(`
		SELECT
			model,
			COALESCE(SUM(cost_usd), 0),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0)
		FROM spans
		WHERE start_time >= ? AND model IS NOT NULL
		GROUP BY model
		ORDER BY SUM(cost_usd) DESC
	`, since)
	var costModels []costModelRow
	if mrows != nil {
		defer mrows.Close()
		for mrows.Next() {
			var m costModelRow
			_ = mrows.Scan(&m.Model, &m.TotalCost, &m.InTokens, &m.OutTokens)
			costModels = append(costModels, m)
		}
	}

	srows, _ := h.db.Query(`
		SELECT session_id, MIN(start_time), COALESCE(SUM(cost_usd), 0)
		FROM spans
		WHERE start_time >= ? AND session_id IS NOT NULL
		GROUP BY session_id
		ORDER BY SUM(cost_usd) DESC
		LIMIT 10
	`, since)
	var costSessions []costSessionRow
	if srows != nil {
		defer srows.Close()
		for srows.Next() {
			var s costSessionRow
			_ = srows.Scan(&s.SessionID, &s.StartTime, &s.CostUSD)
			costSessions = append(costSessions, s)
		}
	}

	data := map[string]any{
		"ActiveNav":    "costs",
		"DailyCosts":   dailyCosts,
		"CostModels":   costModels,
		"CostSessions": costSessions,
		"Since":        since.Format("2006-01-02"),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tmpl.ExecuteTemplate(w, "costs.html", data)
}

// ---- tools page ----

type toolStatsRow struct {
	Tool      string
	CallCount int64
	AvgDurMS  float64
	FailCount int64
	FailRate  float64
}

func (h *Handler) serveTools(w http.ResponseWriter, r *http.Request) {
	rows, _ := h.db.Query(`
		SELECT
			tool_name,
			COUNT(*) AS call_count,
			AVG(duration_ms) AS avg_dur_ms,
			COUNT(*) FILTER (WHERE status_code = 2) AS fail_count,
			100.0 * COUNT(*) FILTER (WHERE status_code = 2) / COUNT(*) AS fail_rate
		FROM spans
		WHERE tool_name IS NOT NULL
		GROUP BY tool_name
		ORDER BY call_count DESC
	`)
	var tools []toolStatsRow
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var t toolStatsRow
			_ = rows.Scan(&t.Tool, &t.CallCount, &t.AvgDurMS, &t.FailCount, &t.FailRate)
			tools = append(tools, t)
		}
	}

	data := map[string]any{
		"ActiveNav": "tools",
		"Tools":     tools,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tmpl.ExecuteTemplate(w, "tools.html", data)
}

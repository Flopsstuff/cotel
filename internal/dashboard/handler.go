// Package dashboard serves the cotel analytics UI.
package dashboard

import (
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

//go:embed templates/*.html
var tmplFS embed.FS

var tmpl = template.Must(template.New("").Funcs(template.FuncMap{
	"fmtCost": func(f float64) string { return fmt.Sprintf("$%.4f", f) },
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
	switch {
	case path == "/" || path == "/dashboard":
		h.serveIndex(w, r)
	case path == "/sessions":
		h.serveSessions(w, r)
	case strings.HasPrefix(path, "/sessions/"):
		h.serveSession(w, r, strings.TrimPrefix(path, "/sessions/"))
	case path == "/healthz":
		h.serveHealthz(w, r)
	default:
		http.NotFound(w, r)
	}
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

	// Daily cost for bar chart: last 30 days, one row per day.
	drows, _ := h.db.Query(`
		SELECT
			strftime(CAST(start_time AS TIMESTAMP), '%Y-%m-%d') AS day,
			COALESCE(SUM(cost_usd), 0)
		FROM spans
		WHERE start_time >= ? AND name = 'claude_code.session'
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
}

func (h *Handler) serveSessions(w http.ResponseWriter, r *http.Request) {
	page := 1
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 0 {
		page = p
	}
	offset := (page - 1) * pageSize

	var total int64
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM spans WHERE name = 'claude_code.session'`).Scan(&total)

	totalPages := int(math.Ceil(float64(total) / pageSize))
	if totalPages == 0 {
		totalPages = 1
	}

	rows, _ := h.db.Query(`
		SELECT
			s.session_id,
			COALESCE(s.model, ''),
			s.start_time,
			s.duration_ms,
			COALESCE(s.cost_usd, 0),
			COALESCE(s.input_tokens, 0),
			COALESCE(s.output_tokens, 0),
			COALESCE(t.tool_calls, 0),
			COALESCE(e.errors, 0) > 0 AS has_error
		FROM spans s
		LEFT JOIN (
			SELECT session_id, COUNT(*) AS tool_calls
			FROM spans WHERE name = 'claude_code.tool_use'
			GROUP BY session_id
		) t ON t.session_id = s.session_id
		LEFT JOIN (
			SELECT session_id, SUM(CASE WHEN status_code = 2 THEN 1 ELSE 0 END) AS errors
			FROM spans
			GROUP BY session_id
		) e ON e.session_id = s.session_id
		WHERE s.name = 'claude_code.session'
		ORDER BY s.start_time DESC
		LIMIT ? OFFSET ?
	`, pageSize, offset)

	var sessions []sessionListRow
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var s sessionListRow
			_ = rows.Scan(&s.SessionID, &s.Model, &s.StartTime, &s.DurationMs,
				&s.CostUSD, &s.InTokens, &s.OutTokens, &s.ToolCalls, &s.HasError)
			sessions = append(sessions, s)
		}
	}

	data := map[string]any{
		"ActiveNav":   "sessions",
		"Sessions":    sessions,
		"Page":        page,
		"TotalPages":  totalPages,
		"TotalCount":  total,
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
	SpanID        string
	Name          string
	ToolName      string
	StartTime     time.Time
	RelativeMS    float64
	DurationMs    float64
	InTokens      sql.NullInt64
	OutTokens     sql.NullInt64
	StatusCode    int32
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

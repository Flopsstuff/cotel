// Package api provides a JSON REST API over the cotel span database.
package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// DB is the read-only database interface used by all handlers.
type DB interface {
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
}

// Handler is the root JSON API handler, mounted under /api/v1/.
type Handler struct {
	db DB
}

// New returns an API Handler backed by db.
func New(db DB) *Handler {
	return &Handler{db: db}
}

// ServeHTTP routes all /api/v1/ requests.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if os.Getenv("COTEL_DEV_CORS") == "true" {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/v1")

	switch {
	case path == "/overview":
		h.handleOverview(w, r)
	case path == "/sessions" || path == "/sessions/":
		h.handleSessions(w, r)
	case strings.HasPrefix(path, "/sessions/"):
		h.handleSession(w, r, strings.TrimPrefix(path, "/sessions/"))
	case path == "/costs" || path == "/costs/":
		h.handleCosts(w, r)
	case path == "/tools" || path == "/tools/":
		h.handleTools(w, r)
	case path == "/models" || path == "/models/":
		h.handleModels(w, r)
	case path == "/health" || path == "/health/":
		h.handleHealth(w, r)
	default:
		jsonError(w, "not found", http.StatusNotFound)
	}
}

// ---- helpers ----

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// headers already sent; nothing useful to do
		_ = err
	}
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	fmt.Fprintf(w, `{"error":%q}`, msg)
}

func queryInt(r *http.Request, key string, fallback int) int {
	if v := r.URL.Query().Get(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

// ---- /api/v1/health ----

type healthResponse struct {
	Status     string `json:"status"`
	SpanCount  int64  `json:"span_count"`
	DBSizeBytes int64 `json:"db_size_bytes"`
}

func (h *Handler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	var spans int64
	_ = h.db.QueryRow("SELECT COUNT(*) FROM spans").Scan(&spans)

	var dbSize int64
	// DuckDB reports approximate file size via pragma; fall back to 0.
	_ = h.db.QueryRow("SELECT total_blocks * block_size FROM pragma_database_size()").Scan(&dbSize)

	jsonOK(w, healthResponse{
		Status:      "ok",
		SpanCount:   spans,
		DBSizeBytes: dbSize,
	})
}

// ---- /api/v1/overview ----

type overviewResponse struct {
	SessionsCount     int64          `json:"sessions_count"`
	TotalCostUSD      float64        `json:"total_cost_usd"`
	TotalInputTokens  int64          `json:"total_input_tokens"`
	TotalOutputTokens int64          `json:"total_output_tokens"`
	TotalCacheTokens  int64          `json:"total_cache_tokens"`
	DailyCosts        []dailyCostRow `json:"daily_costs"`
	TopModels         []topModelRow  `json:"top_models"`
	TopTools          []topToolRow   `json:"top_tools"`
}

type dailyCostRow struct {
	Date    string  `json:"date"`
	CostUSD float64 `json:"cost_usd"`
}

type topModelRow struct {
	Model     string `json:"model"`
	SpanCount int64  `json:"span_count"`
}

type topToolRow struct {
	ToolName  string `json:"tool_name"`
	CallCount int64  `json:"call_count"`
}

func (h *Handler) handleOverview(w http.ResponseWriter, _ *http.Request) {
	since := time.Now().AddDate(0, 0, -30)

	var resp overviewResponse
	_ = h.db.QueryRow(`
		SELECT
			COUNT(DISTINCT session_id),
			COALESCE(SUM(cost_usd), 0),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(cache_read_tokens) + SUM(cache_write_tokens), 0)
		FROM spans
		WHERE start_time >= ? AND session_id IS NOT NULL
	`, since).Scan(
		&resp.SessionsCount,
		&resp.TotalCostUSD,
		&resp.TotalInputTokens,
		&resp.TotalOutputTokens,
		&resp.TotalCacheTokens,
	)

	rows, _ := h.db.Query(`
		SELECT strftime(CAST(start_time AS TIMESTAMP), '%Y-%m-%d') AS day,
		       COALESCE(SUM(cost_usd), 0)
		FROM spans
		WHERE start_time >= ? AND cost_usd IS NOT NULL
		GROUP BY day ORDER BY day ASC
	`, since)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var r dailyCostRow
			_ = rows.Scan(&r.Date, &r.CostUSD)
			resp.DailyCosts = append(resp.DailyCosts, r)
		}
	}

	mrows, _ := h.db.Query(`
		SELECT model, COUNT(*) AS span_count
		FROM spans
		WHERE start_time >= ? AND model IS NOT NULL AND model <> ''
		GROUP BY model ORDER BY span_count DESC LIMIT 5
	`, since)
	if mrows != nil {
		defer mrows.Close()
		for mrows.Next() {
			var r topModelRow
			_ = mrows.Scan(&r.Model, &r.SpanCount)
			resp.TopModels = append(resp.TopModels, r)
		}
	}

	trows, _ := h.db.Query(`
		SELECT tool_name, COUNT(*) AS call_count
		FROM spans
		WHERE start_time >= ? AND tool_name IS NOT NULL AND tool_name <> ''
		GROUP BY tool_name ORDER BY call_count DESC LIMIT 5
	`, since)
	if trows != nil {
		defer trows.Close()
		for trows.Next() {
			var r topToolRow
			_ = trows.Scan(&r.ToolName, &r.CallCount)
			resp.TopTools = append(resp.TopTools, r)
		}
	}

	if resp.DailyCosts == nil {
		resp.DailyCosts = []dailyCostRow{}
	}
	if resp.TopModels == nil {
		resp.TopModels = []topModelRow{}
	}
	if resp.TopTools == nil {
		resp.TopTools = []topToolRow{}
	}

	jsonOK(w, resp)
}

// ---- /api/v1/sessions ----

type sessionItem struct {
	SessionID    string  `json:"session_id"`
	FirstSeen    string  `json:"first_seen"`
	LastSeen     string  `json:"last_seen"`
	Model        string  `json:"model"`
	CostUSD      float64 `json:"cost_usd"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	ToolCalls    int64   `json:"tool_calls"`
	Status       string  `json:"status"`
}

type sessionsResponse struct {
	Items []sessionItem `json:"items"`
	Total int64         `json:"total"`
	Page  int           `json:"page"`
	Limit int           `json:"limit"`
}

func (h *Handler) handleSessions(w http.ResponseWriter, r *http.Request) {
	page := queryInt(r, "page", 1)
	limit := queryInt(r, "limit", 50)
	sort := r.URL.Query().Get("sort")
	if sort == "" {
		sort = "start_time"
	}
	order := strings.ToUpper(r.URL.Query().Get("order"))
	if order != "ASC" {
		order = "DESC"
	}

	allowedSorts := map[string]string{
		"start_time": "MIN(start_time)",
		"cost_usd":   "SUM(cost_usd)",
		"tool_calls": "COUNT(*) FILTER (WHERE tool_name IS NOT NULL AND tool_name <> '')",
	}
	sortExpr, ok := allowedSorts[sort]
	if !ok {
		sortExpr = "MIN(start_time)"
	}

	var total int64
	_ = h.db.QueryRow(`SELECT COUNT(DISTINCT session_id) FROM spans WHERE session_id IS NOT NULL`).Scan(&total)

	offset := (page - 1) * limit
	q := fmt.Sprintf(`
		SELECT
			session_id,
			MIN(start_time),
			MAX(end_time),
			COALESCE(MAX(model), ''),
			COALESCE(SUM(cost_usd), 0),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COUNT(*) FILTER (WHERE tool_name IS NOT NULL AND tool_name <> ''),
			MAX(CASE WHEN status_code = 2 THEN 1 ELSE 0 END)
		FROM spans
		WHERE session_id IS NOT NULL
		GROUP BY session_id
		ORDER BY %s %s
		LIMIT ? OFFSET ?
	`, sortExpr, order)

	rows, err := h.db.Query(q, limit, offset)
	if err != nil {
		jsonError(w, "query failed", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	items := []sessionItem{}
	for rows.Next() {
		var s sessionItem
		var firstSeen, lastSeen time.Time
		var hasError int
		if err := rows.Scan(&s.SessionID, &firstSeen, &lastSeen, &s.Model,
			&s.CostUSD, &s.InputTokens, &s.OutputTokens, &s.ToolCalls, &hasError); err != nil {
			continue
		}
		s.FirstSeen = firstSeen.UTC().Format(time.RFC3339)
		s.LastSeen = lastSeen.UTC().Format(time.RFC3339)
		s.Status = "ok"
		if hasError == 1 {
			s.Status = "error"
		}
		items = append(items, s)
	}

	jsonOK(w, sessionsResponse{
		Items: items,
		Total: total,
		Page:  page,
		Limit: limit,
	})
}

// ---- /api/v1/sessions/{id} ----

type spanDetail struct {
	StartTime    string  `json:"start_time"`
	DurationMS   float64 `json:"duration_ms"`
	Name         string  `json:"name"`
	ToolName     string  `json:"tool_name,omitempty"`
	Model        string  `json:"model,omitempty"`
	InputTokens  *int64  `json:"input_tokens,omitempty"`
	OutputTokens *int64  `json:"output_tokens,omitempty"`
	Status       string  `json:"status"`
	Attributes   string  `json:"attributes"`
}

type sessionDetailResponse struct {
	SessionID         string       `json:"session_id"`
	FirstSeen         string       `json:"first_seen"`
	LastSeen          string       `json:"last_seen"`
	Model             string       `json:"model"`
	CostUSD           float64      `json:"cost_usd"`
	InputTokens       int64        `json:"input_tokens"`
	OutputTokens      int64        `json:"output_tokens"`
	CacheReadTokens   int64        `json:"cache_read_tokens"`
	CacheWriteTokens  int64        `json:"cache_write_tokens"`
	Spans             []spanDetail `json:"spans"`
}

func (h *Handler) handleSession(w http.ResponseWriter, _ *http.Request, sessionID string) {
	if sessionID == "" {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}

	var resp sessionDetailResponse
	var firstSeen, lastSeen time.Time
	err := h.db.QueryRow(`
		SELECT
			session_id,
			MIN(start_time),
			MAX(end_time),
			COALESCE(MAX(model), ''),
			COALESCE(SUM(cost_usd), 0),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(cache_read_tokens), 0),
			COALESCE(SUM(cache_write_tokens), 0)
		FROM spans
		WHERE session_id = ?
		GROUP BY session_id
	`, sessionID).Scan(
		&resp.SessionID, &firstSeen, &lastSeen,
		&resp.Model, &resp.CostUSD,
		&resp.InputTokens, &resp.OutputTokens,
		&resp.CacheReadTokens, &resp.CacheWriteTokens,
	)
	if err != nil || resp.SessionID == "" {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}
	resp.FirstSeen = firstSeen.UTC().Format(time.RFC3339)
	resp.LastSeen = lastSeen.UTC().Format(time.RFC3339)

	rows, _ := h.db.Query(`
		SELECT
			start_time,
			duration_ms,
			name,
			COALESCE(tool_name, ''),
			COALESCE(model, ''),
			input_tokens,
			output_tokens,
			status_code,
			COALESCE(attributes, '{}')
		FROM spans
		WHERE session_id = ?
		ORDER BY start_time ASC
	`, sessionID)

	resp.Spans = []spanDetail{}
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var s spanDetail
			var st time.Time
			var statusCode int32
			var inputTokens, outputTokens sql.NullInt64
			_ = rows.Scan(&st, &s.DurationMS, &s.Name, &s.ToolName, &s.Model,
				&inputTokens, &outputTokens, &statusCode, &s.Attributes)
			s.StartTime = st.UTC().Format(time.RFC3339)
			if inputTokens.Valid {
				s.InputTokens = &inputTokens.Int64
			}
			if outputTokens.Valid {
				s.OutputTokens = &outputTokens.Int64
			}
			s.Status = "ok"
			if statusCode == 2 {
				s.Status = "error"
			}
			if s.ToolName == "" {
				s.ToolName = ""
			}
			resp.Spans = append(resp.Spans, s)
		}
	}

	jsonOK(w, resp)
}

// ---- /api/v1/costs ----

type costDayRow struct {
	Date    string  `json:"date"`
	CostUSD float64 `json:"cost_usd"`
}

type costModelRow struct {
	Model   string  `json:"model"`
	CostUSD float64 `json:"cost_usd"`
}

type topSessionRow struct {
	SessionID string  `json:"session_id"`
	CostUSD   float64 `json:"cost_usd"`
	FirstSeen string  `json:"first_seen"`
}

type costsResponse struct {
	Daily       []costDayRow    `json:"daily"`
	ByModel     []costModelRow  `json:"by_model"`
	TopSessions []topSessionRow `json:"top_sessions"`
}

func (h *Handler) handleCosts(w http.ResponseWriter, r *http.Request) {
	from, to := parseDateRange(r)

	var resp costsResponse

	drows, _ := h.db.Query(`
		SELECT strftime(CAST(start_time AS TIMESTAMP), '%Y-%m-%d') AS day,
		       COALESCE(SUM(cost_usd), 0)
		FROM spans
		WHERE start_time >= ? AND start_time <= ? AND cost_usd IS NOT NULL
		GROUP BY day ORDER BY day ASC
	`, from, to)
	resp.Daily = []costDayRow{}
	if drows != nil {
		defer drows.Close()
		for drows.Next() {
			var r costDayRow
			_ = drows.Scan(&r.Date, &r.CostUSD)
			resp.Daily = append(resp.Daily, r)
		}
	}

	mrows, _ := h.db.Query(`
		SELECT model, COALESCE(SUM(cost_usd), 0)
		FROM spans
		WHERE start_time >= ? AND start_time <= ? AND model IS NOT NULL AND model <> ''
		GROUP BY model ORDER BY SUM(cost_usd) DESC
	`, from, to)
	resp.ByModel = []costModelRow{}
	if mrows != nil {
		defer mrows.Close()
		for mrows.Next() {
			var r costModelRow
			_ = mrows.Scan(&r.Model, &r.CostUSD)
			resp.ByModel = append(resp.ByModel, r)
		}
	}

	srows, _ := h.db.Query(`
		SELECT session_id, COALESCE(SUM(cost_usd), 0), MIN(start_time)
		FROM spans
		WHERE start_time >= ? AND start_time <= ? AND session_id IS NOT NULL
		GROUP BY session_id ORDER BY SUM(cost_usd) DESC LIMIT 10
	`, from, to)
	resp.TopSessions = []topSessionRow{}
	if srows != nil {
		defer srows.Close()
		for srows.Next() {
			var ts topSessionRow
			var firstSeen time.Time
			_ = srows.Scan(&ts.SessionID, &ts.CostUSD, &firstSeen)
			ts.FirstSeen = firstSeen.UTC().Format(time.RFC3339)
			resp.TopSessions = append(resp.TopSessions, ts)
		}
	}

	jsonOK(w, resp)
}

func parseDateRange(r *http.Request) (time.Time, time.Time) {
	to := time.Now()
	from := to.AddDate(0, 0, -30)

	if v := r.URL.Query().Get("from"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			from = t
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			to = t.Add(24*time.Hour - time.Second)
		}
	}
	return from, to
}

// ---- /api/v1/tools ----

type toolItem struct {
	Name          string  `json:"name"`
	Calls         int64   `json:"calls"`
	AvgDurationMS float64 `json:"avg_duration_ms"`
	FailCount     int64   `json:"fail_count"`
	FailRate      float64 `json:"fail_rate"`
}

type toolsResponse struct {
	Items []toolItem `json:"items"`
}

func (h *Handler) handleTools(w http.ResponseWriter, _ *http.Request) {
	rows, _ := h.db.Query(`
		SELECT
			tool_name,
			COUNT(*) AS calls,
			AVG(duration_ms) AS avg_dur_ms,
			COUNT(*) FILTER (WHERE status_code = 2) AS fail_count,
			100.0 * COUNT(*) FILTER (WHERE status_code = 2) / COUNT(*) AS fail_rate
		FROM spans
		WHERE tool_name IS NOT NULL AND tool_name <> ''
		GROUP BY tool_name ORDER BY calls DESC
	`)

	items := []toolItem{}
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var t toolItem
			_ = rows.Scan(&t.Name, &t.Calls, &t.AvgDurationMS, &t.FailCount, &t.FailRate)
			items = append(items, t)
		}
	}

	jsonOK(w, toolsResponse{Items: items})
}

// ---- /api/v1/models ----

type modelItem struct {
	Model             string  `json:"model"`
	SpanCount         int64   `json:"span_count"`
	TotalCostUSD      float64 `json:"total_cost_usd"`
	TotalInputTokens  int64   `json:"total_input_tokens"`
	TotalOutputTokens int64   `json:"total_output_tokens"`
}

type modelsResponse struct {
	Items []modelItem `json:"items"`
}

func (h *Handler) handleModels(w http.ResponseWriter, _ *http.Request) {
	rows, _ := h.db.Query(`
		SELECT
			model,
			COUNT(*) AS span_count,
			COALESCE(SUM(cost_usd), 0),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0)
		FROM spans
		WHERE model IS NOT NULL AND model <> ''
		GROUP BY model ORDER BY span_count DESC
	`)

	items := []modelItem{}
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var m modelItem
			_ = rows.Scan(&m.Model, &m.SpanCount, &m.TotalCostUSD, &m.TotalInputTokens, &m.TotalOutputTokens)
			items = append(items, m)
		}
	}

	jsonOK(w, modelsResponse{Items: items})
}

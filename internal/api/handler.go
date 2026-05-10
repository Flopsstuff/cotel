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
	db      DB
	tokenDB TokenStore
}

// New returns an API Handler backed by db.
func New(db DB) *Handler {
	return &Handler{db: db}
}

// SetTokenStore attaches a writable token store for the /tokens endpoints.
func (h *Handler) SetTokenStore(ts TokenStore) *Handler {
	h.tokenDB = ts
	return h
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
	case path == "/bash-commands" || path == "/bash-commands/":
		h.handleBashCommands(w, r)
	case path == "/models" || path == "/models/":
		h.handleModels(w, r)
	case path == "/history" || path == "/history/":
		h.handleHistory(w, r)
	case path == "/users" || path == "/users/":
		h.handleUsers(w, r)
	case path == "/health" || path == "/health/":
		h.handleHealth(w, r)
	case path == "/tokens" || path == "/tokens/":
		h.handleTokens(w, r)
	case strings.HasPrefix(path, "/tokens/"):
		rest := strings.TrimPrefix(path, "/tokens/")
		if strings.HasSuffix(rest, "/rotate") {
			h.handleTokenRotate(w, r, strings.TrimSuffix(rest, "/rotate"))
		} else {
			h.handleTokenByID(w, r, rest)
		}
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

// userIDClause returns an additional SQL WHERE fragment and its argument when
// the request contains a non-empty user_id query parameter. The caller appends
// the clause directly to the end of their existing WHERE and appends arg to
// their args slice. When no user_id is present both return values are zero.
func userIDClause(r *http.Request) (clause string, arg string) {
	if uid := r.URL.Query().Get("user_id"); uid != "" {
		return " AND user_id = ?", uid
	}
	return "", ""
}

// ---- /api/v1/users ----

type userRow struct {
	UserID            string  `json:"user_id"`
	IsDefault         bool    `json:"is_default"`
	Sessions          int64   `json:"sessions"`
	TotalCostUSD      float64 `json:"total_cost_usd"`
	TotalInputTokens  int64   `json:"total_input_tokens"`
	TotalOutputTokens int64   `json:"total_output_tokens"`
	SpanCount         int64   `json:"span_count"`
	LastSeen          string  `json:"last_seen"`
}

type usersResponse struct {
	Items []userRow `json:"items"`
}

func (h *Handler) handleUsers(w http.ResponseWriter, _ *http.Request) {
	rows, _ := h.db.Query(`
		SELECT
			COALESCE(user_id, '') AS user_id,
			user_id IS NULL       AS is_default,
			COUNT(DISTINCT session_id) AS sessions,
			COALESCE(SUM(cost_usd), 0) AS total_cost_usd,
			COALESCE(SUM(input_tokens), 0) AS total_input_tokens,
			COALESCE(SUM(output_tokens), 0) AS total_output_tokens,
			COUNT(*) AS span_count,
			MAX(end_time) AS last_seen
		FROM spans
		GROUP BY user_id
		ORDER BY total_cost_usd DESC
	`)
	items := []userRow{}
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var u userRow
			var lastSeen time.Time
			var isDefault bool
			_ = rows.Scan(&u.UserID, &isDefault, &u.Sessions,
				&u.TotalCostUSD, &u.TotalInputTokens, &u.TotalOutputTokens,
				&u.SpanCount, &lastSeen)
			u.IsDefault = isDefault
			u.LastSeen = lastSeen.UTC().Format(time.RFC3339)
			items = append(items, u)
		}
	}
	jsonOK(w, usersResponse{Items: items})
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
	UsersCount        int64          `json:"users_count"`
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

func (h *Handler) handleOverview(w http.ResponseWriter, r *http.Request) {
	since := time.Now().AddDate(0, 0, -30)
	uidClause, uid := userIDClause(r)

	var resp overviewResponse
	args := []any{since}
	if uid != "" {
		args = append(args, uid)
	}
	_ = h.db.QueryRow(`
		SELECT
			COUNT(DISTINCT session_id),
			COALESCE(SUM(cost_usd), 0),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(cache_read_tokens) + SUM(cache_write_tokens), 0)
		FROM spans
		WHERE start_time >= ? AND session_id IS NOT NULL`+uidClause,
		args...,
	).Scan(
		&resp.SessionsCount,
		&resp.TotalCostUSD,
		&resp.TotalInputTokens,
		&resp.TotalOutputTokens,
		&resp.TotalCacheTokens,
	)

	// Count distinct users (NULL counts as one "default" group).
	_ = h.db.QueryRow(`SELECT COUNT(DISTINCT user_id) FROM spans`).Scan(&resp.UsersCount)

	costArgs := []any{since}
	if uid != "" {
		costArgs = append(costArgs, uid)
	}
	rows, _ := h.db.Query(`
		SELECT strftime(CAST(start_time AS TIMESTAMP), '%Y-%m-%d') AS day,
		       COALESCE(SUM(cost_usd), 0)
		FROM spans
		WHERE start_time >= ? AND cost_usd IS NOT NULL`+uidClause+`
		GROUP BY day ORDER BY day ASC
	`, costArgs...)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var row dailyCostRow
			_ = rows.Scan(&row.Date, &row.CostUSD)
			resp.DailyCosts = append(resp.DailyCosts, row)
		}
	}

	modelArgs := []any{since}
	if uid != "" {
		modelArgs = append(modelArgs, uid)
	}
	mrows, _ := h.db.Query(`
		SELECT model, COUNT(*) AS span_count
		FROM spans
		WHERE start_time >= ? AND model IS NOT NULL AND model <> ''`+uidClause+`
		GROUP BY model ORDER BY span_count DESC LIMIT 5
	`, modelArgs...)
	if mrows != nil {
		defer mrows.Close()
		for mrows.Next() {
			var row topModelRow
			_ = mrows.Scan(&row.Model, &row.SpanCount)
			resp.TopModels = append(resp.TopModels, row)
		}
	}

	toolArgs := []any{since}
	if uid != "" {
		toolArgs = append(toolArgs, uid)
	}
	trows, _ := h.db.Query(`
		SELECT tool_name, COUNT(*) AS call_count
		FROM spans
		WHERE start_time >= ? AND tool_name IS NOT NULL AND tool_name <> ''`+uidClause+`
		GROUP BY tool_name ORDER BY call_count DESC LIMIT 5
	`, toolArgs...)
	if trows != nil {
		defer trows.Close()
		for trows.Next() {
			var row topToolRow
			_ = trows.Scan(&row.ToolName, &row.CallCount)
			resp.TopTools = append(resp.TopTools, row)
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

	uidClause, uid := userIDClause(r)

	var total int64
	totalArgs := []any{}
	if uid != "" {
		totalArgs = append(totalArgs, uid)
	}
	_ = h.db.QueryRow(`SELECT COUNT(DISTINCT session_id) FROM spans WHERE session_id IS NOT NULL`+uidClause, totalArgs...).Scan(&total)

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
		WHERE session_id IS NOT NULL%s
		GROUP BY session_id
		ORDER BY %s %s
		LIMIT ? OFFSET ?
	`, uidClause, sortExpr, order)

	listArgs := []any{}
	if uid != "" {
		listArgs = append(listArgs, uid)
	}
	listArgs = append(listArgs, limit, offset)
	rows, err := h.db.Query(q, listArgs...)
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
	uidClause, uid := userIDClause(r)

	baseArgs := []any{from, to}
	if uid != "" {
		baseArgs = append(baseArgs, uid)
	}

	var resp costsResponse

	drows, _ := h.db.Query(`
		SELECT strftime(CAST(start_time AS TIMESTAMP), '%Y-%m-%d') AS day,
		       COALESCE(SUM(cost_usd), 0)
		FROM spans
		WHERE start_time >= ? AND start_time <= ? AND cost_usd IS NOT NULL`+uidClause+`
		GROUP BY day ORDER BY day ASC
	`, baseArgs...)
	resp.Daily = []costDayRow{}
	if drows != nil {
		defer drows.Close()
		for drows.Next() {
			var row costDayRow
			_ = drows.Scan(&row.Date, &row.CostUSD)
			resp.Daily = append(resp.Daily, row)
		}
	}

	mrows, _ := h.db.Query(`
		SELECT model, COALESCE(SUM(cost_usd), 0)
		FROM spans
		WHERE start_time >= ? AND start_time <= ? AND model IS NOT NULL AND model <> ''`+uidClause+`
		GROUP BY model ORDER BY SUM(cost_usd) DESC
	`, baseArgs...)
	resp.ByModel = []costModelRow{}
	if mrows != nil {
		defer mrows.Close()
		for mrows.Next() {
			var row costModelRow
			_ = mrows.Scan(&row.Model, &row.CostUSD)
			resp.ByModel = append(resp.ByModel, row)
		}
	}

	srows, _ := h.db.Query(`
		SELECT session_id, COALESCE(SUM(cost_usd), 0), MIN(start_time)
		FROM spans
		WHERE start_time >= ? AND start_time <= ? AND session_id IS NOT NULL`+uidClause+`
		GROUP BY session_id ORDER BY SUM(cost_usd) DESC LIMIT 10
	`, baseArgs...)
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

func (h *Handler) handleTools(w http.ResponseWriter, r *http.Request) {
	uidClause, uid := userIDClause(r)
	args := []any{}
	if uid != "" {
		args = append(args, uid)
	}
	rows, _ := h.db.Query(`
		SELECT
			tool_name,
			COUNT(*) AS calls,
			AVG(duration_ms) AS avg_dur_ms,
			COUNT(*) FILTER (WHERE status_code = 2) AS fail_count,
			100.0 * COUNT(*) FILTER (WHERE status_code = 2) / COUNT(*) AS fail_rate
		FROM spans
		WHERE tool_name IS NOT NULL AND tool_name <> ''`+uidClause+`
		GROUP BY tool_name ORDER BY calls DESC
	`, args...)

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

// ---- /api/v1/bash-commands ----

type bashCommandItem struct {
	Command       string  `json:"command"`
	Calls         int64   `json:"calls"`
	AvgDurationMS float64 `json:"avg_duration_ms"`
	FailCount     int64   `json:"fail_count"`
	FailRate      float64 `json:"fail_rate"`
}

type bashCommandsResponse struct {
	Items []bashCommandItem `json:"items"`
}

// handleBashCommands returns per-command breakdown for Bash tool spans.
// The command is extracted from the span attributes JSON: direct "command" key first,
// then falling back to the "command" field inside a JSON-encoded "tool_input" value.
func (h *Handler) handleBashCommands(w http.ResponseWriter, r *http.Request) {
	uidClause, uid := userIDClause(r)
	args := []any{}
	if uid != "" {
		args = append(args, uid)
	}
	rows, _ := h.db.Query(`
		WITH cmd AS (
			SELECT
				COALESCE(
					attributes->>'command',
					TRY_CAST(attributes->>'tool_input' AS JSON)->>'command'
				) AS bash_command,
				duration_ms,
				status_code
			FROM spans
			WHERE tool_name = 'Bash'`+uidClause+`
		)
		SELECT
			bash_command,
			COUNT(*) AS calls,
			AVG(duration_ms) AS avg_dur_ms,
			COUNT(*) FILTER (WHERE status_code = 2) AS fail_count,
			100.0 * COUNT(*) FILTER (WHERE status_code = 2) / COUNT(*) AS fail_rate
		FROM cmd
		WHERE bash_command IS NOT NULL
		GROUP BY bash_command ORDER BY calls DESC
	`, args...)

	items := []bashCommandItem{}
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var t bashCommandItem
			_ = rows.Scan(&t.Command, &t.Calls, &t.AvgDurationMS, &t.FailCount, &t.FailRate)
			items = append(items, t)
		}
	}

	jsonOK(w, bashCommandsResponse{Items: items})
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

func (h *Handler) handleModels(w http.ResponseWriter, r *http.Request) {
	uidClause, uid := userIDClause(r)
	args := []any{}
	if uid != "" {
		args = append(args, uid)
	}
	rows, _ := h.db.Query(`
		SELECT
			model,
			COUNT(*) AS span_count,
			COALESCE(SUM(cost_usd), 0),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0)
		FROM spans
		WHERE model IS NOT NULL AND model <> ''`+uidClause+`
		GROUP BY model ORDER BY span_count DESC
	`, args...)

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

// ---- /api/v1/history ----

type historyBucket struct {
	Bucket       string  `json:"bucket"`
	Sessions     int64   `json:"sessions"`
	Spans        int64   `json:"spans"`
	CostUSD      float64 `json:"cost_usd"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
}

type historyModelRow struct {
	Bucket  string  `json:"bucket"`
	Model   string  `json:"model"`
	CostUSD float64 `json:"cost_usd"`
	Spans   int64   `json:"spans"`
}

type heatmapCell struct {
	Date    string  `json:"date"`
	Hour    int     `json:"hour"`
	Count   int64   `json:"count"`
	CostUSD float64 `json:"cost_usd"`
}

type historyResponse struct {
	Granularity string            `json:"granularity"`
	From        string            `json:"from"`
	To          string            `json:"to"`
	Buckets     []historyBucket   `json:"buckets"`
	ByModel     []historyModelRow `json:"by_model"`
	Heatmap     []heatmapCell     `json:"heatmap"`
}

func historyBucketExpr(gran string) string {
	switch gran {
	case "hour":
		return "strftime(CAST(start_time AS TIMESTAMP), '%Y-%m-%d %H:00')"
	case "week":
		return "strftime(date_trunc('week', CAST(start_time AS TIMESTAMP))::TIMESTAMP, '%Y-%m-%d')"
	case "month":
		return "strftime(CAST(start_time AS TIMESTAMP), '%Y-%m')"
	default:
		return "strftime(CAST(start_time AS TIMESTAMP), '%Y-%m-%d')"
	}
}

func (h *Handler) handleHistory(w http.ResponseWriter, r *http.Request) {
	gran := r.URL.Query().Get("granularity")
	switch gran {
	case "hour", "day", "week", "month":
	default:
		gran = "day"
	}

	from, to := parseDateRange(r)
	bucketExpr := historyBucketExpr(gran)

	var resp historyResponse
	resp.Granularity = gran
	resp.From = from.Format("2006-01-02")
	resp.To = to.Format("2006-01-02")

	uidClause, uid := userIDClause(r)
	baseArgs := []any{from, to}
	if uid != "" {
		baseArgs = append(baseArgs, uid)
	}

	brows, _ := h.db.Query(fmt.Sprintf(`
		SELECT
			%s AS bucket,
			COUNT(DISTINCT session_id) AS sessions,
			COUNT(*) AS spans,
			COALESCE(SUM(cost_usd), 0) AS cost_usd,
			COALESCE(SUM(input_tokens), 0) AS input_tokens,
			COALESCE(SUM(output_tokens), 0) AS output_tokens
		FROM spans
		WHERE start_time >= ? AND start_time <= ?%s
		GROUP BY bucket ORDER BY bucket ASC
	`, bucketExpr, uidClause), baseArgs...)
	resp.Buckets = []historyBucket{}
	if brows != nil {
		defer brows.Close()
		for brows.Next() {
			var b historyBucket
			_ = brows.Scan(&b.Bucket, &b.Sessions, &b.Spans, &b.CostUSD, &b.InputTokens, &b.OutputTokens)
			resp.Buckets = append(resp.Buckets, b)
		}
	}

	mrows, _ := h.db.Query(fmt.Sprintf(`
		SELECT
			%s AS bucket,
			model,
			COALESCE(SUM(cost_usd), 0) AS cost_usd,
			COUNT(*) AS spans
		FROM spans
		WHERE start_time >= ? AND start_time <= ? AND model IS NOT NULL AND model <> ''%s
		GROUP BY bucket, model ORDER BY bucket ASC, SUM(cost_usd) DESC
	`, bucketExpr, uidClause), baseArgs...)
	resp.ByModel = []historyModelRow{}
	if mrows != nil {
		defer mrows.Close()
		for mrows.Next() {
			var m historyModelRow
			_ = mrows.Scan(&m.Bucket, &m.Model, &m.CostUSD, &m.Spans)
			resp.ByModel = append(resp.ByModel, m)
		}
	}

	hrows, _ := h.db.Query(`
		SELECT
			strftime(CAST(start_time AS TIMESTAMP), '%Y-%m-%d') AS date,
			CAST(strftime(CAST(start_time AS TIMESTAMP), '%H') AS INTEGER) AS hour,
			COUNT(*) AS count,
			COALESCE(SUM(cost_usd), 0) AS cost_usd
		FROM spans
		WHERE start_time >= ? AND start_time <= ?`+uidClause+`
		GROUP BY date, hour ORDER BY date ASC, hour ASC
	`, baseArgs...)
	resp.Heatmap = []heatmapCell{}
	if hrows != nil {
		defer hrows.Close()
		for hrows.Next() {
			var c heatmapCell
			_ = hrows.Scan(&c.Date, &c.Hour, &c.Count, &c.CostUSD)
			resp.Heatmap = append(resp.Heatmap, c)
		}
	}

	jsonOK(w, resp)
}

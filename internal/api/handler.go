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

	"github.com/Flopsstuff/cotel/internal/storage"
)

// DB is the read-only database interface used by all handlers.
type DB interface {
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
}

// Handler is the root JSON API handler, mounted under /api/v1/.
type Handler struct {
	db              DB
	tokenDB         TokenStore
	userStore       UserStore
	publicIngestURL string
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

// SetUserStore attaches a writable store for the /users and /settings endpoints.
func (h *Handler) SetUserStore(us UserStore) *Handler {
	h.userStore = us
	return h
}

// SetPublicIngestURL configures the public-facing OTLP ingest URL returned by /health.
func (h *Handler) SetPublicIngestURL(u string) *Handler {
	h.publicIngestURL = u
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
	case strings.HasPrefix(path, "/users/"):
		rest := strings.TrimPrefix(path, "/users/")
		if strings.HasSuffix(rest, "/rotate-token") {
			h.handleUserRotateToken(w, r, strings.TrimSuffix(rest, "/rotate-token"))
		} else {
			h.handleUserByID(w, r, rest)
		}
	case path == "/settings" || path == "/settings/":
		h.handleSettings(w, r)
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

// rangeSince maps a rolling-window range key to its lower bound. "all" has no
// lower bound (nil). Windows roll from now, not calendar-aligned (ADR-0011).
func rangeSince(rangeKey string, now time.Time) *time.Time {
	var t time.Time
	switch rangeKey {
	case "all":
		return nil
	case "year":
		t = now.AddDate(0, 0, -365)
	case "week":
		t = now.AddDate(0, 0, -7)
	case "day":
		t = now.Add(-24 * time.Hour)
	default: // month
		t = now.AddDate(0, 0, -30)
	}
	return &t
}

// parseRange reads the range query parameter, falling back to "month" for
// missing or unrecognised values, and returns the normalised key with its lower
// bound.
func parseRange(r *http.Request) (string, *time.Time) {
	return parseRangeDefault(r, "month")
}

// parseRangeDefault is parseRange with a caller-chosen fallback. /sessions and
// /models pass "all" because they had no time filter before ADR-0014, and a
// "month" default would silently truncate every existing caller.
func parseRangeDefault(r *http.Request, def string) (string, *time.Time) {
	rk := r.URL.Query().Get("range")
	switch rk {
	case "all", "year", "month", "week", "day":
	default:
		rk = def
	}
	return rk, rangeSince(rk, time.Now())
}

// parseSortOrder resolves sort/order against a whitelist of sort keys. Unknown
// values fall back to def / "desc" rather than 400ing.
func parseSortOrder(r *http.Request, allowed map[string]string, def string) (sort, order string) {
	sort = r.URL.Query().Get("sort")
	if _, ok := allowed[sort]; !ok {
		sort = def
	}
	order = strings.ToLower(r.URL.Query().Get("order"))
	if order != "asc" {
		order = "desc"
	}
	return sort, order
}

// parsePaging reads the 1-based page and the page size. limit defaults to 0,
// meaning unpaginated, and is clamped to 500.
func parsePaging(r *http.Request) (page, limit int) {
	page = queryInt(r, "page", 1)
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			limit = n
		}
	}
	if limit > 500 {
		limit = 500
	}
	return page, limit
}

// escapeLike escapes the LIKE/ILIKE wildcards so a search term is matched
// literally under `ESCAPE '\'`.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// likeFilter builds an ILIKE fragment for column and appends its argument when
// q is non-empty; both are zero-valued otherwise.
func likeFilter(column, q string) (clause string, args []any) {
	q = strings.TrimSpace(q)
	if q == "" {
		return "", nil
	}
	return " AND " + column + ` ILIKE ? ESCAPE '\'`, []any{"%" + escapeLike(q) + "%"}
}

// userIDClause returns an additional SQL WHERE fragment and its argument when
// the request contains a non-empty user_id query parameter. The caller appends
// the clause directly to the end of their existing WHERE and appends arg to
// their args slice (only when arg is non-empty). When no user_id is present
// both return values are empty strings.
//
// The special value "__anonymous__" maps to user_id IS NULL (spans with no user_id).
func userIDClause(r *http.Request) (clause string, arg string) {
	return userIDClauseOn(r, "")
}

// userIDClauseOn is userIDClause with the column qualified by a table alias
// prefix (e.g. "du."), for queries that reference more than one table.
func userIDClauseOn(r *http.Request, prefix string) (clause string, arg string) {
	uid := r.URL.Query().Get("user_id")
	if uid == "" {
		return "", ""
	}
	if uid == "__anonymous__" {
		return " AND " + prefix + "user_id IS NULL", ""
	}
	return " AND " + prefix + "user_id = ?", uid
}

// usageCTE unions raw spans with rolled-up daily_usage into one row set that
// every additive range-scoped figure sums over (ADR-0014). It is the ADR-0011
// split: the roll-up consumes whole UTC days, so the earliest surviving raw day
// is fully raw and the aggregate side is bounded by `day < raw_floor` (strict) —
// `<=` would double count the boundary day.
//
// The roll-up writes UnknownSentinel into the session_id/model/tool_name primary
// key columns for spans that carried none. Mapping it back to NULL here keeps
// the aggregate side filtering exactly like the raw side, which drops NULL and
// ''; without it a phantom "unknown" model or tool appears once a window is old
// enough to have been rolled up.
//
// first_seen degrades to the aggregate's day at midnight for rolled-up rows:
// daily_usage keeps no intra-day timestamp. It is a lower bound on the real
// start, never a later one.
const usageCTE = `
WITH raw_floor AS (
    SELECT MIN(start_time) AS ts FROM spans
),
usage AS (
    SELECT
        CAST(CAST(start_time AS TIMESTAMP) AS DATE) AS day,
        start_time AS first_seen,
        NULLIF(session_id, '') AS session_id,
        user_id,
        NULLIF(model, '') AS model,
        NULLIF(tool_name, '') AS tool_name,
        CAST(1 AS BIGINT) AS spans,
        cost_usd AS cost,
        input_tokens,
        output_tokens,
        COALESCE(cache_read_tokens, 0) + COALESCE(cache_write_tokens, 0) AS cache_tokens
    FROM spans
    WHERE TRUE%[1]s
    UNION ALL
    SELECT
        du.day,
        CAST(du.day AS TIMESTAMPTZ),
        NULLIF(du.session_id, '%[2]s'),
        du.user_id,
        NULLIF(du.model, '%[2]s'),
        NULLIF(du.tool_name, '%[2]s'),
        du.span_count,
        du.total_cost_usd,
        du.total_input_tokens,
        du.total_output_tokens,
        COALESCE(du.total_cache_read_tokens, 0) + COALESCE(du.total_cache_write_tokens, 0)
    FROM daily_usage du CROSS JOIN raw_floor rf
    WHERE (rf.ts IS NULL OR du.day < CAST(CAST(rf.ts AS TIMESTAMP) AS DATE))%[3]s
)`

// usageFilter carries the WHERE fragments that scope usageCTE to a time window
// and, optionally, one user, together with their arguments in statement order —
// raw side first, aggregate side second.
type usageFilter struct {
	raw  string
	agg  string
	args []any
}

// newUsageFilter builds the scoping fragments for usageCTE. from and to are
// inclusive bounds; nil leaves that side unbounded.
func newUsageFilter(r *http.Request, from, to *time.Time) usageFilter {
	rawUID, rawUIDArg := userIDClause(r)
	aggUID, aggUIDArg := userIDClauseOn(r, "du.")

	f := usageFilter{raw: rawUID, agg: aggUID}
	if rawUIDArg != "" {
		f.args = append(f.args, rawUIDArg)
	}
	if from != nil {
		f.raw += " AND start_time >= ?"
		f.args = append(f.args, *from)
	}
	if to != nil {
		f.raw += " AND start_time <= ?"
		f.args = append(f.args, *to)
	}
	if aggUIDArg != "" {
		f.args = append(f.args, aggUIDArg)
	}
	if from != nil {
		f.agg += " AND du.day >= CAST(CAST(? AS TIMESTAMP) AS DATE)"
		f.args = append(f.args, *from)
	}
	if to != nil {
		f.agg += " AND du.day <= CAST(CAST(? AS TIMESTAMP) AS DATE)"
		f.args = append(f.args, *to)
	}
	return f
}

func (f usageFilter) cte() string {
	return fmt.Sprintf(usageCTE, f.raw, storage.UnknownSentinel, f.agg)
}

// ---- /api/v1/users — delegated to users.go ----

// ---- /api/v1/health ----

type healthResponse struct {
	Status          string          `json:"status"`
	SpanCount       int64           `json:"span_count"`
	DBSizeBytes     int64           `json:"db_size_bytes"`
	Retention       retentionHealth `json:"retention"`
	PublicIngestURL string          `json:"public_ingest_url,omitempty"`
}

// retentionHealth surfaces the outcome of the last retention-worker cycle so a
// silently-failing roll-up (e.g. a NOT NULL constraint crash) is observable.
type retentionHealth struct {
	Status    string `json:"status"`               // "ok" | "error" | "unknown"
	LastRunAt string `json:"last_run_at,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

func (h *Handler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	var spans int64
	_ = h.db.QueryRow("SELECT COUNT(*) FROM spans").Scan(&spans)

	var dbSize int64
	// DuckDB reports approximate file size via pragma; fall back to 0.
	_ = h.db.QueryRow("SELECT total_blocks * block_size FROM pragma_database_size()").Scan(&dbSize)

	ret := h.retentionHealth()
	status := "ok"
	if ret.Status == "error" {
		status = "degraded"
	}

	jsonOK(w, healthResponse{
		Status:          status,
		SpanCount:       spans,
		DBSizeBytes:     dbSize,
		Retention:       ret,
		PublicIngestURL: h.publicIngestURL,
	})
}

// retentionHealth reads the retention-worker status the worker persisted to the
// settings table. A fresh DB (worker not yet run) reports status "unknown".
func (h *Handler) retentionHealth() retentionHealth {
	get := func(key string) string {
		var v string
		_ = h.db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&v)
		return v
	}
	status := get("retention_last_status")
	if status == "" {
		status = "unknown"
	}
	return retentionHealth{
		Status:    status,
		LastRunAt: get("retention_last_run_at"),
		LastError: get("retention_last_error"),
	}
}

// ---- /api/v1/overview ----

type overviewResponse struct {
	Range             string         `json:"range"`
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
	rangeKey, since := parseRange(r)
	f := newUsageFilter(r, since, nil)
	cte := f.cte()

	resp := overviewResponse{
		Range:      rangeKey,
		DailyCosts: []dailyCostRow{},
		TopModels:  []topModelRow{},
		TopTools:   []topToolRow{},
	}

	// The anonymous bucket has no user_id, so COUNT(DISTINCT user_id) would drop
	// it; it is one principal on the users list and counts as one here too.
	_ = h.db.QueryRow(cte+`
SELECT
    COUNT(DISTINCT session_id),
    COUNT(DISTINCT user_id) + CASE WHEN COUNT(*) FILTER (WHERE user_id IS NULL) > 0 THEN 1 ELSE 0 END,
    COALESCE(SUM(cost), 0),
    COALESCE(SUM(input_tokens), 0),
    COALESCE(SUM(output_tokens), 0),
    COALESCE(SUM(cache_tokens), 0)
FROM usage
`, f.args...).Scan(
		&resp.SessionsCount,
		&resp.UsersCount,
		&resp.TotalCostUSD,
		&resp.TotalInputTokens,
		&resp.TotalOutputTokens,
		&resp.TotalCacheTokens,
	)

	rows, _ := h.db.Query(cte+`
SELECT strftime(day, '%Y-%m-%d') AS d, COALESCE(SUM(cost), 0)
FROM usage
WHERE cost IS NOT NULL
GROUP BY d ORDER BY d ASC
`, f.args...)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var row dailyCostRow
			_ = rows.Scan(&row.Date, &row.CostUSD)
			resp.DailyCosts = append(resp.DailyCosts, row)
		}
	}

	mrows, _ := h.db.Query(cte+`
SELECT model, CAST(SUM(spans) AS BIGINT) AS span_count
FROM usage
WHERE model IS NOT NULL
GROUP BY model ORDER BY span_count DESC, model ASC LIMIT 5
`, f.args...)
	if mrows != nil {
		defer mrows.Close()
		for mrows.Next() {
			var row topModelRow
			_ = mrows.Scan(&row.Model, &row.SpanCount)
			resp.TopModels = append(resp.TopModels, row)
		}
	}

	trows, _ := h.db.Query(cte+`
SELECT tool_name, CAST(SUM(spans) AS BIGINT) AS call_count
FROM usage
WHERE tool_name IS NOT NULL
GROUP BY tool_name ORDER BY call_count DESC, tool_name ASC LIMIT 5
`, f.args...)
	if trows != nil {
		defer trows.Close()
		for trows.Next() {
			var row topToolRow
			_ = trows.Scan(&row.ToolName, &row.CallCount)
			resp.TopTools = append(resp.TopTools, row)
		}
	}

	jsonOK(w, resp)
}

// ---- /api/v1/sessions ----

type sessionItem struct {
	SessionID    string  `json:"session_id"`
	UserID       string  `json:"user_id"`
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
	Range string        `json:"range"`
	// CoveredSince names the start of the window this list actually answers for,
	// or null when the selected range is fully covered by raw spans. A session row
	// needs a start time, model and status, none of which daily_usage keeps
	// (ADR-0014), so the list is raw-only and a longer range is clamped.
	CoveredSince *string `json:"covered_since"`
}

func (h *Handler) handleSessions(w http.ResponseWriter, r *http.Request) {
	rangeKey, since := parseRangeDefault(r, "all")
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
	sinceClause := ""
	scopeArgs := []any{}
	if uid != "" {
		scopeArgs = append(scopeArgs, uid)
	}
	if since != nil {
		sinceClause = " AND start_time >= ?"
		scopeArgs = append(scopeArgs, *since)
	}
	uidClause += sinceClause

	var total int64
	_ = h.db.QueryRow(`SELECT COUNT(DISTINCT session_id) FROM spans WHERE session_id IS NOT NULL`+uidClause, scopeArgs...).Scan(&total)

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
			MAX(CASE WHEN status_code = 2 THEN 1 ELSE 0 END),
			COALESCE(MAX(user_id), '')
		FROM spans
		WHERE session_id IS NOT NULL%s
		GROUP BY session_id
		ORDER BY %s %s
		LIMIT ? OFFSET ?
	`, uidClause, sortExpr, order)

	listArgs := append(append([]any{}, scopeArgs...), limit, offset)
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
			&s.CostUSD, &s.InputTokens, &s.OutputTokens, &s.ToolCalls, &hasError, &s.UserID); err != nil {
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
		Items:        items,
		Total:        total,
		Page:         page,
		Limit:        limit,
		Range:        rangeKey,
		CoveredSince: h.rawCoveredSince(r, since),
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
			CAST(epoch_ms(end_time) - epoch_ms(start_time) AS DOUBLE) AS duration_ms,
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
	// Range is the range key that scoped this response, or null when explicit
	// from/to bounds superseded it (ADR-0014).
	Range *string `json:"range"`
}

func (h *Handler) handleCosts(w http.ResponseWriter, r *http.Request) {
	rangeKey, since := parseRange(r)

	from, to, explicit := explicitDateRange(r)
	echo := &rangeKey
	if explicit {
		echo = nil
	} else {
		from, to = since, nil
	}

	f := newUsageFilter(r, from, to)
	cte := f.cte()

	resp := costsResponse{
		Daily:       []costDayRow{},
		ByModel:     []costModelRow{},
		TopSessions: []topSessionRow{},
		Range:       echo,
	}

	drows, _ := h.db.Query(cte+`
SELECT strftime(day, '%Y-%m-%d') AS d, COALESCE(SUM(cost), 0)
FROM usage
WHERE cost IS NOT NULL
GROUP BY d ORDER BY d ASC
`, f.args...)
	if drows != nil {
		defer drows.Close()
		for drows.Next() {
			var row costDayRow
			_ = drows.Scan(&row.Date, &row.CostUSD)
			resp.Daily = append(resp.Daily, row)
		}
	}

	mrows, _ := h.db.Query(cte+`
SELECT model, COALESCE(SUM(cost), 0) AS cost
FROM usage
WHERE model IS NOT NULL
GROUP BY model ORDER BY cost DESC, model ASC
`, f.args...)
	if mrows != nil {
		defer mrows.Close()
		for mrows.Next() {
			var row costModelRow
			_ = mrows.Scan(&row.Model, &row.CostUSD)
			resp.ByModel = append(resp.ByModel, row)
		}
	}

	srows, _ := h.db.Query(cte+`
SELECT session_id, COALESCE(SUM(cost), 0) AS cost, MIN(first_seen)
FROM usage
WHERE session_id IS NOT NULL
GROUP BY session_id ORDER BY cost DESC, session_id ASC LIMIT 10
`, f.args...)
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

// explicitDateRange reports the from/to bounds when the caller supplied either
// one. They are the narrower, more specific statement, so they beat the range
// key; ok is false when neither is present and the caller falls back to range.
func explicitDateRange(r *http.Request) (from, to *time.Time, ok bool) {
	q := r.URL.Query()
	if q.Get("from") == "" && q.Get("to") == "" {
		return nil, nil, false
	}
	f, t := parseDateRange(r)
	return &f, &t, true
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
	Total int        `json:"total"`
	Page  int        `json:"page"`
	Limit int        `json:"limit"`
	Range string     `json:"range"`
	Sort  string     `json:"sort"`
	Order string     `json:"order"`
	// DurationStatsSince names the instant from which avg_duration_ms and
	// fail_rate are actually backed by data, or null when the whole selected
	// range is covered. Aggregate rows rolled up before schema v9 hold no
	// duration or failure sums, so they count toward calls but sit outside
	// those two denominators.
	DurationStatsSince *string `json:"duration_stats_since"`
}

// toolSortExprs whitelists sort keys to their SELECT-list alias, so the caller
// can never inject an ORDER BY expression.
var toolSortExprs = map[string]string{
	"name":            "name",
	"calls":           "calls",
	"avg_duration_ms": "avg_duration_ms",
	"fail_count":      "fail_count",
	"fail_rate":       "fail_rate",
}

// toolsStatsCTE unions raw spans with rolled-up daily_usage into one row per
// tool (ADR-0012). The roll-up consumes whole UTC days, so the earliest
// surviving raw day is fully raw and the aggregate side is bounded by
// `day < raw_floor` (strict) — `<=` would double count the boundary day.
//
// The aggregate side must exclude the roll-up's 'unknown' sentinel: it stands
// for a NULL or empty tool_name, which the raw side filters out entirely, so
// without this a phantom tool appears once a range is old enough to be rolled
// up. Duration and failure sums are NULL on pre-v9 rows; SUM skips them and the
// matching *_calls denominators count only the rows that carry a sum.
const toolsStatsCTE = `
WITH raw_floor AS (
    SELECT MIN(start_time) AS ts FROM spans
),
parts AS (
    SELECT
        tool_name AS name,
        COUNT(*) AS calls,
        SUM(CAST(epoch_ms(end_time) - epoch_ms(start_time) AS DOUBLE)) AS dur_sum,
        COUNT(*) AS dur_calls,
        COUNT(*) FILTER (WHERE status_code = 2) AS fails,
        COUNT(*) AS fail_calls
    FROM spans
    WHERE tool_name IS NOT NULL AND tool_name <> ''%[1]s%[2]s
    GROUP BY tool_name
    UNION ALL
    SELECT
        du.tool_name AS name,
        CAST(SUM(du.span_count) AS BIGINT) AS calls,
        SUM(du.total_duration_ms) AS dur_sum,
        CAST(COALESCE(SUM(du.span_count) FILTER (WHERE du.total_duration_ms IS NOT NULL), 0) AS BIGINT) AS dur_calls,
        CAST(SUM(du.fail_count) AS BIGINT) AS fails,
        CAST(COALESCE(SUM(du.span_count) FILTER (WHERE du.fail_count IS NOT NULL), 0) AS BIGINT) AS fail_calls
    FROM daily_usage du CROSS JOIN raw_floor rf
    WHERE du.tool_name <> '%[3]s'
      AND (rf.ts IS NULL OR du.day < CAST(CAST(rf.ts AS TIMESTAMP) AS DATE))%[4]s%[5]s
    GROUP BY du.tool_name
),
tool_stats AS (
    SELECT
        name,
        CAST(SUM(calls) AS BIGINT) AS calls,
        CASE WHEN SUM(dur_calls) > 0
             THEN SUM(COALESCE(dur_sum, 0)) / SUM(dur_calls) ELSE 0.0 END AS avg_duration_ms,
        CAST(SUM(COALESCE(fails, 0)) AS BIGINT) AS fail_count,
        CASE WHEN SUM(fail_calls) > 0
             THEN 100.0 * SUM(COALESCE(fails, 0)) / SUM(fail_calls) ELSE 0.0 END AS fail_rate
    FROM parts
    GROUP BY name
)`

func (h *Handler) handleTools(w http.ResponseWriter, r *http.Request) {
	rangeKey, since := parseRange(r)
	sort, order := parseSortOrder(r, toolSortExprs, "calls")
	page, limit := parsePaging(r)

	rawUID, rawUIDArg := userIDClause(r)
	aggUID, aggUIDArg := userIDClauseOn(r, "du.")

	var rawSince, aggSince string
	filterArgs := []any{}
	if rawUIDArg != "" {
		filterArgs = append(filterArgs, rawUIDArg)
	}
	if since != nil {
		rawSince = " AND start_time >= ?"
		filterArgs = append(filterArgs, *since)
	}
	if aggUIDArg != "" {
		filterArgs = append(filterArgs, aggUIDArg)
	}
	if since != nil {
		aggSince = " AND du.day >= CAST(CAST(? AS TIMESTAMP) AS DATE)"
		filterArgs = append(filterArgs, *since)
	}

	qFilter, qArgs := likeFilter("name", r.URL.Query().Get("q"))
	filterArgs = append(filterArgs, qArgs...)

	args := append([]any{}, filterArgs...)
	limitClause := ""
	if limit > 0 {
		limitClause = " LIMIT ? OFFSET ?"
		args = append(args, limit, (page-1)*limit)
	}

	cte := fmt.Sprintf(toolsStatsCTE, rawUID, rawSince, storage.UnknownSentinel, aggUID, aggSince)
	rows, err := h.db.Query(cte+fmt.Sprintf(`
SELECT name, calls, avg_duration_ms, fail_count, fail_rate, COUNT(*) OVER () AS total
FROM tool_stats
WHERE TRUE%s
ORDER BY %s %s NULLS LAST, name ASC%s
`, qFilter, toolSortExprs[sort], order, limitClause), args...)
	if err != nil {
		jsonError(w, "query failed", http.StatusInternalServerError)
		return
	}

	items := []toolItem{}
	total := 0
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var t toolItem
			_ = rows.Scan(&t.Name, &t.Calls, &t.AvgDurationMS, &t.FailCount, &t.FailRate, &total)
			items = append(items, t)
		}
	}
	// COUNT(*) OVER () rides on the result rows, so a page past the end has no
	// row to read it from. Re-count so total stays the match count either way.
	if len(items) == 0 {
		_ = h.db.QueryRow(cte+fmt.Sprintf("\nSELECT COUNT(*) FROM tool_stats WHERE TRUE%s\n", qFilter), filterArgs...).Scan(&total)
	}

	jsonOK(w, toolsResponse{
		Items:              items,
		Total:              total,
		Page:               page,
		Limit:              limit,
		Range:              rangeKey,
		Sort:               sort,
		Order:              order,
		DurationStatsSince: h.durationStatsSince(r, since),
	})
}

// durationStatsSince returns the instant from which the tool duration and
// failure figures are complete: the day after the newest in-window aggregate
// row that predates schema v9. It is nil when no such row exists, or when the
// gap it describes falls entirely before the selected range.
func (h *Handler) durationStatsSince(r *http.Request, since *time.Time) *string {
	aggUID, aggUIDArg := userIDClauseOn(r, "du.")
	args := []any{}
	if aggUIDArg != "" {
		args = append(args, aggUIDArg)
	}
	aggSince := ""
	if since != nil {
		aggSince = " AND du.day >= CAST(CAST(? AS TIMESTAMP) AS DATE)"
		args = append(args, *since)
	}

	var gapEnd sql.NullTime
	err := h.db.QueryRow(fmt.Sprintf(`
WITH raw_floor AS (SELECT MIN(start_time) AS ts FROM spans)
SELECT MAX(du.day)
FROM daily_usage du CROSS JOIN raw_floor rf
WHERE du.tool_name <> '%s'
  AND (rf.ts IS NULL OR du.day < CAST(CAST(rf.ts AS TIMESTAMP) AS DATE))
  AND (du.total_duration_ms IS NULL OR du.fail_count IS NULL)%s%s
`, storage.UnknownSentinel, aggUID, aggSince), args...).Scan(&gapEnd)
	if err != nil || !gapEnd.Valid {
		return nil
	}

	coveredFrom := gapEnd.Time.UTC().AddDate(0, 0, 1)
	if since != nil && !coveredFrom.After(*since) {
		return nil
	}
	s := coveredFrom.Format(time.RFC3339)
	return &s
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
	Total int               `json:"total"`
	Page  int               `json:"page"`
	Limit int               `json:"limit"`
	Range string            `json:"range"`
	Sort  string            `json:"sort"`
	Order string            `json:"order"`
	// CoveredSince names the start of the window this block actually answers
	// for, or null when the selected range is fully covered. A command string is
	// unbounded, so daily_usage carries no command dimension (ADR-0012) and the
	// breakdown is raw-only — a range reaching past the raw floor is clamped.
	CoveredSince *string `json:"covered_since"`
}

// bashSortExprs whitelists sort keys to their SELECT-list alias.
var bashSortExprs = map[string]string{
	"command":         "command",
	"calls":           "calls",
	"avg_duration_ms": "avg_duration_ms",
	"fail_count":      "fail_count",
	"fail_rate":       "fail_rate",
}

// handleBashCommands returns per-command breakdown for Bash tool spans.
// The command is extracted from the span attributes JSON: direct "command" key first,
// then falling back to the "command" field inside a JSON-encoded "tool_input" value.
func (h *Handler) handleBashCommands(w http.ResponseWriter, r *http.Request) {
	rangeKey, since := parseRange(r)
	sort, order := parseSortOrder(r, bashSortExprs, "calls")
	page, limit := parsePaging(r)

	uidClause, uid := userIDClause(r)
	filterArgs := []any{}
	if uid != "" {
		filterArgs = append(filterArgs, uid)
	}
	sinceClause := ""
	if since != nil {
		sinceClause = " AND start_time >= ?"
		filterArgs = append(filterArgs, *since)
	}

	qFilter, qArgs := likeFilter("command", r.URL.Query().Get("q"))
	filterArgs = append(filterArgs, qArgs...)

	args := append([]any{}, filterArgs...)
	limitClause := ""
	if limit > 0 {
		limitClause = " LIMIT ? OFFSET ?"
		args = append(args, limit, (page-1)*limit)
	}

	cte := `
WITH cmd AS (
    SELECT
        COALESCE(
            attributes->>'command',
            TRY_CAST(attributes->>'tool_input' AS JSON)->>'command'
        ) AS command,
        CAST(epoch_ms(end_time) - epoch_ms(start_time) AS DOUBLE) AS duration_ms,
        status_code
    FROM spans
    WHERE tool_name = 'Bash'` + uidClause + sinceClause + `
),
cmd_stats AS (
    SELECT
        command,
        COUNT(*) AS calls,
        AVG(duration_ms) AS avg_duration_ms,
        COUNT(*) FILTER (WHERE status_code = 2) AS fail_count,
        100.0 * COUNT(*) FILTER (WHERE status_code = 2) / COUNT(*) AS fail_rate
    FROM cmd
    WHERE command IS NOT NULL
    GROUP BY command
)`

	rows, err := h.db.Query(cte+fmt.Sprintf(`
SELECT command, calls, avg_duration_ms, fail_count, fail_rate, COUNT(*) OVER () AS total
FROM cmd_stats
WHERE TRUE%s
ORDER BY %s %s NULLS LAST, command ASC%s
`, qFilter, bashSortExprs[sort], order, limitClause), args...)
	if err != nil {
		jsonError(w, "query failed", http.StatusInternalServerError)
		return
	}

	items := []bashCommandItem{}
	total := 0
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var t bashCommandItem
			_ = rows.Scan(&t.Command, &t.Calls, &t.AvgDurationMS, &t.FailCount, &t.FailRate, &total)
			items = append(items, t)
		}
	}
	if len(items) == 0 {
		_ = h.db.QueryRow(cte+fmt.Sprintf("\nSELECT COUNT(*) FROM cmd_stats WHERE TRUE%s\n", qFilter), filterArgs...).Scan(&total)
	}

	jsonOK(w, bashCommandsResponse{
		Items:        items,
		Total:        total,
		Page:         page,
		Limit:        limit,
		Range:        rangeKey,
		Sort:         sort,
		Order:        order,
		CoveredSince: h.rawCoveredSince(r, since),
	})
}

// rawCoveredSince returns the raw floor when the selected range reaches back
// past it into days that only survive as aggregates, and nil when the range is
// fully covered by raw spans. Shared by the endpoints that cannot answer from
// the roll-up — the Bash breakdown (ADR-0012) and the sessions list (ADR-0014).
func (h *Handler) rawCoveredSince(r *http.Request, since *time.Time) *string {
	aggUID, aggUIDArg := userIDClauseOn(r, "du.")
	args := []any{}
	if aggUIDArg != "" {
		args = append(args, aggUIDArg)
	}
	aggSince := ""
	if since != nil {
		aggSince = " AND du.day >= CAST(CAST(? AS TIMESTAMP) AS DATE)"
		args = append(args, *since)
	}

	var rawFloor sql.NullTime
	err := h.db.QueryRow(fmt.Sprintf(`
WITH raw_floor AS (SELECT MIN(start_time) AS ts FROM spans)
SELECT (SELECT ts FROM raw_floor)
FROM raw_floor rf
WHERE EXISTS (
    SELECT 1 FROM daily_usage du
    WHERE (rf.ts IS NULL OR du.day < CAST(CAST(rf.ts AS TIMESTAMP) AS DATE))%s%s
)
`, aggUID, aggSince), args...).Scan(&rawFloor)
	if err != nil || !rawFloor.Valid {
		return nil
	}
	s := rawFloor.Time.UTC().Format(time.RFC3339)
	return &s
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
	Range string      `json:"range"`
}

func (h *Handler) handleModels(w http.ResponseWriter, r *http.Request) {
	rangeKey, since := parseRangeDefault(r, "all")
	f := newUsageFilter(r, since, nil)

	rows, _ := h.db.Query(f.cte()+`
SELECT
    model,
    CAST(SUM(spans) AS BIGINT) AS span_count,
    COALESCE(SUM(cost), 0),
    COALESCE(SUM(input_tokens), 0),
    COALESCE(SUM(output_tokens), 0)
FROM usage
WHERE model IS NOT NULL
GROUP BY model ORDER BY span_count DESC, model ASC
`, f.args...)

	items := []modelItem{}
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var m modelItem
			_ = rows.Scan(&m.Model, &m.SpanCount, &m.TotalCostUSD, &m.TotalInputTokens, &m.TotalOutputTokens)
			items = append(items, m)
		}
	}

	jsonOK(w, modelsResponse{Items: items, Range: rangeKey})
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

package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/Flopsstuff/cotel/internal/api"
	"github.com/Flopsstuff/cotel/internal/storage"
)

// usageRow is one rolled-up daily_usage row seeded by the range tests.
type usageRow struct {
	day     time.Time
	session string
	model   string
	tool    string
	user    string
	spans   int64
	cost    float64
	in, out int64
}

func addUsageRow(t *testing.T, db *storage.DB, r usageRow) {
	t.Helper()
	var user any
	if r.user != "" {
		user = r.user
	}
	_, err := db.Exec(`
		INSERT INTO daily_usage
		  (day, session_id, model, tool_name, user_id, span_count,
		   total_input_tokens, total_output_tokens,
		   total_cache_read_tokens, total_cache_write_tokens, total_cost_usd)
		VALUES (CAST(? AS DATE), ?, ?, ?, ?, ?, ?, ?, 0, 0, ?)
	`, r.day.UTC().Format("2006-01-02"), r.session, r.model, r.tool, user,
		r.spans, r.in, r.out, r.cost)
	if err != nil {
		t.Fatalf("insert daily_usage: %v", err)
	}
}

// seedRangeFixture lays raw spans and rolled-up rows either side of the raw
// floor, so each range key reaches a different subset:
//
//	day    -400  agg  carol  5 spans  $8   in 80  — only "all"
//	day     -60  agg  bob    3 spans  $4   in 40  — "year" and "all"
//	day      -5  agg  bob    7 spans  $99  in 700 — the floor day: never counted
//	day      -5  raw  alice  1 span   $2   in 20  — the raw floor itself
//	hour     -1  raw  alice  1 span   $1   in 10
//
// The floor-day aggregate is the double-count trap: the roll-up consumes whole
// UTC days, so that day is already fully represented by the raw span.
func seedRangeFixture(t *testing.T, db *storage.DB) {
	t.Helper()
	floor := floorNoon()

	insertSpan(t, db, storage.Span{
		TraceID: "tr", SpanID: "raw-floor", Name: "llm",
		SessionID: "s-raw-1", Model: "sonnet", ToolName: "Bash", UserID: "alice",
		StartTime: floor, EndTime: floor.Add(time.Second),
		CostUSD:   ptr(2.0), InputTokens: ptr(int64(20)), OutputTokens: ptr(int64(2)),
	})
	recent := time.Now().Add(-time.Hour)
	insertSpan(t, db, storage.Span{
		TraceID: "tr", SpanID: "raw-recent", Name: "llm",
		SessionID: "s-raw-2", Model: "sonnet", ToolName: "Read", UserID: "alice",
		StartTime: recent, EndTime: recent.Add(time.Second),
		CostUSD:   ptr(1.0), InputTokens: ptr(int64(10)), OutputTokens: ptr(int64(1)),
	})

	addUsageRow(t, db, usageRow{
		day: floor, session: "s-floor", model: "opus", tool: "Grep", user: "bob",
		spans: 7, cost: 99, in: 700, out: 70,
	})
	addUsageRow(t, db, usageRow{
		day: floor.AddDate(0, 0, -55), session: "s-year", model: "opus", tool: "Grep", user: "bob",
		spans: 3, cost: 4, in: 40, out: 4,
	})
	addUsageRow(t, db, usageRow{
		day: floor.AddDate(0, 0, -395), session: "s-old", model: "haiku", tool: "Glob", user: "carol",
		spans: 5, cost: 8, in: 80, out: 8,
	})
}

func num(t *testing.T, body map[string]any, key string) float64 {
	t.Helper()
	v, ok := body[key].(float64)
	if !ok {
		t.Fatalf("%s: want number, got %v (%T)", key, body[key], body[key])
	}
	return v
}

// pairs flattens a list-of-objects response field into label→number.
func pairs(t *testing.T, body map[string]any, field, labelKey, valueKey string) map[string]float64 {
	t.Helper()
	items, _ := body[field].([]any)
	out := map[string]float64{}
	for _, it := range items {
		m := it.(map[string]any)
		out[m[labelKey].(string)] = m[valueKey].(float64)
	}
	return out
}

// TestOverview_RangeAcrossUnion walks every range key over the fixture: the
// short windows resolve from raw spans alone, the long ones pick up the
// aggregate rows, and the floor day is counted exactly once.
func TestOverview_RangeAcrossUnion(t *testing.T) {
	db, ro := openTestDB(t)
	seedRangeFixture(t, db)
	h := api.New(ro)

	cases := []struct {
		rangeKey     string
		sessions     float64
		users        float64
		cost         float64
		input        float64
		output       float64
		dailyBuckets int
	}{
		{"day", 1, 1, 1, 10, 1, 1},
		{"week", 2, 1, 3, 30, 3, 2},
		{"month", 2, 1, 3, 30, 3, 2},
		{"year", 3, 2, 7, 70, 7, 3},
		{"all", 4, 3, 15, 150, 15, 4},
	}
	for _, tc := range cases {
		t.Run(tc.rangeKey, func(t *testing.T) {
			code, body := getJSON(t, h, "/api/v1/overview?range="+tc.rangeKey)
			if code != http.StatusOK {
				t.Fatalf("want 200, got %d: %v", code, body)
			}
			if body["range"] != tc.rangeKey {
				t.Errorf("range echo: want %q, got %v", tc.rangeKey, body["range"])
			}
			if got := num(t, body, "sessions_count"); got != tc.sessions {
				t.Errorf("sessions_count: want %v, got %v", tc.sessions, got)
			}
			if got := num(t, body, "users_count"); got != tc.users {
				t.Errorf("users_count: want %v, got %v", tc.users, got)
			}
			if got := num(t, body, "total_cost_usd"); got != tc.cost {
				t.Errorf("total_cost_usd: want %v, got %v", tc.cost, got)
			}
			if got := num(t, body, "total_input_tokens"); got != tc.input {
				t.Errorf("total_input_tokens: want %v, got %v", tc.input, got)
			}
			if got := num(t, body, "total_output_tokens"); got != tc.output {
				t.Errorf("total_output_tokens: want %v, got %v", tc.output, got)
			}
			daily, _ := body["daily_costs"].([]any)
			if len(daily) != tc.dailyBuckets {
				t.Errorf("daily_costs: want %d buckets, got %d (%v)", tc.dailyBuckets, len(daily), daily)
			}
		})
	}
}

// TestOverview_FloorDayNotDoubleCounted pins the boundary directly: the raw
// floor's calendar day also has a $99 aggregate row, which belongs to the same
// day the raw span already covers and must not be added to it.
func TestOverview_FloorDayNotDoubleCounted(t *testing.T) {
	db, ro := openTestDB(t)
	seedRangeFixture(t, db)
	h := api.New(ro)

	_, body := getJSON(t, h, "/api/v1/overview?range=all")
	byDay := pairs(t, body, "daily_costs", "date", "cost_usd")
	floorDay := floorNoon().UTC().Format("2006-01-02")
	if got := byDay[floorDay]; got != 2 {
		t.Errorf("floor day %s: want cost 2 (raw only), got %v — full map %v", floorDay, got, byDay)
	}
	if got := num(t, body, "total_cost_usd"); got != 15 {
		t.Errorf("total_cost_usd: want 15, got %v (floor-day aggregate leaked in?)", got)
	}
}

// TestOverview_RangeComposesWithUserID checks the two filters intersect rather
// than one overriding the other, including for users_count, which used to
// ignore both.
func TestOverview_RangeComposesWithUserID(t *testing.T) {
	db, ro := openTestDB(t)
	seedRangeFixture(t, db)
	h := api.New(ro)

	cases := []struct {
		path     string
		sessions float64
		users    float64
		cost     float64
	}{
		{"/api/v1/overview?range=all&user_id=alice", 2, 1, 3},
		{"/api/v1/overview?range=all&user_id=bob", 1, 1, 4},
		{"/api/v1/overview?range=all&user_id=carol", 1, 1, 8},
		{"/api/v1/overview?range=month&user_id=bob", 0, 0, 0},
		{"/api/v1/overview?range=week&user_id=alice", 2, 1, 3},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			_, body := getJSON(t, h, tc.path)
			if got := num(t, body, "sessions_count"); got != tc.sessions {
				t.Errorf("sessions_count: want %v, got %v", tc.sessions, got)
			}
			if got := num(t, body, "users_count"); got != tc.users {
				t.Errorf("users_count: want %v, got %v", tc.users, got)
			}
			if got := num(t, body, "total_cost_usd"); got != tc.cost {
				t.Errorf("total_cost_usd: want %v, got %v", tc.cost, got)
			}
		})
	}
}

// TestOverview_TopListsSpanTheUnion confirms top_models and top_tools count
// rolled-up spans too, and that the roll-up's 'unknown' sentinel never shows up
// as a model or tool of its own.
func TestOverview_TopListsSpanTheUnion(t *testing.T) {
	db, ro := openTestDB(t)
	seedRangeFixture(t, db)
	addUsageRow(t, db, usageRow{
		day: floorNoon().AddDate(0, 0, -55), session: storage.UnknownSentinel,
		model: storage.UnknownSentinel, tool: storage.UnknownSentinel,
		spans: 11, cost: 1,
	})
	h := api.New(ro)

	_, body := getJSON(t, h, "/api/v1/overview?range=all")

	models := pairs(t, body, "top_models", "model", "span_count")
	wantModels := map[string]float64{"haiku": 5, "opus": 3, "sonnet": 2}
	for m, want := range wantModels {
		if models[m] != want {
			t.Errorf("top_models[%s]: want %v, got %v (full %v)", m, want, models[m], models)
		}
	}
	if _, ok := models[storage.UnknownSentinel]; ok {
		t.Errorf("top_models leaked the roll-up sentinel: %v", models)
	}

	tools := pairs(t, body, "top_tools", "tool_name", "call_count")
	wantTools := map[string]float64{"Glob": 5, "Grep": 3, "Bash": 1, "Read": 1}
	for tn, want := range wantTools {
		if tools[tn] != want {
			t.Errorf("top_tools[%s]: want %v, got %v (full %v)", tn, want, tools[tn], tools)
		}
	}
	if _, ok := tools[storage.UnknownSentinel]; ok {
		t.Errorf("top_tools leaked the roll-up sentinel: %v", tools)
	}
}

// TestOverview_UnknownRangeFallsBack keeps the never-400 rule: a nonsense key
// resolves to the endpoint default.
func TestOverview_UnknownRangeFallsBack(t *testing.T) {
	db, ro := openTestDB(t)
	seedRangeFixture(t, db)
	h := api.New(ro)

	for _, path := range []string{"/api/v1/overview?range=fortnight", "/api/v1/overview?range="} {
		code, body := getJSON(t, h, path)
		if code != http.StatusOK {
			t.Fatalf("%s: want 200, got %d", path, code)
		}
		if body["range"] != "month" {
			t.Errorf("%s: want range=month, got %v", path, body["range"])
		}
		if got := num(t, body, "total_cost_usd"); got != 3 {
			t.Errorf("%s: want the month total 3, got %v", path, got)
		}
	}
}

// TestModels_RangeDefaultsToAll pins criterion (e) for /models: no range
// parameter must return exactly what it returned before ADR-0014.
func TestModels_RangeDefaultsToAll(t *testing.T) {
	db, ro := openTestDB(t)
	seedRangeFixture(t, db)
	h := api.New(ro)

	code, body := getJSON(t, h, "/api/v1/models")
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d", code)
	}
	if body["range"] != "all" {
		t.Errorf("range echo: want all, got %v", body["range"])
	}
	spans := pairs(t, body, "items", "model", "span_count")
	want := map[string]float64{"haiku": 5, "opus": 3, "sonnet": 2}
	if len(spans) != len(want) {
		t.Fatalf("items: want %d models, got %v", len(want), spans)
	}
	for m, n := range want {
		if spans[m] != n {
			t.Errorf("span_count[%s]: want %v, got %v", m, n, spans[m])
		}
	}

	costs := pairs(t, body, "items", "model", "total_cost_usd")
	if costs["opus"] != 4 {
		t.Errorf("opus cost: want 4 (aggregate only), got %v", costs["opus"])
	}
	if costs["sonnet"] != 3 {
		t.Errorf("sonnet cost: want 3 (raw only), got %v", costs["sonnet"])
	}
}

func TestModels_RangeScopesToRawWindow(t *testing.T) {
	db, ro := openTestDB(t)
	seedRangeFixture(t, db)
	h := api.New(ro)

	_, body := getJSON(t, h, "/api/v1/models?range=month")
	if body["range"] != "month" {
		t.Errorf("range echo: want month, got %v", body["range"])
	}
	spans := pairs(t, body, "items", "model", "span_count")
	if len(spans) != 1 || spans["sonnet"] != 2 {
		t.Errorf("month window: want only sonnet=2, got %v", spans)
	}

	_, yearBody := getJSON(t, h, "/api/v1/models?range=year")
	yearSpans := pairs(t, yearBody, "items", "model", "span_count")
	if yearSpans["opus"] != 3 {
		t.Errorf("year window: want opus=3 from the aggregate, got %v", yearSpans)
	}
	if _, ok := yearSpans["haiku"]; ok {
		t.Errorf("year window: 400-day-old aggregate leaked in: %v", yearSpans)
	}
}

// TestSessions_RangeDefaultsToAllAndClamps covers criterion (e) for /sessions
// and the covered_since clamp the list reports instead of absorbing.
func TestSessions_RangeDefaultsToAllAndClamps(t *testing.T) {
	db, ro := openTestDB(t)
	seedRangeFixture(t, db)
	h := api.New(ro)

	code, body := getJSON(t, h, "/api/v1/sessions")
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d", code)
	}
	if body["range"] != "all" {
		t.Errorf("range echo: want all, got %v", body["range"])
	}
	if got := num(t, body, "total"); got != 2 {
		t.Errorf("total: want the 2 raw sessions, got %v", got)
	}
	// "all" reaches past the raw floor into aggregate-only days, which cannot
	// produce session rows — so the list must say where its coverage starts.
	covered, ok := body["covered_since"].(string)
	if !ok {
		t.Fatalf("covered_since: want the raw floor, got %v", body["covered_since"])
	}
	ts, err := time.Parse(time.RFC3339, covered)
	if err != nil {
		t.Fatalf("covered_since %q is not RFC3339: %v", covered, err)
	}
	if d := ts.Sub(floorNoon()); d < -time.Second || d > time.Second {
		t.Errorf("covered_since: want the raw floor %s, got %s", floorNoon(), ts)
	}
}

func TestSessions_RangeScopesAndReportsFullCoverage(t *testing.T) {
	db, ro := openTestDB(t)
	seedRangeFixture(t, db)
	h := api.New(ro)

	cases := []struct {
		rangeKey  string
		total     float64
		wantClamp bool
	}{
		{"day", 1, false},
		{"week", 2, false},
		{"month", 2, false},
		{"year", 2, true},
		{"all", 2, true},
	}
	for _, tc := range cases {
		t.Run(tc.rangeKey, func(t *testing.T) {
			_, body := getJSON(t, h, "/api/v1/sessions?range="+tc.rangeKey)
			if got := num(t, body, "total"); got != tc.total {
				t.Errorf("total: want %v, got %v", tc.total, got)
			}
			items, _ := body["items"].([]any)
			if float64(len(items)) != tc.total {
				t.Errorf("items: want %v rows, got %d", tc.total, len(items))
			}
			if got := body["covered_since"] != nil; got != tc.wantClamp {
				t.Errorf("covered_since set = %v, want %v (got %v)", got, tc.wantClamp, body["covered_since"])
			}
		})
	}
}

// TestSessions_BlankSessionIDIsNotASession pins the two panels to the same
// number: a span with an empty session_id used to group into a list row of its
// own, whose link 404s, while the Overview count excluded it.
func TestSessions_BlankSessionIDIsNotASession(t *testing.T) {
	db, ro := openTestDB(t)
	seedRangeFixture(t, db)
	now := time.Now().Add(-2 * time.Hour)
	insertSpan(t, db, storage.Span{
		TraceID: "tr", SpanID: "no-session", Name: "llm", SessionID: "", UserID: "alice",
		StartTime: now, EndTime: now.Add(time.Second),
	})
	h := api.New(ro)

	_, sessions := getJSON(t, h, "/api/v1/sessions?limit=100")
	if got := num(t, sessions, "total"); got != 2 {
		t.Errorf("/sessions total: want 2, got %v", got)
	}
	for _, it := range sessions["items"].([]any) {
		if id := it.(map[string]any)["session_id"].(string); id == "" {
			t.Errorf("/sessions listed a blank session_id row: %v", it)
		}
	}

	// Compared on "month", where the union adds no aggregate sessions, so the
	// count and the list are answering for exactly the same set. On "all" they
	// legitimately differ: the count spans the roll-up, the list cannot.
	_, monthSessions := getJSON(t, h, "/api/v1/sessions?range=month&limit=100")
	_, overview := getJSON(t, h, "/api/v1/overview?range=month")
	if got := num(t, overview, "sessions_count"); got != num(t, monthSessions, "total") {
		t.Errorf("sessions_count %v disagrees with /sessions total %v",
			got, num(t, monthSessions, "total"))
	}
}

func TestCosts_RangeAndExplicitBounds(t *testing.T) {
	db, ro := openTestDB(t)
	seedRangeFixture(t, db)
	h := api.New(ro)

	t.Run("defaults to month", func(t *testing.T) {
		_, body := getJSON(t, h, "/api/v1/costs")
		if body["range"] != "month" {
			t.Errorf("range echo: want month, got %v", body["range"])
		}
		daily := pairs(t, body, "daily", "date", "cost_usd")
		if len(daily) != 2 {
			t.Errorf("daily: want the 2 raw days, got %v", daily)
		}
	})

	t.Run("all reaches the aggregates", func(t *testing.T) {
		_, body := getJSON(t, h, "/api/v1/costs?range=all")
		daily := pairs(t, body, "daily", "date", "cost_usd")
		if len(daily) != 4 {
			t.Errorf("daily: want 4 days, got %v", daily)
		}
		byModel := pairs(t, body, "by_model", "model", "cost_usd")
		if byModel["haiku"] != 8 || byModel["opus"] != 4 || byModel["sonnet"] != 3 {
			t.Errorf("by_model: want haiku=8 opus=4 sonnet=3, got %v", byModel)
		}
		sessions := pairs(t, body, "top_sessions", "session_id", "cost_usd")
		if sessions["s-old"] != 8 || sessions["s-year"] != 4 {
			t.Errorf("top_sessions: want the rolled-up sessions ranked by cost, got %v", sessions)
		}
		if _, ok := sessions["s-floor"]; ok {
			t.Errorf("top_sessions: floor-day aggregate leaked in: %v", sessions)
		}
	})

	t.Run("explicit bounds beat the range key", func(t *testing.T) {
		from := floorNoon().UTC().Format("2006-01-02")
		_, body := getJSON(t, h, "/api/v1/costs?range=all&from="+from)
		if body["range"] != nil {
			t.Errorf("range echo: want null when from/to win, got %v", body["range"])
		}
		daily := pairs(t, body, "daily", "date", "cost_usd")
		if len(daily) != 2 {
			t.Errorf("daily: want only the 2 days inside from/to, got %v", daily)
		}
	})
}

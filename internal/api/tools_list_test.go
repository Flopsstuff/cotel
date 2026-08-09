package api_test

import (
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/Flopsstuff/cotel/internal/api"
	"github.com/Flopsstuff/cotel/internal/storage"
)

// addToolSpans inserts n raw spans for one tool, each lasting durMS, of which
// fails carry the OTLP error status.
func addToolSpans(t *testing.T, db *storage.DB, tool string, start time.Time, n int, durMS int, fails int) {
	t.Helper()
	for i := 0; i < n; i++ {
		var status int32
		if i < fails {
			status = 2
		}
		insertSpan(t, db, storage.Span{
			TraceID: "tr", SpanID: fmt.Sprintf("%s-%d-%d", tool, start.Unix(), i),
			Name: "tool", SessionID: "sess", ToolName: tool, StatusCode: status,
			StartTime: start, EndTime: start.Add(time.Duration(durMS) * time.Millisecond),
		})
	}
}

// aggRow is one rolled-up daily_usage row. durationMS/failCount are pointers so
// a test can write the pre-v9 NULLs the migration leaves behind.
type aggRow struct {
	day        time.Time
	tool       string
	spanCount  int64
	durationMS *float64
	failCount  *int64
}

func addAggRow(t *testing.T, db *storage.DB, r aggRow) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO daily_usage
		  (day, session_id, model, tool_name, user_id, span_count, total_cost_usd, total_duration_ms, fail_count)
		VALUES (CAST(? AS DATE), ?, 'm', ?, NULL, ?, 0, ?, ?)
	`, r.day.UTC().Format("2006-01-02"), "agg-"+r.tool, r.tool, r.spanCount, r.durationMS, r.failCount)
	if err != nil {
		t.Fatalf("insert daily_usage: %v", err)
	}
}

func f64(v float64) *float64 { return &v }
func i64(v int64) *int64     { return &v }

// floorNoon returns noon UTC five days ago. Tests that straddle the raw/aggregate
// boundary anchor their raw spans here so the span and its calendar day agree
// whatever the session timezone and the current time of day are.
func floorNoon() time.Time {
	return time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -5).Add(12 * time.Hour)
}

// toolNames reads the item names out of a /tools response in order.
func toolNames(t *testing.T, body map[string]any) []string {
	t.Helper()
	items, _ := body["items"].([]any)
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.(map[string]any)["name"].(string))
	}
	return out
}

func toolByName(t *testing.T, body map[string]any, name string) map[string]any {
	t.Helper()
	items, _ := body["items"].([]any)
	for _, it := range items {
		m := it.(map[string]any)
		if m["name"] == name {
			return m
		}
	}
	t.Fatalf("tool %q not in %v", name, toolNames(t, body))
	return nil
}

// seedSortableTools creates five tools whose metrics are distinct on name,
// calls and avg_duration_ms, and deliberately tied on fail_count/fail_rate so
// the paging tests also exercise the name tiebreak.
func seedSortableTools(t *testing.T, db *storage.DB) {
	t.Helper()
	now := time.Now().Add(-time.Hour)
	addToolSpans(t, db, "Alpha", now, 5, 100, 1)   // rate 20
	addToolSpans(t, db, "Bravo", now, 4, 200, 2)   // rate 50
	addToolSpans(t, db, "Charlie", now, 3, 300, 0) // rate 0
	addToolSpans(t, db, "Delta", now, 2, 400, 2)   // rate 100
	addToolSpans(t, db, "Echo", now, 1, 500, 0)    // rate 0
}

// TestTools_EchoAndDefaults confirms the response carries the echo fields with
// their documented defaults when no query parameters are supplied.
func TestTools_EchoAndDefaults(t *testing.T) {
	db, ro := openTestDB(t)
	addToolSpans(t, db, "Bash", time.Now().Add(-time.Hour), 2, 100, 0)
	h := api.New(ro)

	code, body := getJSON(t, h, "/api/v1/tools")
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d", code)
	}
	if body["range"] != "month" || body["sort"] != "calls" || body["order"] != "desc" {
		t.Errorf("defaults: range/sort/order = %v/%v/%v", body["range"], body["sort"], body["order"])
	}
	if body["limit"].(float64) != 0 || body["page"].(float64) != 1 {
		t.Errorf("paging defaults: limit=%v page=%v", body["limit"], body["page"])
	}
	if body["total"].(float64) != 1 {
		t.Errorf("total: want 1, got %v", body["total"])
	}
	if body["duration_stats_since"] != nil {
		t.Errorf("duration_stats_since: want null with no pre-v9 rows, got %v", body["duration_stats_since"])
	}
}

// TestTools_InvalidParamsFallBack confirms bad values fall back to defaults
// rather than erroring (trust the boundary).
func TestTools_InvalidParamsFallBack(t *testing.T) {
	db, ro := openTestDB(t)
	addToolSpans(t, db, "Bash", time.Now().Add(-time.Hour), 1, 10, 0)
	h := api.New(ro)

	code, body := getJSON(t, h, "/api/v1/tools?range=decade&sort=whatever&order=sideways&page=-3")
	if code != http.StatusOK {
		t.Fatalf("want 200 (no 400), got %d", code)
	}
	if body["range"] != "month" || body["sort"] != "calls" || body["order"] != "desc" {
		t.Errorf("fallbacks: range/sort/order = %v/%v/%v", body["range"], body["sort"], body["order"])
	}
	if body["page"].(float64) != 1 {
		t.Errorf("page fallback: want 1, got %v", body["page"])
	}
}

// TestTools_RangeScopesMetrics checks each window bounds the metrics: a tool
// used only outside the window disappears, and a tool used in both is counted
// only for the calls inside it.
func TestTools_RangeScopesMetrics(t *testing.T) {
	db, ro := openTestDB(t)
	now := time.Now()
	addToolSpans(t, db, "Recent", now.Add(-2*time.Hour), 3, 100, 0)
	addToolSpans(t, db, "Recent", now.AddDate(0, 0, -10), 4, 100, 0)
	addToolSpans(t, db, "Ancient", now.AddDate(0, 0, -200), 7, 100, 0)
	h := api.New(ro)

	cases := []struct {
		rangeKey  string
		wantTools int
		wantCalls float64
	}{
		{"day", 1, 3},
		{"week", 1, 3},
		{"month", 1, 7},
		{"year", 2, 7},
		{"all", 2, 7},
	}
	for _, tc := range cases {
		t.Run(tc.rangeKey, func(t *testing.T) {
			_, body := getJSON(t, h, "/api/v1/tools?range="+tc.rangeKey)
			if got := int(body["total"].(float64)); got != tc.wantTools {
				t.Fatalf("total: want %d tools, got %d (%v)", tc.wantTools, got, toolNames(t, body))
			}
			if got := toolByName(t, body, "Recent")["calls"].(float64); got != tc.wantCalls {
				t.Errorf("Recent calls: want %v, got %v", tc.wantCalls, got)
			}
		})
	}
}

// TestTools_UnionNoDoubleCountOnBoundaryDay pins the strict `day < raw_floor`
// bound: an aggregate row dated to the earliest raw day is already represented
// by raw spans, so counting it again would inflate that day.
func TestTools_UnionNoDoubleCountOnBoundaryDay(t *testing.T) {
	db, ro := openTestDB(t)
	rawFloor := floorNoon()
	addToolSpans(t, db, "Bash", rawFloor, 2, 100, 0)
	addAggRow(t, db, aggRow{day: rawFloor, tool: "Bash", spanCount: 50, durationMS: f64(5000), failCount: i64(0)})
	addAggRow(t, db, aggRow{day: rawFloor.AddDate(0, 0, -1), tool: "Bash", spanCount: 8, durationMS: f64(800), failCount: i64(0)})
	h := api.New(ro)

	_, body := getJSON(t, h, "/api/v1/tools?range=all")
	// 2 raw + 8 from the strictly-earlier day; the boundary day's 50 are excluded.
	if got := toolByName(t, body, "Bash")["calls"].(float64); got != 10 {
		t.Errorf("calls: want 10 (boundary-day aggregate excluded), got %v", got)
	}
}

// TestTools_UnknownSentinelHidden proves the roll-up's placeholder for a NULL or
// empty tool_name never surfaces as a tool of its own.
func TestTools_UnknownSentinelHidden(t *testing.T) {
	db, ro := openTestDB(t)
	rawFloor := floorNoon()
	addToolSpans(t, db, "Bash", rawFloor, 1, 100, 0)
	addAggRow(t, db, aggRow{
		day: rawFloor.AddDate(0, 0, -5), tool: storage.UnknownSentinel,
		spanCount: 99, durationMS: f64(9900), failCount: i64(3),
	})
	h := api.New(ro)

	_, body := getJSON(t, h, "/api/v1/tools?range=all")
	for _, n := range toolNames(t, body) {
		if n == storage.UnknownSentinel {
			t.Fatalf("the %q sentinel leaked into the tool list: %v", storage.UnknownSentinel, toolNames(t, body))
		}
	}
	if got := body["total"].(float64); got != 1 {
		t.Errorf("total: want 1, got %v", got)
	}
}

// TestTools_PreV9AggregateRowKeepsAverageHonest is the point of the nullable
// columns: a rolled-up row with no duration or failure sums still counts toward
// calls, but must stay out of both denominators rather than contributing a 0.
func TestTools_PreV9AggregateRowKeepsAverageHonest(t *testing.T) {
	db, ro := openTestDB(t)
	rawFloor := floorNoon()
	addToolSpans(t, db, "Bash", rawFloor, 2, 100, 1)
	addAggRow(t, db, aggRow{day: rawFloor.AddDate(0, 0, -1), tool: "Bash", spanCount: 8})
	h := api.New(ro)

	_, body := getJSON(t, h, "/api/v1/tools?range=all")
	item := toolByName(t, body, "Bash")
	if got := item["calls"].(float64); got != 10 {
		t.Errorf("calls: want 10 (the aggregate row counts), got %v", got)
	}
	if got := item["avg_duration_ms"].(float64); got != 100 {
		t.Errorf("avg_duration_ms: want 100 (2 covered calls), got %v — the NULL row dragged the average", got)
	}
	if got := item["fail_rate"].(float64); got != 50 {
		t.Errorf("fail_rate: want 50 (1 of 2 covered calls), got %v", got)
	}
	if body["duration_stats_since"] == nil {
		t.Error("duration_stats_since: want the shortfall reported, got null")
	}
}

// TestTools_DurationStatsSinceNullWhenCovered keeps the field honest in the
// other direction: a fully-populated aggregate row reports no shortfall.
func TestTools_DurationStatsSinceNullWhenCovered(t *testing.T) {
	db, ro := openTestDB(t)
	rawFloor := floorNoon()
	addToolSpans(t, db, "Bash", rawFloor, 2, 100, 0)
	addAggRow(t, db, aggRow{
		day: rawFloor.AddDate(0, 0, -1), tool: "Bash",
		spanCount: 8, durationMS: f64(800), failCount: i64(0),
	})
	h := api.New(ro)

	_, body := getJSON(t, h, "/api/v1/tools?range=all")
	if body["duration_stats_since"] != nil {
		t.Errorf("want null, got %v", body["duration_stats_since"])
	}
	if got := toolByName(t, body, "Bash")["avg_duration_ms"].(float64); got != 100 {
		t.Errorf("avg_duration_ms: want 100 over all 10 calls, got %v", got)
	}
}

// TestTools_GlobalSortAcrossPages is the pagination contract: for every sort key
// in both directions, the pages concatenated must equal the unpaginated list.
// That only holds if the ordering is global rather than applied per page.
func TestTools_GlobalSortAcrossPages(t *testing.T) {
	db, ro := openTestDB(t)
	seedSortableTools(t, db)
	h := api.New(ro)

	for _, sort := range []string{"name", "calls", "avg_duration_ms", "fail_count", "fail_rate"} {
		for _, order := range []string{"asc", "desc"} {
			t.Run(sort+"/"+order, func(t *testing.T) {
				base := "/api/v1/tools?range=all&sort=" + sort + "&order=" + order
				_, full := getJSON(t, h, base+"&limit=0")
				want := toolNames(t, full)
				if len(want) != 5 {
					t.Fatalf("unpaginated: want 5 tools, got %v", want)
				}

				var got []string
				for page := 1; page <= 3; page++ {
					_, body := getJSON(t, h, base+"&limit=2&page="+strconv.Itoa(page))
					if total := body["total"].(float64); total != 5 {
						t.Errorf("page %d total: want 5, got %v", page, total)
					}
					got = append(got, toolNames(t, body)...)
				}
				if len(got) != len(want) {
					t.Fatalf("pages concatenated: want %v, got %v", want, got)
				}
				for i := range want {
					if got[i] != want[i] {
						t.Fatalf("pages concatenated: want %v, got %v", want, got)
					}
				}
			})
		}
	}
}

// TestTools_LimitZeroReturnsEverything keeps the unpaginated default intact.
func TestTools_LimitZeroReturnsEverything(t *testing.T) {
	db, ro := openTestDB(t)
	seedSortableTools(t, db)
	h := api.New(ro)

	_, body := getJSON(t, h, "/api/v1/tools?range=all&limit=0&page=3")
	if got := len(toolNames(t, body)); got != 5 {
		t.Errorf("limit=0: want all 5 tools regardless of page, got %d", got)
	}
}

// TestTools_QueryFilter filters server-side on the tool name, and total counts
// matches rather than the whole table.
func TestTools_QueryFilter(t *testing.T) {
	db, ro := openTestDB(t)
	seedSortableTools(t, db)
	h := api.New(ro)

	// Matches Alpha and Charlie, and only case-insensitively.
	_, body := getJSON(t, h, "/api/v1/tools?range=all&q=HA")
	names := toolNames(t, body)
	if len(names) != 2 || body["total"].(float64) != 2 {
		t.Fatalf("q=HA: want Alpha+Charlie, got %v total=%v", names, body["total"])
	}

	_, none := getJSON(t, h, "/api/v1/tools?range=all&q=nothing-matches")
	if none["total"].(float64) != 0 || len(toolNames(t, none)) != 0 {
		t.Errorf("no match: want empty, got %v total=%v", toolNames(t, none), none["total"])
	}
}

// TestTools_UserFilterStillWorks guards the pre-existing user_id filter across
// the rewrite, on both halves of the union.
func TestTools_UserFilterStillWorks(t *testing.T) {
	db, ro := openTestDB(t)
	now := time.Now().Add(-time.Hour)
	insertSpan(t, db, storage.Span{
		TraceID: "tr", SpanID: "u1", Name: "tool", SessionID: "s", ToolName: "Bash",
		UserID: "alice", StartTime: now, EndTime: now.Add(100 * time.Millisecond),
	})
	insertSpan(t, db, storage.Span{
		TraceID: "tr", SpanID: "u2", Name: "tool", SessionID: "s", ToolName: "Grep",
		UserID: "bob", StartTime: now, EndTime: now.Add(100 * time.Millisecond),
	})
	h := api.New(ro)

	_, body := getJSON(t, h, "/api/v1/tools?range=all&user_id=alice")
	if names := toolNames(t, body); len(names) != 1 || names[0] != "Bash" {
		t.Errorf("user_id=alice: want [Bash], got %v", names)
	}
}

// ---- /api/v1/bash-commands ----

func addBashSpan(t *testing.T, db *storage.DB, spanID, command string, start time.Time, durMS int, status int32) {
	t.Helper()
	insertSpan(t, db, storage.Span{
		TraceID: "tr", SpanID: spanID, Name: "tool", SessionID: "sess", ToolName: "Bash",
		StatusCode: status, StartTime: start, EndTime: start.Add(time.Duration(durMS) * time.Millisecond),
		Attributes: fmt.Sprintf(`{"command":%q}`, command),
	})
}

func bashCommands(t *testing.T, body map[string]any) []string {
	t.Helper()
	items, _ := body["items"].([]any)
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.(map[string]any)["command"].(string))
	}
	return out
}

// TestBashCommands_EchoAndSortAcrossPages mirrors the tools contract: the echo
// fields are present and paging sits on top of a global sort.
func TestBashCommands_EchoAndSortAcrossPages(t *testing.T) {
	db, ro := openTestDB(t)
	now := time.Now().Add(-time.Hour)
	for i, cmd := range []string{"git status", "ls -la", "npm test"} {
		for n := 0; n <= i; n++ {
			addBashSpan(t, db, fmt.Sprintf("b%d-%d", i, n), cmd, now, 100*(i+1), 0)
		}
	}
	h := api.New(ro)

	_, body := getJSON(t, h, "/api/v1/bash-commands")
	if body["sort"] != "calls" || body["order"] != "desc" || body["range"] != "month" {
		t.Errorf("echo: sort/order/range = %v/%v/%v", body["sort"], body["order"], body["range"])
	}
	if body["total"].(float64) != 3 {
		t.Fatalf("total: want 3, got %v", body["total"])
	}

	base := "/api/v1/bash-commands?range=all&sort=command&order=asc"
	_, full := getJSON(t, h, base+"&limit=0")
	want := bashCommands(t, full)

	var got []string
	for page := 1; page <= 2; page++ {
		_, p := getJSON(t, h, base+"&limit=2&page="+strconv.Itoa(page))
		got = append(got, bashCommands(t, p)...)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("pages concatenated: want %v, got %v", want, got)
	}
}

// TestBashCommands_RangeScopesAndQueryFilters checks the window bounds the rows
// and q matches on the command text.
func TestBashCommands_RangeScopesAndQueryFilters(t *testing.T) {
	db, ro := openTestDB(t)
	now := time.Now()
	addBashSpan(t, db, "recent", "git status", now.Add(-2*time.Hour), 100, 0)
	addBashSpan(t, db, "old", "npm test", now.AddDate(0, 0, -60), 100, 0)
	h := api.New(ro)

	_, day := getJSON(t, h, "/api/v1/bash-commands?range=day")
	if cmds := bashCommands(t, day); len(cmds) != 1 || cmds[0] != "git status" {
		t.Errorf("range=day: want [git status], got %v", cmds)
	}
	_, all := getJSON(t, h, "/api/v1/bash-commands?range=all")
	if got := len(bashCommands(t, all)); got != 2 {
		t.Errorf("range=all: want 2 commands, got %d", got)
	}
	_, q := getJSON(t, h, "/api/v1/bash-commands?range=all&q=npm")
	if cmds := bashCommands(t, q); len(cmds) != 1 || cmds[0] != "npm test" {
		t.Errorf("q=npm: want [npm test], got %v", cmds)
	}
}

// TestBashCommands_CoveredSinceClamped reports the window the raw-only
// breakdown actually answers for when the range reaches past the raw floor.
func TestBashCommands_CoveredSinceClamped(t *testing.T) {
	db, ro := openTestDB(t)
	rawFloor := floorNoon()
	addBashSpan(t, db, "b1", "git status", rawFloor, 100, 0)
	h := api.New(ro)

	_, uncovered := getJSON(t, h, "/api/v1/bash-commands?range=all")
	if uncovered["covered_since"] != nil {
		t.Errorf("no aggregates: want covered_since null, got %v", uncovered["covered_since"])
	}

	addAggRow(t, db, aggRow{day: rawFloor.AddDate(0, 0, -5), tool: "Bash", spanCount: 12, durationMS: f64(1200), failCount: i64(0)})
	_, clamped := getJSON(t, h, "/api/v1/bash-commands?range=all")
	if clamped["covered_since"] == nil {
		t.Fatal("aggregates predate the raw floor: want covered_since set, got null")
	}
	if _, err := time.Parse(time.RFC3339, clamped["covered_since"].(string)); err != nil {
		t.Errorf("covered_since is not RFC3339: %v", clamped["covered_since"])
	}

	// A window that starts after the aggregates is fully covered by raw spans.
	_, dayRange := getJSON(t, h, "/api/v1/bash-commands?range=day")
	if dayRange["covered_since"] != nil {
		t.Errorf("range=day: want covered_since null, got %v", dayRange["covered_since"])
	}
}

// TestBashCommands_UnrangedFilterReturnsRows pins the tool filter against a
// DuckDB planner fault: a bare `tool_name = 'Bash'` on spans is pushed into an
// ART index scan that yields no rows, so the breakdown came back empty whenever
// no other predicate was present — which is every unranged request.
func TestBashCommands_UnrangedFilterReturnsRows(t *testing.T) {
	db, ro := openTestDB(t)
	addBashSpan(t, db, "b1", "git status", time.Now().Add(-time.Hour), 100, 0)
	addToolSpans(t, db, "Read", time.Now().Add(-time.Hour), 2, 10, 0)
	h := api.New(ro)

	_, body := getJSON(t, h, "/api/v1/bash-commands?range=all")
	if cmds := bashCommands(t, body); len(cmds) != 1 || cmds[0] != "git status" {
		t.Fatalf("range=all: want [git status], got %v (total=%v)", cmds, body["total"])
	}
	if body["total"].(float64) != 1 {
		t.Errorf("total: want 1, got %v", body["total"])
	}
}

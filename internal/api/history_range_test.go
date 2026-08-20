package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/Flopsstuff/cotel/internal/api"
	"github.com/Flopsstuff/cotel/internal/storage"
)

// sumBuckets totals one numeric field across the bucket series.
func sumBuckets(t *testing.T, body map[string]any, field string) float64 {
	t.Helper()
	var total float64
	for _, it := range bucketList(t, body) {
		v, ok := it.(map[string]any)[field].(float64)
		if !ok {
			t.Fatalf("buckets[].%s: want number, got %v", field, it)
		}
		total += v
	}
	return total
}

// sumByModel totals by_model spans per model. by_model is one row per (bucket,
// model), so a model that spans several buckets has several rows.
func sumByModel(t *testing.T, body map[string]any) map[string]float64 {
	t.Helper()
	rows, _ := body["by_model"].([]any)
	out := map[string]float64{}
	for _, it := range rows {
		m := it.(map[string]any)
		out[m["model"].(string)] += m["spans"].(float64)
	}
	return out
}

func bucketList(t *testing.T, body map[string]any) []any {
	t.Helper()
	items, ok := body["buckets"].([]any)
	if !ok {
		t.Fatalf("buckets: want a list, got %v", body["buckets"])
	}
	return items
}

// TestHistory_RangeDefaultsToMonth pins the default that preserves the previous
// behaviour: parseDateRange already answered a bare /history over the last 30
// days, so a caller that passes no range must still see exactly that.
func TestHistory_RangeDefaultsToMonth(t *testing.T) {
	db, ro := openTestDB(t)
	seedRangeFixture(t, db)
	h := api.New(ro)

	for _, path := range []string{"/api/v1/history", "/api/v1/history?range=fortnight", "/api/v1/history?range="} {
		code, body := getJSON(t, h, path)
		if code != http.StatusOK {
			t.Fatalf("%s: want 200, got %d: %v", path, code, body)
		}
		if body["range"] != "month" {
			t.Errorf("%s: range echo: want month, got %v", path, body["range"])
		}
		if got := len(bucketList(t, body)); got != 2 {
			t.Errorf("%s: want the 2 raw days, got %d buckets", path, got)
		}
		if got := sumBuckets(t, body, "cost_usd"); got != 3 {
			t.Errorf("%s: cost over buckets: want 3, got %v", path, got)
		}
	}
}

// TestHistory_DayGranularityAcrossUnion is the criterion this ticket exists for:
// on a long range the day series keeps charting past the raw floor instead of
// stopping there, and the floor day is counted exactly once.
func TestHistory_DayGranularityAcrossUnion(t *testing.T) {
	db, ro := openTestDB(t)
	seedRangeFixture(t, db)
	h := api.New(ro)

	cases := []struct {
		rangeKey string
		buckets  int
		spans    float64
		cost     float64
		sessions float64
	}{
		{"day", 1, 1, 1, 1},
		{"week", 2, 2, 3, 2},
		{"month", 2, 2, 3, 2},
		{"year", 3, 5, 7, 3},
		{"all", 4, 10, 15, 4},
	}
	for _, tc := range cases {
		t.Run(tc.rangeKey, func(t *testing.T) {
			code, body := getJSON(t, h, "/api/v1/history?granularity=day&range="+tc.rangeKey)
			if code != http.StatusOK {
				t.Fatalf("want 200, got %d: %v", code, body)
			}
			if body["range"] != tc.rangeKey {
				t.Errorf("range echo: want %q, got %v", tc.rangeKey, body["range"])
			}
			if body["granularity"] != "day" {
				t.Errorf("granularity echo: want day, got %v", body["granularity"])
			}
			if got := len(bucketList(t, body)); got != tc.buckets {
				t.Errorf("buckets: want %d, got %d (%v)", tc.buckets, got, body["buckets"])
			}
			if got := sumBuckets(t, body, "spans"); got != tc.spans {
				t.Errorf("spans: want %v, got %v", tc.spans, got)
			}
			if got := sumBuckets(t, body, "cost_usd"); got != tc.cost {
				t.Errorf("cost_usd: want %v, got %v", tc.cost, got)
			}
			if got := sumBuckets(t, body, "sessions"); got != tc.sessions {
				t.Errorf("sessions: want %v, got %v", tc.sessions, got)
			}
			// The union covers a day-granularity window whole, so there is no
			// shortfall left to report.
			if body["covered_since"] != nil {
				t.Errorf("covered_since: want null at day granularity, got %v", body["covered_since"])
			}
		})
	}
}

// TestHistory_FloorDayNotDoubleCounted pins the boundary directly: the raw
// floor's calendar day also carries a $99 aggregate row, which stands for the
// same day the raw span already covers.
func TestHistory_FloorDayNotDoubleCounted(t *testing.T) {
	db, ro := openTestDB(t)
	seedRangeFixture(t, db)
	h := api.New(ro)

	_, body := getJSON(t, h, "/api/v1/history?granularity=day&range=all")
	byDay := pairs(t, body, "buckets", "bucket", "cost_usd")
	floorDay := floorNoon().UTC().Format("2006-01-02")
	if got := byDay[floorDay]; got != 2 {
		t.Errorf("floor day %s: want cost 2 (raw only), got %v — full map %v", floorDay, got, byDay)
	}
	spansByDay := pairs(t, body, "buckets", "bucket", "spans")
	if got := spansByDay[floorDay]; got != 1 {
		t.Errorf("floor day %s: want 1 span (raw only), got %v", floorDay, got)
	}
}

// TestHistory_HourStaysRawOnly holds the line daily_usage cannot cross: it
// buckets whole UTC days, so an hour series can only be raw. The response must
// clamp and say where its coverage starts rather than relabel day-shaped data.
func TestHistory_HourStaysRawOnly(t *testing.T) {
	db, ro := openTestDB(t)
	seedRangeFixture(t, db)
	h := api.New(ro)

	_, body := getJSON(t, h, "/api/v1/history?granularity=hour&range=all")
	if body["granularity"] != "hour" {
		t.Fatalf("granularity echo: want hour, got %v", body["granularity"])
	}
	if got := len(bucketList(t, body)); got != 2 {
		t.Errorf("buckets: want the 2 raw hours, got %d (%v)", got, body["buckets"])
	}
	if got := sumBuckets(t, body, "spans"); got != 2 {
		t.Errorf("spans: want 2 raw spans, got %v — aggregate rows leaked into an hour bucket", got)
	}
	if got := sumBuckets(t, body, "cost_usd"); got != 3 {
		t.Errorf("cost_usd: want 3, got %v", got)
	}
	for _, it := range bucketList(t, body) {
		if b := it.(map[string]any)["bucket"].(string); len(b) != len("2006-01-02 15:00") {
			t.Errorf("bucket %q is not hour-shaped", b)
		}
	}

	models := sumByModel(t, body)
	if len(models) != 1 || models["sonnet"] != 2 {
		t.Errorf("by_model: want only the raw sonnet spans, got %v", models)
	}

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

// TestHistory_HourReportsFullCoverageWhenRangeIsShort keeps the note honest in
// the other direction: a window that never reaches past the raw floor has
// nothing to disclose.
func TestHistory_HourReportsFullCoverageWhenRangeIsShort(t *testing.T) {
	db, ro := openTestDB(t)
	seedRangeFixture(t, db)
	h := api.New(ro)

	cases := map[string]bool{"day": false, "week": false, "month": false, "year": true, "all": true}
	for rangeKey, wantClamp := range cases {
		t.Run(rangeKey, func(t *testing.T) {
			_, body := getJSON(t, h, "/api/v1/history?granularity=hour&range="+rangeKey)
			if got := body["covered_since"] != nil; got != wantClamp {
				t.Errorf("covered_since set = %v, want %v (got %v)", got, wantClamp, body["covered_since"])
			}
		})
	}
}

// TestHistory_HeatmapIsAlwaysRawOnly covers the one series that stays raw at
// every granularity: it resolves hour of day, which the roll-up does not keep.
func TestHistory_HeatmapIsAlwaysRawOnly(t *testing.T) {
	db, ro := openTestDB(t)
	seedRangeFixture(t, db)
	h := api.New(ro)

	_, body := getJSON(t, h, "/api/v1/history?granularity=day&range=all")
	cells, _ := body["heatmap"].([]any)
	if len(cells) != 2 {
		t.Errorf("heatmap: want the 2 raw spans' cells, got %d (%v)", len(cells), cells)
	}
	if body["heatmap_covered_since"] == nil {
		t.Error("heatmap_covered_since: want the raw floor on range=all, got null")
	}

	_, month := getJSON(t, h, "/api/v1/history?granularity=day&range=month")
	if month["heatmap_covered_since"] != nil {
		t.Errorf("heatmap_covered_since: want null on a fully-raw window, got %v", month["heatmap_covered_since"])
	}
}

// TestHistory_ExplicitBoundsBeatRange matches the precedence /costs sets: the
// narrower statement wins and the response echoes a null range.
func TestHistory_ExplicitBoundsBeatRange(t *testing.T) {
	db, ro := openTestDB(t)
	seedRangeFixture(t, db)
	h := api.New(ro)

	from := floorNoon().UTC().Format("2006-01-02")
	_, body := getJSON(t, h, "/api/v1/history?granularity=day&range=all&from="+from)
	if body["range"] != nil {
		t.Errorf("range echo: want null when from/to win, got %v", body["range"])
	}
	if body["from"] != from {
		t.Errorf("from echo: want %q, got %v", from, body["from"])
	}
	if got := len(bucketList(t, body)); got != 2 {
		t.Errorf("buckets: want only the 2 days inside from/to, got %d (%v)", got, body["buckets"])
	}
	if got := sumBuckets(t, body, "cost_usd"); got != 3 {
		t.Errorf("cost_usd: want 3, got %v — range=all leaked past the explicit bound", got)
	}
}

// TestHistory_CoarseGranularitiesSpanTheUnion checks week and month roll the
// union's whole-day rows up rather than falling back to raw spans, and that the
// roll-up's 'unknown' sentinel never surfaces as a model.
func TestHistory_CoarseGranularitiesSpanTheUnion(t *testing.T) {
	db, ro := openTestDB(t)
	seedRangeFixture(t, db)
	addUsageRow(t, db, usageRow{
		day: floorNoon().AddDate(0, 0, -55), session: storage.UnknownSentinel,
		model: storage.UnknownSentinel, tool: storage.UnknownSentinel,
		spans: 11, cost: 1,
	})
	h := api.New(ro)

	for _, gran := range []string{"week", "month"} {
		t.Run(gran, func(t *testing.T) {
			_, body := getJSON(t, h, "/api/v1/history?range=all&granularity="+gran)
			if got := sumBuckets(t, body, "spans"); got != 21 {
				t.Errorf("spans: want 21 across the union, got %v", got)
			}
			if got := sumBuckets(t, body, "cost_usd"); got != 16 {
				t.Errorf("cost_usd: want 16 across the union, got %v", got)
			}
			if body["covered_since"] != nil {
				t.Errorf("covered_since: want null, got %v", body["covered_since"])
			}

			models := sumByModel(t, body)
			if _, ok := models[storage.UnknownSentinel]; ok {
				t.Errorf("by_model leaked the roll-up sentinel: %v", models)
			}
			var total float64
			for _, n := range models {
				total += n
			}
			if total != 10 {
				t.Errorf("by_model spans: want 10 attributed spans, got %v (%v)", total, models)
			}
		})
	}
}

// TestHistory_RangeComposesWithUserID checks the two filters intersect on both
// sides of the union rather than one overriding the other.
func TestHistory_RangeComposesWithUserID(t *testing.T) {
	db, ro := openTestDB(t)
	seedRangeFixture(t, db)
	h := api.New(ro)

	cases := []struct {
		path  string
		spans float64
		cost  float64
	}{
		{"/api/v1/history?granularity=day&range=all&user_id=alice", 2, 3},
		{"/api/v1/history?granularity=day&range=all&user_id=bob", 3, 4},
		{"/api/v1/history?granularity=day&range=all&user_id=carol", 5, 8},
		{"/api/v1/history?granularity=day&range=month&user_id=bob", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			_, body := getJSON(t, h, tc.path)
			if got := sumBuckets(t, body, "spans"); got != tc.spans {
				t.Errorf("spans: want %v, got %v", tc.spans, got)
			}
			if got := sumBuckets(t, body, "cost_usd"); got != tc.cost {
				t.Errorf("cost_usd: want %v, got %v", tc.cost, got)
			}
		})
	}
}

package storage

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func costPtr(v float64) *float64 { return &v }

// addRangeSpan inserts one raw span. An empty actor stores actor_id NULL (anonymous).
func addRangeSpan(t *testing.T, db *DB, spanID, actor, session string, start time.Time, cost float64) {
	t.Helper()
	if err := db.InsertSpan(Span{
		TraceID: "tr", SpanID: spanID, Name: "llm", SessionID: session,
		ActorID: actor, StartTime: start, EndTime: start.Add(time.Second), CostUSD: costPtr(cost),
	}); err != nil {
		t.Fatalf("InsertSpan(%s): %v", spanID, err)
	}
}

// addDailyUsageRow inserts one rolled-up aggregate row. An empty actor stores
// actor_id NULL (anonymous).
func addDailyUsageRow(t *testing.T, db *DB, day time.Time, actor, session, model string, cost float64) {
	t.Helper()
	var uid any
	if actor != "" {
		uid = actor
	}
	_, err := db.rw.Exec(`
		INSERT INTO daily_usage (day, session_id, model, tool_name, actor_id, span_count, total_cost_usd)
		VALUES (CAST(? AS DATE), ?, ?, 'tool', ?, 1, ?)
	`, day.Format("2006-01-02"), session, model, uid, cost)
	if err != nil {
		t.Fatalf("insert daily_usage: %v", err)
	}
}

func setActorCreatedAt(t *testing.T, db *DB, id string, ts time.Time) {
	t.Helper()
	if _, err := db.rw.Exec(`UPDATE actors SET created_at = ? WHERE id = ?`, ts, id); err != nil {
		t.Fatalf("set created_at: %v", err)
	}
}

func actorByName(actors []ActorWithStats, name string) (ActorWithStats, bool) {
	for _, u := range actors {
		if u.Name == name {
			return u, true
		}
	}
	return ActorWithStats{}, false
}

func actorNames(actors []ActorWithStats) []string {
	out := make([]string, len(actors))
	for i, u := range actors {
		out[i] = u.Name
	}
	return out
}

// TestListActorsPage_RangeScopesStats verifies that cost/sessions honour the
// range lower bound while created_at/last_seen stay all-time.
func TestListActorsPage_RangeScopesStats(t *testing.T) {
	db := openTestActorDB(t)
	mustCreateTestActor(t, db, "alice")
	now := time.Now()

	addRangeSpan(t, db, "s-now", "alice", "sess-now", now.Add(-1*time.Hour), 1)
	addRangeSpan(t, db, "s-3d", "alice", "sess-3d", now.AddDate(0, 0, -3), 2)
	addRangeSpan(t, db, "s-10d", "alice", "sess-10d", now.AddDate(0, 0, -10), 4)
	addRangeSpan(t, db, "s-200d", "alice", "sess-200d", now.AddDate(0, 0, -200), 8)

	cases := []struct {
		name         string
		since        *time.Time
		wantCost     float64
		wantSessions int64
	}{
		{"day", ptrTime(now.Add(-24 * time.Hour)), 1, 1},
		{"week", ptrTime(now.AddDate(0, 0, -7)), 3, 2},
		{"month", ptrTime(now.AddDate(0, 0, -30)), 7, 3},
		{"all", nil, 15, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actors, _, err := db.ListActorsPage(ListActorsOptions{Since: tc.since})
			if err != nil {
				t.Fatalf("ListActorsPage: %v", err)
			}
			u, ok := actorByName(actors, "alice")
			if !ok {
				t.Fatal("alice missing from list")
			}
			if u.TotalCostUSD != tc.wantCost {
				t.Errorf("cost: want %v, got %v", tc.wantCost, u.TotalCostUSD)
			}
			if u.Sessions != tc.wantSessions {
				t.Errorf("sessions: want %v, got %v", tc.wantSessions, u.Sessions)
			}
		})
	}
}

// TestListActorsPage_UnionBoundaryNoDoubleCount models the snapped (whole-day)
// roll-up: raw_floor's day is fully raw, so a daily_usage row on that same day is
// stale and must be excluded by the strict `day < raw_floor` bound (else it would
// double count against the raw floor-day spans). A strictly-earlier aggregate day
// is included.
func TestListActorsPage_UnionBoundaryNoDoubleCount(t *testing.T) {
	db := openTestActorDB(t)
	mustCreateTestActor(t, db, "alice")
	// Anchor to midnight UTC so the raw span and the floor-day aggregate share a
	// calendar day regardless of the current time of day.
	today := time.Now().UTC().Truncate(24 * time.Hour)
	floor := today.AddDate(0, 0, -5)

	// The only raw span (noon on floor's day), so raw_floor's day is floor's day.
	addRangeSpan(t, db, "b-raw", "alice", "sess-floor", floor.Add(12*time.Hour), 3)
	// Stale aggregate on the same day as raw_floor — must be excluded by `<`.
	addDailyUsageRow(t, db, floor, "alice", "sess-floor-agg", "m1", 5)
	// A strictly-earlier aggregate day — included.
	addDailyUsageRow(t, db, floor.AddDate(0, 0, -2), "alice", "sess-older", "m1", 7)

	actors, _, err := db.ListActorsPage(ListActorsOptions{})
	if err != nil {
		t.Fatalf("ListActorsPage: %v", err)
	}
	u, ok := actorByName(actors, "alice")
	if !ok {
		t.Fatal("alice missing")
	}
	if u.TotalCostUSD != 10 { // 3 (raw floor day) + 7 (earlier agg); floor-day agg 5 excluded
		t.Errorf("cost: want 10 (strict boundary, no double count), got %v", u.TotalCostUSD)
	}
	if u.Sessions != 2 { // {sess-floor (raw), sess-older (agg)}
		t.Errorf("sessions: want 2, got %v", u.Sessions)
	}
}

// TestListActorsPage_AggregateRangeLowerBound checks that the range lower bound
// is applied to daily_usage (day >= since) as well as raw spans.
func TestListActorsPage_AggregateRangeLowerBound(t *testing.T) {
	db := openTestActorDB(t)
	mustCreateTestActor(t, db, "alice")
	now := time.Now()

	addRangeSpan(t, db, "s-now", "alice", "sess-now", now.Add(-1*time.Hour), 1)
	addDailyUsageRow(t, db, now.AddDate(0, 0, -10), "alice", "sess-mid", "m1", 50)
	addDailyUsageRow(t, db, now.AddDate(0, 0, -40), "alice", "sess-old", "m1", 100)

	month, _, err := db.ListActorsPage(ListActorsOptions{Since: ptrTime(now.AddDate(0, 0, -30))})
	if err != nil {
		t.Fatalf("month: %v", err)
	}
	u, _ := actorByName(month, "alice")
	if u.TotalCostUSD != 51 || u.Sessions != 2 { // now (1) + mid (50); old excluded
		t.Errorf("month: want cost 51 sessions 2, got %v/%v", u.TotalCostUSD, u.Sessions)
	}

	all, _, err := db.ListActorsPage(ListActorsOptions{})
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	ua, _ := actorByName(all, "alice")
	if ua.TotalCostUSD != 151 || ua.Sessions != 3 {
		t.Errorf("all: want cost 151 sessions 3, got %v/%v", ua.TotalCostUSD, ua.Sessions)
	}
}

// TestListActorsPage_SortAllKeys sorts by every key in both directions.
func TestListActorsPage_SortAllKeys(t *testing.T) {
	db := openTestActorDB(t)
	a := mustCreateTestActor(t, db, "a-actor")
	b := mustCreateTestActor(t, db, "b-actor")
	c := mustCreateTestActor(t, db, "c-actor")

	base := time.Now().Add(-48 * time.Hour)
	setActorCreatedAt(t, db, a.ID, base)
	setActorCreatedAt(t, db, b.ID, base.Add(time.Hour))
	setActorCreatedAt(t, db, c.ID, base.Add(2*time.Hour))

	now := time.Now()
	// cost: a=10, b=30, c=20 ; sessions: a=1, b=3, c=2 ; last_seen: a>b>c
	addRangeSpan(t, db, "a1", "a-actor", "a1", now, 10)
	addRangeSpan(t, db, "b1", "b-actor", "b1", now.Add(-1*time.Hour), 10)
	addRangeSpan(t, db, "b2", "b-actor", "b2", now.Add(-1*time.Hour), 10)
	addRangeSpan(t, db, "b3", "b-actor", "b3", now.Add(-1*time.Hour), 10)
	addRangeSpan(t, db, "c1", "c-actor", "c1", now.Add(-2*time.Hour), 10)
	addRangeSpan(t, db, "c2", "c-actor", "c2", now.Add(-2*time.Hour), 10)

	cases := []struct {
		sort, order string
		want        []string
	}{
		{"name", "asc", []string{"a-actor", "b-actor", "c-actor"}},
		{"name", "desc", []string{"c-actor", "b-actor", "a-actor"}},
		{"created_at", "asc", []string{"a-actor", "b-actor", "c-actor"}},
		{"created_at", "desc", []string{"c-actor", "b-actor", "a-actor"}},
		{"cost", "asc", []string{"a-actor", "c-actor", "b-actor"}},
		{"cost", "desc", []string{"b-actor", "c-actor", "a-actor"}},
		{"sessions", "asc", []string{"a-actor", "c-actor", "b-actor"}},
		{"sessions", "desc", []string{"b-actor", "c-actor", "a-actor"}},
		{"last_seen", "asc", []string{"c-actor", "b-actor", "a-actor"}},
		{"last_seen", "desc", []string{"a-actor", "b-actor", "c-actor"}},
	}
	for _, tc := range cases {
		t.Run(tc.sort+"_"+tc.order, func(t *testing.T) {
			actors, _, err := db.ListActorsPage(ListActorsOptions{Sort: tc.sort, Order: tc.order})
			if err != nil {
				t.Fatalf("ListActorsPage: %v", err)
			}
			if got := actorNames(actors); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("sort=%s order=%s: want %v, got %v", tc.sort, tc.order, tc.want, got)
			}
		})
	}
}

// TestListActorsPage_PagesConcatToFullList proves the sort order is global: page 1
// followed by page 2 equals the full sorted list, not a re-sorted page.
func TestListActorsPage_PagesConcatToFullList(t *testing.T) {
	db := openTestActorDB(t)
	now := time.Now()
	for i, name := range []string{"u1", "u2", "u3", "u4", "u5"} {
		mustCreateTestActor(t, db, name)
		addRangeSpan(t, db, "sp-"+name, name, "sess-"+name, now, float64((i+1)*10))
	}

	full, total, err := db.ListActorsPage(ListActorsOptions{Sort: "cost", Order: "desc"})
	if err != nil {
		t.Fatalf("full: %v", err)
	}
	if total != 5 || len(full) != 5 {
		t.Fatalf("full list: want 5 rows/total, got %d rows total=%d", len(full), total)
	}

	var paged []ActorWithStats
	for page := 1; page <= 3; page++ {
		rows, tot, err := db.ListActorsPage(ListActorsOptions{Sort: "cost", Order: "desc", Page: page, Limit: 2})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if tot != 5 {
			t.Errorf("page %d: total want 5, got %d", page, tot)
		}
		paged = append(paged, rows...)
	}

	if !reflect.DeepEqual(actorNames(full), actorNames(paged)) {
		t.Errorf("page concat != full list:\n full=%v\npaged=%v", actorNames(full), actorNames(paged))
	}
}

// TestListActorsPage_QueryFilter filters on a case-insensitive name substring.
func TestListActorsPage_QueryFilter(t *testing.T) {
	db := openTestActorDB(t)
	for _, name := range []string{"alice", "alicia", "bob"} {
		mustCreateTestActor(t, db, name)
	}

	actors, total, err := db.ListActorsPage(ListActorsOptions{Query: "ALI"})
	if err != nil {
		t.Fatalf("ListActorsPage: %v", err)
	}
	if total != 2 || len(actors) != 2 {
		t.Fatalf("q=ALI: want 2 rows/total, got %d rows total=%d (%v)", len(actors), total, actorNames(actors))
	}
	if _, ok := actorByName(actors, "alice"); !ok {
		t.Error("q=ALI must match alice")
	}
	if _, ok := actorByName(actors, "alicia"); !ok {
		t.Error("q=ALI must match alicia")
	}
	if _, ok := actorByName(actors, "bob"); ok {
		t.Error("q=ALI must not match bob")
	}
}

// TestListActorsPage_AnonymousSortsInline proves the __anonymous__ row is produced
// in SQL and sorts by its own stats, not appended at the end.
func TestListActorsPage_AnonymousSortsInline(t *testing.T) {
	db := openTestActorDB(t)
	mustCreateTestActor(t, db, "low")
	mustCreateTestActor(t, db, "high")
	now := time.Now()
	addRangeSpan(t, db, "low1", "low", "sess-low", now, 10)
	addRangeSpan(t, db, "high1", "high", "sess-high", now, 100)
	addRangeSpan(t, db, "anon1", "", "sess-anon", now, 50) // anonymous, between low and high

	actors, _, err := db.ListActorsPage(ListActorsOptions{Sort: "cost", Order: "desc"})
	if err != nil {
		t.Fatalf("ListActorsPage: %v", err)
	}
	want := []string{"high", "Anonymous", "low"}
	if got := actorNames(actors); !reflect.DeepEqual(got, want) {
		t.Errorf("anonymous not sorted inline: want %v, got %v", want, got)
	}
}

// TestListActorsPage_AnonymousSurvivesRollup covers the anonymous bucket after
// retention has purged its raw spans: the cost lives only in daily_usage, and
// the row must still be listed — GetActorWithStats reports it either way, so
// gating the list row on raw spans alone would make the two endpoints disagree.
func TestListActorsPage_AnonymousSurvivesRollup(t *testing.T) {
	db := openTestActorDB(t)
	mustCreateTestActor(t, db, "alice")
	today := time.Now().UTC().Truncate(24 * time.Hour)

	// alice's raw span is the only one left, so raw_floor lands on today.
	addRangeSpan(t, db, "a-raw", "alice", "sess-alice", today.Add(12*time.Hour), 10)
	// Anonymous data exists only as a strictly-earlier aggregate.
	addDailyUsageRow(t, db, today.AddDate(0, 0, -2), "", "sess-anon", "m1", 50)

	actors, total, err := db.ListActorsPage(ListActorsOptions{Sort: "cost", Order: "desc"})
	if err != nil {
		t.Fatalf("ListActorsPage: %v", err)
	}
	if total != 2 {
		t.Errorf("total: want 2 (alice + anonymous), got %d", total)
	}
	u, ok := actorByName(actors, "Anonymous")
	if !ok {
		t.Fatalf("anonymous row dropped after roll-up: got %v", actorNames(actors))
	}
	if u.TotalCostUSD != 50 {
		t.Errorf("anonymous cost: want 50, got %v", u.TotalCostUSD)
	}
	if u.Sessions != 1 {
		t.Errorf("anonymous sessions: want 1, got %v", u.Sessions)
	}

	one, err := db.GetActorWithStats(AnonymousActorID, nil)
	if err != nil {
		t.Fatalf("GetActorWithStats(anonymous): %v", err)
	}
	if one.TotalCostUSD != u.TotalCostUSD || one.Sessions != u.Sessions {
		t.Errorf("detail disagrees with list: detail %v/%v, list %v/%v",
			one.TotalCostUSD, one.Sessions, u.TotalCostUSD, u.Sessions)
	}
}

// TestListActorsPage_LimitZeroReturnsAll confirms limit=0 disables paging.
func TestListActorsPage_LimitZeroReturnsAll(t *testing.T) {
	db := openTestActorDB(t)
	for _, name := range []string{"u1", "u2", "u3"} {
		mustCreateTestActor(t, db, name)
	}
	now := time.Now()
	addRangeSpan(t, db, "anon", "", "sess-anon", now, 1)

	actors, total, err := db.ListActorsPage(ListActorsOptions{Limit: 0})
	if err != nil {
		t.Fatalf("ListActorsPage: %v", err)
	}
	if len(actors) != 4 || total != 4 { // 3 actors + anonymous
		t.Errorf("limit=0: want 4 rows/total, got %d rows total=%d", len(actors), total)
	}
}

// TestListActorsPage_PagePastEndKeepsTotal pins that total stays the match count
// on an out-of-range page, so a client can still tell how many pages exist.
func TestListActorsPage_PagePastEndKeepsTotal(t *testing.T) {
	db := openTestActorDB(t)
	for _, name := range []string{"u1", "u2", "u3"} {
		mustCreateTestActor(t, db, name)
	}

	actors, total, err := db.ListActorsPage(ListActorsOptions{Limit: 2, Page: 99})
	if err != nil {
		t.Fatalf("ListActorsPage: %v", err)
	}
	if len(actors) != 0 {
		t.Errorf("page past end: want 0 rows, got %d", len(actors))
	}
	if total != 3 {
		t.Errorf("page past end: want total=3, got %d", total)
	}

	_, qTotal, err := db.ListActorsPage(ListActorsOptions{Query: "u1", Limit: 2, Page: 99})
	if err != nil {
		t.Fatalf("ListActorsPage(q): %v", err)
	}
	if qTotal != 1 {
		t.Errorf("page past end with q: want total=1, got %d", qTotal)
	}
}

// TestGetActorWithStats covers named actors, range scoping, anonymous, and 404s.
func TestGetActorWithStats(t *testing.T) {
	db := openTestActorDB(t)
	u := mustCreateTestActor(t, db, "alice")
	now := time.Now()
	addRangeSpan(t, db, "recent", "alice", "sess-recent", now.Add(-1*time.Hour), 5)
	addRangeSpan(t, db, "old", "alice", "sess-old", now.AddDate(0, 0, -60), 20)
	addRangeSpan(t, db, "anon", "", "sess-anon", now, 7)

	t.Run("named month-scoped", func(t *testing.T) {
		got, err := db.GetActorWithStats(u.ID, ptrTime(now.AddDate(0, 0, -30)))
		if err != nil {
			t.Fatalf("GetActorWithStats: %v", err)
		}
		if got.Name != "alice" || got.Token != u.Token {
			t.Errorf("identity mismatch: %+v", got.Actor)
		}
		if got.TotalCostUSD != 5 || got.Sessions != 1 {
			t.Errorf("month scope: want cost 5 sessions 1, got %v/%v", got.TotalCostUSD, got.Sessions)
		}
	})

	t.Run("named all-time", func(t *testing.T) {
		got, err := db.GetActorWithStats(u.ID, nil)
		if err != nil {
			t.Fatalf("GetActorWithStats: %v", err)
		}
		if got.TotalCostUSD != 25 || got.Sessions != 2 {
			t.Errorf("all-time: want cost 25 sessions 2, got %v/%v", got.TotalCostUSD, got.Sessions)
		}
	})

	t.Run("anonymous", func(t *testing.T) {
		got, err := db.GetActorWithStats(AnonymousActorID, nil)
		if err != nil {
			t.Fatalf("GetActorWithStats(anon): %v", err)
		}
		if got.ID != AnonymousActorID || got.TotalCostUSD != 7 || got.Sessions != 1 {
			t.Errorf("anon: want cost 7 sessions 1, got %+v", got)
		}
	})

	t.Run("unknown 404", func(t *testing.T) {
		if _, err := db.GetActorWithStats("no-such-id", nil); !errors.Is(err, ErrNotFound) {
			t.Errorf("unknown actor: want ErrNotFound, got %v", err)
		}
	})

	t.Run("soft-deleted 404", func(t *testing.T) {
		if err := db.SoftDeleteActor(u.ID); err != nil {
			t.Fatalf("SoftDeleteActor: %v", err)
		}
		if _, err := db.GetActorWithStats(u.ID, nil); !errors.Is(err, ErrNotFound) {
			t.Errorf("soft-deleted actor: want ErrNotFound, got %v", err)
		}
	})
}

func ptrTime(t time.Time) *time.Time { return &t }

// A span stored without a session id (Codex and Grok emit most of theirs that
// way) is not a session: before this was counted, an actor whose only spans were
// sessionless showed one session on the Actors page and none on Sessions.
func TestActorSessionsIgnoreEmptySessionID(t *testing.T) {
	db := openTestActorDB(t)
	u := mustCreateTestActor(t, db, "codex")
	now := time.Now()
	addRangeSpan(t, db, "noise-1", "codex", "", now.Add(-time.Hour), 0)
	addRangeSpan(t, db, "noise-2", "codex", "", now.Add(-time.Hour), 0)

	got, err := db.GetActorWithStats(u.ID, nil)
	if err != nil {
		t.Fatalf("GetActorWithStats: %v", err)
	}
	if got.Sessions != 0 {
		t.Errorf("GetActorWithStats: want 0 sessions, got %d", got.Sessions)
	}

	addRangeSpan(t, db, "turn", "codex", "conv-1", now.Add(-time.Hour), 1)
	actors, _, err := db.ListActorsPage(ListActorsOptions{})
	if err != nil {
		t.Fatalf("ListActorsPage: %v", err)
	}
	row, ok := actorByName(actors, "codex")
	if !ok {
		t.Fatalf("codex missing from %v", actorNames(actors))
	}
	if row.Sessions != 1 {
		t.Errorf("ListActorsPage: want 1 session, got %d", row.Sessions)
	}
}

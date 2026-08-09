package storage

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func costPtr(v float64) *float64 { return &v }

// addRangeSpan inserts one raw span. An empty user stores user_id NULL (anonymous).
func addRangeSpan(t *testing.T, db *DB, spanID, user, session string, start time.Time, cost float64) {
	t.Helper()
	if err := db.InsertSpan(Span{
		TraceID: "tr", SpanID: spanID, Name: "llm", SessionID: session,
		UserID: user, StartTime: start, EndTime: start.Add(time.Second), CostUSD: costPtr(cost),
	}); err != nil {
		t.Fatalf("InsertSpan(%s): %v", spanID, err)
	}
}

// addDailyUsageRow inserts one rolled-up aggregate row. An empty user stores
// user_id NULL (anonymous).
func addDailyUsageRow(t *testing.T, db *DB, day time.Time, user, session, model string, cost float64) {
	t.Helper()
	var uid any
	if user != "" {
		uid = user
	}
	_, err := db.rw.Exec(`
		INSERT INTO daily_usage (day, session_id, model, tool_name, user_id, span_count, total_cost_usd)
		VALUES (CAST(? AS DATE), ?, ?, 'tool', ?, 1, ?)
	`, day.Format("2006-01-02"), session, model, uid, cost)
	if err != nil {
		t.Fatalf("insert daily_usage: %v", err)
	}
}

func setUserCreatedAt(t *testing.T, db *DB, id string, ts time.Time) {
	t.Helper()
	if _, err := db.rw.Exec(`UPDATE users SET created_at = ? WHERE id = ?`, ts, id); err != nil {
		t.Fatalf("set created_at: %v", err)
	}
}

func userByName(users []UserWithStats, name string) (UserWithStats, bool) {
	for _, u := range users {
		if u.Name == name {
			return u, true
		}
	}
	return UserWithStats{}, false
}

func userNames(users []UserWithStats) []string {
	out := make([]string, len(users))
	for i, u := range users {
		out[i] = u.Name
	}
	return out
}

// TestListUsersPage_RangeScopesStats verifies that cost/sessions honour the
// range lower bound while created_at/last_seen stay all-time.
func TestListUsersPage_RangeScopesStats(t *testing.T) {
	db := openTestUserDB(t)
	mustCreateTestUser(t, db, "alice")
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
			users, _, err := db.ListUsersPage(ListUsersOptions{Since: tc.since})
			if err != nil {
				t.Fatalf("ListUsersPage: %v", err)
			}
			u, ok := userByName(users, "alice")
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

// TestListUsersPage_UnionBoundaryNoDoubleCount seeds a boundary day whose early
// slice lives only in daily_usage and late slice only in spans, plus a strictly
// earlier aggregate day. The boundary day must be included exactly once (CAST <=
// raw_floor, not <) and a session straddling the boundary counted once.
func TestListUsersPage_UnionBoundaryNoDoubleCount(t *testing.T) {
	db := openTestUserDB(t)
	mustCreateTestUser(t, db, "alice")
	now := time.Now()
	boundary := now.AddDate(0, 0, -5)

	// Late slice of the boundary day: the only raw span, so raw_floor == boundary.
	addRangeSpan(t, db, "b-late", "alice", "sess-boundary", boundary.Add(18*time.Hour), 3)
	// Early slice of the same boundary day, already rolled up (same session).
	addDailyUsageRow(t, db, boundary, "alice", "sess-boundary", "m1", 5)
	// A strictly earlier aggregate day.
	addDailyUsageRow(t, db, boundary.AddDate(0, 0, -2), "alice", "sess-older", "m1", 7)

	users, _, err := db.ListUsersPage(ListUsersOptions{})
	if err != nil {
		t.Fatalf("ListUsersPage: %v", err)
	}
	u, ok := userByName(users, "alice")
	if !ok {
		t.Fatal("alice missing")
	}
	if u.TotalCostUSD != 15 { // 3 (raw) + 5 (agg boundary) + 7 (agg older)
		t.Errorf("cost: want 15 (no drop, no double count), got %v", u.TotalCostUSD)
	}
	if u.Sessions != 2 { // {sess-boundary, sess-older}; straddling session counts once
		t.Errorf("sessions: want 2, got %v", u.Sessions)
	}
}

// TestListUsersPage_SortAllKeys sorts by every key in both directions.
func TestListUsersPage_SortAllKeys(t *testing.T) {
	db := openTestUserDB(t)
	a := mustCreateTestUser(t, db, "a-user")
	b := mustCreateTestUser(t, db, "b-user")
	c := mustCreateTestUser(t, db, "c-user")

	base := time.Now().Add(-48 * time.Hour)
	setUserCreatedAt(t, db, a.ID, base)
	setUserCreatedAt(t, db, b.ID, base.Add(time.Hour))
	setUserCreatedAt(t, db, c.ID, base.Add(2*time.Hour))

	now := time.Now()
	// cost: a=10, b=30, c=20 ; sessions: a=1, b=3, c=2 ; last_seen: a>b>c
	addRangeSpan(t, db, "a1", "a-user", "a1", now, 10)
	addRangeSpan(t, db, "b1", "b-user", "b1", now.Add(-1*time.Hour), 10)
	addRangeSpan(t, db, "b2", "b-user", "b2", now.Add(-1*time.Hour), 10)
	addRangeSpan(t, db, "b3", "b-user", "b3", now.Add(-1*time.Hour), 10)
	addRangeSpan(t, db, "c1", "c-user", "c1", now.Add(-2*time.Hour), 10)
	addRangeSpan(t, db, "c2", "c-user", "c2", now.Add(-2*time.Hour), 10)

	cases := []struct {
		sort, order string
		want        []string
	}{
		{"name", "asc", []string{"a-user", "b-user", "c-user"}},
		{"name", "desc", []string{"c-user", "b-user", "a-user"}},
		{"created_at", "asc", []string{"a-user", "b-user", "c-user"}},
		{"created_at", "desc", []string{"c-user", "b-user", "a-user"}},
		{"cost", "asc", []string{"a-user", "c-user", "b-user"}},
		{"cost", "desc", []string{"b-user", "c-user", "a-user"}},
		{"sessions", "asc", []string{"a-user", "c-user", "b-user"}},
		{"sessions", "desc", []string{"b-user", "c-user", "a-user"}},
		{"last_seen", "asc", []string{"c-user", "b-user", "a-user"}},
		{"last_seen", "desc", []string{"a-user", "b-user", "c-user"}},
	}
	for _, tc := range cases {
		t.Run(tc.sort+"_"+tc.order, func(t *testing.T) {
			users, _, err := db.ListUsersPage(ListUsersOptions{Sort: tc.sort, Order: tc.order})
			if err != nil {
				t.Fatalf("ListUsersPage: %v", err)
			}
			if got := userNames(users); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("sort=%s order=%s: want %v, got %v", tc.sort, tc.order, tc.want, got)
			}
		})
	}
}

// TestListUsersPage_PagesConcatToFullList proves the sort order is global: page 1
// followed by page 2 equals the full sorted list, not a re-sorted page.
func TestListUsersPage_PagesConcatToFullList(t *testing.T) {
	db := openTestUserDB(t)
	now := time.Now()
	for i, name := range []string{"u1", "u2", "u3", "u4", "u5"} {
		mustCreateTestUser(t, db, name)
		addRangeSpan(t, db, "sp-"+name, name, "sess-"+name, now, float64((i+1)*10))
	}

	full, total, err := db.ListUsersPage(ListUsersOptions{Sort: "cost", Order: "desc"})
	if err != nil {
		t.Fatalf("full: %v", err)
	}
	if total != 5 || len(full) != 5 {
		t.Fatalf("full list: want 5 rows/total, got %d rows total=%d", len(full), total)
	}

	var paged []UserWithStats
	for page := 1; page <= 3; page++ {
		rows, tot, err := db.ListUsersPage(ListUsersOptions{Sort: "cost", Order: "desc", Page: page, Limit: 2})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if tot != 5 {
			t.Errorf("page %d: total want 5, got %d", page, tot)
		}
		paged = append(paged, rows...)
	}

	if !reflect.DeepEqual(userNames(full), userNames(paged)) {
		t.Errorf("page concat != full list:\n full=%v\npaged=%v", userNames(full), userNames(paged))
	}
}

// TestListUsersPage_QueryFilter filters on a case-insensitive name substring.
func TestListUsersPage_QueryFilter(t *testing.T) {
	db := openTestUserDB(t)
	for _, name := range []string{"alice", "alicia", "bob"} {
		mustCreateTestUser(t, db, name)
	}

	users, total, err := db.ListUsersPage(ListUsersOptions{Query: "ALI"})
	if err != nil {
		t.Fatalf("ListUsersPage: %v", err)
	}
	if total != 2 || len(users) != 2 {
		t.Fatalf("q=ALI: want 2 rows/total, got %d rows total=%d (%v)", len(users), total, userNames(users))
	}
	if _, ok := userByName(users, "alice"); !ok {
		t.Error("q=ALI must match alice")
	}
	if _, ok := userByName(users, "alicia"); !ok {
		t.Error("q=ALI must match alicia")
	}
	if _, ok := userByName(users, "bob"); ok {
		t.Error("q=ALI must not match bob")
	}
}

// TestListUsersPage_AnonymousSortsInline proves the __anonymous__ row is produced
// in SQL and sorts by its own stats, not appended at the end.
func TestListUsersPage_AnonymousSortsInline(t *testing.T) {
	db := openTestUserDB(t)
	mustCreateTestUser(t, db, "low")
	mustCreateTestUser(t, db, "high")
	now := time.Now()
	addRangeSpan(t, db, "low1", "low", "sess-low", now, 10)
	addRangeSpan(t, db, "high1", "high", "sess-high", now, 100)
	addRangeSpan(t, db, "anon1", "", "sess-anon", now, 50) // anonymous, between low and high

	users, _, err := db.ListUsersPage(ListUsersOptions{Sort: "cost", Order: "desc"})
	if err != nil {
		t.Fatalf("ListUsersPage: %v", err)
	}
	want := []string{"high", "Anonymous", "low"}
	if got := userNames(users); !reflect.DeepEqual(got, want) {
		t.Errorf("anonymous not sorted inline: want %v, got %v", want, got)
	}
}

// TestListUsersPage_LimitZeroReturnsAll confirms limit=0 disables paging.
func TestListUsersPage_LimitZeroReturnsAll(t *testing.T) {
	db := openTestUserDB(t)
	for _, name := range []string{"u1", "u2", "u3"} {
		mustCreateTestUser(t, db, name)
	}
	now := time.Now()
	addRangeSpan(t, db, "anon", "", "sess-anon", now, 1)

	users, total, err := db.ListUsersPage(ListUsersOptions{Limit: 0})
	if err != nil {
		t.Fatalf("ListUsersPage: %v", err)
	}
	if len(users) != 4 || total != 4 { // 3 users + anonymous
		t.Errorf("limit=0: want 4 rows/total, got %d rows total=%d", len(users), total)
	}
}

// TestGetUserWithStats covers named users, range scoping, anonymous, and 404s.
func TestGetUserWithStats(t *testing.T) {
	db := openTestUserDB(t)
	u := mustCreateTestUser(t, db, "alice")
	now := time.Now()
	addRangeSpan(t, db, "recent", "alice", "sess-recent", now.Add(-1*time.Hour), 5)
	addRangeSpan(t, db, "old", "alice", "sess-old", now.AddDate(0, 0, -60), 20)
	addRangeSpan(t, db, "anon", "", "sess-anon", now, 7)

	t.Run("named month-scoped", func(t *testing.T) {
		got, err := db.GetUserWithStats(u.ID, ptrTime(now.AddDate(0, 0, -30)))
		if err != nil {
			t.Fatalf("GetUserWithStats: %v", err)
		}
		if got.Name != "alice" || got.Token != u.Token {
			t.Errorf("identity mismatch: %+v", got.User)
		}
		if got.TotalCostUSD != 5 || got.Sessions != 1 {
			t.Errorf("month scope: want cost 5 sessions 1, got %v/%v", got.TotalCostUSD, got.Sessions)
		}
	})

	t.Run("named all-time", func(t *testing.T) {
		got, err := db.GetUserWithStats(u.ID, nil)
		if err != nil {
			t.Fatalf("GetUserWithStats: %v", err)
		}
		if got.TotalCostUSD != 25 || got.Sessions != 2 {
			t.Errorf("all-time: want cost 25 sessions 2, got %v/%v", got.TotalCostUSD, got.Sessions)
		}
	})

	t.Run("anonymous", func(t *testing.T) {
		got, err := db.GetUserWithStats(AnonymousUserID, nil)
		if err != nil {
			t.Fatalf("GetUserWithStats(anon): %v", err)
		}
		if got.ID != AnonymousUserID || got.TotalCostUSD != 7 || got.Sessions != 1 {
			t.Errorf("anon: want cost 7 sessions 1, got %+v", got)
		}
	})

	t.Run("unknown 404", func(t *testing.T) {
		if _, err := db.GetUserWithStats("no-such-id", nil); !errors.Is(err, ErrNotFound) {
			t.Errorf("unknown user: want ErrNotFound, got %v", err)
		}
	})

	t.Run("soft-deleted 404", func(t *testing.T) {
		if err := db.SoftDeleteUser(u.ID); err != nil {
			t.Fatalf("SoftDeleteUser: %v", err)
		}
		if _, err := db.GetUserWithStats(u.ID, nil); !errors.Is(err, ErrNotFound) {
			t.Errorf("soft-deleted user: want ErrNotFound, got %v", err)
		}
	})
}

func ptrTime(t time.Time) *time.Time { return &t }

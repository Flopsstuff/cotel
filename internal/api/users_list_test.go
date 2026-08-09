package api_test

import (
	"testing"
	"time"

	"github.com/Flopsstuff/cotel/internal/storage"
)

func addCostSpan(t *testing.T, db *storage.DB, spanID, user, session string, cost float64) {
	t.Helper()
	now := time.Now()
	insertSpan(t, db, storage.Span{
		TraceID: "tr", SpanID: spanID, Name: "llm", SessionID: session,
		UserID: user, StartTime: now.Add(-1 * time.Hour), EndTime: now.Add(-1 * time.Hour).Add(time.Second),
		CostUSD: ptr(cost),
	})
}

// TestListUsers_EchoAndDefaults confirms the response carries the echo fields
// with their defaults when no query parameters are supplied.
func TestListUsers_EchoAndDefaults(t *testing.T) {
	db, ro := openTestDB(t)
	h := newUserHandler(t, db, ro)
	createAPIUser(t, db, "alice")

	code, body := getJSON(t, h, "/api/v1/users")
	if code != 200 {
		t.Fatalf("want 200, got %d", code)
	}
	if body["range"] != "month" || body["sort"] != "cost" || body["order"] != "desc" {
		t.Errorf("defaults: range/sort/order = %v/%v/%v", body["range"], body["sort"], body["order"])
	}
	if body["limit"].(float64) != 0 {
		t.Errorf("default limit: want 0, got %v", body["limit"])
	}
	if body["total"].(float64) != 1 {
		t.Errorf("total: want 1, got %v", body["total"])
	}
}

// TestListUsers_GlobalSortAcrossPages proves paging sits on top of a global sort:
// page 1 (limit 2, cost desc) holds the two globally most expensive users.
func TestListUsers_GlobalSortAcrossPages(t *testing.T) {
	db, ro := openTestDB(t)
	h := newUserHandler(t, db, ro)
	createAPIUser(t, db, "cheap")
	createAPIUser(t, db, "mid")
	createAPIUser(t, db, "pricey")
	addCostSpan(t, db, "c1", "cheap", "sc", 10)
	addCostSpan(t, db, "m1", "mid", "sm", 20)
	addCostSpan(t, db, "p1", "pricey", "sp", 30)

	_, body := getJSON(t, h, "/api/v1/users?range=all&sort=cost&order=desc&page=1&limit=2")
	users, _ := body["users"].([]any)
	if len(users) != 2 {
		t.Fatalf("page 1 limit 2: want 2 users, got %d", len(users))
	}
	if body["total"].(float64) != 3 {
		t.Errorf("total: want 3, got %v", body["total"])
	}
	first := users[0].(map[string]any)
	second := users[1].(map[string]any)
	if first["name"] != "pricey" || second["name"] != "mid" {
		t.Errorf("global sort broken: page 1 = %v, %v", first["name"], second["name"])
	}
}

// TestListUsers_QueryFilter filters server-side on name.
func TestListUsers_QueryFilter(t *testing.T) {
	db, ro := openTestDB(t)
	h := newUserHandler(t, db, ro)
	createAPIUser(t, db, "alice")
	createAPIUser(t, db, "bob")

	_, body := getJSON(t, h, "/api/v1/users?q=ali")
	users, _ := body["users"].([]any)
	if len(users) != 1 || body["total"].(float64) != 1 {
		t.Fatalf("q=ali: want 1 user/total, got %d users total=%v", len(users), body["total"])
	}
	if users[0].(map[string]any)["name"] != "alice" {
		t.Errorf("q=ali: want alice, got %v", users[0].(map[string]any)["name"])
	}
}

// TestListUsers_InvalidParamsFallBack confirms bad values fall back to defaults
// rather than erroring (trust the boundary).
func TestListUsers_InvalidParamsFallBack(t *testing.T) {
	db, ro := openTestDB(t)
	h := newUserHandler(t, db, ro)
	createAPIUser(t, db, "alice")

	code, body := getJSON(t, h, "/api/v1/users?range=decade&sort=whatever&order=sideways")
	if code != 200 {
		t.Fatalf("want 200 (no 400), got %d", code)
	}
	if body["range"] != "month" || body["sort"] != "cost" || body["order"] != "desc" {
		t.Errorf("fallbacks: range/sort/order = %v/%v/%v", body["range"], body["sort"], body["order"])
	}
}

// TestGetUser_ByID returns a single user and 404s for unknown ids.
func TestGetUser_ByID(t *testing.T) {
	db, ro := openTestDB(t)
	h := newUserHandler(t, db, ro)
	u := createAPIUser(t, db, "alice")
	addCostSpan(t, db, "a1", "alice", "sa", 12)

	code, body := getJSON(t, h, "/api/v1/users/"+u.ID+"?range=all")
	if code != 200 {
		t.Fatalf("want 200, got %d", code)
	}
	if body["name"] != "alice" || body["token"] != u.Token {
		t.Errorf("identity mismatch: %v", body)
	}
	if body["cost"].(float64) != 12 {
		t.Errorf("cost: want 12, got %v", body["cost"])
	}

	if code, _ := getJSON(t, h, "/api/v1/users/no-such-id"); code != 404 {
		t.Errorf("unknown id: want 404, got %d", code)
	}
}

// TestGetUser_Anonymous resolves the synthetic anonymous row.
func TestGetUser_Anonymous(t *testing.T) {
	db, ro := openTestDB(t)
	h := newUserHandler(t, db, ro)
	addCostSpan(t, db, "anon1", "", "sanon", 9)

	code, body := getJSON(t, h, "/api/v1/users/__anonymous__?range=all")
	if code != 200 {
		t.Fatalf("want 200, got %d", code)
	}
	if body["id"] != "__anonymous__" || body["cost"].(float64) != 9 {
		t.Errorf("anon: got %v", body)
	}
}

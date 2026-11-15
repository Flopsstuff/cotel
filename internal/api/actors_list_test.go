package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Flopsstuff/cotel/internal/storage"
)

func addCostSpan(t *testing.T, db *storage.DB, spanID, actor, session string, cost float64) {
	t.Helper()
	now := time.Now()
	insertSpan(t, db, storage.Span{
		TraceID: "tr", SpanID: spanID, Name: "llm", SessionID: session,
		ActorID: actor, StartTime: now.Add(-1 * time.Hour), EndTime: now.Add(-1 * time.Hour).Add(time.Second),
		CostUSD: ptr(cost),
	})
}

// TestListActors_EchoAndDefaults confirms the response carries the echo fields
// with their defaults when no query parameters are supplied.
func TestListActors_EchoAndDefaults(t *testing.T) {
	db, ro := openTestDB(t)
	h := newActorHandler(t, db, ro)
	createAPIActor(t, db, "alice")

	code, body := getJSON(t, h, "/api/v1/actors")
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

// TestListActors_GlobalSortAcrossPages proves paging sits on top of a global sort:
// page 1 (limit 2, cost desc) holds the two globally most expensive actors.
func TestListActors_GlobalSortAcrossPages(t *testing.T) {
	db, ro := openTestDB(t)
	h := newActorHandler(t, db, ro)
	createAPIActor(t, db, "cheap")
	createAPIActor(t, db, "mid")
	createAPIActor(t, db, "pricey")
	addCostSpan(t, db, "c1", "cheap", "sc", 10)
	addCostSpan(t, db, "m1", "mid", "sm", 20)
	addCostSpan(t, db, "p1", "pricey", "sp", 30)

	_, body := getJSON(t, h, "/api/v1/actors?range=all&sort=cost&order=desc&page=1&limit=2")
	actors, _ := body["actors"].([]any)
	if len(actors) != 2 {
		t.Fatalf("page 1 limit 2: want 2 actors, got %d", len(actors))
	}
	if body["total"].(float64) != 3 {
		t.Errorf("total: want 3, got %v", body["total"])
	}
	first := actors[0].(map[string]any)
	second := actors[1].(map[string]any)
	if first["name"] != "pricey" || second["name"] != "mid" {
		t.Errorf("global sort broken: page 1 = %v, %v", first["name"], second["name"])
	}
}

// TestListActors_QueryFilter filters server-side on name.
func TestListActors_QueryFilter(t *testing.T) {
	db, ro := openTestDB(t)
	h := newActorHandler(t, db, ro)
	createAPIActor(t, db, "alice")
	createAPIActor(t, db, "bob")

	_, body := getJSON(t, h, "/api/v1/actors?q=ali")
	actors, _ := body["actors"].([]any)
	if len(actors) != 1 || body["total"].(float64) != 1 {
		t.Fatalf("q=ali: want 1 actor/total, got %d actors total=%v", len(actors), body["total"])
	}
	if actors[0].(map[string]any)["name"] != "alice" {
		t.Errorf("q=ali: want alice, got %v", actors[0].(map[string]any)["name"])
	}
}

// TestListActors_InvalidParamsFallBack confirms bad values fall back to defaults
// rather than erroring (trust the boundary).
func TestListActors_InvalidParamsFallBack(t *testing.T) {
	db, ro := openTestDB(t)
	h := newActorHandler(t, db, ro)
	createAPIActor(t, db, "alice")

	code, body := getJSON(t, h, "/api/v1/actors?range=decade&sort=whatever&order=sideways")
	if code != 200 {
		t.Fatalf("want 200 (no 400), got %d", code)
	}
	if body["range"] != "month" || body["sort"] != "cost" || body["order"] != "desc" {
		t.Errorf("fallbacks: range/sort/order = %v/%v/%v", body["range"], body["sort"], body["order"])
	}
}

// TestGetActor_ByID returns a single actor and 404s for unknown ids.
func TestGetActor_ByID(t *testing.T) {
	db, ro := openTestDB(t)
	h := newActorHandler(t, db, ro)
	u := createAPIActor(t, db, "alice")
	addCostSpan(t, db, "a1", "alice", "sa", 12)

	code, body := getJSON(t, h, "/api/v1/actors/"+u.ID+"?range=all")
	if code != 200 {
		t.Fatalf("want 200, got %d", code)
	}
	if body["name"] != "alice" || body["token"] != u.Token {
		t.Errorf("identity mismatch: %v", body)
	}
	if body["cost"].(float64) != 12 {
		t.Errorf("cost: want 12, got %v", body["cost"])
	}

	if code, _ := getJSON(t, h, "/api/v1/actors/no-such-id"); code != 404 {
		t.Errorf("unknown id: want 404, got %d", code)
	}
}

// TestGetActor_Anonymous resolves the synthetic anonymous row.
func TestGetActor_Anonymous(t *testing.T) {
	db, ro := openTestDB(t)
	h := newActorHandler(t, db, ro)
	addCostSpan(t, db, "anon1", "", "sanon", 9)

	code, body := getJSON(t, h, "/api/v1/actors/__anonymous__?range=all")
	if code != 200 {
		t.Fatalf("want 200, got %d", code)
	}
	if body["id"] != "__anonymous__" || body["cost"].(float64) != 9 {
		t.Errorf("anon: got %v", body)
	}
}

// The pre-v11 spellings keep working after the users → actors rename
// (ADR-0027): the /users route, the user_id filter and the user_* delete modes.
func TestLegacyUserSpellings(t *testing.T) {
	db, ro := openTestDB(t)
	h := newActorHandler(t, db, ro)
	alice := createAPIActor(t, db, "alice")
	createAPIActor(t, db, "bob")
	addCostSpan(t, db, "s1", "alice", "sess-a", 1.0)
	addCostSpan(t, db, "s2", "bob", "sess-b", 2.0)

	code, body := getJSON(t, h, "/api/v1/users")
	if code != 200 || body["total"].(float64) != 2 {
		t.Fatalf("/users alias: code %d, total %v", code, body["total"])
	}
	code, body = getJSON(t, h, "/api/v1/users/"+alice.ID)
	if code != 200 || body["name"] != "alice" {
		t.Fatalf("/users/{id} alias: code %d, body %v", code, body)
	}

	_, body = getJSON(t, h, "/api/v1/overview?range=all&user_id=alice")
	if body["sessions_count"].(float64) != 1 {
		t.Fatalf("user_id filter alias: sessions_count %v, want 1", body["sessions_count"])
	}

	if code := deleteActor(t, h, alice.ID, "user_only"); code != 204 {
		t.Fatalf("mode=user_only alias: code %d, want 204", code)
	}
}

func patchActor(t *testing.T, h http.Handler, id, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/actors/"+id, strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// PATCH /actors/{id} sets, normalizes and clears paperclip_agent_id (ADR-0029).
func TestPatchActor_PaperclipAgentID(t *testing.T) {
	db, ro := openTestDB(t)
	h := newActorHandler(t, db, ro)
	a := createAPIActor(t, db, "Wayland")
	const id = "709C0DB6-7E36-42AE-89DF-EF06D7F1ACAD"

	code, body := patchActor(t, h, a.ID, `{"paperclip_agent_id":" `+id+` "}`)
	if code != 200 || body["paperclip_agent_id"] != strings.ToLower(id) || body["name"] != "Wayland" {
		t.Fatalf("set: %d %v", code, body)
	}
	if _, got := getJSON(t, h, "/api/v1/actors/"+a.ID); got["paperclip_agent_id"] != strings.ToLower(id) {
		t.Fatalf("GET after set: %v", got["paperclip_agent_id"])
	}
	if code, body = patchActor(t, h, a.ID, `{"paperclip_agent_id":""}`); code != 200 || body["paperclip_agent_id"] != nil {
		t.Fatalf("clear: %d %v", code, body)
	}

	for _, bad := range []string{`{"paperclip_agent_id":"not-a-uuid"}`, `{}`, `nope`} {
		if code, _ := patchActor(t, h, a.ID, bad); code != 400 {
			t.Errorf("body %s: code %d, want 400", bad, code)
		}
	}
	if code, _ := patchActor(t, h, "no-such-actor", `{"paperclip_agent_id":""}`); code != 404 {
		t.Errorf("unknown actor: code %d, want 404", code)
	}
	if code, _ := patchActor(t, h, "__anonymous__", `{"paperclip_agent_id":""}`); code != 404 {
		t.Errorf("anonymous: code %d, want 404", code)
	}
}

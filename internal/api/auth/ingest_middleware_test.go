package auth_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Flopsstuff/cotel/internal/api/auth"
	"github.com/Flopsstuff/cotel/internal/ingest"
	"github.com/Flopsstuff/cotel/internal/storage"
)

const (
	liveIngestKey = "cotel_ik_" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	deadIngestKey = "cotel_ik_" + "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	aliceToken    = "cotel_" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	bobToken      = "cotel_" + "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	unknownToken  = "cotel_" + "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

// fakeIngestAuthDB knows two actors and one live ingest key. keyErr makes every
// IsIngestKey call fail, as a broken database would.
type fakeIngestAuthDB struct {
	allowAnonymous bool
	keyErr         error
	keyLookups     int
}

func (f *fakeIngestAuthDB) GetActorByToken(token string) (*storage.Actor, error) {
	switch token {
	case aliceToken:
		return &storage.Actor{Name: "alice", Token: aliceToken}, nil
	case bobToken:
		return &storage.Actor{Name: "bob", Token: bobToken}, nil
	}
	return nil, errors.New("not found")
}

func (f *fakeIngestAuthDB) GetSettingBool(_ string, _ bool) bool { return f.allowAnonymous }

func (f *fakeIngestAuthDB) IsIngestKey(token string) (bool, error) {
	f.keyLookups++
	if f.keyErr != nil {
		return false, f.keyErr
	}
	return token == liveIngestKey, nil
}

// actorEcho answers 200 with the attributed actor name ("" when anonymous) in
// a header, so a test can see what the middleware put in the context.
var actorEcho = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Test-Actor", ingest.ActorNameFromContext(r.Context()))
	w.WriteHeader(http.StatusOK)
})

func TestIngestMiddleware(t *testing.T) {
	tests := []struct {
		name           string
		allowAnonymous bool
		keyErr         error
		bearer         string // "" = no Authorization header
		actorHeader    string // "" = header not set
		wantCode       int
		wantActor      string
	}{
		// Ingest key in Authorization.
		{"key + actor token", false, nil, liveIngestKey, aliceToken, 200, "alice"},
		{"key + other actor token", false, nil, liveIngestKey, bobToken, 200, "bob"},
		{"key, no actor header, anon off", false, nil, liveIngestKey, "", 200, ""},
		{"key, no actor header, anon on", true, nil, liveIngestKey, "", 200, ""},
		{"key + whitespace actor header", false, nil, liveIngestKey, "   ", 200, ""},
		{"key + unknown actor token", false, nil, liveIngestKey, unknownToken, 200, ""},
		{"key + ingest key as actor token", false, nil, liveIngestKey, liveIngestKey, 200, ""},
		{"unknown key", true, nil, deadIngestKey, "", 401, ""},
		{"unknown key + valid actor header", true, nil, deadIngestKey, aliceToken, 401, ""},
		{"key lookup error fails closed", true, errors.New("db down"), liveIngestKey, aliceToken, 401, ""},

		// Actor token in Authorization: today's behaviour, header ignored.
		{"actor bearer", false, nil, aliceToken, "", 200, "alice"},
		{"actor bearer ignores actor header", false, nil, aliceToken, bobToken, 200, "alice"},
		{"unknown actor bearer", true, nil, unknownToken, "", 401, ""},
		{"unknown actor bearer + actor header", true, nil, unknownToken, aliceToken, 401, ""},

		// No bearer: allow_anonymous decides, the actor header never identifies.
		{"no bearer, anon on", true, nil, "", "", 200, ""},
		{"no bearer, anon off", false, nil, "", "", 401, ""},
		{"no bearer + actor header, anon on", true, nil, "", aliceToken, 200, ""},
		{"no bearer + actor header, anon off", false, nil, "", aliceToken, 401, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := &fakeIngestAuthDB{allowAnonymous: tt.allowAnonymous, keyErr: tt.keyErr}
			h := auth.IngestMiddleware(db, actorEcho)
			req := httptest.NewRequest(http.MethodPost, "/v1/traces", nil)
			if tt.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+tt.bearer)
			}
			if tt.actorHeader != "" {
				req.Header.Set(auth.ActorTokenHeader, tt.actorHeader)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			if w.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (body %q)", w.Code, tt.wantCode, w.Body.String())
			}
			if tt.wantCode == http.StatusUnauthorized {
				if got := w.Body.String(); got != `{"error":"unauthorized"}` {
					t.Errorf("401 body = %q", got)
				}
				if ct := w.Header().Get("Content-Type"); ct != "application/json" {
					t.Errorf("401 Content-Type = %q", ct)
				}
				return
			}
			if got := w.Header().Get("X-Test-Actor"); got != tt.wantActor {
				t.Errorf("attributed actor = %q, want %q", got, tt.wantActor)
			}
		})
	}
}

// Only cotel_ik_ bearers cost an ingest key lookup; actor tokens and anonymous
// requests take exactly the old path.
func TestIngestMiddleware_KeyLookupOnlyForKeys(t *testing.T) {
	for _, bearer := range []string{"", aliceToken, unknownToken, "someOtherToken"} {
		db := &fakeIngestAuthDB{allowAnonymous: true}
		h := auth.IngestMiddleware(db, actorEcho)
		req := httptest.NewRequest(http.MethodPost, "/v1/traces", nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		h.ServeHTTP(httptest.NewRecorder(), req)
		if db.keyLookups != 0 {
			t.Errorf("bearer %q: %d ingest key lookups, want 0", bearer, db.keyLookups)
		}
	}
}

// Export and import stay on Middleware: an ingest key, which every agent can
// read from its environment, must not authorize them.
func TestMiddleware_RejectsIngestKey(t *testing.T) {
	for _, allowAnonymous := range []bool{true, false} {
		db := &fakeIngestAuthDB{allowAnonymous: allowAnonymous}
		h := auth.Middleware(db, actorEcho)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/export", nil)
		req.Header.Set("Authorization", "Bearer "+liveIngestKey)
		req.Header.Set(auth.ActorTokenHeader, aliceToken)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("allow_anonymous=%v: ingest key on export got %d, want 401", allowAnonymous, w.Code)
		}
	}
}

// End to end against real storage: a key created in the database authorizes
// ingest with the actor header attributing it, and stops working once deleted.
func TestIngestMiddleware_RealStorage(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.SetSetting("allow_anonymous", "false"); err != nil {
		t.Fatal(err)
	}
	actor, err := db.CreateActor("agent-7")
	if err != nil {
		t.Fatal(err)
	}
	key, err := db.CreateIngestKey("paperclip")
	plaintext := key.Token
	if err != nil {
		t.Fatal(err)
	}

	send := func(h http.Handler) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/traces", nil)
		req.Header.Set("Authorization", "Bearer "+plaintext)
		req.Header.Set(auth.ActorTokenHeader, actor.Token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	h := auth.IngestMiddleware(db, actorEcho)
	if w := send(h); w.Code != 200 || w.Header().Get("X-Test-Actor") != "agent-7" {
		t.Fatalf("live key: code %d actor %q, want 200 agent-7", w.Code, w.Header().Get("X-Test-Actor"))
	}
	if w := send(auth.Middleware(db, actorEcho)); w.Code != http.StatusUnauthorized {
		t.Fatalf("live key on Middleware: code %d, want 401", w.Code)
	}
	if err := db.DeleteIngestKey(key.ID); err != nil {
		t.Fatal(err)
	}
	if w := send(h); w.Code != http.StatusUnauthorized {
		t.Fatalf("deleted key: code %d, want 401", w.Code)
	}
}

// Only a request authorized by an ingest key is marked for attribution by
// paperclip.agent.id; actor-token and anonymous requests are not (ADR-0029).
func TestIngestMiddleware_MarksIngestKeyRequests(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	actor, err := db.CreateActor("flop")
	if err != nil {
		t.Fatal(err)
	}
	key, err := db.CreateIngestKey("paperclip")
	if err != nil {
		t.Fatal(err)
	}
	mark := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ingest.IngestKeyAuthFromContext(r.Context()) {
			w.Header().Set("X-Test-Ingest-Key", "1")
		}
		w.WriteHeader(http.StatusOK)
	})
	h := auth.IngestMiddleware(db, mark)
	for _, c := range []struct {
		name, auth string
		want       string
	}{
		{"ingest key", "Bearer " + key.Token, "1"},
		{"actor token", "Bearer " + actor.Token, ""},
		{"anonymous", "", ""},
	} {
		req := httptest.NewRequest(http.MethodPost, "/v1/traces", nil)
		if c.auth != "" {
			req.Header.Set("Authorization", c.auth)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if got := w.Header().Get("X-Test-Ingest-Key"); got != c.want {
			t.Errorf("%s: ingest-key mark = %q, want %q", c.name, got, c.want)
		}
	}
}

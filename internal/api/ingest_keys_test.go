package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Flopsstuff/cotel/internal/api"
	"github.com/Flopsstuff/cotel/internal/storage"
)

func newIngestKeyHandler(t *testing.T, db *storage.DB, ro *storage.ReadDB) http.Handler {
	t.Helper()
	return api.New(ro).SetActorStore(db).SetIngestKeyStore(db)
}

func doIngestKeys(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestIngestKeys_ListEmpty(t *testing.T) {
	db, ro := openTestDB(t)
	h := newIngestKeyHandler(t, db, ro)

	w := doIngestKeys(t, h, http.MethodGet, "/api/v1/ingest-keys", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := strings.TrimSpace(w.Body.String()); got != `{"ingest_keys":[]}` {
		t.Fatalf("body = %s, want an empty array, not null", got)
	}
}

func TestIngestKeys_CreateListDelete(t *testing.T) {
	db, ro := openTestDB(t)
	h := newIngestKeyHandler(t, db, ro)

	w := doIngestKeys(t, h, http.MethodPost, "/api/v1/ingest-keys", `{"name":"  Paperclip  "}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201 (%s)", w.Code, w.Body.String())
	}
	var created struct {
		ID, Name, Token string
		CreatedAt       string `json:"created_at"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("create body: %v", err)
	}
	if created.Name != "Paperclip" {
		t.Errorf("name = %q, want trimmed %q", created.Name, "Paperclip")
	}
	if !regexp.MustCompile(`^cotel_ik_[0-9a-f]{64}$`).MatchString(created.Token) {
		t.Errorf("token = %q", created.Token)
	}
	if ts, err := time.Parse(time.RFC3339, created.CreatedAt); err != nil || ts.Location() != time.UTC {
		t.Errorf("created_at = %q, want RFC3339 UTC (err %v)", created.CreatedAt, err)
	}
	if ok, err := db.IsIngestKey(created.Token); err != nil || !ok {
		t.Fatalf("created token not accepted: ok=%v err=%v", ok, err)
	}

	code, body := getJSON(t, h, "/api/v1/ingest-keys")
	if code != http.StatusOK {
		t.Fatalf("list: status = %d", code)
	}
	keys, _ := body["ingest_keys"].([]any)
	if len(keys) != 1 {
		t.Fatalf("list: %d keys, want 1", len(keys))
	}
	item := keys[0].(map[string]any)
	// The list returns the token so it can be copied again, like an actor token.
	if item["id"] != created.ID || item["name"] != "Paperclip" || item["token"] != created.Token ||
		item["created_at"] != created.CreatedAt {
		t.Errorf("list item = %v", item)
	}

	if w := doIngestKeys(t, h, http.MethodDelete, "/api/v1/ingest-keys/"+created.ID, ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d, want 204", w.Code)
	}
	if ok, _ := db.IsIngestKey(created.Token); ok {
		t.Fatal("deleted key still accepted")
	}
	w = doIngestKeys(t, h, http.MethodDelete, "/api/v1/ingest-keys/"+created.ID, "")
	if w.Code != http.StatusNotFound || strings.TrimSpace(w.Body.String()) != `{"error":"not found"}` {
		t.Fatalf("delete again: %d %s, want 404 not found", w.Code, w.Body.String())
	}
}

func TestIngestKeys_CreateValidation(t *testing.T) {
	db, ro := openTestDB(t)
	h := newIngestKeyHandler(t, db, ro)

	tests := []struct {
		name     string
		body     string
		wantCode int
	}{
		{"missing name", `{}`, 400},
		{"empty name", `{"name":""}`, 400},
		{"blank name", `{"name":"   "}`, 400},
		{"not json", `name=x`, 400},
		{"wrong type", `{"name":7}`, 400},
		{"101 chars", `{"name":"` + strings.Repeat("a", 101) + `"}`, 400},
		{"100 chars", `{"name":"` + strings.Repeat("a", 100) + `"}`, 201},
		{"100 multibyte chars", `{"name":"` + strings.Repeat("é", 100) + `"}`, 201},
		{"100 chars after trim", `{"name":"  ` + strings.Repeat("a", 100) + `  "}`, 201},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := doIngestKeys(t, h, http.MethodPost, "/api/v1/ingest-keys", tt.body)
			if w.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (%s)", w.Code, tt.wantCode, w.Body.String())
			}
			if tt.wantCode == http.StatusBadRequest {
				var e map[string]string
				if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || e["error"] == "" {
					t.Errorf("400 body = %s, want {\"error\":...}", w.Body.String())
				}
			}
		})
	}
}

func TestIngestKeys_Methods(t *testing.T) {
	db, ro := openTestDB(t)
	h := newIngestKeyHandler(t, db, ro)

	tests := []struct {
		method, path string
		wantCode     int
	}{
		{http.MethodPut, "/api/v1/ingest-keys", 405},
		{http.MethodDelete, "/api/v1/ingest-keys", 405},
		{http.MethodPatch, "/api/v1/ingest-keys", 405},
		{http.MethodGet, "/api/v1/ingest-keys/some-id", 405},
		{http.MethodPost, "/api/v1/ingest-keys/some-id", 405},
		{http.MethodDelete, "/api/v1/ingest-keys/unknown-id", 404},
	}
	for _, tt := range tests {
		w := doIngestKeys(t, h, tt.method, tt.path, "")
		if w.Code != tt.wantCode {
			t.Errorf("%s %s: status = %d, want %d", tt.method, tt.path, w.Code, tt.wantCode)
		}
	}
}

// Without a store attached the endpoints do not exist, like /actors.
func TestIngestKeys_NoStore(t *testing.T) {
	_, ro := openTestDB(t)
	h := api.New(ro)
	if w := doIngestKeys(t, h, http.MethodGet, "/api/v1/ingest-keys", ""); w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

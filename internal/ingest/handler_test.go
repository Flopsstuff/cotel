package ingest_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Flopsstuff/cotel/internal/ingest"
	"github.com/Flopsstuff/cotel/internal/storage"
)

// memStore captures inserted spans without a real DB.
type memStore struct {
	spans []storage.Span
}

func (m *memStore) InsertSpan(s storage.Span) error {
	m.spans = append(m.spans, s)
	return nil
}

func TestGoldenPayload(t *testing.T) {
	payload, err := os.ReadFile("../../testdata/fixtures/sample-otlp-payload.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	store := &memStore{}
	h := ingest.New(store)

	req := httptest.NewRequest(http.MethodPost, "/v1/traces",
		bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Flush drains the async write queue and waits for all spans to be stored.
	h.Flush()

	if len(store.spans) < 2 {
		t.Fatalf("expected ≥2 spans, got %d", len(store.spans))
	}

	// Verify session span extraction.
	var sessionSpan *storage.Span
	for i := range store.spans {
		if store.spans[i].Name == "claude_code.session" {
			sessionSpan = &store.spans[i]
			break
		}
	}
	if sessionSpan == nil {
		t.Fatal("no session span found")
	}
	if sessionSpan.SessionID != "sess_abc123" {
		t.Errorf("session_id: got %q, want %q", sessionSpan.SessionID, "sess_abc123")
	}
	if sessionSpan.Model != "claude-sonnet-4-6" {
		t.Errorf("model: got %q, want %q", sessionSpan.Model, "claude-sonnet-4-6")
	}
	if sessionSpan.CostUSD == nil || *sessionSpan.CostUSD < 0.003 {
		t.Errorf("cost_usd: got %v, want ≥0.003", sessionSpan.CostUSD)
	}

	// Verify tool span.
	var toolSpan *storage.Span
	for i := range store.spans {
		if store.spans[i].Name == "claude_code.tool_use" {
			toolSpan = &store.spans[i]
			break
		}
	}
	if toolSpan == nil {
		t.Fatal("no tool span found")
	}
	if toolSpan.ToolName != "Bash" {
		t.Errorf("tool_name: got %q, want %q", toolSpan.ToolName, "Bash")
	}

	// Response must be valid JSON.
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Errorf("response not valid JSON: %v", err)
	}
}

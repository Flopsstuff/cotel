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

	if len(store.spans) < 3 {
		t.Fatalf("expected ≥3 spans, got %d", len(store.spans))
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

	// Verify tool span (successful).
	var toolSpan *storage.Span
	for i := range store.spans {
		if store.spans[i].Name == "claude_code.tool_use" && store.spans[i].StatusCode == 1 {
			toolSpan = &store.spans[i]
			break
		}
	}
	if toolSpan == nil {
		t.Fatal("no successful tool span found")
	}
	if toolSpan.ToolName != "Bash" {
		t.Errorf("tool_name: got %q, want %q", toolSpan.ToolName, "Bash")
	}
	if toolSpan.SessionID != "sess_abc123" {
		t.Errorf("tool span session_id: got %q, want sess_abc123 (must be present on child spans)", toolSpan.SessionID)
	}

	// Verify error span (status_code = 2 = STATUS_CODE_ERROR).
	var errSpan *storage.Span
	for i := range store.spans {
		if store.spans[i].StatusCode == 2 {
			errSpan = &store.spans[i]
			break
		}
	}
	if errSpan == nil {
		t.Fatal("no error span found (status_code=2)")
	}
	if errSpan.ToolName != "Bash" {
		t.Errorf("error span tool_name: got %q, want %q", errSpan.ToolName, "Bash")
	}

	// Verify session span status_code is OK (1).
	if sessionSpan.StatusCode != 1 {
		t.Errorf("session span status_code: got %d, want 1 (STATUS_CODE_OK)", sessionSpan.StatusCode)
	}

	// Response must be valid JSON.
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Errorf("response not valid JSON: %v", err)
	}
}

// TestBetaPayload verifies the real Claude Code beta telemetry format:
// no claude_code.session root span; session.id lives in resource attributes.
func TestBetaPayload(t *testing.T) {
	payload, err := os.ReadFile("../../testdata/fixtures/sample-otlp-payload-beta.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	store := &memStore{}
	h := ingest.New(store)

	req := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	h.Flush()

	if len(store.spans) < 3 {
		t.Fatalf("expected ≥3 spans, got %d", len(store.spans))
	}

	// All spans must inherit session_id from resource attributes.
	for i, s := range store.spans {
		if s.SessionID != "sess_beta_456" {
			t.Errorf("span[%d] %q: session_id got %q, want %q", i, s.Name, s.SessionID, "sess_beta_456")
		}
	}

	// Model invocation span must have cost and token data.
	var modelSpan *storage.Span
	for i := range store.spans {
		if store.spans[i].Name == "claude_code.model_invocation" {
			modelSpan = &store.spans[i]
			break
		}
	}
	if modelSpan == nil {
		t.Fatal("model_invocation span not found")
	}
	if modelSpan.Model != "claude-opus-4-7" {
		t.Errorf("model: got %q, want claude-opus-4-7", modelSpan.Model)
	}
	if modelSpan.CostUSD == nil || *modelSpan.CostUSD < 0.009 {
		t.Errorf("cost_usd: got %v, want ≥0.009", modelSpan.CostUSD)
	}
}

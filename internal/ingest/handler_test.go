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

	// Model invocation span: cost must be server-computed from token counts
	// (fixture has no cost_usd — mirrors real Claude Code beta).
	// claude-opus-4-7 with 2048 in + 768 out + 512 cache-read + 256 cache-write → ~$0.094
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
	if modelSpan.CostUSD == nil || *modelSpan.CostUSD < 0.05 {
		t.Errorf("cost_usd: got %v, want ≥0.05 (server-computed from tokens)", modelSpan.CostUSD)
	}
	// cache_creation_tokens is the real Claude Code beta key; must map to CacheWriteTokens.
	if modelSpan.CacheWriteTokens == nil || *modelSpan.CacheWriteTokens != 256 {
		t.Errorf("cache_write_tokens: got %v, want 256", modelSpan.CacheWriteTokens)
	}

	// tool_name is the real Claude Code beta key; tool spans must have ToolName set.
	var toolSpan *storage.Span
	for i := range store.spans {
		if store.spans[i].Name == "claude_code.tool_use" && store.spans[i].StatusCode == 1 {
			toolSpan = &store.spans[i]
			break
		}
	}
	if toolSpan == nil {
		t.Fatal("no successful tool_use span found")
	}
	if toolSpan.ToolName != "Read" {
		t.Errorf("tool_name: got %q, want %q", toolSpan.ToolName, "Read")
	}
}

// buildMinimalPayload constructs a single-span OTLP JSON payload for unit testing.
func buildMinimalPayload(spanAttrs string) string {
	return `{"resourceSpans":[{"resource":{"attributes":[` +
		`{"key":"service.name","value":{"stringValue":"test"}},` +
		`{"key":"session.id","value":{"stringValue":"sess_test"}}` +
		`]},"scopeSpans":[{"spans":[{` +
		`"traceId":"aabbccdd00000000aabbccdd00000000",` +
		`"spanId":"aabbccdd00000000",` +
		`"name":"claude_code.model_invocation",` +
		`"startTimeUnixNano":"1778327029000000000",` +
		`"endTimeUnixNano":"1778327129000000000",` +
		`"status":{"code":1},` +
		`"attributes":[` + spanAttrs + `]}]}]}]}`
}

// TestCostComputedFromTokens verifies that ingest derives cost_usd when the span
// carries token counts but no cost_usd attribute (real Claude Code beta behaviour).
func TestCostComputedFromTokens(t *testing.T) {
	// claude-sonnet-4-6: $3/MTok in → 1M tokens = $3.00
	attrs := `{"key":"model","value":{"stringValue":"claude-sonnet-4-6"}},` +
		`{"key":"input_tokens","value":{"intValue":"1000000"}}`
	store := &memStore{}
	h := ingest.New(store)

	req := httptest.NewRequest(http.MethodPost, "/v1/traces",
		bytes.NewBufferString(buildMinimalPayload(attrs)))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(httptest.NewRecorder(), req)
	h.Flush()

	if len(store.spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(store.spans))
	}
	s := store.spans[0]
	if s.CostUSD == nil {
		t.Fatal("cost_usd must be derived from token counts when absent from span")
	}
	const want = 3.00 // 1M * $3/MTok
	if got := *s.CostUSD; got < want-0.001 || got > want+0.001 {
		t.Errorf("computed cost: got %.6f, want %.6f", got, want)
	}
}

// TestProducerCostUSDUsedWhenPresent verifies that an explicit cost_usd from the
// producer is stored as-is and not overwritten by the pricing computation.
func TestProducerCostUSDUsedWhenPresent(t *testing.T) {
	attrs := `{"key":"model","value":{"stringValue":"claude-sonnet-4-6"}},` +
		`{"key":"input_tokens","value":{"intValue":"1000000"}},` +
		`{"key":"cost_usd","value":{"doubleValue":0.00123}}`
	store := &memStore{}
	h := ingest.New(store)

	req := httptest.NewRequest(http.MethodPost, "/v1/traces",
		bytes.NewBufferString(buildMinimalPayload(attrs)))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(httptest.NewRecorder(), req)
	h.Flush()

	if len(store.spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(store.spans))
	}
	s := store.spans[0]
	if s.CostUSD == nil {
		t.Fatal("cost_usd must be set")
	}
	if got := *s.CostUSD; got != 0.00123 {
		t.Errorf("producer cost_usd must be preserved: got %.6f, want 0.00123", got)
	}
}

// TestUnknownModelCostIsNil verifies that an unrecognised model id does not
// cause an ingest failure — cost_usd stays nil so it is distinguishable from
// a genuine zero-cost span.
func TestUnknownModelCostIsNil(t *testing.T) {
	attrs := `{"key":"model","value":{"stringValue":"claude-unknown-99"}},` +
		`{"key":"input_tokens","value":{"intValue":"1000000"}}`
	store := &memStore{}
	h := ingest.New(store)

	req := httptest.NewRequest(http.MethodPost, "/v1/traces",
		bytes.NewBufferString(buildMinimalPayload(attrs)))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(httptest.NewRecorder(), req)
	h.Flush()

	if len(store.spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(store.spans))
	}
	s := store.spans[0]
	if s.CostUSD != nil {
		t.Errorf("unknown model: cost_usd should be nil, got %v", *s.CostUSD)
	}
}

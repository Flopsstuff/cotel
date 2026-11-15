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
	// claude-opus-4-7 with 2048 in + 768 out + 512 cache-read + 256 cache-write → ~$0.0313
	// (corrected 2026-08 Opus 4.7 rates: $5/$25/$0.50/$6.25 per MTok; prior rates were $15/$75)
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
	if modelSpan.CostUSD == nil || *modelSpan.CostUSD < 0.03 {
		t.Errorf("cost_usd: got %v, want ≥0.03 (server-computed from tokens)", modelSpan.CostUSD)
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

// TestCostComputedForDatedModelID verifies the shape Claude Code actually sends
// for Haiku 4.5 — a dated snapshot id — resolves to a price and not to a
// "pricing: unknown model" warning with cost_usd left nil.
func TestCostComputedForDatedModelID(t *testing.T) {
	// claude-haiku-4-5: $1/MTok in → 1M tokens = $1.00
	attrs := `{"key":"model","value":{"stringValue":"claude-haiku-4-5-20251001"}},` +
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
		t.Fatal("dated model id must resolve to a price, got nil cost_usd")
	}
	const want = 1.00 // 1M * $1/MTok
	if got := *s.CostUSD; got < want-0.001 || got > want+0.001 {
		t.Errorf("computed cost: got %.6f, want %.6f", got, want)
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

// A protobuf export must be answered with a protobuf ExportTraceServiceResponse
// (an empty one is zero bytes), not with JSON: protobuf exporters decode the body.
func TestTracesResponseMatchesRequestEncoding(t *testing.T) {
	cases := []struct {
		name, contentType, wantCT, wantBody string
		body                                []byte
	}{
		{"protobuf", "application/x-protobuf", "application/x-protobuf", "", nil},
		{"json", "application/json", "application/json", `{}`, []byte(`{"resourceSpans":[]}`)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := ingest.New(&memStore{})
			req := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(c.body))
			req.Header.Set("Content-Type", c.contentType)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d (%s)", w.Code, w.Body.String())
			}
			if ct := w.Header().Get("Content-Type"); ct != c.wantCT {
				t.Errorf("Content-Type = %q, want %q", ct, c.wantCT)
			}
			if got := w.Body.String(); got != c.wantBody {
				t.Errorf("body = %q, want %q", got, c.wantBody)
			}
		})
	}
}

// resolvingStore is a Storer that also maps Paperclip agent ids to actors.
type resolvingStore struct {
	memStore
	agents  map[string]string
	lookups int
}

func (r *resolvingStore) ActorNameByPaperclipAgentID(id string) (string, error) {
	r.lookups++
	return r.agents[id], nil
}

func agentPayload(resourceAgent, spanAgent string) []byte {
	attr := func(v string) string {
		if v == "" {
			return ""
		}
		return `{"key":"paperclip.agent.id","value":{"stringValue":"` + v + `"}}`
	}
	return []byte(`{"resourceSpans":[{"resource":{"attributes":[` + attr(resourceAgent) + `]},
	  "scopeSpans":[{"spans":[
	    {"traceId":"00000000000000000000000000000001","spanId":"0000000000000001","name":"a",
	     "startTimeUnixNano":"1","endTimeUnixNano":"2","attributes":[` + attr(spanAgent) + `]},
	    {"traceId":"00000000000000000000000000000001","spanId":"0000000000000002","name":"b",
	     "startTimeUnixNano":"1","endTimeUnixNano":"2","attributes":[` + attr(spanAgent) + `]}]}]}]}`)
}

// Spans are attributed by paperclip.agent.id only on ingest-key requests with
// no actor token, the actor token wins, and unauthenticated requests never are.
func TestPaperclipAgentAttribution(t *testing.T) {
	const wayland, daedalus = "709c0db6-7e36-42ae-89df-ef06d7f1acad", "386b876d-eeba-4bf9-bc10-0dec7b09ee8a"
	cases := []struct {
		name          string
		ingestKey     bool
		actorToken    string // actor name injected by the middleware
		resAgent      string
		spanAgent     string
		want          string
		wantLookupMax int
	}{
		{"ingest key, resource attr", true, "", wayland, "", "Wayland", 1},
		{"ingest key, span attr only", true, "", "", daedalus, "Daedalus", 1},
		{"resource attr beats span attr", true, "", wayland, daedalus, "Wayland", 1},
		{"unknown agent stays anonymous", true, "", "11111111-1111-1111-1111-111111111111", "", "", 1},
		{"actor token wins", true, "flop", wayland, "", "flop", 0},
		{"no ingest key: attribute ignored", false, "", wayland, "", "", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := &resolvingStore{agents: map[string]string{wayland: "Wayland", daedalus: "Daedalus"}}
			h := ingest.New(store)
			req := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(agentPayload(c.resAgent, c.spanAgent)))
			req.Header.Set("Content-Type", "application/json")
			ctx := req.Context()
			if c.ingestKey {
				ctx = ingest.WithIngestKeyAuth(ctx)
			}
			if c.actorToken != "" {
				ctx = ingest.WithActorName(ctx, c.actorToken)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req.WithContext(ctx))
			h.Flush()
			if w.Code != http.StatusOK {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if len(store.spans) != 2 {
				t.Fatalf("stored %d spans, want 2", len(store.spans))
			}
			for _, s := range store.spans {
				if s.ActorID != c.want {
					t.Errorf("span %s actor = %q, want %q", s.SpanID, s.ActorID, c.want)
				}
			}
			// Two spans from one agent cost at most one lookup.
			if store.lookups > c.wantLookupMax {
				t.Errorf("lookups = %d, want <= %d", store.lookups, c.wantLookupMax)
			}
		})
	}
}

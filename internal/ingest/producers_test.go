package ingest_test

import (
	"bytes"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Flopsstuff/cotel/internal/ingest"
	"github.com/Flopsstuff/cotel/internal/storage"
)

// The span shapes below are the ones Codex 0.160 (codex-app-server) and Grok CLI
// 1.0.77 export through Paperclip, trimmed to the attributes that matter; ids
// are synthetic.

const (
	codexConv  = "0000c0de-0000-7000-8000-000000000001"
	grokSessID = "00006a0c-0000-7000-8000-000000000002"
)

type kv struct {
	key string
	val string // already-encoded OTLP AnyValue JSON, e.g. {"stringValue":"x"}
}

func s(v string) string { return `{"stringValue":"` + v + `"}` }
func i(v string) string { return `{"intValue":"` + v + `"}` }

// ingestOne posts one span from service svc and returns what was stored.
func ingestOne(t *testing.T, svc, spanName string, attrs ...kv) storage.Span {
	t.Helper()
	stored := ingestSpans(t, svc, spanName, attrs...)
	if len(stored) != 1 {
		t.Fatalf("expected 1 span, got %d", len(stored))
	}
	return stored[0]
}

// ingestSpans posts one span from service svc and returns every span stored.
func ingestSpans(t *testing.T, svc, spanName string, attrs ...kv) []storage.Span {
	t.Helper()
	parts := make([]string, 0, len(attrs))
	for _, a := range attrs {
		parts = append(parts, `{"key":"`+a.key+`","value":`+a.val+`}`)
	}
	payload := `{"resourceSpans":[{"resource":{"attributes":[` +
		`{"key":"service.name","value":{"stringValue":"` + svc + `"}}` +
		`]},"scopeSpans":[{"spans":[{` +
		`"traceId":"aabbccdd00000000aabbccdd00000000",` +
		`"spanId":"aabbccdd00000000",` +
		`"name":"` + spanName + `",` +
		`"startTimeUnixNano":"1778327029000000000",` +
		`"endTimeUnixNano":"1778327129000000000",` +
		`"attributes":[` + strings.Join(parts, ",") + `]}]}]}]}`

	store := &memStore{}
	h := ingest.New(store)
	req := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(httptest.NewRecorder(), req)
	h.Flush()
	return store.spans
}

func wantTokens(t *testing.T, name string, got *int64, want int64) {
	t.Helper()
	if got == nil {
		t.Errorf("%s: got nil, want %d", name, want)
		return
	}
	if *got != want {
		t.Errorf("%s: got %d, want %d", name, *got, want)
	}
}

func wantCost(t *testing.T, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("cost_usd: got nil, want %.6f", want)
	}
	if math.Abs(*got-want) > 1e-9 {
		t.Errorf("cost_usd: got %.9f, want %.9f", *got, want)
	}
}

// The Codex turn span is where a session gets its model, tokens and cost: the
// conversation id rides as thread.id, and input_tokens includes the cached part.
func TestCodexTurnSpan(t *testing.T) {
	sp := ingestOne(t, "codex-app-server", "session_task.turn",
		kv{"thread.id", s(codexConv)},
		kv{"model", s("gpt-5.6-sol")},
		kv{"codex.turn.token_usage.input_tokens", i("174228")},
		kv{"codex.turn.token_usage.cached_input_tokens", i("130176")},
		kv{"codex.turn.token_usage.non_cached_input_tokens", i("44052")},
		kv{"codex.turn.token_usage.cache_write_input_tokens", i("0")},
		kv{"codex.turn.token_usage.output_tokens", i("934")},
		kv{"codex.turn.token_usage.reasoning_output_tokens", i("82")},
	)
	if sp.SessionID != codexConv {
		t.Errorf("session_id: got %q, want %q", sp.SessionID, codexConv)
	}
	if sp.Model != "gpt-5.6-sol" {
		t.Errorf("model: got %q", sp.Model)
	}
	wantTokens(t, "input", sp.InputTokens, 44052)
	wantTokens(t, "cache_read", sp.CacheReadTokens, 130176)
	wantTokens(t, "cache_write", sp.CacheWriteTokens, 0)
	wantTokens(t, "output", sp.OutputTokens, 934)
	// gpt-5.6-sol: $4 in, $0.40 cached, $20 out per MTok.
	wantCost(t, sp.CostUSD, (44052*4.0+130176*0.40+934*20.0)/1e6)
}

// Without non_cached_input_tokens the input column is total minus cached.
func TestCodexTurnSpanDerivesNonCachedInput(t *testing.T) {
	sp := ingestOne(t, "codex-app-server", "session_task.turn",
		kv{"thread.id", s(codexConv)},
		kv{"model", s("gpt-5.6-sol")},
		kv{"codex.turn.token_usage.input_tokens", i("1000")},
		kv{"codex.turn.token_usage.cached_input_tokens", i("600")},
		kv{"codex.turn.token_usage.output_tokens", i("10")},
	)
	wantTokens(t, "input", sp.InputTokens, 400)
	wantTokens(t, "cache_read", sp.CacheReadTokens, 600)
}

// thread.id on most Codex spans is the tracing layer's OS thread number, not a
// session, and a Codex span without a session is not stored (ADR-0030).
func TestCodexIntegerThreadIDIsNotASession(t *testing.T) {
	if got := ingestSpans(t, "codex-app-server", "fs.get_metadata", kv{"thread.id", i("3")}); len(got) != 0 {
		t.Errorf("stored %d spans, want 0 (session_id %q)", len(got), got[0].SessionID)
	}
}

// handle_responses repeats the turn's usage per request as gen_ai.usage.*; the
// turn span already carries the sum. It has no session, so it is not stored and
// cannot add to the turn's tokens.
func TestCodexPerRequestUsageIsNotCounted(t *testing.T) {
	got := ingestSpans(t, "codex-app-server", "handle_responses",
		kv{"thread.id", i("5")},
		kv{"gen_ai.usage.input_tokens", i("24325")},
		kv{"gen_ai.usage.output_tokens", i("196")},
		kv{"gen_ai.usage.cache_read.input_tokens", i("11264")},
	)
	if len(got) != 0 {
		t.Errorf("handle_responses: stored %d spans, want 0", len(got))
	}
}

// The Codex spans that make up nearly all of its volume carry no session and
// are dropped; every other producer's sessionless spans are stored (ADR-0030).
func TestOnlyCodexSessionlessSpansAreDropped(t *testing.T) {
	for _, name := range []string{"receiving", "append_items", "persist_rollout_items", "realtime_conversation.running_state"} {
		if got := ingestSpans(t, "codex-app-server", name, kv{"thread.id", i("7")}); len(got) != 0 {
			t.Errorf("codex %s: stored %d spans, want 0", name, len(got))
		}
	}
	for _, svc := range []string{"grok-cli", "claude-code", ""} {
		if got := ingestSpans(t, svc, "anything"); len(got) != 1 {
			t.Errorf("%q sessionless span: stored %d, want 1", svc, len(got))
		}
	}
}

// WouldDrop applies the same rule to a stored span's JSON-decoded attributes,
// where integers arrive as float64.
func TestWouldDrop(t *testing.T) {
	cases := []struct {
		svc, name string
		attrs     map[string]any
		want      bool
	}{
		{"codex-app-server", "receiving", map[string]any{"thread.id": float64(5)}, true},
		{"codex-app-server", "fs.read_file", map[string]any{}, true},
		{"codex-app-server", "session_task.turn", map[string]any{"thread.id": codexConv}, false},
		{"codex-app-server", "exec", map[string]any{"conversation.id": codexConv}, false},
		{"grok-cli", "sampling.stream_decode", map[string]any{}, false},
		{"claude-code", "claude_code.tool", map[string]any{}, false},
	}
	for _, c := range cases {
		if got := ingest.WouldDrop(c.svc, c.name, c.attrs); got != c.want {
			t.Errorf("WouldDrop(%s, %s) = %v, want %v", c.svc, c.name, got, c.want)
		}
	}
}

// A Codex tool call is the span with tool_name and call_id; the broker span that
// names the same nested call has no call_id and is not a second call.
func TestCodexToolSpans(t *testing.T) {
	call := ingestOne(t, "codex-app-server", "exec_command",
		kv{"thread.id", s(codexConv)},
		kv{"tool_name", s("exec_command")},
		kv{"call_id", s("exec-1")},
	)
	if call.ToolName != "exec_command" || call.SessionID != codexConv {
		t.Errorf("exec_command: tool=%q session=%q", call.ToolName, call.SessionID)
	}

	broker := ingestOne(t, "codex-app-server", "code_mode.broker.invoke_tool",
		kv{"conversation.id", s(codexConv)},
		kv{"tool_name", s("exec_command")},
		kv{"thread.id", i("5")},
	)
	if broker.ToolName != "" {
		t.Errorf("broker span must not count as a tool call, got %q", broker.ToolName)
	}
	if broker.SessionID != codexConv {
		t.Errorf("broker session_id from conversation.id: got %q", broker.SessionID)
	}
}

// The Grok turn span carries session, model and the turn's usage; its
// input_tokens includes cache_read_tokens.
func TestGrokTurnSpan(t *testing.T) {
	sp := ingestOne(t, "grok-cli", "session.process_conversation_turn",
		kv{"session_id", s(grokSessID)},
		kv{"model_id", s("grok-4.7")},
		kv{"input_tokens", i("204311")},
		kv{"cache_read_tokens", i("171008")},
		kv{"output_tokens", i("1294")},
		kv{"last_sample.output_tokens", i("159")},
	)
	if sp.SessionID != grokSessID {
		t.Errorf("session_id: got %q", sp.SessionID)
	}
	if sp.Model != "grok-4.7" {
		t.Errorf("model: got %q", sp.Model)
	}
	wantTokens(t, "input", sp.InputTokens, 204311-171008)
	wantTokens(t, "cache_read", sp.CacheReadTokens, 171008)
	wantTokens(t, "output", sp.OutputTokens, 1294)
	// grok-4.7: $2 in, $0.50 cached, $6 out per MTok.
	wantCost(t, sp.CostUSD, ((204311-171008)*2.0+171008*0.50+1294*6.0)/1e6)
}

// sampling.stream_decode repeats output_tokens per sample; counted on the turn.
func TestGrokSampleSpanCarriesNoTokens(t *testing.T) {
	sp := ingestOne(t, "grok-cli", "sampling.stream_decode", kv{"output_tokens", i("317")})
	if sp.OutputTokens != nil {
		t.Errorf("stream_decode must carry no tokens, got %d", *sp.OutputTokens)
	}
}

// tool.decision is the permission check; tool.execution is the call.
func TestGrokToolSpans(t *testing.T) {
	exec := ingestOne(t, "grok-cli", "tool.execution",
		kv{"session_id", s(grokSessID)}, kv{"tool_name", s("read_file")})
	if exec.ToolName != "read_file" || exec.SessionID != grokSessID {
		t.Errorf("tool.execution: tool=%q session=%q", exec.ToolName, exec.SessionID)
	}
	decision := ingestOne(t, "grok-cli", "tool.decision", kv{"tool_name", s("read_file")})
	if decision.ToolName != "" {
		t.Errorf("tool.decision must not count as a tool call, got %q", decision.ToolName)
	}
}

// The adapters key on service.name: a Claude Code span with the same attribute
// names is decoded exactly as before.
func TestClaudeCodeSpansAreNotAdapted(t *testing.T) {
	sp := ingestOne(t, "claude-code", "claude_code.tool",
		kv{"session_id", s(grokSessID)},
		kv{"model_id", s("grok-4.7")},
		kv{"tool_name", s("Read")},
		kv{"output_tokens", i("10")},
	)
	if sp.SessionID != "" || sp.Model != "" {
		t.Errorf("claude-code: session=%q model=%q, want both empty", sp.SessionID, sp.Model)
	}
	if sp.ToolName != "Read" {
		t.Errorf("claude-code tool: got %q", sp.ToolName)
	}
	wantTokens(t, "output", sp.OutputTokens, 10)
}

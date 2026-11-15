package ingest

import (
	"fmt"
	"strings"

	"github.com/Flopsstuff/cotel/internal/storage"
)

// Producer adapters map the span attributes of harnesses other than Claude Code
// onto the columns decodeSpan fills from Claude Code's names. Each one was
// written against what the harness actually exports through Paperclip, not
// against a spec: neither harness follows a published convention for sessions
// or usage, and both emit several spans per logical event, so the rules below
// also choose which span carries a count, to keep sums from double counting.
//
// They run after the generic extraction and before cost is derived, and only
// for spans whose service.name names the harness. Claude Code spans are never
// touched.

// normalizeProducer applies the adapter for svcName, if there is one.
func normalizeProducer(svcName, spanName string, attrs map[string]any, s *storage.Span) {
	svc := strings.ToLower(svcName)
	switch {
	case strings.HasPrefix(svc, "codex"):
		normalizeCodex(attrs, s)
	case strings.HasPrefix(svc, "grok"):
		normalizeGrok(spanName, attrs, s)
	}
}

// Codex (service.name "codex-app-server", Rust tracing over OTLP).
//
//   - Session: conversation.id on exec spans; on the turn span and the tool
//     handler spans the same id travels as thread.id. thread.id is also the
//     tracing layer's OS thread number (an integer) on every other span, so it
//     counts only when it is a UUID string.
//   - Usage: the turn span (session_task.turn) carries the turn's totals under
//     codex.turn.token_usage.*, next to model. The per-request handle_responses
//     spans repeat the same tokens as gen_ai.usage.* without a model; they sum
//     to the turn total, so they are not read. input_tokens includes the cached
//     part; non_cached_input_tokens is what bills at the input rate.
//   - Tools: a tool invocation is the span with both tool_name and call_id
//     (exec, exec_command). code_mode.broker.invoke_tool names the same nested
//     call again without a call_id and is not counted.
func normalizeCodex(attrs map[string]any, s *storage.Span) {
	if s.SessionID == "" {
		if v := str(attrs["conversation.id"]); v != "" {
			s.SessionID = v
		} else if v, ok := attrs["thread.id"].(string); ok && isUUID(v) {
			s.SessionID = v
		}
	}

	const p = "codex.turn.token_usage."
	if _, ok := attrs[p+"input_tokens"]; ok {
		total := toInt64(attrs[p+"input_tokens"])
		cached := toInt64(attrs[p+"cached_input_tokens"])
		in := total - cached
		if v, ok := attrs[p+"non_cached_input_tokens"]; ok {
			in = toInt64(v)
		}
		s.InputTokens = ptr(max(in, 0))
		s.CacheReadTokens = ptr(cached)
		s.CacheWriteTokens = ptr(toInt64(attrs[p+"cache_write_input_tokens"]))
		s.OutputTokens = ptr(toInt64(attrs[p+"output_tokens"]))
	}

	if s.ToolName != "" && str(attrs["call_id"]) == "" {
		s.ToolName = ""
	}
}

// Grok CLI (service.name "grok-cli").
//
//   - Session: session_id; model: model_id.
//   - Usage: session.process_conversation_turn carries the turn's totals.
//     sampling.stream_decode repeats output_tokens per sample and would double
//     the turn's output, so token counts are kept on the turn span only.
//     input_tokens includes cache_read_tokens (OpenAI-style accounting), so the
//     cached part is taken out of the input column.
//   - Tools: tool.execution is the call; tool.decision is the permission check
//     before it and names the same tool.
func normalizeGrok(spanName string, attrs map[string]any, s *storage.Span) {
	if s.SessionID == "" {
		s.SessionID = str(attrs["session_id"])
	}
	if s.Model == "" {
		s.Model = str(attrs["model_id"])
	}

	if spanName == "session.process_conversation_turn" {
		if s.InputTokens != nil && s.CacheReadTokens != nil {
			s.InputTokens = ptr(max(*s.InputTokens-*s.CacheReadTokens, 0))
		}
	} else {
		s.InputTokens, s.OutputTokens = nil, nil
		s.CacheReadTokens, s.CacheWriteTokens = nil, nil
	}

	if spanName != "tool.execution" {
		s.ToolName = ""
	}
}

func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func ptr(n int64) *int64 { return &n }

// isUUID reports whether s has the 8-4-4-4-12 hex shape of a UUID.
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return false
			}
		}
	}
	return true
}

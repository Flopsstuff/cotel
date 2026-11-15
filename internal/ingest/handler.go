// Package ingest accepts OTLP/HTTP trace payloads and persists them.
package ingest

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	collectortracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"

	"github.com/Flopsstuff/cotel/internal/pricing"
	"github.com/Flopsstuff/cotel/internal/storage"
)

type Storer interface {
	InsertSpan(storage.Span) error
}

// AgentResolver maps a Paperclip agent id to the name of the actor it belongs
// to ("" when none). *storage.DB implements it; a Storer that does not is simply
// never asked, and such spans stay anonymous.
type AgentResolver interface {
	ActorNameByPaperclipAgentID(agentID string) (string, error)
}

// PaperclipAgentIDAttr is the attribute Paperclip sets on every span it exports
// for an agent, via OTEL_RESOURCE_ATTRIBUTES (ADR-0029).
const PaperclipAgentIDAttr = "paperclip.agent.id"

type Handler struct {
	store Storer
	queue chan storage.Span
	done  chan struct{}

	// Spans dropped by dropSpan since the last log line; see noteDropped.
	dropMu     sync.Mutex
	dropped    int64
	dropLogged time.Time
}

func New(store Storer) *Handler {
	h := &Handler{
		store: store,
		queue: make(chan storage.Span, 4096),
		done:  make(chan struct{}),
	}
	go h.drain()
	return h
}

// drain serialises writes to DuckDB (one writer at a time).
func (h *Handler) drain() {
	for s := range h.queue {
		_ = h.store.InsertSpan(s)
	}
	close(h.done)
}

// Flush closes the write queue and waits for all pending spans to be stored.
// Call in tests; not for production use.
func (h *Handler) Flush() {
	close(h.queue)
	<-h.done
}

// dropLogEvery bounds how often noteDropped logs: Codex sends a batch every few
// seconds while it runs, and a line per batch would drown the log.
const dropLogEvery = time.Minute

// noteDropped counts spans dropped by dropSpan and logs the running total at
// most once per dropLogEvery, so the volume being discarded stays visible.
func (h *Handler) noteDropped(n int64) {
	if n == 0 {
		return
	}
	h.dropMu.Lock()
	defer h.dropMu.Unlock()
	h.dropped += n
	if now := time.Now(); now.Sub(h.dropLogged) >= dropLogEvery {
		log.Printf("ingest: dropped %d Codex spans without a session (ADR-0030)", h.dropped)
		h.dropped, h.dropLogged = 0, now
	}
}

// ServeHTTP handles POST /v1/traces (OTLP/HTTP protobuf or JSON).
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20)) // 10 MB cap
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req collectortracepb.ExportTraceServiceRequest

	ct := r.Header.Get("Content-Type")
	switch ct {
	case "application/json", "":
		if err := protojson.Unmarshal(body, &req); err != nil {
			http.Error(w, fmt.Sprintf("json decode: %v", err), http.StatusBadRequest)
			return
		}
	default: // application/x-protobuf or anything else
		if err := proto.Unmarshal(body, &req); err != nil {
			http.Error(w, fmt.Sprintf("proto decode: %v", err), http.StatusBadRequest)
			return
		}
	}

	// Actor identity comes from the auth middleware context: an actor token wins.
	// Only when a shared ingest key authorized the request and no actor token
	// named the sender may the paperclip.agent.id attribute name it (ADR-0029).
	actorName := ActorNameFromContext(r.Context())
	byAgent := actorName == "" && IngestKeyAuthFromContext(r.Context())
	resolve := h.agentResolver(byAgent)

	var dropped int64
	for _, rs := range req.ResourceSpans {
		svcName := attrStr(rs.GetResource().GetAttributes(), "service.name")
		resAttrs := attrsToMap(rs.GetResource().GetAttributes())
		resJSON, _ := json.Marshal(resAttrs)
		resActor := actorName
		if byAgent {
			resActor = resolve(attrStr(rs.GetResource().GetAttributes(), PaperclipAgentIDAttr))
		}

		for _, ss := range rs.ScopeSpans {
			for _, sp := range ss.Spans {
				spanActor := resActor
				if byAgent && spanActor == "" {
					// Some exporters put the attribute on the span, not the resource.
					spanActor = resolve(attrStr(sp.GetAttributes(), PaperclipAgentIDAttr))
				}
				s := decodeSpan(sp, svcName, resAttrs, string(resJSON), spanActor)
				if dropSpan(svcName, &s) {
					dropped++
					continue
				}
				h.queue <- s
			}
		}
	}
	h.noteDropped(dropped)

	writeOTLPSuccess(w, r)
}

// agentResolver returns a per-request lookup from Paperclip agent id to actor
// name, memoized so a batch from one agent costs one query. When attribution by
// agent is not allowed, or the store cannot resolve agents, every lookup is "".
func (h *Handler) agentResolver(enabled bool) func(agentID string) string {
	r, ok := h.store.(AgentResolver)
	if !enabled || !ok {
		return func(string) string { return "" }
	}
	seen := map[string]string{}
	return func(agentID string) string {
		// The dashboard stores the id lowercased; match a sender's casing to it.
		agentID = strings.ToLower(strings.TrimSpace(agentID))
		if agentID == "" {
			return ""
		}
		if name, hit := seen[agentID]; hit {
			return name
		}
		name, err := r.ActorNameByPaperclipAgentID(agentID)
		if err != nil {
			log.Printf("ingest: resolve paperclip agent %s: %v", agentID, err)
			name = ""
		}
		seen[agentID] = name
		return name
	}
}

// sessionIDKeys are the attribute key names Claude Code may use for the session
// identifier, checked in order of preference.
var sessionIDKeys = []string{"session.id", "claude.session.id", "cc.session.id"}

func decodeSpan(sp *tracepb.Span, svcName string, resAttrs map[string]any, resJSON string, actorName string) storage.Span {
	attrs := attrsToMap(sp.Attributes)
	attrsJSON, _ := json.Marshal(attrs)

	s := storage.Span{
		TraceID:       fmt.Sprintf("%x", sp.TraceId),
		SpanID:        fmt.Sprintf("%x", sp.SpanId),
		ParentSpanID:  fmt.Sprintf("%x", sp.ParentSpanId),
		Name:          sp.Name,
		StartTime:     time.Unix(0, int64(sp.StartTimeUnixNano)),
		EndTime:       time.Unix(0, int64(sp.EndTimeUnixNano)),
		ServiceName:   svcName,
		StatusCode:    int32(sp.GetStatus().GetCode()),
		Attributes:    string(attrsJSON),
		ResourceAttrs: resJSON,
	}

	// session.id: check span attributes first, fall back to resource attributes
	// (OpenTelemetry convention places process/session identity on the resource).
	for _, key := range sessionIDKeys {
		if v, ok := attrs[key]; ok {
			s.SessionID = fmt.Sprintf("%v", v)
			break
		}
	}
	if s.SessionID == "" {
		for _, key := range sessionIDKeys {
			if v, ok := resAttrs[key]; ok {
				s.SessionID = fmt.Sprintf("%v", v)
				break
			}
		}
	}

	// Actor identity is injected by auth middleware; empty = anonymous (stored as NULL).
	s.ActorID = actorName

	if v, ok := attrs["model"]; ok {
		s.Model = fmt.Sprintf("%v", v)
	}
	if v, ok := attrs["tool_name"]; ok {
		s.ToolName = fmt.Sprintf("%v", v)
	} else if v, ok := attrs["tool.name"]; ok {
		s.ToolName = fmt.Sprintf("%v", v)
	}
	if v, ok := attrs["input_tokens"]; ok {
		n := toInt64(v)
		s.InputTokens = &n
	}
	if v, ok := attrs["output_tokens"]; ok {
		n := toInt64(v)
		s.OutputTokens = &n
	}
	if v, ok := attrs["cache_read_tokens"]; ok {
		n := toInt64(v)
		s.CacheReadTokens = &n
	}
	if v, ok := attrs["cache_creation_tokens"]; ok {
		n := toInt64(v)
		s.CacheWriteTokens = &n
	} else if v, ok := attrs["cache_write_tokens"]; ok {
		n := toInt64(v)
		s.CacheWriteTokens = &n
	}
	if v, ok := attrs["cost_usd"]; ok {
		f := toFloat64(v)
		s.CostUSD = &f
	}

	normalizeProducer(svcName, sp.Name, attrs, &s)

	// Derive cost from token counts when the producer did not supply cost_usd.
	// Claude Code beta telemetry omits the field; we compute it ingest-time so
	// the stored row is always queryable without a per-query price JOIN.
	if s.CostUSD == nil && s.Model != "" {
		in := derefInt64(s.InputTokens)
		out := derefInt64(s.OutputTokens)
		cr := derefInt64(s.CacheReadTokens)
		cw := derefInt64(s.CacheWriteTokens)
		if in+out+cr+cw > 0 {
			if c := pricing.Compute(s.Model, in, out, cr, cw); c > 0 {
				s.CostUSD = &c
			}
		}
	}

	return s
}

func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

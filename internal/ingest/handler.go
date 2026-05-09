// Package ingest accepts OTLP/HTTP trace payloads and persists them.
package ingest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	collectortracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"

	"github.com/Flopsstuff/cotel/internal/storage"
)

type Storer interface {
	InsertSpan(storage.Span) error
}

type Handler struct {
	store Storer
	queue chan storage.Span
	done  chan struct{}
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

	for _, rs := range req.ResourceSpans {
		svcName := attrStr(rs.GetResource().GetAttributes(), "service.name")
		resJSON, _ := json.Marshal(attrsToMap(rs.GetResource().GetAttributes()))

		for _, ss := range rs.ScopeSpans {
			for _, sp := range ss.Spans {
				h.queue <- decodeSpan(sp, svcName, string(resJSON))
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{}`))
}

func decodeSpan(sp *tracepb.Span, svcName, resJSON string) storage.Span {
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

	if v, ok := attrs["session.id"]; ok {
		s.SessionID = fmt.Sprintf("%v", v)
	}
	if v, ok := attrs["model"]; ok {
		s.Model = fmt.Sprintf("%v", v)
	}
	if v, ok := attrs["tool.name"]; ok {
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
	if v, ok := attrs["cache_write_tokens"]; ok {
		n := toInt64(v)
		s.CacheWriteTokens = &n
	}
	if v, ok := attrs["cost_usd"]; ok {
		f := toFloat64(v)
		s.CostUSD = &f
	}
	return s
}

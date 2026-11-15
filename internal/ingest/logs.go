package ingest

import (
	"io"
	"net/http"
)

// maxLogsBody caps how much of a /v1/logs body is read before it is discarded.
const maxLogsBody = 10 << 20 // 10 MiB

// LogsSink accepts OTLP/HTTP log exports (POST /v1/logs) and discards them.
// cotel stores traces only, but exporters that also send logs (Claude Code run
// by Paperclip, for one) retry or complain on a 404, so the endpoint answers
// success with an empty ExportLogsServiceResponse.
type LogsSink struct{}

// NewLogsSink returns the /v1/logs handler.
func NewLogsSink() LogsSink { return LogsSink{} }

func (LogsSink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	_, _ = io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, maxLogsBody))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{}`))
}

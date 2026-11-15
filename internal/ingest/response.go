package ingest

import (
	"net/http"
	"strings"
)

// writeOTLPSuccess answers a successful OTLP/HTTP export in the encoding the
// request used, as the OTLP spec requires: an empty JSON object for a JSON
// request, and an empty Export*ServiceResponse - which encodes to zero bytes -
// for a protobuf one. Exporters that use protobuf (Claude Code when Paperclip
// configures it) try to decode the body as protobuf, and a JSON "{}" is not one.
func writeOTLPSuccess(w http.ResponseWriter, r *http.Request) {
	if isJSONRequest(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
}

// isJSONRequest reports whether an OTLP request body is JSON. A missing
// Content-Type is treated as JSON, matching how the traces handler decodes it.
func isJSONRequest(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	return ct == "" || strings.HasPrefix(ct, "application/json")
}

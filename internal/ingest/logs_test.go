package ingest

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLogsSink(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		contentType string
		body        []byte
		wantCode    int
		wantCT      string
		wantBody    string
	}{
		// An empty ExportLogsServiceResponse encodes to zero protobuf bytes.
		{"post protobuf", http.MethodPost, "application/x-protobuf", []byte{0x0a, 0x02, 0x08, 0x01}, 200, "application/x-protobuf", ""},
		{"post json", http.MethodPost, "application/json", []byte(`{"resourceLogs":[]}`), 200, "application/json", `{}`},
		{"post json with charset", http.MethodPost, "application/json; charset=utf-8", []byte(`{}`), 200, "application/json", `{}`},
		{"post empty, no content type", http.MethodPost, "", nil, 200, "application/json", `{}`},
		{"post over the cap", http.MethodPost, "application/x-protobuf", bytes.Repeat([]byte("x"), maxLogsBody+1), 200, "application/x-protobuf", ""},
		{"get", http.MethodGet, "application/x-protobuf", nil, 405, "", ""},
		{"put", http.MethodPut, "application/x-protobuf", nil, 405, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/v1/logs", bytes.NewReader(tt.body))
			if tt.contentType != "" {
				req.Header.Set("Content-Type", tt.contentType)
			}
			w := httptest.NewRecorder()
			NewLogsSink().ServeHTTP(w, req)

			if w.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d", w.Code, tt.wantCode)
			}
			if tt.wantCode != http.StatusOK {
				return
			}
			if got := w.Body.String(); got != tt.wantBody {
				t.Errorf("body = %q, want %q", got, tt.wantBody)
			}
			if ct := w.Header().Get("Content-Type"); ct != tt.wantCT {
				t.Errorf("Content-Type = %q, want %q", ct, tt.wantCT)
			}
		})
	}
}

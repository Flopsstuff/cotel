package ingest

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLogsSink(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		body     []byte
		wantCode int
		wantBody string
	}{
		{"post protobuf", http.MethodPost, []byte{0x0a, 0x02, 0x08, 0x01}, 200, `{}`},
		{"post empty", http.MethodPost, nil, 200, `{}`},
		{"post over the cap", http.MethodPost, bytes.Repeat([]byte("x"), maxLogsBody+1), 200, `{}`},
		{"get", http.MethodGet, nil, 405, ""},
		{"put", http.MethodPut, nil, 405, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/v1/logs", bytes.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/x-protobuf")
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
			if ct := w.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
		})
	}
}

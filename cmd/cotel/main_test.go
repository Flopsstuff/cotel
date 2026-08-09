package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestReadyGate covers the gate contract in isolation: closed → retryable 503,
// open → delegate to the installed handler.
func TestReadyGate(t *testing.T) {
	g := &readyGate{}

	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/traces", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("closed gate: want 503, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Errorf("closed gate: 503 response missing Retry-After header")
	}

	g.set(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	rec2 := httptest.NewRecorder()
	g.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/v1/traces", nil))
	if rec2.Code != http.StatusNoContent {
		t.Fatalf("open gate: want 204 (delegated), got %d", rec2.Code)
	}
}

// TestGatedServersAcceptBeforeReady is the FLO-556 regression guard, measured
// against real TCP sockets rather than argued: while the gate is still closed
// (simulating a slow storage.Open), the ingest and dashboard ports must ACCEPT
// the connection and answer 503 on every probe — never a dial error / reset,
// which is what silently dropped telemetry on each deploy. After the gate is
// opened the request is delegated to the real handler.
func TestGatedServersAcceptBeforeReady(t *testing.T) {
	gs, err := startGatedServers("127.0.0.1:0", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("startGatedServers: %v", err)
	}
	defer gs.Close()

	client := &http.Client{Timeout: time.Second}
	probe := func(url string) (*http.Response, error) {
		return client.Post(url, "application/json", strings.NewReader("{}"))
	}

	// Probe both ports for a window during which storage is "still opening".
	// Every probe must connect and return 503.
	const window = 300 * time.Millisecond
	targets := map[string]string{
		"ingest":    "http://" + gs.ingestAddr + "/v1/traces",
		"dashboard": "http://" + gs.dashAddr + "/api/v1/health",
	}
	for name, url := range targets {
		probes, refused := 0, 0
		deadline := time.Now().Add(window)
		for time.Now().Before(deadline) {
			resp, err := probe(url)
			if err != nil {
				refused++
				t.Fatalf("%s port refused a connection during the init window (FLO-556 bug): %v", name, err)
			}
			if resp.StatusCode != http.StatusServiceUnavailable {
				resp.Body.Close()
				t.Fatalf("%s: want 503 while gate closed, got %d", name, resp.StatusCode)
			}
			if resp.Header.Get("Retry-After") == "" {
				t.Errorf("%s: 503 response missing Retry-After header", name)
			}
			resp.Body.Close()
			probes++
			time.Sleep(10 * time.Millisecond)
		}
		t.Logf("%s: %d probes answered 503, %d connections refused over a %s init window", name, probes, refused, window)
	}

	// Storage ready → open the gates → traffic is delegated.
	gs.ingest.set(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	resp, err := probe(targets["ingest"])
	if err != nil {
		t.Fatalf("ingest probe after gate opened: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("ingest after gate opened: want 202 (delegated), got %d", resp.StatusCode)
	}
}

// TestRunHealthcheck verifies the container HEALTHCHECK helper maps /healthz
// results to process exit codes.
func TestRunHealthcheck(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ok.Close()

	// host:port form.
	if code := runHealthcheck(ok.Listener.Addr().String()); code != 0 {
		t.Errorf("healthy /healthz (host:port): want exit 0, got %d", code)
	}
	// ":port" form must resolve to 127.0.0.1:port.
	if _, port, ok2 := strings.Cut(ok.Listener.Addr().String(), ":"); ok2 {
		if code := runHealthcheck(":" + port); code != 0 {
			t.Errorf("healthy /healthz (:port): want exit 0, got %d", code)
		}
	}

	degraded := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer degraded.Close()
	if code := runHealthcheck(degraded.Listener.Addr().String()); code != 1 {
		t.Errorf("503 /healthz: want exit 1, got %d", code)
	}

	// Nothing listening → dial error → exit 1.
	if code := runHealthcheck("127.0.0.1:1"); code != 1 {
		t.Errorf("unreachable /healthz: want exit 1, got %d", code)
	}
}

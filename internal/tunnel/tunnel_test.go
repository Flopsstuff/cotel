package tunnel_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Flopsstuff/cotel/internal/tunnel"
)

// readyBody is cloudflared 2026.9.3's /ready payload, verified against the
// binary the image pins.
const readyBody = `{"status":200,"readyConnections":4,"connectorId":"eca7539d-e94c-4516-a71e-dac7690f5430"}`

func proberFor(t *testing.T, status int, body string) *tunnel.Prober {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ready" {
			t.Errorf("want a request to /ready, got %s", r.URL.Path)
		}
		w.WriteHeader(status)
		w.Write([]byte(body)) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return tunnel.New(strings.TrimPrefix(srv.URL, "http://"))
}

func TestHealthRegisteredConnectionsAreOK(t *testing.T) {
	h := proberFor(t, http.StatusOK, readyBody).Health(context.Background())
	if !h.OK {
		t.Errorf("want ok, got %+v", h)
	}
	if h.ReadyConnections == nil || *h.ReadyConnections != 4 {
		t.Errorf("want 4 ready connections, got %v", h.ReadyConnections)
	}
	if h.Error != "" {
		t.Errorf("want no error, got %q", h.Error)
	}
}

// cloudflared is running but has registered nothing: traffic is dropped exactly
// as if the process were gone, so process liveness is not the signal.
func TestHealthZeroConnectionsIsNotOK(t *testing.T) {
	h := proberFor(t, http.StatusServiceUnavailable, `{"status":503,"readyConnections":0}`).Health(context.Background())
	if h.OK {
		t.Errorf("zero connections must not read as ok, got %+v", h)
	}
	if h.ReadyConnections == nil || *h.ReadyConnections != 0 {
		t.Errorf("a cloudflared that answered must report its count, got %v", h.ReadyConnections)
	}
	if h.Error == "" {
		t.Error("want a reason")
	}
}

// The failure mode this whole field exists for: cloudflared exited, so nothing
// answers on the metrics port.
func TestHealthUnreachableListenerIsNotOK(t *testing.T) {
	// Port 0 never has a listener, so this fails without waiting for a timeout.
	h := tunnel.New("127.0.0.1:0").Health(context.Background())
	if h.OK {
		t.Errorf("want not ok, got %+v", h)
	}
	if h.ReadyConnections != nil {
		t.Errorf("an unreachable cloudflared has no count, got %v", *h.ReadyConnections)
	}
	if !strings.Contains(h.Error, "unreachable") {
		t.Errorf("want an unreachable error, got %q", h.Error)
	}
}

func TestHealthUnreadableBodyIsNotOK(t *testing.T) {
	h := proberFor(t, http.StatusOK, "not json").Health(context.Background())
	if h.OK || h.Error == "" {
		t.Errorf("want not ok with a reason, got %+v", h)
	}
}

// Unset means "this deployment runs no tunnel", which is not the same claim as
// "the tunnel is down" — a tokenless local or CI instance must make neither.
func TestFromEnvIsNilWithoutAnAddress(t *testing.T) {
	t.Setenv(tunnel.EnvMetricsAddr, "")
	if p := tunnel.FromEnv(); p != nil {
		t.Errorf("want nil prober, got one for %q", p.Addr())
	}
	t.Setenv(tunnel.EnvMetricsAddr, "  ")
	if p := tunnel.FromEnv(); p != nil {
		t.Errorf("whitespace is not an address, got a prober for %q", p.Addr())
	}
	t.Setenv(tunnel.EnvMetricsAddr, "127.0.0.1:2000")
	p := tunnel.FromEnv()
	if p == nil {
		t.Fatal("want a prober when the address is set")
	}
	if p.Addr() != "127.0.0.1:2000" {
		t.Errorf("want the configured address, got %q", p.Addr())
	}
}

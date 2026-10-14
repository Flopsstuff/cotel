// Package tunnel reports the state of the cloudflared process the container
// entrypoint runs beside cotel.
//
// A dead tunnel is invisible from inside cotel: it has its own process, its own
// failure modes, and the application keeps answering on the LAN while the
// public ingest URL every client exports to returns a Cloudflare error. The
// state comes from cloudflared's own metrics listener rather than from process
// liveness, because a running cloudflared with no registered edge connection
// drops traffic exactly like a dead one.
package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// EnvMetricsAddr names cloudflared's metrics listener. The entrypoint exports
// it only on the paths where it actually started a tunnel, so unset means "no
// tunnel is expected here" rather than "the tunnel is down" — which is what
// keeps a tokenless local or CI instance out of the tunnel verdict.
const EnvMetricsAddr = "COTEL_TUNNEL_METRICS_ADDR"

// probeTimeout bounds one /ready request. The listener is in this container, so
// anything slower than this is as good as unreachable — and /healthz drives the
// container HEALTHCHECK, whose own timeout is 5s.
const probeTimeout = 2 * time.Second

// Health is the tunnel's state, reported under the "tunnel" key of /healthz.
type Health struct {
	OK bool `json:"ok"`
	// Null when cloudflared could not be asked at all. That is distinct from
	// zero, which means it answered and has no edge connection.
	ReadyConnections *int   `json:"ready_connections"`
	Error            string `json:"error,omitempty"`
}

// Prober asks cloudflared's metrics listener for its readiness.
type Prober struct {
	addr   string
	url    string
	client *http.Client
}

// New returns a Prober for a cloudflared metrics listener address
// (host:port, as passed to cloudflared's --metrics).
func New(addr string) *Prober {
	return &Prober{
		addr:   addr,
		url:    "http://" + addr + "/ready",
		client: &http.Client{Timeout: probeTimeout},
	}
}

// FromEnv returns a Prober for the address in EnvMetricsAddr, or nil when the
// variable is unset or empty — the deployment then runs no tunnel and /healthz
// says nothing about one.
func FromEnv() *Prober {
	addr := strings.TrimSpace(os.Getenv(EnvMetricsAddr))
	if addr == "" {
		return nil
	}
	return New(addr)
}

// Addr is the metrics listener this Prober asks.
func (p *Prober) Addr() string { return p.addr }

// Health probes cloudflared's /ready route. Every failure is reported as data
// rather than an error: the caller is a health endpoint that must answer.
func (p *Prober) Health(ctx context.Context) Health {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url, nil)
	if err != nil {
		return Health{Error: fmt.Sprintf("cloudflared metrics address %q is unusable: %v", p.addr, err)}
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return Health{Error: fmt.Sprintf("cloudflared metrics listener %s unreachable: %v", p.addr, err)}
	}
	defer resp.Body.Close() //nolint:errcheck

	var body struct {
		ReadyConnections *int `json:"readyConnections"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Health{Error: fmt.Sprintf("cloudflared /ready returned HTTP %d with an unreadable body: %v", resp.StatusCode, err)}
	}

	h := Health{ReadyConnections: body.ReadyConnections}
	switch {
	case body.ReadyConnections == nil:
		h.Error = fmt.Sprintf("cloudflared /ready returned HTTP %d without a connection count", resp.StatusCode)
	case *body.ReadyConnections < 1:
		h.Error = "cloudflared has no registered edge connection"
	case resp.StatusCode != http.StatusOK:
		h.Error = fmt.Sprintf("cloudflared /ready returned HTTP %d", resp.StatusCode)
	default:
		h.OK = true
	}
	return h
}

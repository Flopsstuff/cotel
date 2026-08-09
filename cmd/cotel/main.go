package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Flopsstuff/cotel/internal/api"
	"github.com/Flopsstuff/cotel/internal/api/auth"
	"github.com/Flopsstuff/cotel/internal/dashboard"
	"github.com/Flopsstuff/cotel/internal/export"
	"github.com/Flopsstuff/cotel/internal/importpkg"
	"github.com/Flopsstuff/cotel/internal/ingest"
	"github.com/Flopsstuff/cotel/internal/storage"
)

func main() {
	dbQuery := flag.String("db-query", "", "run SQL query against DuckDB, print first column of first row, and exit")
	healthcheck := flag.Bool("healthcheck", false, "probe the local dashboard /healthz and exit 0 (ready) or 1; used by the container HEALTHCHECK")
	flag.Parse()

	dbPath := env("COTEL_DB_PATH", "/data/cotel.duckdb")
	ingestAddr := env("COTEL_INGEST_ADDR", ":4318")
	dashAddr := env("COTEL_DASH_ADDR", ":8080")

	if *healthcheck {
		os.Exit(runHealthcheck(dashAddr))
	}

	if *dbQuery != "" {
		ro, err := storage.OpenReadOnly(dbPath)
		if err != nil {
			log.Fatalf("open storage: %v", err)
		}
		defer ro.Close()
		var val interface{}
		if err := ro.QueryRow(*dbQuery).Scan(&val); err != nil {
			log.Fatalf("db-query: %v", err)
		}
		fmt.Println(val)
		return
	}

	// Bind BEFORE storage.Open: WAL replay + schema migration can block for
	// minutes on a large DB, and a bound port answering a retryable 503 keeps
	// OTLP clients retrying where a connection reset would drop their spans.
	srv, err := startGatedServers(ingestAddr, dashAddr)
	if err != nil {
		log.Fatalf("%v", err)
	}
	log.Printf("listening on ingest %s and dashboard %s (storage initialising, serving 503 until ready)", srv.ingestAddr, srv.dashAddr)

	openStart := time.Now()
	log.Printf("opening db %s", dbPath)
	db, err := storage.Open(dbPath)
	if err != nil {
		log.Fatalf("open storage: %v", err)
	}
	defer db.Close()
	log.Printf("db ready: schema/migrations applied in %s", time.Since(openStart).Round(time.Millisecond))

	retentionCfg := storage.RetentionConfig{
		RawDays:       envInt("COTEL_RETENTION_RAW_DAYS", storage.DefaultRetention.RawDays),
		AggregateDays: envInt("COTEL_RETENTION_AGGREGATE_DAYS", storage.DefaultRetention.AggregateDays),
	}
	retentionInterval := envDuration("COTEL_RETENTION_INTERVAL", 6*time.Hour)
	go db.RunRetentionWorker(retentionCfg, retentionInterval)

	ingestMux := http.NewServeMux()
	ingestMux.Handle("/v1/traces", auth.Middleware(db, ingest.New(db)))

	ro := db.ReadOnly()
	apiHandler := api.New(ro).SetPublicIngestURL(parsePublicIngestURL(os.Getenv("COTEL_PUBLIC_INGEST_URL"))).SetUserStore(db)
	dashMux := http.NewServeMux()
	dashMux.Handle("/api/v1/export", auth.Middleware(db, export.NewHandler(db)))
	dashMux.Handle("/api/v1/import", auth.Middleware(db, importpkg.NewHandler(db)))
	dashMux.Handle("/api/v1/", apiHandler)
	dashMux.Handle("/", dashboard.New(ro))

	// Open the gates: from here the ports serve live traffic instead of 503.
	srv.ingest.set(ingestMux)
	srv.dash.set(dashMux)
	log.Printf("ready: serving live traffic on ingest %s and dashboard %s", srv.ingestAddr, srv.dashAddr)

	// Block forever; the serve goroutines own the listeners. A fatal serve error
	// terminates the process via log.Fatalf inside serveGate.
	select {}
}

// readyGate is an http.Handler that returns 503 until its real handler is
// installed via set, then delegates every request to it. It lets a listener
// bind and accept connections during slow startup (WAL replay + schema
// migration) so clients get a retryable 503 rather than a connection reset.
type readyGate struct {
	h atomic.Pointer[http.Handler]
}

func (g *readyGate) set(h http.Handler) { g.h.Store(&h) }

func (g *readyGate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if hp := g.h.Load(); hp != nil {
		(*hp).ServeHTTP(w, r)
		return
	}
	// Retry-After tells well-behaved OTLP/HTTP exporters to back off and retry,
	// which is exactly what turns a "deploy restart" into a no-loss event.
	w.Header().Set("Retry-After", "5")
	http.Error(w, "cotel: storage initialising, retry shortly", http.StatusServiceUnavailable)
}

// gatedServers holds the two readiness gates and their resolved listen
// addresses. The gates start closed (503) and are opened by main once storage
// is ready.
type gatedServers struct {
	ingest, dash         *readyGate
	ingestAddr, dashAddr string
	servers              []*http.Server
}

// startGatedServers binds both listeners and starts serving their gates in the
// background immediately, before the caller runs the blocking storage.Open.
// Binding here (not after Open) is the whole point: the ports accept
// connections during initialisation. Returns the resolved addresses so callers
// (and tests) can reach the ports even when :0 was requested.
func startGatedServers(ingestAddr, dashAddr string) (*gatedServers, error) {
	ingestLn, err := net.Listen("tcp", ingestAddr)
	if err != nil {
		return nil, fmt.Errorf("listen ingest %s: %w", ingestAddr, err)
	}
	dashLn, err := net.Listen("tcp", dashAddr)
	if err != nil {
		ingestLn.Close() //nolint:errcheck
		return nil, fmt.Errorf("listen dashboard %s: %w", dashAddr, err)
	}

	gs := &gatedServers{
		ingest:     &readyGate{},
		dash:       &readyGate{},
		ingestAddr: ingestLn.Addr().String(),
		dashAddr:   dashLn.Addr().String(),
	}
	gs.servers = append(gs.servers,
		serveGate("ingest", ingestLn, gs.ingest),
		serveGate("dashboard", dashLn, gs.dash),
	)
	return gs, nil
}

// Close shuts down both HTTP servers. Unused by main (which serves forever) but
// used by tests to stop the background serve goroutines cleanly.
func (gs *gatedServers) Close() {
	for _, s := range gs.servers {
		s.Close() //nolint:errcheck
	}
}

// serveGate serves gate on ln in a background goroutine. A real serve error is
// fatal, matching the previous behaviour of a failed ListenAndServe; a clean
// shutdown via (*http.Server).Close returns ErrServerClosed and is ignored.
func serveGate(name string, ln net.Listener, gate *readyGate) *http.Server {
	srv := &http.Server{Handler: gate}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Fatalf("%s: %v", name, err)
		}
	}()
	return srv
}

// runHealthcheck probes the local dashboard /healthz and returns a process exit
// code: 0 when ready (HTTP 200), 1 otherwise. Self-contained so the container
// HEALTHCHECK needs no curl/wget in the runtime image. While storage is still
// opening, the gate returns 503, so the container reports "starting" until the
// DB is ready.
func runHealthcheck(dashAddr string) int {
	host := dashAddr
	if strings.HasPrefix(host, ":") {
		host = "127.0.0.1" + host
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + host + "/healthz")
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		return 1
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: /healthz returned %d\n", resp.StatusCode)
		return 1
	}
	return 0
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
		log.Printf("warning: invalid %s=%q, using default %d", key, v, fallback)
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		log.Printf("warning: invalid %s=%q, using default %s", key, v, fallback)
	}
	return fallback
}

// parsePublicIngestURL validates raw as an absolute http/https URL.
// Returns the trimmed URL on success, or "" with a logged warning if invalid.
func parsePublicIngestURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		log.Printf("warning: COTEL_PUBLIC_INGEST_URL=%q is not a valid URL, falling back to localhost", raw)
		return ""
	}
	return strings.TrimRight(raw, "/")
}

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
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

	// An open that aborts inside libduckdb kills the process from C++, where no
	// Go handler runs, so this warning is the only breadcrumb linking the crash
	// to the checkpoint that produced the unreplayable WAL.
	if marker, ok := readCheckpointFailureMarker(dbPath); ok {
		log.Printf("WARNING: a previous checkpoint left %s (%s); if this open crashes, follow docs/operations/duckdb-recovery.md",
			checkpointFailureMarkerPath(dbPath), marker)
	}

	openStart := time.Now()
	log.Printf("opening db %s", dbPath)
	db, err := storage.Open(dbPath, storage.WithWALAutocheckpoint(env("COTEL_WAL_AUTOCHECKPOINT", storage.DefaultWALAutocheckpoint)))
	if err != nil {
		log.Fatalf("open storage: %v", err)
	}
	defer db.Close()
	log.Printf("db ready: schema/migrations applied in %s", time.Since(openStart).Round(time.Millisecond))

	// Fold (and thereby verify) indexes now that ALTER/CREATE INDEX have run.
	// A hard failure here exits before the gates open, so a migration that
	// corrupts an ART index fails the deploy instead of serving traffic.
	ckCtx, ckCancel := context.WithTimeout(context.Background(), shutdownCheckpointTimeout)
	if code := runCheckpoint(ckCtx, db, dbPath, checkpointPhaseStartup); code != 0 {
		ckCancel()
		srv.Close()
		db.Close() //nolint:errcheck // the deferred Close never runs past os.Exit
		os.Exit(code)
	}
	ckCancel()

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

	// Block until a stop signal, then checkpoint before exiting. A deploy sends
	// SIGTERM; without folding the WAL here the next start replays it, which is
	// the multi-minute cold-start cost on a large DB. A fatal serve error still
	// terminates the process via log.Fatalf inside serveGate.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Printf("received %s: stopping listeners and checkpointing before shutdown", sig)
	srv.Close() // quiesce ingest so no writes race the checkpoint

	ctx, cancel := context.WithTimeout(context.Background(), shutdownCheckpointTimeout)
	code := runCheckpoint(ctx, db, dbPath, checkpointPhaseShutdown)
	cancel()
	db.Close() //nolint:errcheck // the deferred Close never runs past os.Exit
	os.Exit(code)
}

// shutdownCheckpointTimeout bounds a CHECKPOINT so a stuck fold cannot hang
// the process. On shutdown it stays inside the container stop grace period
// (docker-compose stop_grace_period); on startup it keeps a wedged fold from
// blocking the deploy. A hit deadline is benign at both sites.
const shutdownCheckpointTimeout = 8 * time.Second

// exitCheckpointFailed is the exit code for a CHECKPOINT that failed outright,
// as opposed to one that merely ran out of time. Used at both startup (after
// schema apply) and shutdown.
const exitCheckpointFailed = 3

const (
	checkpointPhaseStartup  = "startup"
	checkpointPhaseShutdown = "shutdown"
)

// checkpointer is the CHECKPOINT surface of *storage.DB, narrowed so the
// fold can be exercised without a live database.
type checkpointer interface {
	Checkpoint(ctx context.Context) error
}

// runCheckpoint folds the WAL and returns the process exit code the caller
// should use if it is going to stop.
//
// A hit deadline is benign and returns 0: the WAL is whole and the next open
// replays it, which is the cost the checkpoint was trying to avoid, not a
// threat to the data. Any other failure is the opposite case. A CHECKPOINT that
// errors out does so because the database is damaged, and the WAL it leaves
// behind can abort the next open inside libduckdb - a C++ abort() that no Go
// code can catch - so the database never opens again until someone repairs it
// by hand. That cannot look like a clean exit: it gets a non-zero code plus a
// marker beside the database file, which outlives the container's logs.
//
// Startup and shutdown share this function so a failed fold is the same
// observable (exit 3, `<db>.checkpoint-failed`) wherever it happens. A timeout
// stays 0 at both sites: at shutdown the process exits cleanly with the WAL
// left for replay; at startup the process continues and serves traffic.
func runCheckpoint(ctx context.Context, db checkpointer, dbPath, phase string) int {
	start := time.Now()
	err := db.Checkpoint(ctx)
	elapsed := time.Since(start).Round(time.Millisecond)

	switch {
	case err == nil:
		if phase == checkpointPhaseShutdown {
			log.Printf("checkpoint complete in %s; exiting", elapsed)
		} else {
			log.Printf("startup checkpoint complete in %s", elapsed)
		}
		clearCheckpointFailureMarker(dbPath)
		return 0
	case errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil:
		if phase == checkpointPhaseShutdown {
			log.Printf("checkpoint on shutdown timed out after %s, WAL left for replay on next start: %v", elapsed, err)
		} else {
			log.Printf("startup checkpoint timed out after %s, continuing: %v", elapsed, err)
		}
		return 0
	default:
		if phase == checkpointPhaseShutdown {
			log.Printf("checkpoint on shutdown FAILED after %s, the WAL left behind may not be replayable: %v", elapsed, err)
		} else {
			log.Printf("startup checkpoint FAILED after %s: %v", elapsed, err)
		}
		writeCheckpointFailureMarker(dbPath, err, phase)
		return exitCheckpointFailed
	}
}

func checkpointFailureMarkerPath(dbPath string) string { return dbPath + ".checkpoint-failed" }

func writeCheckpointFailureMarker(dbPath string, cause error, phase string) {
	path := checkpointFailureMarkerPath(dbPath)
	line := fmt.Sprintf("%s checkpoint on %s failed: %v\n", time.Now().UTC().Format(time.RFC3339), phase, cause)
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		log.Printf("could not write checkpoint failure marker %s: %v", path, err)
		return
	}
	log.Printf("wrote checkpoint failure marker %s; recovery procedure: docs/operations/duckdb-recovery.md", path)
}

// clearCheckpointFailureMarker drops the marker after a clean fold, so it only
// ever describes the most recent failed checkpoint.
func clearCheckpointFailureMarker(dbPath string) {
	path := checkpointFailureMarkerPath(dbPath)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("could not remove stale checkpoint failure marker %s: %v", path, err)
	}
}

func readCheckpointFailureMarker(dbPath string) (string, bool) {
	body, err := os.ReadFile(checkpointFailureMarkerPath(dbPath))
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(body)), true
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

// Close shuts down both HTTP servers. main calls it on a stop signal to quiesce
// ingest before the shutdown checkpoint; tests use it to stop the background
// serve goroutines cleanly.
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

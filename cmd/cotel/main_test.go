package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Flopsstuff/cotel/internal/storage"
)

// fakeCheckpointer stands in for *storage.DB: the failure this guards against
// comes from a damaged database file, which cannot be produced in a unit test.
type fakeCheckpointer struct {
	err   error
	block bool // wait for ctx to expire, like a CHECKPOINT that outruns the deadline
}

func (f fakeCheckpointer) Checkpoint(ctx context.Context) error {
	if f.block {
		<-ctx.Done()
		return fmt.Errorf("checkpoint: %w", ctx.Err())
	}
	return f.err
}

// TestRunCheckpoint pins what each fold outcome shows the outside world, at
// both the shutdown site and the post-schema startup site. A CHECKPOINT that
// fails outright can leave a WAL that aborts the next open inside libduckdb, so
// it must not return 0; a CHECKPOINT that merely ran out of time leaves a
// replayable WAL and must stay the benign case it was.
func TestRunCheckpoint(t *testing.T) {
	for _, phase := range []string{checkpointPhaseShutdown, checkpointPhaseStartup} {
		t.Run(phase, func(t *testing.T) {
			t.Run("success exits 0 and leaves no marker", func(t *testing.T) {
				dbPath := filepath.Join(t.TempDir(), "cotel.duckdb")
				if code := runCheckpoint(context.Background(), fakeCheckpointer{}, dbPath, phase); code != 0 {
					t.Fatalf("clean checkpoint: want exit 0, got %d", code)
				}
				if _, err := os.Stat(checkpointFailureMarkerPath(dbPath)); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("clean checkpoint: marker should not exist, stat err = %v", err)
				}
			})

			t.Run("success clears a marker from an earlier failed fold", func(t *testing.T) {
				dbPath := filepath.Join(t.TempDir(), "cotel.duckdb")
				marker := checkpointFailureMarkerPath(dbPath)
				if err := os.WriteFile(marker, []byte("stale\n"), 0o644); err != nil {
					t.Fatalf("seed marker: %v", err)
				}
				if code := runCheckpoint(context.Background(), fakeCheckpointer{}, dbPath, phase); code != 0 {
					t.Fatalf("clean checkpoint: want exit 0, got %d", code)
				}
				if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("clean checkpoint: stale marker should be gone, stat err = %v", err)
				}
			})

			t.Run("hard failure exits non-zero and records a marker", func(t *testing.T) {
				dbPath := filepath.Join(t.TempDir(), "cotel.duckdb")
				cause := errors.New("checkpoint: Invalid node type for TransformToDeprecated: 0")
				code := runCheckpoint(context.Background(), fakeCheckpointer{err: cause}, dbPath, phase)
				if code != exitCheckpointFailed {
					t.Fatalf("failed checkpoint: want exit %d, got %d", exitCheckpointFailed, code)
				}
				body, ok := readCheckpointFailureMarker(dbPath)
				if !ok {
					t.Fatalf("failed checkpoint: no marker written at %s", checkpointFailureMarkerPath(dbPath))
				}
				if !strings.Contains(body, cause.Error()) {
					t.Errorf("marker should carry the cause, got %q", body)
				}
				if !strings.Contains(body, phase) {
					t.Errorf("marker should name the phase %q, got %q", phase, body)
				}
			})

			t.Run("timeout stays benign: exit 0, no marker", func(t *testing.T) {
				dbPath := filepath.Join(t.TempDir(), "cotel.duckdb")
				ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
				defer cancel()
				if code := runCheckpoint(ctx, fakeCheckpointer{block: true}, dbPath, phase); code != 0 {
					t.Fatalf("timed-out checkpoint: want exit 0, got %d", code)
				}
				if _, err := os.Stat(checkpointFailureMarkerPath(dbPath)); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("timed-out checkpoint: marker should not exist, stat err = %v", err)
				}
			})

			t.Run("unwritable marker path still exits non-zero", func(t *testing.T) {
				// Exit code is the signal that must survive; the marker is a bonus that
				// a read-only or full volume can legitimately deny.
				dbPath := filepath.Join(t.TempDir(), "missing-dir", "cotel.duckdb")
				code := runCheckpoint(context.Background(), fakeCheckpointer{err: errors.New("boom")}, dbPath, phase)
				if code != exitCheckpointFailed {
					t.Fatalf("want exit %d, got %d", exitCheckpointFailed, code)
				}
			})
		})
	}
}

// TestStartupCheckpointFailureDoesNotServeTraffic is the control flow main
// follows after schema apply: a hard fold failure closes the listeners without
// ever opening the gates, so the process does not serve live traffic.
func TestStartupCheckpointFailureDoesNotServeTraffic(t *testing.T) {
	gs, err := startGatedServers("127.0.0.1:0", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("startGatedServers: %v", err)
	}
	defer gs.Close()

	dbPath := filepath.Join(t.TempDir(), "cotel.duckdb")
	cause := errors.New("checkpoint: Invalid node type for GetAllocatorIdx: 0")
	code := runCheckpoint(context.Background(), fakeCheckpointer{err: cause}, dbPath, checkpointPhaseStartup)
	if code != exitCheckpointFailed {
		t.Fatalf("startup checkpoint failure: want exit %d, got %d", exitCheckpointFailed, code)
	}
	if _, ok := readCheckpointFailureMarker(dbPath); !ok {
		t.Fatalf("startup checkpoint failure: no marker at %s", checkpointFailureMarkerPath(dbPath))
	}

	// main() only calls set() after a 0; we never did, so both ports stay 503.
	client := &http.Client{Timeout: time.Second}
	for _, url := range []string{
		"http://" + gs.ingestAddr + "/v1/traces",
		"http://" + gs.dashAddr + "/api/v1/health",
	} {
		resp, err := client.Post(url, "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatalf("probe %s after failed startup checkpoint: %v", url, err)
		}
		if resp.StatusCode != http.StatusServiceUnavailable {
			resp.Body.Close()
			t.Fatalf("%s: want 503 (gates still closed), got %d", url, resp.StatusCode)
		}
		resp.Body.Close()
	}

	gs.Close()
}

// TestStartupCheckpointOnHealthyDB is the other half: after schema apply on a
// real file, the fold succeeds, leaves no marker, and a subsequent start (the
// schema-skip path) still succeeds. The non-empty case also records how long
// the extra start checkpoint costs, so a regression into multi-second startup
// is visible.
func TestStartupCheckpointOnHealthyDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cotel.duckdb")

	db, err := storage.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), shutdownCheckpointTimeout)
	code := runCheckpoint(ctx, db, path, checkpointPhaseStartup)
	cancel()
	if code != 0 {
		db.Close()
		t.Fatalf("fresh-db startup checkpoint: want exit 0, got %d", code)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	db, err = storage.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO spans (trace_id, span_id, name, start_time, end_time)
		SELECT 't' || i::VARCHAR, 's' || i::VARCHAR, 'probe',
		       TIMESTAMP '2026-01-01 00:00:00' + to_seconds(i),
		       TIMESTAMP '2026-01-01 00:00:00' + to_seconds(i + 1)
		FROM range(35000) t(i)
	`); err != nil {
		db.Close()
		t.Fatalf("bulk insert: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close after insert: %v", err)
	}

	db, err = storage.Open(path)
	if err != nil {
		t.Fatalf("open non-empty: %v", err)
	}

	start := time.Now()
	ctx, cancel = context.WithTimeout(context.Background(), shutdownCheckpointTimeout)
	code = runCheckpoint(ctx, db, path, checkpointPhaseStartup)
	elapsed := time.Since(start)
	cancel()
	if code != 0 {
		db.Close()
		t.Fatalf("non-empty startup checkpoint: want exit 0, got %d", code)
	}
	if _, err := os.Stat(checkpointFailureMarkerPath(path)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("healthy startup checkpoint: marker should not exist, stat err = %v", err)
	}
	t.Logf("startup checkpoint on 35000-span DB: %s", elapsed)
	if elapsed > time.Second {
		t.Errorf("startup checkpoint took %s on a healthy 35000-span DB; want well under 1s", elapsed)
	}

	var n int
	if err := db.ReadOnly().QueryRow("SELECT COUNT(*) FROM spans").Scan(&n); err != nil {
		db.Close()
		t.Fatalf("count: %v", err)
	}
	if n != 35000 {
		db.Close()
		t.Fatalf("after startup checkpoint: got %d spans, want 35000", n)
	}

	// Force the next Open to re-apply schema.sql (DROP/CREATE INDEX on the
	// populated table) — that is the path whose fold we actually need to be
	// cheap, because that is when indexes serialize.
	if _, err := db.Exec(`DELETE FROM settings WHERE key = 'schema_sql_sha256'`); err != nil {
		db.Close()
		t.Fatalf("drop schema hash: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close before re-apply: %v", err)
	}

	openStart := time.Now()
	db, err = storage.Open(path)
	if err != nil {
		t.Fatalf("open after schema hash drop: %v", err)
	}
	defer db.Close()
	schemaElapsed := time.Since(openStart)

	start = time.Now()
	ctx, cancel = context.WithTimeout(context.Background(), shutdownCheckpointTimeout)
	code = runCheckpoint(ctx, db, path, checkpointPhaseStartup)
	elapsed = time.Since(start)
	cancel()
	if code != 0 {
		t.Fatalf("post-schema-reapply checkpoint: want exit 0, got %d", code)
	}
	t.Logf("schema re-apply on 35000-span DB: %s; startup checkpoint after it: %s",
		schemaElapsed, elapsed)
	if elapsed > time.Second {
		t.Errorf("startup checkpoint after schema re-apply took %s; want well under 1s", elapsed)
	}
}

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

// TestGatedServersAcceptBeforeReady guards the regression against real TCP
// sockets: while the gate is still closed (simulating a slow storage.Open) the
// ingest and dashboard ports must ACCEPT the connection and answer 503 on every
// probe — never a dial error / reset, which is what silently dropped telemetry
// on each deploy. After the gate is opened requests are delegated.
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
				t.Fatalf("%s port refused a connection during the init window: %v", name, err)
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

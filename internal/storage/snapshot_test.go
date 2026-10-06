package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func insertOneSpan(t *testing.T, db *DB, spanID string, at time.Time) {
	t.Helper()
	inp := int64(10)
	out := int64(20)
	cost := 0.001
	if err := db.InsertSpan(Span{
		TraceID:      "trace-" + spanID,
		SpanID:       spanID,
		Name:         "claude_code.session",
		StartTime:    at,
		EndTime:      at.Add(time.Second),
		SessionID:    "session-1",
		Model:        "claude-sonnet-4-6",
		ToolName:     "Bash",
		InputTokens:  &inp,
		OutputTokens: &out,
		CostUSD:      &cost,
	}); err != nil {
		t.Fatalf("insert span %s: %v", spanID, err)
	}
}

func TestSnapshotWritesManifestLast(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	defer db.Close() //nolint:errcheck
	insertOneSpan(t, db, "span-001", time.Now())

	dir := t.TempDir()
	m, err := db.Snapshot(SnapshotConfig{Dir: dir, Keep: 4})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	for _, name := range []string{"schema.sql", "load.sql", "spans.parquet", "users.parquet", SnapshotManifestName} {
		if _, err := os.Stat(filepath.Join(m.Directory, name)); err != nil {
			t.Errorf("snapshot is missing %s: %v", name, err)
		}
	}
	if got := m.Tables["spans"]; got != 1 {
		t.Errorf("manifest spans count = %d, want 1", got)
	}
	ddl, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		t.Fatalf("read schema.sql: %v", err)
	}
	want, err := schemaVersion(string(ddl))
	if err != nil {
		t.Fatalf("schemaVersion: %v", err)
	}
	if m.SchemaVersion != want {
		t.Errorf("manifest schema_version = %d, want %d", m.SchemaVersion, want)
	}

	// The manifest on disk must be the manifest returned, and it must parse:
	// a restore reads it to decide whether the snapshot is usable at all.
	body, err := os.ReadFile(filepath.Join(m.Directory, SnapshotManifestName))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var onDisk SnapshotManifest
	if err := json.Unmarshal(body, &onDisk); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if onDisk.Instant != m.Instant || onDisk.Tables["spans"] != m.Tables["spans"] {
		t.Errorf("manifest on disk %+v does not match returned %+v", onDisk, m)
	}

	// No temporary manifest may survive the run, or a reader could mistake it
	// for the real thing.
	entries, err := os.ReadDir(m.Directory)
	if err != nil {
		t.Fatalf("read snapshot dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Errorf("snapshot left a temporary file behind: %s", e.Name())
		}
	}
}

// TestSnapshotLoadSQLCarriesAbsolutePaths pins the constraint the layout is
// built on: DuckDB bakes the export directory's absolute path into load.sql, so
// a snapshot cannot be exported to a temporary directory and renamed, and must
// be mounted at the same path to be restored.
func TestSnapshotLoadSQLCarriesAbsolutePaths(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	defer db.Close() //nolint:errcheck
	insertOneSpan(t, db, "span-001", time.Now())

	m, err := db.Snapshot(SnapshotConfig{Dir: t.TempDir(), Keep: 4})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	load, err := os.ReadFile(filepath.Join(m.Directory, "load.sql"))
	if err != nil {
		t.Fatalf("read load.sql: %v", err)
	}
	if !strings.Contains(string(load), m.Directory) {
		t.Errorf("load.sql does not reference %s by absolute path:\n%s", m.Directory, load)
	}
}

func TestImportSnapshotRestoresEveryTable(t *testing.T) {
	src := filepath.Join(t.TempDir(), "source.duckdb")
	db, err := Open(src)
	if err != nil {
		t.Fatalf("open source db: %v", err)
	}
	insertOneSpan(t, db, "span-001", time.Now())
	insertOneSpan(t, db, "span-002", time.Now())
	if err := db.SetSetting("snapshot-test-key", "kept"); err != nil {
		t.Fatalf("set setting: %v", err)
	}
	m, err := db.Snapshot(SnapshotConfig{Dir: t.TempDir(), Keep: 4})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close source db: %v", err)
	}

	restored := filepath.Join(t.TempDir(), "restored.duckdb")
	got, err := ImportSnapshot(restored, m.Directory)
	if err != nil {
		t.Fatalf("ImportSnapshot: %v", err)
	}
	if got.Instant != m.Instant {
		t.Errorf("imported manifest instant = %q, want %q", got.Instant, m.Instant)
	}

	// The restored file must be openable by the same code path production uses,
	// not just by the import.
	reopened, err := Open(restored)
	if err != nil {
		t.Fatalf("open restored db: %v", err)
	}
	defer reopened.Close() //nolint:errcheck

	var spans int64
	if err := reopened.rw.QueryRow("SELECT count(*) FROM spans").Scan(&spans); err != nil {
		t.Fatalf("count restored spans: %v", err)
	}
	if spans != 2 {
		t.Errorf("restored spans = %d, want 2", spans)
	}
	v, err := reopened.GetSetting("snapshot-test-key")
	if err != nil || v != "kept" {
		t.Errorf("restored setting = %q (err %v), want %q", v, err, "kept")
	}
	// The secondary indexes are rebuilt from the data by the import rather than
	// copied, which is the property that keeps a damaged index out of a restore.
	var indexes int64
	if err := reopened.rw.QueryRow("SELECT count(*) FROM duckdb_indexes()").Scan(&indexes); err != nil {
		t.Fatalf("count restored indexes: %v", err)
	}
	if indexes == 0 {
		t.Error("restored database has no secondary indexes")
	}
}

func TestImportSnapshotRefusesIncompleteSnapshot(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "spans.parquet"), []byte("not really parquet"), 0o644); err != nil {
		t.Fatalf("write stub file: %v", err)
	}
	if _, err := ImportSnapshot(filepath.Join(t.TempDir(), "restored.duckdb"), dir); err == nil {
		t.Fatal("ImportSnapshot accepted a snapshot with no manifest")
	}
}

func TestImportSnapshotRefusesPopulatedTarget(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	defer db.Close() //nolint:errcheck
	insertOneSpan(t, db, "span-001", time.Now())
	m, err := db.Snapshot(SnapshotConfig{Dir: t.TempDir(), Keep: 4})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	target := filepath.Join(t.TempDir(), "occupied.duckdb")
	occupied, err := Open(target)
	if err != nil {
		t.Fatalf("open target db: %v", err)
	}
	if err := occupied.Close(); err != nil {
		t.Fatalf("close target db: %v", err)
	}

	_, err = ImportSnapshot(target, m.Directory)
	if err == nil {
		t.Fatal("ImportSnapshot overwrote a database that already had tables")
	}
	if !strings.Contains(err.Error(), "empty database file") {
		t.Errorf("error does not say what to do about it: %v", err)
	}
}

func TestPruneSnapshotsKeepsNewestCompleteOnly(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	mk := func(at time.Time, complete bool) string {
		name := at.Format(snapshotDirLayout)
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if complete {
			if err := os.WriteFile(filepath.Join(path, SnapshotManifestName), []byte("{}"), 0o644); err != nil {
				t.Fatalf("write manifest: %v", err)
			}
		}
		return name
	}
	oldest := mk(base, true)
	middle := mk(base.Add(6*time.Hour), true)
	newest := mk(base.Add(12*time.Hour), true)
	halfWritten := mk(base.Add(18*time.Hour), false)
	// Anything whose name is not a snapshot instant belongs to someone else.
	foreign := "forensics-20261004"
	if err := os.MkdirAll(filepath.Join(dir, foreign), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", foreign, err)
	}

	if err := pruneSnapshots(dir, 2); err != nil {
		t.Fatalf("pruneSnapshots: %v", err)
	}

	for _, name := range []string{middle, newest, foreign} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s should have survived the prune: %v", name, err)
		}
	}
	for _, name := range []string{oldest, halfWritten} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s should have been pruned (err %v)", name, err)
		}
	}
}

func TestPruneSnapshotsNeverEmptiesTheDirectory(t *testing.T) {
	dir := t.TempDir()
	only := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC).Format(snapshotDirLayout)
	if err := os.MkdirAll(filepath.Join(dir, only), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, only, SnapshotManifestName), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := pruneSnapshots(dir, 0); err != nil {
		t.Fatalf("pruneSnapshots: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, only)); err != nil {
		t.Errorf("a Keep of 0 deleted the last snapshot standing: %v", err)
	}
}

func TestSnapshotWaitDefersUntilDue(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	mkComplete := func(at time.Time) {
		path := filepath.Join(dir, at.Format(snapshotDirLayout))
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(path, SnapshotManifestName), []byte("{}"), 0o644); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
	}

	if wait := snapshotWait(dir, 6*time.Hour, now); wait > 0 {
		t.Errorf("an empty directory must be due immediately, got %s", wait)
	}

	mkComplete(now.Add(-1 * time.Hour))
	if wait := snapshotWait(dir, 6*time.Hour, now); wait != 5*time.Hour {
		t.Errorf("wait after a 1h-old snapshot = %s, want 5h", wait)
	}

	mkComplete(now.Add(-7 * time.Hour))
	if wait := snapshotWait(dir, 6*time.Hour, now); wait != 5*time.Hour {
		t.Errorf("wait must follow the newest snapshot, got %s, want 5h", wait)
	}

	// A directory dated in the future must not defer snapshots by the skew.
	mkComplete(now.Add(72 * time.Hour))
	if wait := snapshotWait(dir, 6*time.Hour, now); wait != 6*time.Hour {
		t.Errorf("wait with a future-dated snapshot = %s, want it capped at 6h", wait)
	}
}

func TestRunSnapshotWorkerReturnsWhenDisabled(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	defer db.Close() //nolint:errcheck

	done := make(chan struct{})
	go func() {
		db.RunSnapshotWorker(SnapshotConfig{Keep: 4}, time.Hour)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunSnapshotWorker did not return with snapshots disabled")
	}
}

func TestRecordSnapshotRunSurfacesFailure(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	defer db.Close() //nolint:errcheck

	// A failed run must not advance the recorded directory: the last snapshot
	// that exists is still the last one that succeeded.
	db.recordSnapshotRun(SnapshotManifest{Directory: "/snapshots/good"}, nil)
	db.recordSnapshotRun(SnapshotManifest{}, os.ErrPermission)

	status, err := db.GetSetting(settingSnapshotStatus)
	if err != nil || status != "error" {
		t.Errorf("status = %q (err %v), want %q", status, err, "error")
	}
	if dir, err := db.GetSetting(settingSnapshotDir); err != nil || dir != "/snapshots/good" {
		t.Errorf("last dir = %q (err %v), want %q", dir, err, "/snapshots/good")
	}
	if msg, err := db.GetSetting(settingSnapshotError); err != nil || msg == "" {
		t.Errorf("last error = %q (err %v), want the failure text", msg, err)
	}
}

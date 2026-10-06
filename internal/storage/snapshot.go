package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Snapshot defaults. 6h × 56 kept snapshots is 14 days of reach: the September
// 2026 corruption went unnoticed for six days, so depth past the moment someone
// notices is worth more than a tighter recovery point. At the measured 4.2 MiB
// per snapshot that whole window costs ~235 MB.
const (
	DefaultSnapshotKeep     = 56
	DefaultSnapshotInterval = 6 * time.Hour
)

// SnapshotManifestName is written last in a snapshot directory, and nothing
// else writes it. Its presence is therefore the only "this snapshot is
// complete" signal: a directory without it is a failed or half-written run,
// prunable and never restorable.
const SnapshotManifestName = "snapshot.json"

// snapshotDirLayout names a snapshot directory after its UTC instant. Colons
// are out because the directory has to survive a copy to any filesystem, and
// the remaining format still sorts lexicographically in chronological order,
// which is what the pruner relies on.
const snapshotDirLayout = "2006-01-02T15-04-05Z"

// snapshotTimeout bounds one EXPORT DATABASE. The export runs on the single
// writer connection with ingest queued behind it (0.15-0.3 s on the 152 MB
// production database), so a wedged export must not be able to hold that
// connection indefinitely. A cancelled export leaves an incomplete directory,
// which the next successful run prunes.
const snapshotTimeout = 5 * time.Minute

// snapshotRetryCap bounds the wait after a failed run, so a transient failure
// does not cost a whole interval of backup coverage.
const snapshotRetryCap = 15 * time.Minute

// Setting keys used to surface snapshot-worker health on /api/v1/health.
const (
	settingSnapshotStatus = "snapshot_last_status" // "ok" | "error"
	settingSnapshotError  = "snapshot_last_error"  // last error text, "" on success
	settingSnapshotRunAt  = "snapshot_last_run_at" // RFC3339 timestamp of last attempt
	settingSnapshotDir    = "snapshot_last_dir"    // directory of the last complete snapshot
)

// SnapshotConfig controls where snapshots are written and how many are kept.
// An empty Dir disables snapshots entirely, so a dev checkout does not start
// writing them into its container filesystem.
type SnapshotConfig struct {
	Dir  string
	Keep int
}

// SnapshotManifest is the content of snapshot.json: what the snapshot claims to
// hold, so a restore can be checked against it rather than trusted.
type SnapshotManifest struct {
	Instant       string           `json:"instant"`
	DurationMS    int64            `json:"duration_ms"`
	Format        string           `json:"format"`
	SchemaVersion int              `json:"schema_version"`
	Tables        map[string]int64 `json:"tables"`
	Directory     string           `json:"directory"`
}

// RunSnapshotWorker exports the whole database to a dated directory under
// cfg.Dir, then every interval. Returns immediately (snapshots disabled) when
// cfg.Dir is empty.
//
// Each cycle only runs when the newest complete snapshot is older than
// interval. That is what keeps a restart - or a crash loop - from spending the
// retained window on snapshots minutes apart: the depth the worker buys is
// bounded by Keep, so churning it is the one way to lose coverage silently.
//
// A failed cycle is not silent: it is logged at ERROR level with the
// consecutive-failure count and recorded on /api/v1/health (status "degraded").
func (db *DB) RunSnapshotWorker(cfg SnapshotConfig, interval time.Duration) {
	if cfg.Dir == "" {
		log.Printf("snapshots disabled: no snapshot directory configured")
		return
	}
	// A non-positive interval would make every cycle due the moment the last
	// one finished, which is a busy loop on the connection ingest uses.
	if interval <= 0 {
		log.Printf("warning: ignoring snapshot interval %s, using %s", interval, DefaultSnapshotInterval)
		interval = DefaultSnapshotInterval
	}
	log.Printf("snapshot worker: directory %s, interval %s, keeping %d", cfg.Dir, interval, cfg.Keep)

	consecutiveFailures := 0
	for {
		wait := snapshotWait(cfg.Dir, interval, time.Now())
		if wait <= 0 {
			m, err := db.Snapshot(cfg)
			db.recordSnapshotRun(m, err)
			switch {
			case err != nil:
				consecutiveFailures++
				log.Printf("ERROR snapshot worker: export failed (consecutive failures=%d, dir=%s): %v",
					consecutiveFailures, cfg.Dir, err)
				wait = min(interval, snapshotRetryCap)
			default:
				consecutiveFailures = 0
				log.Printf("snapshot: wrote %s in %dms (%d tables, schema_version %d)",
					m.Directory, m.DurationMS, len(m.Tables), m.SchemaVersion)
				wait = interval
			}
		}
		time.Sleep(wait)
	}
}

// snapshotWait reports how long to wait before the next snapshot is due, given
// the newest complete snapshot already on disk. An unreadable or empty
// directory yields 0: let Snapshot produce the real error rather than guess here.
func snapshotWait(dir string, interval time.Duration, now time.Time) time.Duration {
	newest, ok := newestSnapshotInstant(dir)
	if !ok {
		return 0
	}
	// Capped at one interval so a future-dated directory - a volume carried over
	// from a host with a fast clock - delays the next snapshot by at most the
	// interval instead of by the skew.
	return min(interval-now.Sub(newest), interval)
}

func newestSnapshotInstant(dir string) (time.Time, bool) {
	var newest time.Time
	for _, s := range listSnapshots(dir) {
		if s.complete && s.instant.After(newest) {
			newest = s.instant
		}
	}
	return newest, !newest.IsZero()
}

// Snapshot writes one complete snapshot and prunes older ones.
func (db *DB) Snapshot(cfg SnapshotConfig) (SnapshotManifest, error) {
	return db.snapshotAt(cfg, time.Now())
}

func (db *DB) snapshotAt(cfg SnapshotConfig, now time.Time) (SnapshotManifest, error) {
	if cfg.Dir == "" {
		return SnapshotManifest{}, errors.New("snapshot: no directory configured")
	}
	instant := now.UTC().Truncate(time.Second)
	target := filepath.Join(cfg.Dir, instant.Format(snapshotDirLayout))
	if err := os.MkdirAll(target, 0o755); err != nil {
		return SnapshotManifest{}, fmt.Errorf("snapshot: create %s: %w", target, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), snapshotTimeout)
	defer cancel()

	// The export runs on db.rw, the same single connection ingest and the
	// dashboard use, which is why it cannot race the WAL checkpoint: the two are
	// serialised by construction rather than by a lock that has to be taken
	// correctly. It is also why it never writes to the live database file.
	start := time.Now()
	stmt := fmt.Sprintf("EXPORT DATABASE '%s' (FORMAT PARQUET, COMPRESSION ZSTD)", quoteSQLLiteral(target))
	if _, err := db.rw.ExecContext(ctx, stmt); err != nil {
		return SnapshotManifest{}, fmt.Errorf("snapshot: export to %s: %w", target, err)
	}
	elapsed := time.Since(start)

	tables, err := snapshotTableCounts(ctx, db.rw, target)
	if err != nil {
		return SnapshotManifest{}, fmt.Errorf("snapshot: count exported rows in %s: %w", target, err)
	}

	m := SnapshotManifest{
		Instant:       instant.Format(time.RFC3339),
		DurationMS:    elapsed.Milliseconds(),
		Format:        "parquet-zstd",
		SchemaVersion: snapshotSchemaVersion(ctx, db.rw, target),
		Tables:        tables,
		Directory:     target,
	}
	if err := writeSnapshotManifest(target, m); err != nil {
		return SnapshotManifest{}, err
	}

	if err := pruneSnapshots(cfg.Dir, cfg.Keep); err != nil {
		// The snapshot itself is complete and restorable; a failed prune only
		// costs disk, so it must not report the run as a failed backup.
		log.Printf("WARNING snapshot: pruning %s failed, snapshots may accumulate: %v", cfg.Dir, err)
	}
	return m, nil
}

// snapshotTableCounts reads the row count of every table the export wrote, from
// the Parquet files themselves rather than from the live tables: the manifest
// has to describe what the snapshot holds, not what the database held a moment
// after it was taken. count(*) over Parquet is answered from file metadata.
func snapshotTableCounts(ctx context.Context, q *sql.DB, dir string) (map[string]int64, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.parquet"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("export wrote no parquet files")
	}
	sort.Strings(files)
	counts := make(map[string]int64, len(files))
	for _, f := range files {
		var n int64
		stmt := fmt.Sprintf("SELECT count(*) FROM read_parquet('%s')", quoteSQLLiteral(f))
		if err := q.QueryRowContext(ctx, stmt).Scan(&n); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		counts[strings.TrimSuffix(filepath.Base(f), ".parquet")] = n
	}
	return counts, nil
}

// snapshotSchemaVersion reads the schema version out of the exported
// schema_version table. A snapshot of a database too old to have that table is
// still a valid snapshot, so an unreadable version is recorded as 0 rather than
// failing the run.
func snapshotSchemaVersion(ctx context.Context, q *sql.DB, dir string) int {
	var v sql.NullInt64
	stmt := fmt.Sprintf("SELECT max(version) FROM read_parquet('%s')",
		quoteSQLLiteral(filepath.Join(dir, "schema_version.parquet")))
	if err := q.QueryRowContext(ctx, stmt).Scan(&v); err != nil || !v.Valid {
		return 0
	}
	return int(v.Int64)
}

// writeSnapshotManifest writes snapshot.json via a temporary file in the same
// directory, so a crash mid-write cannot leave a truncated manifest that would
// make an incomplete snapshot look complete.
func writeSnapshotManifest(dir string, m SnapshotManifest) error {
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("snapshot: encode manifest: %w", err)
	}
	tmp := filepath.Join(dir, "."+SnapshotManifestName+".tmp")
	if err := os.WriteFile(tmp, append(body, '\n'), 0o644); err != nil {
		return fmt.Errorf("snapshot: write manifest: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, SnapshotManifestName)); err != nil {
		return fmt.Errorf("snapshot: publish manifest: %w", err)
	}
	return nil
}

// ReadSnapshotManifest loads the manifest of a snapshot directory. A missing
// manifest means the snapshot is incomplete and must not be restored from.
func ReadSnapshotManifest(dir string) (SnapshotManifest, error) {
	body, err := os.ReadFile(filepath.Join(dir, SnapshotManifestName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return SnapshotManifest{}, fmt.Errorf("%s has no %s: the snapshot is incomplete and cannot be restored", dir, SnapshotManifestName)
		}
		return SnapshotManifest{}, err
	}
	var m SnapshotManifest
	if err := json.Unmarshal(body, &m); err != nil {
		return SnapshotManifest{}, fmt.Errorf("%s/%s: %w", dir, SnapshotManifestName, err)
	}
	return m, nil
}

type snapshotDir struct {
	name     string
	instant  time.Time
	complete bool
}

// listSnapshots returns the snapshot directories under dir, oldest first. Only
// directories whose name parses as a snapshot instant are returned: anything
// else in the volume belongs to someone else and the pruner must not touch it.
func listSnapshots(dir string) []snapshotDir {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []snapshotDir
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		instant, err := time.Parse(snapshotDirLayout, e.Name())
		if err != nil {
			continue
		}
		_, statErr := os.Stat(filepath.Join(dir, e.Name(), SnapshotManifestName))
		out = append(out, snapshotDir{name: e.Name(), instant: instant, complete: statErr == nil})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].instant.Before(out[j].instant) })
	return out
}

// pruneSnapshots keeps the keep newest complete snapshots and deletes the rest,
// incomplete directories included. It is only ever called after a successful
// export, so the newest complete snapshot is always one that was just verified
// to exist - the last one standing can never be pruned away.
func pruneSnapshots(dir string, keep int) error {
	if keep < 1 {
		keep = 1
	}
	var complete, doomed []string
	for _, s := range listSnapshots(dir) {
		if s.complete {
			complete = append(complete, s.name)
			continue
		}
		doomed = append(doomed, s.name)
	}
	if excess := len(complete) - keep; excess > 0 {
		doomed = append(doomed, complete[:excess]...)
	}
	var errs []error
	for _, name := range doomed {
		if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
			errs = append(errs, err)
			continue
		}
		log.Printf("snapshot: pruned %s", filepath.Join(dir, name))
	}
	return errors.Join(errs...)
}

// recordSnapshotRun persists the outcome of one snapshot cycle so the health
// endpoint can report degradation. Best-effort: a failure to write status must
// not take down the worker, so write errors are only logged.
func (db *DB) recordSnapshotRun(m SnapshotManifest, runErr error) {
	status := "ok"
	msg := ""
	if runErr != nil {
		status = "error"
		msg = runErr.Error()
	}
	vals := map[string]string{
		settingSnapshotStatus: status,
		settingSnapshotError:  msg,
		settingSnapshotRunAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if runErr == nil {
		vals[settingSnapshotDir] = m.Directory
	}
	for k, v := range vals {
		if err := db.SetSetting(k, v); err != nil {
			log.Printf("ERROR snapshot worker: failed to record %s: %v", k, err)
		}
	}
}

// ImportSnapshot restores the snapshot in dir into the database file at path,
// and verifies the result against the snapshot's manifest.
//
// The target must be empty or absent: the exported schema.sql issues plain
// CREATE TABLE, so importing over populated tables fails halfway and leaves a
// mixed database. The import runs the snapshot's own load.sql, which carries
// the **absolute** paths the export baked in, so dir must be visible at the
// same path it was written to.
func ImportSnapshot(path, dir string) (SnapshotManifest, error) {
	m, err := ReadSnapshotManifest(dir)
	if err != nil {
		return SnapshotManifest{}, err
	}

	rw, err := sql.Open("duckdb", path+"?storage_compatibility_version="+StorageCompatibilityVersion)
	if err != nil {
		return SnapshotManifest{}, fmt.Errorf("open %s: %w", path, err)
	}
	defer rw.Close() //nolint:errcheck
	rw.SetMaxOpenConns(1)

	var existing int
	if err := rw.QueryRow("SELECT count(*) FROM duckdb_tables() WHERE schema_name = 'main'").Scan(&existing); err != nil {
		return SnapshotManifest{}, fmt.Errorf("inspect %s: %w", path, err)
	}
	if existing > 0 {
		return SnapshotManifest{}, fmt.Errorf("%s already holds %d tables: import needs an empty database file", path, existing)
	}

	if _, err := rw.Exec(fmt.Sprintf("IMPORT DATABASE '%s'", quoteSQLLiteral(dir))); err != nil {
		return SnapshotManifest{}, fmt.Errorf("import %s into %s: %w", dir, path, err)
	}

	if err := verifyImportedCounts(rw, m); err != nil {
		return SnapshotManifest{}, err
	}

	// Fold the import's WAL now, so the first start of the restored database
	// does not have to replay it.
	if _, err := rw.Exec("CHECKPOINT"); err != nil {
		return SnapshotManifest{}, fmt.Errorf("checkpoint %s after import: %w", path, err)
	}
	return m, nil
}

// verifyImportedCounts compares the restored tables against what the manifest
// said the snapshot held. A restore that silently loaded fewer rows than it was
// given is the failure mode a backup exists to rule out, so it is an error.
func verifyImportedCounts(q *sql.DB, m SnapshotManifest) error {
	var errs []error
	for table, want := range m.Tables {
		var got int64
		// Table names come from the snapshot's own file listing, not from user
		// input, and the quoting keeps an odd one from breaking the statement.
		if err := q.QueryRow(fmt.Sprintf(`SELECT count(*) FROM "%s"`, strings.ReplaceAll(table, `"`, `""`))).Scan(&got); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", table, err))
			continue
		}
		if got != want {
			errs = append(errs, fmt.Errorf("%s: restored %d rows, snapshot holds %d", table, got, want))
		}
	}
	return errors.Join(errs...)
}

// quoteSQLLiteral escapes a value for a single-quoted DuckDB string literal.
// EXPORT/IMPORT DATABASE and read_parquet take a literal path, not a bind
// parameter, so the path has to be interpolated.
func quoteSQLLiteral(s string) string { return strings.ReplaceAll(s, "'", "''") }

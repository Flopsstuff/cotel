package storage

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

func appliedVersion(t *testing.T, db *DB) int {
	t.Helper()
	v, ok := appliedSchemaVersion(db.rw)
	if !ok {
		t.Fatalf("schema_version not recorded")
	}
	return v
}

func embeddedVersion(t *testing.T) int {
	t.Helper()
	ddl, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		t.Fatalf("read schema.sql: %v", err)
	}
	v, err := schemaVersion(string(ddl))
	if err != nil {
		t.Fatalf("schemaVersion: %v", err)
	}
	return v
}

func embeddedSHA(t *testing.T) string {
	t.Helper()
	ddl, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		t.Fatalf("read schema.sql: %v", err)
	}
	sum := sha256.Sum256(ddl)
	return hex.EncodeToString(sum[:])
}

// Fresh database: no version marker → full schema applied, version recorded.
func TestEnsureSchema_FreshDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.duckdb")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if got, want := appliedVersion(t, db), embeddedVersion(t); got != want {
		t.Fatalf("recorded version = %d, want %d", got, want)
	}
	// A schema-created table must exist and the seed row must be present.
	if v, err := db.GetSetting("allow_anonymous"); err != nil || v != "true" {
		t.Fatalf("allow_anonymous seed = %q, err=%v; want \"true\"", v, err)
	}
}

// Pre-marker database: tables exist but schema_version does not (created before
// the marker) → full schema runs once, idempotently, and records the version.
func TestEnsureSchema_PreMarkerDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "premarker.duckdb")

	// Simulate an old database: a spans table, no schema_version table/rows.
	raw, err := sql.Open("duckdb", path)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	if _, err := raw.Exec(`CREATE TABLE spans (
		span_id VARCHAR PRIMARY KEY, session_id VARCHAR,
		start_time TIMESTAMPTZ, name VARCHAR, user_id VARCHAR
	)`); err != nil {
		t.Fatalf("seed old spans: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO spans (span_id, user_id) VALUES ('a', 'alice')`); err != nil {
		t.Fatalf("seed span row: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("raw close: %v", err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatalf("open pre-marker db: %v", err)
	}
	defer db.Close()

	if got, want := appliedVersion(t, db), embeddedVersion(t); got != want {
		t.Fatalf("recorded version = %d, want %d", got, want)
	}
	// The user backfill migration must have run against the pre-existing spans.
	var n int
	if err := db.rw.QueryRow(`SELECT count(*) FROM users WHERE name = 'alice'`).Scan(&n); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if n != 1 {
		t.Fatalf("backfilled users for 'alice' = %d, want 1", n)
	}
}

// Up-to-date database: version already matches → schema is skipped entirely.
// Proven by deleting the seed row schema.sql would re-insert and confirming a
// reopen leaves it deleted.
func TestEnsureSchema_CurrentVersionSkipsDDL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "current.duckdb")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if _, err := db.rw.Exec(`DELETE FROM settings WHERE key = 'allow_anonymous'`); err != nil {
		t.Fatalf("delete seed: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	db2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close()

	if _, err := db2.GetSetting("allow_anonymous"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("seed row was re-inserted (DDL re-ran); guard did not skip, err=%v", err)
	}
	if got, want := appliedVersion(t, db2), embeddedVersion(t); got != want {
		t.Fatalf("recorded version = %d, want %d", got, want)
	}
}

// An older recorded version must still trigger a full (idempotent) apply so a
// new migration lands exactly once.
func TestEnsureSchema_StaleVersionReapplies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stale.duckdb")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	// Roll the recorded version back and drop the seed the schema would restore.
	if _, err := db.rw.Exec(`DELETE FROM schema_version WHERE version >= 2`); err != nil {
		t.Fatalf("roll back version: %v", err)
	}
	if _, err := db.rw.Exec(`DELETE FROM settings WHERE key = 'allow_anonymous'`); err != nil {
		t.Fatalf("delete seed: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	db2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close()

	if v, err := db2.GetSetting("allow_anonymous"); err != nil || v != "true" {
		t.Fatalf("stale db not re-migrated: allow_anonymous = %q, err=%v", v, err)
	}
	if got, want := appliedVersion(t, db2), embeddedVersion(t); got != want {
		t.Fatalf("recorded version = %d, want %d", got, want)
	}
}

// Current version but a stale recorded file hash → DDL must re-apply. This is
// the safety net for any schema.sql edit the version scheme fails to notice.
func TestEnsureSchema_StaleSHAReapplies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stalesha.duckdb")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	// Version stays current; only the recorded hash is wrong, and we drop a seed
	// the full schema would restore.
	if err := db.SetSetting(schemaSHAKey, "stale"); err != nil {
		t.Fatalf("corrupt sha: %v", err)
	}
	if _, err := db.rw.Exec(`DELETE FROM settings WHERE key = 'allow_anonymous'`); err != nil {
		t.Fatalf("delete seed: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	db2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close()

	if v, err := db2.GetSetting("allow_anonymous"); err != nil || v != "true" {
		t.Fatalf("stale-hash db not re-applied: allow_anonymous = %q, err=%v", v, err)
	}
	if got := appliedSchemaSHA(db2.rw); got != embeddedSHA(t) {
		t.Fatalf("recorded sha = %q, want embedded sha after re-apply", got)
	}
	if got, want := appliedVersion(t, db2), embeddedVersion(t); got != want {
		t.Fatalf("recorded version = %d, want %d", got, want)
	}
}

func TestSchemaVersion_Parsing(t *testing.T) {
	ddl := `-- Schema version: 3
INSERT INTO schema_version (version) VALUES (1) ON CONFLICT DO NOTHING;
INSERT INTO schema_version (version) VALUES (3) ON CONFLICT DO NOTHING;
INSERT INTO schema_version (version) VALUES (2) ON CONFLICT DO NOTHING;`
	v, err := schemaVersion(ddl)
	if err != nil {
		t.Fatalf("schemaVersion: %v", err)
	}
	if v != 3 {
		t.Fatalf("schemaVersion = %d, want 3", v)
	}

	if _, err := schemaVersion("-- no inserts here"); err == nil {
		t.Fatalf("expected error for schema without version rows")
	}
}

// The `-- Schema version: N` header is documentation; keep it honest by
// asserting it matches the highest version row the file actually inserts.
func TestSchemaHeaderMatchesInserts(t *testing.T) {
	ddl, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		t.Fatalf("read schema.sql: %v", err)
	}
	m := regexp.MustCompile(`(?m)^--\s*Schema version:\s*(\d+)`).FindStringSubmatch(string(ddl))
	if m == nil {
		t.Fatalf("schema.sql: missing `-- Schema version: N` header")
	}
	header, _ := strconv.Atoi(m[1])
	inserts, err := schemaVersion(string(ddl))
	if err != nil {
		t.Fatalf("schemaVersion: %v", err)
	}
	if header != inserts {
		t.Fatalf("header version %d != highest inserted version %d", header, inserts)
	}
}

package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"time"

	_ "github.com/marcboeker/go-duckdb"
)

// DefaultWALAutocheckpoint caps how large the write-ahead log grows before
// DuckDB folds it into the main file. DuckDB's own default is 16MB. Opening a
// file with an un-checkpointed WAL replays it, and on a large database that
// replay - not the schema migration - is what makes a cold start take minutes.
// A graceful stop checkpoints and avoids replay entirely; this cap only matters
// for an ungraceful kill (OOM, SIGKILL after the grace period), where it bounds
// how much durable WAL the next open has to replay. Chosen below the WAL a
// retention roll-up leaves (~6MB) so that purge is folded promptly. The cost is
// slightly more frequent checkpoints during ingest, which is cheap at cotel's
// write rate (~6MB of WAL/day).
const DefaultWALAutocheckpoint = "4MB"

// walSizeRe matches a DuckDB byte-size literal like "4MB" or "512KB".
var walSizeRe = regexp.MustCompile(`(?i)^[0-9]+\s*(b|kb|mb|gb|tb)?$`)

// Option configures Open.
type Option func(*openConfig)

type openConfig struct {
	walAutocheckpoint string
}

// WithWALAutocheckpoint overrides DuckDB's checkpoint_threshold. Empty leaves
// the DuckDB default (16MB). See DefaultWALAutocheckpoint for the trade-off.
func WithWALAutocheckpoint(size string) Option {
	return func(c *openConfig) { c.walAutocheckpoint = size }
}

func nullableStr(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

//go:embed schema.sql
var schemaFS embed.FS

type DB struct {
	rw   *sql.DB // single writer connection
	path string
}

// ReadDB is a read-only view over DuckDB used by the dashboard.
type ReadDB struct {
	db *sql.DB
}

func (r *ReadDB) QueryRow(query string, args ...any) *sql.Row {
	return r.db.QueryRow(query, args...)
}

func (r *ReadDB) Query(query string, args ...any) (*sql.Rows, error) {
	return r.db.Query(query, args...)
}

// ReadOnly returns a dashboard-safe view sharing the writer's pool.
// A separate read-only connection to the same file misses WAL-buffered writes;
// sharing rw ensures dashboard queries always see committed data.
func (d *DB) ReadOnly() *ReadDB {
	return &ReadDB{db: d.rw}
}

// OpenReadOnly opens a read-only connection to a DuckDB file. Safe to call
// concurrently with an open read-write connection from another process.
func OpenReadOnly(path string) (*ReadDB, error) {
	ro, err := sql.Open("duckdb", path+"?access_mode=read_only")
	if err != nil {
		return nil, fmt.Errorf("open duckdb read-only %q: %w", path, err)
	}
	ro.SetMaxOpenConns(4)
	return &ReadDB{db: ro}, nil
}

func (r *ReadDB) Close() error { return r.db.Close() }

func Open(path string, opts ...Option) (*DB, error) {
	var cfg openConfig
	for _, o := range opts {
		o(&cfg)
	}
	dsn := path
	if path == ":memory:" {
		dsn = "" // go-duckdb uses "" for in-memory, not ":memory:"
	}
	rw, err := sql.Open("duckdb", dsn)
	if err != nil {
		return nil, fmt.Errorf("open duckdb %q: %w", path, err)
	}
	// DuckDB supports one writer; serialise all writes through this connection.
	rw.SetMaxOpenConns(1)

	if cfg.walAutocheckpoint != "" {
		// PRAGMA takes a literal, not a bind parameter, so validate before
		// interpolating to keep the statement injection-safe. A rejected value
		// must not take down startup - warn and fall back to the DuckDB default.
		if !walSizeRe.MatchString(cfg.walAutocheckpoint) {
			log.Printf("warning: ignoring invalid wal_autocheckpoint=%q (want e.g. 4MB), using DuckDB default", cfg.walAutocheckpoint)
		} else if _, err := rw.Exec(fmt.Sprintf("PRAGMA wal_autocheckpoint='%s'", cfg.walAutocheckpoint)); err != nil {
			log.Printf("warning: could not set wal_autocheckpoint=%q, using DuckDB default: %v", cfg.walAutocheckpoint, err)
		}
	}

	if err := ensureSchema(rw); err != nil {
		return nil, err
	}
	return &DB{rw: rw, path: path}, nil
}

var schemaVersionRe = regexp.MustCompile(`(?i)INSERT\s+INTO\s+schema_version\s*\(\s*version\s*\)\s*VALUES\s*\(\s*(\d+)\s*\)`)

// schemaVersion is the highest version schema.sql declares, read from the
// file's own `INSERT INTO schema_version` rows. A new migration must add its
// version row.
func schemaVersion(ddl string) (int, error) {
	max := 0
	for _, m := range schemaVersionRe.FindAllStringSubmatch(ddl, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return 0, fmt.Errorf("schema.sql: bad version %q: %w", m[1], err)
		}
		if n > max {
			max = n
		}
	}
	if max == 0 {
		return 0, fmt.Errorf("schema.sql: no `INSERT INTO schema_version` rows found")
	}
	return max, nil
}

// appliedSchemaVersion reports the highest version recorded in the database.
// ok is false when schema_version is absent (a fresh database, or one created
// before the version marker existed) — in both cases the full schema must run.
func appliedSchemaVersion(rw *sql.DB) (version int, ok bool) {
	var v sql.NullInt64
	if err := rw.QueryRow(`SELECT max(version) FROM schema_version`).Scan(&v); err != nil {
		return 0, false // table missing → treat as unversioned
	}
	if !v.Valid {
		return 0, false // table exists but empty
	}
	return int(v.Int64), true
}

const schemaSHAKey = "schema_sql_sha256"

// appliedSchemaSHA returns the sha256 of the schema.sql last applied, or "" when
// none is recorded (settings table absent, or an older DB written before the
// hash was tracked). "" never equals a real hash, so it forces a re-apply.
func appliedSchemaSHA(rw *sql.DB) string {
	var v string
	if err := rw.QueryRow(`SELECT value FROM settings WHERE key = ?`, schemaSHAKey).Scan(&v); err != nil {
		return ""
	}
	return v
}

func recordSchemaSHA(rw *sql.DB, sha string) error {
	_, err := rw.Exec(`
		INSERT INTO settings (key, value, updated_at) VALUES (?, ?, now())
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = now()
	`, schemaSHAKey, sha)
	return err
}

// ensureSchema applies schema.sql unless the database already records both the
// target version and the exact file hash. The hash is the safety net: any edit
// the version scheme misses (forgotten or malformed version row) changes it and
// forces a full, idempotent re-apply, so a stale schema is never silently
// skipped — the guard fails toward doing too much, never too little.
func ensureSchema(rw *sql.DB) error {
	ddl, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		return err
	}
	target, err := schemaVersion(string(ddl))
	if err != nil {
		return err
	}
	sum := sha256.Sum256(ddl)
	sha := hex.EncodeToString(sum[:])
	if current, ok := appliedSchemaVersion(rw); ok && current >= target && appliedSchemaSHA(rw) == sha {
		return nil
	}
	if _, err := rw.Exec(string(ddl)); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	if err := recordSchemaSHA(rw, sha); err != nil {
		return fmt.Errorf("record schema hash: %w", err)
	}
	return nil
}

func (db *DB) Close() error { return db.rw.Close() }

// Checkpoint folds the write-ahead log into the main database file so the next
// open does not have to replay it. The write connection has already applied the
// log in memory, so this is cheap in steady state; it is the shutdown counterpart
// to the replay a cold open would otherwise pay. Honours ctx so a hung checkpoint
// cannot stall shutdown past the container stop grace period.
func (db *DB) Checkpoint(ctx context.Context) error {
	if _, err := db.rw.ExecContext(ctx, "CHECKPOINT"); err != nil {
		return fmt.Errorf("checkpoint: %w", err)
	}
	return nil
}

// Span represents a decoded OTLP span ready for storage.
type Span struct {
	TraceID          string
	SpanID           string
	ParentSpanID     string
	Name             string
	StartTime        time.Time
	EndTime          time.Time
	ServiceName      string
	SessionID        string
	Model            string
	ToolName         string
	// UserID identifies the sender; set via OTEL resource attribute user.id (empty = unset → stored as NULL).
	UserID           string
	// StatusCode is the OTLP span status: 0=UNSET, 1=OK, 2=ERROR.
	StatusCode       int32
	InputTokens      *int64
	OutputTokens     *int64
	CacheReadTokens  *int64
	CacheWriteTokens *int64
	CostUSD          *float64
	Attributes       string // JSON
	ResourceAttrs    string // JSON
	// IngestedAt is populated on export; zero on ingest (DB uses DEFAULT now()).
	IngestedAt       time.Time
}

const insertSpan = `
INSERT OR IGNORE INTO spans (
  trace_id, span_id, parent_span_id, name,
  start_time, end_time, service_name,
  session_id, model, tool_name, user_id, status_code,
  input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd,
  attributes, resource_attrs
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

// Exec runs an arbitrary SQL statement on the write connection. Used by tests
// for bulk data setup (e.g. INSERT … SELECT FROM range).
func (db *DB) Exec(query string, args ...any) (sql.Result, error) {
	return db.rw.Exec(query, args...)
}

func (db *DB) InsertSpan(s Span) error {
	if s.Attributes == "" {
		s.Attributes = "{}"
	}
	if s.ResourceAttrs == "" {
		s.ResourceAttrs = "{}"
	}
	_, err := db.rw.Exec(insertSpan,
		s.TraceID, s.SpanID, s.ParentSpanID, s.Name,
		s.StartTime, s.EndTime, s.ServiceName,
		s.SessionID, s.Model, s.ToolName, nullableStr(s.UserID), s.StatusCode,
		s.InputTokens, s.OutputTokens, s.CacheReadTokens, s.CacheWriteTokens, s.CostUSD,
		s.Attributes, s.ResourceAttrs,
	)
	return err
}

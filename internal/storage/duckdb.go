package storage

import (
	"database/sql"
	"embed"
	"fmt"
	"regexp"
	"strconv"
	"time"

	_ "github.com/marcboeker/go-duckdb"
)

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

func Open(path string) (*DB, error) {
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

	if err := ensureSchema(rw); err != nil {
		return nil, err
	}
	return &DB{rw: rw, path: path}, nil
}

var schemaVersionRe = regexp.MustCompile(`(?i)INSERT\s+INTO\s+schema_version\s*\(\s*version\s*\)\s*VALUES\s*\(\s*(\d+)\s*\)`)

// schemaVersion is the highest version the embedded schema.sql declares. It is
// derived from the file's own `INSERT INTO schema_version` rows rather than a
// hand-kept constant, so it can never drift from the migrations actually
// applied. Adding a migration means adding its version row; that is the single
// bump the guard keys off.
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

// ensureSchema applies schema.sql only when the database is behind the embedded
// schema. Every statement in schema.sql is idempotent, but running the lot on
// each start is expensive on a large database — the user backfill alone scans
// every span (SELECT DISTINCT user_id FROM spans). Guarding on the recorded
// version turns an unchanged restart into a single cheap query and runs each
// migration exactly once, on the deploy that introduces it.
func ensureSchema(rw *sql.DB) error {
	ddl, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		return err
	}
	target, err := schemaVersion(string(ddl))
	if err != nil {
		return err
	}
	if current, ok := appliedSchemaVersion(rw); ok && current >= target {
		return nil
	}
	if _, err := rw.Exec(string(ddl)); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	return nil
}

func (db *DB) Close() error { return db.rw.Close() }

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

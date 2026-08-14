package storage

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// pushdownBrokenSpanCols are the VARCHAR columns of spans for which the bundled
// DuckDB engine answers a bare `col = <constant>` with zero rows. Empty since
// spans stopped carrying a generated column (ADR-0013).
//
// The trigger is the table shape, not the column: a VIRTUAL generated column
// takes a logical slot but no storage slot, so every column after it has logical
// index = physical index + 1. A column is answered wrongly exactly when its
// logical index collides with the physical index of an indexed column - the scan
// then probes that unrelated ART index for the constant and finds nothing.
//
// Adding, reordering or indexing a column moves the collisions, which is why
// the test below derives the set from the live schema rather than trusting this
// list to stay complete.
var pushdownBrokenSpanCols = map[string]bool{}

// spansVarcharCols reads the VARCHAR columns of spans in declaration order.
func spansVarcharCols(t *testing.T, r *ReadDB) []string {
	t.Helper()
	rows, err := r.Query(`
		SELECT column_name FROM information_schema.columns
		WHERE table_name = 'spans' AND data_type = 'VARCHAR'
		ORDER BY ordinal_position`)
	if err != nil {
		t.Fatalf("read spans columns: %v", err)
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatalf("scan column name: %v", err)
		}
		cols = append(cols, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate spans columns: %v", err)
	}
	if len(cols) == 0 {
		t.Fatal("spans has no VARCHAR columns")
	}
	return cols
}

// pushdownFixture inserts spans that give every VARCHAR column a non-empty
// value, some shared across rows and some unique per row.
func pushdownFixture(t *testing.T, db *DB) {
	t.Helper()
	for i := 0; i < 3; i++ {
		err := db.InsertSpan(Span{
			TraceID:      fmt.Sprintf("trace-%d", i),
			SpanID:       fmt.Sprintf("span-%d", i),
			ParentSpanID: "parent-shared",
			Name:         "tool.execution",
			StartTime:    time.Unix(int64(i), 0),
			EndTime:      time.Unix(int64(i)+1, 0),
			ServiceName:  "claude-code",
			SessionID:    fmt.Sprintf("session-%d", i),
			Model:        "claude-opus-4",
			ToolName:     "Bash",
			UserID:       fmt.Sprintf("user-%d", i),
		})
		if err != nil {
			t.Fatalf("insert span %d: %v", i, err)
		}
	}
}

// truthCounts scans every row once, with no WHERE clause for the optimizer to
// rewrite, and returns per column a probe value and how many rows carry it.
func truthCounts(t *testing.T, r *ReadDB, cols []string) (map[string]string, map[string]int) {
	t.Helper()
	sel := ""
	for i, c := range cols {
		if i > 0 {
			sel += ", "
		}
		sel += `"` + c + `"`
	}
	rows, err := r.Query("SELECT " + sel + " FROM spans")
	if err != nil {
		t.Fatalf("scan spans: %v", err)
	}
	defer rows.Close()

	probe := map[string]string{}
	want := map[string]int{}
	for rows.Next() {
		vals := make([]*string, len(cols))
		dest := make([]any, len(cols))
		for i := range vals {
			dest[i] = &vals[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatalf("scan row: %v", err)
		}
		for i, c := range cols {
			if vals[i] == nil {
				continue
			}
			if _, ok := probe[c]; !ok {
				probe[c] = *vals[i]
			}
			if probe[c] == *vals[i] {
				want[c]++
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate spans: %v", err)
	}
	for _, c := range cols {
		if _, ok := probe[c]; !ok {
			t.Fatalf("column %q is NULL in every fixture row - extend pushdownFixture so the guard covers it", c)
		}
	}
	return probe, want
}

// TestSpansEqualityUnderFilterPushdown guards a wrong-results trap in the
// bundled DuckDB engine: on the spans table a bare `col = <constant>` can be
// pushed into the scan and then match nothing, turning a silently empty answer
// into something no reviewer can spot by reading the SQL.
//
// It fails in both directions on purpose. A column that starts disagreeing means
// the layout grew a hole again - look for a generated column before reaching for
// a COALESCE workaround; a column in pushdownBrokenSpanCols that starts agreeing
// means the trap is gone and its entry can go.
func TestSpansEqualityUnderFilterPushdown(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	pushdownFixture(t, db)

	ro := db.ReadOnly()
	cols := spansVarcharCols(t, ro)
	probe, want := truthCounts(t, ro, cols)

	count := func(where, arg string) int {
		var n int
		if err := ro.QueryRow("SELECT COUNT(*) FROM spans WHERE "+where, arg).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", where, err)
		}
		return n
	}

	for _, c := range cols {
		q := `"` + c + `"`
		bare := count(q+" = ?", probe[c])

		if got := count("COALESCE("+q+", '') = ?", probe[c]); got != want[c] {
			t.Errorf("COALESCE(%s, '') = %q returned %d rows, want %d: the fallback workaround no longer returns the truth on %s",
				c, probe[c], got, want[c], duckDBVersion(t, ro))
		}

		switch broken := pushdownBrokenSpanCols[c]; {
		case broken && bare == want[c]:
			t.Errorf("%s = %q now returns the correct %d rows on %s: drop %q from pushdownBrokenSpanCols and remove its COALESCE workaround",
				c, probe[c], want[c], duckDBVersion(t, ro), c)
		case !broken && bare != want[c]:
			t.Errorf("%s = %q returned %d rows, want %d on %s: this column is now silently dropped by filter pushdown - check whether spans grew a generated column",
				c, probe[c], bare, want[c], duckDBVersion(t, ro))
		}
	}

	// Disabling the optimizer must make every column agree; if it does not, the
	// wrong answers are coming from somewhere other than filter pushdown.
	if _, err := db.Exec("SET disabled_optimizers='filter_pushdown'"); err != nil {
		t.Fatalf("disable filter_pushdown: %v", err)
	}
	defer db.Exec("SET disabled_optimizers=''")

	for _, c := range cols {
		if got := count(`"`+c+`" = ?`, probe[c]); got != want[c] {
			t.Errorf("with filter_pushdown disabled, %s = %q returned %d rows, want %d: the cause is not filter pushdown",
				c, probe[c], got, want[c])
		}
	}
}

// TestUpgradeDropsGeneratedDurationColumn covers the upgrade path the fresh-database
// test cannot: a populated v9 database still carries duration_ms as a generated
// column, and the migration has to remove it without touching the rows around it.
func TestUpgradeDropsGeneratedDurationColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v9.duckdb")

	raw, err := sql.Open("duckdb", path)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	if _, err := raw.Exec(`CREATE TABLE spans (
		trace_id VARCHAR NOT NULL, span_id VARCHAR NOT NULL PRIMARY KEY,
		parent_span_id VARCHAR, name VARCHAR NOT NULL,
		start_time TIMESTAMPTZ NOT NULL, end_time TIMESTAMPTZ NOT NULL,
		duration_ms DOUBLE GENERATED ALWAYS AS (epoch_ms(end_time) - epoch_ms(start_time)),
		service_name VARCHAR, session_id VARCHAR, model VARCHAR, tool_name VARCHAR,
		user_id VARCHAR, status_code TINYINT DEFAULT 0,
		input_tokens INTEGER, output_tokens INTEGER,
		cache_read_tokens INTEGER, cache_write_tokens INTEGER, cost_usd DOUBLE,
		attributes JSON, resource_attrs JSON, ingested_at TIMESTAMPTZ DEFAULT now()
	)`); err != nil {
		t.Fatalf("seed v9 spans: %v", err)
	}
	for _, idx := range []string{"session_id", "start_time", "name", "user_id"} {
		if _, err := raw.Exec(`CREATE INDEX idx_spans_` + idx + ` ON spans(` + idx + `)`); err != nil {
			t.Fatalf("seed index on %s: %v", idx, err)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := raw.Exec(`
			INSERT INTO spans (trace_id, span_id, name, start_time, end_time, service_name, session_id, model, tool_name, user_id)
			VALUES ('t', ?, 'tool.execution', ?, ?, 'claude-code', ?, 'claude-opus-4', 'Bash', ?)`,
			fmt.Sprintf("span-%d", i), time.Unix(int64(i), 0), time.Unix(int64(i)+1, 0),
			fmt.Sprintf("session-%d", i), fmt.Sprintf("user-%d", i)); err != nil {
			t.Fatalf("seed span %d: %v", i, err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("raw close: %v", err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatalf("open v9 db: %v", err)
	}
	defer db.Close()
	ro := db.ReadOnly()

	var cols int
	if err := ro.QueryRow(`
		SELECT COUNT(*) FROM information_schema.columns
		WHERE table_name = 'spans' AND column_name = 'duration_ms'`).Scan(&cols); err != nil {
		t.Fatalf("read spans columns: %v", err)
	}
	if cols != 0 {
		t.Errorf("duration_ms still declared on spans after upgrade")
	}

	var total int
	if err := ro.QueryRow(`SELECT COUNT(*) FROM spans`).Scan(&total); err != nil {
		t.Fatalf("count spans: %v", err)
	}
	if total != 3 {
		t.Errorf("spans after upgrade = %d, want 3: the migration moved rows", total)
	}

	var bash int
	if err := ro.QueryRow(`SELECT COUNT(*) FROM spans WHERE tool_name = 'Bash'`).Scan(&bash); err != nil {
		t.Fatalf("count Bash spans: %v", err)
	}
	if bash != 3 {
		t.Errorf("tool_name = 'Bash' returned %d rows, want 3 on %s: the upgraded layout still traps bare equality",
			bash, duckDBVersion(t, ro))
	}

	var dur float64
	if err := ro.QueryRow(`
		SELECT CAST(epoch_ms(end_time) - epoch_ms(start_time) AS DOUBLE)
		FROM spans WHERE span_id = 'span-0'`).Scan(&dur); err != nil {
		t.Fatalf("read computed duration: %v", err)
	}
	if dur != 1000 {
		t.Errorf("computed duration_ms = %v, want 1000", dur)
	}
}

func duckDBVersion(t *testing.T, r *ReadDB) string {
	t.Helper()
	var v string
	if err := r.QueryRow("SELECT version()").Scan(&v); err != nil {
		t.Fatalf("read duckdb version: %v", err)
	}
	return v
}

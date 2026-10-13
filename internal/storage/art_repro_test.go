// Synthetic reproducer for the ART-index corruption that took production down
// on 2026-09-28 and still has no upstream fix. It is built from the schema
// alone — no production bytes — so it can be run anywhere and, if the board
// ever decides to, published.
//
// The trigger, as reported upstream and as it happened here, is a sequence
// rather than a single statement: a long-lived table carrying ART indexes gets
// a VARCHAR column added by ALTER TABLE, an index is built on that added
// column, and large replace transactions then mutate it. The damage does not
// surface at write time, and it accumulates — production ran for weeks after
// the migration before an open aborted. So the sequence here is a loop, and
// every pass through it ends in a full stop and start: the checkpoint that
// folds the index on stop, and the open that reads it back, are the only two
// places this class of damage is ever reported.
//
// Size and repetitions are tunable, and turning them up is the first thing to
// try when the verdict is "did not reproduce" — a negative result at one size
// is evidence, not a proof of absence:
//
//	COTEL_ART_REPRO_SPANS=200000 COTEL_ART_REPRO_CYCLES=6 \
//	  CGO_ENABLED=1 go test -timeout 60m -v -run TestARTIndex ./internal/storage/

package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// defaultReproSpans / defaultReproCycles keep the test inside the ordinary
// `go test ./...` budget so every engine or driver bump runs it without anyone
// remembering to. The production table that was damaged held ~34 700 spans, so
// a deliberate hunt should raise these well past the defaults.
const (
	defaultReproSpans  = 20_000
	defaultReproCycles = 2
)

// reproSpanDays spreads the generated spans over more than the 30-day raw
// retention window, so each replace transaction has both rows to roll up and
// rows to keep. A roll-up that moves nothing exercises nothing.
const reproSpanDays = 60

// reproUsers is how many distinct values the index on the added column gets.
// Low cardinality over many rows is what gives the ART long shared prefixes and
// deep nodes, i.e. the structure the defect damages.
const reproUsers = 12

// preUserIDSchema is `spans` as it stood at schema version 2: every column the
// table has today except user_id, with the three ART indexes of that era. The
// point is to reach the ALTER from a table that was built and filled without
// the column, which is the state production was in.
const preUserIDSchema = `
CREATE TABLE schema_version (
    version     INTEGER PRIMARY KEY,
    applied_at  TIMESTAMPTZ DEFAULT now()
);
INSERT INTO schema_version (version) VALUES (1), (2);

CREATE TABLE spans (
    trace_id        VARCHAR NOT NULL,
    span_id         VARCHAR NOT NULL PRIMARY KEY,
    parent_span_id  VARCHAR,
    name            VARCHAR NOT NULL,
    start_time      TIMESTAMPTZ NOT NULL,
    end_time        TIMESTAMPTZ NOT NULL,
    service_name    VARCHAR,
    session_id      VARCHAR,
    model           VARCHAR,
    tool_name       VARCHAR,
    status_code     TINYINT DEFAULT 0,
    input_tokens    INTEGER,
    output_tokens   INTEGER,
    cache_read_tokens  INTEGER,
    cache_write_tokens INTEGER,
    cost_usd        DOUBLE,
    attributes      JSON,
    resource_attrs  JSON,
    ingested_at     TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX idx_spans_session_id ON spans(session_id);
CREATE INDEX idx_spans_start_time ON spans(start_time);
CREATE INDEX idx_spans_name       ON spans(name);
`

// seedSpans inserts a batch in one statement. Values are derived from the row
// number, never from a captured payload: the reproducer must carry no user id,
// session id or span attribute that ever belonged to anyone. Parameters are
// (now, span-days, first row number, last row number).
const seedSpans = `
INSERT INTO spans (
    trace_id, span_id, parent_span_id, name,
    start_time, end_time, service_name, session_id, model, tool_name,
    status_code, input_tokens, output_tokens,
    cache_read_tokens, cache_write_tokens, cost_usd,
    attributes, resource_attrs)
SELECT
    'trace-' || (i // 7)::VARCHAR,
    'span-' || i::VARCHAR,
    CASE WHEN i % 7 = 0 THEN NULL ELSE 'span-' || ((i // 7) * 7)::VARCHAR END,
    'span.name.' || (i % 17)::VARCHAR,
    ts,
    ts + INTERVAL 250 MILLISECOND,
    'synthetic-producer',
    'session-' || (i // 500)::VARCHAR,
    'model-' || (i % 3)::VARCHAR,
    CASE WHEN i % 5 = 0 THEN NULL ELSE 'tool-' || (i % 11)::VARCHAR END,
    CASE WHEN i % 23 = 0 THEN 2 ELSE 0 END,
    i % 4096, i % 512, i % 8192, i % 256, (i % 97) / 1000.0,
    '{"synthetic":' || i::VARCHAR || '}',
    '{"synthetic.resource":' || (i % 3)::VARCHAR || '}'
FROM (
    SELECT i, CAST(? AS TIMESTAMPTZ) - to_seconds((i % ?) * 86400 + (i * 37) % 86400) AS ts
    FROM range(?, ?) t(i)
) s
`

// populateAddedColumn writes the added VARCHAR column for every row that has no
// value yet. This is the mutation the defect needs: a bulk write through an ART
// index that was built on a column the table did not originally have.
var populateAddedColumn = fmt.Sprintf(
	`UPDATE spans SET user_id = 'user-' || (hash(span_id) %% %d)::VARCHAR WHERE user_id IS NULL`,
	reproUsers)

func envCount(t *testing.T, key string, def int) int {
	t.Helper()
	raw := os.Getenv(key)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		t.Fatalf("%s=%q: want a positive integer", key, raw)
	}
	return n
}

// openRaw opens the file with the same DSN Open builds, so the on-disk format
// of the seeded database matches what cotel would have written.
func openRaw(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("duckdb", path+"?storage_compatibility_version="+StorageCompatibilityVersion)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	db.SetMaxOpenConns(1)
	return db
}

func mustExec(t *testing.T, db *sql.DB, what, query string, args ...any) {
	t.Helper()
	start := time.Now()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	t.Logf("%s: ok in %s", what, time.Since(start).Round(time.Millisecond))
}

func indexNames(t *testing.T, ro *ReadDB) []string {
	t.Helper()
	rows, err := ro.Query(`SELECT index_name FROM duckdb_indexes() ORDER BY index_name`)
	if err != nil {
		t.Fatalf("duckdb_indexes(): %v", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan index name: %v", err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate indexes: %v", err)
	}
	return names
}

// TestARTIndexSurvivesAddedColumnAndBulkReplace runs the production sequence
// end to end against whatever engine the tree currently links, and fails if the
// database stops opening, an index goes missing, a checkpoint refuses to fold,
// or an indexed equality starts disagreeing with the same predicate written so
// the index cannot serve it. The last check is the one that catches the quiet
// variant of this damage: a wrong answer rather than an error.
func TestARTIndexSurvivesAddedColumnAndBulkReplace(t *testing.T) {
	spans := envCount(t, "COTEL_ART_REPRO_SPANS", defaultReproSpans)
	cycles := envCount(t, "COTEL_ART_REPRO_CYCLES", defaultReproCycles)
	path := filepath.Join(t.TempDir(), "art-repro.duckdb")
	ctx := context.Background()
	now := time.Now().UTC()

	// Phase 1 — a long-lived, indexed, pre-migration database.
	var engine string
	seed := openRaw(t, path)
	if err := seed.QueryRow("SELECT version()").Scan(&engine); err != nil {
		t.Fatalf("version(): %v", err)
	}
	t.Logf("engine %s: %d spans over %d days, then %d replace/restart cycles", engine, spans, reproSpanDays, cycles)
	mustExec(t, seed, "pre-migration schema", preUserIDSchema)
	mustExec(t, seed, fmt.Sprintf("seed %d spans", spans), seedSpans, now, reproSpanDays, 0, spans)
	mustExec(t, seed, "seed checkpoint", "CHECKPOINT")
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed: %v", err)
	}
	requireNoWAL(t, path, "after seeding")

	// Phase 2 — the migration, as cotel performs it on the next start:
	// schema.sql adds user_id VARCHAR to the filled table, then builds an ART
	// index on that added column.
	db, err := Open(path)
	if err != nil {
		t.Fatalf("REPRODUCED: the open that runs the migration failed: %v", err)
	}
	defer func() {
		if db != nil {
			db.Close()
		}
	}()
	if v, ok := appliedSchemaVersion(db.rw); !ok || v < 10 {
		t.Fatalf("schema did not migrate: applied version %d (recorded=%v)", v, ok)
	}
	t.Logf("indexes after migration: %v", indexNames(t, db.ReadOnly()))

	// The startup checkpoint cotel runs immediately after schema apply: the
	// earliest point at which a migration that damaged an index says so.
	if err := db.Checkpoint(ctx); err != nil {
		t.Fatalf("REPRODUCED: startup checkpoint right after the migration failed: %v", err)
	}

	// Phase 3 — the loop production actually ran: fill the added column, fold
	// the batch away in one large replace transaction, stop, start, repeat.
	batch := spans / 2
	if batch < 1 {
		batch = 1
	}
	for cycle := 1; cycle <= cycles; cycle++ {
		mustExec(t, db.rw, fmt.Sprintf("cycle %d: populate added column", cycle), populateAddedColumn)

		before := countSpans(t, db)
		start := time.Now()
		if err := db.RollupAndPurge(DefaultRetention); err != nil {
			t.Fatalf("cycle %d: roll-up and purge: %v", cycle, err)
		}
		after := countSpans(t, db)
		if after == before {
			t.Fatalf("cycle %d: replace transaction deleted nothing (%d spans, all inside the %d-day window?)",
				cycle, before, DefaultRetention.RawDays)
		}
		t.Logf("cycle %d: replace transaction %d spans -> %d in %s",
			cycle, before, after, time.Since(start).Round(time.Millisecond))

		// The stop. The shutdown checkpoint folds the mutated index into the
		// main file; this is where the 2026-09-28 damage first became visible.
		if err := db.Checkpoint(ctx); err != nil {
			t.Fatalf("REPRODUCED: cycle %d: shutdown checkpoint after the replace transaction failed: %v", cycle, err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("cycle %d: close: %v", cycle, err)
		}
		db = nil
		requireNoWAL(t, path, fmt.Sprintf("after the cycle %d shutdown checkpoint", cycle))

		// The start. On 1.1.3 the equivalent open aborted inside
		// duckdb_open_ext; a Go error here is the same defect reported sanely.
		db, err = Open(path)
		if err != nil {
			t.Fatalf("REPRODUCED: cycle %d: reopen after the replace transaction failed: %v", cycle, err)
		}
		if err := db.Checkpoint(ctx); err != nil {
			t.Fatalf("REPRODUCED: cycle %d: startup checkpoint after reopen failed: %v", cycle, err)
		}
		assertIndexesIntact(t, db, after)

		// Fresh ingest after the restart, so the next cycle mutates an index
		// that has already been folded and read back once.
		if cycle < cycles {
			lo := cycle * spans
			mustExec(t, db.rw, fmt.Sprintf("cycle %d: ingest %d more spans", cycle, batch),
				seedSpans, now, reproSpanDays, lo, lo+batch)
		}
	}

	t.Logf("did not reproduce on %s after %d cycles: every open succeeded, both checkpoints folded "+
		"on every cycle, all four indexes survived, and indexed probes agreed with unindexed ones",
		engine, cycles)
}

// assertIndexesIntact is the verdict after a restart: the four indexes exist,
// no row was lost, and the index on the added column still answers correctly.
func assertIndexesIntact(t *testing.T, db *DB, wantSpans int) {
	t.Helper()
	want := []string{"idx_spans_name", "idx_spans_session_id", "idx_spans_start_time", "idx_spans_user_id"}
	got := indexNames(t, db.ReadOnly())
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("after reopen: indexes %v, want %v", got, want)
	}
	if n := countSpans(t, db); n != wantSpans {
		t.Fatalf("after reopen: %d spans, want %d", n, wantSpans)
	}
	assertIndexedProbesAgree(t, db)
}

func countSpans(t *testing.T, db *DB) int {
	t.Helper()
	var n int
	if err := db.ReadOnly().QueryRow("SELECT count(*) FROM spans").Scan(&n); err != nil {
		t.Fatalf("count spans: %v", err)
	}
	return n
}

// assertIndexedProbesAgree compares an equality on the added column — which the
// ART index on it can serve — against the same predicate wrapped in an
// expression, which it cannot. A damaged or mis-addressed index shows up as a
// disagreement; a wrong count here is this defect's silent form, and neither a
// checkpoint nor an open reports it.
func assertIndexedProbesAgree(t *testing.T, db *DB) {
	t.Helper()
	ro := db.ReadOnly()
	for u := 0; u < reproUsers; u++ {
		want := fmt.Sprintf("user-%d", u)
		var indexed, scanned int
		if err := ro.QueryRow("SELECT count(*) FROM spans WHERE user_id = ?", want).Scan(&indexed); err != nil {
			t.Fatalf("indexed probe %s: %v", want, err)
		}
		if err := ro.QueryRow("SELECT count(*) FROM spans WHERE user_id || '' = ?", want).Scan(&scanned); err != nil {
			t.Fatalf("unindexed probe %s: %v", want, err)
		}
		if indexed != scanned {
			t.Fatalf("REPRODUCED: user_id = %q matched %d rows through the index, %d by scan", want, indexed, scanned)
		}
		if indexed == 0 {
			t.Fatalf("probe %s matched nothing by either path; the fixture built no entries for it", want)
		}
	}
}

func requireNoWAL(t *testing.T, path, when string) {
	t.Helper()
	info, err := os.Stat(path + ".wal")
	if err == nil {
		t.Fatalf("%s: %s is still present (%d bytes); the checkpoint did not fold", when, path+".wal", info.Size())
	}
	if !os.IsNotExist(err) {
		t.Fatalf("stat wal %s: %v", when, err)
	}
}

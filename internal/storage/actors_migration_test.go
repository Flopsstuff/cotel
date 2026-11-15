package storage

import (
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// seedV10 builds a database exactly as schema version 10 left it - testdata
// holds a frozen copy of that release's schema.sql - and fills it with an
// actor, attributed and anonymous spans, and a rolled-up day.
func seedV10(t *testing.T, path string) {
	t.Helper()
	ddl, err := os.ReadFile(filepath.Join("testdata", "schema_v10.sql"))
	if err != nil {
		t.Fatalf("read v10 schema: %v", err)
	}
	raw, err := sql.Open("duckdb", path)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	defer raw.Close()
	stmts := []string{
		string(ddl),
		`INSERT INTO users (id, name, token) VALUES ('u1', 'Daedalus', 'cotel_` + repeatHex("a") + `')`,
		`INSERT INTO users (id, name, token, deleted_at) VALUES ('u2', 'Gone', 'cotel_` + repeatHex("b") + `', now())`,
		`INSERT INTO spans (trace_id, span_id, name, start_time, end_time, session_id, user_id, cost_usd)
		 VALUES ('t', 's1', 'n', now(), now(), 'sess1', 'Daedalus', 1.5),
		        ('t', 's2', 'n', now(), now(), 'sess2', NULL, 0.5)`,
		`INSERT INTO daily_usage (day, session_id, model, tool_name, user_id, span_count, total_cost_usd)
		 VALUES (DATE '2026-01-01', 'old', 'm', '', 'Daedalus', 3, 2.0)`,
	}
	for _, s := range stmts {
		if _, err := raw.Exec(s); err != nil {
			t.Fatalf("seed v10: %v\n%s", err, s)
		}
	}
}

func repeatHex(c string) string {
	out := ""
	for i := 0; i < 64; i++ {
		out += c
	}
	return out
}

func TestMigrateUsersToActors_FromV10(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v10.duckdb")
	seedV10(t, path)

	db, err := Open(path)
	if err != nil {
		t.Fatalf("open v10 database: %v", err)
	}
	defer db.Close()

	if v, _ := appliedSchemaVersion(db.rw); v != 11 {
		t.Fatalf("schema version = %d, want 11", v)
	}

	// The old names are gone, so no query can silently read an empty twin.
	tx, err := db.rw.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ table, col string }{{"spans", "user_id"}, {"daily_usage", "user_id"}} {
		if ok, err := columnExists(tx, c.table, c.col); err != nil || ok {
			t.Errorf("%s.%s still exists (err=%v)", c.table, c.col, err)
		}
	}
	if ok, err := tableExists(tx, "users"); err != nil || ok {
		t.Errorf("users table still exists (err=%v)", err)
	}
	_ = tx.Rollback()

	// Tokens survive the table rename, and soft-deletes keep rejecting.
	a, err := db.GetActorByToken("cotel_" + repeatHex("a"))
	if err != nil || a.Name != "Daedalus" {
		t.Fatalf("token lookup after migration = %+v, %v", a, err)
	}
	if _, err := db.GetActorByToken("cotel_" + repeatHex("b")); err == nil {
		t.Fatal("soft-deleted actor's token accepted after migration")
	}

	// Attribution survives the column renames, raw and rolled up alike.
	got, err := db.GetActorWithStats("u1", nil)
	if err != nil {
		t.Fatalf("GetActorWithStats: %v", err)
	}
	if got.TotalCostUSD != 3.5 || got.Sessions != 2 {
		t.Fatalf("Daedalus stats = cost %v sessions %d, want 3.5 / 2", got.TotalCostUSD, got.Sessions)
	}
	var anon int
	if err := db.rw.QueryRow(`SELECT count(*) FROM spans WHERE actor_id IS NULL`).Scan(&anon); err != nil || anon != 1 {
		t.Fatalf("anonymous spans = %d (err=%v), want 1", anon, err)
	}

	// The secondary indexes come back under the new name.
	rows, err := db.rw.Query(`SELECT index_name FROM duckdb_indexes() WHERE table_name = 'spans'`)
	if err != nil {
		t.Fatal(err)
	}
	var idx []string
	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		idx = append(idx, n)
	}
	rows.Close()
	sort.Strings(idx)
	want := []string{"idx_spans_actor_id", "idx_spans_name", "idx_spans_session_id", "idx_spans_start_time"}
	if len(idx) != len(want) {
		t.Fatalf("spans indexes = %v, want %v", idx, want)
	}
	for i := range want {
		if idx[i] != want[i] {
			t.Fatalf("spans indexes = %v, want %v", idx, want)
		}
	}

	// New ingest lands in the renamed column.
	if err := db.InsertSpan(Span{TraceID: "t", SpanID: "s3", Name: "n", StartTime: time.Now(), EndTime: time.Now(), ActorID: "Daedalus"}); err != nil {
		t.Fatalf("insert after migration: %v", err)
	}
}

// A second open of a migrated file, and a reopen after the schema hash changes,
// must both be no-ops for the rename.
func TestMigrateUsersToActors_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v10.duckdb")
	seedV10(t, path)
	for i := 0; i < 2; i++ {
		db, err := Open(path)
		if err != nil {
			t.Fatalf("open #%d: %v", i+1, err)
		}
		if i == 0 {
			// Force the next open to re-apply schema.sql and re-run the rename check.
			if _, err := db.rw.Exec(`UPDATE settings SET value = 'stale' WHERE key = ?`, schemaSHAKey); err != nil {
				t.Fatal(err)
			}
		}
		db.Close()
	}
	db, err := Open(path)
	if err != nil {
		t.Fatalf("final open: %v", err)
	}
	defer db.Close()
	var n int
	if err := db.rw.QueryRow(`SELECT count(*) FROM actors`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("actors rows = %d (err=%v), want 2", n, err)
	}
}

func TestMigrateUsersToActors_RefusesBothTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "both.duckdb")
	seedV10(t, path)
	raw, err := sql.Open("duckdb", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE actors (id VARCHAR)`); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	if db, err := Open(path); err == nil {
		db.Close()
		t.Fatal("open succeeded with both users and actors present")
	}
}

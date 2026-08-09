package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func insertProbe(t *testing.T, db *DB, id string) {
	t.Helper()
	if err := db.InsertSpan(Span{
		TraceID:   id,
		SpanID:    id,
		Name:      "probe",
		StartTime: time.Unix(0, 0),
		EndTime:   time.Unix(0, 1),
	}); err != nil {
		t.Fatalf("insert %s: %v", id, err)
	}
}

// TestCheckpointPersistsAcrossReopen exercises the shutdown path: a CHECKPOINT
// followed by a clean close folds the WAL into the main file, and the rows are
// still there on the next open.
func TestCheckpointPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cp.duckdb")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	insertProbe(t, db, "a")
	insertProbe(t, db, "b")

	if err := db.Checkpoint(context.Background()); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	db2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close()

	var n int
	if err := db2.ReadOnly().QueryRow("SELECT COUNT(*) FROM spans").Scan(&n); err != nil {
		t.Fatalf("count after reopen: %v", err)
	}
	if n != 2 {
		t.Fatalf("after reopen: got %d spans, want 2", n)
	}
}

// TestCheckpointHonoursContext proves the shutdown checkpoint cannot hang past
// its deadline: a cancelled context returns an error instead of blocking.
func TestCheckpointHonoursContext(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := db.Checkpoint(ctx); err == nil {
		t.Fatal("checkpoint with cancelled context: got nil error, want non-nil")
	}
}

package storage

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// wantStorageVersion is the `storage_version` number
// StorageCompatibilityVersion must produce. See that constant for why 64 and
// not whatever the engine's default happens to be.
const wantStorageVersion = 64

// TestNewDatabaseIsWrittenAtPinnedStorageVersion reads the format number out of
// the file header rather than asking the engine, because the question is what a
// *different*, older engine would find there. A driver or engine bump that
// quietly raises it makes every new file unreadable by the previous release,
// which is only discovered when a rollback is attempted and fails.
func TestNewDatabaseIsWrittenAtPinnedStorageVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "format.duckdb")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	insertProbe(t, db, "a")
	if err := db.Checkpoint(context.Background()); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Main header: an 8-byte checksum, the magic "DUCK", then the format
	// number as a little-endian uint64.
	hdr := make([]byte, 20)
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	defer f.Close()
	if _, err := f.ReadAt(hdr, 0); err != nil {
		t.Fatalf("read header: %v", err)
	}

	if magic := string(hdr[8:12]); magic != "DUCK" {
		t.Fatalf("header magic: got %q, want \"DUCK\" - not a DuckDB file?", magic)
	}
	if got := binary.LittleEndian.Uint64(hdr[12:20]); got != wantStorageVersion {
		t.Fatalf("storage_version: got %d, want %d - a file at %d cannot be opened by an older DuckDB, so this breaks rollback",
			got, wantStorageVersion, got)
	}
}

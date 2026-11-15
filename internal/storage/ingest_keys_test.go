package storage

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

var ingestKeyRe = regexp.MustCompile(`^cotel_ik_[0-9a-f]{64}$`)

func TestCreateIngestKey_RetrievableToken(t *testing.T) {
	db := openTestActorDB(t)

	k, err := db.CreateIngestKey("paperclip")
	if err != nil {
		t.Fatalf("CreateIngestKey: %v", err)
	}
	if !ingestKeyRe.MatchString(k.Token) {
		t.Fatalf("token %q does not match %s", k.Token, ingestKeyRe)
	}
	if k.ID == "" || k.Name != "paperclip" || k.CreatedAt.IsZero() {
		t.Fatalf("record = %+v", k)
	}
	// The token is kept so it can be copied again later, like an actor token.
	keys, err := db.ListIngestKeys()
	if err != nil || len(keys) != 1 || keys[0].Token != k.Token {
		t.Fatalf("list = %+v (err %v), want the created token back", keys, err)
	}
}

func TestCreateIngestKey_Unique(t *testing.T) {
	db := openTestActorDB(t)
	a, err := db.CreateIngestKey("same")
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.CreateIngestKey("same")
	if err != nil {
		t.Fatalf("second key with the same name: %v", err)
	}
	if a.Token == b.Token {
		t.Fatal("two keys share a token")
	}
}

func TestListIngestKeys(t *testing.T) {
	db := openTestActorDB(t)

	keys, err := db.ListIngestKeys()
	if err != nil {
		t.Fatalf("ListIngestKeys (empty): %v", err)
	}
	if keys == nil || len(keys) != 0 {
		t.Fatalf("empty list = %#v, want non-nil empty slice", keys)
	}

	var want []string
	for _, name := range []string{"first", "second", "third"} {
		k, err := db.CreateIngestKey(name)
		if err != nil {
			t.Fatal(err)
		}
		// Pin distinct creation times so "oldest first" is observable.
		if _, err := db.rw.Exec(
			`UPDATE ingest_keys SET created_at = TIMESTAMPTZ '2026-01-01 00:00:00+00' + to_days(?) WHERE id = ?`,
			len(want), k.ID,
		); err != nil {
			t.Fatalf("pin created_at: %v", err)
		}
		want = append(want, name)
	}

	keys, err = db.ListIngestKeys()
	if err != nil {
		t.Fatalf("ListIngestKeys: %v", err)
	}
	if len(keys) != len(want) {
		t.Fatalf("got %d keys, want %d", len(keys), len(want))
	}
	for i, k := range keys {
		if k.Name != want[i] {
			t.Errorf("keys[%d] = %q, want %q (oldest first)", i, k.Name, want[i])
		}
	}
}

func TestDeleteIngestKey(t *testing.T) {
	db := openTestActorDB(t)
	k, err := db.CreateIngestKey("doomed")
	plaintext := k.Token
	if err != nil {
		t.Fatal(err)
	}

	if err := db.DeleteIngestKey(k.ID); err != nil {
		t.Fatalf("DeleteIngestKey: %v", err)
	}
	if n := queryInt64(t, db, `SELECT count(*) FROM ingest_keys WHERE id = ?`, k.ID); n != 0 {
		t.Fatalf("row survived delete")
	}
	if ok, err := db.IsIngestKey(plaintext); err != nil || ok {
		t.Fatalf("deleted key still valid: ok=%v err=%v", ok, err)
	}
	if err := db.DeleteIngestKey(k.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: err = %v, want ErrNotFound", err)
	}
	if err := db.DeleteIngestKey("no-such-id"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: err = %v, want ErrNotFound", err)
	}
}

func TestIsIngestKey(t *testing.T) {
	db := openTestActorDB(t)
	live, err := db.CreateIngestKey("live")
	plaintext := live.Token
	if err != nil {
		t.Fatal(err)
	}
	actor := mustCreateTestActor(t, db, "alice")

	tests := []struct {
		name  string
		token string
		want  bool
	}{
		{"live key", plaintext, true},
		{"empty", "", false},
		{"actor token", actor.Token, false},
		{"unknown key", "cotel_ik_" + repeatHex("0"), false},
		{"prefix only", "cotel_ik_", false},
		{"display prefix", plaintext[:12], false},
		{"key with trailing space", plaintext + " ", false},
		{"no prefix", plaintext[len("cotel_ik_"):], false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := db.IsIngestKey(tt.token)
			if err != nil {
				t.Fatalf("IsIngestKey: %v", err)
			}
			if got != tt.want {
				t.Fatalf("IsIngestKey(%q) = %v, want %v", tt.token, got, tt.want)
			}
		})
	}
}

// seedV11 builds a database exactly as schema version 11 left it: testdata holds
// a frozen copy of that release's schema.sql, applied and hash-recorded the way
// a v11 binary would have done it.
func seedV11(t *testing.T, path string) {
	t.Helper()
	ddl, err := os.ReadFile(filepath.Join("testdata", "schema_v11.sql"))
	if err != nil {
		t.Fatalf("read v11 schema: %v", err)
	}
	raw, err := sql.Open("duckdb", path)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	defer raw.Close()
	if _, err := raw.Exec(string(ddl)); err != nil {
		t.Fatalf("apply v11 schema: %v", err)
	}
	if err := recordSchemaSHA(raw, "v11-sha"); err != nil {
		t.Fatalf("record v11 sha: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO actors (id, name, token) VALUES ('a1', 'Daedalus', 'cotel_` + repeatHex("a") + `')`); err != nil {
		t.Fatalf("seed actor: %v", err)
	}
}

// A v11 database gains ingest_keys and schema_version 12 on open, keeps its
// data, and a second open is a no-op.
func TestSchemaV12_FromV11(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v11.duckdb")
	seedV11(t, path)

	for i := 0; i < 2; i++ {
		db, err := Open(path)
		if err != nil {
			t.Fatalf("open #%d: %v", i+1, err)
		}
		if v := appliedVersion(t, db); v < 12 {
			t.Fatalf("open #%d: schema version = %d, want >= 12", i+1, v)
		}
		if n := queryInt64(t, db, `SELECT count(*) FROM actors WHERE name = 'Daedalus'`); n != 1 {
			t.Fatalf("open #%d: v11 actor lost (%d rows)", i+1, n)
		}
		if i == 0 {
			if _, err := db.CreateIngestKey("fleet"); err != nil {
				t.Fatalf("CreateIngestKey on migrated db: %v", err)
			}
		}
		keys, err := db.ListIngestKeys()
		if err != nil {
			t.Fatalf("open #%d: ListIngestKeys: %v", i+1, err)
		}
		if len(keys) != 1 || keys[0].Name != "fleet" {
			t.Fatalf("open #%d: ingest keys = %+v, want the one created", i+1, keys)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("close #%d: %v", i+1, err)
		}
	}
}

// Re-applying schema.sql over a database that already has ingest keys (any
// hash change does this) must leave them intact.
func TestSchemaV12_ReapplyKeepsKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reapply.duckdb")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	kept, err := db.CreateIngestKey("keep")
	plaintext := kept.Token
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting(schemaSHAKey, "stale"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close()
	if appliedSchemaSHA(db2.rw) != embeddedSHA(t) {
		t.Fatal("schema was not re-applied")
	}
	if ok, err := db2.IsIngestKey(plaintext); err != nil || !ok {
		t.Fatalf("key lost on re-apply: ok=%v err=%v", ok, err)
	}
}

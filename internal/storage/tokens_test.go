package storage

import (
	"testing"
)

func TestTokenCRUD(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	defer db.Close()

	// Empty list initially.
	rows, err := db.ListTokens()
	if err != nil {
		t.Fatalf("ListTokens (empty): %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("ListTokens: got %d rows, want 0", len(rows))
	}

	// CreateToken.
	const (
		id1    = "aaaaaaaa-0000-0000-0000-000000000001"
		name1  = "Dev laptop"
		hash1  = "abc123hash"
		prefix1 = "cotel_ab"
	)
	if err := db.CreateToken(id1, name1, hash1, prefix1); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	// Create a second token.
	const (
		id2    = "aaaaaaaa-0000-0000-0000-000000000002"
		name2  = "CI server"
		hash2  = "def456hash"
		prefix2 = "cotel_de"
	)
	if err := db.CreateToken(id2, name2, hash2, prefix2); err != nil {
		t.Fatalf("CreateToken 2: %v", err)
	}

	// ListTokens returns both tokens.
	rows, err = db.ListTokens()
	if err != nil {
		t.Fatalf("ListTokens: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("ListTokens: got %d rows, want 2", len(rows))
	}
	if rows[0].ID != id1 || rows[0].Name != name1 || rows[0].Prefix != prefix1 {
		t.Errorf("first token: got %+v, want id=%s name=%s prefix=%s", rows[0], id1, name1, prefix1)
	}
	if rows[1].ID != id2 || rows[1].Name != name2 || rows[1].Prefix != prefix2 {
		t.Errorf("second token: got %+v, want id=%s name=%s prefix=%s", rows[1], id2, name2, prefix2)
	}
	// Hashes must not appear in list results.
	for _, r := range rows {
		if r.CreatedAt.IsZero() {
			t.Errorf("token %s: CreatedAt is zero", r.ID)
		}
	}

	// ValidateToken — correct hash matches.
	if !db.ValidateToken(hash1) {
		t.Error("ValidateToken(hash1): got false, want true")
	}

	// ValidateToken — unknown hash returns false.
	if db.ValidateToken("notahash") {
		t.Error("ValidateToken(unknown): got true, want false")
	}

	// last_used_at is set after a successful validate.
	rows, err = db.ListTokens()
	if err != nil {
		t.Fatalf("ListTokens after validate: %v", err)
	}
	if rows[0].LastUsedAt == nil {
		t.Error("LastUsedAt: want non-nil after ValidateToken")
	}
	if rows[1].LastUsedAt != nil {
		t.Error("LastUsedAt for unused token: want nil")
	}

	// DeleteToken removes the first token.
	if err := db.DeleteToken(id1); err != nil {
		t.Fatalf("DeleteToken: %v", err)
	}
	rows, err = db.ListTokens()
	if err != nil {
		t.Fatalf("ListTokens after delete: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListTokens after delete: got %d rows, want 1", len(rows))
	}
	if rows[0].ID != id2 {
		t.Errorf("remaining token: got id=%s, want %s", rows[0].ID, id2)
	}

	// ValidateToken on deleted hash returns false.
	if db.ValidateToken(hash1) {
		t.Error("ValidateToken(deleted token): got true, want false")
	}

	// DeleteToken on non-existent id is a no-op (no error).
	if err := db.DeleteToken("does-not-exist"); err != nil {
		t.Errorf("DeleteToken(non-existent): unexpected error: %v", err)
	}
}

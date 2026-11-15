package storage

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// IngestKeyPrefix starts every ingest key plaintext. It is a sub-prefix of the
// actor token prefix "cotel_", so callers that tell the two apart must check
// this one first.
const IngestKeyPrefix = "cotel_ik_"

// ingestKeyDisplayLen is how many leading plaintext characters are kept as the
// key's display prefix (e.g. "cotel_ik_ab1").
const ingestKeyDisplayLen = 12

// IngestKey is a shared key that authorizes OTLP ingest without identifying an
// actor. Only the sha256 of its plaintext is stored; the plaintext is returned
// once, by CreateIngestKey.
type IngestKey struct {
	ID        string
	Name      string
	Prefix    string
	CreatedAt time.Time
}

func generateIngestKey() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return IngestKeyPrefix + hex.EncodeToString(b[:]), nil
}

func hashIngestKey(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// CreateIngestKey stores a new ingest key under name and returns its record and
// plaintext. The plaintext is not recoverable afterwards.
func (db *DB) CreateIngestKey(name string) (IngestKey, string, error) {
	plaintext, err := generateIngestKey()
	if err != nil {
		return IngestKey{}, "", err
	}
	id := generateID()
	if _, err := db.rw.Exec(
		`INSERT INTO ingest_keys (id, name, token_hash, prefix) VALUES (?, ?, ?, ?)`,
		id, name, hashIngestKey(plaintext), plaintext[:ingestKeyDisplayLen],
	); err != nil {
		return IngestKey{}, "", err
	}
	var k IngestKey
	if err := db.rw.QueryRow(
		`SELECT id, name, prefix, created_at FROM ingest_keys WHERE id = ?`, id,
	).Scan(&k.ID, &k.Name, &k.Prefix, &k.CreatedAt); err != nil {
		return IngestKey{}, "", err
	}
	return k, plaintext, nil
}

// ListIngestKeys returns every ingest key, oldest first.
func (db *DB) ListIngestKeys() ([]IngestKey, error) {
	rows, err := db.rw.Query(
		`SELECT id, name, prefix, created_at FROM ingest_keys ORDER BY created_at ASC, id ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IngestKey{}
	for rows.Next() {
		var k IngestKey
		if err := rows.Scan(&k.ID, &k.Name, &k.Prefix, &k.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// DeleteIngestKey revokes an ingest key by deleting its row. Returns
// ErrNotFound when no key has that id.
func (db *DB) DeleteIngestKey(id string) error {
	result, err := db.rw.Exec(`DELETE FROM ingest_keys WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// IsIngestKey reports whether plaintext is a live ingest key. Anything not
// shaped like one is rejected without touching the database. It only reads, so
// it is safe on the hot ingest path.
func (db *DB) IsIngestKey(plaintext string) (bool, error) {
	if !strings.HasPrefix(plaintext, IngestKeyPrefix) {
		return false, nil
	}
	var n int
	if err := db.rw.QueryRow(
		`SELECT count(*) FROM ingest_keys WHERE token_hash = ?`, hashIngestKey(plaintext),
	).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

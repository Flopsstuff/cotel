package storage

import "time"

// TokenRow is a single row from api_tokens (hash is never returned).
type TokenRow struct {
	ID          string
	Name        string
	Prefix      string
	CreatedAt   time.Time
	LastUsedAt  *time.Time
}

// CreateToken inserts a new API token record. id is a UUID, hash is SHA-256(plaintext),
// prefix is the first 8 display characters.
func (db *DB) CreateToken(id, name, hash, prefix string) error {
	_, err := db.rw.Exec(
		`INSERT INTO api_tokens (id, name, token_hash, prefix) VALUES (?, ?, ?, ?)`,
		id, name, hash, prefix,
	)
	return err
}

// ListTokens returns all tokens ordered by creation time. The hash is never included.
func (db *DB) ListTokens() ([]TokenRow, error) {
	rows, err := db.rw.Query(
		`SELECT id, name, prefix, created_at, last_used_at FROM api_tokens ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TokenRow
	for rows.Next() {
		var t TokenRow
		var lastUsed *time.Time
		if err := rows.Scan(&t.ID, &t.Name, &t.Prefix, &t.CreatedAt, &lastUsed); err != nil {
			return nil, err
		}
		t.LastUsedAt = lastUsed
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteToken removes a token by its UUID. No-op if the id does not exist.
func (db *DB) DeleteToken(id string) error {
	_, err := db.rw.Exec(`DELETE FROM api_tokens WHERE id = ?`, id)
	return err
}

// ValidateToken checks whether a SHA-256 hash matches a stored token.
// Returns true on match and updates last_used_at.
func (db *DB) ValidateToken(hash string) bool {
	res, err := db.rw.Exec(
		`UPDATE api_tokens SET last_used_at = now() WHERE token_hash = ?`,
		hash,
	)
	if err != nil {
		return false
	}
	n, err := res.RowsAffected()
	return err == nil && n > 0
}

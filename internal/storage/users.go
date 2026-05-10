package storage

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// AnonymousUserID is the synthetic ID used to represent spans with no user_id (unauthenticated ingest).
const AnonymousUserID = "__anonymous__"

// ErrNotFound is returned when the target user does not exist or is already soft-deleted.
var ErrNotFound = errors.New("not found")

// User is a named principal whose token authenticates OTLP ingest requests.
type User struct {
	ID        string
	Name      string
	Token     string
	CreatedAt time.Time
}

// UserWithStats adds span-derived metrics to a User.
type UserWithStats struct {
	User
	TotalCostUSD float64
	Sessions     int64
	LastSeen     *time.Time
}

func generateToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "cotel_" + hex.EncodeToString(b[:]), nil
}

func generateID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// CreateUser inserts a new user with a generated token and returns the full record.
func (db *DB) CreateUser(name string) (User, error) {
	token, err := generateToken()
	if err != nil {
		return User{}, err
	}
	id := generateID()
	_, err = db.rw.Exec(
		`INSERT INTO users (id, name, token) VALUES (?, ?, ?)`,
		id, name, token,
	)
	if err != nil {
		return User{}, err
	}
	var u User
	err = db.rw.QueryRow(
		`SELECT id, name, token, created_at FROM users WHERE id = ?`, id,
	).Scan(&u.ID, &u.Name, &u.Token, &u.CreatedAt)
	return u, err
}

// ListUsersWithStats returns all non-deleted users with span-derived cost, session count, and last
// seen. A synthetic "Anonymous" entry is appended when there are spans with no user_id.
func (db *DB) ListUsersWithStats() ([]UserWithStats, error) {
	rows, err := db.rw.Query(`
		SELECT
			u.id, u.name, u.token, u.created_at,
			COALESCE(SUM(s.cost_usd), 0)          AS total_cost_usd,
			COUNT(DISTINCT s.session_id)           AS sessions,
			MAX(s.end_time)                        AS last_seen
		FROM users u
		LEFT JOIN spans s ON s.user_id = u.name
		WHERE u.deleted_at IS NULL
		GROUP BY u.id, u.name, u.token, u.created_at
		ORDER BY u.created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []UserWithStats
	for rows.Next() {
		var u UserWithStats
		var lastSeen *time.Time
		if err := rows.Scan(
			&u.ID, &u.Name, &u.Token, &u.CreatedAt,
			&u.TotalCostUSD, &u.Sessions, &lastSeen,
		); err != nil {
			return nil, err
		}
		u.LastSeen = lastSeen
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Append a synthetic anonymous row if any spans lack a user_id.
	var anonCost float64
	var anonSessions int64
	var anonLastSeen *time.Time
	_ = db.rw.QueryRow(`
		SELECT
			COALESCE(SUM(cost_usd), 0),
			COUNT(DISTINCT session_id),
			MAX(end_time)
		FROM spans
		WHERE user_id IS NULL
	`).Scan(&anonCost, &anonSessions, &anonLastSeen)
	if anonSessions > 0 || anonCost > 0 {
		out = append(out, UserWithStats{
			User:         User{ID: AnonymousUserID, Name: "Anonymous"},
			TotalCostUSD: anonCost,
			Sessions:     anonSessions,
			LastSeen:     anonLastSeen,
		})
	}

	return out, nil
}

// GetUserByToken looks up a non-deleted user by their plaintext token.
func (db *DB) GetUserByToken(token string) (*User, error) {
	var u User
	err := db.rw.QueryRow(
		`SELECT id, name, token, created_at FROM users WHERE token = ? AND deleted_at IS NULL`, token,
	).Scan(&u.ID, &u.Name, &u.Token, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// RotateToken replaces a user's token with a freshly generated one.
func (db *DB) RotateToken(userID string) (User, error) {
	token, err := generateToken()
	if err != nil {
		return User{}, err
	}
	_, err = db.rw.Exec(
		`UPDATE users SET token = ? WHERE id = ?`, token, userID,
	)
	if err != nil {
		return User{}, err
	}
	var u User
	err = db.rw.QueryRow(
		`SELECT id, name, token, created_at FROM users WHERE id = ?`, userID,
	).Scan(&u.ID, &u.Name, &u.Token, &u.CreatedAt)
	return u, err
}

// SoftDeleteUser stamps deleted_at, making the user invisible and their token
// invalid (GetUserByToken filters deleted_at IS NULL). Returns ErrNotFound if
// the user does not exist or is already soft-deleted.
//
// Note: DuckDB v1.8.x has a bug where UPDATE on a UNIQUE-constrained column
// raises a false "duplicate key" error. We therefore do NOT update the token
// column — the deleted_at filter is sufficient to reject auth for deleted users.
func (db *DB) SoftDeleteUser(userID string) error {
	result, err := db.rw.Exec(
		`UPDATE users SET deleted_at = CURRENT_TIMESTAMP WHERE id = ? AND deleted_at IS NULL`,
		userID,
	)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteUserWithHistory hard-deletes all spans and daily_usage rows belonging to the
// user, then hard-deletes the user row. Returns ErrNotFound if the user does not
// exist or is already soft-deleted.
//
// Note: DuckDB v1.x fails with "Vector::Reference used on vector of different type"
// when executing UPDATE on spans.user_id (a column added via ADD COLUMN on a table
// with GENERATED ALWAYS columns). We therefore DELETE span rows rather than
// anonymising them. All history is removed together with the user.
func (db *DB) DeleteUserWithHistory(userID string) error {
	tx, err := db.rw.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var name string
	if err = tx.QueryRow(
		`SELECT name FROM users WHERE id = ? AND deleted_at IS NULL`, userID,
	).Scan(&name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = ErrNotFound
		}
		return err
	}

	if _, err = tx.Exec(`DELETE FROM spans WHERE user_id = ?`, name); err != nil {
		return err
	}

	if _, err = tx.Exec(`DELETE FROM daily_usage WHERE user_id = ?`, name); err != nil {
		return err
	}

	if _, err = tx.Exec(`DELETE FROM users WHERE id = ?`, userID); err != nil {
		return err
	}

	return tx.Commit()
}

// DeleteAnonymousData hard-deletes all spans and daily_usage rows that have no user_id (anonymous ingest).
func (db *DB) DeleteAnonymousData() error {
	tx, err := db.rw.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.Exec(`DELETE FROM spans WHERE user_id IS NULL`); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM daily_usage WHERE user_id IS NULL`); err != nil {
		return err
	}
	return tx.Commit()
}

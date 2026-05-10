package storage

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

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

// ListUsersWithStats returns all users with span-derived cost, session count, and last seen.
// Spans are joined by user name (spans.user_id stores the user's display name).
func (db *DB) ListUsersWithStats() ([]UserWithStats, error) {
	rows, err := db.rw.Query(`
		SELECT
			u.id, u.name, u.token, u.created_at,
			COALESCE(SUM(s.cost_usd), 0)          AS total_cost_usd,
			COUNT(DISTINCT s.session_id)           AS sessions,
			MAX(s.end_time)                        AS last_seen
		FROM users u
		LEFT JOIN spans s ON s.user_id = u.name
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
	return out, rows.Err()
}

// GetUserByToken looks up a user by their plaintext token.
func (db *DB) GetUserByToken(token string) (*User, error) {
	var u User
	err := db.rw.QueryRow(
		`SELECT id, name, token, created_at FROM users WHERE token = ?`, token,
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

// DeleteUser removes a user by ID.
func (db *DB) DeleteUser(userID string) error {
	_, err := db.rw.Exec(`DELETE FROM users WHERE id = ?`, userID)
	return err
}

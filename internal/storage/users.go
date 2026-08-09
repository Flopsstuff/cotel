package storage

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
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

// ListUsersOptions parameterises ListUsersPage.
type ListUsersOptions struct {
	Since *time.Time // lower bound for range-scoped cost/sessions; nil = all-time
	Query string     // case-insensitive substring match on name; "" = no filter
	Sort  string     // name|created_at|last_seen|cost|sessions (invalid → cost)
	Order string     // asc|desc (invalid → desc)
	Page  int        // 1-based page index (invalid → 1)
	Limit int        // rows per page; 0 = unpaginated; clamped to 500
}

// userSortExprs whitelists sort keys to their SELECT-list column, so the caller
// can never inject an ORDER BY expression.
var userSortExprs = map[string]string{
	"name":       "name",
	"created_at": "created_at",
	"last_seen":  "last_seen",
	"cost":       "cost",
	"sessions":   "sessions",
}

// escapeLike escapes the LIKE/ILIKE wildcards so a search term is matched
// literally under `ESCAPE '\'`.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// rangeUsageCTE is the shared union of raw spans and rolled-up daily_usage that
// answers range-scoped cost/sessions (ADR-0011). Raw spans cover every day they
// still exist for; daily_usage covers strictly earlier days. The split is exact
// because the roll-up deletes the spans it aggregates, so the boundary day
// (raw_floor) lives half in each source with no overlap.
const rangeUsageCTE = `
raw_floor AS (
    SELECT MIN(start_time) AS ts FROM spans
),
usage AS (
    SELECT user_id, session_id, cost_usd AS cost
    FROM spans
    WHERE TRUE%s
    UNION ALL
    SELECT du.user_id, du.session_id, du.total_cost_usd AS cost
    FROM daily_usage du CROSS JOIN raw_floor rf
    WHERE (rf.ts IS NULL OR du.day <= CAST(rf.ts AS DATE))%s
)`

// ListUsersPage returns a sorted, optionally paged slice of users with
// range-scoped cost/session stats, plus the total number of matching rows
// (counted before paging). The synthetic __anonymous__ row is produced inside
// the SQL so it sorts, filters, and paginates inline with real users.
func (db *DB) ListUsersPage(opts ListUsersOptions) ([]UserWithStats, int, error) {
	sortExpr, ok := userSortExprs[opts.Sort]
	if !ok {
		sortExpr = "cost"
	}
	order := "DESC"
	if strings.EqualFold(opts.Order, "asc") {
		order = "ASC"
	}
	page := opts.Page
	if page < 1 {
		page = 1
	}
	limit := opts.Limit
	if limit < 0 {
		limit = 0
	}
	if limit > 500 {
		limit = 500
	}

	var rawSince, aggSince string
	args := []any{}
	if opts.Since != nil {
		rawSince = " AND start_time >= ?"
		aggSince = " AND du.day >= CAST(? AS DATE)"
		args = append(args, *opts.Since, *opts.Since)
	}

	var qFilter string
	if q := strings.TrimSpace(opts.Query); q != "" {
		qFilter = " AND name ILIKE ? ESCAPE '\\'"
		args = append(args, "%"+escapeLike(q)+"%")
	}

	limitClause := ""
	if limit > 0 {
		limitClause = " LIMIT ? OFFSET ?"
		args = append(args, limit, (page-1)*limit)
	}

	query := fmt.Sprintf(`
WITH `+rangeUsageCTE+`,
usage_agg AS (
    SELECT user_id,
           COALESCE(SUM(cost), 0) AS cost,
           COUNT(DISTINCT session_id) AS sessions
    FROM usage
    GROUP BY user_id
),
seen AS (
    SELECT user_id, MAX(end_time) AS last_seen FROM spans GROUP BY user_id
),
named AS (
    SELECT u.id, u.name, u.token, u.created_at,
           COALESCE(ua.cost, 0) AS cost,
           COALESCE(ua.sessions, 0) AS sessions,
           s.last_seen AS last_seen
    FROM users u
    LEFT JOIN usage_agg ua ON ua.user_id = u.name
    LEFT JOIN seen s ON s.user_id = u.name
    WHERE u.deleted_at IS NULL
),
anon AS (
    SELECT
        '%s' AS id,
        'Anonymous' AS name,
        CAST(NULL AS VARCHAR) AS token,
        CAST(NULL AS TIMESTAMPTZ) AS created_at,
        COALESCE((SELECT cost FROM usage_agg WHERE user_id IS NULL), 0) AS cost,
        COALESCE((SELECT sessions FROM usage_agg WHERE user_id IS NULL), 0) AS sessions,
        (SELECT last_seen FROM seen WHERE user_id IS NULL) AS last_seen
    FROM (SELECT 1) d
    WHERE EXISTS (SELECT 1 FROM spans WHERE user_id IS NULL)
),
all_users AS (
    SELECT * FROM named
    UNION ALL
    SELECT * FROM anon
)
SELECT id, name, token, created_at, cost, sessions, last_seen,
       COUNT(*) OVER () AS total
FROM all_users
WHERE TRUE%s
ORDER BY %s %s NULLS LAST, name ASC%s
`, rawSince, aggSince, AnonymousUserID, qFilter, sortExpr, order, limitClause)

	rows, err := db.rw.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []UserWithStats
	var total int
	for rows.Next() {
		var u UserWithStats
		var token sql.NullString
		var createdAt, lastSeen sql.NullTime
		if err := rows.Scan(
			&u.ID, &u.Name, &token, &createdAt,
			&u.TotalCostUSD, &u.Sessions, &lastSeen, &total,
		); err != nil {
			return nil, 0, err
		}
		if token.Valid {
			u.Token = token.String
		}
		if createdAt.Valid {
			u.CreatedAt = createdAt.Time
		}
		if lastSeen.Valid {
			ls := lastSeen.Time
			u.LastSeen = &ls
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// GetUserWithStats returns a single user with range-scoped cost/session stats
// (since = nil means all-time). id may be the __anonymous__ pseudo-user, which
// always resolves to the synthetic row. Returns ErrNotFound for an unknown or
// soft-deleted named user.
func (db *DB) GetUserWithStats(id string, since *time.Time) (UserWithStats, error) {
	if id == AnonymousUserID {
		cost, sessions, lastSeen, err := db.principalStats(true, "", since)
		if err != nil {
			return UserWithStats{}, err
		}
		return UserWithStats{
			User:         User{ID: AnonymousUserID, Name: "Anonymous"},
			TotalCostUSD: cost,
			Sessions:     sessions,
			LastSeen:     lastSeen,
		}, nil
	}

	var u UserWithStats
	err := db.rw.QueryRow(
		`SELECT id, name, token, created_at FROM users WHERE id = ? AND deleted_at IS NULL`, id,
	).Scan(&u.ID, &u.Name, &u.Token, &u.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return UserWithStats{}, ErrNotFound
		}
		return UserWithStats{}, err
	}

	cost, sessions, lastSeen, err := db.principalStats(false, u.Name, since)
	if err != nil {
		return UserWithStats{}, err
	}
	u.TotalCostUSD = cost
	u.Sessions = sessions
	u.LastSeen = lastSeen
	return u, nil
}

// principalStats computes range-scoped cost/sessions plus all-time last_seen for
// one principal: the anonymous bucket (isAnon) or a named user. It reuses the
// same span/daily_usage union as the list query so a single row's numbers match
// the list exactly.
func (db *DB) principalStats(isAnon bool, name string, since *time.Time) (float64, int64, *time.Time, error) {
	rawPrincipal, aggPrincipal := "user_id IS NULL", "du.user_id IS NULL"
	if !isAnon {
		rawPrincipal, aggPrincipal = "user_id = ?", "du.user_id = ?"
	}

	var rawSince, aggSince string
	args := []any{}
	if !isAnon {
		args = append(args, name)
	}
	if since != nil {
		rawSince = " AND start_time >= ?"
		args = append(args, *since)
	}
	if !isAnon {
		args = append(args, name)
	}
	if since != nil {
		aggSince = " AND du.day >= CAST(? AS DATE)"
		args = append(args, *since)
	}

	query := fmt.Sprintf(`
WITH raw_floor AS (SELECT MIN(start_time) AS ts FROM spans),
usage AS (
    SELECT session_id, cost_usd AS cost
    FROM spans
    WHERE %s%s
    UNION ALL
    SELECT du.session_id, du.total_cost_usd AS cost
    FROM daily_usage du CROSS JOIN raw_floor rf
    WHERE %s AND (rf.ts IS NULL OR du.day <= CAST(rf.ts AS DATE))%s
)
SELECT COALESCE(SUM(cost), 0), COUNT(DISTINCT session_id) FROM usage
`, rawPrincipal, rawSince, aggPrincipal, aggSince)

	var cost float64
	var sessions int64
	if err := db.rw.QueryRow(query, args...).Scan(&cost, &sessions); err != nil {
		return 0, 0, nil, err
	}

	seenQuery := `SELECT MAX(end_time) FROM spans WHERE ` + rawPrincipal
	seenArgs := []any{}
	if !isAnon {
		seenArgs = append(seenArgs, name)
	}
	var ls sql.NullTime
	if err := db.rw.QueryRow(seenQuery, seenArgs...).Scan(&ls); err != nil {
		return 0, 0, nil, err
	}
	var lastSeen *time.Time
	if ls.Valid {
		t := ls.Time
		lastSeen = &t
	}
	return cost, sessions, lastSeen, nil
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

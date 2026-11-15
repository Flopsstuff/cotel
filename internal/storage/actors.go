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

// AnonymousActorID is the synthetic ID used to represent spans with no actor_id (unauthenticated ingest).
const AnonymousActorID = "__anonymous__"

// ErrNotFound is returned when the target actor does not exist or is already soft-deleted.
var ErrNotFound = errors.New("not found")

// Actor is a named principal whose token authenticates OTLP ingest requests.
type Actor struct {
	ID        string
	Name      string
	Token     string
	CreatedAt time.Time
	// PaperclipAgentID maps a Paperclip agent to this actor: spans authorized by
	// an ingest key and tagged paperclip.agent.id = this value are attributed
	// here (ADR-0029). Empty when unset.
	PaperclipAgentID string
}

// ActorWithStats adds span-derived metrics to an Actor.
type ActorWithStats struct {
	Actor
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

// CreateActor inserts a new actor with a generated token and returns the full record.
func (db *DB) CreateActor(name string) (Actor, error) {
	token, err := generateToken()
	if err != nil {
		return Actor{}, err
	}
	id := generateID()
	_, err = db.rw.Exec(
		`INSERT INTO actors (id, name, token) VALUES (?, ?, ?)`,
		id, name, token,
	)
	if err != nil {
		return Actor{}, err
	}
	var u Actor
	err = db.rw.QueryRow(
		`SELECT id, name, token, created_at FROM actors WHERE id = ?`, id,
	).Scan(&u.ID, &u.Name, &u.Token, &u.CreatedAt)
	return u, err
}

// ListActorsOptions parameterises ListActorsPage.
type ListActorsOptions struct {
	Since *time.Time // lower bound for range-scoped cost/sessions; nil = all-time
	Query string     // case-insensitive substring match on name; "" = no filter
	Sort  string     // name|created_at|last_seen|cost|sessions (invalid → cost)
	Order string     // asc|desc (invalid → desc)
	Page  int        // 1-based page index (invalid → 1)
	Limit int        // rows per page; 0 = unpaginated; clamped to 500
}

// actorSortExprs whitelists sort keys to their SELECT-list column, so the caller
// can never inject an ORDER BY expression.
var actorSortExprs = map[string]string{
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
// answers range-scoped cost/sessions (ADR-0011). The roll-up snaps its cutoff to
// UTC midnight and consumes whole days, so the earliest surviving raw day
// (raw_floor) is fully raw and daily_usage holds only strictly-earlier days. The
// aggregate side is therefore bounded by `day < raw_floor` (strict): `<=` would
// double count the floor day.
const rangeUsageCTE = `
raw_floor AS (
    SELECT MIN(start_time) AS ts FROM spans
),
usage AS (
    SELECT actor_id, session_id, cost_usd AS cost
    FROM spans
    WHERE TRUE%s
    UNION ALL
    SELECT du.actor_id, du.session_id, du.total_cost_usd AS cost
    FROM daily_usage du CROSS JOIN raw_floor rf
    WHERE (rf.ts IS NULL OR du.day < CAST(CAST(rf.ts AS TIMESTAMP) AS DATE))%s
)`

// ListActorsPage returns a sorted, optionally paged slice of actors with
// range-scoped cost/session stats, plus the total number of matching rows
// (counted before paging). The synthetic __anonymous__ row is produced inside
// the SQL so it sorts, filters, and paginates inline with real actors.
func (db *DB) ListActorsPage(opts ListActorsOptions) ([]ActorWithStats, int, error) {
	sortExpr, ok := actorSortExprs[opts.Sort]
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
	filterArgs := []any{}
	if opts.Since != nil {
		rawSince = " AND start_time >= ?"
		aggSince = " AND du.day >= CAST(CAST(? AS TIMESTAMP) AS DATE)"
		filterArgs = append(filterArgs, *opts.Since, *opts.Since)
	}

	var qFilter string
	if q := strings.TrimSpace(opts.Query); q != "" {
		qFilter = " AND name ILIKE ? ESCAPE '\\'"
		filterArgs = append(filterArgs, "%"+escapeLike(q)+"%")
	}

	limitClause := ""
	args := append([]any{}, filterArgs...)
	if limit > 0 {
		limitClause = " LIMIT ? OFFSET ?"
		args = append(args, limit, (page-1)*limit)
	}

	cte := fmt.Sprintf(actorsPageCTE, rawSince, aggSince, AnonymousActorID)
	query := cte + fmt.Sprintf(`
SELECT id, name, token, created_at, cost, sessions, last_seen, paperclip_agent_id,
       COUNT(*) OVER () AS total
FROM all_actors
WHERE TRUE%s
ORDER BY %s %s NULLS LAST, name ASC%s
`, qFilter, sortExpr, order, limitClause)

	rows, err := db.rw.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []ActorWithStats
	var total int
	for rows.Next() {
		var u ActorWithStats
		var token, paperclipAgentID sql.NullString
		var createdAt, lastSeen sql.NullTime
		if err := rows.Scan(
			&u.ID, &u.Name, &token, &createdAt,
			&u.TotalCostUSD, &u.Sessions, &lastSeen, &paperclipAgentID, &total,
		); err != nil {
			return nil, 0, err
		}
		u.PaperclipAgentID = paperclipAgentID.String
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

	// COUNT(*) OVER () rides on the result rows, so a page past the end yields no
	// row to read it from. Re-count so total stays the match count regardless of page.
	if len(out) == 0 {
		countQuery := cte + fmt.Sprintf("\nSELECT COUNT(*) FROM all_actors WHERE TRUE%s\n", qFilter)
		if err := db.rw.QueryRow(countQuery, filterArgs...).Scan(&total); err != nil {
			return nil, 0, err
		}
	}
	return out, total, nil
}

const actorsPageCTE = `
WITH ` + rangeUsageCTE + `,
usage_agg AS (
    SELECT actor_id,
           COALESCE(SUM(cost), 0) AS cost,
           COUNT(DISTINCT session_id) AS sessions
    FROM usage
    GROUP BY actor_id
),
seen AS (
    SELECT actor_id, MAX(end_time) AS last_seen FROM spans GROUP BY actor_id
),
named AS (
    SELECT u.id, u.name, u.token, u.created_at,
           COALESCE(ua.cost, 0) AS cost,
           COALESCE(ua.sessions, 0) AS sessions,
           s.last_seen AS last_seen,
           u.paperclip_agent_id AS paperclip_agent_id
    FROM actors u
    LEFT JOIN usage_agg ua ON ua.actor_id = u.name
    LEFT JOIN seen s ON s.actor_id = u.name
    WHERE u.deleted_at IS NULL
),
anon AS (
    SELECT
        '%s' AS id,
        'Anonymous' AS name,
        CAST(NULL AS VARCHAR) AS token,
        CAST(NULL AS TIMESTAMPTZ) AS created_at,
        COALESCE((SELECT cost FROM usage_agg WHERE actor_id IS NULL), 0) AS cost,
        COALESCE((SELECT sessions FROM usage_agg WHERE actor_id IS NULL), 0) AS sessions,
        (SELECT last_seen FROM seen WHERE actor_id IS NULL) AS last_seen,
        CAST(NULL AS VARCHAR) AS paperclip_agent_id
    FROM (SELECT 1) d
    WHERE EXISTS (SELECT 1 FROM spans WHERE actor_id IS NULL)
       OR EXISTS (SELECT 1 FROM daily_usage WHERE actor_id IS NULL)
),
all_actors AS (
    SELECT * FROM named
    UNION ALL
    SELECT * FROM anon
)`

// GetActorWithStats returns a single actor with range-scoped cost/session stats
// (since = nil means all-time). id may be the __anonymous__ pseudo-actor, which
// always resolves to the synthetic row. Returns ErrNotFound for an unknown or
// soft-deleted named actor.
func (db *DB) GetActorWithStats(id string, since *time.Time) (ActorWithStats, error) {
	if id == AnonymousActorID {
		cost, sessions, lastSeen, err := db.principalStats(true, "", since)
		if err != nil {
			return ActorWithStats{}, err
		}
		return ActorWithStats{
			Actor:        Actor{ID: AnonymousActorID, Name: "Anonymous"},
			TotalCostUSD: cost,
			Sessions:     sessions,
			LastSeen:     lastSeen,
		}, nil
	}

	var u ActorWithStats
	var paperclipAgentID sql.NullString
	err := db.rw.QueryRow(
		`SELECT id, name, token, created_at, paperclip_agent_id FROM actors WHERE id = ? AND deleted_at IS NULL`, id,
	).Scan(&u.ID, &u.Name, &u.Token, &u.CreatedAt, &paperclipAgentID)
	u.PaperclipAgentID = paperclipAgentID.String
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ActorWithStats{}, ErrNotFound
		}
		return ActorWithStats{}, err
	}

	cost, sessions, lastSeen, err := db.principalStats(false, u.Name, since)
	if err != nil {
		return ActorWithStats{}, err
	}
	u.TotalCostUSD = cost
	u.Sessions = sessions
	u.LastSeen = lastSeen
	return u, nil
}

// principalStats computes range-scoped cost/sessions plus all-time last_seen for
// one principal: the anonymous bucket (isAnon) or a named actor. It reuses the
// same span/daily_usage union as the list query so a single row's numbers match
// the list exactly.
func (db *DB) principalStats(isAnon bool, name string, since *time.Time) (float64, int64, *time.Time, error) {
	rawPrincipal, aggPrincipal := "actor_id IS NULL", "du.actor_id IS NULL"
	if !isAnon {
		rawPrincipal, aggPrincipal = "actor_id = ?", "du.actor_id = ?"
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
		aggSince = " AND du.day >= CAST(CAST(? AS TIMESTAMP) AS DATE)"
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
    WHERE %s AND (rf.ts IS NULL OR du.day < CAST(CAST(rf.ts AS TIMESTAMP) AS DATE))%s
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

// GetActorByToken looks up a non-deleted actor by their plaintext token.
func (db *DB) GetActorByToken(token string) (*Actor, error) {
	var u Actor
	err := db.rw.QueryRow(
		`SELECT id, name, token, created_at FROM actors WHERE token = ? AND deleted_at IS NULL`, token,
	).Scan(&u.ID, &u.Name, &u.Token, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// RotateToken replaces an actor's token with a freshly generated one.
func (db *DB) RotateToken(actorID string) (Actor, error) {
	token, err := generateToken()
	if err != nil {
		return Actor{}, err
	}
	_, err = db.rw.Exec(
		`UPDATE actors SET token = ? WHERE id = ?`, token, actorID,
	)
	if err != nil {
		return Actor{}, err
	}
	var u Actor
	err = db.rw.QueryRow(
		`SELECT id, name, token, created_at FROM actors WHERE id = ?`, actorID,
	).Scan(&u.ID, &u.Name, &u.Token, &u.CreatedAt)
	return u, err
}

// SoftDeleteActor stamps deleted_at, making the actor invisible and their token
// invalid (GetActorByToken filters deleted_at IS NULL). Returns ErrNotFound if
// the actor does not exist or is already soft-deleted.
//
// Note: DuckDB v1.8.x has a bug where UPDATE on a UNIQUE-constrained column
// raises a false "duplicate key" error. We therefore do NOT update the token
// column — the deleted_at filter is sufficient to reject auth for deleted actors.
func (db *DB) SoftDeleteActor(actorID string) error {
	result, err := db.rw.Exec(
		`UPDATE actors SET deleted_at = CURRENT_TIMESTAMP WHERE id = ? AND deleted_at IS NULL`,
		actorID,
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

// DeleteActorWithHistory hard-deletes all spans and daily_usage rows belonging to the
// actor, then hard-deletes the actor row. Returns ErrNotFound if the actor does not
// exist or is already soft-deleted.
//
// Note: DuckDB v1.x fails with "Vector::Reference used on vector of different type"
// when executing UPDATE on spans.actor_id (a column added via ADD COLUMN on a table
// with GENERATED ALWAYS columns). We therefore DELETE span rows rather than
// anonymising them. All history is removed together with the actor.
func (db *DB) DeleteActorWithHistory(actorID string) error {
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
		`SELECT name FROM actors WHERE id = ? AND deleted_at IS NULL`, actorID,
	).Scan(&name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = ErrNotFound
		}
		return err
	}

	if _, err = tx.Exec(`DELETE FROM spans WHERE actor_id = ?`, name); err != nil {
		return err
	}

	if _, err = tx.Exec(`DELETE FROM daily_usage WHERE actor_id = ?`, name); err != nil {
		return err
	}

	if _, err = tx.Exec(`DELETE FROM actors WHERE id = ?`, actorID); err != nil {
		return err
	}

	return tx.Commit()
}

// DeleteAnonymousData hard-deletes all spans and daily_usage rows that have no actor_id (anonymous ingest).
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

	if _, err = tx.Exec(`DELETE FROM spans WHERE actor_id IS NULL`); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM daily_usage WHERE actor_id IS NULL`); err != nil {
		return err
	}
	return tx.Commit()
}

// SetActorPaperclipAgentID maps the Paperclip agent agentID to the actor
// actorID, or clears the mapping when agentID is empty. An agent maps to at most
// one actor, so the id is first cleared from any other actor that holds it - the
// latest assignment wins. Returns ErrNotFound for an unknown or deleted actor.
//
// There is deliberately no UNIQUE constraint on the column: DuckDB has raised
// false duplicate-key errors on UPDATEs of UNIQUE-constrained rows (see
// SoftDeleteActor), so uniqueness is kept here instead, in one transaction.
func (db *DB) SetActorPaperclipAgentID(actorID, agentID string) (err error) {
	tx, err := db.rw.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var n int
	if err = tx.QueryRow(
		`SELECT count(*) FROM actors WHERE id = ? AND deleted_at IS NULL`, actorID,
	).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		err = ErrNotFound
		return err
	}
	if agentID != "" {
		if _, err = tx.Exec(
			`UPDATE actors SET paperclip_agent_id = NULL WHERE paperclip_agent_id = ? AND id <> ?`,
			agentID, actorID,
		); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(
		`UPDATE actors SET paperclip_agent_id = ? WHERE id = ?`, nullableStr(agentID), actorID,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// ActorNameByPaperclipAgentID returns the name of the live actor mapped to the
// Paperclip agent agentID, or "" when none is.
func (db *DB) ActorNameByPaperclipAgentID(agentID string) (string, error) {
	if agentID == "" {
		return "", nil
	}
	var name string
	err := db.rw.QueryRow(
		`SELECT name FROM actors WHERE paperclip_agent_id = ? AND deleted_at IS NULL LIMIT 1`, agentID,
	).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return name, err
}

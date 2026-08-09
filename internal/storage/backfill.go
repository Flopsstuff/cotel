package storage

import (
	"database/sql"
	"fmt"
	"log"

	"github.com/Flopsstuff/cotel/internal/pricing"
)

// BackfillResult summarises the changes made by BackfillCostUSD.
type BackfillResult struct {
	SpansScanned   int64
	SpansUpdated   int64
	DailyUpdated   int64
}

// BackfillCostUSD recalculates cost_usd for every span that has a model and at
// least one token counter, then re-aggregates daily_usage for the same models.
// It uses current (as of backfill run date) pricing rates — not historical ones.
// Running it twice yields the same result (idempotent).
func (db *DB) BackfillCostUSD() (BackfillResult, error) {
	var res BackfillResult

	// --- Phase 1: backfill spans ---
	rows, err := db.rw.Query(`
		SELECT span_id, model,
		       COALESCE(input_tokens, 0),
		       COALESCE(output_tokens, 0),
		       COALESCE(cache_read_tokens, 0),
		       COALESCE(cache_write_tokens, 0)
		FROM spans
		WHERE model IS NOT NULL AND model != ''
	`)
	if err != nil {
		return res, fmt.Errorf("backfill: read spans: %w", err)
	}

	type spanCost struct {
		id   string
		cost float64
	}
	var updates []spanCost
	for rows.Next() {
		var id, model string
		var in, out, cr, cw int64
		if err := rows.Scan(&id, &model, &in, &out, &cr, &cw); err != nil {
			rows.Close()
			return res, fmt.Errorf("backfill: scan span: %w", err)
		}
		res.SpansScanned++
		updates = append(updates, spanCost{id: id, cost: pricing.Compute(model, in, out, cr, cw)})
	}
	if err := rows.Close(); err != nil {
		return res, fmt.Errorf("backfill: close span rows: %w", err)
	}

	tx, err := db.rw.Begin()
	if err != nil {
		return res, fmt.Errorf("backfill: begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	stmt, err := tx.Prepare(`UPDATE spans SET cost_usd = ? WHERE span_id = ?`)
	if err != nil {
		return res, fmt.Errorf("backfill: prepare update: %w", err)
	}
	defer stmt.Close()

	for _, u := range updates {
		r, execErr := stmt.Exec(u.cost, u.id)
		if execErr != nil {
			err = fmt.Errorf("backfill: update span %s: %w", u.id, execErr)
			return res, err
		}
		n, _ := r.RowsAffected()
		res.SpansUpdated += n
	}

	if err = tx.Commit(); err != nil {
		return res, fmt.Errorf("backfill: commit spans: %w", err)
	}
	log.Printf("backfill: updated %d/%d spans", res.SpansUpdated, res.SpansScanned)

	// --- Phase 2: re-aggregate daily_usage from spans still in the raw table ---
	// Spans within the retention window are still present; re-rolling them
	// corrects the cost totals for those days without touching purged history.
	// Excludes spans with NULL session_id/tool_name — the daily_usage PK requires
	// non-NULL values on those columns, matching the same constraint in retention.go.
	_, err = db.rw.Exec(`
		INSERT OR REPLACE INTO daily_usage
		  (day, session_id, model, tool_name, user_id,
		   span_count, total_input_tokens, total_output_tokens, total_cost_usd)
		SELECT
		  strftime(CAST(start_time AS TIMESTAMP), '%Y-%m-%d')::DATE AS day,
		  session_id, model, tool_name,
		  MAX(user_id) AS user_id,
		  COUNT(*),
		  COALESCE(SUM(input_tokens), 0),
		  COALESCE(SUM(output_tokens), 0),
		  COALESCE(SUM(cost_usd), 0)
		FROM spans
		WHERE model IS NOT NULL AND model != ''
		  AND session_id IS NOT NULL
		  AND tool_name IS NOT NULL
		GROUP BY day, session_id, model, tool_name
	`)
	if err != nil {
		return res, fmt.Errorf("backfill: re-aggregate daily_usage from spans: %w", err)
	}

	// --- Phase 3: correct daily_usage rows whose raw spans were already purged ---
	// Those rows have total_input_tokens / total_output_tokens but no cache
	// breakdowns (they were never stored in daily_usage). We recompute from the
	// input+output portion only; cache is a minority of cost and better than wrong.
	dailyRows, err := db.rw.Query(`
		SELECT rowid, model, total_input_tokens, total_output_tokens
		FROM daily_usage
		WHERE model IS NOT NULL AND model != ''
		  AND NOT EXISTS (
		      SELECT 1 FROM spans s
		      WHERE strftime(CAST(s.start_time AS TIMESTAMP), '%Y-%m-%d')::DATE = daily_usage.day
		        AND (s.session_id IS NOT DISTINCT FROM daily_usage.session_id)
		        AND (s.model      IS NOT DISTINCT FROM daily_usage.model)
		        AND (s.tool_name  IS NOT DISTINCT FROM daily_usage.tool_name)
		  )
	`)
	if err != nil {
		return res, fmt.Errorf("backfill: read daily_usage orphan rows: %w", err)
	}

	type dailyUpdate struct {
		rowid int64
		cost  float64
	}
	var dailyUpdates []dailyUpdate
	for dailyRows.Next() {
		var rowid, in, out int64
		var model string
		if err = dailyRows.Scan(&rowid, &model, &in, &out); err != nil {
			dailyRows.Close()
			return res, fmt.Errorf("backfill: scan daily_usage: %w", err)
		}
		// cache tokens not stored — compute input+output cost only
		cost := pricing.Compute(model, in, out, 0, 0)
		dailyUpdates = append(dailyUpdates, dailyUpdate{rowid: rowid, cost: cost})
	}
	if err = dailyRows.Close(); err != nil {
		return res, fmt.Errorf("backfill: close daily_usage rows: %w", err)
	}

	tx2, err := db.rw.Begin()
	if err != nil {
		return res, fmt.Errorf("backfill: begin daily tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx2.Rollback()
		}
	}()

	dstmt, err := tx2.Prepare(`UPDATE daily_usage SET total_cost_usd = ? WHERE rowid = ?`)
	if err != nil {
		return res, fmt.Errorf("backfill: prepare daily update: %w", err)
	}
	defer dstmt.Close()

	for _, u := range dailyUpdates {
		r, execErr := dstmt.Exec(u.cost, u.rowid)
		if execErr != nil {
			err = fmt.Errorf("backfill: update daily rowid %d: %w", u.rowid, execErr)
			return res, err
		}
		n, _ := r.RowsAffected()
		res.DailyUpdated += n
	}

	if err = tx2.Commit(); err != nil {
		return res, fmt.Errorf("backfill: commit daily_usage: %w", err)
	}
	log.Printf("backfill: updated %d daily_usage rows (orphan history)", res.DailyUpdated)

	return res, nil
}

// BackfillModelSummary returns per-model cost totals from spans for reporting.
// Used to print before/after comparisons.
func (db *DB) BackfillModelSummary() ([]ModelCostRow, error) {
	rows, err := db.rw.Query(`
		SELECT model,
		       COUNT(*) AS span_count,
		       COALESCE(SUM(cost_usd), 0) AS total_cost
		FROM spans
		WHERE model IS NOT NULL AND model != ''
		GROUP BY model
		ORDER BY span_count DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModelCostRow
	for rows.Next() {
		var r ModelCostRow
		if err := rows.Scan(&r.Model, &r.SpanCount, &r.TotalCostUSD); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ModelCostRow is one row of a per-model cost summary.
type ModelCostRow struct {
	Model        string
	SpanCount    int64
	TotalCostUSD float64
	// NullSpans is set by the caller when reporting pre-fix state.
	NullSpans sql.NullInt64
}

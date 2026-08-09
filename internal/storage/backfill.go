package storage

import (
	"fmt"
	"log"

	"github.com/Flopsstuff/cotel/internal/pricing"
)

// BackfillReport summarises changes from DryRunBackfill or BackfillCostUSD.
type BackfillReport struct {
	// Known-model spans, grouped by model.
	ModelRows []ModelDelta
	// Spans with a non-empty model value that pricing.Compute does not know — left untouched.
	UnknownModel int64
	// Spans with NULL or empty model — left untouched.
	EmptyModel int64
	// Sum of (newCost - oldCost) across all spans that would be (or were) updated.
	TotalDeltaUSD float64
	// Number of spans where newCost differs from the stored cost_usd.
	SpansToUpdate int64
}

// ModelDelta is one row of a per-model cost breakdown.
type ModelDelta struct {
	Model      string
	SpanCount  int64
	OldCostUSD float64
	NewCostUSD float64
	DeltaUSD   float64
}

// ModelCostRow is one row of a simple per-model cost summary (used by BackfillModelSummary).
type ModelCostRow struct {
	Model        string
	SpanCount    int64
	TotalCostUSD float64
}

// spanRow holds raw data read from the spans table for cost computation.
type spanRow struct {
	id       string
	model    string
	in       int64
	out      int64
	cr       int64
	cw       int64
	oldCost  float64
	isEmpty  bool // model is NULL or ""
}

// readAllSpans fetches every span needed for a backfill (all model/token data).
func (db *DB) readAllSpans() ([]spanRow, error) {
	rows, err := db.rw.Query(`
		SELECT span_id,
		       COALESCE(model, '') AS model,
		       COALESCE(input_tokens, 0),
		       COALESCE(output_tokens, 0),
		       COALESCE(cache_read_tokens, 0),
		       COALESCE(cache_write_tokens, 0),
		       COALESCE(cost_usd, 0)
		FROM spans
	`)
	if err != nil {
		return nil, fmt.Errorf("backfill: read spans: %w", err)
	}
	defer rows.Close()

	var out []spanRow
	for rows.Next() {
		var r spanRow
		if err := rows.Scan(&r.id, &r.model, &r.in, &r.out, &r.cr, &r.cw, &r.oldCost); err != nil {
			return nil, fmt.Errorf("backfill: scan span: %w", err)
		}
		r.isEmpty = r.model == ""
		out = append(out, r)
	}
	return out, rows.Err()
}

// computeReport builds a BackfillReport from raw span data without writing anything.
func computeReport(rows []spanRow) (BackfillReport, []spanRow) {
	type accum struct {
		count      int64
		oldCostUSD float64
		newCostUSD float64
	}
	byModel := map[string]*accum{}

	var toWrite []spanRow
	var rep BackfillReport

	for _, r := range rows {
		if r.isEmpty {
			rep.EmptyModel++
			continue
		}
		newCost := pricing.Compute(r.model, r.in, r.out, r.cr, r.cw)
		if newCost == 0 {
			// Compute returned 0 for a non-empty model → model unknown to pricing table.
			rep.UnknownModel++
			continue
		}
		acc := byModel[r.model]
		if acc == nil {
			acc = &accum{}
			byModel[r.model] = acc
		}
		acc.count++
		acc.oldCostUSD += r.oldCost
		acc.newCostUSD += newCost

		if newCost != r.oldCost {
			rep.SpansToUpdate++
			rep.TotalDeltaUSD += newCost - r.oldCost
			toWrite = append(toWrite, spanRow{id: r.id, model: r.model, oldCost: r.oldCost,
				in: r.in, out: r.out, cr: r.cr, cw: r.cw})
			// Store new cost so caller can use it.
			toWrite[len(toWrite)-1].oldCost = newCost // reuse field as newCost sentinel
		}
	}

	for model, acc := range byModel {
		rep.ModelRows = append(rep.ModelRows, ModelDelta{
			Model:      model,
			SpanCount:  acc.count,
			OldCostUSD: acc.oldCostUSD,
			NewCostUSD: acc.newCostUSD,
			DeltaUSD:   acc.newCostUSD - acc.oldCostUSD,
		})
	}
	return rep, toWrite
}

// DryRunBackfill computes what BackfillCostUSD would do without writing anything.
// It is safe to call while the server is running (read-only).
func (db *DB) DryRunBackfill() (BackfillReport, error) {
	rows, err := db.readAllSpans()
	if err != nil {
		return BackfillReport{}, err
	}
	rep, _ := computeReport(rows)
	return rep, nil
}

// BackfillCostUSD recalculates cost_usd for every span whose model is known to
// pricing.Compute(), then re-aggregates daily_usage. Spans with unknown or
// empty models are left untouched and counted in the returned report.
// Running it twice yields the same result (idempotent).
func (db *DB) BackfillCostUSD() (BackfillReport, error) {
	allRows, err := db.readAllSpans()
	if err != nil {
		return BackfillReport{}, err
	}
	rep, updates := computeReport(allRows)

	// --- Phase 1: update spans ---
	tx, err := db.rw.Begin()
	if err != nil {
		return rep, fmt.Errorf("backfill: begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	stmt, err := tx.Prepare(`UPDATE spans SET cost_usd = ? WHERE span_id = ?`)
	if err != nil {
		return rep, fmt.Errorf("backfill: prepare update: %w", err)
	}
	defer stmt.Close()

	var spansDone int64
	for _, u := range updates {
		// u.oldCost was repurposed above to store newCost. Re-compute to be safe.
		newCost := pricing.Compute(u.model, u.in, u.out, u.cr, u.cw)
		if _, execErr := stmt.Exec(newCost, u.id); execErr != nil {
			err = fmt.Errorf("backfill: update span %s: %w", u.id, execErr)
			return rep, err
		}
		spansDone++
	}

	if err = tx.Commit(); err != nil {
		return rep, fmt.Errorf("backfill: commit spans: %w", err)
	}
	log.Printf("backfill: updated %d spans (unknown=%d empty=%d)",
		spansDone, rep.UnknownModel, rep.EmptyModel)

	// --- Phase 2: re-aggregate daily_usage from spans still in the raw table ---
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
		return rep, fmt.Errorf("backfill: re-aggregate daily_usage from spans: %w", err)
	}

	// --- Phase 3: correct daily_usage rows whose raw spans were already purged ---
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
		return rep, fmt.Errorf("backfill: read daily_usage orphan rows: %w", err)
	}

	type dailyUpdate struct {
		rowid int64
		model string
		in    int64
		out   int64
	}
	var dailyUpdates []dailyUpdate
	for dailyRows.Next() {
		var u dailyUpdate
		if err = dailyRows.Scan(&u.rowid, &u.model, &u.in, &u.out); err != nil {
			dailyRows.Close()
			return rep, fmt.Errorf("backfill: scan daily_usage: %w", err)
		}
		dailyUpdates = append(dailyUpdates, u)
	}
	if err = dailyRows.Close(); err != nil {
		return rep, fmt.Errorf("backfill: close daily_usage rows: %w", err)
	}

	tx2, err := db.rw.Begin()
	if err != nil {
		return rep, fmt.Errorf("backfill: begin daily tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx2.Rollback()
		}
	}()

	dstmt, err := tx2.Prepare(`UPDATE daily_usage SET total_cost_usd = ? WHERE rowid = ?`)
	if err != nil {
		return rep, fmt.Errorf("backfill: prepare daily update: %w", err)
	}
	defer dstmt.Close()

	var dailyDone int64
	for _, u := range dailyUpdates {
		cost := pricing.Compute(u.model, u.in, u.out, 0, 0)
		if cost == 0 {
			continue // unknown model in daily_usage — leave as-is
		}
		if _, execErr := dstmt.Exec(cost, u.rowid); execErr != nil {
			err = fmt.Errorf("backfill: update daily rowid %d: %w", u.rowid, execErr)
			return rep, err
		}
		dailyDone++
	}

	if err = tx2.Commit(); err != nil {
		return rep, fmt.Errorf("backfill: commit daily_usage: %w", err)
	}
	log.Printf("backfill: updated %d daily_usage orphan rows", dailyDone)

	return rep, nil
}

// BackfillModelSummary returns per-model cost totals from spans for reporting.
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

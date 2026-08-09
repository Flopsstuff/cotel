package storage

import (
	"fmt"
	"log"
	"time"

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
	id      string
	model   string
	in      int64
	out     int64
	cr      int64
	cw      int64
	oldCost float64
	newCost float64 // recomputed cost; only meaningful in the toWrite slice
	isEmpty bool    // model is NULL or ""
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
		if !pricing.Known(r.model) {
			// Genuinely unpriced model — leave the stored cost untouched. Classify
			// by Known(), not by Compute() == 0: a priced model with zero billable
			// tokens also computes 0 and must NOT be counted as unknown.
			rep.UnknownModel++
			continue
		}
		newCost := pricing.Compute(r.model, r.in, r.out, r.cr, r.cw)
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
				newCost: newCost, in: r.in, out: r.out, cr: r.cr, cw: r.cw})
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
// pricing.Compute(), then corrects only total_cost_usd on existing daily_usage
// rows (never their counters, never materialising new rows). Spans with unknown
// or empty models are left untouched and counted in the returned report.
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
		if _, execErr := stmt.Exec(u.newCost, u.id); execErr != nil {
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

	// --- Phase 2: correct total_cost_usd on existing daily_usage rows ---
	//
	// This is a cost-only, non-destructive UPDATE. It must NEVER touch span_count
	// or the token totals, and must NEVER materialise new rows: daily_usage is
	// created solely by the retention roll-up, and re-deriving it from the raw
	// spans here would (a) clobber span_count/token totals via INSERT OR REPLACE,
	// (b) prematurely materialise ~RawDays of aggregates that /api/v1/export must
	// not yet contain, and (c) corrupt the roll-up boundary day (partly rolled up,
	// partly raw) by overwriting its aggregate with only the surviving raw slice.
	//
	// So we recompute each existing row's cost from its OWN stored token totals and
	// update only total_cost_usd, keyed by the real primary key. Rolled-up rows no
	// longer have their raw spans, so cache tokens are unavailable — daily aggregate
	// cost is therefore input+output only (a small, documented approximation). This
	// is idempotent: the cost is recomputed from stored tokens, not scaled.
	if err = db.backfillDailyUsageCost(); err != nil {
		return rep, err
	}

	return rep, nil
}

// dailyCostRow is one existing daily_usage row keyed by its full primary key.
type dailyCostRow struct {
	day       time.Time
	sessionID *string
	model     string
	toolName  *string
	in        int64
	out       int64
}

// backfillDailyUsageCost recomputes total_cost_usd for every existing known-model
// daily_usage row from its stored token totals, updating only that one column.
func (db *DB) backfillDailyUsageCost() error {
	rows, err := db.rw.Query(`
		SELECT day, session_id, model, tool_name,
		       COALESCE(total_input_tokens, 0),
		       COALESCE(total_output_tokens, 0)
		FROM daily_usage
		WHERE model IS NOT NULL AND model != ''
	`)
	if err != nil {
		return fmt.Errorf("backfill: read daily_usage rows: %w", err)
	}
	var updates []dailyCostRow
	for rows.Next() {
		var r dailyCostRow
		if err := rows.Scan(&r.day, &r.sessionID, &r.model, &r.toolName, &r.in, &r.out); err != nil {
			rows.Close()
			return fmt.Errorf("backfill: scan daily_usage: %w", err)
		}
		updates = append(updates, r)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("backfill: close daily_usage rows: %w", err)
	}

	tx, err := db.rw.Begin()
	if err != nil {
		return fmt.Errorf("backfill: begin daily tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Match on the full primary key. IS NOT DISTINCT FROM handles NULL session_id /
	// tool_name so a NULL-keyed row updates exactly itself and no other.
	stmt, err := tx.Prepare(`
		UPDATE daily_usage SET total_cost_usd = ?
		WHERE day = ?
		  AND session_id IS NOT DISTINCT FROM ?
		  AND model IS NOT DISTINCT FROM ?
		  AND tool_name IS NOT DISTINCT FROM ?`)
	if err != nil {
		return fmt.Errorf("backfill: prepare daily update: %w", err)
	}
	defer stmt.Close()

	var done int64
	for _, u := range updates {
		if !pricing.Known(u.model) {
			continue // unknown model — leave stored cost untouched
		}
		// daily_usage does not persist cache tokens; cost is input+output only.
		cost := pricing.Compute(u.model, u.in, u.out, 0, 0)
		if _, execErr := stmt.Exec(cost, u.day, u.sessionID, u.model, u.toolName); execErr != nil {
			return fmt.Errorf("backfill: update daily_usage row: %w", execErr)
		}
		done++
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("backfill: commit daily_usage: %w", err)
	}
	committed = true
	log.Printf("backfill: recomputed total_cost_usd on %d daily_usage rows", done)
	return nil
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

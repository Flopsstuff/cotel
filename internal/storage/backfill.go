package storage

import (
	"fmt"
	"log"
	"sort"

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
	// Stable order: map iteration is random, and the operator's main check is
	// diffing two consecutive dry-runs to confirm idempotence.
	sort.Slice(rep.ModelRows, func(i, j int) bool {
		if rep.ModelRows[i].SpanCount != rep.ModelRows[j].SpanCount {
			return rep.ModelRows[i].SpanCount > rep.ModelRows[j].SpanCount
		}
		return rep.ModelRows[i].Model < rep.ModelRows[j].Model
	})
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
// pricing.Compute(). Spans with unknown or empty models are left untouched and
// counted in the returned report. Running it twice yields the same result
// (idempotent).
//
// It deliberately does NOT touch daily_usage. Roll-up derives total_cost_usd as
// SUM(spans.cost_usd), so any day rolled up after this backfill is correct by
// construction. Repairing an already-rolled-up row is impossible anyway:
// daily_usage persists only input/output token totals, and on real data cache
// tokens are ~156x that volume, so a recompute from the stored counters would
// recover only a small fraction of the true cost — and would silently lower a
// correct row on re-run. Making aggregates recomputable needs cache-token
// columns on daily_usage; that is tracked separately, not bolted onto a
// backfill. See FLO-552 review.
func (db *DB) BackfillCostUSD() (BackfillReport, error) {
	allRows, err := db.readAllSpans()
	if err != nil {
		return BackfillReport{}, err
	}
	rep, updates := computeReport(allRows)

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

	return rep, nil
}

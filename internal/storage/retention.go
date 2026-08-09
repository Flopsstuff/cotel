package storage

import (
	"log"
	"time"
)

// RetentionConfig controls raw-span and aggregate lifetimes.
type RetentionConfig struct {
	RawDays       int // delete raw spans older than this (default 30)
	AggregateDays int // delete daily_usage rows older than this (default 90)
}

// DefaultRetention is the shipped-default policy.
var DefaultRetention = RetentionConfig{
	RawDays:       30,
	AggregateDays: 90,
}

// UnknownSentinel is written into the daily_usage primary-key columns
// (session_id, model, tool_name) when the source span carries no value for
// them — i.e. the column is SQL NULL or the empty string. The daily_usage PK
// (day, session_id, model, tool_name) is implicitly NOT NULL in DuckDB, so a
// NULL PK component would abort the whole roll-up with
// "NOT NULL constraint failed". Normalising at this boundary keeps the row
// instead of losing it, and makes the volume of unattributed usage countable
// (e.g. `SELECT SUM(span_count) FROM daily_usage WHERE model = 'unknown'`).
//
// This is the shared "span without a model" representation agreed with the
// cost-recalculation work: such a span is reported as *uncovered*,
// never priced as zero. Raw spans are left untouched (they keep NULL/'') — the
// sentinel exists only in the rollup so the dashboard's raw-span views are
// unaffected.
const UnknownSentinel = "unknown"

// Setting keys used to surface retention-worker health on /api/v1/health.
const (
	settingRetentionStatus = "retention_last_status" // "ok" | "error"
	settingRetentionError  = "retention_last_error"  // last error text, "" on success
	settingRetentionRunAt  = "retention_last_run_at" // RFC3339 timestamp of last attempt
)

// RunRetentionWorker rolls up raw spans into daily_usage then purges old data.
// Runs immediately, then every interval (suggest 6h in production).
// Idempotent: safe to re-run after a crash mid-roll-up.
//
// A failed cycle is not silent: it is logged at ERROR level with the
// consecutive-failure count and recorded on /api/v1/health (status "degraded").
// The loop keeps running so a transient failure self-heals on the next tick.
func (db *DB) RunRetentionWorker(cfg RetentionConfig, interval time.Duration) {
	consecutiveFailures := 0
	for {
		err := db.RollupAndPurge(cfg)
		db.recordRetentionRun(err)
		if err != nil {
			consecutiveFailures++
			log.Printf("ERROR retention worker: roll-up/purge failed (consecutive failures=%d, raw_days=%d, aggregate_days=%d): %v",
				consecutiveFailures, cfg.RawDays, cfg.AggregateDays, err)
		} else {
			consecutiveFailures = 0
		}
		time.Sleep(interval)
	}
}

// recordRetentionRun persists the outcome of one retention cycle so the health
// endpoint can report degradation. Best-effort: a failure to write status must
// not take down the worker, so write errors are only logged.
func (db *DB) recordRetentionRun(runErr error) {
	status := "ok"
	msg := ""
	if runErr != nil {
		status = "error"
		msg = runErr.Error()
	}
	for k, v := range map[string]string{
		settingRetentionStatus: status,
		settingRetentionError:  msg,
		settingRetentionRunAt:  time.Now().UTC().Format(time.RFC3339),
	} {
		if err := db.SetSetting(k, v); err != nil {
			log.Printf("ERROR retention worker: failed to record %s: %v", k, err)
		}
	}
}

// RollupAndPurge rolls up raw spans older than cfg.RawDays into daily_usage,
// then deletes the raw rows and any aggregate rows older than cfg.AggregateDays.
func (db *DB) RollupAndPurge(cfg RetentionConfig) error {
	// Roll raw spans older than RawDays into daily_usage before deleting.
	//
	// session_id / model / tool_name are the PK columns and NOT NULL in
	// daily_usage, but nullable (and often empty) in spans. COALESCE(NULLIF(…))
	// maps both NULL and '' to UnknownSentinel so the roll-up never aborts on a
	// NOT NULL violation. GROUP BY uses the SELECT-list positions (1..4) so the
	// normalised values drive the grouping — '' and NULL collapse into one
	// 'unknown' bucket rather than two.
	rollupCutoff := time.Now().AddDate(0, 0, -cfg.RawDays)
	_, err := db.rw.Exec(`
		INSERT OR REPLACE INTO daily_usage
		  (day, session_id, model, tool_name, user_id,
		   span_count, total_input_tokens, total_output_tokens,
		   total_cache_read_tokens, total_cache_write_tokens, total_cost_usd)
		SELECT
		  strftime(CAST(start_time AS TIMESTAMP), '%Y-%m-%d')::DATE AS day,
		  COALESCE(NULLIF(session_id, ''), ?) AS session_id,
		  COALESCE(NULLIF(model, ''), ?)      AS model,
		  COALESCE(NULLIF(tool_name, ''), ?)  AS tool_name,
		  MAX(user_id) AS user_id,
		  COUNT(*) AS span_count,
		  COALESCE(SUM(input_tokens), 0),
		  COALESCE(SUM(output_tokens), 0),
		  COALESCE(SUM(cache_read_tokens), 0),
		  COALESCE(SUM(cache_write_tokens), 0),
		  COALESCE(SUM(cost_usd), 0)
		FROM spans
		WHERE start_time < ?
		GROUP BY 1, 2, 3, 4
	`, UnknownSentinel, UnknownSentinel, UnknownSentinel, rollupCutoff)
	if err != nil {
		return err
	}

	// Purge raw spans past RawDays.
	if _, err := db.rw.Exec(`DELETE FROM spans WHERE start_time < ?`, rollupCutoff); err != nil {
		return err
	}

	// Purge aggregates past AggregateDays.
	aggCutoff := time.Now().AddDate(0, 0, -cfg.AggregateDays)
	if _, err := db.rw.Exec(`DELETE FROM daily_usage WHERE day < ?`, aggCutoff); err != nil {
		return err
	}

	log.Printf("retention: rolled up spans before %s, purged aggregates before %s",
		rollupCutoff.Format("2006-01-02"), aggCutoff.Format("2006-01-02"))
	return nil
}

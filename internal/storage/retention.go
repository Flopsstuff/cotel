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

// RunRetentionWorker rolls up raw spans into daily_usage then purges old data.
// Runs immediately, then every interval (suggest 6h in production).
// Idempotent: safe to re-run after a crash mid-roll-up.
func (db *DB) RunRetentionWorker(cfg RetentionConfig, interval time.Duration) {
	for {
		if err := db.RollupAndPurge(cfg); err != nil {
			log.Printf("retention worker error: %v", err)
		}
		time.Sleep(interval)
	}
}

// RollupAndPurge rolls up raw spans older than cfg.RawDays into daily_usage,
// then deletes the raw rows and any aggregate rows older than cfg.AggregateDays.
func (db *DB) RollupAndPurge(cfg RetentionConfig) error {
	// Roll raw spans older than RawDays into daily_usage before deleting.
	rollupCutoff := time.Now().AddDate(0, 0, -cfg.RawDays)
	_, err := db.rw.Exec(`
		INSERT OR REPLACE INTO daily_usage
		  (day, session_id, model, tool_name,
		   span_count, total_input_tokens, total_output_tokens, total_cost_usd)
		SELECT
		  CAST(start_time AS DATE) AS day,
		  session_id, model, tool_name,
		  COUNT(*) AS span_count,
		  COALESCE(SUM(input_tokens), 0),
		  COALESCE(SUM(output_tokens), 0),
		  COALESCE(SUM(cost_usd), 0)
		FROM spans
		WHERE start_time < ?
		GROUP BY day, session_id, model, tool_name
	`, rollupCutoff)
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

package storage

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
)

// PurgeReport summarises PurgeSpans: how many stored spans matched, by service
// and span name.
type PurgeReport struct {
	Rows  []PurgeRow
	Total int64
}

// PurgeRow is one service and span name with the number of spans matched.
type PurgeRow struct {
	Service string
	Name    string
	Spans   int64
}

// PurgeSpans deletes the stored spans that drop matches, among spans with no
// session; with apply=false it only reports what it would delete. A span with a
// session is never a candidate, whatever drop says. drop receives the span's
// service name, span name and attributes decoded from their stored JSON; a span
// whose attributes do not decode is kept.
//
// The caller passes ingest's rule (ingest.WouldDrop), so the purge removes
// exactly what ingest would no longer store: storage cannot import ingest.
func (db *DB) PurgeSpans(drop func(svc, name string, attrs map[string]any) bool, apply bool) (PurgeReport, error) {
	rows, err := db.rw.Query(`
		SELECT span_id, COALESCE(service_name, ''), name, COALESCE(CAST(attributes AS VARCHAR), '')
		FROM spans
		WHERE COALESCE(session_id, '') = ''`)
	if err != nil {
		return PurgeReport{}, fmt.Errorf("purge: read spans: %w", err)
	}
	type key struct{ svc, name string }
	counts := map[key]int64{}
	var ids []string
	for rows.Next() {
		var id, svc, name, attrsJSON string
		if err := rows.Scan(&id, &svc, &name, &attrsJSON); err != nil {
			rows.Close()
			return PurgeReport{}, fmt.Errorf("purge: scan span: %w", err)
		}
		attrs := map[string]any{}
		if attrsJSON != "" {
			if err := json.Unmarshal([]byte(attrsJSON), &attrs); err != nil {
				continue
			}
		}
		if drop(svc, name, attrs) {
			counts[key{svc, name}]++
			ids = append(ids, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return PurgeReport{}, fmt.Errorf("purge: read spans: %w", err)
	}

	rep := PurgeReport{Total: int64(len(ids))}
	for k, n := range counts {
		rep.Rows = append(rep.Rows, PurgeRow{Service: k.svc, Name: k.name, Spans: n})
	}
	sort.Slice(rep.Rows, func(i, j int) bool {
		if rep.Rows[i].Spans != rep.Rows[j].Spans {
			return rep.Rows[i].Spans > rep.Rows[j].Spans
		}
		return rep.Rows[i].Service+rep.Rows[i].Name < rep.Rows[j].Service+rep.Rows[j].Name
	})
	if !apply || len(ids) == 0 {
		return rep, nil
	}
	if err := db.deleteSpanIDs(ids); err != nil {
		return rep, err
	}
	log.Printf("purge: deleted %d spans", len(ids))
	return rep, nil
}

// deleteSpanIDs deletes the spans with the given ids in one transaction. The ids
// go through a temporary table so the delete is one statement, not one per span.
func (db *DB) deleteSpanIDs(ids []string) (err error) {
	tx, err := db.rw.Begin()
	if err != nil {
		return fmt.Errorf("purge: begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.Exec(`CREATE TEMP TABLE purge_ids (span_id VARCHAR)`); err != nil {
		return fmt.Errorf("purge: create temp table: %w", err)
	}
	stmt, err := tx.Prepare(`INSERT INTO purge_ids VALUES (?)`)
	if err != nil {
		return fmt.Errorf("purge: prepare insert: %w", err)
	}
	for _, id := range ids {
		if _, err = stmt.Exec(id); err != nil {
			stmt.Close()
			return fmt.Errorf("purge: stage span %s: %w", id, err)
		}
	}
	stmt.Close()
	if _, err = tx.Exec(`DELETE FROM spans WHERE span_id IN (SELECT span_id FROM purge_ids)`); err != nil {
		return fmt.Errorf("purge: delete spans: %w", err)
	}
	if _, err = tx.Exec(`DROP TABLE purge_ids`); err != nil {
		return fmt.Errorf("purge: drop temp table: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("purge: commit: %w", err)
	}
	return nil
}

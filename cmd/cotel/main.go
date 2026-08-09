package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Flopsstuff/cotel/internal/api"
	"github.com/Flopsstuff/cotel/internal/api/auth"
	"github.com/Flopsstuff/cotel/internal/dashboard"
	"github.com/Flopsstuff/cotel/internal/export"
	"github.com/Flopsstuff/cotel/internal/importpkg"
	"github.com/Flopsstuff/cotel/internal/ingest"
	"github.com/Flopsstuff/cotel/internal/storage"
)

func main() {
	dbQuery := flag.String("db-query", "", "run SQL query against DuckDB, print first column of first row, and exit")
	backfillCost := flag.Bool("backfill-cost", false, "dry-run: show what cost_usd recalculation would change (no writes)")
	backfillCostApply := flag.Bool("backfill-cost-apply", false, "apply the cost_usd recalculation to all historical spans, then exit")
	flag.Parse()

	dbPath := env("COTEL_DB_PATH", "/data/cotel.duckdb")

	if *dbQuery != "" {
		ro, err := storage.OpenReadOnly(dbPath)
		if err != nil {
			log.Fatalf("open storage: %v", err)
		}
		defer ro.Close()
		var val interface{}
		if err := ro.QueryRow(*dbQuery).Scan(&val); err != nil {
			log.Fatalf("db-query: %v", err)
		}
		fmt.Println(val)
		return
	}

	if *backfillCost || *backfillCostApply {
		db, err := storage.Open(dbPath)
		if err != nil {
			log.Fatalf("open storage: %v", err)
		}
		defer db.Close()

		if *backfillCostApply {
			fmt.Println("=== APPLYING cost_usd backfill ===")
			rep, err := db.BackfillCostUSD()
			if err != nil {
				log.Fatalf("backfill: %v", err)
			}
			printBackfillReport(rep, true)
		} else {
			fmt.Println("=== DRY RUN (no writes) — pass --backfill-cost-apply to apply ===")
			rep, err := db.DryRunBackfill()
			if err != nil {
				log.Fatalf("backfill dry-run: %v", err)
			}
			printBackfillReport(rep, false)
		}
		return
	}

	db, err := storage.Open(dbPath)
	if err != nil {
		log.Fatalf("open storage: %v", err)
	}
	defer db.Close()

	retentionCfg := storage.RetentionConfig{
		RawDays:       envInt("COTEL_RETENTION_RAW_DAYS", storage.DefaultRetention.RawDays),
		AggregateDays: envInt("COTEL_RETENTION_AGGREGATE_DAYS", storage.DefaultRetention.AggregateDays),
	}
	retentionInterval := envDuration("COTEL_RETENTION_INTERVAL", 6*time.Hour)
	go db.RunRetentionWorker(retentionCfg, retentionInterval)

	ingestMux := http.NewServeMux()
	ingestMux.Handle("/v1/traces", auth.Middleware(db, ingest.New(db)))

	ro := db.ReadOnly()
	apiHandler := api.New(ro).SetPublicIngestURL(parsePublicIngestURL(os.Getenv("COTEL_PUBLIC_INGEST_URL"))).SetUserStore(db)
	dashMux := http.NewServeMux()
	dashMux.Handle("/api/v1/export", auth.Middleware(db, export.NewHandler(db)))
	dashMux.Handle("/api/v1/import", auth.Middleware(db, importpkg.NewHandler(db)))
	dashMux.Handle("/api/v1/", apiHandler)
	dashMux.Handle("/", dashboard.New(ro))

	ingestAddr := env("COTEL_INGEST_ADDR", ":4318")
	dashAddr := env("COTEL_DASH_ADDR", ":8080")

	go func() {
		log.Printf("ingest listening on %s", ingestAddr)
		if err := http.ListenAndServe(ingestAddr, ingestMux); err != nil {
			log.Fatalf("ingest: %v", err)
		}
	}()

	log.Printf("dashboard listening on %s", dashAddr)
	if err := http.ListenAndServe(dashAddr, dashMux); err != nil {
		log.Fatalf("dashboard: %v", err)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
		log.Printf("warning: invalid %s=%q, using default %d", key, v, fallback)
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		log.Printf("warning: invalid %s=%q, using default %s", key, v, fallback)
	}
	return fallback
}

// printBackfillReport prints a BackfillReport to stdout.
func printBackfillReport(rep storage.BackfillReport, applied bool) {
	verb := "would change"
	if applied {
		verb = "changed"
	}
	fmt.Printf("\n%-35s  %8s  %12s  %12s  %12s\n",
		"model", "spans", "old_cost_usd", "new_cost_usd", "delta_usd")
	fmt.Println(strings.Repeat("-", 82))
	for _, row := range rep.ModelRows {
		fmt.Printf("%-35s  %8d  %12.4f  %12.4f  %+12.4f\n",
			row.Model, row.SpanCount, row.OldCostUSD, row.NewCostUSD, row.DeltaUSD)
	}
	fmt.Println(strings.Repeat("-", 82))
	fmt.Printf("total delta_usd: %+.4f  (spans that %s: %d)\n",
		rep.TotalDeltaUSD, verb, rep.SpansToUpdate)
	if rep.UnknownModel > 0 {
		fmt.Printf("unknown model (skipped): %d spans\n", rep.UnknownModel)
	}
	if rep.EmptyModel > 0 {
		fmt.Printf("empty/null model (skipped): %d spans\n", rep.EmptyModel)
	}
	fmt.Println()
}

// parsePublicIngestURL validates raw as an absolute http/https URL.
// Returns the trimmed URL on success, or "" with a logged warning if invalid.
func parsePublicIngestURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		log.Printf("warning: COTEL_PUBLIC_INGEST_URL=%q is not a valid URL, falling back to localhost", raw)
		return ""
	}
	return strings.TrimRight(raw, "/")
}

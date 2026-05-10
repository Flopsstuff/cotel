package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Flopsstuff/cotel/internal/api"
	"github.com/Flopsstuff/cotel/internal/dashboard"
	"github.com/Flopsstuff/cotel/internal/export"
	"github.com/Flopsstuff/cotel/internal/ingest"
	"github.com/Flopsstuff/cotel/internal/storage"
)

func main() {
	dbQuery := flag.String("db-query", "", "run SQL query against DuckDB, print first column of first row, and exit")
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
	ingestMux.Handle("/v1/traces", otlpAuth(db, ingest.New(db)))

	ro := db.ReadOnly()
	apiHandler := api.New(ro).SetTokenStore(db)
	dashMux := http.NewServeMux()
	dashMux.Handle("/api/v1/export", export.NewHandler(db))
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

// otlpAuth guards the OTLP ingest endpoint.
// When no tokens are stored (local mode) all requests pass through.
// Once any token exists, requests must supply a valid Bearer cotel_... token.
func otlpAuth(db *storage.DB, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if strings.HasPrefix(bearer, "cotel_") {
			h := sha256.Sum256([]byte(bearer))
			if db.ValidateToken(hex.EncodeToString(h[:])) {
				next.ServeHTTP(w, r)
				return
			}
		}
		// No valid token presented — pass through only in local mode (0 tokens configured).
		n, _ := db.CountTokens()
		if n == 0 {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	})
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

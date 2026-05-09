package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/Flopsstuff/cotel/internal/dashboard"
	"github.com/Flopsstuff/cotel/internal/ingest"
	"github.com/Flopsstuff/cotel/internal/storage"
)

func main() {
	dbPath := env("COTEL_DB_PATH", "/data/cotel.duckdb")

	db, err := storage.Open(dbPath)
	if err != nil {
		log.Fatalf("open storage: %v", err)
	}
	defer db.Close()

	// Retention worker: roll up + purge every 6 hours.
	go db.RunRetentionWorker(storage.DefaultRetention, 6*time.Hour)

	ingestMux := http.NewServeMux()
	ingestMux.Handle("/v1/traces", ingest.New(db))

	dashMux := http.NewServeMux()
	dashMux.Handle("/", dashboard.New(db.ReadOnly()))

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

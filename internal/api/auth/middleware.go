// Package auth provides shared HTTP auth middleware for the cotel API endpoints.
package auth

import (
	"log"
	"net/http"
	"strings"

	"github.com/Flopsstuff/cotel/internal/ingest"
	"github.com/Flopsstuff/cotel/internal/storage"
)

// AuthDB is the database interface required by Middleware.
// *storage.DB satisfies this interface.
type AuthDB interface {
	GetActorByToken(token string) (*storage.Actor, error)
	GetSettingBool(key string, defaultVal bool) bool
}

// Middleware enforces the allow_anonymous auth policy shared across all cotel endpoints.
//
// Rules:
//   - valid cotel_* bearer → identify the actor (injected into ctx), allow
//   - no bearer + allow_anonymous=true → allow anonymously
//   - no bearer + allow_anonymous=false → 401
//   - invalid/unknown bearer → 401
func Middleware(db AuthDB, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if strings.HasPrefix(bearer, "cotel_") {
			actor, err := db.GetActorByToken(bearer)
			if err == nil && actor != nil {
				ctx := ingest.WithActorName(r.Context(), actor.Name)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			unauthorized(w)
			return
		}
		if db.GetSettingBool("allow_anonymous", true) {
			next.ServeHTTP(w, r)
			return
		}
		unauthorized(w)
	})
}

// ActorTokenHeader carries an actor token next to an ingest key, so a fleet that
// shares one Authorization header can still attribute each request.
const ActorTokenHeader = "X-Cotel-Actor-Token"

// IngestAuthDB is the database interface required by IngestMiddleware.
// *storage.DB satisfies this interface.
type IngestAuthDB interface {
	AuthDB
	IsIngestKey(token string) (bool, error)
}

// IngestMiddleware is Middleware plus shared ingest keys, for the OTLP ingest
// endpoints only. An ingest key authorizes a request but does not identify it;
// identity comes from an actor token in the X-Cotel-Actor-Token header.
//
// Rules:
//   - valid cotel_ik_* bearer → allow; attribute to the actor whose token is in
//     X-Cotel-Actor-Token, or anonymously when that header is absent or unknown
//     (even with allow_anonymous=false, since the key authorized the request)
//   - unknown cotel_ik_* bearer, or a database error checking it → 401
//   - any other bearer, or none → exactly Middleware; X-Cotel-Actor-Token is
//     ignored, so it never identifies a request on its own
//
// It never writes to the database: ingest is high frequency and DuckDB has a
// single writer.
func IngestMiddleware(db IngestAuthDB, next http.Handler) http.Handler {
	fallback := Middleware(db, next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !strings.HasPrefix(bearer, storage.IngestKeyPrefix) {
			fallback.ServeHTTP(w, r)
			return
		}
		ok, err := db.IsIngestKey(bearer)
		if err != nil {
			log.Printf("auth: ingest key lookup failed: %v", err)
			unauthorized(w)
			return
		}
		if !ok {
			unauthorized(w)
			return
		}
		ctx := ingest.WithIngestKeyAuth(r.Context())
		if token := strings.TrimSpace(r.Header.Get(ActorTokenHeader)); token != "" {
			if actor, err := db.GetActorByToken(token); err == nil && actor != nil {
				ctx = ingest.WithActorName(ctx, actor.Name)
			}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
}

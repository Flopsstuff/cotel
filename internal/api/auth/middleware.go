// Package auth provides shared HTTP auth middleware for the cotel API endpoints.
package auth

import (
	"net/http"
	"strings"

	"github.com/Flopsstuff/cotel/internal/ingest"
	"github.com/Flopsstuff/cotel/internal/storage"
)

// AuthDB is the database interface required by Middleware.
// *storage.DB satisfies this interface.
type AuthDB interface {
	GetUserByToken(token string) (*storage.User, error)
	GetSettingBool(key string, defaultVal bool) bool
}

// Middleware enforces the allow_anonymous auth policy shared across all cotel endpoints.
//
// Rules:
//   - valid cotel_* bearer → identify the user (injected into ctx), allow
//   - no bearer + allow_anonymous=true → allow anonymously
//   - no bearer + allow_anonymous=false → 401
//   - invalid/unknown bearer → 401
func Middleware(db AuthDB, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if strings.HasPrefix(bearer, "cotel_") {
			user, err := db.GetUserByToken(bearer)
			if err == nil && user != nil {
				ctx := ingest.WithUserName(r.Context(), user.Name)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		if db.GetSettingBool("allow_anonymous", true) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	})
}

package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Flopsstuff/cotel/internal/storage"
)

// TokenStore is implemented by *storage.DB for token CRUD.
type TokenStore interface {
	ListTokens() ([]storage.TokenRow, error)
	CreateToken(id, name, hash, prefix string) error
	DeleteToken(id string) error
}

// tokenListItem is the per-token shape returned by GET /tokens (no plaintext).
type tokenListItem struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	CreatedAt  string  `json:"created_at"`
	LastUsedAt *string `json:"last_used_at"`
}

type tokensListResponse struct {
	Items []tokenListItem `json:"items"`
}

// createTokenResponse is returned once on POST /tokens and POST /tokens/{id}/rotate.
type createTokenResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Prefix    string `json:"prefix"`
	Token     string `json:"token"` // plaintext, returned once only
	CreatedAt string `json:"created_at"`
}

// ---- helpers ----

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// newToken generates a fresh cotel_ token and returns plaintext, SHA-256 hex hash, and display prefix.
func newToken() (plaintext, hash, prefix string, err error) {
	var b [32]byte
	if _, err = rand.Read(b[:]); err != nil {
		return
	}
	plaintext = "cotel_" + hex.EncodeToString(b[:]) // 70 chars
	prefix = plaintext[:8]                           // "cotel_XX"
	h := sha256.Sum256([]byte(plaintext))
	hash = hex.EncodeToString(h[:])
	return
}

// ---- route dispatch ----

// handleTokens dispatches GET (list) and POST (create) on /api/v1/tokens.
func (h *Handler) handleTokens(w http.ResponseWriter, r *http.Request) {
	if h.tokenDB == nil {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.listTokens(w, r)
	case http.MethodPost:
		h.createToken(w, r)
	default:
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleTokenByID dispatches DELETE on /api/v1/tokens/{id}.
func (h *Handler) handleTokenByID(w http.ResponseWriter, r *http.Request, id string) {
	if h.tokenDB == nil {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}
	if id == "" {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodDelete {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := h.tokenDB.DeleteToken(id); err != nil {
		jsonError(w, "delete failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleTokenRotate handles POST /api/v1/tokens/{id}/rotate.
func (h *Handler) handleTokenRotate(w http.ResponseWriter, r *http.Request, id string) {
	if h.tokenDB == nil {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tokens, err := h.tokenDB.ListTokens()
	if err != nil {
		jsonError(w, "list failed", http.StatusInternalServerError)
		return
	}
	var existing *storage.TokenRow
	for i := range tokens {
		if tokens[i].ID == id {
			existing = &tokens[i]
			break
		}
	}
	if existing == nil {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}

	if err := h.tokenDB.DeleteToken(id); err != nil {
		jsonError(w, "revoke failed", http.StatusInternalServerError)
		return
	}

	newID := newUUID()
	plaintext, hash, prefix, err := newToken()
	if err != nil {
		jsonError(w, "token generation failed", http.StatusInternalServerError)
		return
	}
	if err := h.tokenDB.CreateToken(newID, existing.Name, hash, prefix); err != nil {
		jsonError(w, "create failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(createTokenResponse{
		ID:        newID,
		Name:      existing.Name,
		Prefix:    prefix,
		Token:     plaintext,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
}

// ---- handlers ----

func (h *Handler) listTokens(w http.ResponseWriter, _ *http.Request) {
	rows, err := h.tokenDB.ListTokens()
	if err != nil {
		jsonError(w, "list failed", http.StatusInternalServerError)
		return
	}
	items := make([]tokenListItem, 0, len(rows))
	for _, t := range rows {
		item := tokenListItem{
			ID:        t.ID,
			Name:      t.Name,
			Prefix:    t.Prefix,
			CreatedAt: t.CreatedAt.UTC().Format(time.RFC3339),
		}
		if t.LastUsedAt != nil {
			s := t.LastUsedAt.UTC().Format(time.RFC3339)
			item.LastUsedAt = &s
		}
		items = append(items, item)
	}
	jsonOK(w, tokensListResponse{Items: items})
}

func (h *Handler) createToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		jsonError(w, "name required", http.StatusBadRequest)
		return
	}

	id := newUUID()
	plaintext, hash, prefix, err := newToken()
	if err != nil {
		jsonError(w, "token generation failed", http.StatusInternalServerError)
		return
	}
	if err := h.tokenDB.CreateToken(id, req.Name, hash, prefix); err != nil {
		jsonError(w, "create failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(createTokenResponse{
		ID:        id,
		Name:      req.Name,
		Prefix:    prefix,
		Token:     plaintext,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
}

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Flopsstuff/cotel/internal/storage"
)

// maxIngestKeyName bounds an ingest key's display name, in characters.
const maxIngestKeyName = 100

// IngestKeyStore is the write-capable interface used by the ingest key endpoints.
type IngestKeyStore interface {
	CreateIngestKey(name string) (storage.IngestKey, error)
	ListIngestKeys() ([]storage.IngestKey, error)
	DeleteIngestKey(id string) error
}

// SetIngestKeyStore attaches a writable store for the /ingest-keys endpoints.
func (h *Handler) SetIngestKeyStore(s IngestKeyStore) *Handler {
	h.ingestKeyStore = s
	return h
}

// ingestKeyItem is one key as the list and create endpoints return it. The
// token is included, like an actor's: keys are retrievable by design (ADR-0028).
type ingestKeyItem struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Token     string `json:"token"`
	CreatedAt string `json:"created_at"`
}

func toIngestKeyItem(k storage.IngestKey) ingestKeyItem {
	return ingestKeyItem{
		ID:        k.ID,
		Name:      k.Name,
		Token:     k.Token,
		CreatedAt: k.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// handleIngestKeys dispatches GET (list) and POST (create) on /api/v1/ingest-keys.
func (h *Handler) handleIngestKeys(w http.ResponseWriter, r *http.Request) {
	if h.ingestKeyStore == nil {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.listIngestKeys(w)
	case http.MethodPost:
		h.createIngestKey(w, r)
	default:
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleIngestKeyByID handles DELETE /api/v1/ingest-keys/{id} (revoke).
func (h *Handler) handleIngestKeyByID(w http.ResponseWriter, r *http.Request, id string) {
	if h.ingestKeyStore == nil || id == "" || strings.Contains(id, "/") {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodDelete {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	err := h.ingestKeyStore.DeleteIngestKey(id)
	if errors.Is(err, storage.ErrNotFound) {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		jsonError(w, "delete failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listIngestKeys(w http.ResponseWriter) {
	keys, err := h.ingestKeyStore.ListIngestKeys()
	if err != nil {
		queryFailed(w)
		return
	}
	items := make([]ingestKeyItem, 0, len(keys))
	for _, k := range keys {
		items = append(items, toIngestKeyItem(k))
	}
	jsonOK(w, struct {
		IngestKeys []ingestKeyItem `json:"ingest_keys"`
	}{items})
}

func (h *Handler) createIngestKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		jsonError(w, "name required", http.StatusBadRequest)
		return
	}
	if utf8.RuneCountInString(name) > maxIngestKeyName {
		jsonError(w, "name must be at most 100 characters", http.StatusBadRequest)
		return
	}
	k, err := h.ingestKeyStore.CreateIngestKey(name)
	if err != nil {
		jsonError(w, "create failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(toIngestKeyItem(k))
}

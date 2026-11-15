package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Flopsstuff/cotel/internal/storage"
)

// ActorStore is the write-capable interface used by actor management endpoints.
type ActorStore interface {
	CreateActor(name string) (storage.Actor, error)
	ListActorsPage(opts storage.ListActorsOptions) ([]storage.ActorWithStats, int, error)
	GetActorWithStats(id string, since *time.Time) (storage.ActorWithStats, error)
	RotateToken(actorID string) (storage.Actor, error)
	SoftDeleteActor(actorID string) error
	DeleteActorWithHistory(actorID string) error
	DeleteAnonymousData() error
	GetSetting(key string) (string, error)
	SetSetting(key, value string) error
}

type actorItem struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Token        string  `json:"token"`
	CreatedAt    string  `json:"created_at"`
	TotalCostUSD float64 `json:"cost"`
	Sessions     int64   `json:"sessions"`
	LastSeen     *string `json:"last_seen"`
}

type actorsListResponse struct {
	Actors []actorItem `json:"actors"`
	Total  int         `json:"total"`
	Page   int         `json:"page"`
	Limit  int         `json:"limit"`
	Range  string      `json:"range"`
	Sort   string      `json:"sort"`
	Order  string      `json:"order"`
}

// actorSortKeys whitelists the sort keys the actors list accepts; storage maps
// each to its SELECT-list column.
var actorSortKeys = map[string]string{
	"name":       "name",
	"created_at": "created_at",
	"last_seen":  "last_seen",
	"cost":       "cost",
	"sessions":   "sessions",
}

func toActorItem(u storage.ActorWithStats) actorItem {
	item := actorItem{
		ID:           u.ID,
		Name:         u.Name,
		Token:        u.Token,
		TotalCostUSD: u.TotalCostUSD,
		Sessions:     u.Sessions,
	}
	if u.ID != storage.AnonymousActorID {
		item.CreatedAt = u.CreatedAt.UTC().Format(time.RFC3339)
	}
	if u.LastSeen != nil {
		s := u.LastSeen.UTC().Format(time.RFC3339)
		item.LastSeen = &s
	}
	return item
}

func toActorItemPlain(u storage.Actor) actorItem {
	return actorItem{
		ID:        u.ID,
		Name:      u.Name,
		Token:     u.Token,
		CreatedAt: u.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// handleActors dispatches GET (list) and POST (create) on /api/v1/actors.
func (h *Handler) handleActors(w http.ResponseWriter, r *http.Request) {
	if h.actorStore == nil {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.listActors(w, r)
	case http.MethodPost:
		h.createActor(w, r)
	default:
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleActorByID dispatches GET (single actor) and DELETE on /api/v1/actors/{id}.
// The special id "__anonymous__" resolves to the synthetic anonymous row (GET)
// or purges all spans with no actor_id (DELETE).
// For named actors: ?mode=actor_only (default) soft-deletes; ?mode=actor_and_history anonymizes spans then hard-deletes.
func (h *Handler) handleActorByID(w http.ResponseWriter, r *http.Request, id string) {
	if h.actorStore == nil || id == "" {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}
	if r.Method == http.MethodGet {
		h.getActor(w, r, id)
		return
	}
	if r.Method != http.MethodDelete {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if id == storage.AnonymousActorID {
		if err := h.actorStore.DeleteAnonymousData(); err != nil {
			jsonError(w, "delete failed", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	mode := r.URL.Query().Get("mode")
	switch mode {
	case "":
		mode = "actor_only"
	case "user_only", "user_and_history": // pre-v11 names (ADR-0027)
		mode = "actor_" + strings.TrimPrefix(mode, "user_")
	}

	var err error
	switch mode {
	case "actor_only":
		err = h.actorStore.SoftDeleteActor(id)
	case "actor_and_history":
		err = h.actorStore.DeleteActorWithHistory(id)
	default:
		jsonError(w, "invalid mode: must be actor_only or actor_and_history", http.StatusBadRequest)
		return
	}

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

// handleActorRotateToken handles POST /api/v1/actors/{id}/rotate-token.
func (h *Handler) handleActorRotateToken(w http.ResponseWriter, r *http.Request, id string) {
	if h.actorStore == nil || id == "" {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	u, err := h.actorStore.RotateToken(id)
	if err != nil {
		jsonError(w, "rotate failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(toActorItemPlain(u))
}

func (h *Handler) listActors(w http.ResponseWriter, r *http.Request) {
	rangeKey, since := parseRange(r)
	sort, order := parseSortOrder(r, actorSortKeys, "cost")
	// limit defaults to 0 — unpaginated, which keeps the ActorSearch typeahead whole.
	page, limit := parsePaging(r)

	actors, total, err := h.actorStore.ListActorsPage(storage.ListActorsOptions{
		Since: since,
		Query: r.URL.Query().Get("q"),
		Sort:  sort,
		Order: order,
		Page:  page,
		Limit: limit,
	})
	if err != nil {
		jsonError(w, "query failed", http.StatusInternalServerError)
		return
	}
	items := make([]actorItem, 0, len(actors))
	for _, u := range actors {
		items = append(items, toActorItem(u))
	}
	jsonOK(w, actorsListResponse{
		Actors: items,
		Total:  total,
		Page:   page,
		Limit:  limit,
		Range:  rangeKey,
		Sort:   sort,
		Order:  order,
	})
}

// getActor handles GET /api/v1/actors/{id}: a single actor with range-scoped stats.
func (h *Handler) getActor(w http.ResponseWriter, r *http.Request, id string) {
	_, since := parseRange(r)
	u, err := h.actorStore.GetActorWithStats(id, since)
	if errors.Is(err, storage.ErrNotFound) {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		jsonError(w, "query failed", http.StatusInternalServerError)
		return
	}
	jsonOK(w, toActorItem(u))
}

func (h *Handler) createActor(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		jsonError(w, "name required", http.StatusBadRequest)
		return
	}
	u, err := h.actorStore.CreateActor(strings.TrimSpace(req.Name))
	if err != nil {
		jsonError(w, "create failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(toActorItemPlain(u))
}

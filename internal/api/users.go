package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Flopsstuff/cotel/internal/storage"
)

// UserStore is the write-capable interface used by user management endpoints.
type UserStore interface {
	CreateUser(name string) (storage.User, error)
	ListUsersPage(opts storage.ListUsersOptions) ([]storage.UserWithStats, int, error)
	GetUserWithStats(id string, since *time.Time) (storage.UserWithStats, error)
	RotateToken(userID string) (storage.User, error)
	SoftDeleteUser(userID string) error
	DeleteUserWithHistory(userID string) error
	DeleteAnonymousData() error
	GetSetting(key string) (string, error)
	SetSetting(key, value string) error
}

type userItem struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Token        string  `json:"token"`
	CreatedAt    string  `json:"created_at"`
	TotalCostUSD float64 `json:"cost"`
	Sessions     int64   `json:"sessions"`
	LastSeen     *string `json:"last_seen"`
}

type usersListResponse struct {
	Users []userItem `json:"users"`
	Total int        `json:"total"`
	Page  int        `json:"page"`
	Limit int        `json:"limit"`
	Range string     `json:"range"`
	Sort  string     `json:"sort"`
	Order string     `json:"order"`
}

// userSortKeys whitelists the sort keys the users list accepts; storage maps
// each to its SELECT-list column.
var userSortKeys = map[string]string{
	"name":       "name",
	"created_at": "created_at",
	"last_seen":  "last_seen",
	"cost":       "cost",
	"sessions":   "sessions",
}

func toUserItem(u storage.UserWithStats) userItem {
	item := userItem{
		ID:           u.ID,
		Name:         u.Name,
		Token:        u.Token,
		TotalCostUSD: u.TotalCostUSD,
		Sessions:     u.Sessions,
	}
	if u.ID != storage.AnonymousUserID {
		item.CreatedAt = u.CreatedAt.UTC().Format(time.RFC3339)
	}
	if u.LastSeen != nil {
		s := u.LastSeen.UTC().Format(time.RFC3339)
		item.LastSeen = &s
	}
	return item
}

func toUserItemPlain(u storage.User) userItem {
	return userItem{
		ID:        u.ID,
		Name:      u.Name,
		Token:     u.Token,
		CreatedAt: u.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// handleUsers dispatches GET (list) and POST (create) on /api/v1/users.
func (h *Handler) handleUsers(w http.ResponseWriter, r *http.Request) {
	if h.userStore == nil {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.listUsers(w, r)
	case http.MethodPost:
		h.createUser(w, r)
	default:
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleUserByID dispatches GET (single user) and DELETE on /api/v1/users/{id}.
// The special id "__anonymous__" resolves to the synthetic anonymous row (GET)
// or purges all spans with no user_id (DELETE).
// For named users: ?mode=user_only (default) soft-deletes; ?mode=user_and_history anonymizes spans then hard-deletes.
func (h *Handler) handleUserByID(w http.ResponseWriter, r *http.Request, id string) {
	if h.userStore == nil || id == "" {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}
	if r.Method == http.MethodGet {
		h.getUser(w, r, id)
		return
	}
	if r.Method != http.MethodDelete {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if id == storage.AnonymousUserID {
		if err := h.userStore.DeleteAnonymousData(); err != nil {
			jsonError(w, "delete failed", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "user_only"
	}

	var err error
	switch mode {
	case "user_only":
		err = h.userStore.SoftDeleteUser(id)
	case "user_and_history":
		err = h.userStore.DeleteUserWithHistory(id)
	default:
		jsonError(w, "invalid mode: must be user_only or user_and_history", http.StatusBadRequest)
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

// handleUserRotateToken handles POST /api/v1/users/{id}/rotate-token.
func (h *Handler) handleUserRotateToken(w http.ResponseWriter, r *http.Request, id string) {
	if h.userStore == nil || id == "" {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	u, err := h.userStore.RotateToken(id)
	if err != nil {
		jsonError(w, "rotate failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(toUserItemPlain(u))
}

func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	rangeKey, since := parseRange(r)
	sort, order := parseSortOrder(r, userSortKeys, "cost")
	// limit defaults to 0 — unpaginated, which keeps the UserSearch typeahead whole.
	page, limit := parsePaging(r)

	users, total, err := h.userStore.ListUsersPage(storage.ListUsersOptions{
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
	items := make([]userItem, 0, len(users))
	for _, u := range users {
		items = append(items, toUserItem(u))
	}
	jsonOK(w, usersListResponse{
		Users: items,
		Total: total,
		Page:  page,
		Limit: limit,
		Range: rangeKey,
		Sort:  sort,
		Order: order,
	})
}

// getUser handles GET /api/v1/users/{id}: a single user with range-scoped stats.
func (h *Handler) getUser(w http.ResponseWriter, r *http.Request, id string) {
	_, since := parseRange(r)
	u, err := h.userStore.GetUserWithStats(id, since)
	if errors.Is(err, storage.ErrNotFound) {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		jsonError(w, "query failed", http.StatusInternalServerError)
		return
	}
	jsonOK(w, toUserItem(u))
}

func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		jsonError(w, "name required", http.StatusBadRequest)
		return
	}
	u, err := h.userStore.CreateUser(strings.TrimSpace(req.Name))
	if err != nil {
		jsonError(w, "create failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(toUserItemPlain(u))
}

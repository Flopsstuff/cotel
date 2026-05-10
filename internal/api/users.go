package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Flopsstuff/cotel/internal/storage"
)

// UserStore is the write-capable interface used by user management endpoints.
type UserStore interface {
	CreateUser(name string) (storage.User, error)
	ListUsersWithStats() ([]storage.UserWithStats, error)
	RotateToken(userID string) (storage.User, error)
	DeleteUser(userID string) error
	GetSetting(key string) (string, error)
	SetSetting(key, value string) error
}

type userItem struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Token        string   `json:"token"`
	CreatedAt    string   `json:"created_at"`
	TotalCostUSD float64  `json:"cost"`
	Sessions     int64    `json:"sessions"`
	LastSeen     *string  `json:"last_seen"`
}

type usersListResponse struct {
	Users []userItem `json:"users"`
}

func toUserItem(u storage.UserWithStats) userItem {
	item := userItem{
		ID:           u.ID,
		Name:         u.Name,
		Token:        u.Token,
		CreatedAt:    u.CreatedAt.UTC().Format(time.RFC3339),
		TotalCostUSD: u.TotalCostUSD,
		Sessions:     u.Sessions,
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
		h.listUsers(w)
	case http.MethodPost:
		h.createUser(w, r)
	default:
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleUserByID dispatches DELETE on /api/v1/users/{id}.
func (h *Handler) handleUserByID(w http.ResponseWriter, r *http.Request, id string) {
	if h.userStore == nil || id == "" {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodDelete {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := h.userStore.DeleteUser(id); err != nil {
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

func (h *Handler) listUsers(w http.ResponseWriter) {
	users, err := h.userStore.ListUsersWithStats()
	if err != nil {
		jsonError(w, "query failed", http.StatusInternalServerError)
		return
	}
	items := make([]userItem, 0, len(users))
	for _, u := range users {
		items = append(items, toUserItem(u))
	}
	jsonOK(w, usersListResponse{Users: items})
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

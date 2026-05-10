package api

import (
	"encoding/json"
	"net/http"
)

type settingsResponse struct {
	AllowAnonymous bool `json:"allow_anonymous"`
}

// handleSettings dispatches GET and PUT on /api/v1/settings.
func (h *Handler) handleSettings(w http.ResponseWriter, r *http.Request) {
	if h.userStore == nil {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.getSettings(w)
	case http.MethodPut:
		h.putSettings(w, r)
	default:
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) getSettings(w http.ResponseWriter) {
	val, err := h.userStore.GetSetting("allow_anonymous")
	anon := true // default on
	if err == nil {
		anon = val == "true"
	}
	jsonOK(w, settingsResponse{AllowAnonymous: anon})
}

func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AllowAnonymous bool `json:"allow_anonymous"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	val := "false"
	if req.AllowAnonymous {
		val = "true"
	}
	if err := h.userStore.SetSetting("allow_anonymous", val); err != nil {
		jsonError(w, "update failed", http.StatusInternalServerError)
		return
	}
	jsonOK(w, settingsResponse{AllowAnonymous: req.AllowAnonymous})
}

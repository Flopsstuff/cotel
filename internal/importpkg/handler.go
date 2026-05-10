package importpkg

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/Flopsstuff/cotel/internal/storage"
)

// maxUploadBytes caps the HTTP upload body. The 100 MB uncompressed limit is
// enforced separately by ReadZIP; this caps the compressed payload.
const maxUploadBytes = 101 * 1024 * 1024

// ImportDB is the storage interface required by the import handler.
// *storage.DB satisfies this interface.
type ImportDB interface {
	ImportSpans(spans []storage.Span) (int, error)
	ImportDailyUsage(rows []storage.DailyUsageRow) (int, error)
}

// Handler serves POST /api/v1/import.
type Handler struct {
	db ImportDB
}

// NewHandler returns an import Handler backed by db.
func NewHandler(db ImportDB) *Handler {
	return &Handler{db: db}
}

type importResponse struct {
	SpansImported      int      `json:"spans_imported"`
	DailyUsageImported int      `json:"daily_usage_imported"`
	FormatVersion      int      `json:"format_version"`
	Warnings           []string `json:"warnings"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(32 * 1024 * 1024); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			http.Error(w, "upload exceeds 101 MB limit", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid multipart form", http.StatusBadRequest)
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, fmt.Sprintf("read file: %v", err), http.StatusBadRequest)
		return
	}

	manifest, spans, daily, err := ReadZIP(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		if errors.Is(err, ErrTooLarge) {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, fmt.Sprintf("invalid import file: %v", err), http.StatusBadRequest)
		return
	}

	spansN, err := h.db.ImportSpans(spans)
	if err != nil {
		http.Error(w, "import spans failed", http.StatusInternalServerError)
		return
	}

	dailyN, err := h.db.ImportDailyUsage(daily)
	if err != nil {
		http.Error(w, "import daily_usage failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(importResponse{
		SpansImported:      spansN,
		DailyUsageImported: dailyN,
		FormatVersion:      manifest.FormatVersion,
		Warnings:           []string{},
	})
}

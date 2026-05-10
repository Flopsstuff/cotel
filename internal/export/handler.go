package export

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Flopsstuff/cotel/internal/storage"
)

// ExportDB is the storage interface required by the export handler.
// *storage.DB satisfies this interface.
type ExportDB interface {
	ValidateToken(hash string) bool
	ExportSpans(from, to time.Time) ([]storage.Span, error)
	ExportDailyUsage(from, to time.Time) ([]storage.DailyUsageRow, error)
}

// Handler serves GET /api/v1/export.
type Handler struct {
	db ExportDB
}

// NewHandler returns an export Handler backed by db.
func NewHandler(db ExportDB) *Handler {
	return &Handler{db: db}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	hash, ok := bearerHash(r)
	if !ok || !h.db.ValidateToken(hash) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	period := r.URL.Query().Get("period")
	switch period {
	case "day", "week", "month":
	default:
		http.Error(w, "invalid period; must be day, week, or month", http.StatusBadRequest)
		return
	}

	dateStr := r.URL.Query().Get("date")
	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		http.Error(w, "invalid date; must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}

	var start, end time.Time
	switch period {
	case "day":
		start, end = DayRange(date)
	case "week":
		start, end = WeekRange(date)
	case "month":
		start, end = MonthRange(date)
	}

	spans, err := h.db.ExportSpans(start, end)
	if err != nil {
		http.Error(w, "query failed", http.StatusInternalServerError)
		return
	}
	daily, err := h.db.ExportDailyUsage(start, end)
	if err != nil {
		http.Error(w, "query failed", http.StatusInternalServerError)
		return
	}

	if len(spans) == 0 && len(daily) == 0 {
		http.Error(w, "no data for the requested period", http.StatusNotFound)
		return
	}

	tables := make(map[string]TableMeta)
	if len(spans) > 0 {
		tables["spans"] = TableMeta{RowCount: len(spans), Columns: SpansColumns}
	}
	if len(daily) > 0 {
		tables["daily_usage"] = TableMeta{RowCount: len(daily), Columns: DailyUsageColumns}
	}

	m := Manifest{
		FormatVersion: FormatVersion,
		CotelVersion:  CotelVersion,
		ExportAt:      time.Now().UTC(),
		Period:        period,
		PeriodStart:   start,
		PeriodEnd:     end,
		Tables:        tables,
	}

	filename := fmt.Sprintf("cotel-export-%s-%s.zip", period, dateStr)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.WriteHeader(http.StatusOK)

	if err := BuildZIP(w, m, spans, daily); err != nil {
		// Headers already sent; can only log, not change status.
		_ = err
	}
}

func bearerHash(r *http.Request) (string, bool) {
	auth := r.Header.Get("Authorization")
	token, ok := strings.CutPrefix(auth, "Bearer ")
	if !ok || token == "" {
		return "", false
	}
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:]), true
}

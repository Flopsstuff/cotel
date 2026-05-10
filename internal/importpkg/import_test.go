package importpkg_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Flopsstuff/cotel/internal/export"
	"github.com/Flopsstuff/cotel/internal/importpkg"
	"github.com/Flopsstuff/cotel/internal/storage"
)

// ---- ReadZIP unit tests ----

func buildTestZIP(t *testing.T, spans []storage.Span, daily []storage.DailyUsageRow) []byte {
	t.Helper()
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)

	tables := make(map[string]export.TableMeta)
	if len(spans) > 0 {
		tables["spans"] = export.TableMeta{RowCount: len(spans), Columns: export.SpansColumns}
	}
	if len(daily) > 0 {
		tables["daily_usage"] = export.TableMeta{RowCount: len(daily), Columns: export.DailyUsageColumns}
	}

	m := export.Manifest{
		FormatVersion: export.FormatVersion,
		CotelVersion:  export.CotelVersion,
		ExportAt:      time.Now().UTC(),
		Period:        "day",
		PeriodStart:   start,
		PeriodEnd:     end,
		Tables:        tables,
	}

	var buf bytes.Buffer
	if err := export.BuildZIP(&buf, m, spans, daily); err != nil {
		t.Fatalf("BuildZIP: %v", err)
	}
	return buf.Bytes()
}

func TestReadZIP_RoundTrip(t *testing.T) {
	inp := int64(100)
	cost := 0.005
	start := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	wantSpans := []storage.Span{
		{
			SpanID:      "span-rt-1",
			TraceID:     "trace-rt-1",
			Name:        "op.test",
			StartTime:   start,
			EndTime:     start.Add(time.Second),
			SessionID:   "sess-1",
			Model:       "claude-test",
			InputTokens: &inp,
			CostUSD:     &cost,
		},
	}
	wantDaily := []storage.DailyUsageRow{
		{
			Day:               time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
			SessionID:         "sess-1",
			Model:             "claude-test",
			ToolName:          "bash",
			SpanCount:         5,
			TotalInputTokens:  100,
			TotalOutputTokens: 50,
			TotalCostUSD:      0.01,
		},
	}

	data := buildTestZIP(t, wantSpans, wantDaily)
	manifest, gotSpans, gotDaily, err := importpkg.ReadZIP(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("ReadZIP: %v", err)
	}

	if manifest.FormatVersion != export.FormatVersion {
		t.Errorf("format_version: got %d, want %d", manifest.FormatVersion, export.FormatVersion)
	}

	if len(gotSpans) != 1 {
		t.Fatalf("spans: got %d, want 1", len(gotSpans))
	}
	s := gotSpans[0]
	if s.SpanID != "span-rt-1" {
		t.Errorf("SpanID: got %q, want span-rt-1", s.SpanID)
	}
	if s.InputTokens == nil || *s.InputTokens != 100 {
		t.Errorf("InputTokens: got %v, want 100", s.InputTokens)
	}
	if s.CostUSD == nil || *s.CostUSD != 0.005 {
		t.Errorf("CostUSD: got %v, want 0.005", s.CostUSD)
	}
	if !s.StartTime.Equal(start) {
		t.Errorf("StartTime: got %v, want %v", s.StartTime, start)
	}

	if len(gotDaily) != 1 {
		t.Fatalf("daily: got %d, want 1", len(gotDaily))
	}
	d := gotDaily[0]
	if d.SessionID != "sess-1" {
		t.Errorf("SessionID: got %q, want sess-1", d.SessionID)
	}
	if d.SpanCount != 5 {
		t.Errorf("SpanCount: got %d, want 5", d.SpanCount)
	}
	if d.TotalCostUSD != 0.01 {
		t.Errorf("TotalCostUSD: got %f, want 0.01", d.TotalCostUSD)
	}
}

func TestReadZIP_MissingManifest(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, _ := zw.Create("spans.csv")
	_, _ = f.Write([]byte("span_id,trace_id\n"))
	_ = zw.Close()

	data := buf.Bytes()
	_, _, _, err := importpkg.ReadZIP(bytes.NewReader(data), int64(len(data)))
	if err == nil {
		t.Fatal("expected error for missing manifest.json, got nil")
	}
}

func TestReadZIP_TooManyEntries(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i := 0; i < 6; i++ {
		f, _ := zw.Create("file" + string(rune('0'+i)) + ".txt")
		_, _ = f.Write([]byte("x"))
	}
	_ = zw.Close()

	data := buf.Bytes()
	_, _, _, err := importpkg.ReadZIP(bytes.NewReader(data), int64(len(data)))
	if err == nil {
		t.Fatal("expected error for too many entries, got nil")
	}
}

func TestReadZIP_FutureVersion(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	mf, _ := zw.Create("manifest.json")
	_, _ = mf.Write([]byte(`{"format_version":99,"cotel_version":"0.1.0","export_at":"2026-05-01T00:00:00Z","period":"day","period_start":"2026-05-01T00:00:00Z","period_end":"2026-05-02T00:00:00Z","tables":{}}`))
	_ = zw.Close()

	data := buf.Bytes()
	_, _, _, err := importpkg.ReadZIP(bytes.NewReader(data), int64(len(data)))
	if err == nil {
		t.Fatal("expected error for unsupported format_version, got nil")
	}
}

func TestReadZIP_Oversized(t *testing.T) {
	// Write a ZIP with 101 MB of zeros (compresses to almost nothing).
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, _ := zw.Create("big.csv") // Deflate by default
	chunk := make([]byte, 1024*1024)
	for i := 0; i <= 100; i++ {
		_, _ = f.Write(chunk)
	}
	_ = zw.Close()

	data := buf.Bytes()
	_, _, _, err := importpkg.ReadZIP(bytes.NewReader(data), int64(len(data)))
	if !errors.Is(err, importpkg.ErrTooLarge) {
		t.Errorf("oversized: got %v, want ErrTooLarge", err)
	}
}

func TestReadZIP_UnknownColumnsDropped(t *testing.T) {
	// CSV with an extra unknown column; should parse without error.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	mf, _ := zw.Create("manifest.json")
	_, _ = mf.Write([]byte(`{"format_version":1,"cotel_version":"0.1.0","export_at":"2026-05-01T00:00:00Z","period":"day","period_start":"2026-05-01T00:00:00Z","period_end":"2026-05-02T00:00:00Z","tables":{"spans":{"row_count":1,"columns":["span_id","trace_id","unknown_col"]}}}`))

	sf, _ := zw.Create("spans.csv")
	// Header has an extra unknown column; known columns have zero/empty values.
	_, _ = sf.Write([]byte("span_id,trace_id,unknown_col,name,start_time,end_time,parent_span_id,service_name,session_id,model,tool_name,user_id,status_code,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,cost_usd,attributes,resource_attrs,ingested_at,duration_ms\n"))
	_, _ = sf.Write([]byte("span-unk,trace-unk,IGNORED,op.test,2026-05-01T12:00:00Z,2026-05-01T12:00:01Z,,,,,,,,,,,,,,,,\n"))
	_ = zw.Close()

	data := buf.Bytes()
	_, spans, _, err := importpkg.ReadZIP(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("ReadZIP with unknown column: %v", err)
	}
	if len(spans) != 1 {
		t.Fatalf("spans: got %d, want 1", len(spans))
	}
	if spans[0].SpanID != "span-unk" {
		t.Errorf("SpanID: got %q", spans[0].SpanID)
	}
}

// ---- HTTP handler tests ----

type mockDB struct {
	spans  []storage.Span
	daily  []storage.DailyUsageRow
	spanN  int
	dailyN int
}

func (m *mockDB) ImportSpans(spans []storage.Span) (int, error) {
	m.spans = spans
	return m.spanN, nil
}
func (m *mockDB) ImportDailyUsage(rows []storage.DailyUsageRow) (int, error) {
	m.daily = rows
	return m.dailyN, nil
}

func makeMultipartUpload(t *testing.T, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "export.zip")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	return &body, mw.FormDataContentType()
}

func TestHandler_ValidImport200(t *testing.T) {
	inp := int64(10)
	spans := []storage.Span{
		{SpanID: "s1", TraceID: "t1", Name: "op",
			StartTime:   time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
			EndTime:     time.Date(2026, 5, 1, 12, 0, 1, 0, time.UTC),
			InputTokens: &inp},
	}
	data := buildTestZIP(t, spans, nil)
	body, ct := makeMultipartUpload(t, data)

	db := &mockDB{spanN: 1}
	h := importpkg.NewHandler(db)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/import", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		SpansImported      int      `json:"spans_imported"`
		DailyUsageImported int      `json:"daily_usage_imported"`
		FormatVersion      int      `json:"format_version"`
		Warnings           []string `json:"warnings"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.SpansImported != 1 {
		t.Errorf("spans_imported: got %d, want 1", resp.SpansImported)
	}
	if resp.FormatVersion != export.FormatVersion {
		t.Errorf("format_version: got %d, want %d", resp.FormatVersion, export.FormatVersion)
	}
	if resp.Warnings == nil {
		t.Error("warnings: got nil, want empty array")
	}
}

func TestHandler_MethodNotAllowed405(t *testing.T) {
	h := importpkg.NewHandler(&mockDB{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/import", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", w.Code)
	}
}

func TestHandler_BadZIP400(t *testing.T) {
	body, ct := makeMultipartUpload(t, []byte("not a zip file"))

	h := importpkg.NewHandler(&mockDB{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/import", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", w.Code)
	}
}

func TestHandler_MissingFileField400(t *testing.T) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("wrong_field", "value")
	_ = mw.Close()

	h := importpkg.NewHandler(&mockDB{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", w.Code)
	}
}

// ---- Round-trip + idempotency test with real storage ----

func TestRoundTripWithStorage(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer db.Close()

	inp := int64(50)
	start := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	spans := []storage.Span{
		{SpanID: "span-rts-1", TraceID: "trace-rts-1", Name: "op",
			StartTime:   start,
			EndTime:     start.Add(time.Second),
			SessionID:   "sess-rts",
			Model:       "claude-test",
			InputTokens: &inp,
		},
	}
	daily := []storage.DailyUsageRow{
		{Day: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), SessionID: "sess-rts",
			Model: "claude-test", ToolName: "bash", SpanCount: 1, TotalInputTokens: 50},
	}

	data := buildTestZIP(t, spans, daily)

	// First import: expect 1 span + 1 daily inserted.
	_, gotSpans, gotDaily, err := importpkg.ReadZIP(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("ReadZIP: %v", err)
	}

	n, err := db.ImportSpans(gotSpans)
	if err != nil {
		t.Fatalf("ImportSpans: %v", err)
	}
	if n != 1 {
		t.Errorf("first import spans: got %d inserted, want 1", n)
	}

	n, err = db.ImportDailyUsage(gotDaily)
	if err != nil {
		t.Fatalf("ImportDailyUsage: %v", err)
	}
	if n != 1 {
		t.Errorf("first import daily: got %d inserted, want 1", n)
	}

	// Second import (duplicate): expect 0 inserted.
	_, gotSpans2, gotDaily2, err := importpkg.ReadZIP(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("ReadZIP (dup): %v", err)
	}

	n, err = db.ImportSpans(gotSpans2)
	if err != nil {
		t.Fatalf("ImportSpans (dup): %v", err)
	}
	if n != 0 {
		t.Errorf("duplicate import spans: got %d inserted, want 0", n)
	}

	n, err = db.ImportDailyUsage(gotDaily2)
	if err != nil {
		t.Fatalf("ImportDailyUsage (dup): %v", err)
	}
	if n != 0 {
		t.Errorf("duplicate import daily: got %d inserted, want 0", n)
	}
}

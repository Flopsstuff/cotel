package importpkg

import "github.com/Flopsstuff/cotel/internal/storage"

// Migrate applies format-version transforms to parsed import data.
// Currently a no-op for v1; add version-gated cases here when future
// format_version bumps require column-level transforms.
func Migrate(fromVersion int, spans []storage.Span, daily []storage.DailyUsageRow) ([]storage.Span, []storage.DailyUsageRow) {
	// v1 is the current and only supported format — no transforms needed.
	// Example for a future v2 bump:
	//   if fromVersion < 2 { /* backfill new column */ }
	return spans, daily
}

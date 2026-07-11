package tui

// Layout constants shared across all list views.
// All column widths and spacing values live here so every view stays consistent.
const (
	// listColumnGap is the standard space between table columns across main lists.
	listColumnGap = 3
	// Marker and provider render as one aligned cell: !bun / lockbrew.
	toolPrivilegeProviderGap = 0

	// dotsIconW is the health icon width (one rune: ✓ ! ✗ ·).
	dotsIconW = 1
	// dotsGapW is the standard gap between right-side status columns.
	dotsGapW = 1
	// dotsIconNameGapW is the gap between icon and name in dots rows.
	dotsIconNameGapW = 1
	// dotsStatusColW is the fixed width for the status label column.
	// Covers: "ok", "missing", "conflict", "no-source".
	dotsStatusColW = 10
	// dotsRatioColW is the fixed width for synced/managed dot file summaries.
	// Covers compact rows like "12/14".
	dotsRatioColW = 5
	// dotsIgnoredColW is the minimum width for ignored dot file summaries.
	// Covers compact rows like "(3)".
	dotsIgnoredColW = 3
	// dotsNameMinW is the minimum width for the name column.
	dotsNameMinW = 12
)

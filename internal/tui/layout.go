package tui

const (
	listColumnGap = 3

	// Fallback terminal size used until the first WindowSizeMsg arrives.
	defaultTerminalWidth  = 80
	defaultTerminalHeight = 24
	// Marker and provider render as one aligned cell: ! bun / lock brew.
	toolPrivilegeProviderGap = 1

	// One rune wide: the health icon (✓ ! ✗ ·).
	dotsIconW        = 1
	dotsGapW         = 1
	dotsIconNameGapW = 1
	// Covers "ok", "missing", "conflict", "no-source".
	dotsStatusColW = 10
	// Covers compact rows like "12/14".
	dotsRatioColW = 5
	// Covers compact rows like "(3)".
	dotsIgnoredColW = 3
	dotsNameMinW    = 12
)

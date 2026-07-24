package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// palette holds all colours and pre-built lipgloss styles for one theme.
// A palette value is stored on each Model so that two Model instances
// (e.g. in parallel tests) never share mutable state.
type palette struct {
	// Colour tokens.
	colMuted          color.Color
	colInstalled      color.Color
	colMissing        color.Color
	colOutdated       color.Color
	colProvider       color.Color
	colProviderLinux  color.Color
	colProviderSystem color.Color
	colVersion        color.Color
	colSelected       color.Color
	colTitle          color.Color
	colHelp           color.Color
	colStatus         color.Color
	colSection        color.Color
	colDanger         color.Color
	colSurface        color.Color

	// Pre-built styles.
	styleTitle          lipgloss.Style
	styleSep            lipgloss.Style
	styleSelected       lipgloss.Style
	styleNormal         lipgloss.Style
	styleInstalled      lipgloss.Style
	styleIgnored        lipgloss.Style
	styleMissing        lipgloss.Style
	styleOutdated       lipgloss.Style
	styleOrphan         lipgloss.Style
	styleWrongProv      lipgloss.Style
	styleProvider       lipgloss.Style
	styleProviderLinux  lipgloss.Style
	styleProviderSystem lipgloss.Style
	styleVersion        lipgloss.Style
	styleVersionMuted   lipgloss.Style
	styleHelp           lipgloss.Style
	styleNoDesc         lipgloss.Style
	styleHintDesc       lipgloss.Style
	styleStatus         lipgloss.Style
	styleErr            lipgloss.Style
	styleSection        lipgloss.Style
	styleDangerLabel    lipgloss.Style
	styleDangerSection  lipgloss.Style
	styleCursor         lipgloss.Style
	styleActiveText     lipgloss.Style
}

// buildPaletteFor constructs a palette for the given background luminance.
// isDark=true → TokyoNight Night; isDark=false → TokyoNight Day.
func buildPaletteFor(isDark bool) palette {
	var p palette

	if isDark {
		p.colMuted = lipgloss.Color("#6f78a8")
		p.colInstalled = lipgloss.Color("#9ece6a")
		p.colMissing = lipgloss.Color("#ff6b8a")
		p.colOutdated = lipgloss.Color("#f7c46c")
		p.colProvider = lipgloss.Color("#7aa2f7")
		p.colProviderLinux = lipgloss.Color("#e0af68")
		p.colProviderSystem = lipgloss.Color("#4fd6be")
		p.colVersion = lipgloss.Color("#d9a94f")
		p.colSelected = lipgloss.Color("#253553")
		p.colTitle = lipgloss.Color("#e3e9ff")
		p.colHelp = lipgloss.Color("#8a94c2")
		p.colStatus = lipgloss.Color("#7dcfff")
		p.colSection = lipgloss.Color("#7dcfff")
		p.colDanger = lipgloss.Color("#ff5d7a")
		p.colSurface = lipgloss.Color("#1a1b26")

		p.styleNormal = lipgloss.NewStyle().Foreground(lipgloss.Color("#c8d3f5"))
		p.styleSelected = lipgloss.NewStyle().Background(p.colSelected).Foreground(lipgloss.Color("#e3e9ff"))
		p.styleIgnored = lipgloss.NewStyle().Foreground(lipgloss.Color("#4b557a"))
	} else {
		// TokyoNight Day
		p.colMuted = lipgloss.Color("#8b93b8")
		p.colInstalled = lipgloss.Color("#3f6b1f")
		p.colMissing = lipgloss.Color("#c9214d")
		p.colOutdated = lipgloss.Color("#8f5e15")
		p.colProvider = lipgloss.Color("#1f63d8")
		p.colProviderLinux = lipgloss.Color("#a95000")
		p.colProviderSystem = lipgloss.Color("#007b8f")
		p.colVersion = lipgloss.Color("#7c5a18")
		p.colSelected = lipgloss.Color("#dce6ff")
		p.colTitle = lipgloss.Color("#1f3f8f")
		p.colHelp = lipgloss.Color("#59627f")
		p.colStatus = lipgloss.Color("#006d9c")
		p.colSection = lipgloss.Color("#1f63d8")
		p.colDanger = lipgloss.Color("#c9214d")
		p.colSurface = lipgloss.Color("#e1e2e7")

		p.styleNormal = lipgloss.NewStyle().Foreground(lipgloss.Color("#24304f"))
		p.styleSelected = lipgloss.NewStyle().Background(p.colSelected).Foreground(lipgloss.Color("#111827"))
		p.styleIgnored = lipgloss.NewStyle().Foreground(lipgloss.Color("#b7bed4"))
	}

	// Rebuild all styles that reference the colour tokens.
	p.styleTitle = lipgloss.NewStyle().Bold(true).Foreground(p.colTitle).PaddingLeft(1)
	p.styleSep = lipgloss.NewStyle().Foreground(p.colMuted)

	p.styleInstalled = lipgloss.NewStyle().Foreground(p.colInstalled)
	p.styleMissing = lipgloss.NewStyle().Foreground(p.colMissing)
	p.styleOutdated = lipgloss.NewStyle().Foreground(p.colOutdated).Bold(true)
	p.styleOrphan = lipgloss.NewStyle().Foreground(p.colProviderSystem)
	p.styleWrongProv = lipgloss.NewStyle().Foreground(p.colOutdated)
	p.styleProvider = lipgloss.NewStyle().Foreground(p.colProvider)
	p.styleProviderLinux = lipgloss.NewStyle().Foreground(p.colProviderLinux)
	p.styleProviderSystem = lipgloss.NewStyle().Foreground(p.colProviderSystem)
	p.styleVersion = lipgloss.NewStyle().Foreground(p.colVersion)
	p.styleVersionMuted = lipgloss.NewStyle().Foreground(p.colMuted)
	p.styleHelp = lipgloss.NewStyle().Foreground(p.colHelp)
	p.styleNoDesc = lipgloss.NewStyle().Foreground(p.colHelp).Italic(true)
	p.styleHintDesc = lipgloss.NewStyle().Foreground(p.colMuted)
	p.styleStatus = lipgloss.NewStyle().Foreground(p.colStatus).Bold(true)
	p.styleErr = lipgloss.NewStyle().Foreground(p.colMissing).Bold(true)
	p.styleSection = lipgloss.NewStyle().Foreground(p.colSection).Bold(true)
	p.styleDangerLabel = lipgloss.NewStyle().Foreground(p.colDanger)
	p.styleDangerSection = lipgloss.NewStyle().Foreground(p.colDanger).Bold(true)
	p.styleCursor = lipgloss.NewStyle().Foreground(p.colStatus).Bold(true)
	p.styleActiveText = lipgloss.NewStyle().Foreground(p.colTitle).Bold(true)

	return p
}

// defaultPalette returns the dark (TokyoNight Night) palette used as the
// initial value until the terminal replies with its background colour.
func defaultPalette() palette {
	return buildPaletteFor(true)
}

// applyTheme rebuilds m.palette to match the terminal background luminance.
// Called once per model from the tea.BackgroundColorMsg handler.
func (m *Model) applyTheme(isDark bool) {
	m.palette = buildPaletteFor(isDark)
}

func backgroundIsDark(c color.Color) bool {
	if c == nil {
		return true
	}
	r, g, b, _ := c.RGBA()
	lum := 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
	return lum < 32768
}

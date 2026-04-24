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
		p.colMuted = lipgloss.Color("#636da6")
		p.colInstalled = lipgloss.Color("#c3e88d")
		p.colMissing = lipgloss.Color("#ff757f")
		p.colOutdated = lipgloss.Color("#ffc777")
		p.colProvider = lipgloss.Color("#82aaff")
		p.colProviderLinux = lipgloss.Color("#e0af68")
		p.colProviderSystem = lipgloss.Color("#89dceb")
		p.colVersion = lipgloss.Color("#ffc777")
		p.colSelected = lipgloss.Color("#3d59a1")
		p.colTitle = lipgloss.Color("#c0caf5")
		p.colHelp = lipgloss.Color("#565f89")
		p.colStatus = lipgloss.Color("#7aa2f7")
		p.colSection = lipgloss.Color("#7aa2f7")
		p.colDanger = lipgloss.Color("#ff757f")
		p.colSurface = lipgloss.Color("#1a1b26")

		p.styleNormal = lipgloss.NewStyle().Foreground(lipgloss.Color("#a9b1d6"))
		p.styleSelected = lipgloss.NewStyle().Background(p.colSelected).Foreground(lipgloss.Color("#c0caf5"))
		p.styleIgnored = lipgloss.NewStyle().Foreground(lipgloss.Color("#3b4261"))
	} else {
		// TokyoNight Day
		p.colMuted = lipgloss.Color("#6172b0")
		p.colInstalled = lipgloss.Color("#587539")
		p.colMissing = lipgloss.Color("#f52a65")
		p.colOutdated = lipgloss.Color("#8c6c3e")
		p.colProvider = lipgloss.Color("#2e7de9")
		p.colProviderLinux = lipgloss.Color("#b15c00")
		p.colProviderSystem = lipgloss.Color("#007197")
		p.colVersion = lipgloss.Color("#8c6c3e")
		p.colSelected = lipgloss.Color("#b4c2f0")
		p.colTitle = lipgloss.Color("#3760bf")
		p.colHelp = lipgloss.Color("#848cb5")
		p.colStatus = lipgloss.Color("#2e7de9")
		p.colSection = lipgloss.Color("#2e7de9")
		p.colDanger = lipgloss.Color("#f52a65")
		p.colSurface = lipgloss.Color("#e1e2e7")

		p.styleNormal = lipgloss.NewStyle().Foreground(lipgloss.Color("#3760bf"))
		p.styleSelected = lipgloss.NewStyle().Background(p.colSelected).Foreground(lipgloss.Color("#1f2335"))
		p.styleIgnored = lipgloss.NewStyle().Foreground(lipgloss.Color("#c4c8da"))
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
	p.styleCursor = lipgloss.NewStyle().Foreground(p.colTitle).Bold(true)
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

package tui

import (
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
)

// tabKeyMap wraps Model and implements key.Map with tab-aware ShortHelp.
// The compact footer legend shows different bindings depending on the active view.
type tabKeyMap struct{ m *Model }

// ShortHelp returns the most-used bindings for the active tab.
func (t tabKeyMap) ShortHelp() []key.Binding {
	return tabShortHelpBindings(t.m)
}

// FullHelp returns tab-specific bindings plus global navigation/help bindings.
func (t tabKeyMap) FullHelp() [][]key.Binding { return tabFullHelpBindings(t.m) }

func renderStatusBar(m Model) string {
	p := m.palette
	// Left: activity indicator — shown whenever any background work is in flight.
	var left string
	switch {
	case m.loading || m.dotsLoading || m.searching || len(m.scanningProviders) > 0 || len(m.upgradingKeys) > 0:
		text := m.progressText
		if text == "" {
			text = m.statusMsg
		}
		if text == "" {
			text = activityLabel(m)
		}
		style := statusTextStyle(p, text, m.statusIsErr)
		left = screenEdgeInset() + m.spinner.View() + " " + style.Render(text)
	case m.statusMsg != "":
		style := statusTextStyle(p, m.statusMsg, m.statusIsErr)
		left = screenEdgeInset() + style.Render(m.statusMsg)
	}

	// Right: tab-aware key-binding legend — always visible, right-aligned.
	legend := m.help.View(tabKeyMap{&m})
	if m.confirmQuit {
		keyLabel := m.quitConfirmKey
		if keyLabel == "" {
			keyLabel = "q"
		}
		legend = renderPressAgainActionHint(p, "", keyLabel, "quit")
	}

	return alignLR(left, legend, screenContentWidth(m.width), 1)
}

func statusTextStyle(p palette, text string, isErr bool) lipgloss.Style {
	if isErr {
		return p.styleErr
	}
	if strings.HasPrefix(strings.TrimSpace(text), "✓") {
		return p.styleInstalled.Bold(true)
	}
	return p.styleStatus
}

// activityLabel returns a descriptive fallback for the footer when no
// statusMsg or progressText has been set for the current activity.
func activityLabel(m Model) string {
	switch {
	case m.searching:
		return "Searching…"
	case len(m.scanningProviders) > 0:
		names := make([]string, 0, len(m.scanningProviders))
		for p := range m.scanningProviders {
			names = append(names, p)
		}
		sort.Strings(names)
		return "Scanning " + strings.Join(names, ", ") + "…"
	case m.dotsLoading:
		return "Loading dots…"
	default:
		return "Loading…"
	}
}

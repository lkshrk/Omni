package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/app"
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
	// Right: tab-aware key-binding legend — always visible, right-aligned.
	legend := m.help.View(tabKeyMap{&m})
	if m.confirmQuit {
		keyLabel := m.quitConfirmKey
		if keyLabel == "" {
			keyLabel = "q"
		}
		legend = renderPressAgainActionHint(p, "", keyLabel, "quit")
	} else if m.listConfirm.action == listConfirmSyncAll {
		desc := "sync all"
		if m.mode == viewStatus {
			desc = "reconcile all"
		}
		legend = renderPressAgainActionHint(p, "", m.keys.SyncAll.Help().Key, desc)
	}

	contentW := screenContentWidth(m.width)
	base := alignLR("", legend, contentW, 1)
	if footerConfirmationPromptActive(m) {
		return base
	}
	status := renderFooterStatusLayer(m, max(contentW-screenEdgePadding, 1))
	if status == "" {
		return base
	}
	if footerStatusOverlapsLegend(status, legend, contentW) {
		return renderFooterStatusOnly(status, contentW)
	}
	return overlayLine(base, status, screenEdgePadding)
}

func footerConfirmationPromptActive(m Model) bool {
	return m.confirmQuit || m.listConfirm.action == listConfirmSyncAll
}

func footerStatusOverlapsLegend(status, legend string, contentW int) bool {
	if legend == "" {
		return false
	}
	statusEnd := screenEdgePadding + lipgloss.Width(status)
	legendStart := contentW - lipgloss.Width(legend)
	return statusEnd+1 > legendStart
}

func renderFooterStatusOnly(status string, contentW int) string {
	line := screenEdgeInset() + status
	return line + strings.Repeat(" ", max(contentW-lipgloss.Width(line), 0))
}

func renderFooterStatusLayer(m Model, maxWidth int) string {
	p := m.palette
	switch {
	case m.loading || m.dotsLoading || m.doctorRunning || m.searching || len(m.scanningProviders) > 0 || len(m.outdatedProviders) > 0 || m.providerSnapshotRefreshing || m.outdatedSnapshotRefreshing || m.discoveryRefreshing || m.descRefreshing || len(m.upgradingKeys) > 0:
		text := m.progressText
		progress := text != ""
		if text == "" {
			text = m.statusMsg
		}
		if text == "" {
			text = activityLabel(m)
		}
		prefix := m.spinner.View() + " "
		textWidth := max(maxWidth-lipgloss.Width(prefix), 1)
		text = fitCellText(text, textWidth)
		style := statusTextStyle(p, text, m.statusIsErr && !progress)
		return prefix + style.Render(text)
	case m.statusMsg != "":
		text := fitCellText(m.statusMsg, maxWidth)
		style := statusTextStyle(p, text, m.statusIsErr)
		return style.Render(text)
	default:
		return ""
	}
}

func overlayLine(base, fg string, x int) string {
	baseLayer := lipgloss.NewLayer(base)
	fgLayer := lipgloss.NewLayer(fg).X(max(x, 0)).Z(1)
	return lipgloss.NewCompositor(baseLayer, fgLayer).Render()
}

func statusTextStyle(p palette, text string, isErr bool) lipgloss.Style {
	if isErr {
		return p.styleErr
	}
	if strings.HasPrefix(strings.TrimSpace(text), "✓") {
		return p.styleInstalled.Bold(true)
	}
	if isProviderRefreshText(text) {
		return p.styleStatus.Bold(true)
	}
	return p.styleStatus
}

func isProviderRefreshText(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "refreshing provider") || strings.Contains(lower, "refreshing tool")
}

// activityLabel returns a descriptive fallback for the footer when no
// statusMsg or progressText has been set for the current activity.
func activityLabel(m Model) string {
	switch {
	case m.searching:
		return "Searching…"
	case len(m.scanningProviders) > 0:
		return m.toolRefreshStatus(m.refreshToolDone, m.refreshToolTotal)
	case m.providerSnapshotRefreshing || m.discoveryRefreshing:
		return "Finding local tools…"
	case len(m.outdatedProviders) > 0 || m.outdatedSnapshotRefreshing:
		return "Checking updates…"
	case m.descRefreshing:
		return "Refreshing tool descriptions…"
	case m.dotsLoading:
		return "Loading dots…"
	case m.doctorRunning:
		return "Running doctor…"
	default:
		return "Loading…"
	}
}

func toolRefreshStatus(providers map[string]bool, done, total int) string {
	return app.RefreshToolsStatus(app.RefreshProviderScanLabels(providers, nil), done, total)
}

func (m Model) toolRefreshStatus(done, total int) string {
	return app.RefreshToolsStatus(app.RefreshProviderScanLabels(m.scanningProviders, m.providerScanLabels), done, total)
}

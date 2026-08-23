package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/app"
)

type tabKeyMap struct{ m *Model }

func (t tabKeyMap) ShortHelp() []key.Binding {
	return tabShortHelpBindings(t.m)
}

func (t tabKeyMap) FullHelp() [][]key.Binding { return tabFullHelpBindings(t.m) }

func renderStatusBar(m Model) string {
	p := m.palette
	legend := m.help.View(tabKeyMap{&m})
	if m.ctrlCConfirm {
		legend = renderPressAgainActionHint(p, "", "ctrl+c", "quit")
	} else if m.confirmQuit {
		legend = renderPressAgainActionHint(p, "", quitConfirmKeyLabel(m.quitConfirmKey), "quit")
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
	statusWidth := max(contentW*95/100-screenEdgePadding, 1)
	status := renderFooterStatusLayer(m, statusWidth)
	if status == "" {
		return base
	}
	if footerStatusOverlapsLegend(status, legend, contentW) {
		return renderFooterStatusOnly(status, contentW)
	}
	return overlayLine(base, status, screenEdgePadding)
}

func footerConfirmationPromptActive(m Model) bool {
	return m.ctrlCConfirm || m.confirmQuit || m.listConfirm.action == listConfirmSyncAll
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
	case m.spinnerActivityActive():
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

func activityLabel(m Model) string {
	switch {
	case m.searching:
		return "Searching…"
	case m.apmRunning:
		return "Running APM…"
	case len(m.scanningProviders) > 0:
		return m.toolRefreshStatus(m.refreshToolDone, m.refreshToolTotal)
	case m.providerSnapshotRefreshing || m.discoveryRefreshing:
		return "Finding local tools…"
	case len(m.outdatedProviders) > 0 || m.outdatedSnapshotRefreshing:
		return m.outdatedCheckStatus()
	case m.descRefreshing:
		return descriptionRefreshStatus
	case m.dotsLoading:
		return "Loading dots…"
	case m.dotsPeekLoading:
		return "Opening dotfile…"
	case m.doctorRunning:
		return "Running doctor…"
	default:
		return "Loading…"
	}
}

const descriptionRefreshStatus = "Refreshing descriptions…"

func (m Model) toolRefreshStatus(done, total int) string {
	return app.RefreshToolProgressStatus(m.headScanProviderLabel(m.scanningProviders), "", done, total)
}

func (m Model) outdatedCheckStatus() string {
	status := "Checking updates…"
	if m.outdatedTotal <= 0 {
		return status
	}
	done := max(m.outdatedTotal-len(m.outdatedProviders), 0)
	status += fmt.Sprintf(" %d/%d", done, m.outdatedTotal)
	if label := m.headScanProviderLabel(m.outdatedProviders); label != "" {
		status += ": " + label
	}
	return status
}

// Naming every in-flight provider makes the line jitter with map order and overflow the status bar, so one provider heads the line: the one with the most tools left to account for, which is the likeliest to be the slow one and only changes when it finishes.
func (m Model) headScanProviderLabel(providers map[string]bool) string {
	head, headCount := "", -1
	for providerName := range providers {
		if providerName == "" {
			continue
		}
		label := app.RefreshProviderScanLabel(providerName, m.providerScanLabels)
		count := m.providerScanToolCounts[providerName]
		if count > headCount || (count == headCount && label < head) {
			head, headCount = label, count
		}
	}
	if head == "" {
		return ""
	}
	if extra := len(providers) - 1; extra > 0 {
		return fmt.Sprintf("%s +%d", head, extra)
	}
	return head
}

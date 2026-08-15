package tui

import (
	"sort"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
)

type pluginRowsMsg struct {
	rows      []app.PluginRow
	unmanaged map[string][]app.InstalledPlugin
	err       error
}

type pluginRestoreDoneMsg struct{ err error }

type pluginRemoveDoneMsg struct {
	err     error
	warning string
}

type pluginImportAdoptDoneMsg struct {
	err error
	// Set when this claim also added a marketplace, so the caller reloads marketplace rows; a plain plugin claim never changes them.
	reloadMarketplaces bool
}

// Returned instead of pluginImportAdoptDoneMsg when the marketplace is not yet declared, so the TUI can offer to claim both rather than hard-failing like AddPlugin.
type pluginNeedsMarketplaceMsg struct {
	agentID         string
	plugin          app.InstalledPlugin
	group           string
	marketplaceName string
	source          string
}

type pluginAgentsSavedMsg struct{ err error }

func (m *Model) doLoadPluginRows() tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		rows, unmanaged, err := a.PluginRows(ctx)
		return pluginRowsMsg{rows: rows, unmanaged: unmanaged, err: err}
	}
}

func (m *Model) armPluginMarketplaceOffer(msg pluginNeedsMarketplaceMsg) tea.Cmd {
	m.pluginMarketplaceOfferConfirm = true
	m.pluginMarketplaceOfferAgentID = msg.agentID
	m.pluginMarketplaceOfferPlugin = msg.plugin
	m.pluginMarketplaceOfferGroup = msg.group
	m.pluginMarketplaceOfferMarket = msg.marketplaceName
	m.pluginMarketplaceOfferSource = msg.source
	return m.armConfirmationTimeout()
}

type pluginUpdateDoneMsg struct{ err error }

type pluginGroupsSavedMsg struct{ err error }

func (m *Model) doSetPluginGroupMemberships(name string, groups []string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		err := a.SetPluginGroups(ctx, name, groups)
		return pluginGroupsSavedMsg{err: err}
	}
}

type pluginUnmanagedEntry struct {
	agentID string
	plugin  app.InstalledPlugin
}

func pluginUnmanagedFlat(unmanaged map[string][]app.InstalledPlugin) []pluginUnmanagedEntry {
	agentIDs := make([]string, 0, len(unmanaged))
	for id := range unmanaged {
		agentIDs = append(agentIDs, id)
	}
	sort.Strings(agentIDs)
	var out []pluginUnmanagedEntry
	for _, id := range agentIDs {
		plugins := append([]app.InstalledPlugin(nil), unmanaged[id]...)
		sort.Slice(plugins, func(i, j int) bool { return plugins[i].Name < plugins[j].Name })
		for _, p := range plugins {
			out = append(out, pluginUnmanagedEntry{agentID: id, plugin: p})
		}
	}
	return out
}

func pluginTotalRows(m Model) int {
	return len(m.pluginRows) + len(pluginUnmanagedFlat(m.pluginUnmanaged))
}

func clampPluginCursor(m *Model) {
	n := pluginTotalRows(*m)
	if n == 0 {
		m.pluginCursor = 0
		return
	}
	if m.pluginCursor >= n {
		m.pluginCursor = n - 1
	}
	if m.pluginCursor < 0 {
		m.pluginCursor = 0
	}
}

type pluginAddDoneMsg struct{ err error }

func (m *Model) resetPluginForm() {
	m.pluginFormField = 0
	m.pluginFormName.SetValue("")
	m.pluginFormMarketplace.SetValue("")
	m.pluginFormSource.SetValue("")
	m.pluginFormAgents.SetValue("")
	m.pluginFormName.Blur()
	m.pluginFormMarketplace.Blur()
	m.pluginFormSource.Blur()
	m.pluginFormAgents.Blur()
}

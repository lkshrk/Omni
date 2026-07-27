package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
)

func combinePluginErrors(err error, adapterErrs []app.PluginError) error {
	if err == nil && len(adapterErrs) == 0 {
		return nil
	}
	all := make([]error, 0, len(adapterErrs)+1)
	if err != nil {
		all = append(all, err)
	}
	for _, e := range adapterErrs {
		all = append(all, e)
	}
	return errors.Join(all...)
}

func pluginWarningsText(warnings []app.PluginError) string {
	if len(warnings) == 0 {
		return ""
	}
	parts := make([]string, 0, len(warnings))
	for _, w := range warnings {
		parts = append(parts, w.AgentID+": "+w.Err.Error())
	}
	return strings.Join(parts, "; ")
}

type pluginRowsMsg struct {
	rows      []app.PluginRow
	unmanaged map[string][]app.InstalledPlugin
	err       error
}

type pluginRestoreDoneMsg struct{ err error }

type pluginRemoveDoneMsg struct {
	name    string
	err     error
	warning string
}

type pluginImportAdoptDoneMsg struct {
	pluginName string
	err        error
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

func (m *Model) doRestorePlugin() tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		_, err := a.RestorePlugins(ctx, app.RestorePluginOptions{})
		return pluginRestoreDoneMsg{err: err}
	}
}

func (m *Model) doRemovePlugin(name string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		res, err := a.RemovePlugin(ctx, name)
		return pluginRemoveDoneMsg{name: name, err: combinePluginErrors(err, res.Errors), warning: pluginWarningsText(res.Warnings)}
	}
}

// Claiming one row of a plugin unmanaged under several agents must declare all of them, or the other agents' installs vanish from every agents-tab view.
func pluginUnmanagedAgentsFor(unmanaged map[string][]app.InstalledPlugin, name, clickedAgentID string) []string {
	set := map[string]struct{}{clickedAgentID: {}}
	for agentID, plugins := range unmanaged {
		for _, p := range plugins {
			if p.Name == name {
				set[agentID] = struct{}{}
				break
			}
		}
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Mirrors the CLI's importPluginByName conflict guard so the TUI refuses the same ambiguous case instead of silently picking one agent's marketplace.
func pluginUnmanagedConflict(unmanaged map[string][]app.InstalledPlugin, name string, first app.InstalledPlugin) bool {
	for _, plugins := range unmanaged {
		for _, p := range plugins {
			if p.Name != name {
				continue
			}
			if p.Marketplace != first.Marketplace {
				return true
			}
		}
	}
	return false
}

// The marketplace check runs inside the async cmd, not at picker-confirm time: m.marketplaceRows is a cached snapshot that may be stale, so only a fresh read through App is reliable.
func (m *Model) doImportPluginWithGroup(agentID string, p app.InstalledPlugin, group string) tea.Cmd {
	a, ctx := m.app, m.ctx
	unmanaged := m.pluginUnmanaged
	return func() tea.Msg {
		if pluginUnmanagedConflict(unmanaged, p.Name, p) {
			return pluginImportAdoptDoneMsg{pluginName: p.Name, err: fmt.Errorf("plugin %q is unmanaged under multiple agents with conflicting marketplaces; import each manually", p.Name)}
		}
		agentIDs := pluginUnmanagedAgentsFor(unmanaged, p.Name, agentID)
		marketplaces, err := a.Marketplaces()
		if err != nil {
			return pluginImportAdoptDoneMsg{pluginName: p.Name, err: err}
		}
		declared := false
		for _, mk := range marketplaces {
			if mk.Name == p.Marketplace {
				declared = true
				break
			}
		}
		if !declared {
			source, ok, findErr := findMarketplaceSourceForClaim(a, ctx, m.marketplaceUnmanaged, p.Marketplace, agentIDs)
			if findErr != nil {
				return pluginImportAdoptDoneMsg{pluginName: p.Name, err: findErr}
			}
			if !ok {
				return pluginImportAdoptDoneMsg{pluginName: p.Name, err: fmt.Errorf("plugin %q references undeclared marketplace %q with no discoverable source; declare it first", p.Name, p.Marketplace)}
			}
			return pluginNeedsMarketplaceMsg{agentID: agentID, plugin: p, group: group, marketplaceName: p.Marketplace, source: source}
		}
		return doAdoptPlugin(a, ctx, p, agentIDs, group)
	}
}

func doAdoptPlugin(a *app.App, ctx context.Context, p app.InstalledPlugin, agentIDs []string, group string) pluginImportAdoptDoneMsg {
	res, err := a.AddPlugin(ctx, pluginFromInstalled(p, agentIDs))
	if err := combinePluginErrors(err, res.Errors); err != nil {
		return pluginImportAdoptDoneMsg{pluginName: p.Name, err: err}
	}
	if group != "" {
		if err := a.SetPluginGroups(ctx, p.Name, []string{group}); err != nil {
			return pluginImportAdoptDoneMsg{pluginName: p.Name, err: err}
		}
	}
	return pluginImportAdoptDoneMsg{pluginName: p.Name}
}

// Prefers the agents-tab unmanaged marketplace map, falling back to App.FindUndeclaredMarketplace when the marketplace rows have not loaded but the plugin rows have.
func findMarketplaceSourceForClaim(a *app.App, ctx context.Context, unmanagedMarketplaces map[string][]app.InstalledMarketplace, name string, agentIDs []string) (source string, ok bool, err error) {
	for _, marketplaces := range unmanagedMarketplaces {
		for _, mk := range marketplaces {
			if mk.Name == name && mk.Source != "" {
				return mk.Source, true, nil
			}
		}
	}
	return a.FindUndeclaredMarketplace(ctx, name, agentIDs)
}

// If the marketplace claim succeeds but the plugin claim fails, that partial state is intentional — the marketplace claim is a complete unit of work — so nothing is rolled back.
func (m *Model) doClaimPluginAndMarketplace(agentID string, p app.InstalledPlugin, group, marketplaceName, source string) tea.Cmd {
	a, ctx := m.app, m.ctx
	unmanaged := m.pluginUnmanaged
	marketplaceUnmanaged := m.marketplaceUnmanaged
	marketplaceAgentIDs := marketplaceUnmanagedAgentsFor(marketplaceUnmanaged, marketplaceName, agentID)
	return func() tea.Msg {
		if marketplaceUnmanagedConflict(marketplaceUnmanaged, marketplaceName, app.InstalledMarketplace{Name: marketplaceName, Source: source}) {
			return pluginImportAdoptDoneMsg{pluginName: p.Name, err: fmt.Errorf("marketplace %q is unmanaged under multiple agents with conflicting sources; import each manually", marketplaceName)}
		}
		mres, err := a.AddMarketplace(ctx, app.Marketplace{Name: marketplaceName, Source: source, Agents: marketplaceAgentIDs})
		if err := combineMarketplaceErrors(err, mres.Errors); err != nil {
			return pluginImportAdoptDoneMsg{pluginName: p.Name, err: fmt.Errorf("adding marketplace %q: %w", marketplaceName, err)}
		}
		if group != "" {
			if err := a.SetMarketplaceGroups(ctx, marketplaceName, []string{group}); err != nil {
				return pluginImportAdoptDoneMsg{
					pluginName:         p.Name,
					err:                fmt.Errorf("marketplace %q was added, but group assignment failed: %w", marketplaceName, err),
					reloadMarketplaces: true,
				}
			}
		}
		agentIDs := pluginUnmanagedAgentsFor(unmanaged, p.Name, agentID)
		msg := doAdoptPlugin(a, ctx, p, agentIDs, group)
		if msg.err != nil {
			msg.err = fmt.Errorf("marketplace %q was added, but plugin %q failed: %w", marketplaceName, p.Name, msg.err)
		}
		msg.reloadMarketplaces = true
		return msg
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

// Any other key (Back/esc included) cancels the whole claim and writes nothing, mirroring m.cancelGroupPicker leaving no partial state.
func (m *Model) handlePluginMarketplaceOfferConfirmKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	m.cancelConfirmationTimeout()
	agentID := m.pluginMarketplaceOfferAgentID
	p := m.pluginMarketplaceOfferPlugin
	group := m.pluginMarketplaceOfferGroup
	marketplaceName := m.pluginMarketplaceOfferMarket
	source := m.pluginMarketplaceOfferSource
	confirmed := key.Matches(msg, m.keys.Confirm)
	m.pluginMarketplaceOfferConfirm = false
	m.pluginMarketplaceOfferAgentID = ""
	m.pluginMarketplaceOfferPlugin = app.InstalledPlugin{}
	m.pluginMarketplaceOfferGroup = ""
	m.pluginMarketplaceOfferMarket = ""
	m.pluginMarketplaceOfferSource = ""
	if confirmed {
		return []tea.Cmd{m.spinner.Tick, m.doClaimPluginAndMarketplace(agentID, p, group, marketplaceName, source)}
	}
	m.pluginRunning = false
	m.clearAgentsOp()
	return []tea.Cmd{setStatus(m, "cancelled: "+p.Name+" was not claimed", false)}
}

func (m *Model) doSetPluginAgents(row app.PluginRow, ids []string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		res, err := a.SetPluginAgents(ctx, row.Name, ids)
		return pluginAgentsSavedMsg{err: skippedUnavailableErr(combinePluginErrors(err, res.Errors), res.SkippedUnavailable)}
	}
}

type pluginUpdateDoneMsg struct{ err error }

func (m *Model) doUpdatePlugin(name string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		res, err := a.UpdatePlugin(ctx, name)
		return pluginUpdateDoneMsg{err: skippedUnavailableErr(combinePluginErrors(err, res.Errors), res.SkippedUnavailable)}
	}
}

func (m *Model) doInstallPlugin(name, agentID string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		p, ok, err := a.PluginByName(name)
		if err != nil {
			return pluginAgentsSavedMsg{err: err}
		}
		if !ok {
			return pluginAgentsSavedMsg{err: errors.New("plugin " + name + " not found in manifest")}
		}
		ids := p.Agents
		if !contains(ids, agentID) {
			ids = append(append([]string(nil), ids...), agentID)
		}
		res, err := a.SetPluginAgents(ctx, name, ids)
		return pluginAgentsSavedMsg{err: skippedUnavailableErr(combinePluginErrors(err, res.Errors), res.SkippedUnavailable)}
	}
}

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

func pluginHighlightedUnmanaged(m Model) (app.InstalledPlugin, string, bool) {
	if m.pluginCursor < len(m.pluginRows) {
		return app.InstalledPlugin{}, "", false
	}
	flat := pluginUnmanagedFlat(m.pluginUnmanaged)
	idx := m.pluginCursor - len(m.pluginRows)
	if idx < 0 || idx >= len(flat) {
		return app.InstalledPlugin{}, "", false
	}
	e := flat[idx]
	return e.plugin, e.agentID, true
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

func pluginGroupsStatusText(row app.PluginRow) string {
	if len(row.Groups) == 0 {
		return row.Name + ": no group memberships"
	}
	return row.Name + " groups: " + strings.Join(row.Groups, ", ")
}

func (m *Model) openPluginAgentsPicker(row app.PluginRow) tea.Cmd {
	ids := make([]string, 0, len(row.PerAgentStatus))
	for id := range row.PerAgentStatus {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	targeted := func(id string) bool {
		if len(row.Agents) == 0 {
			return true
		}
		for _, a := range row.Agents {
			if a == id {
				return true
			}
		}
		return false
	}
	rows := make([]app.SkillAgentRow, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, app.SkillAgentRow{
			ID:        id,
			Display:   id,
			Targeted:  targeted(id),
			Installed: row.PerAgentStatus[id] == app.PluginStatusInstalled,
		})
	}
	m.skillAgentsSource = row.Name
	m.skillAgentsRows = rows
	m.skillAgentsCursor = 0
	m.pluginAgentsRow = row
	m.pluginAgentsPicker = true
	return nil
}

// Targets every agent unmanaged reports this plugin under, unioned with the clicked row's agent (see pluginUnmanagedAgentsFor).
func pluginFromInstalled(p app.InstalledPlugin, agentIDs []string) app.Plugin {
	return app.Plugin{Name: p.Name, Marketplace: p.Marketplace, Agents: agentIDs}
}

type pluginAddDoneMsg struct{ err error }

func (m *Model) doAddPlugin(p app.Plugin) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		res, err := a.AddPlugin(ctx, p)
		return pluginAddDoneMsg{err: skippedUnavailableErr(combinePluginErrors(err, res.Errors), res.SkippedUnavailable)}
	}
}

func (m *Model) resetPluginForm() {
	m.pluginFormField = 0
	m.pluginFormName.SetValue("")
	m.pluginFormMarketplace.SetValue("")
	m.pluginFormAgents.SetValue("")
	m.pluginFormName.Blur()
	m.pluginFormMarketplace.Blur()
	m.pluginFormAgents.Blur()
}

func (m *Model) focusPluginFormField() {
	m.pluginFormName.Blur()
	m.pluginFormMarketplace.Blur()
	m.pluginFormAgents.Blur()
	switch m.pluginFormField {
	case 0:
		m.pluginFormName.Focus()
	case 1:
		m.pluginFormMarketplace.Focus()
	case 2:
		m.pluginFormAgents.Focus()
	}
}

// Name and marketplace are required; agents is an optional comma-separated list (empty means all MVP agents).
func (m *Model) buildPluginFromForm() (app.Plugin, error) {
	name := strings.TrimSpace(m.pluginFormName.Value())
	if name == "" {
		return app.Plugin{}, errors.New("name is required")
	}
	marketplace := strings.TrimSpace(m.pluginFormMarketplace.Value())
	if marketplace == "" {
		return app.Plugin{}, errors.New("marketplace is required")
	}
	var agents []string
	raw := strings.TrimSpace(m.pluginFormAgents.Value())
	if raw != "" {
		for _, part := range strings.Split(raw, ",") {
			id := strings.TrimSpace(part)
			if id != "" {
				agents = append(agents, id)
			}
		}
	}
	return app.Plugin{Name: name, Marketplace: marketplace, Agents: agents}, nil
}

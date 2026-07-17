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
	"github.com/lkshrk/omni/internal/config"
)

// combinePluginErrors folds a top-level error with per-adapter errors,
// mirroring combineMcpErrors in update_mcp.go.
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

// pluginWarningsText flattens non-fatal adapter warnings into one status line.
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
	// reloadMarketplaces is set when this claim also added a marketplace (the
	// combined offer flow), so the caller reloads marketplace rows too — a
	// plain plugin claim never changes marketplace rows.
	reloadMarketplaces bool
}

// pluginNeedsMarketplaceMsg is returned instead of pluginImportAdoptDoneMsg
// when the plugin's marketplace is not yet declared in the manifest and a
// real source was found, so the TUI can offer to claim both rather than
// hard-failing like AddPlugin does on its own.
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

// pluginUnmanagedAgentsFor returns every agent ID whose unmanaged map has an
// entry named name, unioned with clickedAgentID, sorted for determinism.
// Mirrors mcpUnmanagedAgentsFor's rationale in update_mcp.go: claiming one
// row of a plugin unmanaged under several agents must declare all of them,
// or the other agents' installs vanish from every agents-tab view once the
// name is no longer "unmanaged" but also not targeted.
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

// pluginUnmanagedConflict reports whether name is unmanaged under more than
// one agent with a differing marketplace, mirroring the CLI's
// importPluginByName conflict guard in internal/cli/agents.go so the TUI
// claim path refuses the same ambiguous case instead of silently picking one
// agent's marketplace to write to the manifest.
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

// doImportPluginWithGroup adopts one unmanaged plugin then assigns it to
// group in one command, mirroring doImportMcpServerWithGroup. SetPluginGroups
// assumes the target group already exists, matching the existing plugin
// group-membership picker's behavior. Agents declares every agent unmanaged
// has this plugin under (not just agentID), see pluginUnmanagedAgentsFor.
//
// Marketplace declaration is checked here, inside the async cmd, rather than
// at picker-confirm keypress time: m.marketplaceRows is a cached snapshot
// that may be stale or not yet loaded, so the only reliable check is a fresh
// read through App at execution time (mirrors the CLI's importPluginByName in
// internal/cli/agents.go). If the marketplace is undeclared but a real source
// is discoverable, this returns pluginNeedsMarketplaceMsg instead of erroring
// so the caller can offer to claim both; AddPlugin's own hard error only
// surfaces when no source can be found at all.
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

// doAdoptPlugin performs the AddPlugin + optional SetPluginGroups step,
// factored out of doImportPluginWithGroup so doClaimPluginAndMarketplace can
// reuse it after claiming the marketplace first.
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

// findMarketplaceSourceForClaim resolves a real source for an undeclared
// marketplace being claimed alongside a plugin, mirroring the CLI's
// importPluginByName: prefer the agents-tab unmanaged marketplace map (same
// data the standalone marketplace claim flow uses, see
// marketplaceUnmanagedAgentsFor in update_marketplace.go); fall back to
// App.FindUndeclaredMarketplace when the marketplace isn't present there at
// all (e.g. the marketplace rows haven't loaded but the plugin rows have).
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

// doClaimPluginAndMarketplace claims the marketplace first (mirroring the
// standalone marketplace claim in doImportMarketplaceWithGroup: Agents is the
// sorted union of agents carrying it, via marketplaceUnmanagedAgentsFor),
// then the plugin, in one async cmd chain. If the marketplace claim succeeds
// but the plugin claim fails, that partial state is intentional — the
// marketplace claim is a complete, real unit of work on its own — so the
// error message says the marketplace was added and the plugin failed rather
// than attempting any rollback.
func (m *Model) doClaimPluginAndMarketplace(agentID string, p app.InstalledPlugin, group, marketplaceName, source string) tea.Cmd {
	a, ctx := m.app, m.ctx
	unmanaged := m.pluginUnmanaged
	marketplaceUnmanaged := m.marketplaceUnmanaged
	marketplaceAgentIDs := marketplaceUnmanagedAgentsFor(marketplaceUnmanaged, marketplaceName, agentID)
	return func() tea.Msg {
		if marketplaceUnmanagedConflict(marketplaceUnmanaged, marketplaceName, app.InstalledMarketplace{Name: marketplaceName, Source: source}) {
			return pluginImportAdoptDoneMsg{pluginName: p.Name, err: fmt.Errorf("marketplace %q is unmanaged under multiple agents with conflicting sources; import each manually", marketplaceName)}
		}
		mres, err := a.AddMarketplace(ctx, config.Marketplace{Name: marketplaceName, Source: source, Agents: marketplaceAgentIDs})
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

// armPluginMarketplaceOffer stores msg's payload and arms the confirm state,
// called from the update.go handler for pluginNeedsMarketplaceMsg.
func (m *Model) armPluginMarketplaceOffer(msg pluginNeedsMarketplaceMsg) tea.Cmd {
	m.pluginMarketplaceOfferConfirm = true
	m.pluginMarketplaceOfferAgentID = msg.agentID
	m.pluginMarketplaceOfferPlugin = msg.plugin
	m.pluginMarketplaceOfferGroup = msg.group
	m.pluginMarketplaceOfferMarket = msg.marketplaceName
	m.pluginMarketplaceOfferSource = msg.source
	return m.armConfirmationTimeout()
}

// handlePluginMarketplaceOfferConfirmKeyMsg resolves the "claim both?" offer
// armed by pluginNeedsMarketplaceMsg. Confirm/y claims the marketplace (using
// the source resolved when the offer was armed) then the plugin; any other
// key (Back/esc included) cancels the whole claim with a neutral status
// message and writes nothing, mirroring how the standalone group picker's
// cancel path (m.cancelGroupPicker) leaves no partial state.
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

// doUpdatePlugin updates a manifest plugin on every targeted, available adapter.
func (m *Model) doUpdatePlugin(name string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		res, err := a.UpdatePlugin(ctx, name)
		return pluginUpdateDoneMsg{err: skippedUnavailableErr(combinePluginErrors(err, res.Errors), res.SkippedUnavailable)}
	}
}

// doInstallPlugin re-installs a manifest plugin on one targeted-but-missing
// agent via SetPluginAgents, mirroring doInstallMcpServer.
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

// doSetPluginGroupMemberships persists group membership for a plugin via
// App.SetPluginGroups, mirroring doSetMcpGroupMemberships.
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

// openPluginAgentsPicker builds an agents picker from a managed row's current
// per-adapter status, reusing the skill-agents popup fields, exactly as
// openMcpAgentsPicker does.
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

// pluginFromInstalled builds a config.Plugin for AddPlugin from an unmanaged
// InstalledPlugin, targeting agentIDs (every agent unmanaged reports this
// plugin under, unioned with the clicked row's agent — see
// pluginUnmanagedAgentsFor), mirroring how the mcp import path in
// update_keys.go's "i" case builds a config.McpServer inline.
func pluginFromInstalled(p app.InstalledPlugin, agentIDs []string) config.Plugin {
	return config.Plugin{Name: p.Name, Marketplace: p.Marketplace, Agents: agentIDs}
}

type pluginAddDoneMsg struct{ err error }

// doAddPlugin registers a new plugin built from the add-plugin form.
func (m *Model) doAddPlugin(p config.Plugin) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		res, err := a.AddPlugin(ctx, p)
		return pluginAddDoneMsg{err: skippedUnavailableErr(combinePluginErrors(err, res.Errors), res.SkippedUnavailable)}
	}
}

// resetPluginForm clears the add-plugin form back to its initial state.
func (m *Model) resetPluginForm() {
	m.pluginFormField = 0
	m.pluginFormName.SetValue("")
	m.pluginFormMarketplace.SetValue("")
	m.pluginFormAgents.SetValue("")
	m.pluginFormName.Blur()
	m.pluginFormMarketplace.Blur()
	m.pluginFormAgents.Blur()
}

// focusPluginFormField blurs every add-plugin field then focuses the one at
// m.pluginFormField, mirroring focusMcpFormField.
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

// buildPluginFromForm validates and constructs a config.Plugin from the
// add-plugin form's current field values. Name and marketplace are required;
// agents is an optional comma-separated list (empty means all MVP agents).
func (m *Model) buildPluginFromForm() (config.Plugin, error) {
	name := strings.TrimSpace(m.pluginFormName.Value())
	if name == "" {
		return config.Plugin{}, errors.New("name is required")
	}
	marketplace := strings.TrimSpace(m.pluginFormMarketplace.Value())
	if marketplace == "" {
		return config.Plugin{}, errors.New("marketplace is required")
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
	return config.Plugin{Name: name, Marketplace: marketplace, Agents: agents}, nil
}

package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
)

func (m *Model) doSaveAgentsUse(ids []string) tea.Cmd {
	if m.app == nil {
		return nil
	}
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		err := a.SaveAgentsUse(ctx, ids)
		return agentsUseSavedMsg{ids: ids, err: err}
	}
}

func (m *Model) doRestoreSkills() tea.Cmd {
	if m.app == nil {
		return nil
	}
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		res, _, err := a.RestoreSkills(ctx, app.RestoreSkillsOptions{})
		return skillsRestoredMsg{res: res, err: err}
	}
}

func (m *Model) doImportSkills() tea.Cmd {
	if m.app == nil {
		return nil
	}
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		diff, err := a.ImportSkills(ctx, app.ImportSkillsOptions{})
		return skillsImportedMsg{diff: diff, err: err}
	}
}

func (m *Model) doToggleAgents() tea.Cmd {
	if m.app == nil {
		return nil
	}
	a, ctx := m.app, m.ctx
	disable := m.agentsEnabled
	return func() tea.Msg {
		err := a.SaveAgentsDisabled(ctx, disable)
		return agentsToggledMsg{enabled: !disable, err: err}
	}
}

func (m *Model) doToggleSkillsFeature() tea.Cmd {
	if m.app == nil {
		return nil
	}
	a, ctx := m.app, m.ctx
	disable := m.skillsEnabled
	return func() tea.Msg {
		err := a.SaveSkillsDisabled(ctx, disable)
		return skillsFeatureToggledMsg{enabled: !disable, err: err}
	}
}

func (m *Model) doToggleMcpFeature() tea.Cmd {
	if m.app == nil {
		return nil
	}
	a, ctx := m.app, m.ctx
	disable := m.mcpEnabled
	return func() tea.Msg {
		err := a.SaveMcpDisabled(ctx, disable)
		return mcpFeatureToggledMsg{enabled: !disable, err: err}
	}
}

func (m *Model) doTogglePluginsFeature() tea.Cmd {
	if m.app == nil {
		return nil
	}
	a, ctx := m.app, m.ctx
	disable := m.pluginsEnabled
	return func() tea.Msg {
		err := a.SavePluginsDisabled(ctx, disable)
		return pluginsFeatureToggledMsg{enabled: !disable, err: err}
	}
}

func (m *Model) doUpdateSkills() tea.Cmd {
	if m.app == nil {
		return nil
	}
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		output, _, err := a.UpdateSkills(ctx, app.UpdateSkillsOptions{})
		return skillsUpdatedMsg{updated: output != "", err: err}
	}
}

func (m *Model) loadSkillsManifestCmd() tea.Cmd {
	return m.loadSkillsManifest(false)
}

// Only the explicit refresh key probes each package's source: a routine reload must stay offline and render the recorded verdict.
func (m *Model) loadSkillsManifest(recheckOutdated bool) tea.Cmd {
	a, ctx := m.app, m.ctx
	if a == nil {
		return nil
	}
	return func() tea.Msg {
		if recheckOutdated {
			if _, err := a.RefreshSkillOutdated(ctx, true); err != nil {
				return skillsManifestLoadedMsg{err: err}
			}
		}
		rows, unmanaged, err := a.SkillPackageRowState(ctx)
		if err != nil {
			return skillsManifestLoadedMsg{err: err}
		}
		return skillsManifestLoadedMsg{rows: rows, unmanaged: unmanaged}
	}
}

func (m *Model) doFindSkills(query string) tea.Cmd {
	if m.app == nil {
		return nil
	}
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		results, err := a.FindSkillPackages(ctx, query, "")
		return skillsFoundMsg{results: results, err: err}
	}
}

func (m *Model) doAddSkillPackage(source string) tea.Cmd {
	if m.app == nil {
		return nil
	}
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		_, warnings, err := a.AddSkillPackage(ctx, source)
		return skillAddedMsg{err: err, warning: skillWarningsText(warnings)}
	}
}

// Assigns the group in the same command so the manifest reload already reflects it; a failed group step leaves the adopt in place, since the package is correctly in the manifest by then.
func (m *Model) doAdoptSkillPackageWithGroup(source, group string, createdGroups []string, activeHost string) tea.Cmd {
	if m.app == nil {
		return nil
	}
	a := m.app
	return func() tea.Msg {
		if _, err := a.AdoptSkillPackage(source); err != nil {
			return skillAddedMsg{err: err}
		}
		if group != "" {
			if _, err := a.SetSkillGroupsWithState(m.ctx, source, []string{group}, createdGroups, activeHost); err != nil {
				return skillAddedMsg{err: err}
			}
		}
		return skillAddedMsg{}
	}
}

func (m *Model) doSetSkillAgents(source string, ids []string) tea.Cmd {
	a, ctx := m.app, m.ctx
	if a == nil {
		return nil
	}
	return func() tea.Msg {
		rows, err := a.SetSkillAgentsWithState(ctx, source, ids)
		return skillAgentsSavedMsg{rows: rows, err: err}
	}
}

func (m *Model) openSkillAgentsPicker(row app.SkillPackageRow) tea.Cmd {
	var pickerRows []app.SkillAgentRow
	if m.app != nil {
		var err error
		pickerRows, err = m.app.SkillAgentRows(row.Source)
		if err != nil {
			return setStatus(m, "✗ "+err.Error(), true)
		}
	}
	m.skillAgentsSource = row.Source
	m.skillAgentsRows = pickerRows
	m.skillAgentsCursor = 0
	m.skillAgentsPicker = true
	return nil
}

func (m *Model) doSetSkillGroupMemberships(source string, after, createdGroups []string, activeHost string) tea.Cmd {
	a, ctx := m.app, m.ctx
	if a == nil {
		return nil
	}
	return func() tea.Msg {
		rows, err := a.SetSkillGroupsWithState(ctx, source, after, createdGroups, activeHost)
		return skillsGroupsUpdatedMsg{rows: rows, err: err}
	}
}

type agentsBulkResolveDoneMsg struct {
	result app.BulkDriftResolution
	err    error
}

// Settles every drifted item with one side so a fleet of drift left by sync does not need one resolve per item.
func (m *Model) doAgentsBulkResolve(useManaged bool) tea.Cmd {
	if m.app == nil {
		return nil
	}
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		result, err := a.ResolveAllDrift(ctx, app.ResolveAllDriftOptions{UseManaged: useManaged})
		return agentsBulkResolveDoneMsg{result: result, err: err}
	}
}

type skillDriftResolvedMsg struct {
	res app.SkillDriftResolution
	err error
}

// The row is package-level, so the resolution settles every agent it drifted on.
func (m *Model) doResolveSkillDrift(source string, useLocal bool) tea.Cmd {
	if m.app == nil {
		return nil
	}
	a, ctx := m.app, m.ctx
	strategy := app.SkillDriftUseManaged
	if useLocal {
		strategy = app.SkillDriftUseLocal
	}
	return func() tea.Msg {
		res, err := a.ResolveSkillDrift(ctx, app.ResolveSkillDriftOptions{Source: source, Strategy: strategy})
		return skillDriftResolvedMsg{res: res, err: err}
	}
}

type mcpDriftResolvedMsg struct {
	warnings []string
	err      error
}

// The row names the server, so the resolution is server-level across every agent it drifted on.
func (m *Model) doResolveMcpDrift(name string, useLocal bool) tea.Cmd {
	if m.app == nil {
		return nil
	}
	a, ctx := m.app, m.ctx
	strategy := app.McpDriftUseManaged
	if useLocal {
		strategy = app.McpDriftUseLocal
	}
	return func() tea.Msg {
		res, err := a.ResolveMcpDrift(ctx, app.ResolveMcpDriftOptions{Name: name, Strategy: strategy})
		return mcpDriftResolvedMsg{warnings: res.Warnings, err: err}
	}
}

type pluginDriftResolvedMsg struct {
	warnings []string
	err      error
}

func (m *Model) doResolvePluginDrift(name string, useLocal bool) tea.Cmd {
	if m.app == nil {
		return nil
	}
	a, ctx := m.app, m.ctx
	strategy := app.PluginDriftUseManaged
	if useLocal {
		strategy = app.PluginDriftUseLocal
	}
	return func() tea.Msg {
		res, err := a.ResolvePluginDrift(ctx, app.ResolvePluginDriftOptions{Name: name, Strategy: strategy})
		return pluginDriftResolvedMsg{warnings: res.Warnings, err: err}
	}
}

type skillRemovedMsg struct{ err error }

func (m *Model) doRemoveSkillPackage(source string) tea.Cmd {
	if m.app == nil {
		return nil
	}
	a := m.app
	return func() tea.Msg {
		err := a.RemoveSkillPackage(source)
		return skillRemovedMsg{err: err}
	}
}

func (m *Model) doUninstallSkillPackage(source string) tea.Cmd {
	if m.app == nil {
		return nil
	}
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		err := a.UninstallSkillPackage(ctx, source)
		return skillRemovedMsg{err: err}
	}
}

type agentsIgnoreToggledMsg struct {
	feature    agentsSection
	name       string
	nowIgnored bool
	err        error
}

func (m *Model) doToggleAgentsIgnore(feature agentsSection, name string) tea.Cmd {
	if m.app == nil {
		return nil
	}
	a, ctx := m.app, m.ctx
	featureName := agentsIgnoreFeatureName(feature)
	return func() tea.Msg {
		nowIgnored, err := a.ToggleAgentsIgnore(ctx, featureName, name)
		return agentsIgnoreToggledMsg{feature: feature, name: name, nowIgnored: nowIgnored, err: err}
	}
}

func agentsIgnoreFeatureName(feature agentsSection) string {
	switch feature {
	case agentsSectionMcp:
		return "mcp"
	case agentsSectionPlugins:
		return "plugins"
	case agentsSectionMarketplaces:
		return "marketplaces"
	default:
		return "skills"
	}
}

func (m *Model) doReloadAgentsIgnore() tea.Cmd {
	if m.app == nil {
		return nil
	}
	a := m.app
	return func() tea.Msg {
		cfg, err := a.LoadConfig()
		if err != nil {
			return agentsIgnoreReloadedMsg{err: err}
		}
		return agentsIgnoreReloadedMsg{ignore: cfg.Agents.Ignore}
	}
}

type agentsIgnoreReloadedMsg struct {
	ignore app.AgentsIgnore
	err    error
}

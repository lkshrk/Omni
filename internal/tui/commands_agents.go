package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
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

func (m *Model) doLoadAgentsSummary() tea.Cmd {
	if m.app == nil {
		return nil
	}
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		cfg, err := a.LoadConfig()
		if err != nil {
			return agentsSummaryLoadedMsg{err: err}
		}
		summary, err := a.DashboardAgentsSummary(ctx, cfg)
		return agentsSummaryLoadedMsg{summary: summary, err: err}
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
	disable := m.agentsEnabled // currently enabled → disable, and vice versa
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
		_, _, err := a.UpdateSkills(ctx, app.UpdateSkillsOptions{})
		return skillsUpdatedMsg{err: err}
	}
}

func (m *Model) loadSkillsManifestCmd() tea.Cmd {
	a, ctx := m.app, m.ctx
	if a == nil {
		return nil
	}
	return func() tea.Msg {
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
		results, err := a.FindSkillPackages(ctx, query)
		return skillsFoundMsg{results: results, err: err}
	}
}

func (m *Model) doAddSkillPackage(source string) tea.Cmd {
	if m.app == nil {
		return nil
	}
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		_, err := a.AddSkillPackage(ctx, source)
		return skillAddedMsg{err: err}
	}
}

// doAdoptSkillPackageWithGroup adopts an orphan skill package then assigns it
// to group in one command, so the manifest reload skillAddedMsg triggers
// already reflects the chosen group membership. Skipped (group left at its
// adopt-time default) if the group-assignment step fails after a successful
// adopt, since the package is still correctly in the manifest at that point.
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

// doToggleAgentsIgnore toggles name's membership in feature's ignore list.
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

// doReloadAgentsIgnore reloads the manifest's ignore lists into m.agentsIgnore,
// the source agentsIgnoreSets reads from throughout the agents tab.
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
	ignore config.AgentsIgnore
	err    error
}

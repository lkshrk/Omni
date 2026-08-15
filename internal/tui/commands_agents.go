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

type mcpDriftResolvedMsg struct {
	warnings []string
	err      error
}

type pluginDriftResolvedMsg struct {
	warnings []string
	err      error
}

type skillRemovedMsg struct{ err error }

type agentsIgnoreToggledMsg struct {
	name       string
	nowIgnored bool
	err        error
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

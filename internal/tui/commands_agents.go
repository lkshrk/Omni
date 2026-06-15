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
	disable := m.agentsEnabled // currently enabled → disable, and vice versa
	return func() tea.Msg {
		err := a.SaveAgentsDisabled(ctx, disable)
		return agentsToggledMsg{enabled: !disable, err: err}
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
	a := m.app
	if a == nil {
		return nil
	}
	return func() tea.Msg {
		rows, err := a.SkillRows()
		return skillsManifestLoadedMsg{rows: rows, err: err}
	}
}

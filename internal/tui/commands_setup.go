package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	gosync "github.com/lkshrk/omni/internal/sync"
)

// No writes: skills uses ImportSkills' DryRun path, mcp/plugins are already diff-only.
func (m *Model) doSetupAgentsDiff() tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		skillRows, err := a.UnmanagedSkillPackages(ctx)
		if err != nil {
			return setupAgentsDiffMsg{err: err}
		}
		mcpDiff, err := a.ImportMcpServers(ctx)
		if err != nil {
			return setupAgentsDiffMsg{err: err}
		}
		pluginDiff, err := a.ImportPlugins(ctx)
		if err != nil {
			return setupAgentsDiffMsg{err: err}
		}
		mcpCount := 0
		for _, servers := range mcpDiff.Unmanaged {
			mcpCount += len(servers)
		}
		pluginCount := 0
		for _, plugins := range pluginDiff.Unmanaged {
			pluginCount += len(plugins)
		}
		return setupAgentsDiffMsg{
			unmanagedSkills:  len(skillRows),
			unmanagedMcp:     mcpCount,
			unmanagedPlugins: pluginCount,
		}
	}
}

// Skills additionally take over legacy CLI-managed directories, replacing them with links into Omni's store, which is why this runs only on explicit consent.
func (m *Model) doSetupAgentsImportAll() tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		skillsDiff, err := a.ImportSkills(ctx, app.ImportSkillsOptions{})
		if err != nil {
			return setupAgentsImportDoneMsg{err: err}
		}
		mcpAdopted, err := a.AdoptUnmanagedMcpServers(ctx)
		if err != nil {
			return setupAgentsImportDoneMsg{err: err}
		}
		pluginsAdopted, err := a.AdoptUnmanagedPlugins(ctx)
		if err != nil {
			return setupAgentsImportDoneMsg{err: err}
		}
		return setupAgentsImportDoneMsg{
			skills:     len(skillsDiff.Added),
			mcp:        mcpAdopted.Adopted,
			plugins:    pluginsAdopted.Adopted,
			advisories: setupImportAdvisories(skillsDiff, mcpAdopted, pluginsAdopted),
		}
	}
}

// The CLI's import output is the reference for what the user must be told; the wizard runs the same
// adoption and may not drop any of it. Ordered by severity rather than by channel: mcp adopt warnings
// are the ones raised for a credential that was written, and any surface that truncates the list must
// truncate the harmless entries first.
func setupImportAdvisories(skills app.ImportDiff, mcp app.McpAdoptResult, plugins app.PluginAdoptResult) []string {
	var out []string
	out = append(out, mcp.Warnings...)
	out = append(out, mcp.Skipped...)
	out = append(out, mcp.Conflicts...)
	out = append(out, plugins.Skipped...)
	out = append(out, skills.Warnings...)
	return out
}

func (m *Model) doCreateConfig() tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		snapshot, err := a.CreateEmptyConfigStartupState(ctx)
		if err != nil {
			return toolsLoadedMsg{err: err}
		}
		return toolsLoadedMsgFromStartupState(snapshot)
	}
}

func (m *Model) doImportConfigFile(path string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		if err := a.ImportConfigFile(path); err != nil {
			return setupConfigImportDoneMsg{path: path, err: err}
		}
		return setupConfigImportDoneMsg{path: path}
	}
}

func (m *Model) doSetupImport(disabled []string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		result, err := a.SetupImport(ctx, disabled)
		if err != nil {
			return setupImportDoneMsg{err: err}
		}
		return setupImportDoneMsg{
			added:    result.Added,
			hostInfo: result.HostInfo,
		}
	}
}

func (m *Model) doSetupHost(name string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		result, err := a.EnsureSetupHost(name)
		if err != nil {
			return setupHostDoneMsg{err: err}
		}
		return setupHostDoneMsg{hostName: result.Host.Host, info: result.Info}
	}
}

func (m *Model) doCopyHostGroupsFrom(host string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		result, err := a.CopyHostGroupsToCurrentHost(host)
		if err != nil {
			return hostCopiedMsg{err: err, host: host}
		}
		return hostCopiedMsg{host: host, info: result.Info}
	}
}

func (m *Model) doSetupCopyHostConfigFrom(source string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		result, err := a.CopyHostConfigToCurrentHost(source)
		if err != nil {
			return setupHostCopyDoneMsg{err: err, source: source, target: result.Target}
		}
		return setupHostCopyDoneMsg{source: result.Source, target: result.Target, info: result.Info}
	}
}

func (m *Model) doSetupHostGroups(groups []string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		result, err := a.SetCurrentHostGroups(groups)
		if err != nil {
			return setupHostGroupsDoneMsg{err: err, groups: groups}
		}
		return setupHostGroupsDoneMsg{groups: result.Groups, info: result.Info}
	}
}

func (m *Model) doSetupBootstrapTools() tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		result, err := a.Sync(ctx, gosync.SyncOptions{SkipPrivileged: true})
		if err != nil {
			return setupBootstrapDoneMsg{action: "sync-tools", err: err}
		}
		message := app.SetupBootstrapToolsMessage(result)
		return setupBootstrapDoneMsg{action: "sync-tools", message: message}
	}
}

func (m *Model) doSetupBootstrapDots() tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		ops, err := a.DotsSyncContext(ctx, app.DotSyncOptions{})
		if err != nil {
			return setupBootstrapDoneMsg{action: "sync-dots", err: err}
		}
		message := app.SetupBootstrapDotsMessage(ops)
		return setupBootstrapDoneMsg{action: "sync-dots", message: message}
	}
}

func (m *Model) doSetupDotsRepo(path string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		if _, err := a.ConfigureDotsRepo(ctx, path); err != nil {
			return dangerOpDoneMsg{action: "setup-dots", err: err}
		}
		return dangerOpDoneMsg{action: "setup-dots", detail: "dots configured"}
	}
}

func (m *Model) doSaveDisabledProviders(disabled []string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		err := a.SaveDisabledProviders(ctx, disabled)
		return setupProvidersDoneMsg{err: err}
	}
}

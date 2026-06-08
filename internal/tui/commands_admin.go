package tui

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/dots"
	"github.com/lkshrk/omni/internal/provider"
)

func (m *Model) doSetToolGroupMembership(name, group string, add bool) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		result, err := a.SetToolGroupMembershipWithState(ctx, name, group, add)
		if err != nil {
			return groupChangedMsg{err: err}
		}
		action := "added to"
		if !add {
			action = "removed from"
		}
		return groupChangedMsg{
			detail:          "✓ " + name + " " + action + " " + group,
			tools:           result.Tools,
			toolGroups:      result.State.ToolGroups,
			toolMemberships: result.State.ToolMemberships,
			groupNames:      result.State.GroupNames,
			info:            result.State.HostInfo,
		}
	}
}

func (m *Model) doSetToolGroupMemberships(name string, before, after, createdGroups []string, activeHost string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		result, err := a.SetToolGroupsWithState(ctx, name, after, createdGroups, activeHost)
		if err != nil {
			return groupChangedMsg{err: err}
		}
		return groupChangedMsg{
			detail:          groupMoveDetail(name, after),
			tools:           result.Tools,
			toolGroups:      result.State.ToolGroups,
			toolMemberships: result.State.ToolMemberships,
			groupNames:      result.State.GroupNames,
			info:            result.State.HostInfo,
		}
	}
}

func (m *Model) doSetDotGroupMemberships(name string, before, after, createdGroups []string, activeHost string) tea.Cmd {
	a, ctx := m.app, m.ctx
	_, gen := m.currentDotsOperation()
	return func() tea.Msg {
		change, err := a.SetDotGroupsWithState(ctx, name, after, createdGroups, activeHost)
		if err != nil {
			return dotsLoadedMsg{gen: gen, err: err}
		}
		msg := dotsLoadedMsg{
			gen:            gen,
			dotMemberships: change.DotMemberships,
			detail:         groupMoveDetail(name, after),
		}
		if change.DotsState != nil {
			msg.entries = change.DotsState.Entries
			msg.gitStatus = change.DotsState.GitStatus
			msg.dotMemberships = change.DotsState.DotMemberships
		}
		return msg
	}
}

// doExtractDotIntoGroups splits sub out of the parent dots entry into a new
// entry named name, then assigns that entry to the picked groups. Extract drops
// the new entry in the first group; SetDotGroups then reconciles the full set.
func (m *Model) doExtractDotIntoGroups(parent, sub, name string, after, createdGroups []string, activeHost string) tea.Cmd {
	a, ctx := m.app, m.ctx
	_, gen := m.currentDotsOperation()
	group := ""
	if len(after) > 0 {
		group = after[0]
	}
	return func() tea.Msg {
		if _, err := a.DotsExtract(ctx, parent, sub, app.DotsExtractOptions{Name: name, Group: group}); err != nil {
			return dotsLoadedMsg{gen: gen, err: err}
		}
		change, err := a.SetDotGroupsWithState(ctx, name, after, createdGroups, activeHost)
		if err != nil {
			return dotsLoadedMsg{gen: gen, err: err}
		}
		msg := dotsLoadedMsg{
			gen:            gen,
			dotMemberships: change.DotMemberships,
			detail:         "✓ extracted " + name + groupSuffix(after),
		}
		if change.DotsState != nil {
			msg.entries = change.DotsState.Entries
			msg.gitStatus = change.DotsState.GitStatus
			msg.dotMemberships = change.DotsState.DotMemberships
		}
		return msg
	}
}

func groupSuffix(groups []string) string {
	if len(groups) == 1 && groups[0] != "" {
		return " to " + groups[0]
	}
	return ""
}

func groupMoveDetail(name string, groups []string) string {
	if len(groups) == 1 && groups[0] != "" {
		return "✓ moved " + name + " to " + groups[0]
	}
	return "✓ updated group for " + name
}

func (m *Model) doSetGroupTools(group string, membership, originalMembership, ignores, originalIgnores map[string]bool) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		result, err := a.SetGroupToolsWithState(ctx, group, membership, originalMembership, ignores, originalIgnores)
		if err != nil {
			return groupToolsChangedMsg{err: err}
		}
		return groupToolsChangedMsg{
			detail:          fmt.Sprintf("✓ updated %d tool settings for %s", result.Changed, group),
			tools:           result.Tools,
			toolGroups:      result.State.ToolGroups,
			toolMemberships: result.State.ToolMemberships,
			groupNames:      result.State.GroupNames,
			ignoreLabels:    result.ScopeDisplay.IgnoreLabels,
			toolIgnoreSet:   result.ScopeDisplay.ToolIgnores,
			groupIgnoreSet:  result.ScopeDisplay.GroupIgnores,
		}
	}
}

func (m *Model) doSetGroupDots(group string, membership, originalMembership map[string]bool) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		change, err := a.SetGroupDotsWithState(ctx, group, membership, originalMembership)
		if err != nil {
			return groupDotsChangedMsg{err: err}
		}
		memberships := change.DotMemberships
		msg := groupDotsChangedMsg{
			detail:         fmt.Sprintf("✓ updated %d dotfiles for %s", change.Changed, group),
			dotMemberships: memberships,
		}
		if change.DotsState != nil {
			msg.entries = change.DotsState.Entries
			msg.gitStatus = change.DotsState.GitStatus
			msg.dotMemberships = change.DotsState.DotMemberships
		}
		return msg
	}
}

func (m *Model) doCreateGroup(name string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		state, err := a.CreateGroupWithState(ctx, name)
		if err != nil {
			return createGroupDoneMsg{err: err, name: name}
		}
		return createGroupDoneMsg{
			name:            name,
			groupNames:      state.GroupNames,
			toolGroups:      state.ToolGroups,
			toolMemberships: state.ToolMemberships,
			hostInfo:        state.HostInfo,
		}
	}
}

func (m *Model) doSaveSettingsChange(change app.SettingsChange) tea.Cmd {
	m.settingsSaveGen++
	gen := m.settingsSaveGen
	if m.settingsSaveRunning {
		m.settingsSaveQueued = true
		m.settingsSaveQueuedChanges = append(m.settingsSaveQueuedChanges, change)
		m.settingsSaveQueuedGen = gen
		return nil
	}
	return m.startSettingsChangeSave([]app.SettingsChange{change}, gen)
}

func (m *Model) appendSaveSettingsChangeCmd(cmds *[]tea.Cmd, change app.SettingsChange) {
	if cmd := m.doSaveSettingsChange(change); cmd != nil {
		*cmds = append(*cmds, cmd)
	}
}

func (m *Model) doToggleDotsReminderService(enable bool) tea.Cmd {
	a, ctx := m.app, m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	interval := m.currentDotsReminderInterval()
	return func() tea.Msg {
		if enable {
			service, err := a.InstallDotsReminderService(ctx, app.DotsReminderInstallOptions{Interval: interval, Notify: true, Activate: true})
			return dotsServiceChangedMsg{kind: dotsReminderServiceKind, enabled: true, reminder: service, err: err}
		}
		service, err := a.UninstallDotsReminderService(ctx)
		return dotsServiceChangedMsg{kind: dotsReminderServiceKind, enabled: false, reminder: service, err: err}
	}
}

func (m *Model) doToggleDotsWatchService(enable bool) tea.Cmd {
	a, ctx := m.app, m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	debounce := m.dotsWatchDebounceForServiceInstall()
	return func() tea.Msg {
		if enable {
			service, err := a.InstallDotsWatchService(ctx, app.DotsWatchInstallOptions{Debounce: debounce, Activate: true})
			return dotsServiceChangedMsg{kind: dotsWatchServiceKind, enabled: true, watch: service, err: err}
		}
		service, err := a.UninstallDotsWatchService(ctx)
		return dotsServiceChangedMsg{kind: dotsWatchServiceKind, enabled: false, watch: service, err: err}
	}
}

func (m *Model) doRunDoctor() tea.Cmd {
	a, ctx := m.app, m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		result, err := a.Doctor(ctx)
		return doctorDoneMsg{result: result, err: err}
	}
}

func (m *Model) startSettingsChangeSave(changes []app.SettingsChange, gen int) tea.Cmd {
	m.settingsSaveRunning = true
	m.settingsSaveInFlightGen = gen
	a, ctx := m.app, m.ctx
	changes = append([]app.SettingsChange(nil), changes...)
	return func() tea.Msg {
		settings, _, err := a.SaveSettingsChanges(ctx, changes...)
		if err != nil {
			return settingsSavedMsg{gen: gen, err: err}
		}
		return settingsSavedMsg{gen: gen, settings: settings, hasSettings: true}
	}
}

func (m *Model) doSaveDotsRepoAndSync(repo string) tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	return func() tea.Msg {
		result, err := a.SaveDotsRepoAndSync(ctx, repo, dots.SyncOptions{})
		entries, gitStatus, memberships := dotsSnapshotFromState(result)
		msg := dotsSyncedMsg{gen: gen, entries: entries, gitStatus: gitStatus, dotMemberships: memberships, err: err}
		if result != nil && result.HasSettings {
			msg.settings = result.Settings
			msg.hasSettings = true
		}
		return msg
	}
}

// selectedHostName returns the host name at the current host-assignment cursor, or "".
func (m *Model) selectedHostName() string {
	hosts := app.PrioritizedHostSummaries(m.hostInfo)
	if len(hosts) == 0 {
		return ""
	}
	if m.hostCursor >= 0 && m.hostCursor < len(hosts) {
		return hosts[m.hostCursor].Name
	}
	return ""
}

func (m *Model) doAddGroupToHost(host, group string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		state, err := a.AddGroupToHostWithState(ctx, host, group)
		if err != nil {
			return hostGroupChangedMsg{err: err, host: host, group: group, added: true}
		}
		return hostGroupChangedMsg{
			host:            host,
			group:           group,
			added:           true,
			info:            state.HostInfo,
			groupNames:      state.GroupNames,
			toolGroups:      state.ToolGroups,
			toolMemberships: state.ToolMemberships,
		}
	}
}

func (m *Model) doRemoveGroupFromHost(host, group string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		state, err := a.RemoveGroupFromHostWithState(ctx, host, group)
		if err != nil {
			return hostGroupChangedMsg{err: err, host: host, group: group, added: false}
		}
		return hostGroupChangedMsg{
			host:            host,
			group:           group,
			added:           false,
			info:            state.HostInfo,
			groupNames:      state.GroupNames,
			toolGroups:      state.ToolGroups,
			toolMemberships: state.ToolMemberships,
		}
	}
}

func (m *Model) doSetHostGroups(host string, _ []string, after, createdGroups []string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		state, err := a.SetHostGroupsWithState(ctx, host, after, createdGroups)
		if err != nil {
			return hostGroupChangedMsg{err: err, host: host}
		}
		return hostGroupChangedMsg{
			host:            host,
			detail:          "✓ groups updated for host " + host,
			info:            state.HostInfo,
			groupNames:      state.GroupNames,
			toolGroups:      state.ToolGroups,
			toolMemberships: state.ToolMemberships,
		}
	}
}

func (m *Model) doRemoveHostFromTab(name string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		state, err := a.RemoveHostWithState(ctx, name)
		if err != nil {
			return hostGroupChangedMsg{err: err, host: name}
		}
		return hostGroupChangedMsg{
			host:            name,
			info:            state.HostInfo,
			groupNames:      state.GroupNames,
			toolGroups:      state.ToolGroups,
			toolMemberships: state.ToolMemberships,
		}
	}
}

func (m *Model) doRenameHost(oldName, newName string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		state, err := a.RenameHostWithState(ctx, oldName, newName)
		if err != nil {
			return hostGroupChangedMsg{err: err, host: oldName}
		}
		return hostGroupChangedMsg{
			host:            newName,
			detail:          "✓ " + oldName + " renamed to " + newName,
			info:            state.HostInfo,
			groupNames:      state.GroupNames,
			toolGroups:      state.ToolGroups,
			toolMemberships: state.ToolMemberships,
		}
	}
}

// doRenameGroup renames a group in config and reloads group/host data.
func (m *Model) doRenameGroup(oldName, newName string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		state, err := a.RenameGroupWithState(ctx, oldName, newName)
		if err != nil {
			return groupChangedMsg{err: err}
		}
		return groupChangedMsg{
			detail:          "✓ renamed " + oldName + " → " + newName,
			groupNames:      state.GroupNames,
			toolGroups:      state.ToolGroups,
			toolMemberships: state.ToolMemberships,
			info:            state.HostInfo,
		}
	}
}

// doDeleteGroup removes a group, then reloads group/host data.
func (m *Model) doDeleteGroup(name string, deleteTools bool) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		detail := "✓ deleted group " + name + " (tools moved to this host)"
		if deleteTools {
			detail = "✓ deleted group " + name + " (last-membership tools deleted)"
		}
		result, err := a.DeleteGroupWithDefaultMoveTarget(ctx, name, deleteTools)
		if err != nil {
			return groupChangedMsg{err: err}
		}
		return groupChangedMsg{
			detail:          detail,
			tools:           result.Tools,
			groupNames:      result.State.GroupNames,
			toolGroups:      result.State.ToolGroups,
			toolMemberships: result.State.ToolMemberships,
			info:            result.State.HostInfo,
		}
	}
}

// doRemoveHost removes the named host from settings.json then reloads tools.
func (m *Model) doRemoveHost(name string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		if err := a.RemoveHost(name); err != nil {
			return dangerOpDoneMsg{action: "delete-host", err: err}
		}
		return dangerOpDoneMsg{action: "delete-host", detail: name + " deleted", reload: true}
	}
}

// doResetSettings resets settings to defaults then reloads tools.
func (m *Model) doResetSettings() tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		if err := a.ResetSettings(ctx); err != nil {
			return dangerOpDoneMsg{action: "reset-settings", err: err}
		}
		return dangerOpDoneMsg{action: "reset-settings", detail: "settings cleared", reload: true}
	}
}

// doResetCache deletes and reinitialises the tool cache DB, then reloads tools.
func (m *Model) doResetCache() tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		if err := a.ResetCache(ctx); err != nil {
			return dangerOpDoneMsg{action: "reset-cache", err: err}
		}
		return dangerOpDoneMsg{action: "reset-cache", detail: "cache cleared", reload: true}
	}
}

// doDisableDots removes managed symlinks when dots is configured, optionally
// keeping local materialized copies, then persists dots_disabled=true for this
// machine. Regular settings flows trigger a full settings reload; setup can
// suppress that reload while advancing to the next onboarding step. Safe to
// call when dots is not yet configured — the physical unlink step is skipped in
// that case.
func (m *Model) doDisableDots(keepLocal bool, setupComplete ...bool) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		ops, err := a.DisableDotsForHost(ctx, app.DisableDotsOptions{
			KeepExistingLocal: keepLocal,
			RemoveLocal:       !keepLocal,
		})
		detail := app.DotsDisableDetail(ops)
		done := len(setupComplete) > 0 && setupComplete[0]
		reload := len(setupComplete) == 0 || done
		if err != nil {
			return dangerOpDoneMsg{action: "disable-dots", detail: detail, reload: reload, setupComplete: done, err: err}
		}
		return dangerOpDoneMsg{action: "disable-dots", detail: detail, reload: reload, setupComplete: done}
	}
}

// doEnableDots re-enables dotfile sync: clears the disabled flag and runs a
// sync to restore managed symlinks.
func (m *Model) doEnableDots() tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	return func() tea.Msg {
		if _, err := a.EnableDotsForHost(ctx); err != nil {
			return dangerOpDoneMsg{action: "enable-dots", dotsGen: gen, err: err}
		}
		return dangerOpDoneMsg{action: "enable-dots", dotsGen: gen, detail: "dots enabled", reload: true, mode: viewDots}
	}
}

// doConsolidate switches all tools in ecosystem to manager.
func (m *Model) doConsolidate(ecosystem, manager string) tea.Cmd {
	a, ctx := m.app, m.beginCancellableAction()
	return func() tea.Msg {
		stateResult, err := a.ConsolidateWithState(ctx, ecosystem, manager, nil)
		if err != nil {
			return opCompleteMsg{err: err}
		}
		if stateResult == nil || stateResult.Result == nil {
			return opCompleteMsg{err: fmt.Errorf("consolidate result unavailable")}
		}
		result := stateResult.Result
		groupState := stateResult.State
		return opCompleteMsg{
			message:         app.ConsolidateSummaryText(result, manager),
			tools:           stateResult.Tools,
			groupNames:      groupState.GroupNames,
			toolGroups:      groupState.ToolGroups,
			toolMemberships: groupState.ToolMemberships,
			hostInfo:        groupState.HostInfo,
		}
	}
}

// doClaim adds an orphan tool (installed but not in config) to the given group.
// Pass groupName="" to add to the current host group. installedWith is the
// concrete manager the tool was installed with (e.g. bun), used to pin
// install_with so a single claim matches the bulk `sync --all` claim.
func (m *Model) doClaim(name, prov, installedWith, groupName string, activeHost ...string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		var assignHosts []string
		if len(activeHost) > 0 && activeHost[0] != "" {
			assignHosts = append(assignHosts, activeHost[0])
		}
		result, err := a.AddWithState(ctx, app.AddToolOptions{
			ProviderName: prov,
			Package:      name,
			Name:         name,
			GroupName:    groupName,
			InstallWith:  a.ClaimInstallWith(ctx, prov, installedWith),
			AssignHosts:  assignHosts,
		})
		msg := claimDoneMsg{err: err, name: name, groupName: groupName}
		if result == nil || result.State == nil {
			return msg
		}
		msg.tools = result.Tools
		msg.groupNames = result.State.GroupNames
		msg.toolGroups = result.State.ToolGroups
		msg.toolMemberships = result.State.ToolMemberships
		msg.hostInfo = result.State.HostInfo
		return msg
	}
}

func (m *Model) doSetIgnoreScope(name string, opt scopeOption) tea.Cmd {
	opt.initialChecked = opt.checked
	opt.checked = !opt.checked
	return m.doSaveIgnoreScopes(name, []scopeOption{opt})
}

func (m *Model) doSaveIgnoreScopes(name string, options []scopeOption) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		var changes []app.ToolIgnoreScopeChange
		for _, opt := range options {
			if opt.checked == opt.initialChecked {
				continue
			}
			change := app.ToolIgnoreScopeChange{Ignored: opt.checked, Group: opt.group}
			switch opt.kind {
			case "tool":
				change.Kind = app.ToolIgnoreScopeTool
			case "group":
				change.Kind = app.ToolIgnoreScopeGroup
			case "host":
				change.Kind = app.ToolIgnoreScopeHost
			default:
				return ignoreDoneMsg{err: fmt.Errorf("unknown ignore scope %q", opt.kind), name: name, ignored: opt.checked}
			}
			changes = append(changes, change)
		}
		result, err := a.SetToolIgnoreScopesWithState(ctx, name, changes)
		if err != nil {
			desired := false
			if len(changes) > 0 {
				desired = changes[len(changes)-1].Ignored
			}
			return ignoreDoneMsg{err: err, name: name, ignored: desired}
		}
		msg := ignoreDoneMsg{
			name:           name,
			ignored:        result.Ignored,
			tools:          result.Tools,
			hostScope:      result.HostScopeChanged,
			ignoreLabels:   result.ScopeDisplay.IgnoreLabels,
			toolIgnoreSet:  result.ScopeDisplay.ToolIgnores,
			groupIgnoreSet: result.ScopeDisplay.GroupIgnores,
		}
		return msg
	}
}

// doInstallAndAdd installs a tool and adds it to the selected config group.
// Used for search results and orphan tools that are not yet in config.
func (m *Model) doInstallAndAdd(name, prov string, groupAndHost ...string) tea.Cmd {
	return m.doInstallAndAddTool(&database.ToolCache{Name: name, Provider: prov, Package: name}, groupAndHost...)
}

func (m *Model) doInstallAndAddTool(t *database.ToolCache, groupAndHost ...string) tea.Cmd {
	a, ctx := m.app, m.beginCancellableAction()
	return func() tea.Msg {
		if t == nil {
			return opCompleteMsg{err: fmt.Errorf("missing tool")}
		}
		name := t.Name
		pkg := t.Package
		if pkg == "" {
			pkg = name
		}
		group := ""
		if len(groupAndHost) > 0 {
			group = groupAndHost[0]
		}
		var assignHosts []string
		if len(groupAndHost) > 1 && groupAndHost[1] != "" {
			assignHosts = append(assignHosts, groupAndHost[1])
		}
		removeDiscovered := []string{toolKey(name, t.Provider)}
		result, err := a.InstallAndAddWithState(ctx, app.AddToolOptions{
			ProviderName: t.Provider,
			Package:      pkg,
			Name:         name,
			GroupName:    group,
			InstallWith:  t.InstalledWith,
			Options:      t.Options,
			AssignHosts:  assignHosts,
		})
		if result == nil || result.State == nil {
			return opCompleteMsg{err: err}
		}
		msg := opCompleteMsg{
			err:                  err,
			tools:                result.Tools,
			removeDiscoveredKeys: removeDiscovered,
			groupNames:           result.State.GroupNames,
			toolGroups:           result.State.ToolGroups,
			toolMemberships:      result.State.ToolMemberships,
			hostInfo:             result.State.HostInfo,
		}
		if err == nil {
			msg.message = "installed " + name + " and added to config"
		}
		return msg
	}
}

func (m *Model) doSetProviderScope(name string, opt scopeOption, t *database.ToolCache) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		if t == nil || t.InstalledWith == "" {
			return opCompleteMsg{err: fmt.Errorf("provider pin: installed provider is unknown")}
		}
		result, err := a.SetToolProviderScopeWithState(ctx, name, app.ToolProviderScopeOptions{
			Kind:         app.ToolProviderScopeKind(opt.kind),
			ProviderName: t.Provider,
			Package:      t.Package,
			InstallWith:  t.InstalledWith,
		})
		if err != nil {
			return opCompleteMsg{err: fmt.Errorf("pin provider: %w", err)}
		}
		return opCompleteMsg{message: "pinned " + name + " via " + opt.label, tools: result.Tools, toolProviderPins: result.ScopeDisplay.ToolProviderPins}
	}
}

func (m *Model) doClearProviderOverride(name, configProv, installedWith string) tea.Cmd {
	a, ctx := m.app, m.beginCancellableAction()
	return func() tea.Msg {
		result, err := a.ClearProviderOverrideWithState(ctx, name, configProv, installedWith)
		fromProvider := installedWith
		toProvider := configProv
		var tools []*database.ToolCache
		var pins map[string]string
		if result != nil {
			fromProvider = result.FromProvider
			toProvider = result.ToProvider
			tools = result.Tools
			if result.ScopeDisplay != nil {
				pins = result.ScopeDisplay.ToolProviderPins
			}
		}
		if err != nil {
			return migrateProviderDoneMsg{err: fmt.Errorf("remove provider override: %w", err), name: name, fromProvider: fromProvider, toProvider: toProvider, clearedProviderOverride: true}
		}
		return migrateProviderDoneMsg{name: name, fromProvider: fromProvider, toProvider: toProvider, tools: tools, toolProviderPins: pins, clearedProviderOverride: true}
	}
}

func (m *Model) doMigrateProvider(name, configProv, installedWith string) tea.Cmd {
	a, ctx := m.app, m.beginCancellableAction()
	return func() tea.Msg {
		result, err := a.MigrateInstallationWithState(ctx, name, installedWith, configProv)
		var tools []*database.ToolCache
		if result != nil {
			tools = result.Tools
		}
		if err != nil {
			return migrateProviderDoneMsg{err: fmt.Errorf("reinstall with default: %w", err), name: name, fromProvider: installedWith, toProvider: configProv}
		}
		return migrateProviderDoneMsg{name: name, fromProvider: installedWith, toProvider: configProv, tools: tools}
	}
}

func (m *Model) doApplyProviderSolution(name, fromProvider string, solution provider.ErrorSolution) tea.Cmd {
	a, ctx := m.app, m.beginCancellableAction()
	target := solution.TargetProvider
	return func() tea.Msg {
		key := toolKey(name, fromProvider)
		result, err := a.ApplyProviderSolutionWithState(ctx, name, fromProvider, solution)
		var tools []*database.ToolCache
		if result != nil {
			tools = result.Tools
		}
		if err != nil {
			return opCompleteMsg{key: key, err: fmt.Errorf("apply fix: %w", err)}
		}
		message := fmt.Sprintf("reinstalled %s with %s", name, target)
		if result != nil && result.Result != nil && result.Result.UninstallWarning != nil {
			message += ", cleanup warning"
		}
		return opCompleteMsg{key: key, message: message, tools: tools}
	}
}

// openFilePicker configures and opens the file picker popup.
// title is shown as the popup header.
// currentPath sets the starting directory: opens at the path itself if it
// already exists as a directory, otherwise falls back to its parent (or home).
// allowFiles controls whether plain files are selectable in addition to directories.
// Returns any command needed to initialize the picker.
func (m *Model) openFilePicker(title, currentPath string, allowFiles bool) tea.Cmd {
	fp, cmd := newPathPicker(currentPath, allowFiles, filePickerContentWidth(*m), filePickerListHeight(*m))

	m.dotsFilePicker = fp
	m.filePickerTitle = title
	m.filePickerAllowFiles = allowFiles
	m.showFilePicker = true
	return cmd
}

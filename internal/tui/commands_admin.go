package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/dots"
	"github.com/lkshrk/omni/internal/provider"
)

func (m *Model) doSetToolGroupMembership(name, group string, add bool) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		var err error
		if add {
			err = a.MoveToolToGroup(name, group)
		} else {
			err = a.RemoveToolFromGroup(name, group)
		}
		if err != nil {
			return groupChangedMsg{err: err}
		}
		groupNames, toolGroups, memberships := m.reloadToolGroups()
		tools, _ := a.ListTools(ctx, "")
		action := "added to"
		if !add {
			action = "removed from"
		}
		return groupChangedMsg{
			detail:          "✓ " + name + " " + action + " " + group,
			tools:           tools,
			toolGroups:      toolGroups,
			toolMemberships: memberships,
			groupNames:      groupNames,
		}
	}
}

func (m *Model) doSetToolGroupMemberships(name string, before, after, createdGroups []string, activeHost string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		for _, group := range createdGroups {
			if err := a.CreateGroup(group); err != nil {
				return groupChangedMsg{err: err}
			}
		}
		beforeSet := stringSet(before)
		afterSet := stringSet(after)
		if err := ensureMembershipGroupsOnHost(a, activeHost, afterSet); err != nil {
			return groupChangedMsg{err: err}
		}
		for group := range beforeSet {
			if !afterSet[group] {
				if err := a.RemoveToolFromGroup(name, group); err != nil {
					return groupChangedMsg{err: err}
				}
			}
		}
		for group := range afterSet {
			if !beforeSet[group] {
				if err := a.MoveToolToGroup(name, group); err != nil {
					return groupChangedMsg{err: err}
				}
			}
		}
		groups, _ := a.Groups(ctx)
		memberships, _ := a.ToolMembershipMap(ctx)
		info, _ := a.HostStatus()
		tools, _ := a.ListTools(ctx, "")
		return groupChangedMsg{
			detail:          groupMoveDetail(name, after),
			tools:           tools,
			toolGroups:      compactToolGroupMapForHost(memberships, info),
			toolMemberships: memberships,
			groupNames:      buildGroupNames(groups),
			info:            info,
		}
	}
}

func (m *Model) doSetDotGroupMemberships(name string, before, after, createdGroups []string, activeHost string) tea.Cmd {
	a, ctx := m.app, m.ctx
	_, gen := m.currentDotsOperation()
	return func() tea.Msg {
		for _, group := range createdGroups {
			if err := a.CreateGroup(group); err != nil {
				return dotsLoadedMsg{gen: gen, err: err}
			}
		}
		beforeSet := stringSet(before)
		afterSet := stringSet(after)
		if err := ensureMembershipGroupsOnHost(a, activeHost, afterSet); err != nil {
			return dotsLoadedMsg{gen: gen, err: err}
		}
		for group := range afterSet {
			if !beforeSet[group] {
				if err := a.MoveDotToGroup(name, group); err != nil {
					return dotsLoadedMsg{gen: gen, err: err}
				}
			}
		}
		for group := range beforeSet {
			if !afterSet[group] {
				if err := a.RemoveDotFromGroup(name, group); err != nil {
					return dotsLoadedMsg{gen: gen, err: err}
				}
			}
		}
		result, err := a.DiscoverDotsStatus(ctx)
		if err != nil {
			return dotsLoadedMsg{gen: gen, err: err}
		}
		return dotsLoadedMsg{
			gen:            gen,
			entries:        result.Entries,
			gitStatus:      result.GitStatus,
			dotMemberships: loadDotMemberships(a, ctx),
			detail:         groupMoveDetail(name, after),
		}
	}
}

func groupMoveDetail(name string, groups []string) string {
	if len(groups) == 1 && groups[0] != "" {
		return "✓ moved " + name + " to " + groups[0]
	}
	return "✓ updated group for " + name
}

func ensureMembershipGroupsOnHost(a *app.App, activeHost string, groups map[string]bool) error {
	if activeHost == "" {
		return nil
	}
	for group := range groups {
		if group == "" {
			continue
		}
		if err := a.AddGroupToHost(activeHost, group); err != nil {
			return err
		}
	}
	return nil
}

func (m *Model) doSetGroupTools(group string, membership, originalMembership, ignores, originalIgnores map[string]bool) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		changed := 0
		for name, desired := range membership {
			if originalMembership[name] == desired {
				continue
			}
			var err error
			if desired {
				err = a.MoveToolToGroup(name, group)
			} else {
				err = a.RemoveToolFromGroup(name, group)
			}
			if err != nil {
				return groupToolsChangedMsg{err: err}
			}
			changed++
		}
		for name, desired := range ignores {
			if originalIgnores[name] == desired {
				continue
			}
			if err := a.SetGroupIgnore(group, name, desired); err != nil {
				return groupToolsChangedMsg{err: err}
			}
			changed++
		}
		groups, _ := a.Groups(ctx)
		memberships, _ := a.ToolMembershipMap(ctx)
		info, _ := a.HostStatus()
		tools, _ := a.ListTools(ctx, "")
		var hostIgnore []string
		if info != nil && info.Active != "" {
			if prof, ok := info.Hosts[info.Active]; ok {
				hostIgnore = prof.Ignore
			}
		}
		labels := buildIgnoreLabels(a.ConfigPath, groups, hostIgnore)
		toolIgnores, groupIgnores, _ := buildToolScopeState(a.ConfigPath, groups)
		return groupToolsChangedMsg{
			detail:          fmt.Sprintf("✓ updated %d tool settings for %s", changed, group),
			tools:           tools,
			toolGroups:      compactToolGroupMapForHost(memberships, info),
			toolMemberships: memberships,
			groupNames:      buildGroupNames(groups),
			ignoreLabels:    labels,
			toolIgnoreSet:   toolIgnores,
			groupIgnoreSet:  groupIgnores,
		}
	}
}

func (m *Model) doSetGroupDots(group string, membership, originalMembership map[string]bool) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		changed := 0
		for name, desired := range membership {
			if originalMembership[name] == desired {
				continue
			}
			var err error
			if desired {
				err = a.MoveDotToGroup(name, group)
			} else {
				err = a.RemoveDotFromGroup(name, group)
			}
			if err != nil {
				return groupDotsChangedMsg{err: err}
			}
			changed++
		}
		memberships := loadDotMemberships(a, ctx)
		msg := groupDotsChangedMsg{
			detail:         fmt.Sprintf("✓ updated %d dotfiles for %s", changed, group),
			dotMemberships: memberships,
		}
		settings, _ := a.LoadSettings()
		if settings.DotsRepo == "" {
			return msg
		}
		result, err := a.DiscoverDotsStatus(ctx)
		if err != nil {
			return groupDotsChangedMsg{err: err, dotMemberships: memberships}
		}
		msg.entries = result.Entries
		msg.gitStatus = result.GitStatus
		return msg
	}
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

// doCreateGroup creates a new empty named group in config.
func (m *Model) doCreateGroup(name string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		if err := a.CreateGroup(name); err != nil {
			return createGroupDoneMsg{err: err, name: name}
		}
		if err := a.AddGroupToHost(shortHostname(), name); err != nil {
			return createGroupDoneMsg{err: err, name: name}
		}
		groupNames, toolGroups, memberships, info := m.reloadToolContext()
		return createGroupDoneMsg{
			name:            name,
			groupNames:      groupNames,
			toolGroups:      toolGroups,
			toolMemberships: memberships,
			hostInfo:        info,
		}
	}
}

// doSaveSettings persists the current settings to the config file.
func (m *Model) doSaveSettings() tea.Cmd {
	m.settingsSaveGen++
	gen := m.settingsSaveGen
	snapshot := cloneSettingsSnapshot(m.settings)
	if m.settingsSaveRunning {
		m.settingsSaveQueued = true
		m.settingsSaveQueuedSnapshot = snapshot
		m.settingsSaveQueuedGen = gen
		return nil
	}
	return m.startSettingsSave(snapshot, gen)
}

func (m *Model) appendSaveSettingsCmd(cmds *[]tea.Cmd) {
	if cmd := m.doSaveSettings(); cmd != nil {
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

func (m *Model) startSettingsSave(settings config.Settings, gen int) tea.Cmd {
	m.settingsSaveRunning = true
	m.settingsSaveInFlightGen = gen
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		return settingsSavedMsg{gen: gen, err: a.SaveSettings(ctx, settings)}
	}
}

func cloneSettingsSnapshot(in config.Settings) config.Settings {
	out := in
	if in.Ecosystems != nil {
		out.Ecosystems = make(map[string]config.EcosystemSettings, len(in.Ecosystems))
		for name, eco := range in.Ecosystems {
			eco.Priority = append([]string(nil), eco.Priority...)
			out.Ecosystems[name] = eco
		}
	}
	if in.DotsDisabled != nil {
		dotsDisabled := *in.DotsDisabled
		out.DotsDisabled = &dotsDisabled
	}
	if in.DisabledProviders != nil {
		out.DisabledProviders = append([]string{}, in.DisabledProviders...)
	}
	return out
}

func (m *Model) doSaveSettingsAndDotsSync(settings config.Settings) tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	snapshot := cloneSettingsSnapshot(settings)
	return func() tea.Msg {
		if err := a.SaveSettings(ctx, snapshot); err != nil {
			return dotsSyncedMsg{gen: gen, err: fmt.Errorf("save dots repo: %w", err)}
		}
		_, syncErr := a.DotsSyncContext(ctx, dots.SyncOptions{})
		result, err := a.DiscoverDotsStatus(ctx)
		if err != nil {
			if result != nil {
				return dotsSyncedMsg{gen: gen, entries: result.Entries, gitStatus: result.GitStatus, dotMemberships: loadDotMemberships(a, ctx), err: combineDotsErrors(syncErr, err)}
			}
			return dotsSyncedMsg{gen: gen, err: combineDotsErrors(syncErr, err)}
		}
		return dotsSyncedMsg{gen: gen, entries: result.Entries, gitStatus: result.GitStatus, dotMemberships: loadDotMemberships(a, ctx), err: syncErr}
	}
}

// selectedHostName returns the host name at the current host-assignment cursor, or "".
func (m *Model) selectedHostName() string {
	if m.hostInfo == nil || len(m.hostInfo.Hosts) == 0 {
		return ""
	}
	names := make([]string, 0, len(m.hostInfo.Hosts))
	for n := range m.hostInfo.Hosts {
		names = append(names, n)
	}
	sort.Strings(names)
	if m.hostCursor >= 0 && m.hostCursor < len(names) {
		return names[m.hostCursor]
	}
	return ""
}

// doAddGroupToHost appends a group to a host and reloads host info.
func (m *Model) doAddGroupToHost(host, group string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		if err := a.AddGroupToHost(host, group); err != nil {
			return hostGroupChangedMsg{err: err, host: host, group: group, added: true}
		}
		groupNames, toolGroups, memberships, info := m.reloadToolContext()
		return hostGroupChangedMsg{host: host, group: group, added: true, info: info, groupNames: groupNames, toolGroups: toolGroups, toolMemberships: memberships}
	}
}

// doRemoveGroupFromHost removes a group from a host and reloads host info.
func (m *Model) doRemoveGroupFromHost(host, group string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		if err := a.RemoveGroupFromHost(host, group); err != nil {
			return hostGroupChangedMsg{err: err, host: host, group: group, added: false}
		}
		groupNames, toolGroups, memberships, info := m.reloadToolContext()
		return hostGroupChangedMsg{host: host, group: group, added: false, info: info, groupNames: groupNames, toolGroups: toolGroups, toolMemberships: memberships}
	}
}

// doSetHostGroups persists a staged host group-assignment edit.
func (m *Model) doSetHostGroups(host string, before, after, createdGroups []string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		beforeSet := make(map[string]bool, len(before))
		afterSet := make(map[string]bool, len(after))
		for _, group := range before {
			beforeSet[group] = true
		}
		for _, group := range after {
			afterSet[group] = true
		}
		for _, group := range createdGroups {
			if afterSet[group] {
				if err := a.CreateGroup(group); err != nil {
					return hostGroupChangedMsg{err: err, host: host}
				}
			}
		}
		for group := range beforeSet {
			if !afterSet[group] {
				if err := a.RemoveGroupFromHost(host, group); err != nil {
					return hostGroupChangedMsg{err: err, host: host}
				}
			}
		}
		for group := range afterSet {
			if !beforeSet[group] {
				if err := a.AddGroupToHost(host, group); err != nil {
					return hostGroupChangedMsg{err: err, host: host}
				}
			}
		}
		groupNames, toolGroups, memberships, info := m.reloadToolContext()
		return hostGroupChangedMsg{host: host, detail: "✓ groups updated for host " + host, info: info, groupNames: groupNames, toolGroups: toolGroups, toolMemberships: memberships}
	}
}

// doRemoveHostFromTab removes a host from settings.json and reloads host info.
func (m *Model) doRemoveHostFromTab(name string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		if err := a.RemoveHost(name); err != nil {
			return hostGroupChangedMsg{err: err, host: name}
		}
		groupNames, toolGroups, memberships, info := m.reloadToolContext()
		return hostGroupChangedMsg{host: name, info: info, groupNames: groupNames, toolGroups: toolGroups, toolMemberships: memberships}
	}
}

// doRenameHost renames a host and reloads host info.
func (m *Model) doRenameHost(oldName, newName string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		if err := a.RenameHost(oldName, newName); err != nil {
			return hostGroupChangedMsg{err: err, host: oldName}
		}
		groupNames, toolGroups, memberships, info := m.reloadToolContext()
		return hostGroupChangedMsg{host: newName, detail: "✓ " + oldName + " renamed to " + newName, info: info, groupNames: groupNames, toolGroups: toolGroups, toolMemberships: memberships}
	}
}

// doRenameGroup renames a group in config and reloads group/host data.
func (m *Model) doRenameGroup(oldName, newName string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		if err := a.RenameGroup(oldName, newName); err != nil {
			return groupChangedMsg{err: err}
		}
		groupNames, toolGroups, memberships := m.reloadToolGroups()
		info, _ := a.HostStatus()
		return groupChangedMsg{
			detail:          "✓ renamed " + oldName + " → " + newName,
			groupNames:      groupNames,
			toolGroups:      toolGroups,
			toolMemberships: memberships,
			info:            info,
		}
	}
}

// doDeleteGroup removes a group, then reloads group/host data.
func (m *Model) doDeleteGroup(name string, deleteTools bool) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		opts := app.DeleteGroupOptions{MoveTo: shortHostname()}
		detail := "✓ deleted group " + name + " (tools moved to this host)"
		if deleteTools {
			opts = app.DeleteGroupOptions{DeleteTools: true}
			detail = "✓ deleted group " + name + " (last-membership tools deleted)"
		}
		if err := a.DeleteGroup(ctx, name, opts); err != nil {
			return groupChangedMsg{err: err}
		}
		groupNames, toolGroups, memberships := m.reloadToolGroups()
		tools, _ := a.ListTools(ctx, "")
		info, _ := a.HostStatus()
		return groupChangedMsg{
			detail:          detail,
			tools:           tools,
			groupNames:      groupNames,
			toolGroups:      toolGroups,
			toolMemberships: memberships,
			info:            info,
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
		detail := dotsDisableDetail(ops)
		done := len(setupComplete) > 0 && setupComplete[0]
		reload := len(setupComplete) == 0 || done
		if err != nil {
			return dangerOpDoneMsg{action: "disable-dots", detail: detail, reload: reload, setupComplete: done, err: err}
		}
		return dangerOpDoneMsg{action: "disable-dots", detail: detail, reload: reload, setupComplete: done}
	}
}

func dotsDisableDetail(ops []dots.Op) string {
	if len(ops) == 0 {
		return "dots disabled"
	}
	var unlinked, conflicts int
	for _, op := range ops {
		switch op.Kind {
		case dots.OpUnlink:
			unlinked++
		case dots.OpUnlinkConflict:
			conflicts++
		}
	}
	detail := fmt.Sprintf("%d unlinked", unlinked)
	if conflicts > 0 {
		detail += fmt.Sprintf(", %d conflicts", conflicts)
	}
	return detail
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
		result, err := a.Consolidate(ctx, ecosystem, manager, nil)
		tools, _ := a.ListTools(ctx, "") // non-fatal: consolidate succeeded; stale list retained if refresh fails
		if err != nil {
			return opCompleteMsg{err: err}
		}
		parts := []string{fmt.Sprintf("%d migrated to %s", len(result.Migrated), manager)}
		if len(result.Failed) > 0 {
			parts = append(parts, fmt.Sprintf("%d failed", len(result.Failed)))
		}
		if len(result.UninstallWarnings) > 0 {
			parts = append(parts, fmt.Sprintf("%d uninstall warning(s)", len(result.UninstallWarnings)))
		}
		return opCompleteMsg{message: strings.Join(parts, ", "), tools: tools}
	}
}

// doClaim adds an orphan tool (installed but not in config) to the given group.
// Pass groupName="" to add to the current host group.
func (m *Model) doClaim(name, prov, groupName string, activeHost ...string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		if err := a.Add(ctx, prov, name, name, groupName, ""); err != nil {
			return claimDoneMsg{err: err, name: name, groupName: groupName}
		}
		if groupName != "" {
			if err := a.AddGroupToHost(shortHostname(), groupName); err != nil {
				tools, _ := a.ListTools(ctx, "")
				groupNames, toolGroups, memberships, info := m.reloadToolContext()
				return claimDoneMsg{err: err, name: name, groupName: groupName, tools: tools, groupNames: groupNames, toolGroups: toolGroups, toolMemberships: memberships, hostInfo: info}
			}
		}
		if len(activeHost) > 0 && activeHost[0] != "" && groupName != "" {
			if err := a.AddGroupToHost(activeHost[0], groupName); err != nil {
				tools, _ := a.ListTools(ctx, "")
				groupNames, toolGroups, memberships, info := m.reloadToolContext()
				return claimDoneMsg{err: err, name: name, groupName: groupName, tools: tools, groupNames: groupNames, toolGroups: toolGroups, toolMemberships: memberships, hostInfo: info}
			}
		}
		tools, _ := a.ListTools(ctx, "") // non-fatal: claim succeeded; stale list retained if refresh fails
		groupNames, toolGroups, memberships, info := m.reloadToolContext()
		return claimDoneMsg{name: name, groupName: groupName, tools: tools, groupNames: groupNames, toolGroups: toolGroups, toolMemberships: memberships, hostInfo: info}
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
		changedHost := false
		ignored := false
		for _, opt := range options {
			if opt.checked == opt.initialChecked {
				continue
			}
			desired := opt.checked
			ignored = ignored || desired
			var err error
			switch opt.kind {
			case "tool":
				err = a.SetToolIgnore(name, desired)
			case "group":
				err = a.SetGroupIgnore(opt.group, name, desired)
			case "host":
				changedHost = true
				err = a.SetGlobalToolIgnore(name, desired)
			default:
				err = fmt.Errorf("unknown ignore scope %q", opt.kind)
			}
			if err != nil {
				return ignoreDoneMsg{err: err, name: name, ignored: desired}
			}
		}
		groups, _ := a.Groups(ctx)
		tools, _ := a.ListTools(ctx, "")
		var hostIgnore []string
		if info, _ := a.HostStatus(); info != nil && info.Active != "" {
			if prof, ok := info.Hosts[info.Active]; ok {
				hostIgnore = prof.Ignore
			}
		}
		labels := buildIgnoreLabels(a.ConfigPath, groups, hostIgnore)
		toolIgnores, groupIgnores, _ := buildToolScopeState(a.ConfigPath, groups)
		if labels[name] != "" {
			ignored = true
		}
		return ignoreDoneMsg{name: name, ignored: ignored, tools: tools, hostScope: changedHost, ignoreLabels: labels, toolIgnoreSet: toolIgnores, groupIgnoreSet: groupIgnores}
	}
}

// doInstallAndAdd installs a tool and adds it to the selected config group.
// Used for search results and orphan tools that are not yet in config.
func (m *Model) doInstallAndAdd(name, prov string, groupAndHost ...string) tea.Cmd {
	a, ctx := m.app, m.beginCancellableAction()
	return func() tea.Msg {
		group := ""
		if len(groupAndHost) > 0 {
			group = groupAndHost[0]
		}
		removeDiscovered := []string{toolKey(name, prov)}
		if err := a.Install(ctx, name, prov); err != nil {
			return opCompleteMsg{err: err}
		}
		if err := a.Add(ctx, prov, name, name, group, ""); err != nil {
			// Install succeeded but config save failed. Surface as error so the
			// status bar renders red — config is the recoverable part the user
			// must address; pretending green ✓ would hide the failure.
			return opCompleteMsg{err: fmt.Errorf("installed %s but config save failed: %w", name, err)}
		}
		if group != "" {
			if err := a.AddGroupToHost(shortHostname(), group); err != nil {
				tools, _ := a.ListTools(ctx, "")
				groupNames, toolGroups, memberships, info := m.reloadToolContext()
				return opCompleteMsg{err: fmt.Errorf("installed %s and added to config but host update failed: %w", name, err), tools: tools, removeDiscoveredKeys: removeDiscovered, groupNames: groupNames, toolGroups: toolGroups, toolMemberships: memberships, hostInfo: info}
			}
		}
		if len(groupAndHost) > 1 && groupAndHost[1] != "" && group != "" {
			if err := a.AddGroupToHost(groupAndHost[1], group); err != nil {
				tools, _ := a.ListTools(ctx, "")
				groupNames, toolGroups, memberships, info := m.reloadToolContext()
				return opCompleteMsg{err: fmt.Errorf("installed %s and added to config but host update failed: %w", name, err), tools: tools, removeDiscoveredKeys: removeDiscovered, groupNames: groupNames, toolGroups: toolGroups, toolMemberships: memberships, hostInfo: info}
			}
		}
		tools, _ := a.ListTools(ctx, "") // non-fatal: install+add succeeded; stale list retained if refresh fails
		groupNames, toolGroups, memberships, info := m.reloadToolContext()
		return opCompleteMsg{message: "installed " + name + " and added to config", tools: tools, removeDiscoveredKeys: removeDiscovered, groupNames: groupNames, toolGroups: toolGroups, toolMemberships: memberships, hostInfo: info}
	}
}

func (m *Model) doSetProviderScope(name string, opt scopeOption, t *database.ToolCache) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		if t == nil || t.InstalledWith == "" {
			return opCompleteMsg{err: fmt.Errorf("provider pin: installed provider is unknown")}
		}
		var err error
		switch opt.kind {
		case "provider-host":
			err = a.SetToolHostInstallSpec(name, t.Provider, t.Package, t.InstalledWith)
		case "provider-tool":
			err = a.SetToolDefaultInstallSpec(name, t.Provider, t.Package, t.InstalledWith)
		case "provider-ecosystem":
			err = a.PinEcosystemForHost(ctx, t.Provider, t.InstalledWith)
		default:
			err = fmt.Errorf("unknown provider scope %q", opt.kind)
		}
		if err != nil {
			return opCompleteMsg{err: fmt.Errorf("pin provider: %w", err)}
		}
		tools, err := a.ListTools(ctx, "")
		if err != nil {
			return opCompleteMsg{err: fmt.Errorf("pin provider: list tools: %w", err)}
		}
		groups, _ := a.Groups(ctx)
		_, _, pins := buildToolScopeState(a.ConfigPath, groups)
		if pins == nil {
			pins = make(map[string]string)
		}
		pins[name] = t.InstalledWith
		return opCompleteMsg{message: "pinned " + name + " via " + opt.label, tools: tools, toolProviderPins: pins}
	}
}

func (m *Model) doClearProviderOverride(name, configProv, installedWith string) tea.Cmd {
	a, ctx := m.app, m.beginCancellableAction()
	return func() tea.Msg {
		var (
			err     error
			result  *app.SwitchResult
			cleared app.ClearInstallOverrideResult
		)
		if installedWith != "" {
			result, cleared, err = a.ReinstallWithDefaultAfterClearingInstallOverride(ctx, name, configProv)
		} else {
			cleared, err = a.ClearToolInstallOverride(ctx, name, configProv)
			if err == nil {
				if refreshErr := a.RefreshInstalled(ctx, nil); refreshErr != nil {
					err = fmt.Errorf("refresh installed: %w", refreshErr)
				}
			}
		}
		fromProvider := installedWith
		toProvider := configProv
		if result != nil {
			fromProvider = result.FromProvider
			toProvider = result.ToProvider
		} else if fromProvider == "" {
			fromProvider = cleared.InstallWith
		}
		if err != nil {
			return migrateProviderDoneMsg{err: fmt.Errorf("remove provider override: %w", err), name: name, fromProvider: fromProvider, toProvider: toProvider, clearedProviderOverride: true}
		}
		tools, err := a.ListTools(ctx, "")
		if err != nil {
			return migrateProviderDoneMsg{err: fmt.Errorf("remove provider override: list tools: %w", err), name: name, fromProvider: fromProvider, toProvider: toProvider, clearedProviderOverride: true}
		}
		groups, _ := a.Groups(ctx)
		_, _, pins := buildToolScopeState(a.ConfigPath, groups)
		return migrateProviderDoneMsg{name: name, fromProvider: fromProvider, toProvider: toProvider, tools: tools, toolProviderPins: pins, clearedProviderOverride: true}
	}
}

// doMigrateProvider installs the tool via the config-intended provider, removes
// it from the currently-installed (wrong) provider, and updates the config.
// syncWrongProv means: tool is installed via installedWith but config says configProv.
// Migrate = install via configProv, uninstall from installedWith, rewrite config.
func (m *Model) doMigrateProvider(name, configProv, installedWith string) tea.Cmd {
	a, ctx := m.app, m.beginCancellableAction()
	return func() tea.Msg {
		if _, err := a.MigrateInstallation(ctx, name, installedWith, configProv); err != nil {
			return migrateProviderDoneMsg{err: fmt.Errorf("reinstall with default: %w", err), name: name, fromProvider: installedWith, toProvider: configProv}
		}
		// Re-probe all providers so InstalledWith is updated in the DB before we
		// return the fresh tool list. Runs silently (no progress channel needed here).
		if err := a.RefreshInstalled(ctx, nil); err != nil {
			return migrateProviderDoneMsg{err: fmt.Errorf("reinstall with default: refresh installed: %w", err), name: name, fromProvider: installedWith, toProvider: configProv}
		}
		if err := a.RefreshDescriptions(ctx, 0); err != nil {
			return migrateProviderDoneMsg{err: fmt.Errorf("reinstall with default: refresh descriptions: %w", err), name: name, fromProvider: installedWith, toProvider: configProv}
		}
		tools, err := a.ListTools(ctx, "")
		if err != nil {
			return migrateProviderDoneMsg{err: fmt.Errorf("reinstall with default: list tools: %w", err), name: name, fromProvider: installedWith, toProvider: configProv}
		}
		return migrateProviderDoneMsg{name: name, fromProvider: installedWith, toProvider: configProv, tools: tools}
	}
}

func (m *Model) doApplyProviderSolution(name, fromProvider string, solution provider.ErrorSolution) tea.Cmd {
	a, ctx := m.app, m.beginCancellableAction()
	target := solution.TargetProvider
	return func() tea.Msg {
		key := toolKey(name, fromProvider)
		if target == "" {
			return opCompleteMsg{key: key, err: fmt.Errorf("apply fix: missing target provider")}
		}
		result, err := a.Switch(ctx, name, fromProvider, target)
		if err != nil {
			return opCompleteMsg{key: key, err: fmt.Errorf("apply fix: %w", err)}
		}
		if err := a.RefreshInstalled(ctx, nil); err != nil {
			return opCompleteMsg{key: key, err: fmt.Errorf("apply fix: refresh installed: %w", err)}
		}
		if err := a.RefreshDescriptions(ctx, 0); err != nil {
			return opCompleteMsg{key: key, err: fmt.Errorf("apply fix: refresh descriptions: %w", err)}
		}
		tools, err := a.ListTools(ctx, "")
		if err != nil {
			return opCompleteMsg{key: key, err: fmt.Errorf("apply fix: list tools: %w", err)}
		}
		message := fmt.Sprintf("reinstalled %s with %s", name, target)
		if result != nil && result.UninstallWarning != nil {
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
// Returns a tea.Cmd that starts the async directory read.
func (m *Model) openFilePicker(title, currentPath string, allowFiles bool) tea.Cmd {
	fp, cmd := newPathPicker(currentPath, allowFiles, filePickerContentWidth(*m), filePickerListHeight(*m))

	m.dotsFilePicker = fp
	m.filePickerTitle = title
	m.filePickerAllowFiles = allowFiles
	m.showFilePicker = true
	return cmd
}

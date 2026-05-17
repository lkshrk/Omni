package tui

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

const (
	pickerMembershipTool = "tool"
	pickerMembershipDot  = "dot"
)

func (m *Model) handleGroupPickerKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd

	switch {
	case !m.pickerCreatingGroup && key.Matches(msg, m.keys.Back):
		m.cancelGroupPicker()
	case !m.pickerCreatingGroup && key.Matches(msg, m.keys.Up):
		if m.pickerCursor > 0 {
			m.pickerCursor--
		}
	case !m.pickerCreatingGroup && key.Matches(msg, m.keys.Down):
		if m.pickerCursor < len(m.pickerGroups)-1 {
			m.pickerCursor++
		}
	case !m.pickerCreatingGroup && key.Matches(msg, m.keys.Confirm):
		m.confirmGroupPickerSelection(&cmds)
	case m.pickerCreatingGroup && key.Matches(msg, m.keys.Confirm):
		m.submitGroupPickerNewGroup(&cmds)
	case m.pickerCreatingGroup && key.Matches(msg, m.keys.Back):
		m.cancelGroupPicker()
	default:
		if m.pickerCreatingGroup {
			var cmd tea.Cmd
			m.settingsInput, cmd = m.settingsInput.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}

	return cmds
}

// cancelGroupPicker closes the picker and drains the reassign queue (user pressed Escape).
func (m *Model) cancelGroupPicker() {
	m.pendingGroupReassign = nil
	m.reassignCreatedGroups = nil
	m.closeGroupPicker()
}

func (m *Model) closeGroupPicker() {
	m.settingsInput.Blur()
	m.pickerCreatingGroup = false
	m.pickerPurposeClaim = false
	m.pickerPurposeInstall = false
	wasDotAdd := m.pickerPurposeDotAdd
	wasReassign := m.pickerPurposeReassign
	m.pickerPurposeDotAdd = false
	m.pickerPurposeReassign = false
	m.pickerDotAddPath = ""
	m.pickerDotAddRawPath = ""
	m.pickerActionTool = database.ToolCache{}
	m.pickerActionToolSet = false
	if wasDotAdd {
		m.mode = viewDots
	} else {
		m.mode = viewList
	}
	m.pickerGroups = nil
	// If this was a reassignment and more tools are queued, open next picker.
	if wasReassign && len(m.pendingGroupReassign) > 0 {
		m.openNextReassignPicker()
	} else if wasReassign {
		m.reassignCreatedGroups = nil
	}
}

// startGroupReassignQueue begins iterating through claimed tools with group
// pickers so the user can move them out of the machine hostname group.
func (m *Model) startGroupReassignQueue(names []string) {
	if len(names) == 0 {
		return
	}
	m.pendingGroupReassign = append([]string(nil), names...)
	m.reassignCreatedGroups = nil
	m.openNextReassignPicker()
}

func (m *Model) openNextReassignPicker() {
	if len(m.pendingGroupReassign) == 0 {
		return
	}
	// Carry forward any groups created in previous reassign pickers.
	for _, g := range m.pickerCreatedGroups {
		m.reassignCreatedGroups = appendUniqueString(m.reassignCreatedGroups, g)
	}
	name := m.pendingGroupReassign[0]
	m.pendingGroupReassign = m.pendingGroupReassign[1:]
	// Find the tool in allTools to set as pickerActionTool.
	for _, t := range m.allTools {
		if t != nil && t.Name == name {
			m.pickerActionTool = *t
			m.pickerActionToolSet = true
			break
		}
	}
	m.mode = viewGroupPicker
	groups := prioritizedPickerGroups(*m)
	// Include groups created during earlier reassign pickers that may not
	// yet appear in m.groupNames (the async groupChangedMsg hasn't arrived).
	for _, g := range m.reassignCreatedGroups {
		if !slices.Contains(groups, g) {
			groups = append(groups, g)
		}
	}
	m.pickerGroups = append(groups, groupPickerNewSentinel)
	m.pickerCursor = 0
	m.pickerCreatingGroup = false
	m.pickerPurposeReassign = true
	m.pickerCreatedGroups = nil
}

func (m *Model) confirmGroupPickerSelection(cmds *[]tea.Cmd) {
	if m.pickerCursor < 0 || m.pickerCursor >= len(m.pickerGroups) {
		return
	}
	selected := m.pickerGroups[m.pickerCursor]
	if isNewGroupSentinel(selected) {
		m.openPickerNewGroupInput(cmds)
		return
	}
	if m.runGroupPickerAction(selected, cmds) {
		m.closeGroupPicker()
	}
}

func (m *Model) submitGroupPickerNewGroup(cmds *[]tea.Cmd) {
	newGroup := strings.TrimSpace(m.settingsInput.Value())
	if newGroup != "" {
		if !slices.Contains(nonSentinelGroups(m.pickerGroups), newGroup) {
			m.pickerCreatedGroups = appendUniqueString(m.pickerCreatedGroups, newGroup)
		}
		if !m.runGroupPickerAction(newGroup, cmds) {
			return
		}
	}
	m.closeGroupPicker()
}

func (m *Model) openPickerNewGroupInput(cmds *[]tea.Cmd) {
	m.pickerCreatingGroup = true
	m.settingsInput.SetValue("")
	m.settingsInput.Placeholder = "new group name…"
	m.settingsInput.Focus()
	*cmds = append(*cmds, textinput.Blink)
}

func (m *Model) runGroupPickerAction(group string, cmds *[]tea.Cmd) bool {
	if m.pickerPurposeDotAdd {
		m.beginDotsOperation("Adding " + m.pickerDotAddRawPath + "…")
		*cmds = append(*cmds, m.spinner.Tick, m.doDotsAdd(m.pickerDotAddPath, m.pickerDotAddRawPath, group))
		return true
	}
	if m.pickerPurposeReassign {
		t, ok := m.groupPickerActionTool()
		if !ok {
			return true
		}
		// Don't set m.loading — the move is fire-and-forget so the next
		// reassign picker can accept input immediately. A stale
		// groupChangedMsg from this op must not block the next picker.
		*cmds = append(*cmds, m.doSetToolGroupMembership(t.Name, group, true))
		return true
	}
	t, ok := m.groupPickerActionTool()
	if !ok {
		return true
	}
	m.loading = true
	if m.pickerPurposeClaim || m.pickerPurposeInstall {
		claimGroup := group
		if isProtectedGroupName(claimGroup) {
			claimGroup = ""
		}
		if m.pickerPurposeInstall {
			activeHost := m.activeHostForCreatedGroup(group)
			m.closeGroupPicker()
			if m.blockPrivilegedToolAction(&t, provider.PrivilegeActionInstall) {
				if m.adminTerminal != nil {
					m.adminTerminal.addToConfig = true
					m.adminTerminal.addGroup = claimGroup
					m.adminTerminal.addHost = activeHost
				}
				m.loading = false
				return false
			}
			startOp(m, "Installing "+t.Name+"…")
			m.startRowOperation(t.Name, t.Provider, m.statusMsg)
			*cmds = append(*cmds, m.spinner.Tick, m.doInstallAndAdd(t.Name, t.Provider, claimGroup, activeHost))
			return false
		}
		startOp(m, "Adding "+t.Name+" to config…")
		*cmds = append(*cmds, m.spinner.Tick, m.doClaim(t.Name, t.Provider, claimGroup, m.activeHostForCreatedGroup(group)))
		return true
	}
	m.loading = false
	*cmds = append(*cmds, setStatus(m, "✗ group picker has no assignment action", true))
	return true
}

func (m *Model) groupPickerActionTool() (database.ToolCache, bool) {
	if m.pickerActionToolSet {
		return m.pickerActionTool, true
	}
	t := m.selectedTool()
	if t == nil {
		return database.ToolCache{}, false
	}
	return *t, true
}

func (m *Model) activeHostForCreatedGroup(group string) string {
	if m.hostInfo == nil || !slices.Contains(m.pickerCreatedGroups, group) {
		return ""
	}
	return m.hostInfo.Active
}

func (m *Model) handleGroupMembershipKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd
	switch {
	case m.pickerCreatingGroup && key.Matches(msg, m.keys.Confirm):
		m.submitGroupMembershipNewGroup()
	case m.pickerCreatingGroup && key.Matches(msg, m.keys.Back):
		m.closeGroupMembershipPicker()
	case m.pickerCreatingGroup:
		var cmd tea.Cmd
		m.settingsInput, cmd = m.settingsInput.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case key.Matches(msg, m.keys.Back):
		m.closeGroupMembershipPicker()
	case key.Matches(msg, m.keys.Up):
		if m.pickerCursor > 0 {
			m.pickerCursor--
		}
	case key.Matches(msg, m.keys.Down):
		if m.pickerCursor < len(m.pickerGroups)-1 {
			m.pickerCursor++
		}
	case key.Matches(msg, m.keys.Toggle):
		if m.selectedPickerGroupIsNewSentinel() {
			m.openPickerNewGroupInput(&cmds)
		} else {
			cmds = append(cmds, m.selectGroupMembership()...)
			m.saveGroupMembershipPicker(&cmds)
		}
	case key.Matches(msg, m.keys.Confirm):
		if m.selectedPickerGroupIsNewSentinel() {
			m.openPickerNewGroupInput(&cmds)
		} else {
			m.saveGroupMembershipPicker(&cmds)
		}
	}
	return cmds
}

func (m *Model) closeGroupMembershipPicker() {
	m.restoreGroupMembershipDraft()
	m.finishGroupMembershipPicker()
}

func (m *Model) finishGroupMembershipPicker() {
	nextMode := viewList
	if m.pickerMembershipKind == pickerMembershipDot {
		nextMode = viewDots
	}
	m.mode = nextMode
	m.pickerGroups = nil
	m.pickerCursor = 0
	m.pickerMembershipKind = ""
	m.pickerMembershipName = ""
	m.pickerMembershipKey = ""
	m.pickerOriginalGroups = nil
	m.pickerCreatedGroups = nil
	m.pickerCreatingGroup = false
	m.settingsInput.Blur()
}

func (m *Model) restoreGroupMembershipDraft() {
	switch m.pickerMembershipKind {
	case pickerMembershipDot:
		if m.pickerMembershipName == "" {
			return
		}
		if m.dotMemberships == nil {
			m.dotMemberships = make(map[string][]string)
		}
		m.dotMemberships[m.pickerMembershipName] = append([]string(nil), m.pickerOriginalGroups...)
	default:
		if m.pickerMembershipKey == "" {
			return
		}
		if m.toolMemberships == nil {
			m.toolMemberships = make(map[string][]string)
		}
		m.toolMemberships[m.pickerMembershipKey] = append([]string(nil), m.pickerOriginalGroups...)
	}
}

func (m *Model) selectedMembershipTarget() (name string, memberships []string, ok bool) {
	if m.pickerMembershipKind == pickerMembershipDot {
		if m.pickerMembershipName == "" {
			return "", nil, false
		}
		return m.pickerMembershipName, append([]string(nil), m.dotMemberships[m.pickerMembershipName]...), true
	}
	key := m.pickerMembershipKey
	if key == "" {
		t := m.selectedTool()
		if t == nil {
			return "", nil, false
		}
		key = toolMembershipKey(t)
		if m.pickerMembershipName == "" {
			return t.Name, append([]string(nil), m.toolMemberships[key]...), true
		}
	}
	targetName := m.pickerMembershipName
	if targetName == "" {
		targetName = toolNameFromKey(key)
	}
	if targetName == "" {
		return "", nil, false
	}
	return targetName, append([]string(nil), m.toolMemberships[key]...), true
}

func (m *Model) setSelectedMemberships(memberships []string) {
	if m.pickerMembershipKind == pickerMembershipDot {
		if m.dotMemberships == nil {
			m.dotMemberships = make(map[string][]string)
		}
		m.dotMemberships[m.pickerMembershipName] = memberships
		return
	}
	if m.toolMemberships == nil {
		m.toolMemberships = make(map[string][]string)
	}
	key := m.pickerMembershipKey
	if key == "" {
		if t := m.selectedTool(); t != nil {
			key = toolMembershipKey(t)
		}
	}
	if key != "" {
		m.toolMemberships[key] = memberships
	}
}

func (m *Model) selectGroupMembership() []tea.Cmd {
	_, _, ok := m.selectedMembershipTarget()
	if !ok || m.pickerCursor < 0 || m.pickerCursor >= len(m.pickerGroups) {
		return nil
	}
	group := m.pickerGroups[m.pickerCursor]
	if isNewGroupSentinel(group) {
		return nil
	}
	m.setSelectedMemberships([]string{group})
	return nil
}

func (m *Model) selectedPickerGroupIsNewSentinel() bool {
	return m.pickerCursor >= 0 && m.pickerCursor < len(m.pickerGroups) && isNewGroupSentinel(m.pickerGroups[m.pickerCursor])
}

func (m *Model) submitGroupMembershipNewGroup() {
	newGroup := strings.TrimSpace(m.settingsInput.Value())
	m.settingsInput.Blur()
	m.pickerCreatingGroup = false
	if newGroup == "" {
		return
	}
	if !slices.Contains(nonSentinelGroups(m.pickerGroups), newGroup) {
		m.pickerCreatedGroups = appendUniqueString(m.pickerCreatedGroups, newGroup)
		m.pickerGroups = appendGroupToPicker(m, newGroup)
	}
	if _, _, ok := m.selectedMembershipTarget(); !ok {
		return
	}
	m.setSelectedMemberships([]string{newGroup})
	for i, group := range m.pickerGroups {
		if group == newGroup {
			m.pickerCursor = i
			break
		}
	}
}

func appendGroupToPicker(m *Model, group string) []string {
	groups := append(nonSentinelGroups(m.pickerGroups), group)
	slices.Sort(groups)
	groups = prioritizePickerGroupList(*m, groups)
	return append(groups, groupPickerNewSentinel)
}

func nonSentinelGroups(groups []string) []string {
	out := make([]string, 0, len(groups))
	for _, group := range groups {
		if !isNewGroupSentinel(group) {
			out = append(out, group)
		}
	}
	return out
}

func appendUniqueString(values []string, value string) []string {
	if slices.Contains(values, value) {
		return values
	}
	return append(values, value)
}

func removeString(values []string, value string) []string {
	out := values[:0]
	for _, v := range values {
		if v != value {
			out = append(out, v)
		}
	}
	return out
}

func (m *Model) saveGroupMembershipPicker(cmds *[]tea.Cmd) {
	name, next, ok := m.selectedMembershipTarget()
	if !ok {
		m.finishGroupMembershipPicker()
		return
	}
	if sameStringSet(m.pickerOriginalGroups, next) {
		m.finishGroupMembershipPicker()
		return
	}
	created := createdMembershipGroups(m.pickerCreatedGroups, next)
	host := ""
	if m.hostInfo != nil {
		host = m.hostInfo.Active
	}
	if m.pickerMembershipKind == pickerMembershipDot {
		m.beginDotsOperation("Updating groups for " + name + "…")
		*cmds = append(*cmds, m.spinner.Tick, m.doSetDotGroupMemberships(name, m.pickerOriginalGroups, next, created, host))
	} else {
		m.loading = true
		startOp(m, "Updating groups for "+name+"…")
		*cmds = append(*cmds, m.spinner.Tick, m.doSetToolGroupMemberships(name, m.pickerOriginalGroups, next, created, host))
	}
	m.finishGroupMembershipPicker()
}

func createdMembershipGroups(created, memberships []string) []string {
	membershipSet := stringSet(memberships)
	var out []string
	for _, group := range created {
		if membershipSet[group] {
			out = append(out, group)
		}
	}
	slices.Sort(out)
	return out
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	ac := append([]string(nil), a...)
	bc := append([]string(nil), b...)
	slices.Sort(ac)
	slices.Sort(bc)
	return slices.Equal(ac, bc)
}

func (m *Model) handleScopePickerKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd
	switch {
	case key.Matches(msg, m.keys.Back):
		m.closeScopePicker()
	case key.Matches(msg, m.keys.Up):
		if m.scopeCursor > 0 {
			m.scopeCursor--
		}
	case key.Matches(msg, m.keys.Down):
		if m.scopeCursor < len(m.scopeOptions)-1 {
			m.scopeCursor++
		}
	case key.Matches(msg, m.keys.Toggle):
		if m.mode == viewProviderScope {
			m.selectHighlightedProviderScopeOption()
			m.saveScopePickerSelection(&cmds)
		} else {
			m.toggleSelectedScopeOption()
		}
	case key.Matches(msg, m.keys.Confirm):
		m.saveScopePickerSelection(&cmds)
	}
	return cmds
}

func (m *Model) selectHighlightedProviderScopeOption() {
	if m.scopeCursor < 0 || m.scopeCursor >= len(m.scopeOptions) {
		return
	}
	for i := range m.scopeOptions {
		m.scopeOptions[i].checked = i == m.scopeCursor
	}
}

func (m *Model) closeScopePicker() {
	m.mode = viewList
	m.scopeOptions = nil
	m.scopeCursor = 0
	m.scopeTarget = database.ToolCache{}
	m.scopeTargetSet = false
}

func (m *Model) toggleSelectedScopeOption() {
	if m.scopeCursor < 0 || m.scopeCursor >= len(m.scopeOptions) {
		return
	}
	if m.mode == viewProviderScope {
		next := !m.scopeOptions[m.scopeCursor].checked
		for i := range m.scopeOptions {
			m.scopeOptions[i].checked = false
		}
		m.scopeOptions[m.scopeCursor].checked = next
		return
	}
	m.scopeOptions[m.scopeCursor].checked = !m.scopeOptions[m.scopeCursor].checked
}

func (m *Model) saveScopePickerSelection(cmds *[]tea.Cmd) {
	if !m.scopeTargetSet {
		return
	}
	t := m.scopeTarget
	switch m.mode {
	case viewIgnoreScope:
		if !scopeOptionsChanged(m.scopeOptions) {
			m.closeScopePicker()
			return
		}
		m.loading = true
		startOp(m, "Updating ignore scope for "+t.Name+"…")
		*cmds = append(*cmds, m.spinner.Tick, m.doSaveIgnoreScopes(t.Name, m.scopeOptions))
	case viewProviderScope:
		opt, ok := selectedProviderScopeOption(m.scopeOptions)
		if !ok {
			*cmds = append(*cmds, setStatus(m, "✗ select a provider scope with space", true))
			return
		}
		m.loading = true
		startOp(m, "Pinning provider for "+t.Name+"…")
		toolCopy := t
		*cmds = append(*cmds, m.spinner.Tick, m.doSetProviderScope(t.Name, opt, &toolCopy))
	}
	m.closeScopePicker()
}

func scopeOptionsChanged(options []scopeOption) bool {
	for _, opt := range options {
		if opt.checked != opt.initialChecked {
			return true
		}
	}
	return false
}

func selectedProviderScopeOption(options []scopeOption) (scopeOption, bool) {
	for _, opt := range options {
		if opt.checked {
			return opt, true
		}
	}
	return scopeOption{}, false
}

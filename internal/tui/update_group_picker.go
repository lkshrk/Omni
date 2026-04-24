package tui

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/database"
)

const (
	pickerMembershipTool = "tool"
	pickerMembershipDot  = "dot"
)

func (m *Model) handleGroupPickerKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd

	switch {
	case !m.pickerCreatingGroup && key.Matches(msg, m.keys.Back):
		m.closeGroupPicker()
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
		m.closeGroupPicker()
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

func (m *Model) closeGroupPicker() {
	m.settingsInput.Blur()
	m.pickerCreatingGroup = false
	m.pickerPurposeClaim = false
	m.pickerPurposeInstall = false
	m.pickerActionTool = database.ToolCache{}
	m.pickerActionToolSet = false
	m.mode = viewList
	m.pickerGroups = nil
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
	m.runGroupPickerAction(selected, cmds)
	m.closeGroupPicker()
}

func (m *Model) submitGroupPickerNewGroup(cmds *[]tea.Cmd) {
	newGroup := strings.TrimSpace(m.settingsInput.Value())
	if newGroup != "" {
		if !slices.Contains(nonSentinelGroups(m.pickerGroups), newGroup) {
			m.pickerCreatedGroups = appendUniqueString(m.pickerCreatedGroups, newGroup)
		}
		m.runGroupPickerAction(newGroup, cmds)
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

func (m *Model) runGroupPickerAction(group string, cmds *[]tea.Cmd) {
	t, ok := m.groupPickerActionTool()
	if !ok {
		return
	}
	m.loading = true
	if m.pickerPurposeClaim || m.pickerPurposeInstall {
		claimGroup := group
		if claimGroup == "base" {
			claimGroup = ""
		}
		if m.pickerPurposeInstall {
			startOp(m, "Installing "+t.Name+"…")
			m.startRowOperation(t.Name, t.Provider, m.statusMsg)
			*cmds = append(*cmds, m.spinner.Tick, m.doInstallAndAdd(t.Name, t.Provider, claimGroup, m.activeProfileForCreatedGroup(group)))
			return
		}
		startOp(m, "Adding "+t.Name+" to config…")
		*cmds = append(*cmds, m.spinner.Tick, m.doClaim(t.Name, t.Provider, claimGroup, m.activeProfileForCreatedGroup(group)))
		return
	}
	m.loading = false
	*cmds = append(*cmds, setStatus(m, "✗ group picker has no assignment action", true))
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

func (m *Model) activeProfileForCreatedGroup(group string) string {
	if m.profileInfo == nil || !slices.Contains(m.pickerCreatedGroups, group) {
		return ""
	}
	return m.profileInfo.Active
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
			cmds = append(cmds, m.toggleSelectedGroupMembership()...)
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

func (m *Model) toggleSelectedGroupMembership() []tea.Cmd {
	name, memberships, ok := m.selectedMembershipTarget()
	if !ok || m.pickerCursor < 0 || m.pickerCursor >= len(m.pickerGroups) {
		return nil
	}
	group := m.pickerGroups[m.pickerCursor]
	if isNewGroupSentinel(group) {
		return nil
	}
	hasMembership := slices.Contains(memberships, group)
	if hasMembership && len(memberships) <= 1 {
		return []tea.Cmd{setStatus(m, "✗ "+name+" needs at least one group", true)}
	}
	if hasMembership {
		memberships = removeString(memberships, group)
	} else {
		memberships = append(memberships, group)
	}
	slices.Sort(memberships)
	m.setSelectedMemberships(memberships)
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
	_, memberships, ok := m.selectedMembershipTarget()
	if !ok {
		return
	}
	memberships = appendUniqueString(memberships, newGroup)
	slices.Sort(memberships)
	m.setSelectedMemberships(memberships)
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
	profile := ""
	if m.profileInfo != nil {
		profile = m.profileInfo.Active
	}
	if m.pickerMembershipKind == pickerMembershipDot {
		m.beginDotsOperation("Updating groups for " + name + "…")
		*cmds = append(*cmds, m.spinner.Tick, m.doSetDotGroupMemberships(name, m.pickerOriginalGroups, next, created, profile))
	} else {
		m.loading = true
		startOp(m, "Updating groups for "+name+"…")
		*cmds = append(*cmds, m.spinner.Tick, m.doSetToolGroupMemberships(name, m.pickerOriginalGroups, next, created, profile))
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
		m.toggleSelectedScopeOption()
	case key.Matches(msg, m.keys.Confirm):
		m.saveScopePickerSelection(&cmds)
	}
	return cmds
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

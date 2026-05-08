package tui

import (
	"slices"
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

func (m *Model) handleGroupsKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd

	if handled, subCmds := m.handleHostSubmodeKeyMsg(msg); handled {
		return subCmds
	}
	m.handleGroupsNavigationKeyMsg(msg, &cmds)
	return cmds
}

type groupAssignmentKeyHandlers struct {
	editor      *groupAssignmentEditor
	placeholder string
	rowCount    func() int
	clamp       func()
	close       func()
	toggle      func()
	save        func(*[]tea.Cmd)
	extra       func(tea.KeyPressMsg, *[]tea.Cmd)
}

func (e *groupAssignmentEditor) reset() {
	*e = groupAssignmentEditor{}
}

func (e *groupAssignmentEditor) start(group string, names []string, memberOf func(string) bool) {
	*e = groupAssignmentEditor{
		group:              group,
		membership:         make(map[string]bool, len(names)),
		originalMembership: make(map[string]bool, len(names)),
	}
	for _, name := range names {
		member := memberOf(name)
		e.membership[name] = member
		e.originalMembership[name] = member
	}
}

func (e *groupAssignmentEditor) clamp(rowCount int) {
	if rowCount == 0 {
		e.cursor = 0
		return
	}
	if e.cursor >= rowCount {
		e.cursor = rowCount - 1
	}
	if e.cursor < 0 {
		e.cursor = 0
	}
}

func (e *groupAssignmentEditor) toggle(name string) {
	if name == "" {
		return
	}
	if e.membership == nil {
		e.membership = make(map[string]bool)
	}
	e.membership[name] = !e.membership[name]
}

func (e groupAssignmentEditor) snapshot() (string, map[string]bool, map[string]bool) {
	return e.group, copyBoolMap(e.membership), copyBoolMap(e.originalMembership)
}

func (e groupAssignmentEditor) changed() bool {
	return boolMapsChanged(e.membership, e.originalMembership)
}

func (m *Model) handleGroupToolsKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	return m.handleGroupAssignmentKeyMsg(msg, groupAssignmentKeyHandlers{
		editor:      &m.groupToolsEditor,
		placeholder: "search tools…",
		rowCount:    func() int { return len(groupToolRows(*m)) },
		clamp:       m.clampGroupToolsCursor,
		close:       m.closeGroupToolsEditor,
		toggle:      m.toggleGroupToolMembership,
		save:        m.saveGroupToolsEditor,
		extra: func(msg tea.KeyPressMsg, cmds *[]tea.Cmd) {
			switch {
			case key.Matches(msg, m.keys.PrevTab):
				m.cycleGroupToolsProvider(-1)
			case key.Matches(msg, m.keys.NextTab):
				m.cycleGroupToolsProvider(1)
			case key.Matches(msg, m.keys.Ignore):
				m.toggleGroupToolIgnore()
			default:
				return
			}
		},
	})
}

func (m *Model) handleGroupDotsKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	return m.handleGroupAssignmentKeyMsg(msg, groupAssignmentKeyHandlers{
		editor:      &m.groupDotsEditor,
		placeholder: "search dotfiles…",
		rowCount:    func() int { return len(groupDotRows(*m)) },
		clamp:       m.clampGroupDotsCursor,
		close:       m.closeGroupDotsEditor,
		toggle:      m.toggleGroupDotMembership,
		save:        m.saveGroupDotsEditor,
	})
}

func (m *Model) handleGroupAssignmentKeyMsg(msg tea.KeyPressMsg, h groupAssignmentKeyHandlers) []tea.Cmd {
	var cmds []tea.Cmd
	if h.editor.searchActive {
		switch {
		case key.Matches(msg, m.keys.Confirm):
			h.editor.search = strings.TrimSpace(m.settingsInput.Value())
			h.editor.searchActive = false
			m.settingsInput.Blur()
			h.clamp()
		case key.Matches(msg, m.keys.Back):
			h.editor.searchActive = false
			m.settingsInput.Blur()
		default:
			var cmd tea.Cmd
			m.settingsInput, cmd = m.settingsInput.Update(msg)
			h.editor.search = strings.TrimSpace(m.settingsInput.Value())
			h.clamp()
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		return cmds
	}

	switch {
	case key.Matches(msg, m.keys.Back):
		h.close()
	case key.Matches(msg, m.keys.Search):
		h.editor.searchActive = true
		m.settingsInput.SetValue(h.editor.search)
		m.settingsInput.Placeholder = h.placeholder
		m.settingsInput.Focus()
		cmds = append(cmds, textinput.Blink)
	case key.Matches(msg, m.keys.Up):
		if h.editor.cursor > 0 {
			h.editor.cursor--
		}
	case key.Matches(msg, m.keys.Down):
		if h.editor.cursor < h.rowCount()-1 {
			h.editor.cursor++
		}
	case key.Matches(msg, m.keys.Toggle):
		h.toggle()
	case key.Matches(msg, m.keys.Confirm):
		h.save(&cmds)
	default:
		if h.extra != nil {
			h.extra(msg, &cmds)
		}
	}
	return cmds
}

func (m *Model) closeGroupToolsEditor() {
	m.mode = viewGroups
	m.groupToolsEditor.reset()
	m.groupToolsProviderIdx = 0
	m.groupToolsIgnore = nil
	m.groupToolsOriginalIgnore = nil
	m.settingsInput.Blur()
}

func (m *Model) closeGroupDotsEditor() {
	m.mode = viewGroups
	m.groupDotsEditor.reset()
	m.settingsInput.Blur()
}

func (m *Model) cycleGroupToolsProvider(delta int) {
	providers := groupToolProviders(*m)
	if len(providers) == 0 {
		m.groupToolsProviderIdx = 0
		return
	}
	count := len(providers) + 1 // all + providers
	m.groupToolsProviderIdx = (m.groupToolsProviderIdx + delta) % count
	if m.groupToolsProviderIdx < 0 {
		m.groupToolsProviderIdx += count
	}
	m.clampGroupToolsCursor()
}

func (m *Model) clampGroupToolsCursor() {
	m.groupToolsEditor.clamp(len(groupToolRows(*m)))
}

func (m *Model) clampGroupDotsCursor() {
	m.groupDotsEditor.clamp(len(groupDotRows(*m)))
}

func (m *Model) selectedGroupToolRow() (groupToolRow, bool) {
	rows := groupToolRows(*m)
	if m.groupToolsEditor.cursor < 0 || m.groupToolsEditor.cursor >= len(rows) {
		return groupToolRow{}, false
	}
	return rows[m.groupToolsEditor.cursor], true
}

func (m *Model) selectedGroupDotRow() (groupDotRow, bool) {
	rows := groupDotRows(*m)
	if m.groupDotsEditor.cursor < 0 || m.groupDotsEditor.cursor >= len(rows) {
		return groupDotRow{}, false
	}
	return rows[m.groupDotsEditor.cursor], true
}

func (m *Model) toggleGroupToolMembership() {
	row, ok := m.selectedGroupToolRow()
	if !ok || row.tool == nil {
		return
	}
	m.groupToolsEditor.toggle(row.tool.Name)
	m.clampGroupToolsCursor()
}

func (m *Model) toggleGroupToolIgnore() {
	row, ok := m.selectedGroupToolRow()
	if !ok || row.tool == nil {
		return
	}
	m.groupToolsIgnore[row.tool.Name] = !m.groupToolsIgnore[row.tool.Name]
	m.clampGroupToolsCursor()
}

func (m *Model) toggleGroupDotMembership() {
	row, ok := m.selectedGroupDotRow()
	if !ok || row.name == "" {
		return
	}
	m.groupDotsEditor.toggle(row.name)
	m.clampGroupDotsCursor()
}

func (m *Model) saveGroupToolsEditor(cmds *[]tea.Cmd) {
	group, membership, originalMembership := m.groupToolsEditor.snapshot()
	ignores := copyBoolMap(m.groupToolsIgnore)
	originalIgnores := copyBoolMap(m.groupToolsOriginalIgnore)
	if !groupToolsChanged(membership, originalMembership, ignores, originalIgnores) {
		m.closeGroupToolsEditor()
		return
	}
	m.loading = true
	startOp(m, "Updating tools for "+group+"…")
	*cmds = append(*cmds, m.spinner.Tick, m.doSetGroupTools(group, membership, originalMembership, ignores, originalIgnores))
	m.closeGroupToolsEditor()
}

func (m *Model) saveGroupDotsEditor(cmds *[]tea.Cmd) {
	group, membership, originalMembership := m.groupDotsEditor.snapshot()
	if !m.groupDotsEditor.changed() {
		m.closeGroupDotsEditor()
		return
	}
	m.loading = true
	startOp(m, "Updating dotfiles for "+group+"…")
	*cmds = append(*cmds, m.spinner.Tick, m.doSetGroupDots(group, membership, originalMembership))
	m.closeGroupDotsEditor()
}

func copyBoolMap(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func boolMapsChanged(current, original map[string]bool) bool {
	for name, value := range current {
		if original[name] != value {
			return true
		}
	}
	for name, value := range original {
		if current[name] != value {
			return true
		}
	}
	return false
}

func groupToolsChanged(membership, originalMembership, ignores, originalIgnores map[string]bool) bool {
	if boolMapsChanged(membership, originalMembership) {
		return true
	}
	for name, value := range ignores {
		if originalIgnores[name] != value {
			return true
		}
	}
	for name, value := range originalIgnores {
		if ignores[name] != value {
			return true
		}
	}
	return false
}

func (m *Model) handleHostSubmodeKeyMsg(msg tea.KeyPressMsg) (bool, []tea.Cmd) {
	var cmds []tea.Cmd

	if m.hostRenameMode {
		switch {
		case key.Matches(msg, m.keys.Confirm):
			oldName := m.hostRenameName
			newName := strings.TrimSpace(m.settingsInput.Value())
			m.clearHostRenameState()
			if oldName != "" && newName != "" {
				cmds = append(cmds, m.doRenameHost(oldName, newName))
			}
		case key.Matches(msg, m.keys.Back):
			m.clearHostRenameState()
		default:
			var inputCmd tea.Cmd
			m.settingsInput, inputCmd = m.settingsInput.Update(msg)
			cmds = append(cmds, inputCmd)
		}
		return true, cmds
	}

	if m.groupRenameMode {
		switch {
		case key.Matches(msg, m.keys.Confirm):
			oldName := m.groupRenameName
			newName := strings.TrimSpace(m.settingsInput.Value())
			m.clearGroupRenameState()
			if oldName != "" && !isProtectedGroupName(oldName) && newName != "" {
				cmds = append(cmds, m.doRenameGroup(oldName, newName))
			}
		case key.Matches(msg, m.keys.Back):
			m.clearGroupRenameState()
		default:
			var inputCmd tea.Cmd
			m.settingsInput, inputCmd = m.settingsInput.Update(msg)
			cmds = append(cmds, inputCmd)
		}
		return true, cmds
	}

	if m.groupCreating {
		switch {
		case key.Matches(msg, m.keys.Confirm):
			newName := strings.TrimSpace(m.settingsInput.Value())
			m.groupCreating = false
			m.settingsInput.Blur()
			if newName != "" {
				m.loading = true
				startOp(m, "Creating group "+newName+"…")
				cmds = append(cmds, m.spinner.Tick, m.doCreateGroup(newName))
			}
		case key.Matches(msg, m.keys.Back):
			m.groupCreating = false
			m.settingsInput.Blur()
		default:
			var inputCmd tea.Cmd
			m.settingsInput, inputCmd = m.settingsInput.Update(msg)
			cmds = append(cmds, inputCmd)
		}
		return true, cmds
	}

	if m.groupDeleteConfirm {
		hasContent := m.groupHasContent(m.groupDeleteName)
		switch {
		case key.Matches(msg, m.keys.Up):
			if hasContent && m.groupDeleteChoice > 0 {
				m.groupDeleteChoice--
			}
		case key.Matches(msg, m.keys.Down):
			if hasContent && m.groupDeleteChoice < 1 {
				m.groupDeleteChoice++
			}
		case key.Matches(msg, m.keys.Confirm):
			m.cancelConfirmationTimeout()
			groupName := m.groupDeleteName
			if groupName != "" && !isProtectedGroupName(groupName) {
				deleteTools := hasContent && m.groupDeleteChoice == 1
				m.groupDeleteConfirm = false
				m.groupDeleteName = ""
				m.groupDeleteChoice = 0
				cmds = append(cmds, m.doDeleteGroup(groupName, deleteTools))
			} else {
				m.groupDeleteConfirm = false
				m.groupDeleteName = ""
				m.groupDeleteChoice = 0
			}
		case key.Matches(msg, m.keys.Back):
			m.cancelConfirmationTimeout()
			m.groupDeleteConfirm = false
			m.groupDeleteName = ""
			m.groupDeleteChoice = 0
		}
		return true, cmds
	}

	if m.hostEditMode == 1 {
		if m.pickerCreatingGroup {
			switch {
			case key.Matches(msg, m.keys.Confirm):
				m.addHostPickerGroupFromInput()
			case key.Matches(msg, m.keys.Back):
				m.pickerCreatingGroup = false
				m.settingsInput.Blur()
			default:
				var inputCmd tea.Cmd
				m.settingsInput, inputCmd = m.settingsInput.Update(msg)
				cmds = append(cmds, inputCmd)
			}
			return true, cmds
		}
		switch {
		case key.Matches(msg, m.keys.Up):
			if m.hostGroupIdx > 0 {
				m.hostGroupIdx--
			}
		case key.Matches(msg, m.keys.Down):
			if m.hostGroupIdx < len(m.hostGroupPicker)-1 {
				m.hostGroupIdx++
			}
		case key.Matches(msg, m.keys.Toggle):
			m.toggleHostGroupDraft()
		case key.Matches(msg, m.keys.Confirm):
			if m.hostGroupIdx >= 0 && m.hostGroupIdx < len(m.hostGroupPicker) && isNewGroupSentinel(m.hostGroupPicker[m.hostGroupIdx]) {
				m.pickerCreatingGroup = true
				m.settingsInput.SetValue("")
				m.settingsInput.Placeholder = "group name…"
				m.settingsInput.Focus()
				cmds = append(cmds, textinput.Blink)
				return true, cmds
			}
			host := m.hostEditName
			before := editableHostAssignmentGroups(host, m.hostOriginalGroups)
			after := editableHostAssignmentGroups(host, m.hostGroupDraft)
			created := append([]string(nil), m.pickerCreatedGroups...)
			m.clearHostPickerState()
			if host != "" {
				cmds = append(cmds, m.doSetHostGroups(host, before, after, created))
			}
		case key.Matches(msg, m.keys.Back):
			m.clearHostPickerState()
		}
		return true, cmds
	}

	if m.hostCopyConfirm {
		switch {
		case key.Matches(msg, m.keys.Toggle):
			m.cancelConfirmationTimeout()
			host := m.hostCopyName
			m.hostCopyConfirm = false
			m.hostCopyName = ""
			if host != "" {
				cmds = append(cmds, m.doCopyHostGroupsFrom(host))
			}
		case key.Matches(msg, m.keys.Back):
			m.cancelConfirmationTimeout()
			m.hostCopyConfirm = false
			m.hostCopyName = ""
		}
		return true, cmds
	}

	if m.hostDeleteConfirm {
		switch {
		case key.Matches(msg, m.keys.Confirm) || key.Matches(msg, m.keys.Delete):
			m.cancelConfirmationTimeout()
			host := m.hostDeleteName
			m.hostDeleteConfirm = false
			m.hostDeleteName = ""
			if host != "" {
				cmds = append(cmds, m.doRemoveHostFromTab(host))
			}
		case key.Matches(msg, m.keys.Back):
			m.cancelConfirmationTimeout()
			m.hostDeleteConfirm = false
			m.hostDeleteName = ""
		}
		return true, cmds
	}

	return false, nil
}

func (m *Model) handleGroupsNavigationKeyMsg(msg tea.KeyPressMsg, cmds *[]tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Up):
		m.moveGroupsCursorUp()
	case key.Matches(msg, m.keys.Down):
		m.moveGroupsCursorDown()
	case key.Matches(msg, m.keys.Back):
		if !m.hostRequired {
			m.mode = viewList
		}
	default:
		m.handleGroupsActionKey(msg, cmds)
	}
}

func (m *Model) moveGroupsCursorUp() {
	switch m.assignmentSection {
	case 0:
		if m.hostCursor > 0 {
			m.hostCursor--
		}
	case 1:
		if m.groupCursor > 0 {
			m.groupCursor--
		} else {
			m.assignmentSection = 0
			if m.hostInfo != nil && len(m.hostInfo.Hosts) > 0 {
				m.hostCursor = len(m.hostInfo.Hosts) - 1
			}
		}
	}
}

func (m *Model) moveGroupsCursorDown() {
	switch m.assignmentSection {
	case 0:
		nHosts := 0
		if m.hostInfo != nil {
			nHosts = len(m.hostInfo.Hosts)
		}
		if m.hostCursor < nHosts-1 {
			m.hostCursor++
		} else {
			m.assignmentSection = 1
			m.groupCursor = 0
		}
	case 1:
		allGroupNames := buildAllGroupNames(m.groupNames)
		if m.groupCursor < len(allGroupNames)-1 {
			m.groupCursor++
		}
	}
}

func (m *Model) handleGroupsActionKey(msg tea.KeyPressMsg, cmds *[]tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Toggle):
		if m.assignmentSection == 0 {
			m.confirmCopySelectedHostGroups(cmds)
		}
	default:
		switch {
		case key.Matches(msg, m.keys.NewGroup):
			m.startGroupCreation(cmds)
		case key.Matches(msg, m.keys.Rename):
			m.startHostOrGroupRename()
		case key.Matches(msg, m.keys.HostGroups):
			m.startHostGroupEdit(cmds)
		case key.Matches(msg, m.keys.Delete):
			m.startHostDelete(cmds)
		case key.Matches(msg, m.keys.GroupTools):
			m.startHostGroupToolsEdit()
		case key.Matches(msg, m.keys.GroupDots):
			m.startHostGroupDotsEdit(cmds)
		}
	}
}

func (m *Model) startGroupCreation(cmds *[]tea.Cmd) {
	m.assignmentSection = 1
	m.groupCreating = true
	m.settingsInput.SetValue("")
	m.settingsInput.Placeholder = "group name…"
	m.settingsInput.Focus()
	*cmds = append(*cmds, textinput.Blink)
}

func (m *Model) startHostOrGroupRename() {
	switch m.assignmentSection {
	case 0:
		name := m.selectedHostName()
		if name == "" {
			return
		}
		m.settingsInput.SetValue(name)
		m.settingsInput.CursorEnd()
		m.settingsInput.Focus()
		m.hostRenameMode = true
		m.hostRenameName = name
	case 1:
		m.startGroupRename()
	}
}

func (m *Model) startHostGroupEdit(cmds *[]tea.Cmd) {
	if m.assignmentSection != 0 {
		return
	}
	host := m.selectedHostName()
	if host == "" || m.hostInfo == nil {
		return
	}
	all := buildHostAssignmentPickerGroups(host, m.groupNames)
	if len(all) == 0 {
		*cmds = append(*cmds, setStatus(m, "no groups configured", false))
		return
	}
	m.hostGroupPicker = append(append([]string(nil), all...), groupPickerNewSentinel)
	m.hostGroupDraft = hostAssignmentDraftGroups(host, m.hostInfo.Hosts[host].Groups)
	m.hostOriginalGroups = append([]string(nil), m.hostGroupDraft...)
	m.hostGroupIdx = 0
	m.hostEditName = host
	m.hostEditMode = 1
	m.pickerCreatingGroup = false
	m.pickerCreatedGroups = nil
}

func (m *Model) startHostDelete(cmds *[]tea.Cmd) {
	switch m.assignmentSection {
	case 0:
		if host := m.selectedHostName(); host != "" {
			m.hostDeleteConfirm = true
			m.hostDeleteName = host
			*cmds = append(*cmds, m.armConfirmationTimeout())
		}
	case 1:
		if group := m.selectedHostGroupName(); group != "" && !isProtectedGroupName(group) {
			m.groupDeleteConfirm = true
			m.groupDeleteName = group
			m.groupDeleteChoice = 0
			*cmds = append(*cmds, m.armConfirmationTimeout())
		}
	}
}

func (m Model) groupHasContent(group string) bool {
	if group == "" {
		return false
	}
	for _, groups := range m.toolMemberships {
		if slices.Contains(groups, group) {
			return true
		}
	}
	for _, groups := range m.dotMemberships {
		if slices.Contains(groups, group) {
			return true
		}
	}
	return false
}

func buildHostAssignmentPickerGroups(host string, groupNames []string) []string {
	names := make([]string, 0, len(groupNames)+1)
	if host != "" {
		names = append(names, host)
	}
	reusable := append([]string(nil), groupNames...)
	sort.Strings(reusable)
	seen := map[string]bool{host: true}
	for _, name := range reusable {
		if seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

func (m *Model) startGroupRename() {
	group := m.selectedHostGroupName()
	if m.assignmentSection != 1 || group == "" || isProtectedGroupName(group) {
		return
	}
	m.settingsInput.SetValue(group)
	m.settingsInput.CursorEnd()
	m.settingsInput.Focus()
	m.groupRenameMode = true
	m.groupRenameName = group
}

func (m *Model) selectedHostGroupName() string {
	allGroupNames := buildAllGroupNames(m.groupNames)
	if m.groupCursor < 0 || m.groupCursor >= len(allGroupNames) {
		return ""
	}
	return allGroupNames[m.groupCursor]
}

func (m *Model) selectedHostIsCurrentHost() bool {
	if m.hostInfo == nil || m.hostInfo.Active == "" {
		return false
	}
	return m.selectedHostName() == m.hostInfo.Active
}

func (m *Model) canCopySelectedHostGroups() bool {
	return m.assignmentSection == 0 && m.selectedHostName() != "" && !m.selectedHostIsCurrentHost()
}

func isProtectedGroupName(group string) bool {
	return group == "" || group == shortHostname()
}

func (m *Model) startHostGroupToolsEdit() {
	if m.assignmentSection != 1 {
		return
	}
	group := m.selectedHostGroupName()
	if group == "" {
		return
	}
	m.groupToolsProviderIdx = 0
	m.groupToolsIgnore = make(map[string]bool)
	m.groupToolsOriginalIgnore = make(map[string]bool)
	names := make([]string, 0, len(m.allTools))
	members := make(map[string]bool, len(m.allTools))
	for _, t := range m.allTools {
		if t == nil || !t.Tracked || t.Name == "" {
			continue
		}
		names = append(names, t.Name)
		members[t.Name] = slices.Contains(m.toolMemberships[toolMembershipKey(t)], group)
		ignored := m.groupIgnoreSet[t.Name] != nil && m.groupIgnoreSet[t.Name][group]
		m.groupToolsIgnore[t.Name] = ignored
		m.groupToolsOriginalIgnore[t.Name] = ignored
	}
	m.groupToolsEditor.start(group, names, func(name string) bool {
		return members[name]
	})
	m.settingsInput.Blur()
	m.settingsInput.SetValue("")
	m.mode = viewGroupTools
}

func (m *Model) startHostGroupDotsEdit(cmds *[]tea.Cmd) {
	if m.assignmentSection != 1 {
		return
	}
	group := m.selectedHostGroupName()
	if group == "" {
		return
	}
	names := groupDotNames(*m)
	if len(names) == 0 {
		*cmds = append(*cmds, setStatus(m, "no dotfiles configured", false))
		return
	}
	m.groupDotsEditor.start(group, names, func(name string) bool {
		return slices.Contains(m.dotMemberships[name], group)
	})
	m.settingsInput.Blur()
	m.settingsInput.SetValue("")
	m.mode = viewGroupDots
}

func (m *Model) confirmCopySelectedHostGroups(cmds *[]tea.Cmd) {
	if m.hostInfo == nil {
		return
	}
	host := m.selectedHostName()
	if host == "" || m.selectedHostIsCurrentHost() {
		return
	}
	if m.hostCopyConfirm && m.hostCopyName == host {
		m.cancelConfirmationTimeout()
		m.hostCopyConfirm = false
		m.hostCopyName = ""
		*cmds = append(*cmds, m.doCopyHostGroupsFrom(host))
		return
	}
	m.hostCopyConfirm = true
	m.hostCopyName = host
	*cmds = append(*cmds, m.armConfirmationTimeout())
}

func hostAssignmentDraftGroups(host string, groups []string) []string {
	out := []string{}
	if host != "" {
		out = append(out, host)
	}
	for _, group := range groups {
		if group == "" || group == host || slices.Contains(out, group) {
			continue
		}
		out = append(out, group)
	}
	sort.Strings(out)
	if host != "" {
		out = moveStringToFront(out, host)
	}
	return out
}

func editableHostAssignmentGroups(host string, groups []string) []string {
	out := []string{}
	for _, group := range groups {
		if group == "" || group == host || slices.Contains(out, group) {
			continue
		}
		out = append(out, group)
	}
	sort.Strings(out)
	return out
}

func moveStringToFront(groups []string, first string) []string {
	idx := slices.Index(groups, first)
	if idx <= 0 {
		return groups
	}
	out := append([]string{groups[idx]}, groups[:idx]...)
	out = append(out, groups[idx+1:]...)
	return out
}

func (m *Model) applyCopiedHost(host string) {
	if m.hostInfo != nil && m.hostInfo.Active != "" {
		host = m.hostInfo.Active
	}
	if m.hostInfo == nil || host == "" {
		return
	}
	m.hostInfo.Active = host
	m.hostRequired = false
	m.groupFilter = ""
	m.groupTabIdx = 0
	m.toolGroups = compactToolGroupMapForHost(m.toolMemberships, m.hostInfo)
	m.applyHostIgnoreState(host)
	m.applyFilter()
}

func (m *Model) applyHostIgnoreState(host string) {
	m.ignoreSet = make(map[string]bool)
	if m.ignoreLabels == nil {
		m.ignoreLabels = make(map[string]string)
	}
	for name, label := range m.ignoreLabels {
		if label == "host" {
			delete(m.ignoreLabels, name)
		}
	}
	info, ok := m.hostInfo.Hosts[host]
	if !ok {
		return
	}
	for _, name := range info.Ignore {
		if name == "" {
			continue
		}
		m.ignoreSet[name] = true
		if m.toolIgnoreSet[name] || len(m.groupIgnoreSet[name]) > 0 {
			continue
		}
		m.ignoreLabels[name] = "host"
	}
}

func (m *Model) toggleHostGroupDraft() {
	if m.hostGroupIdx < 0 || m.hostGroupIdx >= len(m.hostGroupPicker) {
		return
	}
	group := m.hostGroupPicker[m.hostGroupIdx]
	if isNewGroupSentinel(group) || group == m.hostEditName {
		return
	}
	if slices.Contains(m.hostGroupDraft, group) {
		m.hostGroupDraft = removeString(m.hostGroupDraft, group)
		return
	}
	m.hostGroupDraft = append(m.hostGroupDraft, group)
	sort.Strings(m.hostGroupDraft)
}

func (m *Model) addHostPickerGroupFromInput() {
	name := strings.TrimSpace(m.settingsInput.Value())
	m.settingsInput.Blur()
	m.pickerCreatingGroup = false
	if name == "" || isNewGroupSentinel(name) {
		return
	}
	if !slices.Contains(m.hostGroupPicker, name) {
		insertAt := len(m.hostGroupPicker)
		if insertAt > 0 && isNewGroupSentinel(m.hostGroupPicker[insertAt-1]) {
			insertAt--
		}
		m.hostGroupPicker = append(m.hostGroupPicker[:insertAt], append([]string{name}, m.hostGroupPicker[insertAt:]...)...)
	}
	if !slices.Contains(m.hostGroupDraft, name) {
		m.hostGroupDraft = append(m.hostGroupDraft, name)
		sort.Strings(m.hostGroupDraft)
	}
	if !slices.Contains(m.pickerCreatedGroups, name) {
		m.pickerCreatedGroups = append(m.pickerCreatedGroups, name)
	}
	for i, group := range m.hostGroupPicker {
		if group == name {
			m.hostGroupIdx = i
			return
		}
	}
}

func (m *Model) clearHostPickerState() {
	m.hostEditMode = 0
	m.hostGroupPicker = nil
	m.hostGroupDraft = nil
	m.hostOriginalGroups = nil
	m.hostGroupIdx = 0
	m.hostEditName = ""
	m.pickerCreatingGroup = false
	m.pickerCreatedGroups = nil
	m.settingsInput.Blur()
}

func (m *Model) clearHostRenameState() {
	m.settingsInput.Blur()
	m.hostRenameMode = false
	m.hostRenameName = ""
}

func (m *Model) clearGroupRenameState() {
	m.settingsInput.Blur()
	m.groupRenameMode = false
	m.groupRenameName = ""
}

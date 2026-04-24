package tui

import (
	"slices"
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
)

func (m *Model) handleProfilesKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd

	if handled, subCmds := m.handleProfileSubmodeKeyMsg(msg); handled {
		return subCmds
	}
	m.handleProfilesNavigationKeyMsg(msg, &cmds)
	return cmds
}

type profileGroupAssignmentKeyHandlers struct {
	editor      *profileGroupAssignmentEditor
	placeholder string
	rowCount    func() int
	clamp       func()
	close       func()
	toggle      func()
	save        func(*[]tea.Cmd)
	extra       func(tea.KeyPressMsg, *[]tea.Cmd)
}

func (e *profileGroupAssignmentEditor) reset() {
	*e = profileGroupAssignmentEditor{}
}

func (e *profileGroupAssignmentEditor) start(group string, names []string, memberOf func(string) bool) {
	*e = profileGroupAssignmentEditor{
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

func (e *profileGroupAssignmentEditor) clamp(rowCount int) {
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

func (e *profileGroupAssignmentEditor) toggle(name string) {
	if name == "" {
		return
	}
	if e.membership == nil {
		e.membership = make(map[string]bool)
	}
	e.membership[name] = !e.membership[name]
}

func (e profileGroupAssignmentEditor) snapshot() (string, map[string]bool, map[string]bool) {
	return e.group, copyBoolMap(e.membership), copyBoolMap(e.originalMembership)
}

func (e profileGroupAssignmentEditor) changed() bool {
	return boolMapsChanged(e.membership, e.originalMembership)
}

func (m *Model) handleProfileGroupToolsKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	return m.handleProfileGroupAssignmentKeyMsg(msg, profileGroupAssignmentKeyHandlers{
		editor:      &m.groupToolsEditor,
		placeholder: "search tools…",
		rowCount:    func() int { return len(profileGroupToolRows(*m)) },
		clamp:       m.clampProfileGroupToolsCursor,
		close:       m.closeProfileGroupToolsEditor,
		toggle:      m.toggleProfileGroupToolMembership,
		save:        m.saveProfileGroupToolsEditor,
		extra: func(msg tea.KeyPressMsg, cmds *[]tea.Cmd) {
			switch {
			case key.Matches(msg, m.keys.PrevTab):
				m.cycleProfileGroupToolsProvider(-1)
			case key.Matches(msg, m.keys.NextTab):
				m.cycleProfileGroupToolsProvider(1)
			case key.Matches(msg, m.keys.Ignore):
				m.toggleProfileGroupToolIgnore()
			default:
				return
			}
		},
	})
}

func (m *Model) handleProfileGroupDotsKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	return m.handleProfileGroupAssignmentKeyMsg(msg, profileGroupAssignmentKeyHandlers{
		editor:      &m.groupDotsEditor,
		placeholder: "search dotfiles…",
		rowCount:    func() int { return len(profileGroupDotRows(*m)) },
		clamp:       m.clampProfileGroupDotsCursor,
		close:       m.closeProfileGroupDotsEditor,
		toggle:      m.toggleProfileGroupDotMembership,
		save:        m.saveProfileGroupDotsEditor,
	})
}

func (m *Model) handleProfileGroupAssignmentKeyMsg(msg tea.KeyPressMsg, h profileGroupAssignmentKeyHandlers) []tea.Cmd {
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

func (m *Model) closeProfileGroupToolsEditor() {
	m.mode = viewProfiles
	m.groupToolsEditor.reset()
	m.groupToolsProviderIdx = 0
	m.groupToolsIgnore = nil
	m.groupToolsOriginalIgnore = nil
	m.settingsInput.Blur()
}

func (m *Model) closeProfileGroupDotsEditor() {
	m.mode = viewProfiles
	m.groupDotsEditor.reset()
	m.settingsInput.Blur()
}

func (m *Model) cycleProfileGroupToolsProvider(delta int) {
	providers := profileGroupToolProviders(*m)
	if len(providers) == 0 {
		m.groupToolsProviderIdx = 0
		return
	}
	count := len(providers) + 1 // all + providers
	m.groupToolsProviderIdx = (m.groupToolsProviderIdx + delta) % count
	if m.groupToolsProviderIdx < 0 {
		m.groupToolsProviderIdx += count
	}
	m.clampProfileGroupToolsCursor()
}

func (m *Model) clampProfileGroupToolsCursor() {
	m.groupToolsEditor.clamp(len(profileGroupToolRows(*m)))
}

func (m *Model) clampProfileGroupDotsCursor() {
	m.groupDotsEditor.clamp(len(profileGroupDotRows(*m)))
}

func (m *Model) selectedProfileGroupToolRow() (profileGroupToolRow, bool) {
	rows := profileGroupToolRows(*m)
	if m.groupToolsEditor.cursor < 0 || m.groupToolsEditor.cursor >= len(rows) {
		return profileGroupToolRow{}, false
	}
	return rows[m.groupToolsEditor.cursor], true
}

func (m *Model) selectedProfileGroupDotRow() (profileGroupDotRow, bool) {
	rows := profileGroupDotRows(*m)
	if m.groupDotsEditor.cursor < 0 || m.groupDotsEditor.cursor >= len(rows) {
		return profileGroupDotRow{}, false
	}
	return rows[m.groupDotsEditor.cursor], true
}

func (m *Model) toggleProfileGroupToolMembership() {
	row, ok := m.selectedProfileGroupToolRow()
	if !ok || row.tool == nil {
		return
	}
	m.groupToolsEditor.toggle(row.tool.Name)
	m.clampProfileGroupToolsCursor()
}

func (m *Model) toggleProfileGroupToolIgnore() {
	row, ok := m.selectedProfileGroupToolRow()
	if !ok || row.tool == nil {
		return
	}
	m.groupToolsIgnore[row.tool.Name] = !m.groupToolsIgnore[row.tool.Name]
	m.clampProfileGroupToolsCursor()
}

func (m *Model) toggleProfileGroupDotMembership() {
	row, ok := m.selectedProfileGroupDotRow()
	if !ok || row.name == "" {
		return
	}
	m.groupDotsEditor.toggle(row.name)
	m.clampProfileGroupDotsCursor()
}

func (m *Model) saveProfileGroupToolsEditor(cmds *[]tea.Cmd) {
	group, membership, originalMembership := m.groupToolsEditor.snapshot()
	ignores := copyBoolMap(m.groupToolsIgnore)
	originalIgnores := copyBoolMap(m.groupToolsOriginalIgnore)
	if !profileGroupToolsChanged(membership, originalMembership, ignores, originalIgnores) {
		m.closeProfileGroupToolsEditor()
		return
	}
	m.loading = true
	startOp(m, "Updating tools for "+group+"…")
	*cmds = append(*cmds, m.spinner.Tick, m.doSetProfileGroupTools(group, membership, originalMembership, ignores, originalIgnores))
	m.closeProfileGroupToolsEditor()
}

func (m *Model) saveProfileGroupDotsEditor(cmds *[]tea.Cmd) {
	group, membership, originalMembership := m.groupDotsEditor.snapshot()
	if !m.groupDotsEditor.changed() {
		m.closeProfileGroupDotsEditor()
		return
	}
	m.loading = true
	startOp(m, "Updating dotfiles for "+group+"…")
	*cmds = append(*cmds, m.spinner.Tick, m.doSetProfileGroupDots(group, membership, originalMembership))
	m.closeProfileGroupDotsEditor()
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

func profileGroupToolsChanged(membership, originalMembership, ignores, originalIgnores map[string]bool) bool {
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

func (m *Model) handleProfileSubmodeKeyMsg(msg tea.KeyPressMsg) (bool, []tea.Cmd) {
	var cmds []tea.Cmd

	if m.profileCreating {
		switch {
		case key.Matches(msg, m.keys.Confirm):
			name := strings.TrimSpace(m.settingsInput.Value())
			m.settingsInput.Blur()
			m.profileCreating = false
			if name != "" {
				cmds = append(cmds, m.doCreateProfileFromTab(name))
			}
		case key.Matches(msg, m.keys.Back):
			m.settingsInput.Blur()
			m.profileCreating = false
		default:
			var inputCmd tea.Cmd
			m.settingsInput, inputCmd = m.settingsInput.Update(msg)
			cmds = append(cmds, inputCmd)
		}
		return true, cmds
	}

	if m.profileRenameMode {
		switch {
		case key.Matches(msg, m.keys.Confirm):
			oldName := m.profileRenameName
			newName := strings.TrimSpace(m.settingsInput.Value())
			m.clearProfileRenameState()
			if oldName != "" && newName != "" {
				cmds = append(cmds, m.doRenameProfile(oldName, newName))
			}
		case key.Matches(msg, m.keys.Back):
			m.clearProfileRenameState()
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
			if oldName != "" && oldName != "base" && newName != "" {
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
		switch {
		case key.Matches(msg, m.keys.Up):
			if m.groupDeleteChoice > 0 {
				m.groupDeleteChoice--
			}
		case key.Matches(msg, m.keys.Down):
			if m.groupDeleteChoice < 1 {
				m.groupDeleteChoice++
			}
		case key.Matches(msg, m.keys.Confirm):
			m.cancelConfirmationTimeout()
			groupName := m.groupDeleteName
			if groupName != "" && groupName != "base" {
				deleteTools := m.groupDeleteChoice == 1
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

	if m.profileEditMode == 1 {
		if m.pickerCreatingGroup {
			switch {
			case key.Matches(msg, m.keys.Confirm):
				m.addProfilePickerGroupFromInput()
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
			if m.profileGroupIdx > 0 {
				m.profileGroupIdx--
			}
		case key.Matches(msg, m.keys.Down):
			if m.profileGroupIdx < len(m.profileGroupPicker)-1 {
				m.profileGroupIdx++
			}
		case key.Matches(msg, m.keys.Toggle):
			m.toggleProfileGroupDraft()
		case key.Matches(msg, m.keys.Confirm):
			if m.profileGroupIdx >= 0 && m.profileGroupIdx < len(m.profileGroupPicker) && isNewGroupSentinel(m.profileGroupPicker[m.profileGroupIdx]) {
				m.pickerCreatingGroup = true
				m.settingsInput.SetValue("")
				m.settingsInput.Placeholder = "group name…"
				m.settingsInput.Focus()
				cmds = append(cmds, textinput.Blink)
				return true, cmds
			}
			profile := m.profileEditName
			before := append([]string(nil), m.profileOriginalGroups...)
			after := append([]string(nil), m.profileGroupDraft...)
			created := append([]string(nil), m.pickerCreatedGroups...)
			m.clearProfilePickerState()
			if profile != "" {
				cmds = append(cmds, m.doSetProfileGroups(profile, before, after, created))
			}
		case key.Matches(msg, m.keys.Back):
			m.clearProfilePickerState()
		}
		return true, cmds
	}

	if m.profileEditMode == 2 {
		switch {
		case key.Matches(msg, m.keys.Up):
			if m.profileHostIdx > 0 {
				m.profileHostIdx--
			}
		case key.Matches(msg, m.keys.Down):
			if m.profileHostIdx < len(m.profileHostPicker)-1 {
				m.profileHostIdx++
			}
		case key.Matches(msg, m.keys.Toggle):
			m.toggleProfileHostDraft()
		case key.Matches(msg, m.keys.Confirm):
			profile := m.profileEditName
			before := copyStringMap(m.profileHostOriginal)
			after := copyStringMap(m.profileHostDraft)
			m.clearProfilePickerState()
			if profile != "" {
				cmds = append(cmds, m.doSetProfileHosts(profile, before, after))
			}
		case key.Matches(msg, m.keys.Back):
			m.clearProfilePickerState()
		}
		return true, cmds
	}

	if m.profileDeleteConfirm {
		switch {
		case key.Matches(msg, m.keys.Confirm) || key.Matches(msg, m.keys.Delete):
			m.cancelConfirmationTimeout()
			profile := m.profileDeleteName
			m.profileDeleteConfirm = false
			m.profileDeleteName = ""
			if profile != "" {
				cmds = append(cmds, m.doDeleteProfileFromTab(profile))
			}
		case key.Matches(msg, m.keys.Back):
			m.cancelConfirmationTimeout()
			m.profileDeleteConfirm = false
			m.profileDeleteName = ""
		}
		return true, cmds
	}

	return false, nil
}

func (m *Model) handleProfilesNavigationKeyMsg(msg tea.KeyPressMsg, cmds *[]tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Up):
		m.moveProfilesCursorUp()
	case key.Matches(msg, m.keys.Down):
		m.moveProfilesCursorDown()
	case key.Matches(msg, m.keys.Back):
		if !m.profileRequired {
			m.mode = viewList
		}
	default:
		m.handleProfilesActionKey(msg, cmds)
	}
}

func (m *Model) moveProfilesCursorUp() {
	switch m.profileSection {
	case 0:
		if m.profileCursor > 0 {
			m.profileCursor--
		}
	case 1:
		if m.groupCursor > 0 {
			m.groupCursor--
		} else {
			m.profileSection = 0
			if m.profileInfo != nil && len(m.profileInfo.Profiles) > 0 {
				m.profileCursor = len(m.profileInfo.Profiles) - 1
			}
		}
	}
}

func (m *Model) moveProfilesCursorDown() {
	switch m.profileSection {
	case 0:
		nProfiles := 0
		if m.profileInfo != nil {
			nProfiles = len(m.profileInfo.Profiles)
		}
		if m.profileCursor < nProfiles-1 {
			m.profileCursor++
		} else {
			m.profileSection = 1
			m.groupCursor = 0
		}
	case 1:
		allGroupNames := buildAllGroupNames(m.groupNames)
		if m.groupCursor < len(allGroupNames)-1 {
			m.groupCursor++
		}
	}
}

func (m *Model) handleProfilesActionKey(msg tea.KeyPressMsg, cmds *[]tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Toggle):
		if m.profileSection == 0 {
			m.activateSelectedProfile(cmds)
		}
	default:
		switch {
		case key.Matches(msg, m.keys.NewProfile):
			m.startProfileCreation(cmds)
		case key.Matches(msg, m.keys.NewGroup):
			m.startGroupCreation(cmds)
		case key.Matches(msg, m.keys.Rename):
			m.startProfileOrGroupRename()
		case key.Matches(msg, m.keys.ProfileGroups):
			m.startProfileGroupEdit(cmds)
		case key.Matches(msg, m.keys.EditHosts):
			m.startProfileHostEdit(cmds)
		case key.Matches(msg, m.keys.Delete):
			m.startProfileDelete(cmds)
		case key.Matches(msg, m.keys.GroupTools):
			m.startProfileGroupToolsEdit()
		case key.Matches(msg, m.keys.GroupDots):
			m.startProfileGroupDotsEdit(cmds)
		}
	}
}

func (m *Model) startProfileCreation(cmds *[]tea.Cmd) {
	m.profileCreating = true
	m.settingsInput.SetValue("")
	m.settingsInput.Placeholder = "profile name…"
	m.settingsInput.Focus()
	*cmds = append(*cmds, textinput.Blink)
}

func (m *Model) startGroupCreation(cmds *[]tea.Cmd) {
	m.profileSection = 1
	m.groupCreating = true
	m.settingsInput.SetValue("")
	m.settingsInput.Placeholder = "group name…"
	m.settingsInput.Focus()
	*cmds = append(*cmds, textinput.Blink)
}

func (m *Model) startProfileOrGroupRename() {
	switch m.profileSection {
	case 0:
		name := m.selectedProfileName()
		if name == "" {
			return
		}
		m.settingsInput.SetValue(name)
		m.settingsInput.CursorEnd()
		m.settingsInput.Focus()
		m.profileRenameMode = true
		m.profileRenameName = name
	case 1:
		m.startGroupRename()
	}
}

func (m *Model) startProfileGroupEdit(cmds *[]tea.Cmd) {
	if m.profileSection != 0 {
		return
	}
	profile := m.selectedProfileName()
	if profile == "" || m.profileInfo == nil {
		return
	}
	all := buildAllGroupNames(m.groupNames)
	if len(all) == 0 {
		*cmds = append(*cmds, setStatus(m, "no groups configured", false))
		return
	}
	m.profileGroupPicker = append(append([]string(nil), all...), groupPickerNewSentinel)
	m.profileGroupDraft = append([]string(nil), m.profileInfo.Profiles[profile].Groups...)
	sort.Strings(m.profileGroupDraft)
	m.profileOriginalGroups = append([]string(nil), m.profileGroupDraft...)
	m.profileGroupIdx = 0
	m.profileEditName = profile
	m.profileEditMode = 1
	m.pickerCreatingGroup = false
	m.pickerCreatedGroups = nil
}

func (m *Model) startProfileHostEdit(cmds *[]tea.Cmd) {
	if m.profileSection != 0 {
		return
	}
	profile := m.selectedProfileName()
	if profile == "" || m.profileInfo == nil {
		return
	}
	hosts := profileHostPickerNames(m.profileInfo)
	if len(hosts) == 0 {
		*cmds = append(*cmds, setStatus(m, "no hosts configured", false))
		return
	}
	m.profileHostPicker = hosts
	m.profileHostIdx = 0
	m.profileHostOriginal = copyStringMap(m.profileInfo.Hostnames)
	m.profileHostDraft = copyStringMap(m.profileInfo.Hostnames)
	m.profileEditName = profile
	m.profileEditMode = 2
}

func (m *Model) startProfileDelete(cmds *[]tea.Cmd) {
	switch m.profileSection {
	case 0:
		if profile := m.selectedProfileName(); profile != "" {
			m.profileDeleteConfirm = true
			m.profileDeleteName = profile
			*cmds = append(*cmds, m.armConfirmationTimeout())
		}
	case 1:
		if group := m.selectedProfileGroupName(); group != "" && group != "base" {
			m.groupDeleteConfirm = true
			m.groupDeleteName = group
			m.groupDeleteChoice = 0
			*cmds = append(*cmds, m.armConfirmationTimeout())
		}
	}
}

func (m *Model) startGroupRename() {
	group := m.selectedProfileGroupName()
	if m.profileSection != 1 || group == "" || group == "base" {
		return
	}
	m.settingsInput.SetValue(group)
	m.settingsInput.CursorEnd()
	m.settingsInput.Focus()
	m.groupRenameMode = true
	m.groupRenameName = group
}

func (m *Model) selectedProfileGroupName() string {
	allGroupNames := buildAllGroupNames(m.groupNames)
	if m.groupCursor < 0 || m.groupCursor >= len(allGroupNames) {
		return ""
	}
	return allGroupNames[m.groupCursor]
}

func (m *Model) startProfileGroupToolsEdit() {
	group := m.selectedProfileGroupName()
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
	m.mode = viewProfileGroupTools
}

func (m *Model) startProfileGroupDotsEdit(cmds *[]tea.Cmd) {
	if m.profileSection != 1 {
		return
	}
	group := m.selectedProfileGroupName()
	if group == "" {
		return
	}
	names := profileGroupDotNames(*m)
	if len(names) == 0 {
		*cmds = append(*cmds, setStatus(m, "no dotfiles configured", false))
		return
	}
	m.groupDotsEditor.start(group, names, func(name string) bool {
		return slices.Contains(m.dotMemberships[name], group)
	})
	m.settingsInput.Blur()
	m.settingsInput.SetValue("")
	m.mode = viewProfileGroupDots
}

func (m *Model) activateSelectedProfile(cmds *[]tea.Cmd) {
	if m.profileInfo == nil {
		return
	}
	profile := m.selectedProfileName()
	if profile == "" {
		return
	}
	*cmds = append(*cmds, m.doActivateProfile(profile))
}

func (m *Model) applyActivatedProfile(profile string) {
	if m.profileInfo == nil || profile == "" {
		return
	}
	m.profileInfo.Active = profile
	m.profileRequired = false
	m.groupFilter = ""
	m.groupTabIdx = 0
	m.toolGroups = compactToolGroupMapForProfile(m.toolMemberships, m.profileInfo)
	m.applyProfileIgnoreState(profile)
	m.applyFilter()
}

func (m *Model) applyProfileIgnoreState(profile string) {
	m.ignoreSet = make(map[string]bool)
	if m.ignoreLabels == nil {
		m.ignoreLabels = make(map[string]string)
	}
	for name, label := range m.ignoreLabels {
		if label == "profile" {
			delete(m.ignoreLabels, name)
		}
	}
	prof, ok := m.profileInfo.Profiles[profile]
	if !ok {
		return
	}
	for _, name := range prof.Ignore {
		if name == "" {
			continue
		}
		m.ignoreSet[name] = true
		if m.toolIgnoreSet[name] || len(m.groupIgnoreSet[name]) > 0 {
			continue
		}
		m.ignoreLabels[name] = "profile"
	}
}

func (m *Model) toggleProfileGroupDraft() {
	if m.profileGroupIdx < 0 || m.profileGroupIdx >= len(m.profileGroupPicker) {
		return
	}
	group := m.profileGroupPicker[m.profileGroupIdx]
	if isNewGroupSentinel(group) {
		return
	}
	if slices.Contains(m.profileGroupDraft, group) {
		m.profileGroupDraft = removeString(m.profileGroupDraft, group)
		return
	}
	m.profileGroupDraft = append(m.profileGroupDraft, group)
	sort.Strings(m.profileGroupDraft)
}

func (m *Model) addProfilePickerGroupFromInput() {
	name := strings.TrimSpace(m.settingsInput.Value())
	m.settingsInput.Blur()
	m.pickerCreatingGroup = false
	if name == "" || isNewGroupSentinel(name) {
		return
	}
	if !slices.Contains(m.profileGroupPicker, name) {
		insertAt := len(m.profileGroupPicker)
		if insertAt > 0 && isNewGroupSentinel(m.profileGroupPicker[insertAt-1]) {
			insertAt--
		}
		m.profileGroupPicker = append(m.profileGroupPicker[:insertAt], append([]string{name}, m.profileGroupPicker[insertAt:]...)...)
	}
	if !slices.Contains(m.profileGroupDraft, name) {
		m.profileGroupDraft = append(m.profileGroupDraft, name)
		sort.Strings(m.profileGroupDraft)
	}
	if !slices.Contains(m.pickerCreatedGroups, name) {
		m.pickerCreatedGroups = append(m.pickerCreatedGroups, name)
	}
	for i, group := range m.profileGroupPicker {
		if group == name {
			m.profileGroupIdx = i
			return
		}
	}
}

func (m *Model) toggleProfileHostDraft() {
	if m.profileHostIdx < 0 || m.profileHostIdx >= len(m.profileHostPicker) {
		return
	}
	host := m.profileHostPicker[m.profileHostIdx]
	profile := m.profileEditName
	if host == "" || profile == "" {
		return
	}
	if m.profileHostDraft == nil {
		m.profileHostDraft = make(map[string]string)
	}
	if m.profileHostDraft[host] == profile {
		m.profileHostDraft[host] = ""
		return
	}
	m.profileHostDraft[host] = profile
}

func (m *Model) clearProfilePickerState() {
	m.profileEditMode = 0
	m.profileGroupPicker = nil
	m.profileGroupDraft = nil
	m.profileOriginalGroups = nil
	m.profileGroupIdx = 0
	m.profileHostPicker = nil
	m.profileHostDraft = nil
	m.profileHostOriginal = nil
	m.profileHostIdx = 0
	m.profileEditName = ""
	m.pickerCreatingGroup = false
	m.pickerCreatedGroups = nil
	m.settingsInput.Blur()
}

func (m *Model) clearProfileRenameState() {
	m.settingsInput.Blur()
	m.profileRenameMode = false
	m.profileRenameName = ""
}

func (m *Model) clearGroupRenameState() {
	m.settingsInput.Blur()
	m.groupRenameMode = false
	m.groupRenameName = ""
}

func copyStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func profileHostPickerNames(info *app.ProfileInfo) []string {
	if info == nil {
		return nil
	}
	set := make(map[string]bool, len(info.Hostnames)+1)
	for hostname := range info.Hostnames {
		set[hostname] = true
	}
	if host := shortHostname(); host != "" {
		set[host] = true
	}
	names := make([]string, 0, len(set))
	for hostname := range set {
		names = append(names, hostname)
	}
	sort.Strings(names)
	return names
}

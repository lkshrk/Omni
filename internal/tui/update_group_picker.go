package tui

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

const (
	pickerMembershipTool        = "tool"
	pickerMembershipDot         = "dot"
	pickerMembershipSkill       = "skill"
	pickerMembershipMcp         = "mcp"
	pickerMembershipPlugin      = "plugin"
	pickerMembershipMarketplace = "marketplace"
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
	wasAgentsClaim := m.pickerClaimAgentsSet
	m.pickerPurposeDotAdd = false
	m.pickerPurposeReassign = false
	m.pickerDotAddPath = ""
	m.pickerDotAddRawPath = ""
	m.pickerActionTool = database.ToolCache{}
	m.pickerActionToolSet = false
	m.pickerClaimAgentsRow = agentsAllRow{}
	m.pickerClaimAgentsSet = false
	m.pickerMembershipKind = ""
	switch {
	case wasDotAdd:
		m.mode = viewDots
	case wasAgentsClaim:
		m.mode = viewSkills
	default:
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
	if m.pickerPurposeClaim && m.pickerClaimAgentsSet {
		return m.runAgentsClaimGroupPickerAction(group, cmds)
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
			*cmds = append(*cmds, m.spinner.Tick, m.doInstallAndAddTool(&t, claimGroup, activeHost))
			return false
		}
		startOp(m, "Adding "+t.Name+" to config…")
		*cmds = append(*cmds, m.spinner.Tick, m.doClaim(t.Name, t.Provider, t.InstalledWith, claimGroup, m.activeHostForCreatedGroup(group)))
		return true
	}
	m.loading = false
	*cmds = append(*cmds, setStatus(m, "✗ group picker has no assignment action", true))
	return true
}

// runAgentsClaimGroupPickerAction dispatches the adopt-then-group-assign
// command for an agents-tab orphan row, re-deriving the skill/mcp/plugin
// identity from m.pickerClaimAgentsRow the same way agentsRowClaim used to
// look it up before adopting immediately. Mirrors runGroupPickerAction's
// tools claim branch: sets the running flag, starts the spinner, and closes
// the picker so the manifest reload lands on the underlying list view.
func (m *Model) runAgentsClaimGroupPickerAction(group string, cmds *[]tea.Cmd) bool {
	e := m.pickerClaimAgentsRow
	claimGroup := group
	if isProtectedGroupName(claimGroup) {
		claimGroup = ""
	}
	switch e.feature {
	case agentsSectionSkills:
		rows, _, _ := skillsVisibleRows(*m)
		if e.localIdx < 0 || e.localIdx >= len(rows) {
			m.loading = false
			return true
		}
		src := rows[e.localIdx].Source
		m.skillAddRunning = true
		m.skillsErr = nil
		m.startAgentsOp(agentsRowRunKey(e))
		*cmds = append(*cmds, m.spinner.Tick, m.doAdoptSkillPackageWithGroup(src, claimGroup, m.pickerCreatedGroups, m.activeHostForCreatedGroup(group)))
	case agentsSectionMcp:
		if e.localIdx < len(m.mcpRows) || e.agentID == "" {
			m.loading = false
			return true
		}
		flat := mcpUnmanagedFlat(m.mcpUnmanaged)
		idx := e.localIdx - len(m.mcpRows)
		if idx < 0 || idx >= len(flat) {
			m.loading = false
			return true
		}
		srv := flat[idx].srv
		m.mcpRunning = true
		m.mcpErr = nil
		m.startAgentsOp(agentsRowRunKey(e))
		*cmds = append(*cmds, m.spinner.Tick, m.doImportMcpServerWithGroup(e.agentID, srv, claimGroup))
	case agentsSectionMarketplaces:
		if e.localIdx < len(m.marketplaceRows) || e.agentID == "" {
			m.loading = false
			return true
		}
		flat := marketplaceUnmanagedFlat(m.marketplaceUnmanaged)
		idx := e.localIdx - len(m.marketplaceRows)
		if idx < 0 || idx >= len(flat) {
			m.loading = false
			return true
		}
		mk := flat[idx].marketplace
		m.marketplaceRunning = true
		m.marketplaceErr = nil
		m.startAgentsOp(agentsRowRunKey(e))
		*cmds = append(*cmds, m.spinner.Tick, m.doImportMarketplaceWithGroup(e.agentID, mk, claimGroup))
	default:
		if e.localIdx < len(m.pluginRows) || e.agentID == "" {
			m.loading = false
			return true
		}
		flat := pluginUnmanagedFlat(m.pluginUnmanaged)
		idx := e.localIdx - len(m.pluginRows)
		if idx < 0 || idx >= len(flat) {
			m.loading = false
			return true
		}
		plg := flat[idx].plugin
		m.pluginRunning = true
		m.pluginErr = nil
		m.startAgentsOp(agentsRowRunKey(e))
		*cmds = append(*cmds, m.spinner.Tick, m.doImportPluginWithGroup(e.agentID, plg, claimGroup))
	}
	m.loading = false
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
			// Toggle membership in place and stay in the picker so multiple
			// groups can be selected; Confirm persists the accumulated set.
			cmds = append(cmds, m.selectGroupMembership()...)
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

// agentsClaimMembershipKind maps an agentsAllRow's feature to the
// pickerMembershipKind constant identifying which adopt+group call to
// dispatch on confirm.
func agentsClaimMembershipKind(feature agentsSection) string {
	switch feature {
	case agentsSectionSkills:
		return pickerMembershipSkill
	case agentsSectionMcp:
		return pickerMembershipMcp
	case agentsSectionMarketplaces:
		return pickerMembershipMarketplace
	default:
		return pickerMembershipPlugin
	}
}

// openAgentsClaimGroupPicker opens the group picker in claim mode for an
// agents-tab orphan row, mirroring openGroupPicker(true) for tools: no
// adoption happens here, only on confirm (runGroupPickerAction).
func (m *Model) openAgentsClaimGroupPicker(e agentsAllRow) {
	m.mode = viewGroupPicker
	m.pickerGroups = append(prioritizedPickerGroups(*m), groupPickerNewSentinel)
	m.pickerCursor = 0
	m.pickerCreatingGroup = false
	m.pickerPurposeClaim = true
	m.pickerPurposeInstall = false
	m.pickerCreatedGroups = nil
	m.pickerActionTool = database.ToolCache{}
	m.pickerActionToolSet = false
	m.pickerMembershipKind = agentsClaimMembershipKind(e.feature)
	m.pickerClaimAgentsRow = e
	m.pickerClaimAgentsSet = true
}

func (m *Model) openSkillGroupMembershipPicker() {
	visible, findStart, unmanagedStart := skillsVisibleRows(*m)
	if m.skillsCursor < 0 || m.skillsCursor >= len(visible) {
		return
	}
	if findStart >= 0 && m.skillsCursor >= findStart {
		return
	}
	if unmanagedStart >= 0 && m.skillsCursor >= unmanagedStart {
		return
	}
	r := visible[m.skillsCursor]
	m.mode = viewGroupMembership
	m.pickerGroups = append(prioritizedPickerGroups(*m), groupPickerNewSentinel)
	m.pickerCursor = 0
	m.pickerCreatingGroup = false
	m.pickerCreatedGroups = nil
	m.pickerMembershipKind = pickerMembershipSkill
	m.pickerMembershipName = r.Source
	m.pickerMembershipKey = ""
	m.pickerOriginalGroups = append([]string(nil), r.Groups...)
	if m.skillsMemberships == nil {
		m.skillsMemberships = make(map[string][]string)
	}
	m.skillsMemberships[r.Source] = append([]string(nil), r.Groups...)
}

func (m *Model) skillPickerMemberships() []string {
	if m.skillsMemberships == nil {
		return nil
	}
	return m.skillsMemberships[m.pickerMembershipName]
}

// openMcpGroupMembershipPicker opens the group-membership picker for the
// managed mcp row at m.mcpCursor, mirroring openSkillGroupMembershipPicker.
func (m *Model) openMcpGroupMembershipPicker() {
	if m.mcpCursor < 0 || m.mcpCursor >= len(m.mcpRows) {
		return
	}
	r := m.mcpRows[m.mcpCursor]
	m.mode = viewGroupMembership
	m.pickerGroups = append(prioritizedPickerGroups(*m), groupPickerNewSentinel)
	m.pickerCursor = 0
	m.pickerCreatingGroup = false
	m.pickerCreatedGroups = nil
	m.pickerMembershipKind = pickerMembershipMcp
	m.pickerMembershipName = r.Name
	m.pickerMembershipKey = ""
	m.pickerOriginalGroups = append([]string(nil), r.Groups...)
	if m.mcpMemberships == nil {
		m.mcpMemberships = make(map[string][]string)
	}
	m.mcpMemberships[r.Name] = append([]string(nil), r.Groups...)
}

func (m *Model) mcpPickerMemberships() []string {
	if m.mcpMemberships == nil {
		return nil
	}
	return m.mcpMemberships[m.pickerMembershipName]
}

// openPluginGroupMembershipPicker opens the group-membership picker for the
// managed plugin row at m.pluginCursor, mirroring openMcpGroupMembershipPicker.
func (m *Model) openPluginGroupMembershipPicker() {
	if m.pluginCursor < 0 || m.pluginCursor >= len(m.pluginRows) {
		return
	}
	r := m.pluginRows[m.pluginCursor]
	m.mode = viewGroupMembership
	m.pickerGroups = append(prioritizedPickerGroups(*m), groupPickerNewSentinel)
	m.pickerCursor = 0
	m.pickerCreatingGroup = false
	m.pickerCreatedGroups = nil
	m.pickerMembershipKind = pickerMembershipPlugin
	m.pickerMembershipName = r.Name
	m.pickerMembershipKey = ""
	m.pickerOriginalGroups = append([]string(nil), r.Groups...)
	if m.pluginMemberships == nil {
		m.pluginMemberships = make(map[string][]string)
	}
	m.pluginMemberships[r.Name] = append([]string(nil), r.Groups...)
}

func (m *Model) pluginPickerMemberships() []string {
	if m.pluginMemberships == nil {
		return nil
	}
	return m.pluginMemberships[m.pickerMembershipName]
}

// openMarketplaceGroupMembershipPicker opens the group-membership picker for
// the managed marketplace row at m.marketplaceCursor, mirroring
// openPluginGroupMembershipPicker.
func (m *Model) openMarketplaceGroupMembershipPicker() {
	if m.marketplaceCursor < 0 || m.marketplaceCursor >= len(m.marketplaceRows) {
		return
	}
	r := m.marketplaceRows[m.marketplaceCursor]
	m.mode = viewGroupMembership
	m.pickerGroups = append(prioritizedPickerGroups(*m), groupPickerNewSentinel)
	m.pickerCursor = 0
	m.pickerCreatingGroup = false
	m.pickerCreatedGroups = nil
	m.pickerMembershipKind = pickerMembershipMarketplace
	m.pickerMembershipName = r.Name
	m.pickerMembershipKey = ""
	m.pickerOriginalGroups = append([]string(nil), r.Groups...)
	if m.marketplaceMemberships == nil {
		m.marketplaceMemberships = make(map[string][]string)
	}
	m.marketplaceMemberships[r.Name] = append([]string(nil), r.Groups...)
}

func (m *Model) marketplacePickerMemberships() []string {
	if m.marketplaceMemberships == nil {
		return nil
	}
	return m.marketplaceMemberships[m.pickerMembershipName]
}

func (m *Model) finishGroupMembershipPicker() {
	nextMode := viewList
	switch m.pickerMembershipKind {
	case pickerMembershipDot:
		nextMode = viewDots
	case pickerMembershipSkill, pickerMembershipMcp, pickerMembershipPlugin, pickerMembershipMarketplace:
		nextMode = viewSkills
	}
	if m.pickerDotExtractParent != "" && m.pickerMembershipName != "" {
		// Drop the phantom draft seeded for the pending child extract so a
		// not-yet-created entry never lingers in the membership map.
		delete(m.dotMemberships, m.pickerMembershipName)
	}
	m.mode = nextMode
	m.pickerGroups = nil
	m.pickerCursor = 0
	m.pickerMembershipKind = ""
	m.pickerMembershipName = ""
	m.pickerMembershipKey = ""
	m.pickerDotExtractParent = ""
	m.pickerDotExtractSub = ""
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
	case pickerMembershipSkill:
		if m.pickerMembershipName == "" {
			return
		}
		if m.skillsMemberships == nil {
			m.skillsMemberships = make(map[string][]string)
		}
		m.skillsMemberships[m.pickerMembershipName] = append([]string(nil), m.pickerOriginalGroups...)
	case pickerMembershipMcp:
		if m.pickerMembershipName == "" {
			return
		}
		if m.mcpMemberships == nil {
			m.mcpMemberships = make(map[string][]string)
		}
		m.mcpMemberships[m.pickerMembershipName] = append([]string(nil), m.pickerOriginalGroups...)
	case pickerMembershipPlugin:
		if m.pickerMembershipName == "" {
			return
		}
		if m.pluginMemberships == nil {
			m.pluginMemberships = make(map[string][]string)
		}
		m.pluginMemberships[m.pickerMembershipName] = append([]string(nil), m.pickerOriginalGroups...)
	case pickerMembershipMarketplace:
		if m.pickerMembershipName == "" {
			return
		}
		if m.marketplaceMemberships == nil {
			m.marketplaceMemberships = make(map[string][]string)
		}
		m.marketplaceMemberships[m.pickerMembershipName] = append([]string(nil), m.pickerOriginalGroups...)
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
	if m.pickerMembershipKind == pickerMembershipSkill {
		if m.pickerMembershipName == "" {
			return "", nil, false
		}
		return m.pickerMembershipName, append([]string(nil), m.skillPickerMemberships()...), true
	}
	if m.pickerMembershipKind == pickerMembershipMcp {
		if m.pickerMembershipName == "" {
			return "", nil, false
		}
		return m.pickerMembershipName, append([]string(nil), m.mcpPickerMemberships()...), true
	}
	if m.pickerMembershipKind == pickerMembershipPlugin {
		if m.pickerMembershipName == "" {
			return "", nil, false
		}
		return m.pickerMembershipName, append([]string(nil), m.pluginPickerMemberships()...), true
	}
	if m.pickerMembershipKind == pickerMembershipMarketplace {
		if m.pickerMembershipName == "" {
			return "", nil, false
		}
		return m.pickerMembershipName, append([]string(nil), m.marketplacePickerMemberships()...), true
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
	if m.pickerMembershipKind == pickerMembershipSkill {
		if m.skillsMemberships == nil {
			m.skillsMemberships = make(map[string][]string)
		}
		m.skillsMemberships[m.pickerMembershipName] = memberships
		return
	}
	if m.pickerMembershipKind == pickerMembershipMcp {
		if m.mcpMemberships == nil {
			m.mcpMemberships = make(map[string][]string)
		}
		m.mcpMemberships[m.pickerMembershipName] = memberships
		return
	}
	if m.pickerMembershipKind == pickerMembershipPlugin {
		if m.pluginMemberships == nil {
			m.pluginMemberships = make(map[string][]string)
		}
		m.pluginMemberships[m.pickerMembershipName] = memberships
		return
	}
	if m.pickerMembershipKind == pickerMembershipMarketplace {
		if m.marketplaceMemberships == nil {
			m.marketplaceMemberships = make(map[string][]string)
		}
		m.marketplaceMemberships[m.pickerMembershipName] = memberships
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
	_, current, ok := m.selectedMembershipTarget()
	if !ok || m.pickerCursor < 0 || m.pickerCursor >= len(m.pickerGroups) {
		return nil
	}
	group := m.pickerGroups[m.pickerCursor]
	if isNewGroupSentinel(group) {
		return nil
	}
	// An item may belong to any number of host groups but at most one reusable
	// group. m.groupNames holds the reusable names; groups created during this
	// picker session are reusable too.
	reusable := app.ReusablePredicate(m.groupNames, m.pickerCreatedGroups)
	m.setSelectedMemberships(app.MembershipInvariantToggle(current, group, reusable))
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
	_, current, ok := m.selectedMembershipTarget()
	if !ok {
		return
	}
	// A freshly created group is reusable; adding it evicts any other reusable
	// membership but keeps host-group memberships intact.
	reusable := app.ReusablePredicate(m.groupNames, m.pickerCreatedGroups)
	m.setSelectedMemberships(app.MembershipInvariantToggle(current, newGroup, reusable))
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
	if !app.GroupMembershipsChanged(next, m.pickerOriginalGroups) {
		m.finishGroupMembershipPicker()
		return
	}
	created := app.CreatedMembershipGroups(m.pickerCreatedGroups, next)
	host := ""
	if m.hostInfo != nil {
		host = m.hostInfo.Active
	}
	switch m.pickerMembershipKind {
	case pickerMembershipDot:
		if m.pickerDotExtractParent != "" {
			parent, sub := m.pickerDotExtractParent, m.pickerDotExtractSub
			m.beginDotsOperation("Extracting " + name + "…")
			*cmds = append(*cmds, m.spinner.Tick, m.doExtractDotIntoGroups(parent, sub, name, next, created, host))
			m.finishGroupMembershipPicker()
			return
		}
		m.beginDotsOperation("Updating groups for " + name + "…")
		*cmds = append(*cmds, m.spinner.Tick, m.doSetDotGroupMemberships(name, m.pickerOriginalGroups, next, created, host))
	case pickerMembershipSkill:
		*cmds = append(*cmds, m.doSetSkillGroupMemberships(name, next, created, host))
	case pickerMembershipMcp:
		*cmds = append(*cmds, m.doSetMcpGroupMemberships(name, next))
	case pickerMembershipPlugin:
		*cmds = append(*cmds, m.doSetPluginGroupMemberships(name, next))
	case pickerMembershipMarketplace:
		*cmds = append(*cmds, m.doSetMarketplaceGroupMemberships(name, next))
	default:
		m.loading = true
		startOp(m, "Updating groups for "+name+"…")
		*cmds = append(*cmds, m.spinner.Tick, m.doSetToolGroupMemberships(name, m.pickerOriginalGroups, next, created, host))
	}
	m.finishGroupMembershipPicker()
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

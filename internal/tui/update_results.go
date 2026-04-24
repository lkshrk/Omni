package tui

import (
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/config"
)

func (m *Model) handleDescRefreshDoneMsg(msg descRefreshDoneMsg) {
	if msg.gen != m.descRefreshGen {
		return
	}
	if msg.tools != nil {
		m.allTools = msg.tools
		m.applyFilter()
	}
}

func (m *Model) handleProgressMsg(msg progressMsg) []tea.Cmd {
	if msg.gen != m.progressGen {
		return nil
	}
	m.progressText = msg.text
	if msg.rowKey != "" {
		delete(m.bulkPendingKeys, msg.rowKey)
		switch {
		case msg.rowErr != "":
			m.clearRowOperation()
			m.setToolActionError(msg.rowKey, msg.rowErr)
		case msg.rowDone:
			if m.rowOpKey == msg.rowKey {
				m.clearRowOperation()
			}
			m.clearToolActionError(msg.rowKey)
		case msg.rowStatus != "":
			parts := strings.SplitN(msg.rowKey, "\x00", 2)
			if len(parts) == 2 {
				m.startRowOperation(parts[0], parts[1], msg.rowStatus)
			}
		}
	}
	if m.progressCh != nil {
		return []tea.Cmd{waitForProgress(m.progressCh, m.progressGen)}
	}
	return nil
}

func (m *Model) handleProgressStreamClosedMsg(msg progressStreamClosedMsg) {
	if msg.gen != m.progressGen {
		return
	}
	m.progressCh = nil
}

func (m *Model) handleProgressDoneMsg(msg progressDoneMsg) []tea.Cmd {
	var cmds []tea.Cmd
	if msg.gen != m.progressGen {
		return nil
	}
	m.progressGen++
	if !m.migrating {
		m.loading = false
	}
	delete(m.upgradingKeys, msg.key)
	m.progressText = ""
	m.progressCh = nil
	m.clearRowOperation()
	m.clearBulkPending()
	if msg.tools != nil {
		m.allTools = msg.tools
	}
	if len(msg.claimedNames) > 0 {
		m.removeDiscoveredByName(msg.claimedNames)
	}
	if msg.toolGroups != nil {
		m.toolGroups = msg.toolGroups
	}
	if msg.toolMemberships != nil {
		m.toolMemberships = msg.toolMemberships
	}
	if msg.groupNames != nil {
		m.groupNames = msg.groupNames
	}
	m.applyFilter()
	if msg.rowErrors != nil {
		m.clearRowActionError()
		for key, message := range msg.rowErrors {
			m.setToolActionError(key, message)
		}
	}
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
	} else if msg.message != "" {
		if msg.rowErrors == nil {
			m.clearRowActionError()
		}
		cmds = append(cmds, setStatus(m, "✓ "+msg.message, false))
	}
	return cmds
}

func (m *Model) removeDiscoveredByName(names []string) {
	if len(names) == 0 || len(m.discoveredTools) == 0 {
		return
	}
	claimed := make(map[string]struct{}, len(names))
	for _, name := range names {
		claimed[name] = struct{}{}
	}
	remaining := m.discoveredTools[:0]
	for _, t := range m.discoveredTools {
		if _, ok := claimed[t.Name]; ok {
			continue
		}
		remaining = append(remaining, t)
	}
	m.discoveredTools = remaining
	m.rebuildDiscoveredKeys()
}

func (m *Model) handleOpCompleteMsg(msg opCompleteMsg) []tea.Cmd {
	var cmds []tea.Cmd
	m.loading = false
	rowErrKey := msg.key
	if rowErrKey == "" {
		rowErrKey = m.rowOpKey
	}
	m.clearRowOperation()
	delete(m.upgradingKeys, msg.key)
	if msg.err != nil {
		m.setToolActionError(rowErrKey, msg.err.Error())
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
	} else {
		m.clearRowActionError()
		cmds = append(cmds, setStatus(m, "✓ "+msg.message, false))
		if msg.tools != nil {
			m.allTools = msg.tools
		}
		if msg.toolProviderPins != nil {
			m.toolProviderPins = msg.toolProviderPins
		}
		if msg.tools != nil {
			m.applyFilter()
		}
	}
	return cmds
}

func (m *Model) handleCreateGroupDoneMsg(msg createGroupDoneMsg) []tea.Cmd {
	var cmds []tea.Cmd
	m.loading = false
	m.groupCreating = false
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
		return cmds
	}
	cmds = append(cmds, setStatus(m, "✓ group "+msg.name+" created", false))
	if msg.groupNames != nil {
		m.groupNames = msg.groupNames
		allGroupNames := buildAllGroupNames(m.groupNames)
		for i, name := range allGroupNames {
			if name == msg.name {
				m.groupCursor = i
				break
			}
		}
	}
	return cmds
}

func (m *Model) handleProfileActivatedMsg(msg profileActivatedMsg) []tea.Cmd {
	if msg.err != nil {
		return []tea.Cmd{setStatus(m, "✗ "+msg.err.Error(), true)}
	}
	if msg.info != nil {
		m.profileInfo = msg.info
	}
	m.applyActivatedProfile(msg.profile)
	return []tea.Cmd{setStatus(m, "✓ activated profile "+msg.profile, false)}
}

func (m *Model) handleGroupChangedMsg(msg groupChangedMsg) []tea.Cmd {
	var cmds []tea.Cmd
	m.loading = false
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
		return cmds
	}
	m.clearRowActionError()
	cmds = append(cmds, setStatus(m, msg.detail, false))
	if msg.tools != nil {
		m.allTools = msg.tools
	}
	if msg.toolGroups != nil {
		m.toolGroups = msg.toolGroups
	}
	if msg.groupNames != nil {
		m.groupNames = msg.groupNames
		allGroupNames := buildAllGroupNames(m.groupNames)
		if m.groupCursor >= len(allGroupNames) {
			m.groupCursor = max(len(allGroupNames)-1, 0)
		}
	}
	if msg.info != nil {
		m.profileInfo = msg.info
	}
	m.applyFilter()
	return cmds
}

func (m *Model) handleGroupToolsChangedMsg(msg groupToolsChangedMsg) []tea.Cmd {
	var cmds []tea.Cmd
	m.loading = false
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
		return cmds
	}
	m.clearRowActionError()
	cmds = append(cmds, setStatus(m, msg.detail, false))
	if msg.tools != nil {
		m.allTools = msg.tools
	}
	if msg.toolGroups != nil {
		m.toolGroups = msg.toolGroups
	}
	if msg.toolMemberships != nil {
		m.toolMemberships = msg.toolMemberships
	}
	if msg.groupNames != nil {
		m.groupNames = msg.groupNames
	}
	if msg.ignoreLabels != nil {
		m.ignoreLabels = msg.ignoreLabels
	}
	if msg.toolIgnoreSet != nil {
		m.toolIgnoreSet = msg.toolIgnoreSet
	}
	if msg.groupIgnoreSet != nil {
		m.groupIgnoreSet = msg.groupIgnoreSet
	}
	m.applyFilter()
	return cmds
}

func (m *Model) handleGroupDotsChangedMsg(msg groupDotsChangedMsg) []tea.Cmd {
	var cmds []tea.Cmd
	m.loading = false
	if msg.err != nil {
		if msg.dotMemberships != nil {
			m.dotMemberships = msg.dotMemberships
		}
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
		return cmds
	}
	cmds = append(cmds, setStatus(m, msg.detail, false))
	if msg.dotMemberships != nil {
		m.dotMemberships = msg.dotMemberships
	}
	if msg.entries != nil {
		m.applyDotsSnapshot(msg.entries, msg.gitStatus, msg.dotMemberships)
	}
	return cmds
}

func (m *Model) handleSettingsSavedMsg(msg settingsSavedMsg) []tea.Cmd {
	if msg.gen != 0 && (!m.settingsSaveRunning || msg.gen != m.settingsSaveInFlightGen) {
		return nil
	}
	if msg.gen != 0 && m.settingsSaveQueued {
		snapshot := m.settingsSaveQueuedSnapshot
		gen := m.settingsSaveQueuedGen
		m.settingsSaveQueued = false
		m.settingsSaveQueuedSnapshot = config.Settings{}
		m.settingsSaveQueuedGen = 0
		return []tea.Cmd{m.startSettingsSave(snapshot, gen)}
	}
	if msg.gen != 0 {
		m.settingsSaveRunning = false
		m.settingsSaveInFlightGen = 0
	}
	if msg.err != nil {
		return []tea.Cmd{setStatus(m, "✗ "+msg.err.Error(), true)}
	}
	return []tea.Cmd{setStatus(m, "✓ settings saved", false)}
}

func (m *Model) handleDangerOpDoneMsg(msg dangerOpDoneMsg) []tea.Cmd {
	var cmds []tea.Cmd
	m.loading = false
	if msg.action == "enable-dots" {
		if !m.finishDotsOperation(msg.dotsGen) {
			return cmds
		}
	}
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.action+": "+msg.err.Error(), true))
	} else {
		text := "✓ " + msg.action
		if msg.detail != "" {
			text = "✓ " + msg.detail
		}
		cmds = append(cmds, setStatus(m, text, false))
	}
	if msg.reload {
		if msg.mode != viewList {
			m.setupBackgroundMode = msg.mode
		}
		m.loading = true
		cmds = append(cmds, m.spinner.Tick, loadTools(m.app, m.ctx))
	} else if len(msg.tools) > 0 {
		m.allTools = msg.tools
		m.applyFilter()
	}
	if msg.action == "disable-dots" && msg.err == nil {
		m.dotsEntries = nil
		m.dotsGitStatus = ""
		m.dotsLoaded = false
	} else if msg.action == "enable-dots" && msg.err == nil {
		m.mode = viewDots
		m.dotsLoaded = false
	}
	return cmds
}

func (m *Model) handleProfileGroupChangedMsg(msg profileGroupChangedMsg) []tea.Cmd {
	var cmds []tea.Cmd
	m.loading = false
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
		return cmds
	}
	if msg.info != nil {
		m.profileInfo = msg.info
		if msg.detail != "" && msg.profile != "" {
			m.placeProfileCursor(msg.profile)
		}
	}
	statusText := msg.detail
	if statusText == "" {
		statusText = "✓ " + msg.profile + " deleted"
	}
	if msg.group != "" {
		verb := "added to"
		if !msg.added {
			verb = "removed from"
		}
		statusText = "✓ " + msg.group + " " + verb + " " + msg.profile
	} else if m.profileInfo != nil {
		if n := len(m.profileInfo.Profiles); m.profileCursor >= n {
			m.profileCursor = max(n-1, 0)
		}
	}
	cmds = append(cmds, setStatus(m, statusText, false))
	return cmds
}

func (m *Model) handleProfileCreatedMsg(msg profileCreatedMsg) []tea.Cmd {
	var cmds []tea.Cmd
	m.loading = false
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
		return cmds
	}
	if msg.info != nil {
		m.profileInfo = msg.info
		m.placeProfileCursor(msg.profile)
	}
	cmds = append(cmds, setStatus(m, "✓ profile "+msg.profile+" created", false))
	return cmds
}

func (m *Model) placeProfileCursor(profile string) {
	if m.profileInfo == nil {
		return
	}
	names := make([]string, 0, len(m.profileInfo.Profiles))
	for name := range m.profileInfo.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	for i, name := range names {
		if name == profile {
			m.profileCursor = i
			return
		}
	}
}

func (m *Model) handleClaimDoneMsg(msg claimDoneMsg) []tea.Cmd {
	var cmds []tea.Cmd
	m.loading = false
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
		return cmds
	}
	cmds = append(cmds, setStatusFor(m, claimSuccessStatus(msg), false, statusDurationErr))
	for i, t := range m.discoveredTools {
		if t.Name == msg.name {
			m.discoveredTools = append(m.discoveredTools[:i], m.discoveredTools[i+1:]...)
			m.rebuildDiscoveredKeys()
			break
		}
	}
	if msg.tools != nil {
		m.allTools = msg.tools
		m.applyFilter()
	}
	return cmds
}

func (m *Model) handleIgnoreDoneMsg(msg ignoreDoneMsg) []tea.Cmd {
	var cmds []tea.Cmd
	m.loading = false
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
		return cmds
	}
	m.clearRowActionError()
	verb := "ignored"
	if !msg.ignored {
		verb = "un-ignored"
		if msg.profileScope || msg.ignoreLabels == nil {
			delete(m.ignoreSet, msg.name)
		}
	} else {
		if msg.profileScope || msg.ignoreLabels == nil {
			m.ignoreSet[msg.name] = true
		}
	}
	cmds = append(cmds, setStatus(m, "✓ "+msg.name+" "+verb, false))
	if msg.ignoreLabels != nil {
		m.ignoreLabels = msg.ignoreLabels
	}
	if msg.toolIgnoreSet != nil {
		m.toolIgnoreSet = msg.toolIgnoreSet
	}
	if msg.groupIgnoreSet != nil {
		m.groupIgnoreSet = msg.groupIgnoreSet
	}
	if msg.tools != nil {
		m.allTools = msg.tools
		m.applyFilter()
	}
	return cmds
}

func (m *Model) handleMigrateProviderDoneMsg(msg migrateProviderDoneMsg) []tea.Cmd {
	var cmds []tea.Cmd
	m.loading = false
	m.migrating = false
	m.clearRowOperation()
	if msg.err != nil {
		m.setToolActionError(toolKey(msg.name, msg.toProvider), msg.err.Error())
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
		return cmds
	}
	m.clearRowActionError()
	cmds = append(cmds, setStatusFor(m, migrateProviderSuccessStatus(msg), false, statusDurationErr))
	if msg.tools != nil {
		m.allTools = msg.tools
		m.applyFilter()
	}
	return cmds
}

func claimSuccessStatus(msg claimDoneMsg) string {
	group := msg.groupName
	if group == "" {
		group = "base"
	}
	return "✓ added " + msg.name + " to config (" + group + ")"
}

func migrateProviderSuccessStatus(msg migrateProviderDoneMsg) string {
	switch {
	case msg.fromProvider != "" && msg.toProvider != "":
		return "✓ reinstalled " + msg.name + " with default (" + msg.toProvider + "), removed " + msg.fromProvider
	case msg.toProvider != "":
		return "✓ reinstalled " + msg.name + " with default (" + msg.toProvider + ")"
	default:
		return "✓ reinstalled " + msg.name + " with default provider"
	}
}

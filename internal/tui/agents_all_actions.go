package tui

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// Runs before the per-feature handlers, which have no notion of the row's agentID or status; handled=false falls through.
func (m *Model) handleAgentsAllRowActionKeyMsg(msg tea.KeyPressMsg) (handled bool, cmds []tea.Cmd) {
	entry, ok := agentsAllEntryAt(*m, m.agentsAllCursor)
	if !ok {
		return false, nil
	}

	switch msg.String() {
	case "u":
		if agentsResolveEligible(entry) {
			return m.agentsRowArmResolve(entry, false)
		}
		return m.agentsRowUpgrade(entry)
	case "l":
		return m.agentsRowArmResolve(entry, true)
	case "i":
		return m.agentsRowInstall(entry)
	case "c":
		return m.agentsRowClaim(entry)
	case "g":
		return m.agentsRowGroup(entry)
	case "x":
		return m.agentsRowToggleIgnore(entry)
	case "d":
		return m.agentsRowArmDelete(entry)
	default:
		return false, nil
	}
}

// Shared by agentsRowUpgrade and agentsRowHints so the two cannot drift into different, individually-wrong conditions.
func agentsSkillsUpgradeEligible(e agentsAllRow) bool {
	return e.status == agentsStatusInstalled || (e.status == agentsStatusOutOfSync && agentsMarkNeedsInstall(e.mark))
}

// Eligible on every row, including never-installed ones: a name-based ignore for a not-yet-installed item is a legitimate preemptive filter, not a dangling entry.
func agentsIgnoreEligible(agentsAllRow) bool {
	return true
}

// Shared by agentsRowInstall and agentsRowHints so the two cannot drift into different, individually-wrong conditions.
func agentsInstallEligible(e agentsAllRow) bool {
	// Marketplaces have no per-item install; agentsRowInstall rejects them, so the hint must not promise 'i'.
	if e.feature == agentsSectionMarketplaces {
		return false
	}
	// A drifted row has a live entry already; replacing it is the explicit resolve action, never install.
	if e.mark == agentsMarkDrifted {
		return false
	}
	return e.status == agentsStatusOutOfSync && agentsMarkNeedsInstall(e.mark)
}

// Its own predicate rather than delegating to the now-always-true agentsIgnoreEligible, so group eligibility did not silently widen to Available/orphan rows.
func agentsGroupEligible(e agentsAllRow) bool {
	return !e.synthetic && e.status != agentsStatusAvailable && e.mark != agentsMarkOrphan
}

// Available rows and synthetic ignore-only rows have nothing to delete.
func agentsDeleteEligible(e agentsAllRow) bool {
	if e.feature == agentsSectionSkills && e.mark == agentsMarkOrphan {
		return true
	}
	return !e.synthetic && e.status != agentsStatusAvailable && e.mark != agentsMarkOrphan
}

func agentsDeleteHintLabel(e agentsAllRow) string {
	if e.feature == agentsSectionSkills && e.mark == agentsMarkOrphan {
		return "uninstall"
	}
	return "delete"
}

func agentsAdoptEligible(e agentsAllRow) bool {
	return e.mark == agentsMarkOrphan
}

func (m *Model) agentsRowUpgrade(e agentsAllRow) (bool, []tea.Cmd) {
	if e.feature == agentsSectionPlugins && e.localIdx >= 0 && e.localIdx < len(m.pluginRows) && e.status == agentsStatusUpdates {
		name := m.pluginRows[e.localIdx].Name
		m.pluginRunning = true
		m.pluginErr = nil
		m.startAgentsOp(agentsRowRunKey(e))
		return true, []tea.Cmd{m.spinner.Tick, m.doUpdatePlugin(name)}
	}
	if e.feature == agentsSectionSkills && agentsSkillsUpgradeEligible(e) {
		m.skillsRunning = true
		m.skillsErr = nil
		m.startAgentsOp(agentsRowRunKey(e))
		return true, []tea.Cmd{m.spinner.Tick, m.doUpdateSkills()}
	}
	return false, nil
}

func (m *Model) agentsRowInstall(e agentsAllRow) (bool, []tea.Cmd) {
	if !agentsInstallEligible(e) {
		return false, nil
	}
	switch e.feature {
	case agentsSectionSkills:
		rows, _, _ := skillsVisibleRows(*m)
		if e.localIdx < 0 || e.localIdx >= len(rows) {
			return false, nil
		}
		src := rows[e.localIdx].Source
		m.skillAddRunning = true
		m.skillsErr = nil
		m.startAgentsOp(agentsRowRunKey(e))
		return true, []tea.Cmd{m.spinner.Tick, m.doAddSkillPackage(src)}
	case agentsSectionMcp:
		if e.localIdx >= len(m.mcpRows) || e.agentID == "" {
			return false, nil
		}
		name := m.mcpRows[e.localIdx].Name
		m.mcpRunning = true
		m.mcpErr = nil
		m.startAgentsOp(agentsRowRunKey(e))
		return true, []tea.Cmd{m.spinner.Tick, m.doInstallMcpServer(name, e.agentID)}
	case agentsSectionPlugins:
		if e.localIdx >= len(m.pluginRows) || e.agentID == "" {
			return false, nil
		}
		name := m.pluginRows[e.localIdx].Name
		m.pluginRunning = true
		m.pluginErr = nil
		m.startAgentsOp(agentsRowRunKey(e))
		return true, []tea.Cmd{m.spinner.Tick, m.doInstallPlugin(name, e.agentID)}
	case agentsSectionMarketplaces:
		return false, nil
	default:
		return false, nil
	}
}

// Adoption and group assignment both happen on picker confirm, not on this keypress, mirroring the tools tab's 'c' flow.
func (m *Model) agentsRowClaim(e agentsAllRow) (bool, []tea.Cmd) {
	if !agentsAdoptEligible(e) {
		return false, nil
	}
	switch e.feature {
	case agentsSectionSkills:
		rows, _, _ := skillsVisibleRows(*m)
		if e.localIdx < 0 || e.localIdx >= len(rows) {
			return false, nil
		}
	case agentsSectionMcp:
		if e.localIdx < len(m.mcpRows) || e.agentID == "" {
			return false, nil
		}
		flat := mcpUnmanagedFlat(m.mcpUnmanaged)
		idx := e.localIdx - len(m.mcpRows)
		if idx < 0 || idx >= len(flat) {
			return false, nil
		}
	case agentsSectionMarketplaces:
		if e.localIdx < len(m.marketplaceRows) || e.agentID == "" {
			return false, nil
		}
		flat := marketplaceUnmanagedFlat(m.marketplaceUnmanaged)
		idx := e.localIdx - len(m.marketplaceRows)
		if idx < 0 || idx >= len(flat) {
			return false, nil
		}
	default:
		if e.localIdx < len(m.pluginRows) || e.agentID == "" {
			return false, nil
		}
		flat := pluginUnmanagedFlat(m.pluginUnmanaged)
		idx := e.localIdx - len(m.pluginRows)
		if idx < 0 || idx >= len(flat) {
			return false, nil
		}
	}
	m.openAgentsClaimGroupPicker(e)
	return true, nil
}

func (m *Model) agentsRowGroup(e agentsAllRow) (bool, []tea.Cmd) {
	if !agentsGroupEligible(e) {
		return false, nil
	}
	switch e.feature {
	case agentsSectionSkills:
		m.skillsCursor = e.localIdx
		m.openSkillGroupMembershipPicker()
		return true, nil
	case agentsSectionMcp:
		if e.localIdx >= len(m.mcpRows) {
			return false, nil
		}
		m.mcpCursor = e.localIdx
		m.mcpCursorAgentID = e.agentID
		m.openMcpGroupMembershipPicker()
		return true, nil
	case agentsSectionPlugins:
		if e.localIdx >= len(m.pluginRows) {
			return false, nil
		}
		m.pluginCursor = e.localIdx
		m.pluginCursorAgentID = e.agentID
		m.openPluginGroupMembershipPicker()
		return true, nil
	case agentsSectionMarketplaces:
		if e.localIdx >= len(m.marketplaceRows) {
			return false, nil
		}
		m.marketplaceCursor = e.localIdx
		m.marketplaceCursorAgentID = e.agentID
		m.openMarketplaceGroupMembershipPicker()
		return true, nil
	default:
		return false, nil
	}
}

// Two-step: the first 'x' arms, the second executes (see handleAgentsIgnoreConfirmKeyMsg), any other key cancels.
func (m *Model) agentsRowToggleIgnore(e agentsAllRow) (bool, []tea.Cmd) {
	name := agentsRowName(*m, e)
	if name == "" || !agentsIgnoreEligible(e) {
		return false, nil
	}
	m.agentsIgnoreConfirm = true
	m.agentsIgnoreFeature = e.feature
	m.agentsIgnoreName = name
	m.agentsIgnoreOpKey = agentsRowRunKey(e)
	return true, []tea.Cmd{m.armConfirmationTimeout()}
}

func (m *Model) handleAgentsIgnoreConfirmKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	switch msg.String() {
	case "x":
		m.cancelConfirmationTimeout()
		feature, name := m.agentsIgnoreFeature, m.agentsIgnoreName
		m.agentsIgnoreConfirm = false
		m.agentsIgnoreName = ""
		m.startAgentsOp(m.agentsIgnoreOpKey)
		m.agentsIgnoreOpKey = ""
		// Reload happens on agentsIgnoreToggledMsg — batching it here races the config read against the toggle's write and shows stale rows.
		return []tea.Cmd{m.doToggleAgentsIgnore(feature, name)}
	default:
		m.cancelConfirmationTimeout()
		m.agentsIgnoreConfirm = false
		m.agentsIgnoreName = ""
		m.agentsIgnoreOpKey = ""
		return nil
	}
}

// Drift and managed-package shadowing have two valid owners; there 'u' resolves rather than upgrades.
func agentsResolveEligible(e agentsAllRow) bool {
	if e.synthetic {
		return false
	}
	if e.feature == agentsSectionSkills && e.mark == agentsMarkPackageShadowed {
		return true
	}
	if e.mark != agentsMarkDrifted {
		return false
	}
	switch e.feature {
	case agentsSectionSkills, agentsSectionMcp, agentsSectionPlugins:
		return true
	default:
		return false
	}
}

// Lets the drift keys behave the same under the skills chip as on the all chip.
func skillsChipCursorRow(m Model) (agentsAllRow, bool) {
	rows, findStart, unmanagedStart := skillsVisibleRows(m)
	if m.skillsCursor < 0 || m.skillsCursor >= skillsManagedRowsEnd(findStart, unmanagedStart, len(rows)) {
		return agentsAllRow{}, false
	}
	row := rows[m.skillsCursor]
	status, mark := skillPackageRowStatus(
		row.Installed,
		row.ShadowedByPlugin,
		len(skillShadowedAgents(row, m.enabledAgents)) > 0 &&
			len(skillMissingAgents(row, m.enabledAgents)) == 0,
		len(skillDriftedAgents(row, m.enabledAgents)) > 0,
		skillRowOutdated(row),
	)
	return agentsAllRow{
		feature:  agentsSectionSkills,
		localIdx: m.skillsCursor,
		status:   status,
		mark:     mark,
		sortName: row.Name,
	}, true
}

// Lets the drift keys behave the same under the mcp chip as on the all chip.
func mcpChipCursorRow(m Model) (agentsAllRow, bool) {
	if m.mcpCursor < 0 || m.mcpCursor >= len(m.mcpRows) {
		return agentsAllRow{}, false
	}
	row := m.mcpRows[m.mcpCursor]
	mark := agentsMarkNone
	status := agentsStatusInstalled
	if row.Drifted {
		mark, status = agentsMarkDrifted, agentsStatusOutOfSync
	}
	return agentsAllRow{
		feature:  agentsSectionMcp,
		localIdx: m.mcpCursor,
		status:   status,
		mark:     mark,
		sortName: row.Name,
	}, true
}

func pluginChipCursorRow(m Model) (agentsAllRow, bool) {
	if m.pluginCursor < 0 || m.pluginCursor >= len(m.pluginRows) {
		return agentsAllRow{}, false
	}
	row := m.pluginRows[m.pluginCursor]
	mark := agentsMarkNone
	status := agentsStatusInstalled
	if row.Drifted {
		mark, status = agentsMarkDrifted, agentsStatusOutOfSync
	}
	return agentsAllRow{
		feature:  agentsSectionPlugins,
		localIdx: m.pluginCursor,
		status:   status,
		mark:     mark,
		sortName: row.Name,
	}, true
}

func (m *Model) openAgentsDriftPrompt(drift []string) {
	if len(drift) == 0 {
		return
	}
	m.agentsDriftPromptOpen = true
	m.agentsDriftPromptLines = drift
	clearStatus(m)
}

func (m *Model) closeAgentsDriftPrompt() {
	m.agentsDriftPromptOpen = false
	m.agentsDriftPromptLines = nil
}

// Holding every keypress while the modal is up is what lets U/L mean the batch resolutions without colliding with the tab's "U upgrade all" underneath.
func (m *Model) handleAgentsDriftPromptKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	if msg.IsRepeat {
		return nil
	}
	if m.agentsBulkResolveConfirm {
		return m.handleAgentsBulkResolveConfirmKeyMsg(msg)
	}
	useManaged := key.Matches(msg, m.keys.AgentsUseManagedAll)
	if !useManaged && !key.Matches(msg, m.keys.AgentsUseLocalAll) {
		m.dismissAgentsDriftPromptOn(msg)
		return nil
	}
	m.agentsBulkResolveConfirm = true
	m.agentsBulkResolveUseManaged = useManaged
	return []tea.Cmd{m.armConfirmationTimeout()}
}

// The modal opens unbidden at the end of a run, so the batch keys arm here and only the second press writes.
func (m *Model) handleAgentsBulkResolveConfirmKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	armed := m.keys.AgentsUseLocalAll
	if m.agentsBulkResolveUseManaged {
		armed = m.keys.AgentsUseManagedAll
	}
	useManaged := m.agentsBulkResolveUseManaged
	m.cancelConfirmationTimeout()
	m.agentsBulkResolveConfirm = false
	m.agentsBulkResolveUseManaged = false
	if !key.Matches(msg, armed) {
		m.dismissAgentsDriftPromptOn(msg)
		return nil
	}
	m.closeAgentsDriftPrompt()
	if m.agentsOpInFlight() {
		return []tea.Cmd{setStatus(m, agentsBusyStatus, true)}
	}
	m.skillsRunning, m.mcpRunning, m.pluginRunning = true, true, true
	return []tea.Cmd{m.spinner.Tick, m.doAgentsBulkResolve(useManaged)}
}

func (m *Model) dismissAgentsDriftPromptOn(msg tea.KeyPressMsg) {
	if key.Matches(msg, m.keys.Back) || msg.String() == "q" {
		m.closeAgentsDriftPrompt()
	}
}

// Two-step: the first press arms, the second executes (see handleAgentsResolveConfirmKeyMsg), any other key cancels.
func (m *Model) agentsRowArmResolve(e agentsAllRow, useLocal bool) (bool, []tea.Cmd) {
	if !agentsResolveEligible(e) {
		return false, nil
	}
	source := agentsRowName(*m, e)
	if source == "" {
		return false, nil
	}
	m.agentsResolveConfirm = true
	m.agentsResolveUseLocal = useLocal
	m.agentsResolveFeature = e.feature
	m.agentsResolveSource = source
	m.agentsResolveOpKey = agentsRowRunKey(e)
	return true, []tea.Cmd{m.armConfirmationTimeout()}
}

func (m *Model) handleAgentsResolveConfirmKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	key := "u"
	if m.agentsResolveUseLocal {
		key = "l"
	}
	if msg.String() != key {
		m.cancelConfirmationTimeout()
		m.agentsResolveConfirm = false
		m.agentsResolveSource = ""
		m.agentsResolveOpKey = ""
		return nil
	}
	m.cancelConfirmationTimeout()
	source, useLocal, feature := m.agentsResolveSource, m.agentsResolveUseLocal, m.agentsResolveFeature
	m.agentsResolveConfirm = false
	m.agentsResolveSource = ""
	if m.agentsOpInFlight() {
		m.agentsResolveOpKey = ""
		return []tea.Cmd{setStatus(m, agentsBusyStatus, true)}
	}
	m.startAgentsOp(m.agentsResolveOpKey)
	m.agentsResolveOpKey = ""
	switch feature {
	case agentsSectionMcp:
		m.mcpRunning = true
		m.mcpErr = nil
		return []tea.Cmd{m.spinner.Tick, m.doResolveMcpDrift(source, useLocal)}
	case agentsSectionPlugins:
		m.pluginRunning = true
		m.pluginErr = nil
		return []tea.Cmd{m.spinner.Tick, m.doResolvePluginDrift(source, useLocal)}
	default:
		m.skillsRunning = true
		m.skillsErr = nil
		return []tea.Cmd{m.spinner.Tick, m.doResolveSkillDrift(source, useLocal)}
	}
}

func (m *Model) agentsRowArmDelete(e agentsAllRow) (bool, []tea.Cmd) {
	if !agentsDeleteEligible(e) {
		return false, nil
	}
	name := agentsRowName(*m, e)
	if name == "" {
		return false, nil
	}
	m.agentsDeleteConfirm = true
	m.agentsDeleteUninstall = e.feature == agentsSectionSkills && e.mark == agentsMarkOrphan
	m.agentsDeleteFeature = e.feature
	m.agentsDeleteName = name
	m.agentsDeleteOpKey = agentsRowRunKey(e)
	return true, []tea.Cmd{m.armConfirmationTimeout()}
}

func (m *Model) handleAgentsDeleteConfirmKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	switch msg.String() {
	case "d":
		m.cancelConfirmationTimeout()
		feature, name := m.agentsDeleteFeature, m.agentsDeleteName
		uninstall := m.agentsDeleteUninstall
		m.agentsDeleteConfirm = false
		m.agentsDeleteUninstall = false
		m.agentsDeleteName = ""
		m.startAgentsOp(m.agentsDeleteOpKey)
		m.agentsDeleteOpKey = ""
		switch feature {
		case agentsSectionSkills:
			m.skillsRunning = true
			m.skillsErr = nil
			if uninstall {
				return []tea.Cmd{m.spinner.Tick, m.doUninstallSkillPackage(name)}
			}
			return []tea.Cmd{m.spinner.Tick, m.doRemoveSkillPackage(name)}
		case agentsSectionMcp:
			m.mcpRunning = true
			m.mcpErr = nil
			return []tea.Cmd{m.spinner.Tick, m.doRemoveMcp(name)}
		case agentsSectionPlugins:
			m.pluginRunning = true
			m.pluginErr = nil
			return []tea.Cmd{m.spinner.Tick, m.doRemovePlugin(name)}
		case agentsSectionMarketplaces:
			m.marketplaceRunning = true
			m.marketplaceErr = nil
			return []tea.Cmd{m.spinner.Tick, m.doRemoveMarketplace(name)}
		default:
			return nil
		}
	default:
		m.cancelConfirmationTimeout()
		m.agentsDeleteConfirm = false
		m.agentsDeleteUninstall = false
		m.agentsDeleteName = ""
		m.agentsDeleteOpKey = ""
		return nil
	}
}

func agentsRowHints(m Model, e agentsAllRow) []hintItem {
	if m.agentsDeleteConfirm {
		label := "delete"
		if m.agentsDeleteUninstall {
			label = "uninstall"
		}
		return []hintItem{pressAgainHint(m.keys.Delete.Help().Key, label)}
	}
	if m.agentsResolveConfirm {
		if m.agentsResolveUseLocal {
			return []hintItem{pressAgainHint(m.keys.AgentsUseLocal.Help().Key, agentsUseLocalHintLabel(m, e))}
		}
		return []hintItem{pressAgainHint(m.keys.AgentsUseManaged.Help().Key, "confirm use managed")}
	}
	if m.agentsIgnoreConfirm {
		label := "confirm ignore"
		if m.agentsIgnoreFeature == e.feature && e.status == agentsStatusIgnored {
			label = "confirm include"
		}
		return []hintItem{pressAgainHint(m.keys.Ignore.Help().Key, label)}
	}
	if m.pluginMarketplaceOfferConfirm {
		return confirmActionItems(m.keys.Confirm, "claim marketplace "+m.pluginMarketplaceOfferMarket+" too", m.keys.Back)
	}

	var hints []hintItem
	if agentsResolveEligible(e) {
		hints = append(hints,
			rawHint(m.keys.AgentsUseManaged.Help().Key, m.keys.AgentsUseManaged.Help().Desc),
			rawHint(m.keys.AgentsUseLocal.Help().Key, m.keys.AgentsUseLocal.Help().Desc),
		)
	} else if e.status == agentsStatusUpdates || (e.feature == agentsSectionSkills && agentsSkillsUpgradeEligible(e)) {
		hints = append(hints, rawHint("u", "upgrade"))
	}
	if agentsInstallEligible(e) {
		hints = append(hints, rawHint("i", "install"))
	}
	if agentsAdoptEligible(e) {
		hints = append(hints, rawHint("c", "claim"))
	}
	if agentsGroupEligible(e) {
		hints = append(hints, rawHint("g", "group"))
	}
	if agentsIgnoreEligible(e) {
		ignoreLabel := "ignore"
		if e.status == agentsStatusIgnored {
			ignoreLabel = "unignore"
		}
		hints = append(hints, rawHint("x", ignoreLabel))
	}
	if agentsDeleteEligible(e) {
		hints = append(hints, rawHint("d", agentsDeleteHintLabel(e)))
	}
	return hints
}

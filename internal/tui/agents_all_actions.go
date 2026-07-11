package tui

import (
	tea "charm.land/bubbletea/v2"
)

// handleAgentsAllRowActionKeyMsg intercepts the tools-vocabulary keys (u/i/g/x/d)
// for the row under m.agentsAllCursor before falling through to the per-feature
// handlers, since those handlers have no notion of the row's agentID or status.
// Returns handled=false for any other key so the caller can fall through.
func (m *Model) handleAgentsAllRowActionKeyMsg(msg tea.KeyPressMsg) (handled bool, cmds []tea.Cmd) {
	entry, ok := agentsAllEntryAt(*m, m.agentsAllCursor)
	if !ok {
		return false, nil
	}

	switch msg.String() {
	case "u":
		return m.agentsRowUpgrade(entry)
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

// agentsSkillsUpgradeEligible reports whether a skills row may show/accept the
// 'u' update action. Shared by agentsRowUpgrade and agentsRowHints so the two
// cannot drift into different, individually-wrong conditions again.
func agentsSkillsUpgradeEligible(e agentsAllRow) bool {
	return e.status == agentsStatusInstalled || (e.status == agentsStatusOutOfSync && e.mark == agentsMarkMissing)
}

// agentsIgnoreEligible reports whether a row may show/accept the 'x'
// ignore/unignore action. Shared by agentsRowToggleIgnore and agentsRowHints
// so the two cannot drift into different, individually-wrong conditions.
// Eligible on every row, including never-installed "Available" search-result
// rows: a name-based ignore entry for a not-yet-installed item is a
// legitimate preemptive filter, not a dangling entry. Ignoring an
// Available/orphan row moves it into the Ignored section on the next
// agentsAllRowsList build (see the find-result branch there).
func agentsIgnoreEligible(agentsAllRow) bool {
	return true
}

// agentsInstallEligible reports whether a row may show/accept the 'i'
// install action. Shared by agentsRowInstall and agentsRowHints so the two
// cannot drift into different, individually-wrong conditions again.
func agentsInstallEligible(e agentsAllRow) bool {
	// Marketplaces have no per-item install; agentsRowInstall rejects them,
	// so the hint must not promise 'i'.
	if e.feature == agentsSectionMarketplaces {
		return false
	}
	return e.status == agentsStatusOutOfSync && e.mark == agentsMarkMissing
}

// agentsGroupEligible reports whether a row may show/accept the 'g' group
// action. Shared by agentsRowGroup and agentsRowHints. Kept as its own
// predicate (rather than delegating to agentsIgnoreEligible, which is now
// always-true) so group-assignment eligibility didn't silently widen to
// Available/orphan rows when ignore-eligibility broadened. Marketplaces are
// eligible on managed rows same as skills/mcp/plugins — full parity, see
// agentsRowGroup's marketplace branch. A synthetic row (orphaned ignore-list
// entry, no live item) has no manifest row to assign a group to.
func agentsGroupEligible(e agentsAllRow) bool {
	return !e.synthetic && e.status != agentsStatusAvailable && e.mark != agentsMarkOrphan
}

// agentsDeleteEligible reports whether a row may show/accept the 'd' action.
// Managed skills/mcp/plugins/marketplaces delete manifest entries; orphan
// skills uninstall from disk via the skills CLI. Available rows and synthetic
// ignore-only rows have nothing to delete.
func agentsDeleteEligible(e agentsAllRow) bool {
	if e.feature == agentsSectionSkills && e.mark == agentsMarkOrphan {
		return true
	}
	return !e.synthetic && e.status != agentsStatusAvailable && e.mark != agentsMarkOrphan
}

// agentsDeleteHintLabel is the footer hint for 'd' on this row.
func agentsDeleteHintLabel(e agentsAllRow) string {
	if e.feature == agentsSectionSkills && e.mark == agentsMarkOrphan {
		return "uninstall"
	}
	return "delete"
}

// agentsAdoptEligible reports whether a row is an orphan (installed outside
// the manifest, not yet tracked) that agentsRowHints should surface a claim
// hint for. All three features (skills, mcp, plugin) claim orphans via 'c'
// (agentsRowClaim).
func agentsAdoptEligible(e agentsAllRow) bool {
	return e.mark == agentsMarkOrphan
}

func (m *Model) agentsRowUpgrade(e agentsAllRow) (bool, []tea.Cmd) {
	if e.feature == agentsSectionPlugins && e.localIdx < len(m.pluginRows) && e.status == agentsStatusUpdates {
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

// agentsRowClaim opens the group-membership picker in claim mode for an
// orphan row (installed outside the manifest), mirroring the tools tab's
// 'c' flow: adoption and group assignment both happen on picker confirm, not
// on this keypress. Mirrors agentsRowInstall's per-feature bounds checks,
// guarded by agentsAdoptEligible instead of agentsInstallEligible.
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

// agentsRowToggleIgnore arms the two-step ignore/unignore confirm for e,
// mirroring agentsRowArmDelete: the first 'x' press arms, the second executes
// (see handleAgentsIgnoreConfirmKeyMsg), and any other key cancels.
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
		// Reload happens on agentsIgnoreToggledMsg — batching it here races
		// the config read against the toggle's write and shows stale rows.
		return []tea.Cmd{m.doToggleAgentsIgnore(feature, name)}
	default:
		m.cancelConfirmationTimeout()
		m.agentsIgnoreConfirm = false
		m.agentsIgnoreName = ""
		m.agentsIgnoreOpKey = ""
		return nil
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

// agentsRowHints builds the tools-vocabulary hint items (u/i/g/x/d) for the
// selected agents-all row, mirroring toolInlineHints' visibility logic.
func agentsRowHints(m Model, e agentsAllRow) []hintItem {
	if m.agentsDeleteConfirm {
		label := "delete"
		if m.agentsDeleteUninstall {
			label = "uninstall"
		}
		return []hintItem{pressAgainHint(m.keys.Delete.Help().Key, label)}
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
	if e.status == agentsStatusUpdates || (e.feature == agentsSectionSkills && agentsSkillsUpgradeEligible(e)) {
		hints = append(hints, rawHint("u", "update"))
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

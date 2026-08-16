package tui

import (
	"errors"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
)

const (
	agentsChipAll = iota
	agentsChipSkills
	agentsChipMcp
	agentsChipPlugin
	agentsChipMarketplace
)

type agentsSection int

const (
	agentsSectionSkills agentsSection = iota
	agentsSectionMcp
	agentsSectionPlugins
	agentsSectionMarketplaces
)

type agentsAllRow struct {
	feature  agentsSection
	localIdx int
	agentID  string
	status   agentsRowStatus
	mark     agentsSyncMark
	sortName string
	// Kept beside status: status names only one condition and drift outranks an update, so a drifted package's pending upgrade would otherwise be invisible.
	outdated bool
	// Manufactured for an ignore-list entry with no live row; carries no valid localIdx, so every consumer must check this flag before touching localIdx. Renders name-only, supports only unignore ('x').
	synthetic bool
}

func skillExpandAgents(r app.SkillPackageRow, enabledAgents []string) []string {
	if len(r.Agents) > 0 {
		return r.Agents
	}
	return enabledAgents
}

// Multiple rows share a localIdx (one per agent); key dispatch routes by feature+localIdx and never depends on which agent row triggered it, so it is unaffected.
func agentsAllRowsList(m Model) []agentsAllRow {
	var out []agentsAllRow
	skillsIgnore, mcpIgnore, pluginIgnore, marketplaceIgnore := agentsIgnoreSets(m.agentsIgnore)
	agentFilterID := ""
	if agentFilterIDs := skillAgentIDs(m.skillsRows, m.enabledAgents); m.skillAgentIdx > 0 && m.skillAgentIdx <= len(agentFilterIDs) {
		agentFilterID = agentFilterIDs[m.skillAgentIdx-1]
	}

	if m.skillsSectionEnabled() {
		rows, findStart, unmanagedStart := skillsVisibleRows(m)
		seen := make(map[string]bool, len(rows))
		for i, r := range rows {
			seen[r.Name] = true
			switch {
			case unmanagedStart >= 0 && i >= unmanagedStart && skillsIgnore[r.Name]:
				out = append(out, agentsAllRow{feature: agentsSectionSkills, localIdx: i, status: agentsStatusIgnored, mark: agentsMarkNone, sortName: r.Name})
			case unmanagedStart >= 0 && i >= unmanagedStart:
				out = append(out, agentsAllRow{feature: agentsSectionSkills, localIdx: i, status: agentsStatusOutOfSync, mark: agentsMarkOrphan, sortName: r.Name})
			case findStart >= 0 && i >= findStart && skillsIgnore[r.Name]:
				out = append(out, agentsAllRow{feature: agentsSectionSkills, localIdx: i, status: agentsStatusIgnored, mark: agentsMarkNone, sortName: r.Name})
			case findStart >= 0 && i >= findStart:
				out = append(out, agentsAllRow{feature: agentsSectionSkills, localIdx: i, status: agentsStatusAvailable, mark: agentsMarkNone, sortName: r.Name})
			case skillsIgnore[r.Name] && !r.ShadowedByPlugin:
				out = append(out, agentsAllRow{feature: agentsSectionSkills, localIdx: i, status: agentsStatusIgnored, mark: agentsMarkNone, sortName: r.Name})
			default:
				drifted := len(skillDriftedAgents(r, m.enabledAgents)) > 0
				packageShadowed := len(skillShadowedAgents(r, m.enabledAgents)) > 0 &&
					len(skillMissingAgents(r, m.enabledAgents)) == 0
				// A drifted package is still installed and its upgrade real; a missing one has nothing to upgrade.
				outdated := skillRowOutdated(r) && !r.ShadowedByPlugin && !packageShadowed && (r.Installed || drifted)
				status, mark := skillPackageRowStatus(
					r.Installed, r.ShadowedByPlugin, packageShadowed, drifted, outdated)
				out = append(out, agentsAllRow{
					feature: agentsSectionSkills, localIdx: i,
					status: status, mark: mark, sortName: r.Name, outdated: outdated,
				})
			}
		}
		out = append(out, agentsOrphanedIgnoreRows(agentsSectionSkills, skillsIgnore, seen)...)
	}

	if m.mcpSectionEnabled() {
		seen := make(map[string]bool, len(m.mcpRows))
		for i, row := range m.mcpRows {
			seen[row.Name] = true
			if mcpIgnore[row.Name] && !row.ShadowedByPlugin {
				out = append(out, agentsAllRow{feature: agentsSectionMcp, localIdx: i, status: agentsStatusIgnored, mark: agentsMarkNone, sortName: row.Name})
				continue
			}
			for _, agentID := range skillExpandAgents(app.SkillPackageRow{Agents: row.Agents}, m.enabledAgents) {
				if agentFilterID != "" && agentID != agentFilterID {
					continue
				}
				st, ok := row.PerAgentStatus[agentID]
				if !ok || st == app.McpStatusAgentUnavailable {
					continue
				}
				status, mark := mcpAgentRowStatus(st)
				out = append(out, agentsAllRow{feature: agentsSectionMcp, localIdx: i, agentID: agentID, status: status, mark: mark, sortName: row.Name})
			}
		}
		unmanagedFlat := mcpUnmanagedFlat(m.mcpUnmanaged)
		for i, e := range unmanagedFlat {
			seen[e.srv.Name] = true
			if agentFilterID != "" && e.agentID != agentFilterID {
				continue
			}
			status, mark := agentsStatusOutOfSync, agentsMarkOrphan
			if mcpIgnore[e.srv.Name] {
				status, mark = agentsStatusIgnored, agentsMarkNone
			}
			out = append(out, agentsAllRow{
				feature:  agentsSectionMcp,
				localIdx: len(m.mcpRows) + i,
				agentID:  e.agentID,
				status:   status,
				mark:     mark,
				sortName: e.srv.Name,
			})
		}
		out = append(out, agentsOrphanedIgnoreRows(agentsSectionMcp, mcpIgnore, seen)...)
	}

	if m.pluginsSectionEnabled() {
		seen := make(map[string]bool, len(m.pluginRows))
		for i, row := range m.pluginRows {
			seen[row.Name] = true
			if pluginIgnore[row.Name] {
				out = append(out, agentsAllRow{feature: agentsSectionPlugins, localIdx: i, status: agentsStatusIgnored, mark: agentsMarkNone, sortName: row.Name})
				continue
			}
			for _, agentID := range skillExpandAgents(app.SkillPackageRow{Agents: row.Agents}, m.enabledAgents) {
				if agentFilterID != "" && agentID != agentFilterID {
					continue
				}
				st, ok := row.PerAgentStatus[agentID]
				if !ok || st == app.PluginStatusAgentUnavailable {
					continue
				}
				status, mark := pluginAgentRowStatus(row, agentID)
				out = append(out, agentsAllRow{
					feature: agentsSectionPlugins, localIdx: i, agentID: agentID,
					status: status, mark: mark, sortName: row.Name, outdated: row.Outdated(),
				})
			}
		}
		unmanagedFlat := pluginUnmanagedFlat(m.pluginUnmanaged)
		for i, e := range unmanagedFlat {
			seen[e.plugin.Name] = true
			if agentFilterID != "" && e.agentID != agentFilterID {
				continue
			}
			status, mark := agentsStatusOutOfSync, agentsMarkOrphan
			if pluginIgnore[e.plugin.Name] {
				status, mark = agentsStatusIgnored, agentsMarkNone
			}
			out = append(out, agentsAllRow{
				feature:  agentsSectionPlugins,
				localIdx: len(m.pluginRows) + i,
				agentID:  e.agentID,
				status:   status,
				mark:     mark,
				sortName: e.plugin.Name,
			})
		}
		out = append(out, agentsOrphanedIgnoreRows(agentsSectionPlugins, pluginIgnore, seen)...)
	}

	if m.marketplacesSectionEnabled() {
		seen := make(map[string]bool, len(m.marketplaceRows))
		for i, row := range m.marketplaceRows {
			seen[row.Name] = true
			if marketplaceIgnore[row.Name] {
				out = append(out, agentsAllRow{feature: agentsSectionMarketplaces, localIdx: i, status: agentsStatusIgnored, mark: agentsMarkNone, sortName: row.Name})
				continue
			}
			for _, agentID := range skillExpandAgents(app.SkillPackageRow{Agents: row.Agents}, m.enabledAgents) {
				if agentFilterID != "" && agentID != agentFilterID {
					continue
				}
				st, ok := row.PerAgentStatus[agentID]
				if !ok || st == app.PluginStatusAgentUnavailable {
					continue
				}
				status, mark := marketplaceAgentRowStatus(st)
				out = append(out, agentsAllRow{feature: agentsSectionMarketplaces, localIdx: i, agentID: agentID, status: status, mark: mark, sortName: row.Name})
			}
		}
		unmanagedFlat := marketplaceUnmanagedFlat(m.marketplaceUnmanaged)
		for i, e := range unmanagedFlat {
			seen[e.marketplace.Name] = true
			if agentFilterID != "" && e.agentID != agentFilterID {
				continue
			}
			status, mark := agentsStatusOutOfSync, agentsMarkOrphan
			if marketplaceIgnore[e.marketplace.Name] {
				status, mark = agentsStatusIgnored, agentsMarkNone
			}
			out = append(out, agentsAllRow{
				feature:  agentsSectionMarketplaces,
				localIdx: len(m.marketplaceRows) + i,
				agentID:  e.agentID,
				status:   status,
				mark:     mark,
				sortName: e.marketplace.Name,
			})
		}
		out = append(out, agentsOrphanedIgnoreRows(agentsSectionMarketplaces, marketplaceIgnore, seen)...)
	}

	if q := agentsFilterQuery(m); q != "" {
		kept := out[:0]
		for _, e := range out {
			// Skills rows were already matched on source+name by skillsVisibleRows; every other section only has a name.
			if e.feature != agentsSectionSkills && !strings.Contains(strings.ToLower(e.sortName), q) {
				continue
			}
			kept = append(kept, e)
		}
		out = kept
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].status != out[j].status {
			return out[i].status < out[j].status
		}
		if out[i].feature != out[j].feature {
			return out[i].feature < out[j].feature
		}
		if lowerI, lowerJ := strings.ToLower(out[i].sortName), strings.ToLower(out[j].sortName); lowerI != lowerJ {
			return lowerI < lowerJ
		}
		return out[i].agentID < out[j].agentID
	})
	return out
}

// Without this an ignore-list entry matching no live row stays invisible, with no way to unignore it from the UI.
func agentsOrphanedIgnoreRows(feature agentsSection, ignore, seen map[string]bool) []agentsAllRow {
	var out []agentsAllRow
	for name := range ignore {
		if seen[name] {
			continue
		}
		out = append(out, agentsAllRow{feature: feature, localIdx: -1, status: agentsStatusIgnored, mark: agentsMarkNone, sortName: name, synthetic: true})
	}
	return out
}

func agentsAllEntryAt(m Model, cursor int) (agentsAllRow, bool) {
	rows := agentsAllRowsList(m)
	if cursor < 0 || cursor >= len(rows) {
		return agentsAllRow{}, false
	}
	return rows[cursor], true
}

// Same per-agent-row flatten renderAgentsGroupedTab renders, so navigation and rendering always agree on row identity.
func agentsFilteredRowsList(m Model, feature agentsSection) []agentsAllRow {
	var out []agentsAllRow
	for _, e := range agentsAllRowsList(m) {
		if e.feature == feature {
			out = append(out, e)
		}
	}
	return out
}

// Pairs with agentsFeatureCursor's localIdx so a specific rendered row can be pinned when an item expands into multiple per-agent rows.
func agentsFeatureCursorAgentID(m Model, feature agentsSection) string {
	switch feature {
	case agentsSectionMcp:
		return m.mcpCursorAgentID
	case agentsSectionPlugins:
		return m.pluginCursorAgentID
	case agentsSectionMarketplaces:
		return m.marketplaceCursorAgentID
	default:
		return ""
	}
}

// -1 when nothing matches (e.g. skills, which never carries an agentID, or a stale position after the row set changed).
func agentsChipRowPosition(rows []agentsAllRow, localIdx int, agentID string) int {
	for i, e := range rows {
		if e.localIdx == localIdx && e.agentID == agentID {
			return i
		}
	}
	return -1
}

// When the cursor doesn't resolve to a row in the current flatten, lands on row 0 rather than applying delta from an undefined position.
func (m *Model) agentsChipMoveRow(feature agentsSection, delta int) {
	rows := agentsFilteredRowsList(*m, feature)
	n := len(rows)
	if n == 0 {
		return
	}
	pos := agentsChipRowPosition(rows, agentsFeatureCursor(*m, feature), agentsFeatureCursorAgentID(*m, feature))
	if pos < 0 {
		pos = 0
	} else {
		pos = cursorMove(pos, delta, n, true)
	}
	landed := rows[pos]
	switch feature {
	case agentsSectionSkills:
		m.skillsCursor = landed.localIdx
	case agentsSectionMcp:
		m.mcpCursor = landed.localIdx
		m.mcpCursorAgentID = landed.agentID
	case agentsSectionPlugins:
		m.pluginCursor = landed.localIdx
		m.pluginCursorAgentID = landed.agentID
	case agentsSectionMarketplaces:
		m.marketplaceCursor = landed.localIdx
		m.marketplaceCursorAgentID = landed.agentID
	}
}

// ok=false for the "all" chip, which has no single section.
func agentsChipSection(chip int) (agentsSection, bool) {
	switch chip {
	case agentsChipSkills:
		return agentsSectionSkills, true
	case agentsChipMcp:
		return agentsSectionMcp, true
	case agentsChipPlugin:
		return agentsSectionPlugins, true
	case agentsChipMarketplace:
		return agentsSectionMarketplaces, true
	default:
		return agentsSection(0), false
	}
}

// Syncs the destination cursor from the source's so a row stays selected across chip switches instead of resetting.
func (m *Model) setAgentsChip(next int) {
	from := m.skillTypeIdx
	m.skillTypeIdx = next

	if from == agentsChipAll && next != agentsChipAll {
		if toSection, ok := agentsChipSection(next); ok {
			if entry, ok := agentsAllEntryAt(*m, m.agentsAllCursor); ok && entry.feature == toSection {
				switch next {
				case agentsChipSkills:
					m.skillsCursor = entry.localIdx
					if !entry.synthetic {
						clampSkillsCursor(m)
					}
				case agentsChipMcp:
					m.mcpCursor = entry.localIdx
					m.mcpCursorAgentID = entry.agentID
				case agentsChipPlugin:
					m.pluginCursor = entry.localIdx
					m.pluginCursorAgentID = entry.agentID
				case agentsChipMarketplace:
					m.marketplaceCursor = entry.localIdx
					m.marketplaceCursorAgentID = entry.agentID
				}
				return
			}
		}
		m.resetAgentsChipCursor()
		return
	}

	if next == agentsChipAll && from != agentsChipAll {
		if fromSection, ok := agentsChipSection(from); ok {
			fromCursor := agentsFeatureCursor(*m, fromSection)
			fromAgentID := agentsFeatureCursorAgentID(*m, fromSection)
			for i, e := range agentsAllRowsList(*m) {
				if e.feature == fromSection && e.localIdx == fromCursor && e.agentID == fromAgentID {
					m.agentsAllCursor = i
					return
				}
			}
		}
		clampAgentsAllCursor(m)
		return
	}

	m.resetAgentsChipCursor()
}

func (m *Model) resetAgentsChipCursor() {
	switch m.skillTypeIdx {
	case agentsChipAll:
		clampAgentsAllCursor(m)
	case agentsChipSkills:
		clampSkillsCursor(m)
	case agentsChipMcp:
		m.mcpCursor = 0
		m.mcpCursorAgentID = ""
		m.agentsChipMoveRow(agentsSectionMcp, 0)
	case agentsChipPlugin:
		m.pluginCursor = 0
		m.pluginCursorAgentID = ""
		m.agentsChipMoveRow(agentsSectionPlugins, 0)
	case agentsChipMarketplace:
		m.marketplaceCursor = 0
		m.marketplaceCursorAgentID = ""
		m.agentsChipMoveRow(agentsSectionMarketplaces, 0)
	}
}

func agentsAllCursorMove(m *Model, delta int) {
	m.agentsAllCursor = cursorMove(m.agentsAllCursor, delta, len(agentsAllRowsList(*m)), true)
}

func clampAgentsAllCursor(m *Model) {
	total := len(agentsAllRowsList(*m))
	if m.agentsAllCursor >= total {
		m.agentsAllCursor = total - 1
	}
	if m.agentsAllCursor < 0 {
		m.agentsAllCursor = 0
	}
}

const agentsBusyStatus = "⚠ agents busy — wait for the running operation to finish"

func (m *Model) agentsOpInFlight() bool {
	return m.skillsRunning || m.skillAddRunning || m.mcpRunning || m.pluginRunning || m.marketplaceRunning
}

// Runs before per-chip dispatch so the bulk keys work from any chip; guarded against re-trigger while an equivalent op is in flight and against firing mid-confirm.
func (m *Model) handleAgentsGlobalActionKeyMsg(msg tea.KeyPressMsg) (handled bool, cmds []tea.Cmd) {
	if m.agentsDeleteConfirm || m.agentsIgnoreConfirm || m.agentsResolveConfirm || m.mcpDeleteConfirm || m.pluginDeleteConfirm || m.marketplaceDeleteConfirm {
		return false, nil
	}
	if m.agentsSyncAllConfirm {
		m.cancelConfirmationTimeout()
		m.agentsSyncAllConfirm = false
		if msg.String() == "S" && !m.agentsOpInFlight() {
			return true, m.doAgentsSyncAll()
		}
	}
	switch msg.String() {
	case "U", "S", "R":
		if m.agentsOpInFlight() {
			return true, []tea.Cmd{setStatus(m, agentsBusyStatus, true)}
		}
	case "e":
		// Falls through regardless of an in-flight bulk op: the trace log is a read-only view of past output.
	default:
		return false, nil
	}
	switch msg.String() {
	case "U":
		return true, m.doAgentsUpdateAll()
	case "S":
		m.agentsSyncAllConfirm = true
		return true, []tea.Cmd{m.armConfirmationTimeout()}
	case "e":
		return true, []tea.Cmd{m.openTraceLog()}
	default:
		return true, m.doAgentsSyncAll()
	}
}

// Marketplaces must refresh before outdated plugins are computed: LatestVersion/LatestSha come from the marketplace's local clone, so cached rows cannot know about an update until it is pulled.
func (m *Model) doAgentsUpdateAll() []tea.Cmd {
	m.skillsRunning = true
	m.skillsErr = nil
	m.skillsResult = nil
	ch, gen := m.beginProgressStream()
	a, ctx := m.app, m.ctx
	work := func() tea.Msg {
		defer close(ch)
		sendProgress(ch, gen, "updating APM dependencies…")
		result, err := a.AgentsUpdateAll(ctx, false)
		return agentsProgressDoneMsg{
			gen: gen, skills: true, skillsErr: err,
			report: &app.AgentsSyncAllResult{Output: result.Stdout, Stderr: result.Stderr},
		}
	}
	return []tea.Cmd{m.spinner.Tick, work, waitForProgress(ch, gen)}
}

func skillWarningsText(warnings []string) string {
	return strings.Join(warnings, "; ")
}

// The claim step adopts on-disk directories, so the action is confirmed before it runs (see handleAgentsGlobalActionKeyMsg).
func (m *Model) doAgentsSyncAll() []tea.Cmd {
	return m.doAgentsComposedRun(true)
}

func (m *Model) doAgentsRestoreAll() []tea.Cmd {
	return m.doAgentsComposedRun(false)
}

func (m *Model) doAgentsComposedRun(claim bool) []tea.Cmd {
	m.skillsRunning = true
	m.skillsErr = nil
	m.skillsResult = nil
	ch, gen := m.beginProgressStream()
	a, ctx := m.app, m.ctx
	work := func() tea.Msg {
		defer close(ch)
		done := agentsProgressDoneMsg{gen: gen, skills: true}
		report, err := a.AgentsSyncAll(ctx, app.AgentsSyncAllOptions{
			ImportUnmanaged: claim,
			Progress: func(text string) {
				sendProgress(ch, gen, text)
			},
		})
		if err != nil {
			done.skillsErr = err
		} else {
			for _, featureErr := range report.Errors {
				switch featureErr.Feature {
				case app.AgentsFeatureImport, app.AgentsFeatureSkills:
					done.skillsErr = errors.Join(done.skillsErr, featureErr)
				case app.AgentsFeatureMcp:
					done.mcpErr = errors.Join(done.mcpErr, featureErr)
				case app.AgentsFeaturePlugins:
					done.pluginErr = errors.Join(done.pluginErr, featureErr)
				}
			}
		}
		done.report = &report
		return done
	}
	return []tea.Cmd{m.spinner.Tick, work, waitForProgress(ch, gen)}
}

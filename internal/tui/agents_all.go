package tui

import (
	"errors"
	"fmt"
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
	// synthetic marks a row manufactured for an ignore-list entry whose name
	// matches no live row of its feature (e.g. a skill no longer detected as
	// unmanaged, or an mcp server that's been uninstalled). It carries no
	// localIdx into any per-feature row slice — every consumer must check
	// this flag before touching localIdx. Renders name-only and supports
	// exactly one action, unignore ('x').
	synthetic bool
}

func skillExpandAgents(r app.SkillPackageRow, enabledAgents []string) []string {
	if len(r.Agents) > 0 {
		return r.Agents
	}
	return enabledAgents
}

// agentsAllRowsList flattens the skills, mcp, and plugin rows into a single
// cursor-indexable list, expanding each managed item into one row per target
// agent, then sorts by (status, feature, sortName, agentID) so the list
// groups by status like the tools tab while keeping an item's agent rows
// adjacent. The per-feature localIdx spaces are unchanged from the pre-sort
// universe (multiple rows now share a localIdx, one per agent), so key
// dispatch — which routes item-level actions by feature+localIdx and never
// depends on which agent row triggered them — keeps working unmodified.
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
				status, mark := skillPackageRowStatus(r.Installed, r.ShadowedByPlugin)
				out = append(out, agentsAllRow{feature: agentsSectionSkills, localIdx: i, status: status, mark: mark, sortName: r.Name})
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
				out = append(out, agentsAllRow{feature: agentsSectionPlugins, localIdx: i, agentID: agentID, status: status, mark: mark, sortName: row.Name})
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

// agentsOrphanedIgnoreRows manufactures a synthetic Ignored row for every name
// in ignore that seen doesn't already cover, so an ignore-list entry whose
// name matches no live row (e.g. a skill no longer detected as unmanaged, or
// an uninstalled mcp server) still surfaces in the Ignored section instead of
// being silently invisible with no way to unignore it from the UI.
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

// agentsAllEntryAt returns the full row entry at cursor, or ok=false if
// cursor is out of range.
func agentsAllEntryAt(m Model, cursor int) (agentsAllRow, bool) {
	rows := agentsAllRowsList(m)
	if cursor < 0 || cursor >= len(rows) {
		return agentsAllRow{}, false
	}
	return rows[cursor], true
}

func agentsChipEnabled(m Model, chip int) bool {
	switch chip {
	case agentsChipSkills:
		return m.skillsSectionEnabled()
	case agentsChipMcp:
		return m.mcpSectionEnabled()
	case agentsChipPlugin:
		return m.pluginsSectionEnabled()
	case agentsChipMarketplace:
		return m.marketplacesSectionEnabled()
	default:
		return true
	}
}

func (m *Model) agentsChipMove(delta int) {
	for next := m.skillTypeIdx + delta; next >= agentsChipAll && next <= agentsChipMarketplace; next += delta {
		if agentsChipEnabled(*m, next) {
			m.setAgentsChip(next)
			return
		}
	}
}

// agentsFilteredRowsList returns agentsAllRowsList filtered to feature,
// the same per-agent-row flatten renderAgentsGroupedTab renders for a
// non-"all" chip, so navigation and rendering always agree on row identity.
func agentsFilteredRowsList(m Model, feature agentsSection) []agentsAllRow {
	var out []agentsAllRow
	for _, e := range agentsAllRowsList(m) {
		if e.feature == feature {
			out = append(out, e)
		}
	}
	return out
}

// agentsFeatureCursorAgentID returns the agentID disambiguator paired with
// agentsFeatureCursor's localIdx, so a specific rendered row (not just its
// item) can be pinned when an item expands into multiple per-agent rows.
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

// agentsChipRowPosition returns the index within agentsFilteredRowsList(m,
// feature) of the row matching (localIdx, agentID), or -1 if none matches
// (e.g. skills, which never carries an agentID, or a stale position after
// the row set changed).
func agentsChipRowPosition(rows []agentsAllRow, localIdx int, agentID string) int {
	for i, e := range rows {
		if e.localIdx == localIdx && e.agentID == agentID {
			return i
		}
	}
	return -1
}

// agentsChipMoveRow moves the chip's cursor by delta positions within the
// feature's filtered per-agent-row flatten, then writes the landed row's
// (localIdx, agentID) back to the feature's cursor fields. When the current
// cursor doesn't resolve to a row in the current flatten (e.g. after a
// reload, or an explicit reset-to-first-row call with delta=0), lands
// directly on row 0 instead of applying delta relative to an undefined
// position.
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

// agentsChipSection maps a chip constant to its agentsSection, or ok=false
// for the "all" chip (which has no single section).
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

// setAgentsChip switches the active chip and syncs the destination's cursor
// from the source's, so a row stays selected across chip switches instead of
// resetting. Shared by the keyboard ([/]) and mouse (pill click) entry points.
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

// agentFilterMove cycles skillAgentIdx (the agent-filter chip bar) by delta,
// clamped to [0, len(agentIDs)] same as the skills handler's prior [/] logic.
func (m *Model) agentFilterMove(delta int) {
	if delta < 0 {
		if m.skillAgentIdx > 0 {
			m.skillAgentIdx--
			clampSkillsCursor(m)
		}
		return
	}
	agentIDs := skillAgentIDs(m.skillsRows, m.enabledAgents)
	if m.skillAgentIdx < len(agentIDs) {
		m.skillAgentIdx++
		clampSkillsCursor(m)
	}
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

// agentsAllCursorMove moves m.agentsAllCursor by delta positions, wrapped
// modulo the flattened all-view row list (matching the tools/dots main-tab
// lists). Shared by keyboard up/down and mouse wheel scroll.
func agentsAllCursorMove(m *Model, delta int) {
	m.agentsAllCursor = cursorMove(m.agentsAllCursor, delta, len(agentsAllRowsList(*m)), true)
}

// clampAgentsAllCursor keeps m.agentsAllCursor within [0, total) for the
// flattened all-view row list.
func clampAgentsAllCursor(m *Model) {
	total := len(agentsAllRowsList(*m))
	if m.agentsAllCursor >= total {
		m.agentsAllCursor = total - 1
	}
	if m.agentsAllCursor < 0 {
		m.agentsAllCursor = 0
	}
}

// handleAgentsGlobalActionKeyMsg intercepts the agents tab's three
// capital-key global bulk actions (U update all, S sync all, R refresh)
// before per-chip dispatch, so they work identically from any chip
// (all/skills/mcp/plugin), mirroring how tools' UpgradeAll/SyncAll/Refresh
// work regardless of the active provider/group filter. Guarded against
// re-trigger while an equivalent operation is already in flight, and against
// firing during any row-level confirm (delete) so a stray capital letter
// can't be misread mid-confirmation.
func (m *Model) handleAgentsGlobalActionKeyMsg(msg tea.KeyPressMsg) (handled bool, cmds []tea.Cmd) {
	if m.agentsDeleteConfirm || m.agentsIgnoreConfirm || m.mcpDeleteConfirm || m.pluginDeleteConfirm || m.marketplaceDeleteConfirm {
		return false, nil
	}
	agentsGlobalOpInFlight := m.skillsRunning || m.skillAddRunning || m.mcpRunning || m.pluginRunning || m.marketplaceRunning
	switch msg.String() {
	case "U", "S", "R":
		if agentsGlobalOpInFlight {
			return true, nil
		}
	case "e":
		// Falls through to the open-trace-log case below regardless of an
		// in-flight bulk op: the log itself is a read-only view of past
		// command output, unrelated to whatever bulk action is running.
	default:
		return false, nil
	}
	switch msg.String() {
	case "U":
		return true, m.doAgentsUpdateAll()
	case "S":
		return true, m.doAgentsSyncAll()
	case "e":
		return true, []tea.Cmd{m.openTraceLog()}
	default:
		return true, m.doAgentsRefreshAll()
	}
}

// doAgentsUpdateAll runs the agents tab's "U" global bulk action: skills
// update, a marketplace refresh, an update for every outdated plugin, then an
// install for every manifest plugin/mcp server still missing (so "U" also
// picks up entries added to the manifest on another host, mirroring what "S"
// does for those two sections — mcp has no update concept at all, only
// add/remove, see McpAdapter, so install-missing is the only thing "U" can do
// for it). Marketplaces must refresh BEFORE outdated plugins are computed: a
// plugin's LatestVersion/LatestSha comes from the marketplace's local clone,
// so the cached rows can't know about an update until the clone is pulled —
// computing outdated from the cached rows first would never find anything to
// update. The plugin update and the missing-plugin install both use their
// PreRefreshed variant so that one refresh isn't repeated for each.
func (m *Model) doAgentsUpdateAll() []tea.Cmd {
	runSkills := m.skillsSectionEnabled() && len(m.skillsRows) > 0
	runPlugins := m.pluginsSectionEnabled()
	runMarketplaces := m.marketplacesSectionEnabled()
	runMcp := m.mcpSectionEnabled()
	if !runSkills && !runPlugins && !runMarketplaces && !runMcp {
		return nil
	}
	if runSkills {
		m.skillsRunning = true
		m.skillsErr = nil
	}
	if runPlugins {
		m.pluginRunning = true
		m.pluginErr = nil
	}
	if runMarketplaces {
		m.marketplaceRunning = true
		m.marketplaceErr = nil
	}
	if runMcp {
		m.mcpRunning = true
		m.mcpErr = nil
	}
	ch, gen := m.beginProgressStream()
	a, ctx := m.app, m.ctx
	work := func() tea.Msg {
		defer close(ch)
		done := agentsProgressDoneMsg{gen: gen, skills: runSkills, mcp: runMcp, plugin: runPlugins, marketplace: runMarketplaces}
		if runSkills {
			sendProgress(ch, gen, "updating skills…")
			_, _, err := a.UpdateSkills(ctx, app.UpdateSkillsOptions{})
			done.skillsErr = err
		}
		if runPlugins || runMarketplaces {
			sendProgress(ch, gen, "updating marketplaces…")
			res, err := a.UpdateMarketplaces(ctx)
			mErr := combinePluginErrors(err, res.Errors)
			if runMarketplaces {
				done.marketplaceErr = mErr
			} else {
				done.pluginErr = mErr
			}
		}
		if runPlugins && done.pluginErr == nil {
			rows, _, err := a.PluginRows(ctx)
			if err != nil {
				done.pluginErr = err
			} else {
				var outdated []string
				for _, row := range rows {
					if row.Outdated() {
						outdated = append(outdated, row.Name)
					}
				}
				if len(outdated) > 0 {
					res, err := a.UpdatePluginsPreRefreshed(ctx, outdated, func(name string) {
						sendProgress(ch, gen, "updating plugin "+name+"…")
					})
					done.pluginErr = combinePluginErrors(err, res.Errors)
				}
			}
		}
		if runPlugins && done.pluginErr == nil {
			sendProgress(ch, gen, "installing missing plugins…")
			res, err := a.RestorePluginsPreRefreshed(ctx, app.RestorePluginOptions{})
			done.pluginErr = combinePluginErrors(err, res.Errors)
		}
		if runMcp {
			sendProgress(ch, gen, "installing missing mcp servers…")
			res, err := a.RestoreMcpServers(ctx, app.RestoreMcpOptions{})
			done.mcpErr = combineMcpErrors(err, res.Errors)
		}
		return done
	}
	return []tea.Cmd{m.spinner.Tick, work, waitForProgress(ch, gen)}
}

// combineSkillErrors folds per-package install failures into the returned
// error, mirroring combineMcpErrors/combinePluginErrors: RestoreSkills only
// reports them via res.Failed, so a nil err alone does not mean every
// package installed.
func combineSkillErrors(err error, res app.RestoreSkillsResult) error {
	all := make([]error, 0, len(res.Failed)+1)
	if err != nil {
		all = append(all, err)
	}
	for _, f := range res.Failed {
		all = append(all, fmt.Errorf("%s: %s", f.Name, f.Message))
	}
	return errors.Join(all...)
}

// doAgentsSyncAll runs the agents tab's "S" global bulk action: restore
// skills, mcp, and plugins from their manifests, mirroring tools' SyncAll
// (install missing / add discovered).
func (m *Model) doAgentsSyncAll() []tea.Cmd {
	runSkills := m.skillsSectionEnabled()
	runMcp := m.mcpSectionEnabled()
	runPlugins := m.pluginsSectionEnabled()
	if !runSkills && !runMcp && !runPlugins {
		return nil
	}
	if runSkills {
		m.skillsRunning = true
		m.skillsErr = nil
		m.skillsResult = nil
	}
	if runMcp {
		m.mcpRunning = true
		m.mcpErr = nil
	}
	if runPlugins {
		m.pluginRunning = true
		m.pluginErr = nil
	}
	ch, gen := m.beginProgressStream()
	a, ctx := m.app, m.ctx
	work := func() tea.Msg {
		defer close(ch)
		done := agentsProgressDoneMsg{gen: gen, skills: runSkills, mcp: runMcp, plugin: runPlugins}
		if runSkills {
			sendProgress(ch, gen, "restoring skills…")
			res, _, err := a.RestoreSkills(ctx, app.RestoreSkillsOptions{})
			done.skillsErr = combineSkillErrors(err, res)
		}
		if runMcp {
			sendProgress(ch, gen, "restoring mcp servers…")
			res, err := a.RestoreMcpServers(ctx, app.RestoreMcpOptions{})
			done.mcpErr = combineMcpErrors(err, res.Errors)
		}
		if runPlugins {
			sendProgress(ch, gen, "restoring plugins…")
			res, err := a.RestorePlugins(ctx, app.RestorePluginOptions{})
			done.pluginErr = combinePluginErrors(err, res.Errors)
		}
		return done
	}
	return []tea.Cmd{m.spinner.Tick, work, waitForProgress(ch, gen)}
}

// doAgentsRefreshAll runs the agents tab's "R" global bulk action: reload
// all three row sets plus the dashboard agents summary, mirroring tools'
// Refresh (rescan installed/outdated state).
func (m *Model) doAgentsRefreshAll() []tea.Cmd {
	var cmds []tea.Cmd
	if m.skillsSectionEnabled() {
		m.skillsLoaded = true
		cmds = append(cmds, m.loadSkillsManifestCmd())
	}
	if m.mcpSectionEnabled() {
		m.mcpRunning = true
		cmds = append(cmds, m.spinner.Tick, m.doLoadMcpRows())
	}
	if m.pluginsSectionEnabled() {
		m.pluginRunning = true
		cmds = append(cmds, m.spinner.Tick, m.doLoadPluginRows())
	}
	if m.marketplacesSectionEnabled() {
		m.marketplaceRunning = true
		cmds = append(cmds, m.spinner.Tick, m.doLoadMarketplaceRows())
	}
	cmds = append(cmds, m.doLoadAgentsSummary())
	return cmds
}

// handleAgentsAllKeyMsg dispatches keys for the all chip: up/down move the
// single cursor across the flattened row list, left/right switch chips, and
// any other key is routed to the section under the cursor's own key handler
// (with that section's cursor synced first) so mutating actions (e.g. mcp
// "n", plugin "d") work uniformly across the stacked view.
func (m *Model) handleAgentsAllKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	if m.agentsDeleteConfirm {
		return m.handleAgentsDeleteConfirmKeyMsg(msg)
	}
	if m.agentsIgnoreConfirm {
		return m.handleAgentsIgnoreConfirmKeyMsg(msg)
	}
	if m.pluginMarketplaceOfferConfirm {
		return m.handlePluginMarketplaceOfferConfirmKeyMsg(msg)
	}
	switch msg.String() {
	case "up", "k":
		agentsAllCursorMove(m, -1)
		return nil
	case "down", "j":
		agentsAllCursorMove(m, 1)
		return nil
	case "left", "h":
		m.agentsChipMove(-1)
		return nil
	case "right", "l":
		m.agentsChipMove(1)
		return nil
	}
	if handled, cmds := m.handleAgentsAllRowActionKeyMsg(msg); handled {
		clampAgentsAllCursor(m)
		return cmds
	}

	entry, ok := agentsAllEntryAt(*m, m.agentsAllCursor)
	if !ok {
		return nil
	}
	var cmds []tea.Cmd
	switch entry.feature {
	case agentsSectionSkills:
		m.skillsCursor = entry.localIdx
		cmds = m.handleSkillsKeyMsg(msg)
	case agentsSectionMcp:
		m.mcpCursor = entry.localIdx
		m.mcpCursorAgentID = entry.agentID
		cmds = m.handleMcpKeyMsg(msg)
	case agentsSectionPlugins:
		m.pluginCursor = entry.localIdx
		m.pluginCursorAgentID = entry.agentID
		cmds = m.handlePluginKeyMsg(msg)
	case agentsSectionMarketplaces:
		m.marketplaceCursor = entry.localIdx
		m.marketplaceCursorAgentID = entry.agentID
		cmds = m.handleMarketplaceKeyMsg(msg)
	}
	// load-bearing: delegated handlers ([/] filter, removals) clamp only their section cursor
	clampAgentsAllCursor(m)
	return cmds
}

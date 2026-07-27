package tui

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/app"
)

const agentsAgentColW = 8

// Widest well-known agent ID, so cols.prov starts wide enough to keep column x-offsets roughly comparable to the tools tab.
const agentsAgentIDColFloor = len("claude-code")

// Never shrunk by fitToolColumnsToScreen: reserved on every row like this file's other always-present columns.
const agentsTypeColW = 7

func agentsFeatureLabel(feature agentsSection) string {
	switch feature {
	case agentsSectionSkills:
		return "skills"
	case agentsSectionMcp:
		return "mcp"
	case agentsSectionPlugins:
		return "plugin"
	case agentsSectionMarketplaces:
		return "market"
	default:
		return "plugin"
	}
}

// Flexes the name column via fitToolColumnsToScreen in the tools tab's shrink order; agentsCols and colWidths share a field shape once priv is 0.
func agentsColWidths(m Model, rows []agentsAllRow) colWidths {
	seed := colWidths{name: 20, typ: agentsTypeColW, prov: max(agentsAgentColW, agentsAgentIDColFloor), ver: len("missing"), screenW: m.width}
	measure := rowColWidthMeasure{
		name: func(i int) string { return agentsRowName(m, rows[i]) },
		prov: func(i int) string { return agentsProvCellText(m, rows[i]) },
		ver:  func(i int) string { return agentsVersionCellText(m, rows[i]) },
		group: func(i int) string {
			return agentsGroupBadge(m.palette, agentsRowGroups(m, rows[i]), m.hostInfo, 0, nil)
		},
	}
	return seedWidenCapShrinkColWidths(seed, len(rows), measure)
}

func agentsRowName(m Model, e agentsAllRow) string {
	if e.synthetic {
		return e.sortName
	}
	switch e.feature {
	case agentsSectionSkills:
		rows, _, _ := skillsVisibleRows(m)
		if e.localIdx >= 0 && e.localIdx < len(rows) {
			return rows[e.localIdx].Name
		}
	case agentsSectionMcp:
		if e.localIdx < len(m.mcpRows) {
			return m.mcpRows[e.localIdx].Name
		}
		if flat := mcpUnmanagedFlat(m.mcpUnmanaged); e.localIdx-len(m.mcpRows) < len(flat) {
			return flat[e.localIdx-len(m.mcpRows)].srv.Name
		}
	case agentsSectionPlugins:
		if e.localIdx < len(m.pluginRows) {
			return m.pluginRows[e.localIdx].Name
		}
		if flat := pluginUnmanagedFlat(m.pluginUnmanaged); e.localIdx-len(m.pluginRows) < len(flat) {
			return flat[e.localIdx-len(m.pluginRows)].plugin.Name
		}
	case agentsSectionMarketplaces:
		if e.localIdx < len(m.marketplaceRows) {
			return m.marketplaceRows[e.localIdx].Name
		}
		if flat := marketplaceUnmanagedFlat(m.marketplaceUnmanaged); e.localIdx-len(m.marketplaceRows) < len(flat) {
			return flat[e.localIdx-len(m.marketplaceRows)].marketplace.Name
		}
	}
	return ""
}

// A drifted row names the drifted agents, not the linked ones: a drifted package stays linked elsewhere, so linked would name every agent except the one the row acts against.
func agentsProvCellText(m Model, e agentsAllRow) string {
	if e.synthetic {
		return ""
	}
	if e.feature != agentsSectionSkills {
		return e.agentID
	}
	rows, findStart, unmanagedStart := skillsVisibleRows(m)
	if e.localIdx < 0 || e.localIdx >= len(rows) {
		return ""
	}
	if agentsSkillRowIsFindResult(e.localIdx, findStart, unmanagedStart) {
		return rows[e.localIdx].Source
	}
	if e.status == agentsStatusIgnored {
		return ""
	}
	if e.mark == agentsMarkDrifted {
		return skillAgentsCellText(skillDriftedAgents(rows[e.localIdx], m.enabledAgents))
	}
	if e.mark == agentsMarkPackageShadowed {
		return skillAgentsCellText(skillShadowedAgents(rows[e.localIdx], m.enabledAgents))
	}
	return skillLinkageSummary(rows[e.localIdx], m.enabledAgents)
}

// The catalog find-results block sits between the manifest rows and the unmanaged lockfile rows in skillsVisibleRows' output.
func agentsSkillRowIsFindResult(idx, findStart, unmanagedStart int) bool {
	if findStart < 0 || idx < findStart {
		return false
	}
	return unmanagedStart < 0 || idx < unmanagedStart
}

func agentsVersionCellText(m Model, e agentsAllRow) string {
	if e.synthetic {
		return ""
	}
	if e.mark == agentsMarkShadowed {
		return "via plugin"
	}
	if e.mark == agentsMarkPackageShadowed {
		return "via package"
	}
	if e.mark == agentsMarkDrifted {
		return agentsDriftedCellText(e.outdated)
	}
	switch e.feature {
	case agentsSectionSkills:
		rows, findStart, unmanagedStart := skillsVisibleRows(m)
		if e.localIdx < 0 || e.localIdx >= len(rows) {
			return ""
		}
		if agentsSkillRowIsFindResult(e.localIdx, findStart, unmanagedStart) {
			return rows[e.localIdx].Ref
		}
		return rows[e.localIdx].Updated
	case agentsSectionMcp:
		row, ok := agentsMcpRowAt(m, e.localIdx)
		if !ok {
			return ""
		}
		return row.Version
	case agentsSectionPlugins:
		row, ok := agentsPluginRowAt(m, e.localIdx)
		if !ok {
			return ""
		}
		if row.Version != "" && row.LatestVersion != "" && row.Version != row.LatestVersion {
			return compactVersion(row.Version) + " → " + compactVersion(row.LatestVersion)
		}
		if row.Sha != "" && row.LatestSha != "" && row.Sha != row.LatestSha {
			return shaShort(row.Sha) + " → " + shaShort(row.LatestSha)
		}
		// PathOutdated is a git-history comparison with no before/after pair to render as an arrow, so fall back to a plain label.
		if row.Outdated() {
			return "upgrade available"
		}
		return row.Version
	case agentsSectionMarketplaces:
		row, ok := agentsMarketplaceRowAt(m, e.localIdx)
		if !ok {
			return ""
		}
		return marketplaceUpdatedAtText(row.UpdatedAt)
	default:
		return ""
	}
}

// Drift owns the row's section, so without this the pending upgrade stays invisible until the contested entry is settled.
func agentsDriftedCellText(outdated bool) string {
	if outdated {
		return "drifted · upgrade"
	}
	return "drifted"
}

// Blank when t is the zero value rather than a misleading placeholder date.
func marketplaceUpdatedAtText(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

func agentsMarketplaceRowAt(m Model, localIdx int) (app.MarketplaceRow, bool) {
	if localIdx >= 0 && localIdx < len(m.marketplaceRows) {
		return m.marketplaceRows[localIdx], true
	}
	flat := marketplaceUnmanagedFlat(m.marketplaceUnmanaged)
	idx := localIdx - len(m.marketplaceRows)
	if idx < 0 || idx >= len(flat) {
		return app.MarketplaceRow{}, false
	}
	mk := flat[idx].marketplace
	return app.MarketplaceRow{Name: mk.Name, UpdatedAt: mk.UpdatedAt}, true
}

// Only the fields callers need (name display, version cell) are populated.
func agentsMcpRowAt(m Model, localIdx int) (app.McpServerRow, bool) {
	if localIdx >= 0 && localIdx < len(m.mcpRows) {
		return m.mcpRows[localIdx], true
	}
	flat := mcpUnmanagedFlat(m.mcpUnmanaged)
	idx := localIdx - len(m.mcpRows)
	if idx < 0 || idx >= len(flat) {
		return app.McpServerRow{}, false
	}
	srv := flat[idx].srv
	return app.McpServerRow{Name: srv.Name, Version: srv.Version}, true
}

func agentsPluginRowAt(m Model, localIdx int) (app.PluginRow, bool) {
	if localIdx >= 0 && localIdx < len(m.pluginRows) {
		return m.pluginRows[localIdx], true
	}
	flat := pluginUnmanagedFlat(m.pluginUnmanaged)
	idx := localIdx - len(m.pluginRows)
	if idx < 0 || idx >= len(flat) {
		return app.PluginRow{}, false
	}
	p := flat[idx].plugin
	return app.PluginRow{Name: p.Name, Version: p.Version, LatestVersion: p.LatestVersion, Sha: p.Sha, LatestSha: p.LatestSha, PathOutdated: p.PathOutdated}, true
}

func agentsRowGroups(m Model, e agentsAllRow) []string {
	if e.synthetic {
		return nil
	}
	switch e.feature {
	case agentsSectionSkills:
		rows, _, _ := skillsVisibleRows(m)
		if e.localIdx >= 0 && e.localIdx < len(rows) {
			return rows[e.localIdx].Groups
		}
	case agentsSectionMcp:
		if e.localIdx < len(m.mcpRows) {
			return m.mcpRows[e.localIdx].Groups
		}
	case agentsSectionPlugins:
		if e.localIdx < len(m.pluginRows) {
			return m.pluginRows[e.localIdx].Groups
		}
	case agentsSectionMarketplaces:
		if e.localIdx < len(m.marketplaceRows) {
			return m.marketplaceRows[e.localIdx].Groups
		}
	}
	return nil
}

// Agent IDs are an unbounded, extensible set, so they cycle existing hue styles instead of mapping onto tools' fixed provider taxonomy.
func agentsHuePalette(p palette) []lipgloss.Style {
	return []lipgloss.Style{p.styleProvider, p.styleProviderLinux, p.styleProviderSystem, p.styleOrphan}
}

// Only a literal single agent ID gets a hue; linkage summaries ("2 agents") and placeholders keep the flat styleHelp.
func styleForAgent(p palette, agentID string) lipgloss.Style {
	if agentID == "" {
		return p.styleHelp
	}
	var h uint32
	for i := 0; i < len(agentID); i++ {
		h = h*31 + uint32(agentID[i])
	}
	hues := agentsHuePalette(p)
	return hues[h%uint32(len(hues))]
}

func agentsGroupBadge(p palette, groups []string, info *app.HostInfo, colW int, emphasis func(lipgloss.Style) lipgloss.Style) string {
	return renderGroupPills(p, groups, info, colW, emphasis)
}

func agentsRowRunKey(e agentsAllRow) string {
	return strconv.Itoa(int(e.feature)) + "\x00" + strconv.Itoa(e.localIdx) + "\x00" + e.agentID
}

// Reuses renderToolRow's glyphs and styles for the equivalent syncStatus so agents rows read consistently with the tools tab.
func agentsMarkCell(p palette, status agentsRowStatus, mark agentsSyncMark, selected bool) string {
	emphasis := func(s lipgloss.Style) lipgloss.Style {
		return rowEmphasis(selected, s)
	}
	if status == agentsStatusIgnored {
		return emphasis(p.styleIgnored).Render(iconIgnored)
	}
	switch {
	case mark == agentsMarkOrphan:
		return emphasis(p.styleOrphan).Render(iconOrphan)
	case status == agentsStatusUpdates:
		return emphasis(p.styleOutdated).Render(iconOutdated)
	case mark == agentsMarkDrifted:
		return emphasis(p.styleMissing).Render(iconWrongProv)
	case mark == agentsMarkMissing:
		return emphasis(p.styleMissing).Render(iconMissing)
	case mark == agentsMarkShadowed:
		return emphasis(p.styleWrongProv).Render(iconWrongProv)
	case mark == agentsMarkPackageShadowed:
		return emphasis(p.styleWrongProv).Render(iconWrongProv)
	default:
		return emphasis(p.styleInstalled).Render(iconInstalled)
	}
}

func agentsRowCells(m Model, p palette, cols colWidths, e agentsAllRow, selected bool) (left, right []rowCell) {
	emphasis := func(s lipgloss.Style) lipgloss.Style {
		return rowEmphasis(selected, s)
	}
	groupEmphasis := func(s lipgloss.Style) lipgloss.Style {
		if e.status == agentsStatusIgnored {
			return emphasis(p.styleIgnored)
		}
		return emphasis(s)
	}
	iconGap := " "
	mark := agentsMarkCell(p, e.status, e.mark, selected)
	if m.agentsOpKey != "" && m.agentsOpKey == agentsRowRunKey(e) {
		mark = rowSpinnerIcon(m)
	}

	nameStyle := p.styleNormal
	if e.status == agentsStatusIgnored {
		nameStyle = p.styleIgnored
	}
	if selected {
		nameStyle = nameStyle.Bold(true)
	}

	if e.synthetic {
		nameCell := nameStyle.Render(fitCellText(e.sortName, cols.name))
		left = []rowCell{leftCell(mark+iconGap+nameCell, 0)}
		right = []rowCell{
			leftCell(p.styleIgnored.Render(fitCellText(agentsFeatureLabel(e.feature), cols.typ)), cols.typ),
			leftCell("", cols.prov),
			rightCell("", cols.ver),
		}
		if cols.group != 0 {
			right = append(right, rightCell("", cols.group))
		}
		return left, right
	}

	skillsLinkage := ""

	var nameCell, groupBadge, ver string
	switch e.feature {
	case agentsSectionSkills:
		rows, findStart, unmanagedStart := skillsVisibleRows(m)
		if e.localIdx >= 0 && e.localIdx < len(rows) {
			r := rows[e.localIdx]
			nameCell = nameStyle.Render(fitCellText(r.Name, cols.name))
			groupBadge = agentsGroupBadge(p, r.Groups, m.hostInfo, cols.group, groupEmphasis)
			skillsLinkage = agentsProvCellText(m, e)
			switch {
			case agentsSkillRowIsFindResult(e.localIdx, findStart, unmanagedStart):
				// Catalog hits have no local install state; owner and install count are the only columns worth showing.
				ver = emphasis(p.styleVersionMuted).Render(fitCellText(r.Ref, cols.ver))
			case e.mark == agentsMarkDrifted:
				ver = emphasis(p.styleMissing).Render(fitCellText(agentsDriftedCellText(e.outdated), cols.ver))
			case e.mark == agentsMarkMissing:
				ver = emphasis(p.styleMissing).Render("missing")
			case e.mark == agentsMarkShadowed:
				ver = emphasis(p.styleWrongProv).Render(fitCellText("via plugin", cols.ver))
			case e.mark == agentsMarkPackageShadowed:
				ver = emphasis(p.styleWrongProv).Render(fitCellText("via package", cols.ver))
			case e.status != agentsStatusIgnored:
				verStyle := p.styleVersionMuted
				ver = emphasis(verStyle).Render(fitCellText(r.Updated, cols.ver))
			}
		}
	case agentsSectionMcp:
		mcpVersion := ""
		if e.localIdx < len(m.mcpRows) {
			r := m.mcpRows[e.localIdx]
			nameCell = nameStyle.Render(fitCellText(r.Name, cols.name))
			groupBadge = agentsGroupBadge(p, r.Groups, m.hostInfo, cols.group, groupEmphasis)
			mcpVersion = r.Version
		} else if flat := mcpUnmanagedFlat(m.mcpUnmanaged); e.localIdx-len(m.mcpRows) < len(flat) {
			entry := flat[e.localIdx-len(m.mcpRows)]
			nameCell = nameStyle.Render(fitCellText(entry.srv.Name, cols.name))
			mcpVersion = entry.srv.Version
		}
		switch {
		case e.mark == agentsMarkMissing:
			ver = emphasis(p.styleMissing).Render("missing")
		case e.mark == agentsMarkDrifted:
			ver = emphasis(p.styleMissing).Render(fitCellText(agentsDriftedCellText(e.outdated), cols.ver))
		case e.mark == agentsMarkShadowed:
			ver = emphasis(p.styleWrongProv).Render(fitCellText("via plugin", cols.ver))
		case e.status == agentsStatusIgnored:
			ver = emphasis(p.styleIgnored).Render(fitCellText(mcpVersion, cols.ver))
		case mcpVersion != "":
			ver = emphasis(p.styleVersionMuted).Render(fitCellText(mcpVersion, cols.ver))
		}
	case agentsSectionPlugins:
		if e.localIdx < len(m.pluginRows) {
			r := m.pluginRows[e.localIdx]
			nameCell = nameStyle.Render(fitCellText(r.Name, cols.name))
			groupBadge = agentsGroupBadge(p, r.Groups, m.hostInfo, cols.group, groupEmphasis)
			switch {
			case e.mark == agentsMarkMissing:
				ver = emphasis(p.styleMissing).Render("missing")
			case e.mark == agentsMarkDrifted:
				ver = emphasis(p.styleMissing).Render(fitCellText(agentsDriftedCellText(e.outdated), cols.ver))
			case e.status == agentsStatusIgnored:
				ver = emphasis(p.styleIgnored).Render(fitCellText(r.Version, cols.ver))
			default:
				ver = renderPluginUpdateCell(p, emphasis, r.Update(), r.Version, cols.ver)
			}
		} else if flat := pluginUnmanagedFlat(m.pluginUnmanaged); e.localIdx-len(m.pluginRows) < len(flat) {
			entry := flat[e.localIdx-len(m.pluginRows)]
			nameCell = nameStyle.Render(fitCellText(entry.plugin.Name, cols.name))
			pl := entry.plugin
			switch {
			case e.status == agentsStatusIgnored:
				ver = emphasis(p.styleIgnored).Render(fitCellText(pl.Version, cols.ver))
			default:
				ver = renderPluginUpdateCell(p, emphasis, app.InstalledPluginUpdate(pl), pl.Version, cols.ver)
			}
		}
		if e.mark == agentsMarkMissing && ver == "" {
			ver = emphasis(p.styleMissing).Render("missing")
		}
	case agentsSectionMarketplaces:
		if e.localIdx < len(m.marketplaceRows) {
			r := m.marketplaceRows[e.localIdx]
			nameCell = nameStyle.Render(fitCellText(r.Name, cols.name))
			groupBadge = agentsGroupBadge(p, r.Groups, m.hostInfo, cols.group, groupEmphasis)
			if e.status != agentsStatusIgnored {
				ver = emphasis(p.styleVersionMuted).Render(fitCellText(marketplaceUpdatedAtText(r.UpdatedAt), cols.ver))
			}
		} else if flat := marketplaceUnmanagedFlat(m.marketplaceUnmanaged); e.localIdx-len(m.marketplaceRows) < len(flat) {
			entry := flat[e.localIdx-len(m.marketplaceRows)]
			nameCell = nameStyle.Render(fitCellText(entry.marketplace.Name, cols.name))
			ver = emphasis(p.styleVersionMuted).Render(fitCellText(marketplaceUpdatedAtText(entry.marketplace.UpdatedAt), cols.ver))
		}
		if e.mark == agentsMarkMissing && ver == "" {
			ver = emphasis(p.styleMissing).Render("missing")
		}
	}

	agentLabel := ""
	switch {
	case e.feature == agentsSectionSkills:
		agentLabel = skillsLinkage
	default:
		agentLabel = "-"
	}
	if e.agentID != "" {
		agentLabel = e.agentID
	}
	agentStyle := emphasis(styleForAgent(p, e.agentID))
	if e.status == agentsStatusIgnored {
		agentStyle = emphasis(p.styleIgnored)
	}

	typeStyle := p.styleHelp
	if e.status == agentsStatusIgnored {
		typeStyle = p.styleIgnored
	}

	left = []rowCell{leftCell(mark+iconGap+nameCell, 0)}
	right = []rowCell{
		leftCell(typeStyle.Render(fitCellText(agentsFeatureLabel(e.feature), cols.typ)), cols.typ),
		leftCell(agentStyle.Render(fitCellText(agentLabel, cols.prov)), cols.prov),
		rightCell(ver, cols.ver),
	}
	if cols.group != 0 {
		right = append(right, rightCell(groupBadge, cols.group))
	}
	return left, right
}

// Managed and unmanaged plugin paths share this render so neither re-decides "outdated" independently of app.PluginRow.Outdated.
func renderPluginUpdateCell(p palette, emphasis func(lipgloss.Style) lipgloss.Style, u app.PluginUpdate, fallbackVersion string, width int) string {
	switch u.Kind {
	case app.PluginVersionUpgrade:
		current, latest := fitUpgradeVersionText(compactVersion(u.Current), compactVersion(u.Latest), width)
		return emphasis(p.styleMissing).Render(current) + emphasis(p.styleOutdated).Render(latest)
	case app.PluginShaDrift:
		return emphasis(p.styleMissing).Render(fitCellText(shaShort(u.Current), width/2)) + emphasis(p.styleOutdated).Render(" → "+fitCellText(shaShort(u.Latest), width/2))
	case app.PluginUpdateAvailable:
		return emphasis(p.styleOutdated).Render(fitCellText("upgrade available", width))
	default:
		return emphasis(p.styleVersionMuted).Render(fitCellText(fallbackVersion, width))
	}
}

// Restricted to enabledAgents because PerAgentStatus can carry stale entries for agents no longer installed or enabled on this host.
func skillAgentsWithStatus(r app.SkillPackageRow, enabledAgents []string, wanted app.SkillStatus) []string {
	enabled := make(map[string]bool, len(enabledAgents))
	for _, id := range enabledAgents {
		enabled[id] = true
	}
	var matched []string
	for id, status := range r.PerAgentStatus {
		if status == wanted && enabled[id] {
			matched = append(matched, id)
		}
	}
	sort.Strings(matched)
	return matched
}

func skillLinkedAgents(r app.SkillPackageRow, enabledAgents []string) []string {
	return skillAgentsWithStatus(r, enabledAgents, app.SkillStatusInstalled)
}

func skillDriftedAgents(r app.SkillPackageRow, enabledAgents []string) []string {
	return skillAgentsWithStatus(r, enabledAgents, app.SkillStatusDrifted)
}

func skillShadowedAgents(r app.SkillPackageRow, enabledAgents []string) []string {
	return skillAgentsWithStatus(r, enabledAgents, app.SkillStatusShadowed)
}

func skillMissingAgents(r app.SkillPackageRow, enabledAgents []string) []string {
	return skillAgentsWithStatus(r, enabledAgents, app.SkillStatusMissing)
}

func skillLinkageSummary(r app.SkillPackageRow, enabledAgents []string) string {
	return skillAgentsCellText(skillLinkedAgents(r, enabledAgents))
}

func skillAgentsCellText(ids []string) string {
	switch len(ids) {
	case 0:
		return ""
	case 1:
		return ids[0]
	default:
		return strconv.Itoa(len(ids)) + " agents"
	}
}

// Continuation lines hang-indent to len(label); never splits a name mid-word and never truncates with "+N more".
func wrapNamesLines(m Model, label string, names []string) []string {
	if len(names) == 0 {
		return nil
	}
	indent := strings.Repeat(" ", lipgloss.Width(label))
	width := max(m.width-lipgloss.Width(textRowContentPrefix())-screenEdgePadding, 12)

	var lines []string
	prefix := label
	line := ""
	for _, name := range names {
		candidate := name
		if line != "" {
			candidate = line + ", " + name
		}
		if line != "" && lipgloss.Width(prefix+candidate) > width {
			lines = append(lines, prefix+line)
			prefix = indent
			line = name
			continue
		}
		line = candidate
	}
	if line != "" {
		lines = append(lines, prefix+line)
	}
	return lines
}

func skillDetailLines(m Model, r app.SkillPackageRow) []string {
	var lines []string
	if r.Description != "" {
		lines = append(lines, statusDetailLine(m, r.Description))
	}
	lines = append(lines, statusDetailLine(m, "source: "+r.Source))
	for _, l := range wrapNamesLines(m, "skills: ", r.Skills) {
		lines = append(lines, statusDetailLineIndented(m, l))
	}
	for _, l := range wrapNamesLines(m, "linked: ", skillLinkedAgents(r, m.enabledAgents)) {
		lines = append(lines, statusDetailLineIndented(m, l))
	}
	for _, l := range wrapNamesLines(m, "drifted: ", skillDriftedAgents(r, m.enabledAgents)) {
		lines = append(lines, statusDetailLineIndented(m, l))
	}
	for _, l := range wrapNamesLines(m, "shadowed by managed package: ", skillShadowedAgents(r, m.enabledAgents)) {
		lines = append(lines, statusDetailLineIndented(m, l))
	}
	if skillRowOutdated(r) {
		lines = append(lines, statusDetailLine(m, "upgrade available"))
	}
	if r.ShadowedByPlugin {
		lines = append(lines, statusDetailLine(m, "provided by plugin "+skillPackageRepoNameDisplay(r.Source)))
	}
	if len(r.UnknownAgents) > 0 {
		lines = append(lines, statusDetailLine(m, "unknown agent target(s): "+strings.Join(r.UnknownAgents, ", ")))
	}
	return lines
}

// The plugin's Name has no owner prefix, so the display must match what actually shadowed it, not the full owner/repo source.
func skillPackageRepoNameDisplay(source string) string {
	if i := strings.LastIndexByte(source, '/'); i >= 0 {
		return source[i+1:]
	}
	return source
}

func shaShort(sha string) string {
	if len(sha) <= 7 {
		return sha
	}
	return sha[:7]
}

func agentsStatusLabel(s agentsRowStatus) string {
	switch s {
	case agentsStatusUpdates:
		return "Updates Available"
	case agentsStatusOutOfSync:
		return "Out of Sync"
	case agentsStatusInstalled:
		return "Installed"
	case agentsStatusIgnored:
		return "Ignored"
	default:
		return "Available"
	}
}

func renderAgentsGroupedTab(m Model, p palette, topLines []string, only agentsSection, filtered bool) string {
	pad := screenEdgeInset()
	full := agentsAllRowsList(m)

	var rows []agentsAllRow
	for _, e := range full {
		if filtered && e.feature != only {
			continue
		}
		rows = append(rows, e)
	}

	if len(rows) == 0 {
		chip := agentsChipAll
		if filtered {
			switch only {
			case agentsSectionSkills:
				chip = agentsChipSkills
			case agentsSectionMcp:
				chip = agentsChipMcp
			case agentsSectionPlugins:
				chip = agentsChipPlugin
			case agentsSectionMarketplaces:
				chip = agentsChipMarketplace
			}
		}
		empty := agentsEmptyStateLines(p, pad, chip)
		if !agentsRowsKnownForChip(m, chip) {
			// Rows not loaded yet — onboarding hints would misread as "nothing tracked" while the initial adapter loads are still running.
			empty = nil
		}
		return renderSectionedTab(m, sectionedTab{
			leadingBlank: false,
			top:          topLines,
			sections: []sectionedTabSection{{
				rows:  nil,
				empty: empty,
			}},
		})
	}

	cols := agentsColWidths(m, rows)
	hintPrefix := listHintPrefix()

	selectedIdx := -1
	if filtered {
		cursor := agentsFeatureCursor(m, only)
		cursorAgentID := agentsFeatureCursorAgentID(m, only)
		fallbackIdx := -1
		for i, e := range rows {
			if e.localIdx != cursor {
				continue
			}
			if e.agentID == cursorAgentID {
				selectedIdx = i
				break
			}
			if fallbackIdx < 0 {
				fallbackIdx = i
			}
		}
		// Chip cursor not yet positioned on a real row; fall back to the first row sharing localIdx so something stays selected.
		if selectedIdx < 0 {
			selectedIdx = fallbackIdx
		}
	} else if m.agentsAllCursor >= 0 && m.agentsAllCursor < len(full) {
		selectedIdx = m.agentsAllCursor
	}

	var sections []sectionedTabSection
	var cur *sectionedTabSection
	lastStatus := agentsRowStatus(-1)
	for i, e := range rows {
		if e.status != lastStatus {
			if cur != nil {
				sections = append(sections, *cur)
			}
			cur = &sectionedTabSection{title: agentsStatusLabel(e.status)}
			lastStatus = e.status
		}
		selected := i == selectedIdx && !m.cursorHidden
		left, right := agentsRowCells(m, p, cols, e, selected)
		line := listRowPrefix(p, selected) + renderSplitRow(left, right, rowAvailableWidth(m.width), listColumnGap, listColumnGap)
		var details []string
		if selected {
			width := max(m.width-lipgloss.Width(textRowContentPrefix())-screenEdgePadding, 12)
			for _, detail := range fullMembershipDetailLines(agentsRowGroups(m, e), width) {
				details = append(details, statusDetailLine(m, detail))
			}
			details = append(details, agentsRowDetailLines(m, e)...)
			details = append(details, renderHintItems(p, hintPrefix, agentsRowHints(m, e)))
		}
		cur.rows = append(cur.rows, sectionedTabRow{selected: selected, line: line, details: details})
	}
	if cur != nil {
		sections = append(sections, *cur)
	}

	return renderSectionedTab(m, sectionedTab{
		leadingBlank: false,
		top:          topLines,
		sections:     sections,
	})
}

// The all chip needs every enabled section known — one still-loading section could masquerade as empty.
func agentsRowsKnownForChip(m Model, chip int) bool {
	switch chip {
	case agentsChipSkills:
		return m.skillsRowsKnown
	case agentsChipMcp:
		return m.mcpRowsKnown
	case agentsChipPlugin:
		return m.pluginRowsKnown
	case agentsChipMarketplace:
		return m.marketplaceRowsKnown
	default:
		return (!m.skillsSectionEnabled() || m.skillsRowsKnown) &&
			(!m.mcpSectionEnabled() || m.mcpRowsKnown) &&
			(!m.pluginsSectionEnabled() || m.pluginRowsKnown) &&
			(!m.marketplacesSectionEnabled() || m.marketplaceRowsKnown)
	}
}

func agentsEmptyStateLines(p palette, pad string, chip int) []string {
	switch chip {
	case agentsChipSkills:
		return []string{
			p.styleHelp.Render(pad + "No agent skills tracked yet."),
			"",
			pad + p.styleNormal.Render("[i] import ") + p.styleHelp.Render(" capture legacy lockfile skills"),
			pad + p.styleNormal.Render("[r] sync   ") + p.styleHelp.Render(" install your declared skills on this machine"),
		}
	case agentsChipMcp:
		return []string{
			p.styleHelp.Render(pad + "No MCP servers tracked yet."),
			"",
			pad + p.styleNormal.Render("[i] install") + p.styleHelp.Render(" an out-of-sync MCP server on this machine"),
		}
	case agentsChipPlugin:
		return []string{
			p.styleHelp.Render(pad + "No plugins tracked yet."),
			"",
			pad + p.styleNormal.Render("[i] install") + p.styleHelp.Render(" an out-of-sync plugin on this machine"),
		}
	case agentsChipMarketplace:
		return []string{
			p.styleHelp.Render(pad + "No marketplaces tracked yet."),
			"",
			pad + p.styleNormal.Render("[c] claim  ") + p.styleHelp.Render(" adopt a marketplace already added via an agent CLI"),
		}
	default:
		return []string{p.styleHelp.Render(pad + "No agent items tracked yet.")}
	}
}

// Both sides of a drifted mcp row: 'l use local' adopts the live registration, so its values must be readable without leaving the row. nil when not drifted on this agent.
func mcpDriftDetailLines(m Model, r app.McpServerRow, agentID string) []string {
	fields := r.DriftFields[agentID]
	live, ok := r.DriftLive[agentID]
	if len(fields) == 0 || !ok {
		return nil
	}
	manifestText, liveText := mcpIdentityPair(r, live)
	return []string{
		statusDetailLine(m, "manifest: "+manifestText),
		statusDetailLine(m, agentID+": "+liveText),
		statusDetailLine(m, "differs: "+strings.Join(fields, ", ")),
	}
}

// Keeps a field whenever either side carries one, so a value present on only one side still shows as absent on the other.
func mcpIdentityPair(r app.McpServerRow, live app.InstalledMcpServer) (manifest, installed string) {
	var manifestParts, liveParts []string
	add := func(label, want, got string) {
		if want == "" && got == "" {
			return
		}
		manifestParts = append(manifestParts, label+": "+mcpFieldText(want))
		liveParts = append(liveParts, label+": "+mcpFieldText(got))
	}
	add("transport", r.Transport, live.Transport)
	add("command", r.Command, live.Command)
	add("url", r.URL, live.URL)
	return strings.Join(manifestParts, "  "), strings.Join(liveParts, "  ")
}

func mcpFieldText(value string) string {
	if value == "" {
		return "—"
	}
	return value
}

// Only mcp adopts a value on use-local; skills and plugins keep what is installed, so every other row keeps the bare label.
func agentsUseLocalHintLabel(m Model, e agentsAllRow) string {
	const label = "use local"
	value := mcpDriftLocalValue(m, e)
	if value == "" {
		return "confirm " + label
	}
	budget := m.width - lipgloss.Width(listHintPrefix()) - screenEdgePadding -
		lipgloss.Width("press "+m.keys.AgentsUseLocal.Help().Key+" again to "+label+" ()")
	value = clipValueHead(value, budget)
	if value == "" {
		return "confirm " + label
	}
	return label + " (" + value + ")"
}

func mcpDriftLocalValue(m Model, e agentsAllRow) string {
	if e.feature != agentsSectionMcp || e.synthetic || e.localIdx < 0 || e.localIdx >= len(m.mcpRows) {
		return ""
	}
	row := m.mcpRows[e.localIdx]
	live, ok := row.DriftLive[e.agentID]
	if !ok {
		return ""
	}
	var parts []string
	for _, field := range row.DriftFields[e.agentID] {
		switch field {
		case "transport":
			parts = append(parts, "transport: "+mcpFieldText(live.Transport))
		case "command":
			parts = append(parts, "command: "+mcpFieldText(live.Command))
		case "url":
			parts = append(parts, "url: "+mcpFieldText(live.URL))
		}
	}
	return strings.Join(parts, ", ")
}

// Urls and commands are told apart by their tail, so the head is what can go; the detail lines still carry the value in full.
func clipValueHead(s string, width int) string {
	if width < 1 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return "…" + string(runes[len(runes)-width+1:])
}

func agentsRowDetailLines(m Model, e agentsAllRow) []string {
	if e.synthetic {
		return []string{statusDetailLine(m, "ignored — not currently installed")}
	}
	switch e.feature {
	case agentsSectionSkills:
		rows, findStart, unmanagedStart := skillsVisibleRows(m)
		if e.localIdx < 0 || e.localIdx >= len(rows) {
			return nil
		}
		r := rows[e.localIdx]
		if unmanagedStart >= 0 && e.localIdx >= unmanagedStart {
			summary := "source: " + r.Source
			if r.Updated != "" {
				summary += "  updated: " + r.Updated
			}
			summary += "  not in manifest"
			lines := []string{statusDetailLine(m, summary)}
			for _, l := range wrapNamesLines(m, "linked: ", skillLinkedAgents(r, m.enabledAgents)) {
				lines = append(lines, statusDetailLineIndented(m, l))
			}
			return lines
		}
		if findStart >= 0 && e.localIdx >= findStart {
			return nil
		}
		return skillDetailLines(m, r)
	case agentsSectionMcp:
		if e.localIdx < len(m.mcpRows) {
			r := m.mcpRows[e.localIdx]
			if lines := mcpDriftDetailLines(m, r, e.agentID); lines != nil {
				return lines
			}
			summary := "transport: " + r.Transport
			if r.Command != "" {
				summary += "  command: " + r.Command
			}
			if r.URL != "" {
				summary += "  url: " + r.URL
			}
			if r.ShadowedByPlugin {
				summary += "  provided by plugin " + r.Name
			}
			return []string{statusDetailLine(m, summary)}
		}
		if flat := mcpUnmanagedFlat(m.mcpUnmanaged); e.localIdx-len(m.mcpRows) < len(flat) {
			entry := flat[e.localIdx-len(m.mcpRows)]
			summary := "transport: " + entry.srv.Transport
			if entry.srv.Command != "" {
				summary += "  command: " + entry.srv.Command
			}
			if entry.srv.URL != "" {
				summary += "  url: " + entry.srv.URL
			}
			return []string{statusDetailLine(m, summary)}
		}
		return nil
	case agentsSectionPlugins:
		if e.localIdx < len(m.pluginRows) {
			r := m.pluginRows[e.localIdx]
			summary := "marketplace: " + r.Marketplace
			if r.Version != "" {
				summary += "  version: " + r.Version
			} else if r.Sha != "" {
				summary += "  sha: " + shaShort(r.Sha)
			}
			if r.Description != "" {
				return []string{statusDetailLine(m, r.Description), statusDetailLine(m, summary)}
			}
			return []string{statusDetailLine(m, summary)}
		}
		if flat := pluginUnmanagedFlat(m.pluginUnmanaged); e.localIdx-len(m.pluginRows) < len(flat) {
			pl := flat[e.localIdx-len(m.pluginRows)].plugin
			summary := "marketplace: " + pl.Marketplace + " (unmanaged)"
			if pl.Version != "" {
				summary += "  version: " + pl.Version
			} else if pl.Sha != "" {
				summary += "  sha: " + shaShort(pl.Sha)
			}
			return []string{statusDetailLine(m, summary)}
		}
		return nil
	case agentsSectionMarketplaces:
		if e.localIdx < len(m.marketplaceRows) {
			r := m.marketplaceRows[e.localIdx]
			summary := "source: " + r.Source
			if updated := marketplaceUpdatedAtText(r.UpdatedAt); updated != "" {
				summary += "  updated: " + updated
			}
			return []string{statusDetailLine(m, summary)}
		}
		if flat := marketplaceUnmanagedFlat(m.marketplaceUnmanaged); e.localIdx-len(m.marketplaceRows) < len(flat) {
			mk := flat[e.localIdx-len(m.marketplaceRows)].marketplace
			summary := "source: " + mk.Source + " (unmanaged)"
			if updated := marketplaceUpdatedAtText(mk.UpdatedAt); updated != "" {
				summary += "  updated: " + updated
			}
			return []string{statusDetailLine(m, summary)}
		}
		return nil
	default:
		return nil
	}
}

func agentsFeatureCursor(m Model, feature agentsSection) int {
	switch feature {
	case agentsSectionSkills:
		return m.skillsCursor
	case agentsSectionMcp:
		return m.mcpCursor
	case agentsSectionPlugins:
		return m.pluginCursor
	case agentsSectionMarketplaces:
		return m.marketplaceCursor
	default:
		return m.pluginCursor
	}
}

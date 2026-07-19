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

// agentsAgentIDColFloor reserves space for the longest well-known agent ID
// ("claude-code") so cols.prov starts wide enough that column x-offsets stay
// roughly comparable to the tools tab even though agents' prov content
// (agent IDs / linkage summaries) is shaped differently than tools' short
// package-manager labels.
const agentsAgentIDColFloor = len("claude-code")

// agentsTypeColW is the fixed width of the feature-type column
// (skills/mcp/plugin), positioned between the name and agent columns. Wide
// enough for "skills"/"plugin" (6 runes) plus one column of breathing room;
// never shrunk by fitToolColumnsToScreen, so it's reserved on every row
// regardless of content, matching this file's fixed-grid rule for other
// always-present columns.
const agentsTypeColW = 7

// agentsFeatureLabel returns the display label for a feature's type column.
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

// agentsColWidths sizes the agent-label and version columns from the visible
// flatten, then flexes the name column via fitToolColumnsToScreen — the same
// shrink order the tools tab uses (group -> name -> ver -> prov -> ...) —
// since agentsCols and colWidths share the same field shape once priv is 0.
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

// agentsProvCellText returns the unstyled agent-cell text for width
// measurement, mirroring the agentLabel resolution in agentsRowCells: the
// per-agent ID for mcp/plugin rows, or the linkage summary for skills rows.
func agentsProvCellText(m Model, e agentsAllRow) string {
	if e.synthetic {
		return ""
	}
	if e.feature != agentsSectionSkills {
		return e.agentID
	}
	rows, _, _ := skillsVisibleRows(m)
	if e.localIdx < 0 || e.localIdx >= len(rows) {
		return ""
	}
	if e.status == agentsStatusIgnored {
		return ""
	}
	return skillLinkageSummary(rows[e.localIdx], m.enabledAgents)
}

// agentsVersionCellText returns the unstyled version/date cell text for
// width measurement, mirroring the styled version rendered in agentsRowCells.
func agentsVersionCellText(m Model, e agentsAllRow) string {
	if e.synthetic {
		return ""
	}
	if e.mark == agentsMarkShadowed {
		return "via plugin"
	}
	switch e.feature {
	case agentsSectionSkills:
		rows, _, _ := skillsVisibleRows(m)
		if e.localIdx < 0 || e.localIdx >= len(rows) {
			return ""
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
		// PathOutdated (see PluginRow.Outdated's doc comment) has no comparable
		// before/after value to show as an arrow — it's a git-history
		// comparison, not a version or sha pair — so fall back to a plain
		// label rather than silently showing nothing for the common
		// versionless-plugin case.
		if row.Outdated() {
			return "update available"
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

// marketplaceUpdatedAtText formats a marketplace's last-update time for the
// version/date cell, blank when unknown (t is the zero value) rather than a
// misleading placeholder date.
func marketplaceUpdatedAtText(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

// agentsMarketplaceRowAt resolves a marketplace row's (Name, UpdatedAt) by
// localIdx across both the managed rows and the unmanaged flatten, mirroring
// agentsPluginRowAt.
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

// agentsMcpRowAt resolves a mcp row's (Name, Version) by localIdx across
// both the managed rows and the unmanaged flatten, mirroring
// agentsPluginRowAt's managed/unmanaged split. Only the fields callers need
// (name display, version cell) are populated.
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

// agentsHuePalette is the small set of existing hue-bearing styles cycled
// through for per-agent-ID coloring, since the agent ID set (claude-code,
// codex, cursor, ...) is unbounded/extensible and doesn't map onto tools'
// fixed brew/node/python taxonomy.
func agentsHuePalette(p palette) []lipgloss.Style {
	return []lipgloss.Style{p.styleProvider, p.styleProviderLinux, p.styleProviderSystem, p.styleOrphan}
}

// styleForAgent maps an agent ID to a stable hue via a deterministic hash,
// so the same agentID always renders in the same color. agentID is only
// colored when it's a literal single agent ID (e.g. "claude-code") — skills
// rows carrying a linkage summary ("2 agents") or a "-"/"" placeholder keep
// the flat styleHelp since those strings aren't tied to one agent's hue.
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

// agentsRowRunKey identifies an agents-all row for in-flight op tracking,
// mirroring toolKey's "\x00"-joined composite key convention.
func agentsRowRunKey(e agentsAllRow) string {
	return strconv.Itoa(int(e.feature)) + "\x00" + strconv.Itoa(e.localIdx) + "\x00" + e.agentID
}

// agentsMarkCell renders the sync-status icon for the name cell, reusing the
// same icon glyphs and styles renderToolRow applies for the equivalent
// syncStatus so agents rows read consistently with the tools tab.
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
	case mark == agentsMarkMissing:
		return emphasis(p.styleMissing).Render(iconMissing)
	case mark == agentsMarkShadowed:
		return emphasis(p.styleWrongProv).Render(iconWrongProv)
	default:
		return emphasis(p.styleInstalled).Render(iconInstalled)
	}
}

// agentsRowCells builds the shared [mark+name] [group badge] | [agent]
// [version/date] column layout for one flattened agents row, resolving the
// underlying feature row by (e.feature, e.localIdx).
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
		rows, _, _ := skillsVisibleRows(m)
		if e.localIdx >= 0 && e.localIdx < len(rows) {
			r := rows[e.localIdx]
			nameCell = nameStyle.Render(fitCellText(r.Name, cols.name))
			groupBadge = agentsGroupBadge(p, r.Groups, m.hostInfo, cols.group, groupEmphasis)
			switch {
			case e.mark == agentsMarkMissing:
				ver = emphasis(p.styleMissing).Render("missing")
			case e.mark == agentsMarkShadowed:
				ver = emphasis(p.styleWrongProv).Render(fitCellText("via plugin", cols.ver))
			case e.status != agentsStatusIgnored:
				verStyle := p.styleVersionMuted
				ver = emphasis(verStyle).Render(fitCellText(r.Updated, cols.ver))
			}
			if e.status != agentsStatusIgnored {
				skillsLinkage = skillLinkageSummary(r, m.enabledAgents)
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

// renderPluginUpdateCell renders a plugin row's version cell from its update
// verdict. The managed and unmanaged plugin paths share this one render so
// neither re-decides "outdated" independently of app.PluginRow.Outdated.
func renderPluginUpdateCell(p palette, emphasis func(lipgloss.Style) lipgloss.Style, u app.PluginUpdate, fallbackVersion string, width int) string {
	switch u.Kind {
	case app.PluginVersionUpgrade:
		current, latest := fitUpgradeVersionText(compactVersion(u.Current), compactVersion(u.Latest), width)
		return emphasis(p.styleMissing).Render(current) + emphasis(p.styleOutdated).Render(latest)
	case app.PluginShaDrift:
		return emphasis(p.styleMissing).Render(fitCellText(shaShort(u.Current), width/2)) + emphasis(p.styleOutdated).Render(" → "+fitCellText(shaShort(u.Latest), width/2))
	case app.PluginUpdateAvailable:
		return emphasis(p.styleOutdated).Render(fitCellText("update available", width))
	default:
		return emphasis(p.styleVersionMuted).Render(fitCellText(fallbackVersion, width))
	}
}

// skillLinkedAgents returns the agent IDs with a confirmed symlink into the
// canonical skills store, restricted to enabledAgents (the installed ∩
// configured-use set), sorted for stable display. A row's PerAgentStatus can
// carry stale entries for agents no longer installed/enabled on this host
// (managed rows keyed by prior targets, unmanaged rows keyed by every
// installed agent at scan time); enabledAgents keeps the display in sync with
// the host's current agent set.
func skillLinkedAgents(r app.SkillPackageRow, enabledAgents []string) []string {
	enabled := make(map[string]bool, len(enabledAgents))
	for _, id := range enabledAgents {
		enabled[id] = true
	}
	var linked []string
	for id, ok := range r.PerAgentStatus {
		if ok && enabled[id] {
			linked = append(linked, id)
		}
	}
	sort.Strings(linked)
	return linked
}

// skillLinkageSummary is the agent-cell text for a package-level skills row:
// blank when nothing (or nothing meaningful) is linked, the sole agent ID
// when exactly one is linked, else "N agents".
func skillLinkageSummary(r app.SkillPackageRow, enabledAgents []string) string {
	linked := skillLinkedAgents(r, enabledAgents)
	switch len(linked) {
	case 0:
		return ""
	case 1:
		return linked[0]
	default:
		return strconv.Itoa(len(linked)) + " agents"
	}
}

// wrapNamesLines greedily fills names (comma-separated) onto as many lines
// as needed to fit within the row's available detail-line width, with the
// label prefixed only on the first line and continuation lines hang-indented
// to align under the first name (i.e. padded to len(label), not a fixed
// amount), regardless of terminal width. Never splits a name mid-word. Used
// for skills/linked-agent detail lists that must show every entry (no "+N
// more" truncation).
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

// skillDetailLines builds the full, untruncated detail lines for a managed
// skills row: source, then every skill name, then every linked agent, each
// wrapped across as many lines as needed. Unlike the single-line summary this
// replaced, no "+N more" marker is ever emitted.
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
	if r.ShadowedByPlugin {
		lines = append(lines, statusDetailLine(m, "provided by plugin "+skillPackageRepoNameDisplay(r.Source)))
	}
	return lines
}

// skillPackageRepoNameDisplay extracts a package source's bare repo-segment
// name for the "provided by plugin X" detail line — the plugin's Name has no
// owner prefix, so the display must match what actually shadowed it (see
// app.SkillPackageRow.ShadowedByPlugin's doc comment), not the full
// owner/repo source string.
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

// renderAgentsGroupedTab renders the agents flatten grouped by status
// (Updates Available / Out of Sync / Installed / Available / Ignored), same
// section labels and order as the tools tab. When filtered is true, only
// entries matching only are shown but iteration keeps the same status
// ordering agentsAllRowsList already produced.
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
			// Rows haven't arrived yet (neither cached nor live) — the
			// onboarding hints would misread as "nothing tracked" while the
			// initial adapter CLI loads are still running. Render nothing
			// until the table data is ready.
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
		// No exact (localIdx, agentID) row matched — e.g. the chip's cursor
		// fields haven't been positioned onto a real row yet (still their
		// zero value). Fall back to the first row sharing localIdx so a row
		// is still visibly selected, matching pre-agentID-tracking behavior.
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

// agentsRowsKnownForChip reports whether the rows behind chip reflect reality
// (cache-seeded or live-loaded). The all chip needs every enabled section
// known — one still-loading section could otherwise masquerade as empty.
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

// agentsEmptyStateLines returns the zero-row empty-state content for the
// given chip, mirroring the skills chip's existing rich empty state (import/
// restore hints) in view_skills.go for mcp/plugin, instead of the flat
// generic line that previously fired for every chip.
func agentsEmptyStateLines(p palette, pad string, chip int) []string {
	switch chip {
	case agentsChipSkills:
		return []string{
			p.styleHelp.Render(pad + "No agent skills tracked yet."),
			"",
			pad + p.styleNormal.Render("[i] import ") + p.styleHelp.Render(" capture skills already installed via the skills CLI"),
			pad + p.styleNormal.Render("[r] restore") + p.styleHelp.Render(" install your declared skills on this machine"),
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

// agentsRowDetailLines builds the selected-row detail line (an item-level
// summary — skill names, mcp transport/command, or plugin marketplace/version
// — since the row itself already identifies the single agent it belongs to).
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

// agentsFeatureCursor returns the per-feature cursor value for a type chip,
// which already indexes the same localIdx space the filtered flatten uses.
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

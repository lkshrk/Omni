package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/actions"
	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/text"
)

// renderFilterBar renders the provider and group filter pill bars.
// Group pills are shown when the config has reusable groups. The current host
// group is added separately so users can filter local-only entries.
// Provider pills are provider families from the provider registry, not a
// projection of the currently visible tools.
// Returns empty string when neither condition is met.
func renderFilterBar(m Model) string {
	p := m.palette
	groupNames := visibleGroupNames(m)
	hasGroupPills := len(groupNames) > 0
	hasProvPills := len(m.providerNames) > 0
	if !hasGroupPills && !hasProvPills {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("  ")
	available := max(m.width-lipgloss.Width(screenEdgeInset()), 1)
	used := 0

	if hasProvPills {
		provBar := renderPillBarFit(p, m.providerNames, m.providerTabIdx, available)
		sb.WriteString(provBar)
		used += lipgloss.Width(provBar)
	}
	if hasGroupPills {
		remaining := available - used
		if hasProvPills && remaining > lipgloss.Width(pillBarSeparator) {
			sb.WriteString(p.styleHelp.Render(pillBarSeparator))
			remaining -= lipgloss.Width(pillBarSeparator)
		} else if hasProvPills {
			return sb.String()
		}
		allGroups := buildAllGroupNames(groupNames)
		sb.WriteString(renderPillBarFit(p, allGroups, m.groupTabIdx, remaining))
	}
	return sb.String()
}

type toolFilterKind int

const (
	toolFilterProvider toolFilterKind = iota
	toolFilterGroup
	agentsFilterType
	agentsFilterAgent
)

type toolFilterHitZone struct {
	kind       toolFilterKind
	index      int
	start, end int
	y          int
}

func toolFilterHitZones(m Model) []toolFilterHitZone {
	if m.mode != viewList && m.mode != viewSearch {
		return nil
	}

	groupNames := visibleGroupNames(m)
	hasGroupPills := len(groupNames) > 0
	hasProvPills := len(m.providerNames) > 0
	if !hasGroupPills && !hasProvPills {
		return nil
	}

	x := lipgloss.Width(screenEdgeInset())
	available := max(m.width-x, 1)
	y := toolFilterBarY(m)
	var zones []toolFilterHitZone
	if hasProvPills {
		provZones, used := pillHitZones(toolFilterProvider, m.providerNames, m.providerTabIdx, x, y, available)
		zones = append(zones, provZones...)
		x += used
		available -= used
	}
	if hasGroupPills {
		sepW := lipgloss.Width(pillBarSeparator)
		if hasProvPills && available > sepW {
			x += sepW
			available -= sepW
		} else if hasProvPills {
			return zones
		}
		groupZones, _ := pillHitZones(toolFilterGroup, buildAllGroupNames(groupNames), m.groupTabIdx, x, y, available)
		zones = append(zones, groupZones...)
	}
	return zones
}

func toolFilterBarY(m Model) int {
	if m.mode == viewSearch {
		return 4
	}
	return 2
}

// agentsFilterHitZones computes click targets for the agents tab's two pill
// bars (type chips, agent-ID filter), mirroring exactly what viewSkillsBody
// renders: same "  " x-offset, same shared line, same width budget.
func agentsFilterHitZones(m Model) []toolFilterHitZone {
	if m.mode != viewSkills {
		return nil
	}
	if !m.skillsSectionEnabled() && !m.mcpSectionEnabled() && !m.pluginsSectionEnabled() {
		return nil
	}

	pad := screenEdgeInset()
	available := max(m.width-lipgloss.Width(pad), 1)
	disabledChips := map[int]bool{
		agentsChipSkills: !m.skillsSectionEnabled(),
		agentsChipMcp:    !m.mcpSectionEnabled(),
		agentsChipPlugin: !m.pluginsSectionEnabled(),
	}

	y := 2
	if m.skillsSearchActive {
		y = 3
	}

	x := lipgloss.Width("  ")
	typeLabels := []string{"all", "skills", "mcp", "plugin"}
	typeZones, used := pillHitZonesDim(agentsFilterType, typeLabels, m.skillTypeIdx, disabledChips, x, y, available)
	zones := append([]toolFilterHitZone{}, typeZones...)

	agentIDs := skillAgentIDs(m.skillsRows, m.enabledAgents)
	if len(agentIDs) > 0 {
		sepW := lipgloss.Width(pillBarSeparator)
		remaining := available - used
		if remaining > sepW {
			agentZones, _ := pillHitZones(agentsFilterAgent, agentIDs, m.skillAgentIdx, x+used+sepW, y, remaining-sepW)
			zones = append(zones, agentZones...)
		}
	}
	return zones
}

// pillHitZonesDim mirrors pillHitZones but for renderPillBarDim's label
// convention: labels are used verbatim (no "all" prepend) and a disabled
// index never yields a clickable zone.
func pillHitZonesDim(kind toolFilterKind, labels []string, activeIdx int, disabled map[int]bool, start, y, maxW int) ([]toolFilterHitZone, int) {
	zones := make([]toolFilterHitZone, 0, len(labels))
	x := start
	for i, label := range labels {
		active := activeIdx == i && !disabled[i]
		w := pillCellWidth(label, active, maxW-(x-start))
		if w <= 0 {
			break
		}
		if !disabled[i] {
			zones = append(zones, toolFilterHitZone{kind: kind, index: i, start: x, end: x + w, y: y})
		}
		x += w
	}
	return zones, x - start
}

func pillHitZones(kind toolFilterKind, names []string, activeIdx, start, y, maxW int) ([]toolFilterHitZone, int) {
	labels := make([]string, 0, len(names)+1)
	labels = append(labels, "all")
	labels = append(labels, names...)

	zones := make([]toolFilterHitZone, 0, len(labels))
	x := start
	for i, label := range labels {
		w := pillCellWidth(label, activeIdx == i, maxW-(x-start))
		if w <= 0 {
			break
		}
		zones = append(zones, toolFilterHitZone{kind: kind, index: i, start: x, end: x + w, y: y})
		x += w
	}
	return zones, x - start
}

func pillCellWidth(label string, active bool, maxW int) int {
	if maxW <= 0 {
		return 0
	}
	fullW := lipgloss.Width(pillText(label, active))
	if fullW <= maxW {
		return fullW
	}
	if maxW < 4 {
		return 0
	}
	return maxW
}

func renderPillBarFit(pal palette, names []string, activeIdx, maxW int) string {
	labels := make([]string, 0, len(names)+1)
	labels = append(labels, "all")
	labels = append(labels, names...)
	var sb strings.Builder
	used := 0
	for i, label := range labels {
		active := activeIdx == i
		w := pillCellWidth(label, active, maxW-used)
		if w <= 0 {
			break
		}
		text := pillText(label, active)
		if lipgloss.Width(text) > w {
			if active {
				text = "[" + fitCellText(label, max(w-2, 1)) + "]"
			} else {
				text = " " + fitCellText(label, max(w-2, 1)) + " "
			}
		}
		if active {
			sb.WriteString(pal.styleTitle.Render(text))
		} else {
			sb.WriteString(pal.styleHelp.Render(text))
		}
		used += w
	}
	return sb.String()
}

func pillText(label string, active bool) string {
	if active {
		return "[" + label + "]"
	}
	return " " + label + " "
}

// renderPillBarDim renders pills for a fixed set of names without prepending
// an "all" option (activeIdx maps directly to names[activeIdx]). Pills
// flagged in disabled render with the dim help style and never as active, so
// a disabled section's chip reads as unselectable even if activeIdx briefly
// points at it.
func renderPillBarDim(pal palette, names []string, activeIdx, maxW int, disabled map[int]bool) string {
	var sb strings.Builder
	used := 0
	for i, label := range names {
		active := activeIdx == i && !disabled[i]
		w := pillCellWidth(label, active, maxW-used)
		if w <= 0 {
			break
		}
		text := pillText(label, active)
		if lipgloss.Width(text) > w {
			if active {
				text = "[" + fitCellText(label, max(w-2, 1)) + "]"
			} else {
				text = " " + fitCellText(label, max(w-2, 1)) + " "
			}
		}
		if active {
			sb.WriteString(pal.styleTitle.Render(text))
		} else {
			sb.WriteString(pal.styleHelp.Render(text))
		}
		used += w
	}
	return sb.String()
}

func renderList(m Model) string {
	p := m.palette
	var sb strings.Builder

	// Single filter bar — rendered above the tool list (not scrolled).
	if bar := renderFilterBar(m); bar != "" {
		sb.WriteString(bar)
	}
	sb.WriteByte('\n')
	subtabLines := 1

	if len(m.visibleTools) == 0 {
		if len(m.scanningProviders) > 0 || m.loading || (m.mode == viewSearch && m.searching) {
			return sb.String()
		} else {
			sb.WriteString(p.styleHelp.Render(emptyToolListText(m)))
		}
		return sb.String()
	}

	// Build a flat slice of display lines (section headers + tool rows).
	type displayRow struct {
		text    string
		toolIdx int // -1 for headers/blanks
	}
	var rows []displayRow
	cursorRow := 0
	var lastSec section = -1

	sectionLabel := func(s section) string {
		switch s {
		case sectionUpdates:
			return "Updates Available"
		case sectionQuarantined:
			return "Quarantined Updates"
		case sectionOutOfSync:
			return "Out of Sync"
		case sectionInstalled:
			return "Installed"
		case sectionIgnored:
			return "Ignored"
		default:
			return "Available"
		}
	}

	// Pre-compute column widths then detail lines (detail needs cols for wrap width).
	cols := newColWidthsWithProviderPins(m.visibleTools, m.toolGroups, visibleGroupNames(m), m.toolProviderPins, m.toolFallbacks, m.effectiveSystemManager, m.effectivePythonManager, m.effectiveNodeManager, m.width, func(t *database.ToolCache) bool {
		return m.syncStatusOf(t) == syncWrongProv
	})
	cols = fitToolColumnsForRowErrors(cols, m.visibleTools, m.rowErrors)
	detail := inlineDetailLines(m, m.width, cols)

	for i, t := range m.visibleTools {
		sec := m.displaySection(t)
		if sec != lastSec {
			if lastSec != -1 {
				rows = append(rows, displayRow{text: "", toolIdx: -1})
			}
			rows = append(rows, displayRow{
				text:    renderSectionHeader(p, sectionLabel(sec), m.width),
				toolIdx: -1,
			})
			lastSec = sec
		}

		spinnerView := ""
		key := toolKey(t.Name, t.Provider)
		if len(m.upgradingKeys) > 0 {
			if m.upgradingKeys["*"] && t.Outdated && m.bulkPendingKeys[key] {
				spinnerView = p.styleStatus.Render(iconPending)
			} else if m.upgradingKeys[key] {
				spinnerView = rowSpinnerIcon(m)
			}
		}
		if spinnerView == "" && m.bulkPendingKeys[key] {
			spinnerView = p.styleStatus.Render(iconPending)
		}
		if m.rowOpKey == key {
			spinnerView = rowSpinnerIcon(m)
		}
		group := m.toolGroups[key]
		isIgnored := sec == sectionIgnored
		ss := m.syncStatusOf(t)
		isCursor := i == m.cursor && !m.cursorHidden
		line := renderToolRowWithProviderPin(p, t, cols, spinnerView, group, providerPinForTool(t, m.toolProviderPins), fallbackConcreteForTool(t, m.toolFallbacks), m.effectiveSystemManager, m.effectivePythonManager, m.effectiveNodeManager, isIgnored, isCursor, ss, rowActionErrorStatus(m, t))
		if isCursor {
			cursorRow = len(rows)
			rows = append(rows, displayRow{text: selectedRowPrefix(p) + line, toolIdx: i})
			// Inline detail — expand the selected row with its info.
			for _, dl := range detail {
				rows = append(rows, displayRow{text: dl, toolIdx: -1})
			}
		} else {
			rows = append(rows, displayRow{text: inactiveRowPrefix() + line, toolIdx: i})
		}
	}

	// Viewport: keep the full selected block (tool row + detail lines) visible.
	// Subtract subtabLines so the pill bar doesn't crowd out tool rows.
	avail := listAvailableHeight(m) - subtabLines
	if avail < 1 {
		avail = 1
	}
	bottomOfBlock := cursorRow
	if m.cursor >= 0 {
		bottomOfBlock = cursorRow + len(detail)
	}
	start, end := scrollWindowBounds(len(rows), bottomOfBlock, avail)

	for _, r := range rows[start:end] {
		sb.WriteString(r.text)
		sb.WriteByte('\n')
	}
	return sb.String()
}

func emptyToolListText(m Model) string {
	if m.mode != viewSearch {
		return emptyToolsCTA(m)
	}
	query := strings.TrimSpace(m.filter.Value())
	if len([]rune(query)) < 2 {
		return "  search tools — type at least 2 characters"
	}
	msg := "  no search results for '" + query + "'"
	if m.providerTabIdx > 0 && m.providerTabIdx <= len(m.providerNames) {
		msg += " in " + m.providerNames[m.providerTabIdx-1]
	}
	return fitCellText(msg, screenContentWidth(m.width))
}

func emptyToolsCTA(m Model) string {
	sync := m.keys.SyncAll.Help()
	search := m.keys.Search.Help()
	return fitCellText("  no tools yet — "+sync.Key+" "+actions.MustTUILabel(actions.ToolSyncAll)+"  "+search.Key+" search", screenContentWidth(m.width))
}

// verReserveW is the column budget reserved for the version string.
// Wide enough for "1.2.3 → 4.5.6.7" plus a little breathing room.
const verReserveW = 24

// colWidths holds the pre-computed column widths for the tool list.
type colWidths struct {
	name    int // name column — widest tool name, floor 20
	priv    int // privilege marker column — 0 when no visible tools need it
	typ     int // feature-type column (skills/mcp/plugin) — agents tab only, 0 on tools
	prov    int // provider column — widest label, floor 8
	ver     int // version column — widest displayed version, capped by verReserveW
	group   int // group badge column — widest [badge], 0 when no tools have a group
	screenW int // terminal width — used to right-align the group badge
}

// newColWidths computes all column widths in a single pass over the tool list.
// screenW is the available terminal width (m.width); it expands the name column
// to fill the space left after all other fixed elements so the table uses the
// full pane width.  Short tool names remain short — rows don't pad to the edge.
// groupNames is the list of reusable group names; when non-empty the group
// column is always reserved so it does not flicker in/out as filters change.
func newColWidthsWithProviderPins(tools []*database.ToolCache, toolGroups map[string]string, groupNames []string, providerPins map[string]string, fallbacks map[string]config.FallbackSpec, systemBin, pythonBin, nodeBin string, screenW int, wrongProvider func(*database.ToolCache) bool) colWidths {
	seed := colWidths{name: 20, prov: 8, ver: len("missing"), screenW: screenW}

	// Seed group column width from all known reusable group names so
	// the column is stable regardless of which tools are currently visible.
	for _, g := range groupNames {
		if n := len([]rune(g)) + 2; n > seed.group {
			seed.group = n
		}
	}

	measure := rowColWidthMeasure{
		name: func(i int) string { return nameDisplayText(tools[i]) },
		prov: func(i int) string {
			t := tools[i]
			label := providerDisplayTextForToolWithPin(t, providerPinForTool(t, providerPins), fallbackConcreteForTool(t, fallbacks), systemBin, pythonBin, nodeBin)
			if wrongProvider != nil && wrongProvider(t) && t.InstalledWith != "" && t.InstalledWith != label {
				label = t.InstalledWith
			}
			return label
		},
		ver: func(i int) string { return displayVersionText(tools[i]) },
		group: func(i int) string {
			t := tools[i]
			if g := toolGroups[toolKey(t.Name, t.Provider)]; g != "" {
				return "[" + g + "]"
			}
			return ""
		},
		priv: func(i int) bool {
			if toolHasPrivilegeMarker(tools[i], systemBin) {
				return true
			}
			if wrongProvider != nil && wrongProvider(tools[i]) {
				return true
			}
			return providerPinForTool(tools[i], providerPins) != ""
		},
	}

	// Layout: left group [icon name], right group [provider version group].
	// The flexible space sits between the groups; individual columns stay at
	// their largest observed width so rows across tabs obey the same placement
	// rule.
	return seedWidenCapShrinkColWidths(seed, len(tools), measure)
}

func fitToolColumnsForRowErrors(cols colWidths, tools []*database.ToolCache, rowErrors map[string]string) colWidths {
	if len(rowErrors) == 0 {
		return cols
	}
	maxName := rowAvailableWidth(cols.screenW) - listIconWidth - toolIconNameGapWidth - listColumnGap - toolRightGroupWidth(cols)
	if maxName <= cols.name {
		return cols
	}
	for _, t := range tools {
		if t == nil {
			continue
		}
		rowErr := rowErrors[toolKey(t.Name, t.Provider)]
		if rowErr == "" {
			continue
		}
		rowErr = rowErrorSummary(rowErr)
		needed := lipgloss.Width(nameDisplayText(t)) + 2 + lipgloss.Width(rowErr)
		cols.name = min(max(cols.name, needed), maxName)
	}
	return cols
}

func fitToolColumnsToScreen(cols colWidths) colWidths {
	contentW := rowAvailableWidth(cols.screenW)
	totalW := listIconWidth + toolIconNameGapWidth + cols.name + listColumnGap + toolRightGroupWidth(cols)
	over := totalW - contentW
	if over <= 0 {
		return cols
	}

	shrinkWidth(&cols.group, 6, &over)
	shrinkWidth(&cols.name, 12, &over)
	shrinkWidth(&cols.ver, 8, &over)
	shrinkWidth(&cols.prov, 6, &over)
	shrinkWidth(&cols.group, 1, &over)
	shrinkWidth(&cols.ver, 1, &over)
	shrinkWidth(&cols.name, 1, &over)
	shrinkWidth(&cols.prov, 1, &over)
	return cols
}

func shrinkWidth(width *int, minWidth int, over *int) {
	if width == nil || over == nil || *over <= 0 || *width <= minWidth {
		return
	}
	delta := min(*width-minWidth, *over)
	*width -= delta
	*over -= delta
}

func displayVersionText(t *database.ToolCache) string {
	switch {
	case t.Installed && t.Outdated && t.LatestVersion.Valid:
		return compactVersion(t.Version.String) + " → " + compactVersion(t.LatestVersion.String)
	case t.Installed && t.Version.Valid:
		return compactVersion(t.Version.String)
	case !t.Installed:
		return "missing"
	default:
		return ""
	}
}

func renderToolRowWithProviderPin(p palette, t *database.ToolCache, cols colWidths, spinnerView, group, providerPin, fallbackConcrete, systemBin, pythonBin, nodeBin string, ignored, selected bool, ss syncStatus, rowErrValues ...string) string {
	label := providerLabelForToolWithPin(t, providerPin, fallbackConcrete, systemBin, pythonBin, nodeBin)
	privileged := toolHasPrivilegeMarker(t, systemBin)
	provSystemBin, provPythonBin, provNodeBin := systemBin, pythonBin, nodeBin
	if t.Installed && t.InstalledWith == "" && providerPin == "" && fallbackConcrete == "" {
		provSystemBin, provPythonBin, provNodeBin = "", "", ""
	}
	rowErr := ""
	if len(rowErrValues) > 0 {
		rowErr = rowErrorSummary(rowErrValues[0])
	}
	emphasis := func(s lipgloss.Style) lipgloss.Style {
		return rowEmphasis(selected, s)
	}
	wrongProv := ss == syncWrongProv
	showMarker := wrongProv || providerPin != ""

	iconGap := strings.Repeat(" ", toolIconNameGapWidth)
	groupCell := func(s lipgloss.Style) []rowCell {
		if !t.Tracked {
			return rowGroupBadgeCell(s, "", cols.group)
		}
		badge := ""
		if group != "" {
			badge = "[" + group + "]"
		}
		return rowGroupBadgeCell(s, badge, cols.group)
	}
	split := func(left, right []rowCell) string {
		return renderSplitRow(left, right, rowAvailableWidth(cols.screenW), listColumnGap, listColumnGap)
	}

	if ignored {
		ignoredStyle := emphasis(p.styleIgnored)
		icon := ignoredStyle.Render(iconIgnored)
		name := renderNameCell(p, ignoredStyle, t, "", cols.name, selected)
		mark := ""
		if privileged {
			mark = iconPrivileged
		} else if showMarker {
			mark = providerWrongGlyph
		}
		priv := renderPrivilegeCol(mark, cols.priv, ignoredStyle)
		provText := fitCellText(label, cols.prov)
		provPadding := strings.Repeat(" ", max(0, cols.prov-lipgloss.Width(provText)))
		prov := ignoredStyle.Render(provText) + provPadding
		var ver string
		switch {
		case t.Installed && t.Outdated && t.LatestVersion.Valid:
			current, latest := fitUpgradeVersionText(compactVersion(t.Version.String), compactVersion(t.LatestVersion.String), cols.ver)
			ver = ignoredStyle.Render(current) + emphasis(p.styleOutdated).Render(latest)
		case t.Installed && t.Version.Valid:
			ver = ignoredStyle.Render(fitCellText(compactVersion(t.Version.String), cols.ver))
		default:
			ver = ignoredStyle.Render("ignored")
		}
		left := []rowCell{leftCell(icon+iconGap+name, 0)}
		right := privilegeProviderCells(priv, cols.priv, prov, cols.prov, toolPrivilegeProviderGap)
		right = append(right, rightCell(ver, cols.ver))
		right = append(right, groupCell(ignoredStyle)...)
		return split(left, right)
	}

	var icon string
	if spinnerView != "" {
		icon = spinnerView
	} else if rowErr != "" {
		icon = emphasis(p.styleErr).Render(iconFailed)
	} else {
		switch {
		case ss == syncOrphan:
			icon = emphasis(p.styleOrphan).Render(iconOrphan)
		case ss == syncWrongProv, ss == syncNvmManaged:
			icon = emphasis(p.styleWrongProv).Render(iconWrongProv)
		case t.Installed && t.Outdated:
			icon = emphasis(p.styleOutdated).Render(iconOutdated)
		case t.Installed:
			icon = emphasis(p.styleInstalled).Render(iconInstalled)
		default: // syncMissing or syncOK-but-not-installed
			icon = emphasis(p.styleMissing).Render(iconMissing)
		}
	}

	nameStyle := p.styleNormal
	if selected {
		nameStyle = nameStyle.Bold(true)
	}
	name := renderNameCell(p, nameStyle, t, rowErr, cols.name, selected)
	mark := ""
	if privileged {
		mark = iconPrivileged
	} else if showMarker {
		mark = providerWrongGlyph
	}
	markStyle := emphasis(p.styleHelp)
	if showMarker {
		markStyle = emphasis(p.styleWrongProv)
	}
	priv := renderPrivilegeCol(mark, cols.priv, markStyle)
	displayInstalledWith := t.InstalledWith
	displayProvider := providerForFallbackDisplay(t.Provider, fallbackConcrete)
	if fallbackConcrete != "" {
		displayInstalledWith = fallbackConcrete
	}
	plainLabel := label
	prov := renderProviderColWithExplicit(p, displayProvider, displayInstalledWith, providerPin, provSystemBin, provPythonBin, provNodeBin, plainLabel, cols.prov, selected, showMarker)

	var ver string
	switch {
	case t.Installed && t.Outdated && t.LatestVersion.Valid:
		// Current version styled same as missing (red) — it needs updating.
		current, latest := fitUpgradeVersionText(compactVersion(t.Version.String), compactVersion(t.LatestVersion.String), cols.ver)
		ver = emphasis(p.styleMissing).Render(current) + emphasis(p.styleOutdated).Render(latest)
	case t.Installed && t.Version.Valid:
		ver = emphasis(p.styleVersionMuted).Render(fitCellText(compactVersion(t.Version.String), cols.ver))
	case !t.Installed:
		ver = emphasis(p.styleMissing).Render("missing")
	}

	left := []rowCell{leftCell(icon+iconGap+name, 0)}
	right := privilegeProviderCells(priv, cols.priv, prov, cols.prov, toolPrivilegeProviderGap)
	right = append(right, rightCell(ver, cols.ver))
	right = append(right, groupCell(emphasis(p.styleHelp))...)
	return split(left, right)
}

func renderNameCell(p palette, nameStyle lipgloss.Style, t *database.ToolCache, rowErr string, width int, selected bool) string {
	if rowErr == "" {
		return renderNameWithPackage(p, nameStyle, t, width, selected)
	}
	errStyle := p.styleErr
	if selected {
		errStyle = errStyle.Bold(true)
	}
	if lipgloss.Width(nameDisplayText(t))+2+lipgloss.Width(rowErr) > width {
		errW := lipgloss.Width(rowErr)
		nameW := lipgloss.Width(nameDisplayText(t))
		if nameW+2+errW > width {
			errW = min(errW, max(width-nameW-2, width/2))
			nameW = max(width-errW-2, 1)
		}
		err := fitCellText(rowErr, errW)
		name := renderNameWithPackage(p, nameStyle, t, nameW, selected)
		return name + strings.Repeat(" ", 2) + errStyle.Render(err)
	}
	nameWidth := lipgloss.Width(nameDisplayText(t))
	errWidth := width - nameWidth - 2
	if errWidth <= 0 {
		return nameStyle.Render(fitCellText(nameDisplayText(t), width))
	}
	err := fitCellText(rowErr, errWidth)
	cellWidth := nameWidth + 2 + lipgloss.Width(err)
	return renderNameWithPackage(p, nameStyle, t, nameWidth, selected) + strings.Repeat(" ", 2) + errStyle.Render(err) + strings.Repeat(" ", max(width-cellWidth, 0))
}

func renderNameWithPackage(p palette, nameStyle lipgloss.Style, t *database.ToolCache, width int, selected bool) string {
	plain := nameDisplayText(t)
	if lipgloss.Width(plain) > width {
		return nameStyle.Render(fitCellText(plain, width))
	}
	rendered := nameStyle.Render(t.Name)
	if alias := packageAlias(t); alias != "" {
		aliasStyle := p.styleHelp
		if selected {
			aliasStyle = aliasStyle.Bold(true)
		}
		rendered += aliasStyle.Render(" {" + alias + "}")
	}
	if t.UpdateBlocked == app.UpdateBlockSelfUpdates {
		selfStyle := p.styleVersionMuted
		if selected {
			selfStyle = selfStyle.Bold(true)
		}
		rendered += selfStyle.Render(" (self)")
	}
	return rendered + strings.Repeat(" ", max(width-lipgloss.Width(plain), 0))
}

func nameDisplayText(t *database.ToolCache) string {
	suffix := ""
	if t.UpdateBlocked == app.UpdateBlockSelfUpdates {
		suffix = " (self)"
	}
	if alias := packageAlias(t); alias != "" {
		return t.Name + " {" + alias + "}" + suffix
	}
	return t.Name + suffix
}

func packageAlias(t *database.ToolCache) string {
	if t == nil || t.Package == "" || t.Package == t.Name {
		return ""
	}
	return t.Package
}

func toolHasPrivilegeMarker(t *database.ToolCache, systemBin string) bool {
	return app.ToolHasPrivilegeMarker(t, app.ToolClassificationContext{EffectiveSystemManager: systemBin})
}

func compactVersion(version string) string {
	if head, _, ok := strings.Cut(version, ","); ok {
		return strings.TrimSpace(head)
	}
	return version
}

func fitUpgradeVersionText(current, latest string, width int) (string, string) {
	if width <= 0 {
		width = verReserveW
	}
	combined := current + " → " + latest
	if lipgloss.Width(combined) <= width {
		return current, " → " + latest
	}
	arrowW := lipgloss.Width(" → ")
	latestWidth := width - lipgloss.Width(current) - arrowW
	if latestWidth <= 0 {
		if width <= arrowW {
			return fitCellText(combined, width), ""
		}
		currentWidth := width - arrowW - 1
		if currentWidth < 1 {
			currentWidth = 1
		}
		fittedCurrent := fitCellText(current, currentWidth)
		remaining := width - lipgloss.Width(fittedCurrent) - arrowW
		if remaining <= 0 {
			remaining = 1
		}
		return fittedCurrent, " → " + fitCellText(latest, remaining)
	}
	return current, " → " + fitCellText(latest, latestWidth)
}

func fitCellText(s string, width int) string {
	if width <= 0 || lipgloss.Width(s) <= width {
		return s
	}
	if width <= 1 {
		return "…"
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		w := lipgloss.Width(string(r))
		if used+w > width-1 {
			break
		}
		b.WriteRune(r)
		used += w
	}
	return b.String() + "…"
}

func rowErrorSummary(s string) string {
	return strings.Join(strings.Fields(stripANSIEscapeSequences(s)), " ")
}

func stripANSIEscapeSequences(s string) string {
	if !strings.ContainsRune(s, '\x1b') {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '\x1b' {
			b.WriteByte(s[i])
			i++
			continue
		}
		i++
		if i >= len(s) {
			break
		}
		if s[i] != '[' {
			continue
		}
		i++
		for i < len(s) {
			c := s[i]
			i++
			if c >= 0x40 && c <= 0x7e {
				break
			}
		}
	}
	return b.String()
}

func renderPrivilegeCol(mark string, colW int, style lipgloss.Style) string {
	if colW <= 0 {
		return ""
	}
	if mark == "" {
		return strings.Repeat(" ", colW)
	}
	return style.Render(fitCellText(mark, colW))
}

func privilegeProviderCells(priv string, privW int, provider string, providerW int, gap int) []rowCell {
	if privW <= 0 {
		return []rowCell{rightCell(provider, providerW)}
	}
	if gap < 0 {
		gap = 0
	}
	combined := renderCell(rightCell(priv, privW)) +
		strings.Repeat(" ", gap) +
		renderCell(rightCell(provider, providerW))
	return []rowCell{rightCell(combined, privW+gap+providerW)}
}

// renderProviderCol renders the provider column with per-part styling.
// Meta part (system/python/node) uses normal text colour for all families.
// Concrete part uses the resolved manager label.
// The wrong-provider glyph (if any) is placed in the priv/mark column before
// the provider name (just like the lock icon) so names align vertically.
// plainLabel is the clean label.
func renderProviderColWithExplicit(p palette, raw, installedWith, explicitWith, systemBin, pythonBin, nodeBin, plainLabel string, colW int, selected, wrongProvider bool) string {
	meta, concrete, _ := providerPartsWithExplicit(raw, installedWith, explicitWith, systemBin, pythonBin, nodeBin)

	// Trailing space padding based on the unstyled label width so all columns align.
	labelW := colW
	plainW := lipgloss.Width(plainLabel)
	padding := strings.Repeat(" ", max(0, colW-plainW))

	// Show the concrete package manager when resolved; fall back to the ecosystem
	// name (muted) only for unresolved tools. Wrong-provider concrete tools
	// (brew route, bun install) surface the actual install source.
	label := concrete
	style := providerMetaStyle(p, meta)
	if concrete == "" {
		label = meta
	}
	if wrongProvider && installedWith != "" && installedWith != label {
		label = installedWith
	}
	if selected {
		style = style.Bold(true)
	}
	if plainW > colW {
		fitted := fitCellText(label, labelW)
		return style.Render(fitted) + padding
	}
	return style.Render(label) + padding
}

func providerMetaStyle(p palette, meta string) lipgloss.Style {
	switch app.ToolProviderDisplayRoleFor(meta) {
	case app.ToolProviderDisplayRoleSystem:
		return p.styleProviderSystem
	case app.ToolProviderDisplayRoleNode:
		return p.styleProvider
	case app.ToolProviderDisplayRolePython:
		return p.styleProviderLinux
	default:
		return p.styleNormal
	}
}

// providerPartsWithExplicit splits a raw provider string into a meta label,
// the concrete backend, and whether the backend is an explicit per-tool
// override.
//
// Ecosystem providers resolve concrete from installedWith, explicit pins, or
// effective managers. Explicit pins plus concrete providers/managers render as
// overrides.
func providerPartsWithExplicit(raw, installedWith, explicitWith, systemBin, pythonBin, nodeBin string) (meta, concrete string, isOverride bool) {
	parts := app.ToolProviderDisplayParts(app.ToolProviderDisplayInput{
		Provider:               raw,
		InstalledWith:          installedWith,
		ExplicitProvider:       explicitWith,
		EffectiveSystemManager: systemBin,
		EffectivePythonManager: pythonBin,
		EffectiveNodeManager:   nodeBin,
	})
	return parts.Meta, parts.Concrete, parts.Override
}

func providerLabelForToolWithPin(t *database.ToolCache, providerPin, fallbackConcrete, systemBin, pythonBin, nodeBin string) string {
	if t == nil {
		return ""
	}
	providerName := providerForFallbackDisplay(t.Provider, fallbackConcrete)
	installedWith := t.InstalledWith
	if fallbackConcrete != "" {
		installedWith = fallbackConcrete
	}
	label := app.ToolProviderDisplayLabel(app.ToolProviderDisplayInput{
		Provider:               providerName,
		InstalledWith:          installedWith,
		ExplicitProvider:       providerPin,
		EffectiveSystemManager: systemBin,
		EffectivePythonManager: pythonBin,
		EffectiveNodeManager:   nodeBin,
	})
	if providerPin == "" && installedWith != "" && installedWith != label && installedWith != providerName &&
		!provider.BuiltinIsEcosystem(providerName) {
		return installedWith
	}
	return label
}

func providerForFallbackDisplay(providerName, fallbackConcrete string) string {
	if fallbackConcrete == "" {
		return providerName
	}
	switch role := app.ToolProviderDisplayRoleFor(providerName); role {
	case app.ToolProviderDisplayRoleSystem, app.ToolProviderDisplayRoleNode, app.ToolProviderDisplayRolePython:
		return string(role)
	default:
		return providerName
	}
}

func providerDisplayTextForToolWithPin(t *database.ToolCache, providerPin, fallbackConcrete, systemBin, pythonBin, nodeBin string) string {
	return providerLabelForToolWithPin(t, providerPin, fallbackConcrete, systemBin, pythonBin, nodeBin)
}

func providerPinForTool(t *database.ToolCache, providerPins map[string]string) string {
	if t == nil || providerPins == nil {
		return ""
	}
	return providerPins[t.Name]
}

// inlineDetailLines returns the lines to insert directly below the selected
// tool row. The description is word-wrapped up to just before the right-side
// provider metadata and indented to align with the tool name (not the icon).
// Returns nil when nothing is selected.
func inlineDetailLines(m Model, width int, cols colWidths) []string {
	p := m.palette
	t := m.selectedTool()
	if t == nil {
		return nil
	}

	// Indent starts just after the name column start so selected-row details
	// read as secondary content instead of another table row.
	prefix := listTextPrefix()
	hintPrefix := listHintPrefix()
	prefixW := lipgloss.Width(prefix)
	wrapWidth := toolDetailWrapWidth(width, cols, prefixW)

	var lines []string

	// Description — word-wrapped to terminal width.
	if t.Description.Valid && t.Description.String != "" {
		for _, dl := range text.WrapText(t.Description.String, wrapWidth) {
			lines = append(lines, prefix+p.styleHelp.Render(dl))
		}
	} else {
		lines = append(lines, prefix+p.styleNoDesc.Render("no description available"))
	}

	if line := fullVersionDetailLine(p, t, prefix); line != "" {
		lines = append(lines, line)
	}

	if line := ignoreDetailLine(m, t, prefix); line != "" {
		lines = append(lines, line)
	}

	if line := nvmManagedDetailLine(m, t, prefix); line != "" {
		lines = append(lines, line)
	}
	if line := providerMismatchDetailLine(m, t, prefix); line != "" {
		lines = append(lines, line)
	}

	lines = append(lines, providerCandidateDetailLines(m, t, prefix, wrapWidth)...)

	lines = append(lines, rowActionErrorAdviceLines(m, t, prefix, wrapWidth)...)

	if line := listConfirmationHintsLine(m, t, hintPrefix); line != "" {
		lines = append(lines, line)
	} else if line := rowOperationStatusLine(m, t, prefix); line != "" {
		lines = append(lines, line)
	} else if line := renderInlineHints(p, toolInlineHints(m, t), hintPrefix); line != "" {
		lines = append(lines, line)
	}

	return lines
}

func toolDetailWrapWidth(width int, cols colWidths, prefixW int) int {
	rightStart := rowMarkerWidth + rowAvailableWidth(width) - toolRightGroupWidth(cols)
	wrapWidth := rightStart - listColumnGap - prefixW
	wrapWidth = max(wrapWidth, 20)
	return min(wrapWidth, max(width-prefixW, 1))
}

func toolRightGroupWidth(cols colWidths) int {
	width := cols.prov + listColumnGap + cols.ver
	if cols.priv > 0 {
		width += cols.priv + toolPrivilegeProviderGap
	}
	if cols.typ > 0 {
		width += cols.typ + listColumnGap
	}
	if cols.group > 0 {
		width += listColumnGap + cols.group
	}
	return width
}

func ignoreDetailLine(m Model, t *database.ToolCache, prefix string) string {
	if t == nil {
		return ""
	}
	label := m.ignoreLabels[t.Name]
	if label == "" && m.ignoreSet[t.Name] {
		label = "host"
	}
	if label == "" {
		return ""
	}
	return prefix + m.palette.styleHelp.Render("ignored by ") + m.palette.styleIgnored.Render(label)
}

func nvmManagedDetailLine(m Model, t *database.ToolCache, prefix string) string {
	if t == nil || m.syncStatusOf(t) != syncNvmManaged {
		return ""
	}
	p := m.palette
	prov := t.Provider
	if t.Name == "node" {
		return prefix +
			p.styleWrongProv.Render("nvm-managed runtime") +
			p.styleHelp.Render(": configured for ") +
			p.styleStatus.Render(prov) +
			p.styleHelp.Render(" but active binary is under nvm — press ") +
			p.styleStatus.Render("r") +
			p.styleHelp.Render(" to stop managing node through omni")
	}
	mgr := m.effectiveNodeManagerLabel()
	return prefix +
		p.styleWrongProv.Render("nvm-managed") +
		p.styleHelp.Render(": configured for ") +
		p.styleStatus.Render(prov) +
		p.styleHelp.Render(" but resolves via nvm — press ") +
		p.styleStatus.Render("r") +
		p.styleHelp.Render(" to move to ") +
		p.styleStatus.Render(mgr)
}

func providerMismatchDetailLine(m Model, t *database.ToolCache, prefix string) string {
	if t == nil || m.syncStatusOf(t) != syncWrongProv || t.InstalledWith == "" {
		return ""
	}
	desired, source := app.ExpectedConcreteProviderForTool(t, toolClassificationContext(m, t))
	if desired == "" || desired == t.InstalledWith {
		return ""
	}
	p := m.palette
	return prefix +
		p.styleWrongProv.Render("wrong provider") +
		p.styleHelp.Render(": installed with ") +
		p.styleStatus.Render(t.InstalledWith) +
		p.styleHelp.Render(", expected "+source+" ") +
		p.styleStatus.Render(desired)
}

func providerCandidateDetailLines(m Model, t *database.ToolCache, prefix string, wrapWidth int) []string {
	candidates := providerCandidateOptions(m, t)
	if len(candidates) == 0 {
		return nil
	}
	p := m.palette
	lines := []string{prefix + p.styleHelp.Render("available providers:")}
	selected := clampIndex(m.providerCandidateCursor, len(candidates))
	for i, candidate := range candidates {
		provider := strings.TrimSpace(candidate.Provider)
		pkg := strings.TrimSpace(candidate.EffectivePackage(t.Name))
		if pkg == "" {
			pkg = t.Name
		}
		marker := "  "
		style := p.styleHelp
		if i == selected {
			marker = "› "
			style = p.styleStatus
		}
		for _, line := range text.WrapText(marker+provider+"/"+pkg, wrapWidth) {
			lines = append(lines, prefix+style.Render(line))
		}
	}
	return lines
}

func rowActionErrorAdviceLines(m Model, t *database.ToolCache, prefix string, wrapWidth int) []string {
	if t == nil || len(m.rowActionErrors) == 0 {
		return nil
	}
	actionErr := m.rowActionErrors[toolKey(t.Name, t.Provider)]
	if actionErr == nil || len(actionErr.Solutions) == 0 {
		return nil
	}
	p := m.palette
	applicableIdx := app.FirstApplicableProviderSolutionIndex(actionErr)
	var lines []string
	for i, solution := range actionErr.Solutions {
		if i >= 2 {
			break
		}
		label := strings.TrimSpace(solution.Label)
		command := strings.TrimSpace(solution.Command)
		proposal := label
		if proposal == "" {
			proposal = command
		}
		if proposal != "" {
			line := p.styleHelp.Render("proposal: ") + p.styleStatus.Render(proposal)
			if i == applicableIdx {
				line = hintJoin(p, line, renderActionHintText(p, []hintItem{hintFromBinding(m.keys.ApplySolution)}))
			} else if command != "" && command != proposal {
				line = hintJoin(p, line, p.styleStatus.Render(command))
			}
			lines = append(lines, prefix+line)
		}
		for _, detail := range text.WrapText(strings.TrimSpace(solution.Detail), wrapWidth) {
			if detail != "" {
				lines = append(lines, prefix+p.styleHelp.Render(detail))
			}
		}
	}
	return lines
}

func rowActionErrorStatus(m Model, t *database.ToolCache) string {
	if t == nil || len(m.rowErrors) == 0 {
		return ""
	}
	return m.rowErrors[toolKey(t.Name, t.Provider)]
}

func rowOperationStatusLine(m Model, t *database.ToolCache, prefix string) string {
	if t == nil || m.rowOpKey == "" || m.rowOpStatus == "" {
		return ""
	}
	if m.rowOpKey != toolKey(t.Name, t.Provider) {
		return ""
	}
	status := m.palette.styleStatus.Render(m.rowOpStatus)
	cancel := renderActionHintText(m.palette, []hintItem{rawHint("ctrl+c", "cancel")})
	return prefix + hintJoin(m.palette, status, cancel)
}

func listConfirmationHintsLine(m Model, t *database.ToolCache, prefix string) string {
	c := m.listConfirm
	if c.action == "" {
		return ""
	}
	if c.action == listConfirmSyncAll {
		return ""
	}
	if t == nil || c.name != t.Name || c.provider != t.Provider {
		return ""
	}
	switch c.action {
	case listConfirmDelete:
		return renderConfirmActionHints(m, prefix, m.keys.Delete, actions.MustTUIConfirmDescription(actions.ToolDelete))
	case listConfirmReinstallDefault:
		confirm := actions.MustTUIConfirmDescription(actions.ToolReinstallDefault)
		return renderConfirmActionHints(m, prefix, m.keys.Install, confirm)
	case listConfirmMigrateNvm:
		confirm := "move off " + c.provider + " to " + m.effectiveNodeManagerLabel()
		return renderConfirmActionHints(m, prefix, m.keys.MigrateProvider, confirm)
	case listConfirmRemoveNvmRuntime:
		return renderConfirmActionHints(m, prefix, m.keys.MigrateProvider, "remove node from omni config (nvm owns runtime)")
	case listConfirmClearProviderOverride:
		return renderConfirmActionHints(m, prefix, m.keys.PinProvider, "remove provider override and reinstall with default")
	default:
		return ""
	}
}

func fullVersionDetailLine(p palette, t *database.ToolCache, prefix string) string {
	if !t.Installed || !t.Version.Valid {
		return ""
	}
	if t.Outdated && t.LatestVersion.Valid {
		if compactVersion(t.Version.String) == t.Version.String && compactVersion(t.LatestVersion.String) == t.LatestVersion.String {
			return ""
		}
		return prefix + p.styleHelp.Render("version ") + p.styleVersionMuted.Render(t.Version.String) + p.styleHelp.Render(" → ") + p.styleOutdated.Render(t.LatestVersion.String)
	}
	if compactVersion(t.Version.String) == t.Version.String {
		return ""
	}
	return prefix + p.styleHelp.Render("version ") + p.styleVersionMuted.Render(t.Version.String)
}

// wrapText wraps text to width runes. Kept for test compatibility; delegates to
// text.WrapText but returns []string{""} for empty input to preserve old behaviour.
func wrapText(s string, width int) []string {
	result := text.WrapText(s, width)
	if result == nil {
		return []string{""}
	}
	return result
}

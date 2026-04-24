package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/actions"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/text"
)

// renderFilterBar renders the provider and group filter pill bars.
// Group pills are shown when the config has non-base groups (always present so
// the user can activate the filter without visiting the Profiles tab).
// Provider pills are ecosystem providers from the provider registry, not a
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
		if hasProvPills && remaining > lipgloss.Width("   ·   ") {
			sb.WriteString(p.styleHelp.Render("   ·   "))
			remaining -= lipgloss.Width("   ·   ")
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
		sepW := lipgloss.Width("   ·   ")
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

// providerEcosystem returns the ecosystem name for a raw provider value.
func providerEcosystem(raw string) string {
	if ecosystem, ok := provider.BuiltinEcosystemFor(raw); ok {
		return ecosystem
	}
	return raw
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
		if len(m.scanningProviders) > 0 || m.loading {
			return sb.String()
		} else {
			sb.WriteString(p.styleHelp.Render("  no tools — run 'omni sync' or 'omni add'"))
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
	cols := newColWidths(m.visibleTools, m.toolGroups, visibleGroupNames(m), m.effectiveSystemManager, m.effectivePythonManager, m.effectiveNodeManager, m.width)
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
				spinnerView = m.spinner.View()
			}
		}
		if spinnerView == "" && m.bulkPendingKeys[key] {
			spinnerView = p.styleStatus.Render(iconPending)
		}
		if m.rowOpKey == key {
			spinnerView = m.spinner.View()
		}
		group := m.toolGroups[key]
		isIgnored := sec == sectionIgnored
		ss := m.syncStatusOf(t)
		line := renderToolRow(p, t, cols, spinnerView, group, m.effectiveSystemManager, m.effectivePythonManager, m.effectiveNodeManager, isIgnored, i == m.cursor, ss, rowActionErrorStatus(m, t))
		if i == m.cursor {
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
	start := bottomOfBlock - avail + 1
	if start < 0 {
		start = 0
	}
	end := start + avail
	if end > len(rows) {
		end = len(rows)
		start = max(0, end-avail)
	}

	for _, r := range rows[start:end] {
		sb.WriteString(r.text)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// verReserveW is the column budget reserved for the version string.
// Wide enough for "1.2.3 → 4.5.6.7" plus a little breathing room.
const verReserveW = 24

// colWidths holds the pre-computed column widths for the tool list.
type colWidths struct {
	name    int // name column — widest tool name, floor 20
	prov    int // provider column — widest label, floor 8
	ver     int // version column — widest displayed version, capped by verReserveW
	group   int // group badge column — widest [badge], 0 when no tools have a group
	screenW int // terminal width — used to right-align the group badge
}

// newColWidths computes all column widths in a single pass over the tool list.
// screenW is the available terminal width (m.width); it expands the name column
// to fill the space left after all other fixed elements so the table uses the
// full pane width.  Short tool names remain short — rows don't pad to the edge.
// groupNames is the list of non-base group names; when non-empty the group column
// is always reserved so it does not flicker in/out as filters change.
func newColWidths(tools []*database.ToolCache, toolGroups map[string]string, groupNames []string, systemBin, pythonBin, nodeBin string, screenW int) colWidths {
	cols := colWidths{name: 20, prov: 8, ver: len("missing"), screenW: screenW}

	// Seed group column width from all known group names (including "base") so
	// the column is stable regardless of which tools are currently visible.
	if len(groupNames) > 0 {
		const baseW = len("[base]")
		if baseW > cols.group {
			cols.group = baseW
		}
		for _, g := range groupNames {
			if n := len([]rune(g)) + 2; n > cols.group {
				cols.group = n
			}
		}
	}

	for _, t := range tools {
		if n := lipgloss.Width(nameDisplayText(t)); n > cols.name {
			cols.name = n
		}
		if n := len([]rune(providerLabelForTool(t, systemBin, pythonBin, nodeBin))); n > cols.prov {
			cols.prov = n
		}
		if n := len([]rune(displayVersionText(t))); n > cols.ver {
			cols.ver = n
		}
		if g := toolGroups[toolKey(t.Name, t.Provider)]; g != "" && g != "base" {
			if n := len([]rune(g)) + 2; n > cols.group { // +2 for [ and ]
				cols.group = n
			}
		}
	}

	// Layout: left group [icon name], right group [provider version group].
	// The flexible space sits between the groups; individual columns stay at
	// their largest observed width so rows across tabs obey the same placement
	// rule.
	cols.ver = min(cols.ver, verReserveW)
	cols = fitToolColumnsToScreen(cols)

	return cols
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

func renderToolRow(p palette, t *database.ToolCache, cols colWidths, spinnerView, group, systemBin, pythonBin, nodeBin string, ignored, selected bool, ss syncStatus, rowErrValues ...string) string {
	label := providerLabelForTool(t, systemBin, pythonBin, nodeBin)
	provSystemBin, provPythonBin, provNodeBin := systemBin, pythonBin, nodeBin
	if t.Installed && t.InstalledWith == "" {
		provSystemBin, provPythonBin, provNodeBin = "", "", ""
	}
	rowErr := ""
	if len(rowErrValues) > 0 {
		rowErr = rowErrValues[0]
	}
	emphasis := func(s lipgloss.Style) lipgloss.Style {
		if selected {
			return s.Bold(true)
		}
		return s
	}

	iconGap := strings.Repeat(" ", toolIconNameGapWidth)
	groupCell := func(s lipgloss.Style) []rowCell {
		if cols.group == 0 {
			return nil
		}
		if !t.Tracked {
			return nil
		}
		badge := "[base]"
		badgeStyle := s
		if group != "" && group != "base" {
			badge = "[" + group + "]"
		}
		return []rowCell{rightCell(badgeStyle.Render(fitCellText(badge, cols.group)), cols.group)}
	}
	split := func(left, right []rowCell) string {
		return renderSplitRow(left, right, rowAvailableWidth(cols.screenW), listColumnGap, listColumnGap)
	}

	if ignored {
		ignoredStyle := emphasis(p.styleIgnored)
		icon := ignoredStyle.Render(iconIgnored)
		name := renderNameCell(p, ignoredStyle, t, "", cols.name, selected)
		prov := ignoredStyle.Render(fitCellText(label, cols.prov))
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
		right := []rowCell{
			rightCell(prov, cols.prov),
			rightCell(ver, cols.ver),
		}
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
		case ss == syncWrongProv:
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
		nameStyle = p.styleActiveText
	}
	name := renderNameCell(p, nameStyle, t, rowErr, cols.name, selected)
	prov := renderProviderCol(p, t.Provider, t.InstalledWith, provSystemBin, provPythonBin, provNodeBin, label, cols.prov, selected, ss == syncWrongProv)

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
	right := []rowCell{
		rightCell(prov, cols.prov),
		rightCell(ver, cols.ver),
	}
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
	return rendered + strings.Repeat(" ", max(width-lipgloss.Width(plain), 0))
}

func nameDisplayText(t *database.ToolCache) string {
	if alias := packageAlias(t); alias != "" {
		return t.Name + " {" + alias + "}"
	}
	return t.Name
}

func packageAlias(t *database.ToolCache) string {
	if t == nil || t.Package == "" || t.Package == t.Name {
		return ""
	}
	return t.Package
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
	latestWidth := width - lipgloss.Width(current) - lipgloss.Width(" → ")
	if latestWidth <= 0 {
		return fitCellText(combined, width), ""
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

// renderProviderCol renders the provider column with per-part styling.
// Meta part (system/python/node) uses normal text colour for all families.
// Concrete part: italic+muted when it follows the default manager setting;
// explicit per-tool overrides get a ! suffix, and wrong-provider rows are
// highlighted with the warning style.
// plainLabel must be the output of providerLabel() so the padding is correct.
func renderProviderCol(p palette, raw, installedWith, systemBin, pythonBin, nodeBin, plainLabel string, colW int, selected, wrongProvider bool) string {
	meta, concrete, isOverride := providerParts(raw, installedWith, systemBin, pythonBin, nodeBin)

	// Trailing space padding based on the unstyled label width so all columns align.
	plainW := lipgloss.Width(plainLabel)
	padding := strings.Repeat(" ", max(0, colW-plainW))

	metaStyle := p.styleNormal
	if selected {
		metaStyle = p.styleActiveText
	}

	if concrete == "" {
		return metaStyle.Render(fitCellText(meta, colW)) + padding
	}

	concreteStyle := lipgloss.NewStyle().Foreground(p.colHelp).Italic(true)
	if wrongProvider {
		concreteStyle = p.styleWrongProv.Italic(true)
	}
	if selected {
		concreteStyle = concreteStyle.Bold(true)
	}
	concreteLabel := concrete
	if isOverride {
		concreteLabel += "!"
	}

	styled := metaStyle.Render(meta+"(") + concreteStyle.Render(concreteLabel) + metaStyle.Render(")")
	if plainW > colW {
		return metaStyle.Render(fitProviderLabel(meta, concreteLabel, colW))
	}
	return styled + padding
}

func fitProviderLabel(meta, concrete string, width int) string {
	if width <= 0 {
		return ""
	}
	full := meta + "(" + concrete + ")"
	if lipgloss.Width(full) <= width {
		return full
	}
	if meta != "" {
		short := string([]rune(meta)[0]) + "(" + concrete + ")"
		if lipgloss.Width(short) <= width {
			return short
		}
	}
	if lipgloss.Width(concrete) <= width {
		return concrete
	}
	return fitCellText(full, width)
}

// providerParts splits a raw provider string into a meta label, the concrete
// backend, and whether the backend is an explicit per-tool override.
//
// Ecosystem providers -> isOverride = false; concrete is resolved from
// installedWith / effective managers and may be empty. Concrete providers and
// managers render as explicit overrides.
func providerParts(raw, installedWith, systemBin, pythonBin, nodeBin string) (meta, concrete string, isOverride bool) {
	ecosystem, ok := provider.BuiltinEcosystemFor(raw)
	if !ok {
		return raw, "", false
	}
	if !provider.BuiltinIsEcosystem(raw) {
		return ecosystem, concreteProviderLabel(ecosystem, raw), true
	}
	c := installedWith
	if c == "" || c == raw {
		switch ecosystem {
		case provider.EcosystemSystem:
			c = systemBin
		case provider.EcosystemPython:
			c = pythonBin
		case provider.EcosystemNode:
			c = nodeBin
		}
	}
	if c == raw {
		c = ""
	}
	return ecosystem, c, false
}

func concreteProviderLabel(ecosystem, raw string) string {
	if opt, ok := provider.BuiltinManagerOption(ecosystem, raw); ok && opt.SettingsValue != "" {
		return opt.SettingsValue
	}
	return raw
}

func providerLabelForTool(t *database.ToolCache, systemBin, pythonBin, nodeBin string) string {
	if t != nil && t.Installed && t.InstalledWith == "" {
		return providerLabel(t.Provider, t.InstalledWith, "", "", "")
	}
	return providerLabel(t.Provider, t.InstalledWith, systemBin, pythonBin, nodeBin)
}

// providerLabel converts a raw provider DB value to a human-readable label.
func providerLabel(raw, installedWith, systemBin, pythonBin, nodeBin string) string {
	meta, concrete, isOverride := providerParts(raw, installedWith, systemBin, pythonBin, nodeBin)
	if concrete == "" {
		return meta
	}
	if isOverride {
		concrete += "!"
	}
	return meta + "(" + concrete + ")"
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
		label = "profile"
	}
	if label == "" {
		return ""
	}
	return prefix + m.palette.styleHelp.Render("ignored by ") + m.palette.styleIgnored.Render(label)
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
	return prefix + m.palette.styleStatus.Render(m.rowOpStatus)
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
		return renderConfirmActionHints(m, prefix, m.keys.Delete, actions.MustConfirmDescription(actions.ToolDelete))
	case listConfirmReinstallDefault:
		confirm := actions.MustConfirmDescription(actions.ToolReinstallDefault)
		return renderConfirmActionHints(m, prefix, m.keys.MigrateProvider, confirm)
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

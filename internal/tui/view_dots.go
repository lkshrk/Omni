package tui

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

// dotsSortKey maps health → sort priority (lower = shown first).
func dotsSortKey(h app.DotHealth) int {
	switch h {
	case app.HealthConflict:
		return 0
	case app.HealthMissing, app.HealthNoSource:
		return 1
	default:
		return 2
	}
}

func dotsStateSortKey(state app.DotState) int {
	switch state {
	case app.DotStateConflict, app.DotStateUntrackedConflict, app.DotStateAmbiguous:
		return 0
	case app.DotStateSynced:
		return 2
	case app.DotStateIgnored, app.DotStateInactive, app.DotStateDisabled:
		return 3
	default:
		return 1
	}
}

// sortDotsEntries sorts entries in-place: conflict → missing/no-source → ok,
// then alphabetically within each bucket.
func sortDotsEntries(entries []app.DotStatus) {
	sort.SliceStable(entries, func(i, j int) bool {
		ki, kj := dotsStateSortKey(dotStatusState(entries[i])), dotsStateSortKey(dotStatusState(entries[j]))
		if ki != kj {
			return ki < kj
		}
		return entries[i].Name < entries[j].Name
	})
}

// dotsEntryNameColW returns the name-column width for the dots table.
func dotsEntryNameColW(entries []app.DotStatus) int {
	w := dotsNameMinW
	for _, e := range entries {
		if n := len([]rune(e.Name)); n > w {
			w = n
		}
	}
	return w
}

// truncatePath clips s to maxW runes, appending "…" if truncated.
func truncatePath(s string, maxW int) string {
	if len(s) <= maxW {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxW {
		return s
	}
	if maxW <= 1 {
		return "…"
	}
	return string(runes[:maxW-1]) + "…"
}

// filteredDotsEntries returns entries matching the active group and search filter.
func filteredDotsEntries(m Model) []app.DotStatus {
	q := ""
	if m.dotsSearchActive {
		q = strings.ToLower(m.filter.Value())
	}
	result := make([]app.DotStatus, 0, len(m.dotsEntries))
	for _, e := range m.dotsEntries {
		if m.dotsGroupFilter != "" && e.Group != m.dotsGroupFilter {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(e.Name), q) {
			continue
		}
		result = append(result, e)
	}
	return result
}

// dotsGroupPills returns unique groups present in entries, ordered canonically.
// Returns nil when only one group or no group info is available.
func dotsGroupPills(entries []app.DotStatus) []string {
	seen := make(map[string]bool)
	for _, e := range entries {
		if e.Group != "" {
			seen[e.Group] = true
		}
	}
	if len(seen) <= 1 {
		return nil
	}
	// Canonical order: config → home → custom → others alphabetically.
	pills := []string{""}
	for _, g := range []string{"config", "home", "custom"} {
		if seen[g] {
			pills = append(pills, g)
		}
	}
	others := make([]string, 0)
	for g := range seen {
		switch g {
		case "config", "home", "custom":
		default:
			others = append(others, g)
		}
	}
	sort.Strings(others)
	pills = append(pills, others...)
	return pills
}

type dotsSection struct {
	title   string
	entries []app.DotStatus
}

type dotsVisibleRow struct {
	entry   app.DotStatus
	child   app.DotChild
	isChild bool
}

func dotsVisibleRows(m Model) []dotsVisibleRow {
	entries := filteredDotsEntries(m)
	expanded := m.dotsExpandedName
	rows := make([]dotsVisibleRow, 0, len(entries))
	for _, entry := range entries {
		rows = append(rows, dotsVisibleRow{entry: entry})
		if entry.Name != expanded {
			continue
		}
		rows = appendDotsChildRows(m, rows, entry, entry.Children)
	}
	return rows
}

func appendDotsChildRows(m Model, rows []dotsVisibleRow, entry app.DotStatus, children []app.DotChild) []dotsVisibleRow {
	for _, child := range children {
		rows = append(rows, dotsVisibleRow{entry: entry, child: child, isChild: true})
		if dotsChildExpanded(m, entry.Name, child) {
			rows = appendDotsChildRows(m, rows, entry, child.Children)
		}
	}
	return rows
}

func dotsChildExpandKey(entryName, relPath string) string {
	return entryName + "\x00" + strings.ReplaceAll(relPath, "\\", "/")
}

func dotsChildExpanded(m Model, entryName string, child app.DotChild) bool {
	if !child.IsDir || len(child.Children) == 0 {
		return false
	}
	return m.dotsExpandedChildren[dotsChildExpandKey(entryName, child.RelPath)]
}

func dotsRowExpandable(row dotsVisibleRow) bool {
	if row.isChild {
		return row.child.IsDir && len(row.child.Children) > 0
	}
	return len(row.entry.Children) > 0
}

func dotsRowExpanded(m Model, row dotsVisibleRow) bool {
	if row.isChild {
		return dotsChildExpanded(m, row.entry.Name, row.child)
	}
	return m.dotsExpandedName == row.entry.Name
}

func dotsSections(entries []app.DotStatus) []dotsSection {
	sections := []dotsSection{
		{title: "Conflict"},
		{title: "Out Of Sync"},
		{title: "Synced"},
		{title: "Ignored"},
	}
	for _, entry := range entries {
		switch dotsSectionSortKey(entry) {
		case 0:
			sections[0].entries = append(sections[0].entries, entry)
		case 2:
			sections[2].entries = append(sections[2].entries, entry)
		case 3:
			sections[3].entries = append(sections[3].entries, entry)
		default:
			sections[1].entries = append(sections[1].entries, entry)
		}
	}
	return sections
}

func dotsSectionSortKey(status app.DotStatus) int {
	if isTransientDotCandidate(status) {
		return 1
	}
	return dotsStateSortKey(dotStatusState(status))
}

func isTransientDotCandidate(status app.DotStatus) bool {
	state := dotStatusState(status)
	if status.Group != "" {
		return false
	}
	switch state {
	case app.DotStateLocalOnly, app.DotStateRepoOnly, app.DotStateUntrackedConflict, app.DotStateUntrackedLinked, app.DotStateNoSource:
		return true
	default:
		return false
	}
}

func renderDots(m Model) string {
	p := m.palette
	var sb strings.Builder

	// ── Disabled ──────────────────────────────────────────────────────────────
	if config.BoolVal(m.settings.DotsDisabled) {
		sb.WriteString("\n")
		sb.WriteString(p.styleHelp.Render("  Dotfile sync is disabled for this machine.") + "\n\n")
		sb.WriteString(p.styleNormal.Render("  [enter] ") + p.styleHelp.Render("set up dotfiles from scratch"))
		sb.WriteString("\n")
		sb.WriteString(p.styleHelp.Render("  Or toggle Dotfile Sync in Settings to re-enable without setup.") + "\n")
		return sb.String()
	}

	// ── Not configured ────────────────────────────────────────────────────────
	if m.settings.DotsRepo == "" {
		sb.WriteString("\n")
		sb.WriteString(p.styleNormal.Render("  No dotfiles repo configured yet.") + "\n\n")
		sb.WriteString(p.styleHelp.Render("  omni manages config symlinks from a local git repo,") + "\n")
		sb.WriteString(p.styleHelp.Render("  keeping dotfiles in sync across machines.") + "\n\n")
		sb.WriteString(p.styleNormal.Render("  [enter] ") + p.styleHelp.Render("set up now"))
		sb.WriteString("\n")
		return sb.String()
	}

	// ── Empty state ───────────────────────────────────────────────────────────
	if len(m.dotsEntries) == 0 {
		sb.WriteString("\n")
		sb.WriteString(p.styleNormal.Render("  No dotfiles tracked yet.") + "\n\n")
		sb.WriteString(p.styleHelp.Render("  Add dot entries from this tab or run sync all to discover candidates."))
		sb.WriteString("\n")
		return sb.String()
	}

	// ── Scrollable content ────────────────────────────────────────────────────
	var buf scrollBuf
	write := buf.write
	sections := newListSectionWriter(p, m.width, write)
	hintPrefix := listHintPrefixWithGap(listWideIconGapWidth)

	// ── Top controls ──────────────────────────────────────────────────────────
	pills := dotsGroupPills(m.dotsEntries)
	write(renderDotsTopControls(m, pills) + "\n")

	visible := filteredDotsEntries(m)
	expandedName := m.dotsExpandedName
	cols := dotsTableColumnWidths(p, m, visible)
	contentW := rowAvailableWidth(m.width)
	cols = fitDotsColumnsToWidth(cols, contentW)
	iconNameGap := strings.Repeat(" ", dotsIconNameGapW)
	nameTargetGap := strings.Repeat(" ", dotsGapW)
	rightW := cols.status + dotsGapW + cols.ratio
	if cols.ignore > 0 {
		rightW += dotsGapW + cols.ignore
	}
	if cols.group > 0 {
		rightW += dotsGapW + cols.group
	}
	// Layout inside the row prefix:
	// icon + name + target ... status ratio [ignored] [group]
	fixedW := dotsIconW + dotsIconNameGapW + cols.name + dotsGapW + rightW + dotsGapW
	targetWidth := max(contentW-fixedW, 1)
	splitDotsRow := func(left, right string) string {
		return renderSplitRow(
			[]rowCell{leftCell(left, 0)},
			[]rowCell{rightCell(right, 0)},
			contentW,
			dotsGapW,
			dotsGapW,
		)
	}
	renderDotsRow := func(selected bool, left, right string) string {
		return listRowPrefix(p, selected) + splitDotsRow(left, right)
	}

	rowIndex := 0
	var renderChildRows func(e app.DotStatus, children []app.DotChild)
	renderChildRows = func(e app.DotStatus, children []app.DotChild) {
		for _, child := range children {
			childStatus, childStatusStyle := dotChildStatusDisplay(p, child, dotStatusState(e))
			childStatusCol := renderCell(leftCell(fitCellText(childStatus, cols.status), cols.status))
			childName := renderCell(leftCell(fitCellText(dotChildDisplayName(m, e, child), cols.name), cols.name))
			childTarget := truncatePath(tildePath(child.Path), targetWidth)
			childTargetPadded := renderCell(leftCell(childTarget, targetWidth))
			isChildCursor := rowIndex == m.dotsCursor
			childIgnoreConfirm := m.dotsIgnoreIdx == rowIndex
			childRight := dotRightColumns(p, isChildCursor || childIgnoreConfirm, childStatusCol, childStatusStyle, dotChildCounts(child, dotStatusState(e)), cols.ratio, cols.ignore, "", cols.group)
			childLeft := func(iconStyle, nameStyle, targetStyle lipgloss.Style, iconText, childName, childTarget string) string {
				return iconStyle.Render(iconText) +
					iconNameGap +
					nameStyle.Render(childName) +
					targetStyle.Render(nameTargetGap+childTarget)
			}
			if childIgnoreConfirm {
				buf.markCursor()
				left := childLeft(p.styleIgnored.Bold(true), p.styleActiveText, p.styleHelp.Bold(true), "↳", childName, childTargetPadded)
				write(renderDotsRow(true, left, childRight) + "\n")
				write(renderContextHints(m, hintCtxDotsIgnoreConfirm, hintPrefix) + "\n")
				buf.markCursorEnd()
			} else if isChildCursor {
				buf.markCursor()
				left := childLeft(p.styleHelp.Bold(true), p.styleActiveText, p.styleHelp.Bold(true), "↳", childName, childTargetPadded)
				write(renderDotsRow(true, left, childRight) + "\n")
				if hints := renderContextHints(m, hintCtxDotsRow, hintPrefix); hints != "" {
					write(hints + "\n")
					buf.markCursorEnd()
				} else {
					buf.markCursorEnd()
				}
			} else {
				left := childLeft(p.styleHelp, p.styleHelp, p.styleHelp, "↳", childName, childTargetPadded)
				write(renderDotsRow(false, left, childRight) + "\n")
			}
			rowIndex++
			if dotsChildExpanded(m, e.Name, child) {
				renderChildRows(e, child.Children)
			}
		}
	}
	for _, section := range dotsSections(visible) {
		if len(section.entries) == 0 {
			continue
		}
		sections.Header(section.title)
		for _, e := range section.entries {
			iconStyle, icon, statusLabel := dotStateDisplay(p, dotStatusState(e))
			switch {
			case m.dotsActiveName == e.Name:
				iconStyle = lipgloss.NewStyle()
				icon = m.spinner.View()
			case m.dotsPendingNames[e.Name]:
				iconStyle = p.styleStatus
				icon = iconPending
			}
			statusStyle := dotStatusTextStyle(p, dotStatusState(e))

			nameCol := renderCell(leftCell(fitCellText(dotEntryDisplayName(m, e), cols.name), cols.name))
			statusCol := renderCell(leftCell(fitCellText(statusLabel, cols.status), cols.status))
			target := truncatePath(tildePath(e.TargetPath), targetWidth)
			right := dotRightColumns(p, false, statusCol, statusStyle, dotEntryCounts(e), cols.ratio, cols.ignore, e.Group, cols.group)

			removingConfirm := m.dotsConfirmIdx == rowIndex
			repoConfirm := m.dotsOverwriteIdx == rowIndex && dotHasAction(e, app.DotActionUseRepo)
			localConfirm := m.dotsLocalIdx == rowIndex && dotHasAction(e, app.DotActionUseLocal)
			ignoreConfirm := m.dotsIgnoreIdx == rowIndex
			isCursor := rowIndex == m.dotsCursor

			if isCursor {
				buf.markCursor()
			}

			targetPadded := renderCell(leftCell(target, targetWidth))
			rowLeft := func(iconStyle, nameStyle, targetStyle lipgloss.Style) string {
				return iconStyle.Render(icon) +
					iconNameGap +
					nameStyle.Render(nameCol) +
					targetStyle.Render(nameTargetGap+targetPadded)
			}
			activeRight := dotRightColumns(p, true, statusCol, statusStyle, dotEntryCounts(e), cols.ratio, cols.ignore, e.Group, cols.group)

			switch {
			case removingConfirm:
				left := rowLeft(p.styleMissing, p.styleMissing, p.styleMissing)
				write(renderDotsRow(true, left, activeRight) + "\n")
				write(renderDotsDeleteKeepLocalPrompt(m, e.Name, hintPrefix) + "\n")
				buf.markCursorEnd()
			case repoConfirm:
				left := rowLeft(p.styleOutdated, p.styleOutdated, p.styleOutdated)
				write(renderDotsRow(true, left, activeRight) + "\n")
				write(renderContextHints(m, hintCtxDotsRepoConfirm, hintPrefix) + "\n")
				buf.markCursorEnd()
			case localConfirm:
				left := rowLeft(p.styleOutdated, p.styleOutdated, p.styleOutdated)
				write(renderDotsRow(true, left, activeRight) + "\n")
				write(renderContextHints(m, hintCtxDotsLocalConfirm, hintPrefix) + "\n")
				buf.markCursorEnd()
			case ignoreConfirm:
				left := rowLeft(p.styleIgnored, p.styleIgnored, p.styleIgnored)
				write(renderDotsRow(true, left, activeRight) + "\n")
				write(renderContextHints(m, hintCtxDotsIgnoreConfirm, hintPrefix) + "\n")
				buf.markCursorEnd()
			case isCursor:
				left := rowLeft(iconStyle.Bold(true), p.styleActiveText, p.styleHelp.Bold(true))
				write(renderDotsRow(true, left, activeRight) + "\n")
				if dotHasAction(e, app.DotActionUseRepo) || dotHasAction(e, app.DotActionUseLocal) {
					write(renderContextHints(m, hintCtxDotsConflict, hintPrefix) + "\n")
					buf.markCursorEnd()
				} else {
					if hints := renderContextHints(m, hintCtxDotsRow, hintPrefix); hints != "" {
						write(hints + "\n")
						buf.markCursorEnd()
					} else {
						buf.markCursorEnd()
					}
				}
			default:
				left := rowLeft(iconStyle, p.styleNormal, p.styleHelp)
				write(renderDotsRow(false, left, right) + "\n")
			}
			rowIndex++
			if e.Name == expandedName {
				renderChildRows(e, e.Children)
			}
		}
	}

	// ── Repo section ──────────────────────────────────────────────────────────
	sections.Header("Repo")

	if repoPath := m.settings.DotsRepo; repoPath != "" {
		var gitPart string
		if m.dotsGitStatus == "" {
			gitPart = "  " + p.styleInstalled.Render("✓ clean")
		} else {
			gitPart = "  " + p.styleOutdated.Render("✗ dirty")
		}
		repoW := max(rowAvailableWidth(m.width)-lipgloss.Width(gitPart)-2, 1)
		write(p.styleNormal.PaddingLeft(2).Render(truncatePath(tildePath(repoPath), repoW)) + gitPart + "\n")
		if m.dotsGitStatus != "" {
			for _, line := range strings.Split(m.dotsGitStatus, "\n") {
				if strings.TrimSpace(line) != "" {
					write(p.styleHelp.PaddingLeft(4).Render(line) + "\n")
				}
			}
		}
	}

	return buf.render(listAvailableHeight(m))
}

func renderDotsDeleteKeepLocalPrompt(m Model, name, prefix string) string {
	p := m.palette
	prompt := p.styleHelp.Render("delete ") +
		p.styleProvider.Bold(true).Render(name) +
		p.styleHelp.Render(", keep local? ")
	return prefix + prompt + renderActionHintText(p, contextHintItems(m, hintCtxDotsDeleteConfirm))
}

func renderDotsTopControls(m Model, pills []string) string {
	p := m.palette
	var parts []string
	if len(pills) > 0 {
		var pb strings.Builder
		pb.WriteString("  ")
		for _, g := range pills {
			label := "all"
			if g != "" {
				label = g
			}
			if g == m.dotsGroupFilter {
				pb.WriteString(p.styleTitle.Render(" " + label + " "))
			} else {
				pb.WriteString(p.styleHelp.Render(" " + label + " "))
			}
			pb.WriteString("  ")
		}
		parts = append(parts, strings.TrimRight(pb.String(), " "))
	}
	if m.dotsSearchActive {
		parts = append(parts, p.styleNormal.Render("/")+" "+renderEmptyAwareTextInputView(p, m.filter, m.filter.Placeholder, 0))
	}
	return strings.Join(parts, "   ")
}

// dotHealthDisplay returns the icon style, icon character, and status label
// for a given health value.
func dotHealthDisplay(p palette, h app.DotHealth) (lipgloss.Style, string, string) {
	switch h {
	case app.HealthOK:
		return dotSyncedStyle(p, false), "✓", "ok"
	case app.HealthMissing:
		return p.styleMissing, "✗", "missing"
	case app.HealthConflict:
		return p.styleOutdated, "!", "conflict"
	case app.HealthNoSource:
		return p.styleHelp, "·", "no-source"
	default:
		return p.styleHelp, "·", string(h)
	}
}

func dotStatusState(status app.DotStatus) app.DotState {
	if status.State != "" {
		return status.State
	}
	switch status.Health {
	case app.HealthOK:
		return app.DotStateSynced
	case app.HealthMissing:
		return app.DotStateMissing
	case app.HealthConflict:
		return app.DotStateConflict
	case app.HealthNoSource:
		return app.DotStateNoSource
	default:
		return app.DotState(status.Health)
	}
}

func dotStateDisplay(p palette, state app.DotState) (lipgloss.Style, string, string) {
	switch state {
	case app.DotStateSynced:
		return dotSyncedStyle(p, false), "✓", "synced"
	case app.DotStateModified:
		return p.styleOutdated, "!", "local changes"
	case app.DotStateConflict, app.DotStateUntrackedConflict, app.DotStateAmbiguous:
		return p.styleOutdated, "!", strings.TrimSuffix(string(state), "-conflict")
	case app.DotStateNoSource:
		return p.styleHelp, "·", "no-source"
	case app.DotStateIgnored, app.DotStateInactive, app.DotStateDisabled:
		return p.styleHelp, "·", string(state)
	default:
		return p.styleMissing, "✗", string(state)
	}
}

func dotStatusTextStyle(p palette, state app.DotState) lipgloss.Style {
	switch state {
	case app.DotStateSynced:
		return dotSyncedStyle(p, false)
	case app.DotStateModified:
		return p.styleOutdated
	case app.DotStateConflict, app.DotStateUntrackedConflict, app.DotStateAmbiguous:
		return p.styleOutdated
	case app.DotStateIgnored, app.DotStateInactive, app.DotStateDisabled:
		return p.styleIgnored
	case app.DotStateNoSource:
		return p.styleHelp
	default:
		return p.styleMissing
	}
}

func dotChildStatusDisplay(p palette, child app.DotChild, parentState app.DotState) (string, lipgloss.Style) {
	if child.Ignored {
		return "ignored", p.styleIgnored
	}
	state := parentState
	if child.State != "" {
		state = child.State
	}
	_, _, label := dotStateDisplay(p, state)
	return label, dotStatusTextStyle(p, state)
}

const (
	dotKindFileIcon            = "•"
	dotKindFolderCollapsedIcon = "▸"
	dotKindFolderExpandedIcon  = "▾"
	dotKindFolderEmptyIcon     = "▹"
)

func dotEntryDisplayName(m Model, entry app.DotStatus) string {
	return dotEntryKindIcon(m, entry) + " " + entry.Name
}

func dotChildDisplayName(m Model, entry app.DotStatus, child app.DotChild) string {
	depth := child.Depth
	if depth < 1 {
		depth = 1
	}
	return strings.Repeat("  ", depth-1) + dotChildKindIcon(m, entry, child) + " " + child.Name
}

func dotEntryKindIcon(m Model, entry app.DotStatus) string {
	if !entry.IsDir && len(entry.Children) == 0 {
		return dotKindFileIcon
	}
	if len(entry.Children) == 0 && entry.FileCount == 0 {
		return dotKindFolderEmptyIcon
	}
	if m.dotsExpandedName == entry.Name {
		return dotKindFolderExpandedIcon
	}
	return dotKindFolderCollapsedIcon
}

func dotChildKindIcon(m Model, entry app.DotStatus, child app.DotChild) string {
	if !child.IsDir {
		return dotKindFileIcon
	}
	if len(child.Children) == 0 && child.FileCount == 0 {
		return dotKindFolderEmptyIcon
	}
	if dotsChildExpanded(m, entry.Name, child) {
		return dotKindFolderExpandedIcon
	}
	return dotKindFolderCollapsedIcon
}

type dotsTableColumns struct {
	name   int
	status int
	ratio  int
	ignore int
	group  int
}

func dotsTableColumnWidths(p palette, m Model, entries []app.DotStatus) dotsTableColumns {
	cols := dotsTableColumns{
		name:   dotsNameMinW,
		status: dotsStatusColW,
		ratio:  dotsRatioColW,
	}
	for _, entry := range entries {
		state := dotStatusState(entry)
		_, _, statusLabel := dotStateDisplay(p, state)
		cols.name = max(cols.name, lipgloss.Width(dotEntryDisplayName(m, entry)))
		cols.status = max(cols.status, lipgloss.Width(statusLabel))
		counts := dotEntryCounts(entry)
		cols.ratio = max(cols.ratio, lipgloss.Width(dotRatioText(counts)))
		if ignored := dotIgnoredText(counts); ignored != "" {
			cols.ignore = max(cols.ignore, dotsIgnoredColW, lipgloss.Width(ignored))
		}
		if badge := dotGroupBadge(entry.Group); badge != "" {
			cols.group = max(cols.group, lipgloss.Width(badge))
		}
		visitDotChildren(entry.Children, func(child app.DotChild) {
			childStatus, _ := dotChildStatusDisplay(p, child, state)
			counts := dotChildCounts(child, state)
			cols.name = max(cols.name, lipgloss.Width(dotChildDisplayName(m, entry, child)))
			cols.status = max(cols.status, lipgloss.Width(childStatus))
			cols.ratio = max(cols.ratio, lipgloss.Width(dotRatioText(counts)))
			if ignored := dotIgnoredText(counts); ignored != "" {
				cols.ignore = max(cols.ignore, dotsIgnoredColW, lipgloss.Width(ignored))
			}
		})
	}
	return cols
}

func visitDotChildren(children []app.DotChild, fn func(app.DotChild)) {
	for _, child := range children {
		fn(child)
		visitDotChildren(child.Children, fn)
	}
}

func fitDotsColumnsToWidth(cols dotsTableColumns, contentW int) dotsTableColumns {
	rightW := func(c dotsTableColumns) int {
		width := c.status + dotsGapW + c.ratio
		if c.ignore > 0 {
			width += dotsGapW + c.ignore
		}
		if c.group > 0 {
			width += dotsGapW + c.group
		}
		return width
	}
	totalW := dotsIconW + dotsIconNameGapW + cols.name + dotsGapW + 1 + dotsGapW + rightW(cols)
	over := totalW - max(contentW, 1)
	if over <= 0 {
		return cols
	}
	shrinkWidth(&cols.group, 6, &over)
	shrinkWidth(&cols.name, 8, &over)
	shrinkWidth(&cols.ratio, 4, &over)
	shrinkWidth(&cols.ignore, 3, &over)
	shrinkWidth(&cols.status, 6, &over)
	shrinkWidth(&cols.group, 1, &over)
	shrinkWidth(&cols.ratio, 1, &over)
	shrinkWidth(&cols.ignore, 1, &over)
	shrinkWidth(&cols.status, 1, &over)
	shrinkWidth(&cols.name, 1, &over)
	return cols
}

func dotRightColumns(p palette, selected bool, status string, statusStyle lipgloss.Style, counts app.DotFileCounts, ratioW, ignoredW int, group string, groupW int) string {
	style := p.styleHelp
	if selected {
		style = style.Bold(true)
		statusStyle = statusStyle.Bold(true)
	}
	right := statusStyle.Render(status) + strings.Repeat(" ", dotsGapW) + dotRatioView(p, selected, counts, ratioW)
	if ignoredW > 0 {
		right += strings.Repeat(" ", dotsGapW) + dotIgnoredView(p, selected, counts, ignoredW)
	}
	if groupW == 0 {
		return right
	}
	groupCol := renderCell(rightCell(fitCellText(dotGroupBadge(group), groupW), groupW))
	return right + strings.Repeat(" ", dotsGapW) + style.Render(groupCol)
}

func dotGroupBadge(group string) string {
	if group == "" {
		return ""
	}
	return "[" + group + "]"
}

func dotEntryCounts(entry app.DotStatus) app.DotFileCounts {
	if entry.Counts.Total() > 0 || entry.FileCount <= 0 {
		return entry.Counts
	}
	if dotStatusState(entry) == app.DotStateSynced {
		return app.DotFileCounts{Synced: entry.FileCount}
	}
	return app.DotFileCounts{OutOfSync: entry.FileCount}
}

func dotChildCounts(child app.DotChild, parentState app.DotState) app.DotFileCounts {
	if child.Counts.Total() > 0 || child.FileCount <= 0 {
		return child.Counts
	}
	state := parentState
	if child.State != "" {
		state = child.State
	}
	if state == app.DotStateSynced {
		return app.DotFileCounts{Synced: child.FileCount}
	}
	return app.DotFileCounts{OutOfSync: child.FileCount}
}

func dotRatioText(counts app.DotFileCounts) string {
	managed := counts.Managed()
	if managed == 0 {
		return "-"
	}
	return fmt.Sprintf("%d/%d", counts.Synced, managed)
}

func dotIgnoredText(counts app.DotFileCounts) string {
	if counts.Ignored <= 0 {
		return ""
	}
	return fmt.Sprintf("(%d)", counts.Ignored)
}

func dotRatioView(p palette, selected bool, counts app.DotFileCounts, width int) string {
	text := dotRatioText(counts)
	if width <= 0 {
		return dotRatioStyle(p, selected, counts).Render(text)
	}
	if lipgloss.Width(text) > width {
		return renderCell(rightCell(dotRatioStyle(p, selected, counts).Render(fitCellText(text, width)), width))
	}
	return renderCell(rightCell(dotRatioStyle(p, selected, counts).Render(text), width))
}

func dotIgnoredView(p palette, selected bool, counts app.DotFileCounts, width int) string {
	text := dotIgnoredText(counts)
	style := p.styleIgnored
	if selected {
		style = style.Bold(true)
	}
	if width <= 0 {
		return style.Render(text)
	}
	if lipgloss.Width(text) > width {
		text = fitCellText(text, width)
	}
	return renderCell(rightCell(style.Render(text), width))
}

func dotRatioStyle(p palette, selected bool, counts app.DotFileCounts) lipgloss.Style {
	managed := counts.Managed()
	var style lipgloss.Style
	switch {
	case managed == 0:
		style = p.styleHelp
	case counts.Synced <= 0:
		style = p.styleMissing
	case counts.Synced >= managed:
		style = dotSyncedStyle(p, false)
	default:
		style = p.styleOutdated
	}
	if selected {
		style = style.Bold(true)
	}
	return style
}

func dotSyncedStyle(p palette, selected bool) lipgloss.Style {
	style := p.styleInstalled
	if selected {
		style = style.Bold(true)
	}
	return style
}

func dotHasAction(status app.DotStatus, action app.DotAction) bool {
	for _, candidate := range status.Actions {
		if candidate == action {
			return true
		}
	}
	if len(status.Actions) > 0 {
		return false
	}
	switch dotStatusState(status) {
	case app.DotStateMissing, app.DotStateBroken, app.DotStateModified, app.DotStateLocalOnly, app.DotStateRepoOnly, app.DotStateUntrackedConflict:
		if dotStatusState(status) == app.DotStateUntrackedConflict {
			return action == app.DotActionUseRepo || action == app.DotActionUseLocal || action == app.DotActionIgnore
		}
		return action == app.DotActionSync || action == app.DotActionRemove || action == app.DotActionIgnore
	case app.DotStateSynced:
		return action == app.DotActionRemove || action == app.DotActionIgnore
	case app.DotStateConflict:
		return action == app.DotActionUseRepo || action == app.DotActionUseLocal || action == app.DotActionRemove || action == app.DotActionIgnore
	case app.DotStateNoSource:
		return action == app.DotActionRemove || action == app.DotActionIgnore
	case app.DotStateIgnored:
		return action == app.DotActionUnignore || action == app.DotActionRemove
	}
	return false
}

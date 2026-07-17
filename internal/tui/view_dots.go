package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/app"
)

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
		if q != "" && !strings.Contains(strings.ToLower(e.Name), q) {
			continue
		}
		result = append(result, e)
	}
	return result
}

type dotsVisibleRow struct {
	entry   app.DotStatus
	child   app.DotChild
	isChild bool
}

func dotsEntryMatchesExpanded(m Model, entry app.DotStatus) bool {
	return m.dotsExpandedName != "" &&
		entry.Name == m.dotsExpandedName &&
		app.DotStatusState(entry) == m.dotsExpandedState
}

func dotsVisibleRows(m Model) []dotsVisibleRow {
	entries := filteredDotsEntries(m)
	rows := make([]dotsVisibleRow, 0, len(entries))
	for _, section := range dotsSections(entries) {
		for _, entry := range section.Statuses {
			rows = append(rows, dotsVisibleRow{entry: entry})
			if !dotsEntryMatchesExpanded(m, entry) {
				continue
			}
			rows = appendDotsChildRows(m, rows, entry, entry.Children)
		}
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
		return dotsRowIsDir(row) && len(row.child.Children) > 0
	}
	return dotsRowIsDir(row) && len(row.entry.Children) > 0
}

func dotsRowIsDir(row dotsVisibleRow) bool {
	if row.isChild {
		return row.child.IsDir || len(row.child.Children) > 0
	}
	return row.entry.IsDir || len(row.entry.Children) > 0
}

func dotsRowExpanded(m Model, row dotsVisibleRow) bool {
	if row.isChild {
		return dotsChildExpanded(m, row.entry.Name, row.child)
	}
	return dotsEntryMatchesExpanded(m, row.entry)
}

func dotsSections(entries []app.DotStatus) []app.DotStatusSection {
	return app.DotStatusSections(entries)
}

func renderDots(m Model) string {
	p := m.palette
	var sb strings.Builder

	// ── Disabled ──────────────────────────────────────────────────────────────
	if dotsViewDisabled(m) {
		sb.WriteString("\n")
		sb.WriteString(p.styleHelp.Render("  Dotfile sync is disabled for this machine.") + "\n\n")
		sb.WriteString(p.styleNormal.Render("  [enter] ") + p.styleHelp.Render("set up dotfiles from scratch"))
		sb.WriteString("\n")
		sb.WriteString(p.styleHelp.Render("  Or toggle Dotfile Sync in Settings to re-enable without setup.") + "\n")
		return sb.String()
	}

	// ── Not configured ────────────────────────────────────────────────────────
	if dotsViewUnconfigured(m) {
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
		if m.dotsPreparing {
			sb.WriteString(p.styleHelp.Render("  Loading dotfiles…"))
			sb.WriteString("\n")
			return sb.String()
		}
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
	if m.dotsSearchActive {
		write(renderDotsSearchControl(m) + "\n")
	}

	// ── Repo status (compact, not a list section) ───────────────────────────
	if repoPath := dotsRepoPathForView(m); repoPath != "" {
		var gitPart string
		if m.dotsGitStatus == "" {
			gitPart = "  " + p.styleInstalled.Render("✓ clean")
		} else {
			gitPart = "  " + p.styleOutdated.Render("✗ dirty") + "  " + p.styleHelp.Render("C commit")
		}
		repoW := max(rowAvailableWidth(m.width)-lipgloss.Width(gitPart)-2, 1)
		write(p.styleHelp.PaddingLeft(2).Render(truncatePath(tildePath(repoPath), repoW)) + gitPart + "\n")
		if m.dotsGitStatus != "" {
			lines, overflow := truncatedGitStatus(m.dotsGitStatus, 3)
			for _, line := range lines {
				write(p.styleHelp.PaddingLeft(4).Render(line) + "\n")
			}
			if overflow != "" {
				write(p.styleHelp.PaddingLeft(4).Render(overflow) + "\n")
			}
		}
	}

	visible := filteredDotsEntries(m)
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
	inIgnoredSection := false
	var renderChildRows func(e app.DotStatus, children []app.DotChild)
	renderChildRows = func(e app.DotStatus, children []app.DotChild) {
		for _, child := range children {
			parentState := app.DotStatusState(e)
			childStatus, childStatusStyle := dotChildStatusDisplay(p, child, parentState)
			if inIgnoredSection && !child.Ignored {
				childStatus = "-"
				childStatusStyle = p.styleHelp
			}
			childStatusCol := renderCell(leftCell(fitCellText(childStatus, cols.status), cols.status))
			childName := renderCell(leftCell(fitCellText(dotChildDisplayName(m, e, child), cols.name), cols.name))
			childTarget := truncatePath(tildePath(child.Path), targetWidth)
			childTargetPadded := renderCell(leftCell(childTarget, targetWidth))
			isChildCursor := rowIndex == m.dotsCursor && !m.cursorHidden
			childIgnoreConfirm := m.dotsIgnoreIdx == rowIndex
			childRight := dotRightColumns(p, isChildCursor || childIgnoreConfirm, childStatusCol, childStatusStyle, app.DotChildFileCounts(child, app.DotStatusState(e)), cols.ratio, cols.ignore, "", cols.group)
			childLeft := func(iconStyle, nameStyle, targetStyle lipgloss.Style, iconText, childName, childTarget string) string {
				return iconStyle.Render(iconText) +
					iconNameGap +
					nameStyle.Render(childName) +
					targetStyle.Render(nameTargetGap+childTarget)
			}
			childIconStyle, childNameStyle, childTargetStyle := dotChildRowStyles(p, child, parentState, inIgnoredSection)
			if childIgnoreConfirm {
				buf.markCursor()
				left := childLeft(p.styleIgnored.Bold(true), p.styleActiveText, p.styleHelp.Bold(true), "↳", childName, childTargetPadded)
				write(renderDotsRow(true, left, childRight) + "\n")
				write(renderContextHints(m, hintCtxDotsIgnoreConfirm, hintPrefix) + "\n")
				buf.markCursorEnd()
			} else if isChildCursor {
				buf.markCursor()
				left := childLeft(childIconStyle.Bold(true), p.styleActiveText, childTargetStyle.Bold(true), "↳", childName, childTargetPadded)
				write(renderDotsRow(true, left, childRight) + "\n")
				if hints := renderDotsContextHints(m, hintCtxDotsRow, hintPrefix, m.width); hints != "" {
					write(hints + "\n")
					buf.markCursorEnd()
				} else {
					buf.markCursorEnd()
				}
			} else {
				left := childLeft(childIconStyle, childNameStyle, childTargetStyle, "↳", childName, childTargetPadded)
				write(renderDotsRow(false, left, childRight) + "\n")
			}
			rowIndex++
			if dotsChildExpanded(m, e.Name, child) {
				renderChildRows(e, child.Children)
			}
		}
	}
	for _, section := range dotsSections(visible) {
		if len(section.Statuses) == 0 {
			continue
		}
		inIgnoredSection = section.Title == "Ignored"
		sections.Header(section.Title)
		for _, e := range section.Statuses {
			iconStyle, icon, statusLabel := dotStateDisplay(p, app.DotStatusState(e))
			// Synthesized container entries in the Ignored section (not
			// explicitly ignored themselves) get muted "-" status.
			ignoredContainer := inIgnoredSection && !app.DotStatusHasAction(e, app.DotActionUnignore)
			if ignoredContainer {
				statusLabel = "-"
			}
			switch {
			case m.dotsActiveName == e.Name:
				iconStyle = lipgloss.NewStyle()
				icon = rowSpinnerIcon(m)
			case m.dotsPendingNames[e.Name]:
				iconStyle = p.styleStatus
				icon = iconPending
			}
			statusStyle := dotStatusTextStyle(p, app.DotStatusState(e))
			if ignoredContainer {
				statusStyle = p.styleHelp
			}

			nameCol := renderCell(leftCell(fitCellText(dotEntryDisplayName(m, e), cols.name), cols.name))
			statusCol := renderCell(leftCell(fitCellText(statusLabel, cols.status), cols.status))
			target := truncatePath(tildePath(e.TargetPath), targetWidth)
			right := dotRightColumns(p, false, statusCol, statusStyle, app.DotStatusFileCounts(e), cols.ratio, cols.ignore, e.Group, cols.group)

			removingConfirm := m.dotsConfirmIdx == rowIndex
			repoConfirm := m.dotsOverwriteIdx == rowIndex && app.DotStatusHasAction(e, app.DotActionUseRepo)
			localConfirm := m.dotsLocalIdx == rowIndex && app.DotStatusHasAction(e, app.DotActionUseLocal)
			ignoreConfirm := m.dotsIgnoreIdx == rowIndex
			variantCreate := m.dotsVariantIdx == rowIndex && m.dotsVariantMode == dotsVariantCreate
			variantRemove := m.dotsVariantIdx == rowIndex && m.dotsVariantMode == dotsVariantRemove
			isCursor := rowIndex == m.dotsCursor && !m.cursorHidden

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
			activeRight := dotRightColumns(p, true, statusCol, statusStyle, app.DotStatusFileCounts(e), cols.ratio, cols.ignore, e.Group, cols.group)

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
			case variantCreate:
				left := rowLeft(p.styleProvider, p.styleActiveText, p.styleHelp.Bold(true))
				write(renderDotsRow(true, left, activeRight) + "\n")
				write(renderDotsVariantCreatePrompt(m, e.Name, hintPrefix, m.width) + "\n")
				buf.markCursorEnd()
			case variantRemove:
				left := rowLeft(p.styleMissing, p.styleMissing, p.styleMissing)
				write(renderDotsRow(true, left, activeRight) + "\n")
				write(renderDotsVariantRemovePrompt(m, e.Name, hintPrefix, m.width) + "\n")
				buf.markCursorEnd()
			case isCursor:
				left := rowLeft(iconStyle.Bold(true), p.styleActiveText, p.styleHelp.Bold(true))
				write(renderDotsRow(true, left, activeRight) + "\n")
				if errLine := renderDotsLastError(p, e, m.width); errLine != "" {
					write(errLine + "\n")
				}
				if app.DotStatusHasAction(e, app.DotActionUseRepo) || app.DotStatusHasAction(e, app.DotActionUseLocal) {
					write(renderDotsContextHints(m, hintCtxDotsConflict, hintPrefix, m.width) + "\n")
					buf.markCursorEnd()
				} else {
					if hints := renderDotsContextHints(m, hintCtxDotsRow, hintPrefix, m.width); hints != "" {
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
			if dotsEntryMatchesExpanded(m, e) {
				renderChildRows(e, e.Children)
			}
		}
	}

	renderDotsHistorySection(m, write, sections)

	return buf.render(listAvailableHeight(m))
}

func dotsRepoPathForView(m Model) string {
	availability := dotsViewAvailability(m)
	if availability.Reason == app.DotsSyncAvailabilityNoRepo {
		return ""
	}
	if strings.TrimSpace(availability.RepoPath) != "" {
		return availability.RepoPath
	}
	return ""
}

func renderDotsHistorySection(m Model, write func(string), sections *listSectionWriter) {
	if len(m.dotsHistory) == 0 && strings.TrimSpace(m.dotsHistoryErr) == "" {
		return
	}
	sections.Header("History")
	lineW := max(rowAvailableWidth(m.width)-2, 12)
	if errText := strings.TrimSpace(m.dotsHistoryErr); errText != "" {
		write(m.palette.styleHelp.PaddingLeft(2).Render(fitCellText("history unavailable: "+errText, lineW)) + "\n")
		return
	}
	for i, entry := range m.dotsHistory {
		if i >= 3 {
			break
		}
		write(m.palette.styleHelp.PaddingLeft(2).Render(fitCellText(dotsHistoryTabLine(entry), lineW)) + "\n")
	}
}

func dotsHistoryDashboardLine(entry app.DotsHistoryEntry) string {
	return "last " + dotsHistoryTabLine(entry)
}

func dotsHistoryTabLine(entry app.DotsHistoryEntry) string {
	text := dotsHistoryEntryText(entry)
	if label := dotsHistoryTimeLabel(entry.Time); label != "" {
		return label + " " + text
	}
	return text
}

func dotsHistoryTimeLabel(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("01-02 15:04")
}

func dotsHistoryEntryText(entry app.DotsHistoryEntry) string {
	operation := strings.TrimSpace(entry.Operation)
	if operation == "" {
		operation = "operation"
	}
	if entryName := strings.TrimSpace(entry.Entry); entryName != "" {
		operation += " " + entryName
	}
	status := strings.TrimSpace(entry.Status)
	if status == "" {
		status = "unknown"
	}
	summary := strings.TrimSpace(entry.Summary)
	if summary == "" {
		summary = strings.TrimSpace(entry.Error)
	}
	if summary == "" {
		return operation + ": " + status
	}
	return operation + ": " + status + ", " + summary
}

func renderDotsDeleteKeepLocalPrompt(m Model, name, prefix string) string {
	p := m.palette
	prompt := p.styleHelp.Render("delete ") +
		p.styleProvider.Bold(true).Render(name) +
		p.styleHelp.Render(", keep local? ")
	return prefix + prompt + renderActionHintText(p, contextHintItems(m, hintCtxDotsDeleteConfirm))
}

func renderDotsContextHints(m Model, ctx hintContext, prefix string, width int) string {
	items := contextHintItems(m, ctx)
	for len(items) > 0 {
		rendered := renderHintItems(m.palette, prefix, items)
		if width <= 0 || lipgloss.Width(rendered) <= width {
			return rendered
		}
		items = items[:len(items)-1]
	}
	return ""
}

func renderDotsVariantCreatePrompt(m Model, name, prefix string, width int) string {
	p := m.palette
	hints := renderDotsContextHints(m, hintCtxDotsVariantCreate, "", max(width-lipgloss.Width(prefix), 1))
	prompt := p.styleHelp.Render("create host variant for ") +
		p.styleProvider.Bold(true).Render(name) +
		p.styleHelp.Render(" ")
	line := prefix + prompt + hints
	if width <= 0 || lipgloss.Width(line) <= width {
		return line
	}
	return prefix + hints
}

func renderDotsVariantRemovePrompt(m Model, name, prefix string, width int) string {
	p := m.palette
	hints := renderDotsContextHints(m, hintCtxDotsVariantRemove, "", max(width-lipgloss.Width(prefix), 1))
	prompt := p.styleHelp.Render("remove host variant for ") +
		p.styleProvider.Bold(true).Render(name) +
		p.styleHelp.Render(" ")
	line := prefix + prompt + hints
	if width <= 0 || lipgloss.Width(line) <= width {
		return line
	}
	return prefix + hints
}

func renderDotsSearchControl(m Model) string {
	p := m.palette
	return "  " + p.styleNormal.Render("/") + " " + renderEmptyAwareTextInputView(p, m.filter, m.filter.Placeholder, 0)
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
	state := dotChildStateForDisplay(child, parentState)
	_, _, label := dotStateDisplay(p, state)
	return label, dotStatusTextStyle(p, state)
}

func dotChildStateForDisplay(child app.DotChild, parentState app.DotState) app.DotState {
	if child.Ignored {
		return app.DotStateIgnored
	}
	if child.State != "" {
		return child.State
	}
	return parentState
}

func dotChildRowStyles(p palette, child app.DotChild, parentState app.DotState, inIgnoredSection bool) (lipgloss.Style, lipgloss.Style, lipgloss.Style) {
	if inIgnoredSection && !child.Ignored {
		return p.styleHelp, p.styleHelp, p.styleHelp
	}
	state := dotChildStateForDisplay(child, parentState)
	iconStyle := dotStatusTextStyle(p, state)
	var nameStyle lipgloss.Style
	switch state {
	case app.DotStateSynced:
		nameStyle = p.styleNormal
	case app.DotStateNoSource:
		nameStyle = p.styleHelp
	default:
		nameStyle = dotStatusTextStyle(p, state)
	}
	return iconStyle, nameStyle, p.styleHelp
}

const (
	dotKindFileIcon            = "•"
	dotKindFolderCollapsedIcon = "▸"
	dotKindFolderExpandedIcon  = "▾"
	dotKindFolderEmptyIcon     = "▹"
	dotVariantIcon             = "◇"
	dotChildIndent             = "  "
)

func dotEntryDisplayName(m Model, entry app.DotStatus) string {
	name := dotEntryKindIcon(m, entry) + " " + entry.Name
	if entry.Variant {
		name += " " + dotVariantIcon
	}
	return name
}

func dotChildDisplayName(m Model, entry app.DotStatus, child app.DotChild) string {
	depth := child.Depth
	if depth < 1 {
		depth = 1
	}
	return strings.Repeat(dotChildIndent, depth) + dotChildKindIcon(m, entry, child) + " " + child.Name
}

func dotEntryKindIcon(m Model, entry app.DotStatus) string {
	if !entry.IsDir && len(entry.Children) == 0 {
		return dotKindFileIcon
	}
	if len(entry.Children) == 0 && entry.FileCount == 0 {
		return dotKindFolderEmptyIcon
	}
	if dotsEntryMatchesExpanded(m, entry) {
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
		state := app.DotStatusState(entry)
		_, _, statusLabel := dotStateDisplay(p, state)
		cols.name = max(cols.name, lipgloss.Width(dotEntryDisplayName(m, entry)))
		cols.status = max(cols.status, lipgloss.Width(statusLabel))
		counts := app.DotStatusFileCounts(entry)
		cols.ratio = max(cols.ratio, lipgloss.Width(dotRatioText(counts)))
		if ignored := dotIgnoredText(counts); ignored != "" {
			cols.ignore = max(cols.ignore, dotsIgnoredColW, lipgloss.Width(ignored))
		}
		if badge := dotGroupBadge(entry.Group); badge != "" {
			cols.group = max(cols.group, lipgloss.Width(badge))
		}
		visitDotChildren(entry.Children, func(child app.DotChild) {
			childStatus, _ := dotChildStatusDisplay(p, child, state)
			counts := app.DotChildFileCounts(child, state)
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

// renderDotsLastError shows the recorded failure reason for the selected
// out-of-sync entry, wrapped so multi-line tool output (e.g. stow stderr)
// stays readable instead of being truncated into a transient status line.
func renderDotsLastError(p palette, e app.DotStatus, width int) string {
	text := strings.Join(strings.Fields(e.LastError), " ")
	if text == "" {
		return ""
	}
	const maxErrorRunes = 400
	if runes := []rune(text); len(runes) > maxErrorRunes {
		text = string(runes[:maxErrorRunes-1]) + "…"
	}
	avail := max(rowAvailableWidth(width)-4, 20)
	return p.styleMissing.PaddingLeft(4).Width(avail).Render("✗ " + text)
}

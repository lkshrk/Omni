package tui

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/actions"
	"github.com/lkshrk/omni/internal/apm"
	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/text"
)

const (
	agentsMaxNoticeLines        = 3
	agentsMaxHarnessNoticeLines = 2
	iconDrifted                 = "≠"
)

const (
	agentsFallbackWorkspacePath = "~/.apm/apm.yml"
)

func agentsDetailPair(label, value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return label + ": " + value
}

// Detail order mirrors the tools tab: description first, then the row's metadata.
// The manifest author when the package declares one, else the owner its source is published under.
func agentsRowAuthor(row app.AgentsPackageRow) string {
	if author := strings.TrimSpace(row.Author); author != "" {
		return author
	}
	source := strings.TrimPrefix(strings.TrimSpace(row.Source), "~/")
	if owner, _, ok := strings.Cut(source, "/"); ok && owner != "" && owner != "." {
		return owner
	}
	return ""
}

func agentsPackageDetails(row app.AgentsPackageRow) []string {
	out := []string{
		agentsDetailPair("source", row.Source),
		agentsDetailPair("author", row.Author),
	}
	if row.DeployedFiles > 0 {
		out = append(out, "files: "+strconv.Itoa(row.DeployedFiles))
	}
	provided := slices.Clone(row.Provides)
	slices.SortFunc(provided, func(a, b app.AgentsProvidedChild) int {
		kindRank := func(kind string) int {
			switch strings.ToLower(kind) {
			case "mcp":
				return 0
			case "lsp":
				return 1
			default:
				return 2
			}
		}
		if rank := kindRank(a.Kind) - kindRank(b.Kind); rank != 0 {
			return rank
		}
		if kind := strings.Compare(strings.ToLower(a.Kind), strings.ToLower(b.Kind)); kind != 0 {
			return kind
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	providedNames := make([]string, 0, len(provided))
	for _, child := range provided {
		providedNames = append(providedNames, strings.ToUpper(child.Kind)+" "+child.Name)
	}
	if len(providedNames) > 0 {
		out = append(out, "provides: "+strings.Join(providedNames, ", "))
	}
	issues := slices.Clone(row.Issues)
	slices.Sort(issues)
	if len(issues) > 0 {
		out = append(out, "issues: "+strings.Join(issues, ", "))
	}
	return out
}

func agentsPackageVersion(row app.AgentsPackageRow) string {
	if row.UpdateAvailable && row.LatestVersion != "" {
		return compactVersion(row.Version) + " → " + compactVersion(row.LatestVersion)
	}
	return row.Version
}

const agentsNativeSectionTitle = "Not managed by APM"

func agentsNativeDetail(row app.AgentsNativeRow) string {
	if row.Ignored {
		return row.Kind + " · ignored"
	}
	return row.Kind
}

// Ignored rows read as orphaned rather than drifted: they are deliberately outside APM, not damaged.
func agentsNativeStatus(row app.AgentsNativeRow) app.AgentsPackageStatus {
	if row.Ignored {
		return app.AgentsPackageOrphaned
	}
	return app.AgentsPackageUnavailable
}

func agentsNativeDetails(row app.AgentsNativeRow) []string {
	state := "not declared in the host template"
	switch {
	case row.Ignored:
		state = "ignored" + agentsNativeReasonSuffix(row.Reason)
	case !row.Adoptable:
		state = "retained" + agentsNativeReasonSuffix(row.Reason)
	}
	return []string{
		agentsDetailPair("client", row.Target),
		agentsDetailPair("kind", row.Kind),
		agentsDetailPair("state", state),
		agentsDetailPair("read from", row.Source),
		agentsDetailPair("install root", row.InstallRoot),
	}
}

func agentsNativeReasonSuffix(reason string) string {
	if strings.TrimSpace(reason) == "" {
		return ""
	}
	return " — " + reason
}

func agentsServiceDetails(row app.AgentsServiceRow) []string {
	return []string{
		agentsDetailPair("transport", row.Detail),
		agentsDetailPair("command", row.Command),
		agentsDetailPair("host", row.URLHost),
		agentsDetailPair("deployed to", strings.Join(row.Harnesses, ",")),
		agentsDetailPair("targets", agentsTargetsText(row.Targets)),
	}
}

func agentsRegistryDetails(entry app.AgentsRegistryEntry) []string {
	return []string{
		agentsDetailPair("marketplace", entry.Marketplace),
		agentsDetailPair("version", entry.Version),
		agentsDetailPair("author", entry.Author),
		agentsDetailPair("spec", entry.Spec()),
	}
}

// Mirrors inlineDetailLines: the wrapped description, the metadata, the row's action error, then its hints.
func (m Model) agentsDetailBlock(description string, details []string, ctx hintContext) []string {
	p := m.palette
	prefix, hintPrefix := listTextPrefix(), listHintPrefix()
	wrapWidth := max(rowAvailableWidth(m.width)-lipgloss.Width(prefix), 20)

	var lines []string
	if description != "" {
		for _, wrapped := range text.WrapText(description, wrapWidth) {
			lines = append(lines, prefix+p.styleHelp.Render(wrapped))
		}
	} else if row, ok := m.agentsSelectedRow(); ctx == hintCtxAgentsRow && ok && row.kind == agentsRowPackage {
		lines = append(lines, prefix+p.styleNoDesc.Render("no description available"))
	}
	for _, detail := range details {
		if strings.TrimSpace(detail) == "" {
			continue
		}
		style := p.styleHelp
		if strings.HasPrefix(detail, "issues:") {
			style = p.styleOutdated
		}
		for _, wrapped := range text.WrapText(detail, wrapWidth) {
			lines = append(lines, prefix+style.Render(wrapped))
		}
	}
	if ctx == hintCtxAgentsRow {
		// A limitation is metadata about the row, so it lines up with the other detail lines, as a tool row's advisories do.
		for _, limitation := range agentsRowLimitations(m) {
			for _, wrapped := range text.WrapText(limitation, wrapWidth) {
				lines = append(lines, prefix+p.styleHintDesc.Render(wrapped))
			}
		}
	}
	var interaction string
	if ctx == hintCtxAgentsNativeRow && m.agentsConfirmIdx == m.agentsCursor {
		interaction = renderConfirmActionHints(m, hintPrefix, m.keys.AgentsRemove, actions.MustTUIConfirmDescription(actions.AgentsRemoveNative))
	} else if ctx == hintCtxAgentsRow && m.agentsConfirmIdx == m.agentsCursor {
		interaction = renderConfirmActionHints(m, hintPrefix, m.keys.AgentsRemove, "confirm uninstall")
	} else if ctx == hintCtxAgentsRow && m.apmRunning && m.agentsRowOpSpec != "" {
		if row, ok := m.agentsSelectedRow(); ok && row.kind == agentsRowPackage && agentsUninstallSpec(row.pkg) == m.agentsRowOpSpec {
			interaction = renderRowOperationStatusLine(m, prefix, m.spinner.View()+" running "+m.apmCommand+"…")
		}
	} else {
		interaction = renderContextHints(m, ctx, hintPrefix)
	}
	if interaction != "" {
		lines = append(lines, interaction)
	}
	return lines
}

// The failed op's message sits between the row and its details, as a tool row's action error does.
func (m Model) agentsRowErrorLines(spec string) []string {
	if spec == "" || spec != m.agentsRowOpSpec || m.apmErr == nil {
		return nil
	}
	prefix := listTextPrefix()
	wrapWidth := max(rowAvailableWidth(m.width)-lipgloss.Width(prefix), 20)
	var lines []string
	for _, wrapped := range text.WrapText(m.apmErr.Error(), wrapWidth) {
		lines = append(lines, prefix+m.palette.styleErr.Render(wrapped))
	}
	return lines
}

func renderAgentsFilterControl(m Model) string {
	return screenEdgeInset() + m.palette.styleNormal.Render("/") + " " + renderEmptyAwareTextInputView(m.palette, m.filter, m.filter.Placeholder, 0)
}

func agentsStatusGlyph(p palette, status app.AgentsPackageStatus) (string, lipgloss.Style) {
	switch status {
	case app.AgentsPackageInstalled:
		return iconInstalled, p.styleInstalled
	case app.AgentsPackageDrifted:
		return iconDrifted, p.styleWrongProv
	case app.AgentsPackageUnavailable:
		return iconWrongProv, p.styleOutdated
	case app.AgentsPackageMissing:
		return iconMissing, p.styleMissing
	default:
		return iconOrphan, p.styleOrphan
	}
}

// nil means defaults; an empty non-nil slice means declared targets exclude this surface.
func agentsTargetsText(targets []string) string {
	if targets == nil {
		return "all"
	}
	if len(targets) == 0 {
		return "none"
	}
	return strings.Join(targets, ",")
}

// A raw apm run carries no structured result, so its own marked output is the only summary available.
func apmMarkedLines(output string) []string {
	var lines []string
	for _, line := range strings.Split(output, "\n") {
		if app.IsAPMNoticeLine(line) {
			lines = append(lines, strings.TrimSpace(line))
		}
	}
	return lines
}

// apm prints progress before its verdict, so severity, not order, decides which notices survive the cap.
func agentsNoticeRank(line string) int {
	line = strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(line, "[x]") || strings.HasPrefix(line, "error:"):
		return 0
	case (strings.HasPrefix(line, "[!]") && !strings.Contains(line, "User-scope primitives are fully supported")) ||
		strings.HasPrefix(line, "warning:"):
		return 1
	default:
		return 2
	}
}

func agentsNoticeText(line string) string {
	line = strings.TrimSpace(line)
	body := line
	if len(body) >= 4 && body[0] == '[' && body[2] == ']' {
		body = strings.TrimSpace(body[3:])
	}
	if strings.Contains(body, "files skipped") && strings.Contains(body, "local files exist, not managed by APM") {
		count := strings.Fields(body)
		if len(count) > 0 {
			return count[0] + " existing local files were left unchanged because APM does not own them; press e to review the trace log, and remove them only if APM should manage them"
		}
	}
	if strings.HasPrefix(line, "note:") {
		return strings.TrimSpace(strings.TrimPrefix(line, "note:")) + "; no action is needed unless APM should replace those files"
	}
	switch agentsNoticeRank(line) {
	case 0:
		return "Error: " + body
	case 1:
		return "Warning: " + body
	default:
		return body
	}
}

func agentsNoticeStyle(p palette, line string) lipgloss.Style {
	switch agentsNoticeRank(line) {
	case 0:
		return p.styleErr
	case 1:
		return p.styleOutdated
	default:
		return p.styleHelp
	}
}

func capAgentsNotices(notices []string) []string {
	ordered := make([]string, 0, len(notices))
	seen := make(map[string]bool, len(notices))
	for _, notice := range notices {
		if notice = strings.TrimSpace(notice); notice != "" && !seen[notice] {
			seen[notice] = true
			ordered = append(ordered, notice)
		}
	}
	slices.SortStableFunc(ordered, func(a, b string) int { return agentsNoticeRank(a) - agentsNoticeRank(b) })
	if len(ordered) <= agentsMaxNoticeLines {
		return ordered
	}
	remaining := len(ordered) - agentsMaxNoticeLines
	return append(ordered[:agentsMaxNoticeLines:agentsMaxNoticeLines], strconv.Itoa(remaining)+" more APM messages hidden; press e to view the full trace log")
}

// Harness notices never pass IsAPMNoticeLine, so they need their own capped feed rather than the apm one.
func agentsHarnessNoticeLines(notices []string) []string {
	if len(notices) <= agentsMaxHarnessNoticeLines {
		return notices
	}
	return append(slices.Clone(notices[:agentsMaxHarnessNoticeLines]), "…more harness notices")
}

// Notices are whole sentences, so they wrap rather than losing their tail to the clip every other line takes.
func (m Model) agentsWrappedNotice(text string, style lipgloss.Style) []string {
	pad := screenEdgeInset()
	width := max(m.width-lipgloss.Width(pad), 1)
	wrapped := strings.Split(lipgloss.NewStyle().Width(width).Render(strings.TrimSpace(text)), "\n")
	for i := range wrapped {
		wrapped[i] = strings.TrimRight(wrapped[i], " ")
	}
	out := make([]string, 0, len(wrapped))
	for _, line := range wrapped {
		out = append(out, style.Render(pad+line))
	}
	return out
}

func (m Model) agentsAPMNoticeLines() []string {
	// Leave room for one body row, the footer separator, command/status, summary, and global hints.
	budget := max(listAvailableHeight(m)-5, 1)
	var out []string
	for _, notice := range m.apmNotices {
		wrapped := m.agentsWrappedNotice(agentsNoticeText(notice), agentsNoticeStyle(m.palette, notice))
		if len(out)+len(wrapped) <= budget {
			out = append(out, wrapped...)
			continue
		}
		room := budget - len(out)
		if room > 1 {
			out = append(out, wrapped[:room-1]...)
		}
		hint := "e: full APM log"
		out = append(out, m.agentsWrappedNotice(hint, m.palette.styleHelp)[0])
		return out
	}
	return out
}

// The op block sits outside the scroll window, so a running sync stays visible however long the row list is.
func (m Model) agentsFooterLines() []string {
	p := m.palette
	pad := screenEdgeInset()
	var lines []string
	switch {
	case m.apmRunning && m.agentsRowOpSpec == "":
		lines = append(lines, p.styleStatus.Render(pad+m.spinner.View()+" running "+m.apmCommand+"…"))
	case m.apmCommand != "" && m.agentsRowOpSpec == "":
		mark := p.styleInstalled.Render(iconInstalled)
		if m.apmErr != nil {
			mark = p.styleMissing.Render(iconMissing)
		}
		lines = append(lines, pad+mark+" "+p.styleHelp.Render(m.apmCommand))
	}
	lines = append(lines, m.agentsAPMNoticeLines()...)
	// A row op's failure belongs to that row's detail block, not to the tab-wide footer.
	if m.apmErr != nil && m.agentsRowOpSpec == "" {
		lines = append(lines, m.agentsWrappedNotice(m.apmErr.Error(), p.styleErr)...)
	}
	lines = append(lines, m.agentsRemovalHintLines()...)
	if cause, remedy := agentsReadinessGuidanceParts(m); cause != "" {
		style := p.styleHelp
		if m.agentsReadinessErr != nil || m.agentsReadiness.State == app.AgentsReadinessInvalid {
			style = p.styleErr
		}
		lines = append(lines, m.agentsWrappedNotice(cause, style)...)
		if remedy != "" {
			lines = append(lines, m.agentsWrappedNotice(remedy, p.styleHelp)...)
		}
	}
	for _, notice := range agentsHarnessNoticeLines(m.agentsNotices) {
		lines = append(lines, m.agentsWrappedNotice(notice, p.styleOutdated)...)
	}
	if m.agentsRowsErr != nil {
		lines = append(lines, m.agentsWrappedNotice(m.agentsRowsErr.Error(), p.styleErr)...)
	}
	switch {
	case m.agentsOutdatedChecking:
		lines = append(lines, p.styleStatus.Render(pad+m.spinner.View()+" checking package updates…"))
	case m.agentsOutdatedErr != nil:
		lines = append(lines, m.agentsWrappedNotice("Update check failed: "+m.agentsOutdatedErr.Error()+"; press R to retry", p.styleErr)...)
	case m.agentsOutdatedUnknown > 0:
		lines = append(lines, p.styleHelp.Render(pad+strconv.Itoa(m.agentsOutdatedUnknown)+" package updates could not be checked  ·  R retry"))
	}
	if m.agentsRegistryMode {
		lines = append(lines, p.styleHelp.Render(pad+strconv.Itoa(len(m.agentsVisibleRegistry()))+"/"+strconv.Itoa(len(m.agentsRegistry))+" plugins  ·  esc back"))
	} else if m.agentsRowsKnown {
		lines = append(lines, p.styleHelp.Render(pad+agentsSummaryText(m)))
	}
	return lines
}

func agentsReadinessGuidance(m Model) string {
	cause, remedy := agentsReadinessGuidanceParts(m)
	switch {
	case cause == "":
		return ""
	case remedy == "":
		return cause
	}
	return cause + " · " + remedy
}

// The footer gives the cause and the remedy their own lines; the status line joins them.
func agentsReadinessGuidanceParts(m Model) (cause, remedy string) {
	if m.agentsReadinessPending {
		return "Checking APM readiness…", ""
	}
	if m.agentsReadinessErr != nil {
		return "APM readiness check failed: " + m.agentsReadinessErr.Error(), "R recheck"
	}
	detail := strings.Join(m.agentsReadiness.Details, "; ")
	switch m.agentsReadiness.State {
	case app.AgentsReadinessEmpty:
		return firstNonEmpty(detail, "No APM workspace and no host template"), "commit a host template, then S sync"
	case app.AgentsReadinessTemplateOnly:
		return firstNonEmpty(detail, "APM template is staged but not installed"), "S sync"
	case app.AgentsReadinessLiveIncomplete:
		return firstNonEmpty(detail, "APM manifest has no lockfile"), "S sync"
	case app.AgentsReadinessInvalid:
		return firstNonEmpty(detail, "APM workspace is invalid"), "inspect APM files · R recheck"
	default:
		return "", ""
	}
}

type agentsColWidths struct {
	name, detail, version, targets int
}

func agentsColumnWidths(m Model) agentsColWidths {
	cols := agentsColWidths{name: 20}
	widen := func(name, detail, targets string) {
		cols.name = max(cols.name, lipgloss.Width(name))
		cols.detail = max(cols.detail, lipgloss.Width(detail))
		cols.targets = max(cols.targets, lipgloss.Width(targets))
	}
	for _, row := range m.agentsVisiblePackages() {
		widen(row.Name, agentsRowAuthor(row), agentsTargetsText(row.Targets))
		cols.version = max(cols.version, lipgloss.Width(agentsPackageVersion(row)))
	}
	for _, rows := range [][]app.AgentsServiceRow{m.agentsVisibleServices(m.agentsMCPRows), m.agentsVisibleServices(m.agentsLSPRows)} {
		for _, row := range rows {
			widen(row.Name, row.Detail, agentsTargetsText(row.Targets))
		}
	}
	for _, row := range m.agentsVisibleNatives() {
		widen(row.Identity, agentsNativeDetail(row), row.Target)
	}
	over := listIconWidth + toolIconNameGapWidth + cols.name + listColumnGap + agentsRightGroupWidth(cols) - rowAvailableWidth(m.width)
	shrinkWidth(&cols.detail, 12, &over)
	shrinkWidth(&cols.name, 12, &over)
	shrinkWidth(&cols.targets, 6, &over)
	shrinkWidth(&cols.version, 7, &over)
	shrinkWidth(&cols.detail, 0, &over)
	shrinkWidth(&cols.targets, 0, &over)
	shrinkWidth(&cols.version, 0, &over)
	shrinkWidth(&cols.name, 8, &over)
	return cols
}

func agentsRightGroupWidth(cols agentsColWidths) int {
	width := 0
	for _, w := range []int{cols.detail, cols.version, cols.targets} {
		if w > 0 {
			width += w + listColumnGap
		}
	}
	return max(width-listColumnGap, 0)
}

func (m Model) agentsRowLine(name, detail, version, latest, targets string, status app.AgentsPackageStatus, cols agentsColWidths, selected bool) string {
	p := m.palette
	glyph, glyphStyle := agentsStatusGlyph(p, status)
	if latest != "" {
		glyph, glyphStyle = iconOutdated, p.styleOutdated
	}
	nameStyle := p.styleNormal
	if selected {
		nameStyle = p.styleActiveText
	}
	rest := make([]rowCell, 0, 5)
	add := func(text string, style lipgloss.Style, width int) {
		if width <= 0 {
			return
		}
		rest = append(rest, rightCell(style.Render(fitCellText(text, width)), width))
	}
	add(detail, p.styleHelp, cols.detail)
	if latest != "" && cols.version > 0 {
		current, upgrade := fitUpgradeVersionText(compactVersion(version), compactVersion(latest), cols.version)
		rest = append(rest, rightCell(p.styleVersionMuted.Render(current)+p.styleOutdated.Render(upgrade), cols.version))
	} else {
		add(version, p.styleVersion, cols.version)
	}
	add(targets, p.styleProvider, cols.targets)
	return renderResponsiveGroupListRow(p, selected,
		[]rowCell{
			leftCell(glyphStyle.Render(glyph), listIconWidth),
			leftCell(nameStyle.Render(fitCellText(name, cols.name)), cols.name),
		},
		rest,
		rowAvailableWidth(m.width), listColumnGap, listColumnGap,
	)
}

func (m Model) viewSkillsBody() string {
	p := m.palette
	cols := agentsColumnWidths(m)
	cursor := clampIndex(m.agentsCursor, m.agentsRowCount())
	index := 0

	// The cursor row always carries its detail block, exactly as a tool or dot row does.
	section := func(title string, rows int, render func(i int, selected bool) string, block func(i int) ([]string, []string)) sectionedTabSection {
		out := sectionedTabSection{title: title}
		for i := 0; i < rows; i++ {
			selected := index == cursor && !m.cursorHidden
			row := sectionedTabRow{selected: selected, line: render(i, selected)}
			if selected {
				errorLines, details := block(i)
				row.details = append(errorLines, details...)
			}
			out.rows = append(out.rows, row)
			index++
		}
		return out
	}

	if m.agentsRegistryMode {
		return m.viewAgentsRegistryBody(section)
	}
	packages := m.agentsVisiblePackages()
	updateCount := 0
	for updateCount < len(packages) && packages[updateCount].UpdateAvailable {
		updateCount++
	}
	var sections []sectionedTabSection
	for _, group := range []struct {
		title string
		rows  []app.AgentsPackageRow
	}{{"Updates Available", packages[:updateCount]}, {"Packages", packages[updateCount:]}} {
		rows := group.rows
		sections = append(sections, section(group.title, len(rows), func(i int, selected bool) string {
			row := rows[i]
			return m.agentsRowLine(row.Name, agentsRowAuthor(row), row.Version, row.LatestVersion, agentsTargetsText(row.Targets), row.Status, cols, selected)
		}, func(i int) ([]string, []string) {
			row := rows[i]
			return m.agentsRowErrorLines(agentsUninstallSpec(row)), m.agentsDetailBlock(row.Description, agentsPackageDetails(row), hintCtxAgentsRow)
		}))
	}
	for _, group := range []struct {
		title string
		rows  []app.AgentsServiceRow
	}{{"MCP servers", m.agentsVisibleServices(m.agentsMCPRows)}, {"LSP servers", m.agentsVisibleServices(m.agentsLSPRows)}} {
		rows := group.rows
		sections = append(sections, section(group.title, len(rows), func(i int, selected bool) string {
			row := rows[i]
			return m.agentsRowLine(row.Name, row.Detail, "", "", agentsTargetsText(row.Targets), row.Status, cols, selected)
		}, func(i int) ([]string, []string) {
			return nil, m.agentsDetailBlock("", agentsServiceDetails(rows[i]), hintCtxAgentsRow)
		}))
	}
	if natives := m.agentsVisibleNatives(); len(natives) > 0 {
		sections = append(sections, section(agentsNativeSectionTitle, len(natives), func(i int, selected bool) string {
			row := natives[i]
			return m.agentsRowLine(row.Identity, agentsNativeDetail(row), "", "", row.Target, agentsNativeStatus(row), cols, selected)
		}, func(i int) ([]string, []string) {
			return nil, m.agentsDetailBlock("", agentsNativeDetails(natives[i]), hintCtxAgentsNativeRow)
		}))
	}
	sections = slices.DeleteFunc(sections, func(s sectionedTabSection) bool { return len(s.rows) == 0 })

	if len(sections) == 0 && m.agentsRowsKnown && m.agentsRowsErr == nil {
		empty := rowContentInset() + "Nothing declared in ~/.apm/apm.yml."
		if m.agentsFilterText() != "" {
			empty = rowContentInset() + "No rows match the filter."
		}
		sections = []sectionedTabSection{{empty: []string{p.styleHelp.Render(empty)}}}
	}

	return renderSectionedTab(m, sectionedTab{
		top:      m.agentsBodyTopLines(),
		footer:   m.agentsFooterLines(),
		sections: sections,
	})
}

// Mirrors the dots body: the search control, then the workspace path, then the first section.
func (m Model) agentsBodyTopLines() []string {
	var lines []string
	if m.agentsSearchActive {
		lines = append(lines, renderAgentsFilterControl(m))
	}
	path := agentsWorkspacePath(m)
	if path != "" {
		width := max(rowAvailableWidth(m.width)-2, 1)
		lines = append(lines, m.palette.styleHelp.PaddingLeft(2).Render(truncatePath(tildePath(path), width)))
	}
	return lines
}

// installed, missing, and orphaned always show so the three baseline counts never jump position.
var agentsAlwaysCounted = []app.AgentsPackageStatus{
	app.AgentsPackageInstalled,
	app.AgentsPackageMissing,
	app.AgentsPackageOrphaned,
}

func agentsSummaryText(m Model) string {
	packages := m.agentsVisiblePackages()
	mcp, lsp := m.agentsVisibleServices(m.agentsMCPRows), m.agentsVisibleServices(m.agentsLSPRows)
	counts := map[app.AgentsPackageStatus]int{}
	for _, row := range packages {
		counts[row.Status]++
	}
	for _, rows := range [][]app.AgentsServiceRow{mcp, lsp} {
		for _, row := range rows {
			counts[row.Status]++
		}
	}
	var parts []string
	updates := 0
	for _, row := range packages {
		if row.UpdateAvailable {
			updates++
		}
	}
	if updates > 0 {
		parts = append(parts, strconv.Itoa(updates)+" updates")
	}
	for _, status := range app.AgentsStatusOrder {
		if counts[status] > 0 || slices.Contains(agentsAlwaysCounted, status) {
			parts = append(parts, strconv.Itoa(counts[status])+" "+string(status))
		}
	}
	surfaces := strconv.Itoa(len(packages)) + " pkg  " + strconv.Itoa(len(mcp)) + " mcp  " + strconv.Itoa(len(lsp)) + " lsp"
	if natives := len(m.agentsVisibleNatives()); natives > 0 {
		surfaces += "  " + strconv.Itoa(natives) + " native"
	}
	if m.agentsFilterText() != "" {
		surfaces += "  ·  " + strconv.Itoa(m.agentsRowCount()) + "/" + strconv.Itoa(m.agentsTotalRowCount()) + " shown"
	}
	return strings.Join(parts, "  ") + "  ·  " + surfaces
}

func (m Model) agentsRemovalHintLines() []string {
	out := make([]string, 0, len(m.agentsRemovalHint))
	for _, line := range m.agentsRemovalHint {
		if strings.TrimSpace(line) != "" {
			out = append(out, m.agentsWrappedNotice(line, m.palette.styleHelp)...)
		}
	}
	return out
}

func agentsWorkspacePath(m Model) string {
	if m.app == nil {
		return agentsFallbackWorkspacePath
	}
	if dir, err := apm.GlobalWorkspaceDir(); err == nil {
		return filepath.Join(dir, "apm.yml")
	}
	return agentsFallbackWorkspacePath
}

func (m Model) viewAgentsRegistryBody(section func(string, int, func(int, bool) string, func(int) ([]string, []string)) sectionedTabSection) string {
	p := m.palette
	entries := m.agentsVisibleRegistry()
	cols := agentsColWidths{name: 20}
	statusWidth := len(string(app.AgentsPackageInstalled))
	for _, entry := range entries {
		cols.name = max(cols.name, lipgloss.Width(entry.Name))
		cols.targets = max(cols.targets, lipgloss.Width(entry.Marketplace))
	}
	over := listIconWidth + toolIconNameGapWidth + cols.name + listColumnGap + agentsRightGroupWidth(cols) + statusWidth + listColumnGap - rowAvailableWidth(m.width)
	shrinkWidth(&cols.name, 12, &over)
	shrinkWidth(&cols.targets, 6, &over)
	shrinkWidth(&cols.targets, 0, &over)
	shrinkWidth(&cols.name, 8, &over)

	rows := section("Registry", len(entries), func(i int, selected bool) string {
		entry := entries[i]
		status, glyph, style := "available", iconMissing, p.styleHelp
		if entry.Installed {
			status, glyph, style = string(app.AgentsPackageInstalled), iconInstalled, p.styleInstalled
		}
		nameStyle := p.styleNormal
		if selected {
			nameStyle = p.styleActiveText
		}
		return renderResponsiveGroupListRow(p, selected,
			[]rowCell{
				leftCell(style.Render(glyph), listIconWidth),
				leftCell(nameStyle.Render(fitCellText(entry.Name, cols.name)), cols.name),
			},
			[]rowCell{
				rightCell(style.Render(status), statusWidth),
				rightCell(p.styleProvider.Render(fitCellText(entry.Marketplace, cols.targets)), cols.targets),
			},
			rowAvailableWidth(m.width), listColumnGap, listColumnGap,
		)
	}, func(i int) ([]string, []string) {
		entry := entries[i]
		return nil, m.agentsDetailBlock(entry.Description, agentsRegistryDetails(entry), hintCtxAgentsRegistryRow)
	})

	sections := []sectionedTabSection{rows}
	if len(entries) == 0 {
		empty := rowContentInset() + "No plugins match the filter."
		if len(m.agentsRegistry) == 0 {
			empty = rowContentInset() + app.AgentsRegistryEmptyNotice
		}
		sections = []sectionedTabSection{{empty: []string{p.styleHelp.Render(empty)}}}
	}
	return renderSectionedTab(m, sectionedTab{
		top:      m.agentsBodyTopLines(),
		footer:   m.agentsFooterLines(),
		sections: sections,
	})
}

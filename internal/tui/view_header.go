package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
)

func renderSetup(m Model) string {
	var body string
	switch m.setupStep {
	case 0:
		body = renderSetupPanel(m, setupPanel{
			Lead: "Manage packages and dotfiles from one settings.json.",
			Help: []string{"No config exists yet. Create one to get started?"},
			Footer: renderSetupFooter(m,
				[]hintItem{dangerRawHint("esc", "quit")},
				nil,
				[]hintItem{hintFromBindingDesc(m.keys.Confirm, "create settings.json")},
			),
		})
	case 1, 2:
		body = renderProviderPickerStep(m, m.setupStep)
	case 3:
		var options []setupOption
		for i, c := range nodeMgrChoices {
			options = append(options, setupOption{Label: c.label, Detail: c.desc, Selected: i == m.setupNodeMgrIdx})
		}
		body = renderSetupPanel(m, setupPanel{
			Lead: "Choose a Node.js package manager.",
			Help: []string{
				"If you have multiple installed, omni will use this one.",
				"You can always change this later in Settings.",
			},
			Body: renderSetupOptions(m, options),
			Footer: renderSetupFooter(m,
				[]hintItem{hintFromBindingDesc(m.keys.Back, "skip")},
				nil,
				[]hintItem{hintFromBindingDesc(m.keys.Confirm, "confirm")},
			),
		})
	case 5:
		body = renderSetupPanel(m, setupPanel{
			Lead: "Enable dotfile sync?",
			Help: []string{
				"omni can manage your config symlinks from a git repository,",
				"keeping dotfiles in sync across machines.",
			},
			Footer: renderSetupFooter(m,
				[]hintItem{hintFromBindingDesc(m.keys.Back, "skip for now")},
				nil,
				[]hintItem{hintFromBindingDesc(m.keys.Confirm, "set up dotfile sync")},
			),
		})
	case 6:
		// File picker overlays the full screen; this header is not reached during
		// normal flow since renderFilePicker takes over when showFilePicker=true.
		// Shown only as a placeholder if the picker is somehow not yet active.
		body = renderSetupPanel(m, setupPanel{
			Lead: "Dotfiles repo path",
			Help: []string{"Browse to your local dotfiles git repository."},
			Footer: renderSetupFooter(m,
				[]hintItem{hintFromBindingDesc(m.keys.Back, "skip")},
				nil,
				nil,
			),
		})
	case 7:
		count := len(m.setupCopyHostNames())
		hostWord := "hosts"
		if count == 1 {
			hostWord = "host"
		}
		body = renderSetupPanel(m, setupPanel{
			Lead: "Copy another host's config?",
			Help: []string{
				fmt.Sprintf("Found %d existing %s in this config.", count, hostWord),
				"Copying brings over reusable groups, host settings, and host-specific overrides.",
				"Machine-local entries stay with the source host.",
			},
			Footer: renderSetupFooter(m,
				[]hintItem{hintFromBindingDesc(m.keys.Back, "start fresh")},
				nil,
				[]hintItem{hintFromBindingDesc(m.keys.Confirm, "copy host")},
			),
		})
	case 8:
		var options []setupOption
		names := m.setupCopyHostNames()
		for i, name := range names {
			detail := "no reusable groups"
			if m.hostInfo != nil {
				if host, ok := m.hostInfo.Hosts[name]; ok && len(host.Groups) > 0 {
					detail = compactGroupList(host.Groups)
				}
			}
			options = append(options, setupOption{Label: name, Detail: detail, Selected: i == m.setupCopyHostIdx})
		}
		body = renderSetupPanel(m, setupPanel{
			Lead: "Choose the host to copy.",
			Help: []string{
				"The new host will receive the selected host's reusable groups and host-scoped settings.",
			},
			Body: renderSetupOptions(m, options),
			Footer: renderSetupFooter(m,
				[]hintItem{hintFromBindingDesc(m.keys.Back, "start fresh")},
				nil,
				[]hintItem{hintFromBindingDesc(m.keys.Confirm, "copy selected")},
			),
		})
	case 9:
		var options []setupOption
		for i, name := range m.groupNames {
			checked := m.setupGroupDraft[name]
			options = append(options, setupOption{Label: name, Selected: i == m.setupGroupIdx, Checked: &checked})
		}
		help := []string{"Choose any reusable groups this host should use."}
		optionsBody := renderSetupOptions(m, options)
		if len(options) == 0 {
			help = append(help, "No reusable groups are configured yet.")
		}
		var middle []hintItem
		if len(options) > 0 {
			middle = []hintItem{hintFromBindingDesc(m.keys.Toggle, "toggle")}
		}
		body = renderSetupPanel(m, setupPanel{
			Lead:   "Add existing groups to this host.",
			Help:   help,
			Body:   optionsBody,
			Footer: renderSetupFooter(m, []hintItem{hintFromBindingDesc(m.keys.Back, "skip")}, middle, []hintItem{hintFromBindingDesc(m.keys.Confirm, "continue")}),
		})
	case 10:
		var options []setupOption
		for i, opt := range setupActivationOptions {
			options = append(options, setupOption{Label: opt.label, Detail: opt.detail, Selected: i == m.setupActivationIdx})
		}
		host := shortHostname()
		groupSummary := "no reusable groups"
		if m.hostInfo != nil && m.hostInfo.Active != "" {
			host = m.hostInfo.Active
			if assignment, ok := m.hostInfo.Hosts[host]; ok && len(assignment.Groups) > 0 {
				groupSummary = compactGroupList(assignment.Groups)
			}
		}
		dotsSummary := "dotfiles disabled"
		if strings.TrimSpace(m.settings.DotsRepo) != "" && !config.BoolVal(m.settings.DotsDisabled) {
			dotsSummary = "dotfiles enabled"
		}
		body = renderSetupPanel(m, setupPanel{
			Lead: fmt.Sprintf("Host %q is configured.", host),
			Help: []string{
				"Choose how to activate this machine before entering Omni.",
				"Groups: " + groupSummary + " · " + dotsSummary,
			},
			Body: renderSetupOptions(m, options),
			Footer: renderSetupFooter(m,
				[]hintItem{hintFromBindingDesc(m.keys.Back, "review first")},
				nil,
				[]hintItem{hintFromBindingDesc(m.keys.Confirm, "continue")},
			),
		})
	}

	return body
}

type setupPanel struct {
	Lead   string
	Help   []string
	Body   string
	Footer string
}

type setupOption struct {
	Label    string
	Detail   string
	Selected bool
	Checked  *bool
}

func renderSetupPanel(m Model, panel setupPanel) string {
	p := m.palette
	var sections []string
	if panel.Lead != "" {
		sections = append(sections, p.styleNormal.Render(panel.Lead))
	}
	if len(panel.Help) > 0 {
		var help strings.Builder
		for i, line := range panel.Help {
			if i > 0 {
				help.WriteByte('\n')
			}
			help.WriteString(p.styleHelp.Render(line))
		}
		sections = append(sections, help.String())
	}
	if panel.Body != "" {
		sections = append(sections, panel.Body)
	}
	if panel.Footer != "" {
		sections = append(sections, panel.Footer)
	}
	return strings.Join(sections, "\n\n")
}

func renderSetupFooter(m Model, left, middle, right []hintItem) string {
	return renderPopupActionColumns(m.palette, max(m.width, 1), left, middle, right)
}

func renderSetupOptions(m Model, options []setupOption) string {
	p := m.palette

	// Compute fixed-width prefix: cursor (2) + optional checkbox (4).
	hasCheck := false
	for _, opt := range options {
		if opt.Checked != nil {
			hasCheck = true
			break
		}
	}
	prefixW := 2 // pickerCursor width
	if hasCheck {
		prefixW += 4 // "[x] "
	}

	// Find widest label so descriptions align.
	maxLabelW := 0
	for _, opt := range options {
		if w := len([]rune(opt.Label)); w > maxLabelW {
			maxLabelW = w
		}
	}

	const detailGap = 2 // spaces between label column and description
	descCol := prefixW + maxLabelW + detailGap
	availW := max(m.width-descCol, 10)

	var sb strings.Builder
	for i, opt := range options {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(pickerCursor(p, opt.Selected))
		if opt.Checked != nil {
			mark := "[ ]"
			markStyle := p.styleHelp
			if *opt.Checked {
				mark = "[x]"
				markStyle = p.styleInstalled
			}
			sb.WriteString(markStyle.Render(mark))
			sb.WriteByte(' ')
		}
		labelStyle := p.styleNormal
		if opt.Selected {
			labelStyle = p.styleActiveText
		}
		label := opt.Label
		pad := maxLabelW - len([]rune(label))
		sb.WriteString(labelStyle.Render(label))
		if opt.Detail != "" {
			sb.WriteString(strings.Repeat(" ", pad+detailGap))
			lines := wrapText(opt.Detail, availW)
			indent := strings.Repeat(" ", descCol)
			for j, line := range lines {
				if j > 0 {
					sb.WriteByte('\n')
					sb.WriteString(indent)
				}
				sb.WriteString(p.styleHelp.Render(line))
			}
		}
	}
	return sb.String()
}

func renderSetupPopup(m Model, width int) string {
	popupModel := m
	popupModel.width = max(width, 1)
	return renderSetup(popupModel)
}

func renderPostSetupLoading(m Model) string {
	p := m.palette
	text := m.progressText
	if text == "" {
		text = activityLabel(m)
	}
	rawLines := []string{
		p.styleTitle.Bold(true).Render(loadingGlobeFrame()),
		"",
		postSetupLoadingTextStyle(p, text).Render(text),
		p.styleHelp.Render("Scanning configured package ecosystems."),
	}
	width := 0
	for _, line := range rawLines {
		width = max(width, lipgloss.Width(line))
	}
	lines := make([]string, len(rawLines))
	for i, line := range rawLines {
		lines[i] = centerPopupLine(line, width)
	}
	return strings.Join(lines, "\n")
}

func postSetupLoadingTextStyle(p palette, text string) lipgloss.Style {
	if isProviderRefreshText(text) {
		return p.styleNormal.Bold(true)
	}
	return p.styleNormal
}

func loadingGlobeFrame() string {
	if logoMark == "o" {
		return logoMark
	}
	frames := []string{"🌍", "🌎", "🌏"}
	return frames[int(time.Now().UnixMilli()/250)%len(frames)]
}

func centerPopupLine(line string, width int) string {
	pad := max((max(width, 1)-lipgloss.Width(line))/2, 0)
	return strings.Repeat(" ", pad) + line
}

func setupPopupFrame(m Model) popupFrame {
	return popupFrame{
		Title:    setupPopupTitle(m),
		PaddingY: 1,
		PaddingX: 3,
		Width:    clampPopupDimension(64, 44, popupFrameMaxWidth(m)),
	}
}

func setupPopupTitle(m Model) string {
	switch m.setupStep {
	case 0:
		return logoMark + " Omni - Create settings"
	case 1:
		return logoMark + " Omni - Import tools"
	case 2:
		return logoMark + " Omni - Enable ecosystems"
	case 3:
		return logoMark + " Omni - Choose Node manager"
	case 5:
		return logoMark + " Omni - Dotfile sync"
	case 6:
		return logoMark + " Omni - Choose dotfiles repo"
	case 7:
		return logoMark + " Omni - Copy host"
	case 8:
		return logoMark + " Omni - Choose host"
	case 9:
		return logoMark + " Omni - Choose groups"
	case 10:
		return logoMark + " Omni - Bootstrap host"
	default:
		return logoMark + " Omni"
	}
}

func renderHeader(m Model) string {
	p := m.palette
	title := p.styleTitle.PaddingLeft(0).Render(screenEdgeInset() + logoMark)
	tabs := renderTabs(m)

	right := renderHeaderInfo(m)

	gap := max(screenContentWidth(m.width)-lipgloss.Width(title)-lipgloss.Width(tabs)-lipgloss.Width(right), 0)
	return title + tabs + strings.Repeat(" ", gap) + right
}

func renderHeaderInfo(m Model) string {
	switch m.mode {
	case viewDots:
		return renderDotsHeaderInfo(m)
	case viewGroups:
		return renderGroupsHeaderInfo(m)
	case viewStatus:
		return renderStatusHeaderInfo(m)
	case viewSettings:
		return renderSettingsHeaderInfo(m)
	default:
		return renderToolsHeaderInfo(m)
	}
}

func renderToolsHeaderInfo(m Model) string {
	p := m.palette
	updates := m.sectionCounts[sectionUpdates]
	var counts string
	if updates > 0 {
		counts = fmt.Sprintf("  %d updates  ", updates)
	} else {
		counts = "  "
	}
	counts += fmt.Sprintf("%d tools", len(m.allTools))
	if len(m.searchTools) > 0 {
		counts += fmt.Sprintf(" +%d found", len(m.searchTools))
	}
	return renderHeaderInfoText(p, counts)
}

func renderDotsHeaderInfo(m Model) string {
	if config.BoolVal(m.settings.DotsDisabled) {
		return ""
	}
	p := m.palette
	counts := dotHeaderCounts(m.dotsEntries)
	parts := []string{"  " + dotRatioText(counts)}
	if ignored := dotIgnoredText(counts); ignored != "" {
		parts = append(parts, ignored)
	}
	return renderHeaderInfoText(p, strings.Join(parts, " "))
}

func dotHeaderCounts(entries []app.DotStatus) app.DotFileCounts {
	var total app.DotFileCounts
	for _, entry := range entries {
		if dotStatusState(entry) == app.DotStateIgnored {
			continue
		}
		counts := dotEntryCounts(entry)
		total.Synced += counts.Synced
		total.OutOfSync += counts.OutOfSync
		total.Ignored += counts.Ignored
	}
	return total
}

func renderGroupsHeaderInfo(m Model) string {
	groups := len(buildAllGroupNames(m.groupNames))
	hosts := len(sortedHostNames(m.hostInfo))
	return renderHeaderInfoText(m.palette, "  "+compactCount(groups, "group")+"  "+compactCount(hosts, "host"))
}

func renderSettingsHeaderInfo(m Model) string {
	enabled := 3 - disabledEcosystemProviderCount(m.settings.DisabledProviders)
	dotsLabel := "dots unset"
	if config.BoolVal(m.settings.DotsDisabled) {
		dotsLabel = "dots off"
	} else if m.settings.DotsRepo != "" {
		dotsLabel = "dots on"
	}
	return renderHeaderInfoText(m.palette, fmt.Sprintf("  %d/3 providers  %s", enabled, dotsLabel))
}

func renderStatusHeaderInfo(m Model) string {
	if len(m.allTools) == 0 || m.launchBatchActive {
		return ""
	}
	attention := statusAttentionCount(m)
	if m.doctorRunning {
		if attention > 0 {
			return renderHeaderInfoText(m.palette, fmt.Sprintf("  checking…  %d need attention", attention))
		}
		return renderHeaderInfoText(m.palette, "  checking…")
	}
	if attention == 0 {
		return renderHeaderInfoText(m.palette, "  all clear")
	}
	return renderHeaderInfoText(m.palette, fmt.Sprintf("  %d need attention", attention))
}

func renderHeaderInfoText(p palette, text string) string {
	if text == "" {
		return ""
	}
	return lipgloss.NewStyle().Foreground(p.colMuted).Render(text)
}

func disabledEcosystemProviderCount(disabled []string) int {
	seen := make(map[string]bool, 3)
	for _, name := range disabled {
		switch name {
		case provider.EcosystemSystem, provider.EcosystemNode, provider.EcosystemPython:
			seen[name] = true
		}
	}
	return len(seen)
}

func renderTabs(m Model) string {
	p := m.palette
	active := func(label string) string { return p.styleStatus.Render("  " + label) }
	inactive := func(label string) string { return p.styleHelp.Render("  " + label) }

	activeTab := activeTabMode(m.mode)
	var sb strings.Builder
	for _, tab := range mainTabs() {
		if activeTab == tab.mode {
			sb.WriteString(active(tab.label))
		} else {
			sb.WriteString(inactive(tab.label))
		}
	}
	return sb.String()
}

// activeTabMode maps any viewMode to the parent tab that should be highlighted.
func activeTabMode(mode viewMode) viewMode {
	switch mode {
	case viewSearch, viewCommand, viewGroupPicker,
		viewIgnoreScope, viewProviderScope, viewAdminTerminal:
		return viewList
	case viewGroupTools, viewGroupDots:
		return viewGroups
	case viewGroupMembership:
		// membership picker can overlay tools or dots; default to tools
		return viewList
	default:
		return mode
	}
}

type mainTabHitZone struct {
	mode       viewMode
	start, end int
}

type mainTab struct {
	mode  viewMode
	label string
}

func mainTabs() []mainTab {
	return []mainTab{
		{mode: viewStatus, label: "Dashboard"},
		{mode: viewList, label: "Tools"},
		{mode: viewDots, label: "Dots"},
		{mode: viewGroups, label: "Groups"},
		{mode: viewSettings, label: "Settings"},
	}
}

func mainTabHitZones(m Model) []mainTabHitZone {
	titleW := lipgloss.Width(m.palette.styleTitle.PaddingLeft(0).Render(screenEdgeInset() + logoMark))
	tabs := mainTabs()
	zones := make([]mainTabHitZone, 0, len(tabs))
	x := titleW
	for _, tab := range tabs {
		label := "  " + tab.label
		w := lipgloss.Width(label)
		zones = append(zones, mainTabHitZone{mode: tab.mode, start: x, end: x + w})
		x += w
	}
	return zones
}

func mainTabAtPosition(m Model, x, y int) (viewMode, bool) {
	if y != 0 {
		return viewList, false
	}
	for _, zone := range mainTabHitZones(m) {
		if x >= zone.start && x < zone.end {
			return zone.mode, true
		}
	}
	return viewList, false
}

// renderPalette renders the command suggestion list.
func renderPalette(m Model) string {
	p := m.palette
	if len(m.commandSuggestions) == 0 {
		return p.styleHelp.Render("  no matching commands") + "\n"
	}
	cursor := m.commandCursor
	t := table.New().
		BorderTop(false).BorderBottom(false).
		BorderLeft(false).BorderRight(false).
		BorderHeader(false).BorderColumn(false).
		StyleFunc(func(row, col int) lipgloss.Style {
			sel := row == cursor
			if col == 0 {
				if sel {
					return p.styleActiveText.PaddingLeft(2).PaddingRight(3)
				}
				return p.styleNormal.PaddingLeft(2).PaddingRight(3)
			}
			return p.styleHelp
		})
	for _, cmd := range m.commandSuggestions {
		t.Row(cmd.name, cmd.desc)
	}
	return t.String() + "\n"
}

// renderProviderPickerStep renders the shared provider selection UI used in
// setup steps 1 (first-run import) and 2 (no-host re-run). The only
// difference between the two steps is the introductory subtitle text.
func renderProviderPickerStep(m Model, step int) string {
	lead := "Choose which package ecosystems to enable on this machine."
	help := []string{"Disabled ecosystems can be re-enabled later in Settings."}
	if step == 1 {
		lead = "Choose which ecosystems to import on this machine."
		help = []string{"Enabled ecosystems will be imported from your existing tools."}
	}

	spCursor := m.setupProviderIdx
	options := make([]setupOption, 0, len(m.setupProviders))
	for i, row := range m.setupProviders {
		enabled := row.enabled
		options = append(options, setupOption{
			Label:    row.label,
			Selected: i == spCursor,
			Checked:  &enabled,
		})
	}
	return renderSetupPanel(m, setupPanel{
		Lead: lead,
		Help: help,
		Body: renderSetupOptions(m, options),
		Footer: renderSetupFooter(m,
			nil,
			[]hintItem{hintFromBindingDesc(m.keys.Toggle, "toggle")},
			[]hintItem{hintFromBindingDesc(m.keys.Confirm, "save & continue")},
		),
	})
}

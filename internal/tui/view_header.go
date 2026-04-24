package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/list"
	"charm.land/lipgloss/v2/table"
)

func renderSetup(m Model) string {
	p := m.palette
	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(p.styleTitle.PaddingLeft(0).Render(screenEdgeInset() + logoMark + " Omni - Manage all your packages and dotfiles"))
	sb.WriteString("\n\n")
	sb.WriteString(renderHRule(p, m.width))
	sb.WriteString("\n\n")

	switch m.setupStep {
	case 0:
		sb.WriteString(p.styleNormal.Render("  No settings.json found."))
		sb.WriteString("\n")
		sb.WriteString(p.styleHelp.Render("  Create one now to start managing tools?"))
		sb.WriteString("\n\n")
		sb.WriteString(p.styleInstalled.Render("  [y] ") + p.styleNormal.Render("Yes, create settings.json"))
		sb.WriteString("\n")
		sb.WriteString(p.styleMissing.Render("  [n] ") + p.styleNormal.Render("No, quit"))
	case 1, 2:
		sb.WriteString(renderProviderPickerStep(m, m.setupStep))
	case 3:
		sb.WriteString(p.styleNormal.Render("  Choose a Node.js package manager."))
		sb.WriteString("\n\n")
		sb.WriteString(p.styleHelp.Render("  If you have multiple installed, omni will use this one."))
		sb.WriteString("\n")
		sb.WriteString(p.styleHelp.Render("  You can always change this later in Settings."))
		sb.WriteString("\n\n")
		for i, c := range nodeMgrChoices {
			var bullet string
			if i == m.setupNodeMgrIdx {
				bullet = p.styleInstalled.Render("  \u25b6 ")
			} else {
				bullet = p.styleHelp.Render("    ")
			}
			var label string
			if i == m.setupNodeMgrIdx {
				label = p.styleActiveText.Render(c.label)
			} else {
				label = p.styleNormal.Render(c.label)
			}
			desc := p.styleHelp.Render("  " + c.desc)
			sb.WriteString(bullet + label + desc)
			sb.WriteByte('\n')
		}
		sb.WriteString("\n")
		sb.WriteString(renderContextHints(m, hintCtxSetupNodeManager, textRowHintPrefix()))
	case 4:
		if m.setupExitConfirm {
			sb.WriteString(p.styleMissing.Render("  A profile is required to use omni."))
			sb.WriteString("\n\n")
			sb.WriteString(p.styleHelp.Render("  Skipping profile setup will close omni."))
			sb.WriteString("\n")
			sb.WriteString(p.styleHelp.Render("  You can always create a profile later with 'omni profile add <name>'."))
			sb.WriteString("\n\n")
			sb.WriteString(p.styleMissing.Render("  [y] ") + p.styleNormal.Render("Close omni"))
			sb.WriteString("\n")
			sb.WriteString(p.styleInstalled.Render("  [n] ") + p.styleNormal.Render("Go back and create a profile"))
		} else {
			sb.WriteString(p.styleNormal.Render("  Create a profile for this machine."))
			sb.WriteString("\n\n")
			sb.WriteString(p.styleHelp.Render("  Profiles let you keep per-machine tool sets and ignore lists."))
			sb.WriteString("\n")
			sb.WriteString(p.styleHelp.Render("  A profile is required — omni cannot run without one."))
			sb.WriteString("\n\n")
			inputWidth := max(m.width-14, 20)
			m.settingsInput.SetWidth(inputWidth)
			sb.WriteString(p.styleNormal.Render("  Name: ") + "[ " + m.settingsInput.View() + " ]")
			sb.WriteString("\n\n")
			sb.WriteString(renderActionHints(p, actionHints{
				prefix: "  ",
				items:  []hintItem{hintFromBindingDesc(m.keys.Confirm, "create profile")},
			}))
		}
	case 5:
		sb.WriteString(p.styleNormal.Render("  Enable dotfile sync?"))
		sb.WriteString("\n\n")
		sb.WriteString(p.styleHelp.Render("  omni can manage your config symlinks from a git repository,"))
		sb.WriteString("\n")
		sb.WriteString(p.styleHelp.Render("  keeping dotfiles in sync across machines."))
		sb.WriteString("\n\n")
		sb.WriteString(p.styleInstalled.Render("  [y] ") + p.styleNormal.Render("Yes, set up dotfile sync"))
		sb.WriteString("\n")
		sb.WriteString(p.styleMissing.Render("  [n] ") + p.styleNormal.Render("No, skip for now"))
	case 6:
		// File picker overlays the full screen; this header is not reached during
		// normal flow since renderFilePicker takes over when showFilePicker=true.
		// Shown only as a placeholder if the picker is somehow not yet active.
		sb.WriteString(p.styleNormal.Render("  Dotfiles repo path"))
		sb.WriteString("\n\n")
		sb.WriteString(p.styleHelp.Render("  Browse to your local dotfiles git repository."))
		sb.WriteString("\n")
		sb.WriteString(renderActionHints(p, actionHints{
			prefix: "  ",
			items:  []hintItem{hintFromBindingDesc(m.keys.Back, "skip")},
		}))
	}

	if m.loading {
		sb.WriteString("\n\n  " + m.spinner.View() + " " + p.styleStatus.Render(m.statusMsg))
	}
	return sb.String()
}

func renderSetupPopup(m Model) string {
	popupModel := m
	popupModel.width = min(max(m.width-10, 40), 80)
	return renderSetup(popupModel)
}

func renderHeader(m Model) string {
	p := m.palette
	title := p.styleTitle.PaddingLeft(0).Render(screenEdgeInset() + logoMark)
	tabs := renderTabs(m)

	var right string
	if m.mode == viewDots {
		counts := fmt.Sprintf("  %d entries", len(m.dotsEntries))
		if m.dotsGitStatus != "" {
			counts += "  " + p.styleOutdated.Render("dirty")
		}
		right = lipgloss.NewStyle().Foreground(p.colMuted).Render(counts)
	} else if m.searching {
		right = lipgloss.NewStyle().Foreground(p.colMuted).Render("  searching…")
	} else if len(m.scanningProviders) > 0 {
		right = lipgloss.NewStyle().Foreground(p.colMuted).Render("  scanning…")
	} else {
		updates := m.sectionCounts[sectionUpdates]
		var counts string
		if updates > 0 {
			counts = "  " + p.styleOutdated.Render(fmt.Sprintf("%d updates", updates)) + "  "
		} else {
			counts = "  "
		}
		counts += fmt.Sprintf("%d tools", len(m.allTools))
		if len(m.searchTools) > 0 {
			counts += fmt.Sprintf(" +%d found", len(m.searchTools))
		}
		right = lipgloss.NewStyle().Foreground(p.colMuted).Render(counts)
	}

	gap := max(screenContentWidth(m.width)-lipgloss.Width(title)-lipgloss.Width(tabs)-lipgloss.Width(right), 0)
	return title + tabs + strings.Repeat(" ", gap) + right
}

func renderTabs(m Model) string {
	p := m.palette
	active := func(label string) string { return p.styleActiveText.Render("  " + label) }
	inactive := func(label string) string { return p.styleHelp.Render("  " + label) }

	toolsTab := inactive("Tools")
	dotsTab := inactive("Dots")
	profilesTab := inactive("Profiles")
	optsTab := inactive("Settings")
	switch m.mode {
	case viewDots:
		dotsTab = active("Dots")
	case viewProfiles:
		profilesTab = active("Profiles")
	case viewSettings:
		optsTab = active("Settings")
	default:
		toolsTab = active("Tools")
	}
	return toolsTab + dotsTab + profilesTab + optsTab
}

type mainTabHitZone struct {
	mode       viewMode
	start, end int
}

func mainTabHitZones(m Model) []mainTabHitZone {
	titleW := lipgloss.Width(m.palette.styleTitle.PaddingLeft(0).Render(screenEdgeInset() + logoMark))
	labels := []struct {
		mode  viewMode
		label string
	}{
		{viewList, "  Tools"},
		{viewDots, "  Dots"},
		{viewProfiles, "  Profiles"},
		{viewSettings, "  Settings"},
	}
	zones := make([]mainTabHitZone, 0, len(labels))
	x := titleW
	for _, label := range labels {
		w := lipgloss.Width(label.label)
		zones = append(zones, mainTabHitZone{mode: label.mode, start: x, end: x + w})
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
// setup steps 1 (first-run import) and 2 (no-profile re-run). The only
// difference between the two steps is the introductory subtitle text.
func renderProviderPickerStep(m Model, step int) string {
	p := m.palette
	var sb strings.Builder

	// Step-specific preamble.
	if step == 1 {
		sb.WriteString(p.styleInstalled.Render("  ✓ ") + p.styleNormal.Render("settings.json created."))
		sb.WriteString("\n\n")
		sb.WriteString(p.styleNormal.Render("  Choose which ecosystems to enable on this machine."))
		sb.WriteString("\n\n")
		sb.WriteString(renderActionHints(p, actionHints{
			prefix: "  ",
			items: []hintItem{
				hintFromBindingDesc(m.keys.Toggle, "toggle"),
				hintFromBindingDesc(m.keys.Confirm, "confirm"),
			},
		}))
		sb.WriteString("\n")
		sb.WriteString(p.styleHelp.Render("  Enabled ecosystems will be imported from your existing tools."))
	} else {
		sb.WriteString(p.styleNormal.Render("  Choose which package ecosystems to enable on this machine."))
		sb.WriteString("\n\n")
		sb.WriteString(renderActionHints(p, actionHints{
			prefix: "  ",
			items: []hintItem{
				hintFromBindingDesc(m.keys.Toggle, "toggle"),
				hintFromBindingDesc(m.keys.Confirm, "confirm"),
			},
		}))
		sb.WriteString("\n")
		sb.WriteString(p.styleHelp.Render("  Disabled ecosystems can be re-enabled later in Settings."))
	}
	sb.WriteString("\n\n")

	// Shared provider list.
	spCursor := m.setupProviderIdx
	spItems := make([]any, len(m.setupProviders))
	for i, row := range m.setupProviders {
		var labelStyle lipgloss.Style
		if row.enabled {
			labelStyle = p.styleNormal
		} else {
			labelStyle = p.styleHelp
		}
		if i == spCursor {
			labelStyle = labelStyle.Bold(true)
		}
		spItems[i] = labelStyle.Render(row.label)
	}
	spl := list.New(spItems...).
		Enumerator(func(_ list.Items, i int) string {
			if m.setupProviders[i].enabled {
				return "[✓]"
			}
			return "[ ]"
		}).
		EnumeratorStyleFunc(func(_ list.Items, i int) lipgloss.Style {
			s := lipgloss.NewStyle().PaddingLeft(2).PaddingRight(1)
			if m.setupProviders[i].enabled {
				return s.Foreground(p.colInstalled)
			}
			return s.Foreground(p.colMuted)
		}).
		ItemStyleFunc(func(_ list.Items, _ int) lipgloss.Style {
			return lipgloss.NewStyle() // item text is pre-styled
		})
	sb.WriteString(spl.String() + "\n")
	sb.WriteString("\n")
	sb.WriteString(renderActionHints(p, actionHints{
		prefix: "  ",
		items:  []hintItem{hintFromBindingDesc(m.keys.Confirm, "save & continue")},
	}))
	return sb.String()
}

package tui

import (
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/app"
)

const pillBarSeparator = "   ·   "

// Collapses to the host pill plus " +N" when the full pill row would exceed maxW; the optional emphasis transform applies the row's state styling to every pill, nil renders as-is.
func renderGroupPills(p palette, groups []string, info *app.HostInfo, maxW int, emphasis func(lipgloss.Style) lipgloss.Style) string {
	if emphasis == nil {
		emphasis = func(s lipgloss.Style) lipgloss.Style { return s }
	}
	ordered := app.HostFirstGroups(groups, info)
	if len(ordered) == 0 {
		return ""
	}
	pillStyle := func(name string) lipgloss.Style {
		if app.IsHostGroup(name, info) {
			return emphasis(p.styleProvider)
		}
		return emphasis(p.styleHelp)
	}
	pill := func(name string) string {
		return pillStyle(name).Render("[" + name + "]")
	}
	suffixStyle := emphasis(p.styleHelp)
	parts := make([]string, 0, len(ordered))
	for _, name := range ordered {
		parts = append(parts, pill(name))
	}
	full := strings.Join(parts, " ")
	if maxW <= 0 || lipgloss.Width(full) <= maxW {
		return full
	}
	head := ordered[0]
	suffix := ""
	if len(ordered) > 1 {
		suffix = " +" + strconv.Itoa(len(ordered)-1)
	}
	collapsed := pill(head) + suffixStyle.Render(suffix)
	if lipgloss.Width(collapsed) <= maxW {
		return collapsed
	}
	// Truncate the plain name, never the already-styled ANSI text, then re-wrap it in its pill style.
	budget := maxW - 2 - lipgloss.Width(suffix)
	if budget < 1 {
		return ""
	}
	return pillStyle(head).Render("["+fitCellText(head, budget)+"]") + suffixStyle.Render(suffix)
}

func fullMembershipLabel(groups []string) string {
	groups = uniqueGroups(groups)
	slices.Sort(groups)
	return strings.Join(groups, ", ")
}

func fullMembershipDetailLines(groups []string, width int) []string {
	label := fullMembershipLabel(groups)
	if label == "" {
		return nil
	}
	width = max(width, 1)
	remaining := "groups: " + label
	lines := make([]string, 0, lipgloss.Width(remaining)/width+1)
	for lipgloss.Width(remaining) > width {
		cut := 0
		for i, r := range remaining {
			next := i + len(string(r))
			if lipgloss.Width(remaining[:next]) > width {
				break
			}
			cut = next
		}
		if cut == 0 {
			_, size := utf8.DecodeRuneInString(remaining)
			cut = size
		}
		lines = append(lines, remaining[:cut])
		remaining = remaining[cut:]
	}
	if remaining != "" {
		lines = append(lines, remaining)
	}
	return lines
}

type rowColWidthMeasure struct {
	name, prov, ver, group func(i int) string
	priv                   func(i int) bool
}

// Seed from floor widths, widen to the widest item content, cap the version column at verReserveW, then shrink via fitToolColumnsToScreen so the row fits screenW.
func seedWidenCapShrinkColWidths(seed colWidths, n int, measure rowColWidthMeasure) colWidths {
	cols := seed
	for i := 0; i < n; i++ {
		if measure.name != nil {
			if w := lipgloss.Width(measure.name(i)); w > cols.name {
				cols.name = w
			}
		}
		if measure.prov != nil {
			if w := lipgloss.Width(measure.prov(i)); w > cols.prov {
				cols.prov = w
			}
		}
		if measure.ver != nil {
			if w := len([]rune(measure.ver(i))); w > cols.ver {
				cols.ver = w
			}
		}
		if measure.group != nil {
			if g := measure.group(i); g != "" {
				if w := lipgloss.Width(g); w > cols.group {
					cols.group = w
				}
			}
		}
		if measure.priv != nil && measure.priv(i) {
			cols.priv = lipgloss.Width(iconPrivileged)
		}
	}
	cols.ver = min(cols.ver, verReserveW)
	return fitToolColumnsToScreen(cols)
}

func rowEmphasis(selected bool, s lipgloss.Style) lipgloss.Style {
	if selected {
		return s.Bold(true)
	}
	return s
}

// Pills arrive pre-styled and pre-fit to colW, so this must not re-style or fitCellText the already-ANSI text.
func rowGroupPillsCell(pills string, colW int) []rowCell {
	if colW == 0 {
		return nil
	}
	return []rowCell{rightCell(pills, colW)}
}

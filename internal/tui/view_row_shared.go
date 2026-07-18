package tui

import (
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/app"
)

// pillBarSeparator divides adjacent pill bars (provider/group on the tools
// tab, type/agent-ID on the agents tab) when both are rendered on one line.
const pillBarSeparator = "   ·   "

// renderGroupPills renders an item's group memberships as host-first pills:
// the host group (if any) in a visually distinct style, followed by
// reusable-group pills in the normal group style. When the full pill row
// would exceed maxW (if maxW > 0), it collapses to the host pill plus an
// " +N" count of the remaining groups. Returns "" when there are no visible
// groups.
// renderGroupPills renders an item's memberships as host-first pills. The
// optional emphasis transform lets a row apply its own state styling (bold when
// selected, muted when ignored) to every pill; nil means render as-is.
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
	// Even the collapsed form (host pill plus "+N") overflows maxW — the
	// head group's own name is too long. Truncate the plain name (never the
	// already-styled ANSI text) to fit, then re-wrap it in its pill style.
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

// rowColWidthMeasure supplies the per-item content widths a row table needs
// to size its columns: display name, provider/agent-label column, version
// column, group badge (pre-bracketed, "" when absent), and whether the item
// needs the privilege-marker column.
type rowColWidthMeasure struct {
	name, prov, ver, group func(i int) string
	priv                   func(i int) bool
}

// seedWidenCapShrinkColWidths owns the column-sizing algorithm both the
// tools tab and agents tab use: start from floor widths, widen each column
// to fit the widest item content, cap the version column at verReserveW,
// then shrink everything back down via fitToolColumnsToScreen so the row
// fits screenW. n is the item count; measure resolves per-item content.
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

// rowEmphasis bolds a style when the row is selected, otherwise returns it
// unchanged. Both the tools and agents row renderers apply this identically
// to every cell style on the selected row.
func rowEmphasis(selected bool, s lipgloss.Style) lipgloss.Style {
	if selected {
		return s.Bold(true)
	}
	return s
}

// rowGroupPillsCell renders a right-aligned group-pill cell at cols.group
// width, or a blank cell of the same width when there's nothing to show.
// Unlike rowGroupBadgeCell, pills arrive pre-styled (per-pill host/reusable
// coloring from renderGroupPills) and pre-fit to colW, so this must not
// re-style or fitCellText the already-ANSI text.
func rowGroupPillsCell(pills string, colW int) []rowCell {
	if colW == 0 {
		return nil
	}
	return []rowCell{rightCell(pills, colW)}
}

package tui

import "charm.land/lipgloss/v2"

// pillBarSeparator divides adjacent pill bars (provider/group on the tools
// tab, type/agent-ID on the agents tab) when both are rendered on one line.
const pillBarSeparator = "   ·   "

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

// rowGroupBadgeCell renders a right-aligned "[group]" badge cell at cols.group
// width, or a blank cell of the same width when there's nothing to show. Both
// the tools and agents row renderers use this exact shape for their trailing
// group-badge column.
func rowGroupBadgeCell(style lipgloss.Style, group string, colW int) []rowCell {
	if colW == 0 {
		return nil
	}
	if group == "" {
		return []rowCell{rightCell("", colW)}
	}
	return []rowCell{rightCell(style.Render(fitCellText(group, colW)), colW)}
}

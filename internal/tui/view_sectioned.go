package tui

import "strings"

type sectionedTab struct {
	leadingBlank bool
	top          []string
	// pinnedTop and footer render outside the scroll window, so a long row list cannot push them off screen.
	pinnedTop []string
	footer    []string
	sections  []sectionedTabSection
}

type sectionedTabSection struct {
	title            string
	danger           bool
	blankAfterHeader bool
	rows             []sectionedTabRow
	empty            []string
}

type sectionedTabRow struct {
	selected bool
	line     string
	details  []string
}

func renderSectionedTab(m Model, tab sectionedTab) string {
	var buf scrollBuf
	// A badge arriving from an async result can overrun the last known width, and an over-wide line soft-wraps and desynchronises bubbletea's frame diff, so clip every line on the way out.
	write := func(s string) { buf.write(clipLines(s, m.width)) }
	pinned := len(tab.pinnedTop) > 0 || len(tab.footer) > 0
	sections := newListSectionWriter(m.palette, m.width, write)

	if tab.leadingBlank {
		write("\n")
	}
	for _, line := range tab.top {
		write(line + "\n")
	}
	for _, section := range tab.sections {
		if section.title == "" && !section.danger {
			sections.wroteSection = true
		} else if section.danger {
			if sections.wroteSection {
				write("\n")
			}
			write(renderSectionHeaderDanger(m.palette, section.title, m.width) + "\n")
			sections.wroteSection = true
		} else {
			sections.Header(section.title)
		}
		if section.blankAfterHeader {
			write("\n")
		}
		if len(section.rows) == 0 {
			for _, line := range section.empty {
				write(line + "\n")
			}
			continue
		}
		for _, row := range section.rows {
			if row.selected {
				buf.markCursor()
			}
			write(row.line + "\n")
			for _, detail := range row.details {
				if strings.TrimSpace(detail) == "" {
					write("\n")
				} else {
					write(detail + "\n")
				}
			}
			if row.selected && len(row.details) > 0 {
				buf.markCursorEnd()
			}
		}
	}
	if !pinned {
		return buf.render(listAvailableHeight(m))
	}
	var sb strings.Builder
	for _, line := range tab.pinnedTop {
		sb.WriteString(clipLines(line, m.width) + "\n")
	}
	sb.WriteString(buf.render(max(listAvailableHeight(m)-len(tab.pinnedTop)-len(tab.footer)-1, 1)))
	sb.WriteString("\n")
	for _, line := range tab.footer {
		sb.WriteString(clipLines(line, m.width) + "\n")
	}
	return sb.String()
}

func renderFixedGroupListRow(p palette, selected bool, first, rest []rowCell, firstGap, columnGap int) string {
	return listRowPrefix(p, selected) + renderFixedGroupRow(first, rest, firstGap, columnGap)
}

func renderResponsiveGroupListRow(p palette, selected bool, first, rest []rowCell, totalWidth, minGap, columnGap int) string {
	return listRowPrefix(p, selected) + renderSplitRow(first, rest, totalWidth, minGap, columnGap)
}

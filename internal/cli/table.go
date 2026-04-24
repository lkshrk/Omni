package cli

import (
	"fmt"
	"io"
	"strings"
)

type cliTable struct {
	headers []string
	rows    [][]string
}

func newTable(headers ...string) *cliTable {
	return &cliTable{headers: headers}
}

func (t *cliTable) AddRow(cells ...string) {
	t.rows = append(t.rows, cells)
}

func (t *cliTable) Render(w io.Writer) {
	cols := len(t.headers)
	widths := make([]int, cols)
	for i, h := range t.headers {
		widths[i] = len(h)
	}
	for _, row := range t.rows {
		for i, cell := range row {
			if i >= cols {
				break
			}
			if n := len(cell); n > widths[i] {
				widths[i] = n
			}
		}
	}
	writeRow := func(cells []string) {
		for i, width := range widths {
			cell := ""
			if i < len(cells) {
				cell = cells[i]
			}
			if i == cols-1 {
				fmt.Fprint(w, cell)
				continue
			}
			pad := width - len(cell)
			if pad < 0 {
				pad = 0
			}
			fmt.Fprint(w, cell, strings.Repeat(" ", pad+1))
		}
		fmt.Fprintln(w)
	}
	writeRow(t.headers)
	sep := make([]string, cols)
	for i, width := range widths {
		sep[i] = strings.Repeat("-", width)
	}
	writeRow(sep)
	for _, row := range t.rows {
		writeRow(row)
	}
}

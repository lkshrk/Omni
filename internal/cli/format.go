package cli

import "github.com/lkshrk/omni/internal/text"

// wrapText delegates to the shared text.WrapText helper.
func wrapText(t string, width int) []string {
	return text.WrapText(t, width)
}

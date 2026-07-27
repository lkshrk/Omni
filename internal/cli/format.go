package cli

import "github.com/lkshrk/omni/internal/text"

func wrapText(t string, width int) []string {
	return text.WrapText(t, width)
}

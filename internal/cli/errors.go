package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/lkshrk/omni/internal/provider"
)

func printProviderErrorAdvice(w io.Writer, err error) {
	actionErr, ok := provider.ActionErrorFrom(err)
	if !ok || len(actionErr.Solutions) == 0 {
		return
	}
	for _, solution := range actionErr.Solutions {
		label := strings.TrimSpace(solution.Label)
		command := strings.TrimSpace(solution.Command)
		detail := strings.TrimSpace(solution.Detail)
		if label != "" {
			fmt.Fprintln(w, "suggestion:", label)
		}
		if command != "" {
			fmt.Fprintln(w, "  "+command)
		}
		if detail != "" {
			fmt.Fprintln(w, "  "+detail)
		}
	}
}

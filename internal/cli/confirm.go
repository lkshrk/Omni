package cli

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func confirmAction(cmd *cobra.Command, state *rootState, prompt string) (bool, error) {
	if state != nil && state.yes {
		return true, nil
	}

	fmt.Fprintf(cmd.OutOrStdout(), "%s [y/N] ", prompt)
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && len(line) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
		return false, nil
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer == "y" || answer == "yes" {
		return true, nil
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
	return false, nil
}

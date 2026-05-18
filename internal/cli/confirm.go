package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func confirmAction(cmd *cobra.Command, state *rootState, prompt string) (bool, error) {
	if state != nil && state.yes {
		return true, nil
	}

	fmt.Fprintf(cmd.OutOrStdout(), "%s [y/N] ", prompt)
	yes, ok := readYesNo(false)
	out := cmd.OutOrStdout()
	if !ok {
		fmt.Fprintln(out, "Aborted.")
		return false, nil
	}
	if yes {
		fmt.Fprintln(out, "y")
		return true, nil
	}
	fmt.Fprintln(out, "Aborted.")
	return false, nil
}

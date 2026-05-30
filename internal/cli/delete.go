package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/actions"
)

func newDeleteCmd(state *rootState) *cobra.Command {
	var providerName string

	cmd := &cobra.Command{
		Use:   "delete <tool>",
		Short: actions.MustDescription(actions.ToolDelete),
		Long:  actions.MustLongDescription(actions.ToolDelete),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := requireProvider(providerName); err != nil {
				return err
			}
			ok, err := confirmAction(cmd, state, fmt.Sprintf("Delete %s (%s)?", name, providerName))
			if err != nil || !ok {
				return err
			}
			if err := state.app.Uninstall(cmd.Context(), name, providerName); err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "✓ deleted %s (%s)\n", name, providerName)
			return nil
		},
	}

	addProviderFlag(cmd, &providerName, "provider to use for the delete operation")
	cmd.ValidArgsFunction = completeToolNames(state)
	return cmd
}

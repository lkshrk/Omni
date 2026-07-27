package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/actions"
)

func newToolsRemoveCmd(state *rootState) *cobra.Command {
	var providerName string
	var purge bool

	cmd := &cobra.Command{
		Use:   "remove <tool>",
		Short: "Undeclare a logical tool from config",
		Long: "Remove a logical tool and its group memberships from config. The installed package " +
			"stays on this machine, so nothing breaks until another host syncs; pass --purge to " +
			"uninstall it with the resolved provider as well.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !purge {
				return runToolsRemoveSpec(cmd, state, name)
			}
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

	addProviderFlag(cmd, &providerName, "provider to use for the uninstall performed by --purge")
	cmd.Flags().BoolVar(&purge, "purge", false, "also uninstall the tool from this machine")
	cmd.ValidArgsFunction = completeToolNames(state)
	return cmd
}

func newToolsDeleteSpecCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete-spec <name>",
		Short: actions.MustDescription(actions.ToolDeleteSpec),
		Long:  actions.MustLongDescription(actions.ToolDeleteSpec),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runToolsRemoveSpec(cmd, state, args[0])
		},
	}
	cmd.ValidArgsFunction = completeToolNames(state)
	return cmd
}

func runToolsRemoveSpec(cmd *cobra.Command, state *rootState, name string) error {
	ok, err := confirmAction(cmd, state, fmt.Sprintf("Delete logical tool %q and all group memberships?", name))
	if err != nil || !ok {
		return err
	}
	if err := state.app.RemoveLogicalTool(cmd.Context(), name); err != nil {
		return err
	}
	fmt.Fprintf(cmdOut(cmd), "Deleted logical tool %q.\n", name)
	return nil
}

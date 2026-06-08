package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/actions"
)

func newReinstallCmd(state *rootState) *cobra.Command {
	var fromProvider, toProvider string
	var providerName string
	var reinstallDefault bool

	cmd := &cobra.Command{
		Use:   "reinstall <tool>",
		Short: "Reinstall a tool, optionally moving it to a different provider",
		Long: `Reinstall reinstalls a single tool. With --from/--to it moves the tool
between providers: installs via the new provider, removes the old installation
(best-effort), and rewrites the config entry. With --reinstall-default it
reinstalls using the tool's configured default provider.

Examples:
  omni reinstall black --from brew --to pip
  omni reinstall prettier --from brew --to npm
  omni reinstall ripgrep --from brew --to npm
  omni reinstall black --reinstall-default    # ` + actions.MustLongDescription(actions.ToolReinstallDefault) + ``,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if reinstallDefault {
				if fromProvider != "" || toProvider != "" {
					return fmt.Errorf("--from/--to cannot be combined with --reinstall-default")
				}
				ok, err := confirmAction(cmd, state, fmt.Sprintf("Reinstall %s with its configured default provider?", name))
				if err != nil || !ok {
					return err
				}
				result, err := state.app.ReinstallWithDefault(cmd.Context(), name, providerName)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmdOut(cmd), "✓ reinstalled %s with default provider %s\n", name, result.ToProvider)
				if result.UninstallWarning != nil {
					fmt.Fprintf(cmdOut(cmd), "  warning: could not remove old %s installation: %v\n", result.FromProvider, result.UninstallWarning)
				}
				return nil
			}
			if fromProvider == "" {
				return fmt.Errorf("--from is required (e.g. --from brew)")
			}
			if toProvider == "" {
				return fmt.Errorf("--to is required (e.g. --to pip)")
			}
			result, err := state.app.Switch(cmd.Context(), name, fromProvider, toProvider)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "✓ reinstalled %s: %s → %s\n", name, fromProvider, toProvider)
			if result.UninstallWarning != nil {
				fmt.Fprintf(cmdOut(cmd), "  warning: could not remove old %s installation: %v\n", fromProvider, result.UninstallWarning)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&fromProvider, "from", "", "current provider (brew, npm, pip, …)")
	cmd.Flags().StringVar(&toProvider, "to", "", "target provider (brew, npm, pip, …)")
	addProviderFlag(cmd, &providerName, "configured provider to repair when tool names are duplicated")
	cmd.Flags().BoolVar(&reinstallDefault, "reinstall-default", false, actions.MustDescription(actions.ToolReinstallDefault))
	cmd.ValidArgsFunction = completeToolNames(state)
	_ = cmd.RegisterFlagCompletionFunc("from", completeProviderNames(state))
	_ = cmd.RegisterFlagCompletionFunc("to", completeProviderNames(state))
	return cmd
}

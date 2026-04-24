package cli

import (
	"fmt"

	"github.com/lkshrk/omni/internal/actions"
	"github.com/spf13/cobra"
)

func newSwitchCmd(state *rootState) *cobra.Command {
	var fromProvider, toProvider string
	var providerName string
	var reinstallDefault bool

	cmd := &cobra.Command{
		Use:   "switch <tool>",
		Short: "Move a tool to a different provider",
		Long: `Switch moves a single tool from one provider to another:
installs via the new provider, removes the old installation (best-effort),
and rewrites the config entry.

Examples:
  omni switch black --from brew --to pip
  omni switch prettier --from brew --to npm
  omni switch ripgrep --from brew --to npm
  omni switch black --reinstall-default    # ` + actions.MustLongDescription(actions.ToolReinstallDefault) + ``,
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
				fmt.Printf("✓ reinstalled %s with default provider %s\n", name, result.ToProvider)
				if result.UninstallWarning != nil {
					fmt.Printf("  warning: could not remove old %s installation: %v\n", result.FromProvider, result.UninstallWarning)
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
			fmt.Printf("✓ switched %s: %s → %s\n", name, fromProvider, toProvider)
			if result.UninstallWarning != nil {
				fmt.Printf("  warning: could not remove old %s installation: %v\n", fromProvider, result.UninstallWarning)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&fromProvider, "from", "", "current provider (brew, npm, pip, …)")
	cmd.Flags().StringVar(&toProvider, "to", "", "target provider (brew, npm, pip, …)")
	addProviderFlag(cmd, &providerName, "configured provider to repair when tool names are duplicated")
	cmd.Flags().BoolVar(&reinstallDefault, "reinstall-default", false, actions.MustDescription(actions.ToolReinstallDefault))
	return cmd
}

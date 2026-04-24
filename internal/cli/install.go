package cli

import (
	"fmt"

	"github.com/lkshrk/omni/internal/actions"
	"github.com/spf13/cobra"
)

func newInstallCmd(state *rootState) *cobra.Command {
	var providerName string

	cmd := &cobra.Command{
		Use:   "install <tool>",
		Short: actions.MustDescription(actions.ToolInstall),
		Long:  actions.MustLongDescription(actions.ToolInstall),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if providerName == "" {
				settings, err := state.app.LoadSettings()
				if err != nil {
					return fmt.Errorf("loading settings: %w", err)
				}
				resolved, err := state.app.ResolveProvider(cmd.Context(), settings.EcosystemPriority("system"))
				if err != nil {
					return fmt.Errorf("no provider available; use --provider to specify one")
				}
				providerName = resolved
				fmt.Printf("auto-selected provider: %s\n", providerName)
			}
			if err := state.app.Install(cmd.Context(), name, providerName); err != nil {
				return err
			}
			fmt.Printf("✓ installed %s (%s)\n", name, providerName)

			// After each install, check if any unselected group is now fully
			// satisfied and offer to add it to the active profile.
			activeProfile, activeGroupNames, hasProfile := state.app.ActiveProfileInfo()
			if hasProfile {
				satisfied, err := state.app.CheckSatisfiedGroups(cmd.Context(), activeGroupNames)
				if err == nil {
					promptSatisfiedGroups(state, activeProfile, satisfied, func(g string) error {
						return state.app.AddGroupToProfile(activeProfile, g)
					})
				}
			}
			return nil
		},
	}

	addProviderFlag(cmd, &providerName, "provider to use; omit to auto-select from priority list")
	return cmd
}

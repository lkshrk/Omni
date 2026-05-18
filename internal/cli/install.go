package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/actions"
	gosync "github.com/lkshrk/omni/internal/sync"
)

func newInstallCmd(state *rootState) *cobra.Command {
	var providerName string
	var group string
	var force bool

	cmd := &cobra.Command{
		Use:   "install [tool]",
		Short: actions.MustDescription(actions.ToolInstall),
		Long: actions.MustLongDescription(actions.ToolInstall) + `

Use --group to install all tools in a named group without requiring
bootstrap or host assignment:

  omni tools install --group dev-tools --force`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if group != "" {
				if len(args) > 0 {
					return fmt.Errorf("--group and <tool> argument are mutually exclusive")
				}
				return runInstallGroup(cmd, state, group)
			}
			if len(args) == 0 {
				return fmt.Errorf("tool name is required (or use --group)")
			}
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

			// After each install, check if any unselected reusable group is now
			// fully satisfied and offer to add it to the active host.
			hostname, activeGroupNames, hasHost := state.app.ActiveHostInfo()
			if hasHost {
				satisfied, err := state.app.CheckSatisfiedGroups(cmd.Context(), activeGroupNames)
				if err == nil {
					promptSatisfiedGroups(state, hostname, satisfied, func(g string) error {
						return state.app.ClaimFromMachineGroup(g)
					})
				}
			}
			return nil
		},
	}

	addProviderFlag(cmd, &providerName, "provider to use; omit to auto-select from priority list")
	cmd.Flags().StringVar(&group, "group", "", "install all tools in the named group")
	cmd.Flags().BoolVar(&force, "force", false, "skip bootstrap and host assignment checks")
	cmd.ValidArgsFunction = completeToolNames(state)
	_ = cmd.RegisterFlagCompletionFunc("group", completeGroupNames(state))
	return cmd
}

func runInstallGroup(cmd *cobra.Command, state *rootState, group string) error {
	if !state.app.HasConfig() {
		return fmt.Errorf("no config found; create settings.json first")
	}
	opts := gosync.SyncOptions{
		Group: group,
		Progress: func(msg string) {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", msg)
		},
		ToolProgress: func(event gosync.ProgressEvent) {
			if !event.Done {
				return
			}
			if event.Err != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "  ✗ failed: %s (%s): %v\n", event.Tool.Name, event.Tool.Provider, event.Err)
				return
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  ✓ installed: %s (%s)\n", event.Tool.Name, event.Tool.Provider)
		},
	}
	result, err := state.app.Sync(cmd.Context(), opts)
	if err != nil {
		return err
	}

	installed := result.Installed()
	failed := result.Failed()
	already := result.Skipped()

	out := cmd.OutOrStdout()
	if len(installed) > 0 {
		fmt.Fprintf(out, "\n%d tool(s) installed.\n", len(installed))
	}
	if len(already) > 0 {
		fmt.Fprintf(out, "%d tool(s) already installed.\n", len(already))
	}
	if len(failed) > 0 {
		fmt.Fprintf(out, "%d tool(s) failed.\n", len(failed))
	}
	if len(installed) == 0 && len(failed) == 0 && len(already) == 0 {
		fmt.Fprintln(out, "No tools in group or nothing to install.")
	}
	return nil
}

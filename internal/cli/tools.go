package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/actions"
	appcore "github.com/lkshrk/omni/internal/app"
)

func newToolsCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tools",
		Short: "Manage logical tool specs",
	}
	cmd.AddCommand(
		newToolsSetCmd(state),
		newToolsDeleteSpecCmd(state),
		newToolsNormalizeCmd(state),
		newToolsIgnoreCmd(state),
		newToolsUnignoreCmd(state),
		// moved from root:
		newAddCmd(state),
		newDeleteCmd(state),
		newInstallCmd(state),
		newUpgradeCmd(state),
		newSyncCmd(state),
		newImportCmd(state),
		newSearchCmd(state),
		newRefreshCmd(state),
		newConsolidateCmd(state),
		newSwitchCmd(state),
		newProvidersCmd(state),
		newListCmd(state),
	)
	return cmd
}

func newToolsSetCmd(state *rootState) *cobra.Command {
	var providerName string
	var packageName string
	var installWith string
	var hostScope bool
	var globalScope bool

	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Create or update a logical tool spec",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := requireProvider(providerName); err != nil {
				return err
			}
			if hostScope && globalScope {
				return fmt.Errorf("use either --host or --global, not both")
			}
			if hostScope {
				if err := state.app.SetToolHostInstallSpec(args[0], providerName, packageName, installWith); err != nil {
					return err
				}
			} else {
				if err := state.app.SetTool(args[0], providerName, packageName, installWith); err != nil {
					return err
				}
			}
			details := fmt.Sprintf("provider %q", providerName)
			if packageName != "" {
				details += fmt.Sprintf(" and package %q", packageName)
			}
			if installWith != "" {
				details += fmt.Sprintf(" via %q", installWith)
			}
			if packageName == "" && installWith == "" {
				if hostScope {
					fmt.Printf("Set host override for logical tool %q with provider %q.\n", args[0], providerName)
				} else {
					fmt.Printf("Set logical tool %q with provider %q.\n", args[0], providerName)
				}
			} else {
				if hostScope {
					fmt.Printf("Set host override for logical tool %q with %s.\n", args[0], details)
				} else {
					fmt.Printf("Set logical tool %q with %s.\n", args[0], details)
				}
			}
			return nil
		},
	}
	addProviderFlag(cmd, &providerName, "ecosystem provider for the logical tool")
	cmd.Flags().StringVar(&packageName, "package", "", "package name to install when it differs from the logical name")
	cmd.Flags().StringVar(&installWith, "install-with", "", "concrete provider or manager to use for this tool")
	cmd.Flags().BoolVar(&globalScope, "global", false, "write the default logical tool install spec")
	cmd.Flags().BoolVar(&hostScope, "host", false, "write a host override for this machine")
	cmd.ValidArgsFunction = completeToolNames(state)
	_ = cmd.RegisterFlagCompletionFunc("install-with", completeProviderNames(state))
	return cmd
}

func newToolsDeleteSpecCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete-spec <name>",
		Short: actions.MustDescription(actions.ToolDeleteSpec),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ok, err := confirmAction(cmd, state, fmt.Sprintf("Delete logical tool %q and all group memberships?", args[0]))
			if err != nil || !ok {
				return err
			}
			if err := state.app.RemoveLogicalTool(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Printf("Deleted logical tool %q.\n", args[0])
			return nil
		},
	}
	cmd.ValidArgsFunction = completeToolNames(state)
	return cmd
}

func newToolsNormalizeCmd(state *rootState) *cobra.Command {
	var defaultOverrides bool
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "normalize",
		Short: actions.MustDescription(actions.ToolNormalizeProviderOverrides),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !defaultOverrides {
				return fmt.Errorf("choose what to normalize (supported: --default-overrides)")
			}
			opts := appcore.NormalizeInstallOverridesOptions{
				IncludeDefaults:    true,
				IncludeCurrentHost: true,
				DryRun:             true,
			}
			normalized, err := state.app.NormalizeDefaultInstallOverrides(cmd.Context(), opts)
			if err != nil {
				return err
			}
			if len(normalized) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No default provider overrides to normalize.")
				return nil
			}
			if dryRun {
				fmt.Fprintf(cmd.OutOrStdout(), "Would normalize %d %s:\n", len(normalized), providerOverrideNoun(len(normalized)))
				printNormalizedOverrides(cmd.OutOrStdout(), normalized)
				return nil
			}

			ok, err := confirmAction(cmd, state, fmt.Sprintf("Normalize %d default provider overrides in config?", len(normalized)))
			if err != nil || !ok {
				return err
			}
			normalized, err = state.app.NormalizeDefaultInstallOverrides(cmd.Context(), appcore.NormalizeInstallOverridesOptions{
				IncludeDefaults:    true,
				IncludeCurrentHost: true,
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Normalized %d %s:\n", len(normalized), providerOverrideNoun(len(normalized)))
			printNormalizedOverrides(cmd.OutOrStdout(), normalized)
			return nil
		},
	}
	cmd.Flags().BoolVar(&defaultOverrides, "default-overrides", false, "remove install-with values that only restate resolved ecosystem defaults")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show provider overrides that would be normalized without writing config")
	return cmd
}

func providerOverrideNoun(count int) string {
	if count == 1 {
		return "provider override"
	}
	return "provider overrides"
}

func printNormalizedOverrides(out io.Writer, overrides []appcore.NormalizedInstallOverride) {
	for _, override := range overrides {
		scope := ""
		if override.Host != "" {
			scope = fmt.Sprintf(" (host %s)", override.Host)
		}
		fmt.Fprintf(out, "  %s: %s via %s%s\n", override.Name, override.Provider, override.InstallWith, scope)
	}
}

func newToolsIgnoreCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ignore <name>",
		Short: "Ignore a logical tool everywhere",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := state.app.SetToolIgnore(args[0], true); err != nil {
				return err
			}
			fmt.Printf("Ignored logical tool %q.\n", args[0])
			return nil
		},
	}
	cmd.ValidArgsFunction = completeToolNames(state)
	return cmd
}

func newToolsUnignoreCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unignore <name>",
		Short: "Stop ignoring a logical tool everywhere",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := state.app.SetToolIgnore(args[0], false); err != nil {
				return err
			}
			fmt.Printf("Unignored logical tool %q.\n", args[0])
			return nil
		},
	}
	cmd.ValidArgsFunction = completeToolNames(state)
	return cmd
}

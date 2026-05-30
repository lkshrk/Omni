package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/actions"
	appcore "github.com/lkshrk/omni/internal/app"
	textutil "github.com/lkshrk/omni/internal/text"
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
	var quarantine string
	var hostScope bool
	var globalScope bool

	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Create or update a logical tool spec",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if hostScope && globalScope {
				return fmt.Errorf("use either --host or --global, not both")
			}
			if hostScope && quarantine != "" {
				return fmt.Errorf("--quarantine is tool-wide; do not combine it with --host")
			}
			if providerName == "" && (packageName != "" || installWith != "") {
				return fmt.Errorf("--provider is required when setting package or install-with")
			}
			name := args[0]
			if hostScope {
				if err := requireProvider(providerName); err != nil {
					return err
				}
				if err := state.app.SetToolHostInstallSpec(name, providerName, packageName, installWith); err != nil {
					return err
				}
			} else if providerName != "" {
				if err := state.app.SetTool(name, providerName, packageName, installWith); err != nil {
					return err
				}
			} else if quarantine == "" {
				return requireProvider(providerName)
			}
			if quarantine != "" {
				if err := state.app.SetToolQuarantine(name, quarantine); err != nil {
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
			if quarantine != "" {
				details += fmt.Sprintf(" with quarantine %q", quarantine)
			}
			if providerName == "" {
				fmt.Fprintf(cmdOut(cmd), "Set logical tool %q quarantine to %q.\n", name, quarantine)
				return nil
			}
			if packageName == "" && installWith == "" && quarantine == "" {
				if hostScope {
					fmt.Fprintf(cmdOut(cmd), "Set host override for logical tool %q with provider %q.\n", name, providerName)
				} else {
					fmt.Fprintf(cmdOut(cmd), "Set logical tool %q with provider %q.\n", name, providerName)
				}
			} else {
				if hostScope {
					fmt.Fprintf(cmdOut(cmd), "Set host override for logical tool %q with %s.\n", name, details)
				} else {
					fmt.Fprintf(cmdOut(cmd), "Set logical tool %q with %s.\n", name, details)
				}
			}
			return nil
		},
	}
	addProviderFlag(cmd, &providerName, "ecosystem provider for the logical tool")
	cmd.Flags().StringVar(&packageName, "package", "", "package name to install when it differs from the logical name")
	cmd.Flags().StringVar(&installWith, "install-with", "", "concrete provider or manager to use for this tool")
	cmd.Flags().StringVar(&quarantine, "quarantine", "", "tool update quarantine duration, 0, or exempt")
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
			fmt.Fprintf(cmdOut(cmd), "Deleted logical tool %q.\n", args[0])
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
			out := cmdOut(cmd)
			if len(normalized) == 0 {
				fmt.Fprintln(out, "No default provider overrides to normalize.")
				return nil
			}
			if dryRun {
				fmt.Fprintf(out, "Would normalize %s:\n", textutil.PluralCount(len(normalized), "provider override", "provider overrides"))
				printNormalizedOverrides(out, normalized)
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
			fmt.Fprintf(out, "Normalized %s:\n", textutil.PluralCount(len(normalized), "provider override", "provider overrides"))
			printNormalizedOverrides(out, normalized)
			return nil
		},
	}
	cmd.Flags().BoolVar(&defaultOverrides, "default-overrides", false, "remove install-with values that only restate resolved ecosystem defaults")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show provider overrides that would be normalized without writing config")
	return cmd
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
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := state.app.SetToolIgnore(args[0], true); err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "Ignored logical tool %q.\n", args[0])
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
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := state.app.SetToolIgnore(args[0], false); err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "Unignored logical tool %q.\n", args[0])
			return nil
		},
	}
	cmd.ValidArgsFunction = completeToolNames(state)
	return cmd
}

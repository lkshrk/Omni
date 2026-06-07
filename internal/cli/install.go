package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/actions"
	"github.com/lkshrk/omni/internal/app"
	gosync "github.com/lkshrk/omni/internal/sync"
	textutil "github.com/lkshrk/omni/internal/text"
)

func newInstallCmd(state *rootState) *cobra.Command {
	var providerName string
	var group string
	var force bool
	var allowWeak bool

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
				matchResult, err := state.app.InstallProviderMatches(cmd.Context(), name, "", app.ProviderMatchOptions{AllowWeak: allowWeak})
				if err == nil {
					printProviderMatchInstallResult(cmdOut(cmd), name, matchResult)
					promptSatisfiedGroupsAfterInstall(cmd, state)
					return nil
				}
				if errors.Is(err, app.ErrProviderDiscoveryAlreadyConfigured) {
					if err := state.app.Install(cmd.Context(), name, ""); err != nil {
						return err
					}
					printInstallResult(cmd, state, name, "")
					promptSatisfiedGroupsAfterInstall(cmd, state)
					return nil
				}
				if !errors.Is(err, app.ErrProviderDiscoveryNotConfigured) {
					return err
				}
				resolved, err := state.app.DefaultInstallProvider(cmd.Context())
				if err != nil {
					return err
				}
				providerName = resolved
				fmt.Fprintf(cmdOut(cmd), "auto-selected provider: %s\n", providerName)
			} else {
				matchResult, err := state.app.InstallProviderMatches(cmd.Context(), name, providerName, app.ProviderMatchOptions{AllowWeak: allowWeak})
				if err == nil {
					printProviderMatchInstallResult(cmdOut(cmd), name, matchResult)
					promptSatisfiedGroupsAfterInstall(cmd, state)
					return nil
				}
				if !errors.Is(err, app.ErrProviderDiscoveryAlreadyConfigured) && !errors.Is(err, app.ErrProviderDiscoveryNotConfigured) {
					return err
				}
			}
			if err := state.app.Install(cmd.Context(), name, providerName); err != nil {
				return err
			}
			printInstallResult(cmd, state, name, providerName)

			// After each install, check if any unselected reusable group is now
			// fully satisfied and offer to add it to the active host.
			promptSatisfiedGroupsAfterInstall(cmd, state)
			return nil
		},
	}

	addProviderFlag(cmd, &providerName, "provider to use; omit to auto-select from priority list")
	cmd.Flags().StringVar(&group, "group", "", "install all tools in the named group")
	cmd.Flags().BoolVar(&force, "force", false, "skip bootstrap and host assignment checks")
	cmd.Flags().BoolVar(&allowWeak, "allow-weak", false, "allow best weak provider discovery match when no high-confidence match exists")
	cmd.ValidArgsFunction = completeToolNames(state)
	_ = cmd.RegisterFlagCompletionFunc("group", completeGroupNames(state))
	return cmd
}

func printInstallResult(cmd *cobra.Command, state *rootState, name, providerName string) {
	if providerName == "" {
		providerName = installedProviderLabel(cmd, state, name)
	}
	if providerName == "" {
		fmt.Fprintf(cmdOut(cmd), "✓ installed %s\n", name)
		return
	}
	fmt.Fprintf(cmdOut(cmd), "✓ installed %s (%s)\n", name, providerName)
}

func installedProviderLabel(cmd *cobra.Command, state *rootState, name string) string {
	tools, err := state.app.ListTools(cmd.Context(), "")
	if err != nil {
		return ""
	}
	for _, tool := range tools {
		if tool.Name == name && tool.Installed {
			return tool.Provider
		}
	}
	return ""
}

func printProviderMatchInstallResult(out io.Writer, name string, result *app.ProviderMatchInstallResult) {
	if result == nil {
		return
	}
	if len(result.Added) == 1 {
		fmt.Fprintf(out, "matched provider: %s -> %s/%s\n", name, result.Added[0].Provider, result.Added[0].EffectivePackage(name))
	} else if len(result.Added) > 1 {
		parts := make([]string, 0, len(result.Added))
		for _, added := range result.Added {
			parts = append(parts, fmt.Sprintf("%s/%s", added.Provider, added.EffectivePackage(name)))
		}
		fmt.Fprintf(out, "matched providers: %s -> %s\n", name, strings.Join(parts, ", "))
	}
	if result.SearchErr != nil {
		fmt.Fprintf(out, "warning: %v\n", result.SearchErr)
	}
	if result.Installed.Provider != "" {
		fmt.Fprintf(out, "✓ installed %s (%s)\n", name, result.Installed.Provider)
	}
}

func promptSatisfiedGroupsAfterInstall(cmd *cobra.Command, state *rootState) {
	hostname, activeGroupNames, hasHost := state.app.ActiveHostInfo()
	if !hasHost {
		return
	}
	satisfied, err := state.app.CheckSatisfiedGroups(cmd.Context(), activeGroupNames)
	if err != nil {
		return
	}
	promptSatisfiedGroups(state, hostname, satisfied, func(g string) error {
		return state.app.ClaimFromMachineGroup(g)
	})
}

func runInstallGroup(cmd *cobra.Command, state *rootState, group string) error {
	if !state.app.HasConfig() {
		return fmt.Errorf("no config found; create settings.json first")
	}
	opts := gosync.SyncOptions{
		Group: group,
		Progress: func(msg string) {
			fmt.Fprintf(cmdOut(cmd), "  %s\n", msg)
		},
		ToolProgress: func(event gosync.ProgressEvent) {
			if !event.Done {
				return
			}
			if event.Err != nil {
				fmt.Fprintf(cmdOut(cmd), "  ✗ failed: %s (%s): %v\n", event.Tool.Name, event.Tool.Provider, event.Err)
				return
			}
			fmt.Fprintf(cmdOut(cmd), "  ✓ installed: %s (%s)\n", event.Tool.Name, event.Tool.Provider)
		},
	}
	result, err := state.app.Sync(cmd.Context(), opts)
	if err != nil {
		return err
	}

	summary := app.SummarizeSyncResult(result)

	out := cmd.OutOrStdout()
	for _, line := range app.SyncProviderUnavailableLines(result) {
		fmt.Fprintf(out, "  ! %s\n", line)
	}
	lines := app.SyncResultSummaryLines(result, app.SyncResultSummaryLineOptions{
		IncludeInstalled:        true,
		IncludeAlreadyInstalled: true,
		IncludeFailed:           true,
	})
	if summary.Installed > 0 && len(lines) > 0 {
		fmt.Fprintln(out)
	}
	for _, line := range lines {
		fmt.Fprintln(out, line)
	}
	if len(summary.ProviderUnavailable) > 0 {
		return fmt.Errorf("%s unavailable", textutil.PluralCount(len(summary.ProviderUnavailable), "tool", "tools"))
	}
	if summary.Failed > 0 {
		return fmt.Errorf("%s failed", textutil.PluralCount(summary.Failed, "tool", "tools"))
	}
	if summary.Installed == 0 && summary.Failed == 0 && summary.AlreadyInstalled == 0 && len(summary.ProviderUnavailable) == 0 {
		fmt.Fprintln(out, "No tools in group or nothing to install.")
	}
	return nil
}

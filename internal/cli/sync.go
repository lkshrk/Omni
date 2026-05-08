package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/actions"
	"github.com/lkshrk/omni/internal/app"
	gosync "github.com/lkshrk/omni/internal/sync"
)

func newSyncCmd(state *rootState) *cobra.Command {
	var dryRun bool
	var prune bool
	var providerFilter string
	var group string
	var retryFailed bool
	var all bool

	cmd := &cobra.Command{
		Use:   "sync [group]",
		Short: "Install missing tools from config",
		Long: `Install missing tools from config.

Use --all to ` + actions.MustLongDescription(actions.ToolSyncAll) + `.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				group = args[0]
			}
			if all {
				if group != "" || providerFilter != "" || retryFailed || prune {
					return fmt.Errorf("--all cannot be combined with group, --provider, --retry-failed, or --prune")
				}
			}
			if !state.app.HasConfig() {
				fmt.Fprintln(cmd.ErrOrStderr(), "No config found. Run 'omni init' to get started.")
				return nil
			}
			if all {
				if !dryRun {
					ok, err := confirmAction(cmd, state, "Sync all: add discovered tools to config and install missing configured tools?")
					if err != nil || !ok {
						return err
					}
				}
				result, err := state.app.SyncAll(cmd.Context(), app.SyncAllOptions{
					DryRun: dryRun,
					Progress: func(msg string) {
						fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", msg)
					},
					ToolProgress: func(event gosync.ProgressEvent) {
						if !event.Done {
							return
						}
						if event.Err != nil {
							fmt.Fprintf(cmd.OutOrStdout(), "  ! failed: %s (%s): %v\n", event.Tool.Name, event.Tool.Provider, event.Err)
							return
						}
						fmt.Fprintf(cmd.OutOrStdout(), "  ✓ done: %s (%s)\n", event.Tool.Name, event.Tool.Provider)
					},
				})
				if err != nil {
					return err
				}
				if dryRun {
					fmt.Fprintln(cmd.OutOrStdout(), "Dry-run — no changes made.")
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Sync all complete — %d installed, %d added to config%s.\n", installedCount(result), claimedCount(result), normalizedProviderOverrideSummary(result))
				return nil
			}
			opts := gosync.SyncOptions{
				DryRun:      dryRun,
				Prune:       prune,
				Provider:    providerFilter,
				Group:       group,
				RetryFailed: retryFailed,
				Progress: func(msg string) {
					fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", msg)
				},
				ToolProgress: func(event gosync.ProgressEvent) {
					if !event.Done {
						return
					}
					if event.Err != nil {
						fmt.Fprintf(cmd.OutOrStdout(), "  ! failed: %s (%s): %v\n", event.Tool.Name, event.Tool.Provider, event.Err)
						return
					}
					fmt.Fprintf(cmd.OutOrStdout(), "  ✓ done: %s (%s)\n", event.Tool.Name, event.Tool.Provider)
				},
			}
			result, err := state.app.Sync(cmd.Context(), opts)
			if err != nil {
				return err
			}

			for _, w := range result.Warnings {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning:", w)
			}

			out := cmd.OutOrStdout()
			if dryRun {
				fmt.Fprintln(out, "Dry-run — no changes made.")
			}

			for _, op := range result.Ops {
				switch op.Kind {
				case gosync.OpInstall:
					if dryRun {
						fmt.Fprintf(out, "  → would install: %s (%s)\n", op.Tool.Name, op.Tool.Provider)
					} else {
						fmt.Fprintf(out, "  ✓ installed: %s (%s) %s\n", op.Tool.Name, op.Tool.Provider, op.Version)
					}
				case gosync.OpFailed:
					fmt.Fprintf(out, "  ✗ failed: %s (%s): %v\n", op.Tool.Name, op.Tool.Provider, op.Err)
				case gosync.OpAlreadyInstalled:
					fmt.Fprintf(out, "  ✓ already installed: %s (%s) %s\n", op.Tool.Name, op.Tool.Provider, op.Version)
				case gosync.OpUninstall:
					fmt.Fprintf(out, "  ✗ pruned: %s (%s)\n", op.Tool.Name, op.Tool.Provider)
				case gosync.OpProviderUnavailable:
					fmt.Fprintf(out, "  ! provider unavailable: %s (skipping %s)\n", op.Tool.Provider, op.Tool.Name)
				}
			}

			installed := result.Installed()
			if len(installed) > 0 && !dryRun {
				fmt.Fprintf(out, "\n%d tool(s) installed.\n", len(installed))
			}
			if failed := result.Failed(); len(failed) > 0 {
				fmt.Fprintf(out, "%d tool(s) failed. Run 'omni sync --retry-failed' to try again.\n", len(failed))
			}

			hostname, _, hasHost := state.app.ActiveHostInfo()
			if hasHost {
				promptSatisfiedGroups(state, hostname, result.SatisfiedGroups,
					func(g string) error {
						return state.app.ClaimFromMachineGroup(g)
					})
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be done without making changes")
	cmd.Flags().BoolVar(&prune, "prune", false, "delete local installations no longer in config")
	addProviderFlag(cmd, &providerFilter, "limit sync to one provider")
	cmd.Flags().StringVar(&group, "group", "", "limit sync to one group (overridden by positional arg)")
	cmd.Flags().BoolVar(&retryFailed, "retry-failed", false, "only retry tools that failed in a previous sync")
	cmd.Flags().BoolVar(&all, "all", false, actions.MustDescription(actions.ToolSyncAll))
	return cmd
}

func installedCount(result *app.SyncAllResult) int {
	if result == nil || result.SyncResult == nil {
		return 0
	}
	return len(result.SyncResult.Installed())
}

func claimedCount(result *app.SyncAllResult) int {
	if result == nil {
		return 0
	}
	return len(result.ClaimedNames)
}

func normalizedProviderOverrideSummary(result *app.SyncAllResult) string {
	if result == nil {
		return ""
	}
	count := len(result.NormalizedProviderOverrides)
	if count == 0 {
		return ""
	}
	return fmt.Sprintf(", %d provider overrides normalized", count)
}

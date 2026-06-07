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
	var allowWeak bool

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
				if providerFilter != "" || retryFailed || prune {
					return fmt.Errorf("--all cannot be combined with --provider, --retry-failed, or --prune")
				}
			}
			if !state.app.HasConfig() {
				fmt.Fprintln(cmdErr(cmd), "No config found. Run 'omni bootstrap' to get started.")
				return nil
			}
			out := cmdOut(cmd)
			errOut := cmdErr(cmd)
			if all {
				if !dryRun {
					ok, err := confirmAction(cmd, state, "Sync all: add discovered tools to config and install missing configured tools?")
					if err != nil || !ok {
						return err
					}
				}
				syncOpts := app.SyncAllOptions{
					DryRun:    dryRun,
					Group:     group,
					AllowWeak: allowWeak,
					Progress: func(msg string) {
						fmt.Fprintf(out, "  %s\n", msg)
					},
					ToolProgress: func(event gosync.ProgressEvent) {
						if !event.Done {
							return
						}
						if event.Err != nil {
							fmt.Fprintf(out, "  ! failed: %s (%s): %v\n", event.Tool.Name, event.Tool.Provider, event.Err)
							return
						}
						fmt.Fprintf(out, "  ✓ done: %s (%s)\n", event.Tool.Name, event.Tool.Provider)
					},
				}
				result, err := state.app.SyncAll(cmd.Context(), syncOpts)
				if err != nil {
					return err
				}
				if dryRun {
					fmt.Fprintln(out, "Dry-run — no changes made.")
				}
				fmt.Fprintf(out, "%s.\n", app.SyncAllSummaryText(result, "Sync all complete"))
				if !dryRun && len(result.ClaimedNames) > 0 && !cmd.Flags().Changed("group") && stdinIsTerminal() {
					promptReassignClaimedTools(state, result.ClaimedNames)
				}
				return nil
			}
			opts := gosync.SyncOptions{
				DryRun:      dryRun,
				Prune:       prune,
				Provider:    providerFilter,
				Group:       group,
				RetryFailed: retryFailed,
				AllowWeak:   allowWeak,
				Progress: func(msg string) {
					fmt.Fprintf(out, "  %s\n", msg)
				},
				ToolProgress: func(event gosync.ProgressEvent) {
					if !event.Done {
						return
					}
					if event.Err != nil {
						fmt.Fprintf(out, "  ! failed: %s (%s): %v\n", event.Tool.Name, event.Tool.Provider, event.Err)
						return
					}
					fmt.Fprintf(out, "  ✓ done: %s (%s)\n", event.Tool.Name, event.Tool.Provider)
				},
			}
			result, err := state.app.Sync(cmd.Context(), opts)
			if err != nil {
				return err
			}

			for _, w := range result.Warnings {
				fmt.Fprintln(errOut, "warning:", w)
			}

			if dryRun {
				fmt.Fprintln(out, "Dry-run — no changes made.")
			}

			for _, op := range result.Ops {
				line := app.SyncOperationLine(op, app.SyncOperationLineOptions{DryRun: dryRun, IncludeVersion: true})
				if line != "" {
					fmt.Fprintln(out, line)
				}
			}

			summary := app.SummarizeSyncResult(result)
			summaryLines := app.SyncResultSummaryLines(result, app.SyncResultSummaryLineOptions{
				IncludeInstalled: !dryRun,
				IncludeFailed:    true,
				FailureSuffix:    "Run 'omni sync --retry-failed' to try again.",
			})
			if !dryRun && summary.Installed > 0 && len(summaryLines) > 0 {
				fmt.Fprintln(out)
			}
			for _, line := range summaryLines {
				fmt.Fprintln(out, line)
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
	cmd.Flags().StringVar(&group, "group", "", "limit sync to one group, or assign discovered tools (with --all)")
	cmd.Flags().BoolVar(&retryFailed, "retry-failed", false, "only retry tools that failed in a previous sync")
	cmd.Flags().BoolVar(&all, "all", false, actions.MustDescription(actions.ToolSyncAll))
	cmd.Flags().BoolVar(&allowWeak, "allow-weak", false, "allow best weak provider discovery match when no high-confidence match exists")
	cmd.ValidArgsFunction = completeToolNames(state)
	_ = cmd.RegisterFlagCompletionFunc("group", completeGroupNames(state))
	return cmd
}

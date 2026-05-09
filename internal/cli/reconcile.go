package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/actions"
	"github.com/lkshrk/omni/internal/app"
	syncprogress "github.com/lkshrk/omni/internal/sync"
)

func newReconcileCmd(state *rootState) *cobra.Command {
	var (
		message        string
		skipPrivileged bool
	)

	cmd := &cobra.Command{
		Use:   "reconcile",
		Short: "Sync, upgrade, repair dotfiles, and commit dotfile changes",
		Long:  actions.MustLongDescription(actions.Reconcile),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ok, err := confirmAction(cmd, state, actions.MustConfirmDescription(actions.Reconcile))
			if err != nil || !ok {
				return err
			}
			out := cmd.OutOrStdout()
			result, err := state.app.Reconcile(cmd.Context(), app.ReconcileOptions{
				CommitMessage:  message,
				SkipPrivileged: skipPrivileged,
				Progress: func(msg string) {
					fmt.Fprintf(out, "  %s\n", msg)
				},
				ToolProgress: func(event syncprogress.ProgressEvent) {
					if !event.Done {
						return
					}
					if event.Err != nil {
						fmt.Fprintf(out, "  ! failed: %s (%s): %v\n", event.Tool.Name, event.Tool.Provider, event.Err)
						return
					}
					fmt.Fprintf(out, "  ✓ done: %s (%s)\n", event.Tool.Name, event.Tool.Provider)
				},
			})
			printReconcileSummary(cmd, result)
			return err
		},
	}
	cmd.Flags().StringVarP(&message, "message", "m", "", "Dotfile commit message (default: auto-generated)")
	cmd.Flags().BoolVar(&skipPrivileged, "skip-privileged", false, "skip package actions that need sudo/root access")
	return cmd
}

func printReconcileSummary(cmd *cobra.Command, result *app.ReconcileResult) {
	if result == nil {
		return
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Reconcile complete — %d installed, %d added to config, %d upgraded",
		installedCount(result.SyncAll),
		claimedCount(result.SyncAll),
		reconcileUpgradeCount(result),
	)
	if len(result.DotsOps) > 0 {
		fmt.Fprintf(out, ", %d dotfile ops", len(result.DotsOps))
	}
	if result.DotsCommitted {
		fmt.Fprint(out, ", dotfiles committed")
	} else if result.DotsSkipped != "" {
		fmt.Fprintf(out, ", %s", result.DotsSkipped)
	}
	fmt.Fprintln(out, ".")
}

func reconcileUpgradeCount(result *app.ReconcileResult) int {
	if result == nil || result.UpgradeAll == nil {
		return 0
	}
	return len(result.UpgradeAll.Upgraded)
}

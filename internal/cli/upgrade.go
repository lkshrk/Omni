package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/actions"
	"github.com/lkshrk/omni/internal/app"
	syncprogress "github.com/lkshrk/omni/internal/sync"
)

func newUpgradeCmd(state *rootState) *cobra.Command {
	var (
		all   bool
		force bool
	)

	cmd := &cobra.Command{
		Use:   "upgrade [tool]",
		Short: "Upgrade a tool or all outdated tools",
		Long: `Upgrade a single tool or every outdated tool tracked in the local DB.
` + actions.MustLongDescription(actions.ToolUpdate) + `
` + actions.MustLongDescription(actions.ToolUpdateAll) + `

Examples:
  omni upgrade ripgrep     # upgrade one installed tool
  omni upgrade --all       # upgrade all outdated tools`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			a := state.app

			if all {
				if len(args) > 0 {
					return fmt.Errorf("--all and a tool name are mutually exclusive")
				}
				out := cmdOut(cmd)
				result, err := a.UpgradeAllDetailedWithOptions(ctx, func(msg string) {
					fmt.Fprintf(out, "  %s\n", msg)
				}, func(event syncprogress.ProgressEvent) {
					if !event.Done {
						return
					}
					name := app.ToolNameWithVersion(event.Tool.Name, event.TargetVersion)
					if event.Err != nil {
						fmt.Fprintf(out, "  ! failed: %s (%s): %v\n", name, event.Tool.Provider, event.Err)
						return
					}
					if reason, ok := skippedUpgradeProgressReason(event.Message); ok {
						fmt.Fprintf(out, "  - skipped: %s (%s): %s\n", name, event.Tool.Provider, reason)
						return
					}
					fmt.Fprintf(out, "  ✓ upgraded: %s (%s)\n", name, event.Tool.Provider)
				}, app.UpgradeAllOptions{Force: force})
				if err != nil {
					return err
				}
				if result == nil || len(result.Upgraded)+len(result.Failures)+len(result.Quarantined)+len(result.Skipped) == 0 {
					fmt.Fprintln(out, "Nothing to upgrade.")
				}
				for _, line := range app.UpgradeAllSummaryLines(result) {
					fmt.Fprintln(out, line)
				}
				return nil
			}

			if len(args) == 0 {
				return fmt.Errorf("specify a tool name or use --all")
			}
			name := args[0]
			if err := a.UpgradeInstalledWithOptions(ctx, name, app.UpgradeOptions{Force: force}); err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "✓ upgraded %s\n", name)
			return nil
		},
	}

	cmd.Flags().BoolVar(&all, "all", false, actions.MustDescription(actions.ToolUpdateAll))
	cmd.Flags().BoolVar(&force, "force", false, "bypass update quarantine")
	cmd.ValidArgsFunction = completeToolNames(state)
	return cmd
}

func skippedUpgradeProgressReason(message string) (string, bool) {
	msg := strings.ToLower(strings.TrimSpace(message))
	if !strings.HasPrefix(msg, "skipped ") {
		return "", false
	}
	switch {
	case strings.Contains(msg, "update quarantined"):
		return "update quarantined", true
	case strings.Contains(msg, "manual cask upgrade"):
		return "manual cask upgrade", true
	case strings.Contains(msg, "externally managed") && strings.Contains(msg, "self-upgrade"):
		return "externally managed; cannot self-upgrade", true
	default:
		return "skipped", true
	}
}

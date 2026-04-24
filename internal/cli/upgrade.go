package cli

import (
	"fmt"

	"github.com/lkshrk/omni/internal/actions"
	syncprogress "github.com/lkshrk/omni/internal/sync"
	"github.com/spf13/cobra"
)

func newUpgradeCmd(state *rootState) *cobra.Command {
	var (
		providerName string
		all          bool
	)

	cmd := &cobra.Command{
		Use:   "upgrade [tool]",
		Short: "Upgrade a tool or all outdated tools",
		Long: `Upgrade a single tool or every outdated tool tracked in the local DB.
` + actions.MustLongDescription(actions.ToolUpdate) + `
` + actions.MustLongDescription(actions.ToolUpdateAll) + `

Examples:
  omni upgrade ripgrep --provider brew     # upgrade one tool
  omni upgrade --all                       # upgrade all outdated tools`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			a := state.app

			if all {
				if len(args) > 0 {
					return fmt.Errorf("--all and a tool name are mutually exclusive")
				}
				out := cmd.OutOrStdout()
				result, err := a.UpgradeAllDetailed(ctx, func(msg string) {
					fmt.Fprintf(out, "  %s\n", msg)
				}, func(event syncprogress.ProgressEvent) {
					if !event.Done {
						return
					}
					if event.Err != nil {
						fmt.Fprintf(out, "  ! failed: %s (%s): %v\n", event.Tool.Name, event.Tool.Provider, event.Err)
						return
					}
					fmt.Fprintf(out, "  ✓ upgraded: %s (%s)\n", event.Tool.Name, event.Tool.Provider)
				})
				if err != nil {
					return err
				}
				if result == nil || len(result.Upgraded)+len(result.Failures) == 0 {
					fmt.Fprintln(out, "Nothing to upgrade.")
				}
				return nil
			}

			if len(args) == 0 {
				return fmt.Errorf("specify a tool name or use --all")
			}
			name := args[0]
			if err := requireProvider(providerName); err != nil {
				return err
			}
			if err := a.Upgrade(ctx, name, providerName); err != nil {
				return err
			}
			fmt.Printf("✓ upgraded %s (%s)\n", name, providerName)
			return nil
		},
	}

	addProviderFlag(cmd, &providerName, "provider to use")
	cmd.Flags().BoolVar(&all, "all", false, actions.MustDescription(actions.ToolUpdateAll))
	return cmd
}

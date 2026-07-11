package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/actions"
	appcore "github.com/lkshrk/omni/internal/app"
)

func newToolsMigrateNvmCmd(state *rootState) *cobra.Command {
	var all bool

	cmd := &cobra.Command{
		Use:   "migrate-nvm [tool...]",
		Short: actions.MustDescription(actions.ToolMigrateNvm),
		Long: `Move system-provider tools whose active binaries resolve via nvm onto the
configured node package manager, or remove the Node runtime from omni config when
nvm owns it.

Examples:
  omni tools migrate-nvm --all
  omni tools migrate-nvm pnpm
  omni tools migrate-nvm node pnpm`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !all && len(args) == 0 {
				return fmt.Errorf("pass tool name(s) or use --all")
			}
			if all && len(args) > 0 {
				return fmt.Errorf("use either --all or explicit tool names, not both")
			}
			prompt := "Migrate all nvm-managed system-provider tools?"
			if !all {
				prompt = fmt.Sprintf("Migrate %d nvm-managed tool(s)?", len(args))
			}
			ok, err := confirmAction(cmd, state, prompt)
			if err != nil || !ok {
				return err
			}
			out := cmdOut(cmd)
			var result *appcore.NvmManagedMigrationBatchResult
			if all {
				result, err = state.app.MigrateAllNvmManagedTools(cmd.Context())
			} else {
				result, err = state.app.MigrateNvmManagedTools(cmd.Context(), args)
			}
			if result != nil {
				for _, item := range result.Items {
					if item.Removed {
						fmt.Fprintf(out, "✓ removed %s from omni config (nvm owns runtime; was %s)\n", item.Name, item.FromProvider)
						continue
					}
					fmt.Fprintf(out, "✓ migrated %s: %s → %s\n", item.Name, item.FromProvider, item.ToProvider)
				}
				for _, failure := range result.Failures {
					fmt.Fprintf(out, "✗ %s: %v\n", failure.Name, failure.Err)
				}
				if len(result.Items) == 0 && len(result.Failures) == 0 {
					fmt.Fprintln(out, "No nvm-managed system-provider tools found.")
				}
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "migrate every nvm-managed system-provider tool")
	cmd.ValidArgsFunction = completeToolNames(state)
	return cmd
}

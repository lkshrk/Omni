package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/actions"
)

func newRefreshCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: actions.MustDescription(actions.ToolRefresh),
		Long:  actions.MustLongDescription(actions.ToolRefresh),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			progress := func(msg string) {
				fmt.Fprintf(out, "  %s\n", msg)
			}

			if err := state.app.RefreshInstalled(ctx, progress); err != nil {
				return fmt.Errorf("refreshing installed state: %w", err)
			}
			if err := state.app.RefreshOutdated(ctx, progress); err != nil {
				return fmt.Errorf("refreshing update state: %w", err)
			}
			fmt.Fprintln(out, "  finding local tools...")
			if err := state.app.RefreshDiscovered(ctx); err != nil {
				return fmt.Errorf("refreshing discovered tools: %w", err)
			}
			fmt.Fprintln(out, "  refreshing tool descriptions...")
			if err := state.app.RefreshDescriptions(ctx, 0); err != nil {
				return fmt.Errorf("refreshing tool descriptions: %w", err)
			}
			fmt.Fprintln(out, "Refresh complete.")
			return nil
		},
	}
}

package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/actions"
	"github.com/lkshrk/omni/internal/app"
)

func newRefreshCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: actions.MustDescription(actions.ToolRefresh),
		Long:  actions.MustLongDescription(actions.ToolRefresh),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			out := cmdOut(cmd)
			progress := func(msg string) {
				fmt.Fprintf(out, "  %s\n", msg)
			}

			if err := state.app.RefreshInstalled(ctx, progress); err != nil {
				return fmt.Errorf("refreshing installed state: %w", err)
			}
			if err := state.app.RefreshOutdated(ctx, progress); err != nil {
				return fmt.Errorf("refreshing update state: %w", err)
			}
			if err := state.app.RefreshDiscoveredWithProgress(ctx, func(event app.RefreshDiscoveredProgressEvent) {
				progress(app.RefreshDiscoveredProgressText(event))
			}); err != nil {
				return fmt.Errorf("refreshing discovered tools: %w", err)
			}
			if err := state.app.RefreshDescriptionsWithProgress(ctx, 0, func(event app.RefreshDescriptionsProgressEvent) {
				progress(app.RefreshDescriptionsProgressText(event))
			}); err != nil {
				return fmt.Errorf("refreshing tool descriptions: %w", err)
			}
			fmt.Fprintln(out, "Refresh complete.")
			return nil
		},
	}
}

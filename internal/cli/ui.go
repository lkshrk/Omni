package cli

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/tui"
)

func newUICmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "ui",
		Short: "Launch the interactive TUI",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			model := tui.New(state.app, ctx)
			p := tea.NewProgram(model, tui.ProgramOptions()...)
			if _, err := p.Run(); err != nil {
				return fmt.Errorf("TUI error: %w", err)
			}
			return nil
		},
	}
}

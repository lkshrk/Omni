package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newAgentsDriftCmd(state *rootState) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "drift",
		Short: "Report native agent items APM does not manage",
		Long: "List the natively installed plugins, MCP servers and marketplaces this host's APM " +
			"manifest could cover but does not, minus the entries listed in agents.ignored. Items " +
			"the migration classifier retains for a stated reason are not drift. This reports only; " +
			"it exits 0 whether or not drift exists.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out, err := state.app.AgentsDrift(cmd.Context(), all)
			if err != nil {
				return err
			}
			fmt.Fprint(cmdOut(cmd), out)
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Also list the ignored entries and the retained items with their reasons")
	return cmd
}

package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newAgentsAdoptCmd(state *rootState) *cobra.Command {
	var host string
	cmd := &cobra.Command{
		Use:   "adopt",
		Short: "Preview what adopting a host onto APM would do",
		Long: "Report the host template's shape, what an APM manifest would gain, which native items " +
			"would be replaced, retained, already managed or ignored, and which clients could not be " +
			"read. This command is preview-only: it writes nothing, runs no mutating client command, " +
			"and has no apply mode.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out, err := state.app.AgentsAdopt(cmd.Context(), host)
			if err != nil {
				return err
			}
			fmt.Fprint(cmdOut(cmd), out)
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "Host whose adoption to preview")
	cobra.CheckErr(cmd.MarkFlagRequired("host"))
	return cmd
}

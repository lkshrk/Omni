package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newAgentsMigrateCmd(state *rootState) *cobra.Command {
	var host, snapshot string
	var dryRun, write bool
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Preview or write the apm.yml a host's pre-migration snapshot maps to",
		Long: "Read a pre-migration snapshot and print the apm.yml equivalent of the agent " +
			"declarations that were active on the given host, followed by the marketplace " +
			"registrations apm.yml cannot express. Preview is the default; --write updates only " +
			"Omni's host template.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var out string
			var err error
			if write {
				out, err = state.app.AgentsMigrateWrite(host, snapshot)
			} else {
				out, err = state.app.AgentsMigrate(host, snapshot)
			}
			if err != nil {
				return err
			}
			fmt.Fprint(cmdOut(cmd), out)
			if write {
				fmt.Fprintln(cmdOut(cmd), "Next: omni agents sync")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "Host whose declarations to render")
	cmd.Flags().StringVar(&snapshot, "snapshot", "", "Snapshot directory (default: the one next to the loaded config)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview without writing")
	cmd.Flags().BoolVar(&write, "write", false, "Publish wrappers and update Omni's host template")
	cmd.MarkFlagsMutuallyExclusive("dry-run", "write")
	_ = cmd.MarkFlagRequired("host")
	return cmd
}

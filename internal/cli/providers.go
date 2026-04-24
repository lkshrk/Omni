package cli

import (
	"github.com/spf13/cobra"
)

func newProvidersCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "providers",
		Short: "List available providers on this system",
		RunE: func(cmd *cobra.Command, _ []string) error {
			infos, err := state.app.Providers(cmd.Context())
			if err != nil {
				return err
			}
			tbl := newTable("NAME", "AVAILABLE", "DESCRIPTION")
			for _, p := range infos {
				avail := "no"
				if p.Available {
					avail = "yes"
				}
				tbl.AddRow(p.Name, avail, p.Description)
			}
			tbl.Render(cmd.OutOrStdout())
			return nil
		},
	}
}

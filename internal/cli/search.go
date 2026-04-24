package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newSearchCmd(state *rootState) *cobra.Command {
	var providerFilter string

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search provider registries for packages",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			results, err := state.app.Search(cmd.Context(), args[0], providerFilter)
			if err != nil {
				return err
			}
			if len(results) == 0 {
				fmt.Println("No results found.")
				return nil
			}
			const descWidth = 40                              // match name column width
			indent := strings.Repeat(" ", 40+1+12+1+8+1)     // align continuation lines with description column
			for _, r := range results {
				ver := r.Version
				if ver == "" {
					ver = "-"
				}
				descLines := wrapText(r.Description, descWidth)
				if len(descLines) == 0 {
					fmt.Printf("%-40s %-12s %-8s\n", r.Name, ver, r.Provider)
				} else {
					fmt.Printf("%-40s %-12s %-8s %s\n", r.Name, ver, r.Provider, descLines[0])
					for _, dl := range descLines[1:] {
						fmt.Printf("%s%s\n", indent, dl)
					}
				}
			}
			return nil
		},
	}

	addProviderFlag(cmd, &providerFilter, "filter by provider")
	return cmd
}

package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
)

func newImportCmd(state *rootState) *cobra.Command {
	var opts app.ImportOptions

	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import installed tools into the config",
		Args:  cobra.NoArgs,
		Long: `Import discovers tools already installed by each provider and
adds them to your config file. Tools already present in the config are skipped.

  omni import                            # import from all available providers
  omni import --provider brew            # import only from brew
  omni import --dry-run                  # preview without writing`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := state.app.Import(cmd.Context(), opts)
			if err != nil {
				return err
			}

			if len(result.Added) == 0 && len(result.Skipped) == 0 {
				fmt.Fprintln(cmdOut(cmd), "No installed tools found.")
				return nil
			}

			fmt.Fprintln(cmdOut(cmd), app.ImportCommandSummaryText(result, opts.DryRun))
			for _, t := range result.Added {
				if t.Version != "" {
					fmt.Fprintf(cmdOut(cmd), "  + %-30s (%s) %s\n", t.Name, t.Provider, t.Version)
				} else {
					fmt.Fprintf(cmdOut(cmd), "  + %-30s (%s)\n", t.Name, t.Provider)
				}
			}
			if skipped := app.ImportSkippedSummaryText(result); skipped != "" {
				fmt.Fprintln(cmdOut(cmd), skipped)
			}
			if !opts.DryRun && len(result.Added) > 0 && !cmd.Flags().Changed("group") && stdinIsTerminal() {
				names := make([]string, len(result.Added))
				for i, t := range result.Added {
					names[i] = t.Name
				}
				promptReassignClaimedTools(state, names)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "preview what would be added without writing")
	addProviderFlag(cmd, &opts.Provider, "import only from this provider")
	cmd.Flags().StringVar(&opts.Group, "group", "", "destination group (default: machine hostname group)")
	_ = cmd.RegisterFlagCompletionFunc("group", completeGroupNames(state))
	return cmd
}

package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
	textutil "github.com/lkshrk/omni/internal/text"
)

func newConsolidateCmd(state *rootState) *cobra.Command {
	var (
		dryRun     bool
		toProvider string
	)

	cmd := &cobra.Command{
		Use:   "consolidate (<ecosystem> <manager> | --to <provider>)",
		Short: "Consolidate tools to a single provider or manager",
		Long: `Two modes:

  Ecosystem mode — switch all tools in one ecosystem to a specific manager:
    omni consolidate python uv                 # switch all python tools to uv
    omni consolidate python pip                # switch all python tools to pip3
    omni consolidate node pnpm                 # set node tools to use pnpm

  Provider mode — move every possible tool to one provider (e.g. brew):
    omni consolidate --to brew                 # move all tools to Homebrew
    omni consolidate --to brew --dry-run

Ecosystems and managers:
  python   uv | pip | pip3
  node     npm | pnpm | bun`,
		Args: cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			a := state.app
			out := cmdOut(cmd)

			// Provider mode: --to <provider>
			if toProvider != "" {
				if len(args) > 0 {
					return fmt.Errorf("--to and positional args are mutually exclusive")
				}
				res, err := a.ConsolidateToProvider(ctx, toProvider, dryRun, func(msg string) {
					fmt.Fprintf(out, "  %s\n", msg)
				})
				if err != nil {
					return err
				}
				return printProviderConsolidateResult(out, res, toProvider, dryRun)
			}

			// Ecosystem mode: <ecosystem> <manager>
			if len(args) != 2 {
				return fmt.Errorf("ecosystem mode requires exactly 2 args: <ecosystem> <manager>, or use --to <provider>")
			}
			ecosystem, manager := args[0], args[1]

			if dryRun {
				plan, err := a.ConsolidatePlan(ctx, ecosystem, manager)
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "Dry-run — consolidating %s tools → %s\n\n", ecosystem, manager)
				if len(plan.Migrated) == 0 {
					fmt.Fprintln(out, "  Nothing to migrate (settings would be updated).")
					return nil
				}
				for _, m := range plan.Migrated {
					fmt.Fprintf(out, "  → would migrate: %s (from %s)\n", m.Name, m.FromProvider)
				}
				fmt.Fprintf(out, "\n  %s would be migrated.\n", textutil.PluralCount(len(plan.Migrated), "tool", "tools"))
				return nil
			}

			res, err := a.Consolidate(ctx, ecosystem, manager, func(msg string) {
				fmt.Fprintf(out, "  %s\n", msg)
			})
			if err != nil {
				return err
			}

			if len(res.Migrated) == 0 && len(res.Failed) == 0 {
				fmt.Fprintf(out, "Consolidated %s → %s: nothing to migrate", ecosystem, manager)
				if res.SettingsUpdated {
					fmt.Fprint(out, " (settings updated)")
				}
				fmt.Fprintln(out)
				return nil
			}

			fmt.Fprintf(out, "Consolidated %s tools → %s:\n", ecosystem, manager)
			return printConsolidateLines(out, res, manager)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be done without making changes")
	cmd.Flags().StringVar(&toProvider, "to", "", "move all tools to this provider (e.g. brew)")
	_ = cmd.RegisterFlagCompletionFunc("to", completeProviderNames(state))
	return cmd
}

func printProviderConsolidateResult(out io.Writer, res *app.ConsolidateResult, provider string, dryRun bool) error {
	if dryRun {
		fmt.Fprintf(out, "Dry-run — consolidating all tools → %s\n\n", provider)
		if len(res.Migrated) == 0 {
			fmt.Fprintln(out, "  Nothing to migrate.")
			return nil
		}
		for _, m := range res.Migrated {
			fmt.Fprintf(out, "  → would migrate: %s (from %s)\n", m.Name, m.FromProvider)
		}
		fmt.Fprintf(out, "\n  %s would be migrated.\n", textutil.PluralCount(len(res.Migrated), "tool", "tools"))
		return nil
	}

	if len(res.Migrated) == 0 && len(res.Failed) == 0 {
		fmt.Fprintf(out, "All tools already on %s.\n", provider)
		return nil
	}

	fmt.Fprintf(out, "Consolidated all tools → %s:\n", provider)
	return printConsolidateLines(out, res, provider)
}

func printConsolidateLines(out io.Writer, res *app.ConsolidateResult, manager string) error {
	for _, m := range res.Migrated {
		fmt.Fprintf(out, "  ✓ %s  %s → %s\n", m.Name, m.FromProvider, manager)
	}
	for _, f := range res.Failed {
		fmt.Fprintf(out, "  ✗ %s  %s → %s: %v\n", f.Name, f.FromProvider, manager, f.Err)
	}
	for _, w := range res.UninstallWarnings {
		fmt.Fprintf(out, "  ! %s  could not remove from %s: %v\n", w.Name, w.FromProvider, w.Err)
	}
	fmt.Fprintln(out, "  "+app.ConsolidateSummaryText(res, ""))
	if len(res.Failed) > 0 {
		return fmt.Errorf("%s failed", textutil.PluralCount(len(res.Failed), "tool", "tools"))
	}
	return nil
}

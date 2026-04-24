package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
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

			// Provider mode: --to <provider>
			if toProvider != "" {
				if len(args) > 0 {
					return fmt.Errorf("--to and positional args are mutually exclusive")
				}
				res, err := a.ConsolidateToProvider(ctx, toProvider, dryRun, func(msg string) {
					fmt.Printf("  %s\n", msg)
				})
				if err != nil {
					return err
				}
				printProviderConsolidateResult(res, toProvider, dryRun)
				return nil
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
				fmt.Printf("Dry-run — consolidating %s tools → %s\n\n", ecosystem, manager)
				if len(plan.Migrated) == 0 {
					fmt.Println("  Nothing to migrate (settings would be updated).")
					return nil
				}
				for _, m := range plan.Migrated {
					fmt.Printf("  → would migrate: %s (from %s)\n", m.Name, m.FromProvider)
				}
				fmt.Printf("\n  %d tool(s) would be migrated.\n", len(plan.Migrated))
				return nil
			}

			res, err := a.Consolidate(ctx, ecosystem, manager, func(msg string) {
				fmt.Printf("  %s\n", msg)
			})
			if err != nil {
				return err
			}

			if len(res.Migrated) == 0 && len(res.Failed) == 0 {
				fmt.Printf("Consolidated %s → %s: nothing to migrate", ecosystem, manager)
				if res.SettingsUpdated {
					fmt.Printf(" (settings updated)")
				}
				fmt.Println()
				return nil
			}

			fmt.Printf("Consolidated %s tools → %s:\n", ecosystem, manager)
			printConsolidateLines(res, manager)
			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be done without making changes")
	cmd.Flags().StringVar(&toProvider, "to", "", "move all tools to this provider (e.g. brew)")
	return cmd
}

func printProviderConsolidateResult(res *app.ConsolidateResult, provider string, dryRun bool) {
	if dryRun {
		fmt.Printf("Dry-run — consolidating all tools → %s\n\n", provider)
		if len(res.Migrated) == 0 {
			fmt.Println("  Nothing to migrate.")
			return
		}
		for _, m := range res.Migrated {
			fmt.Printf("  → would migrate: %s (from %s)\n", m.Name, m.FromProvider)
		}
		fmt.Printf("\n  %d tool(s) would be migrated.\n", len(res.Migrated))
		return
	}

	if len(res.Migrated) == 0 && len(res.Failed) == 0 {
		fmt.Printf("All tools already on %s.\n", provider)
		return
	}

	fmt.Printf("Consolidated all tools → %s:\n", provider)
	printConsolidateLines(res, provider)
}

func printConsolidateLines(res *app.ConsolidateResult, manager string) {
	for _, m := range res.Migrated {
		fmt.Printf("  ✓ %s  %s → %s\n", m.Name, m.FromProvider, manager)
	}
	for _, f := range res.Failed {
		fmt.Printf("  ✗ %s  %s → %s: %v\n", f.Name, f.FromProvider, manager, f.Err)
	}
	for _, w := range res.UninstallWarnings {
		fmt.Printf("  ! %s  could not remove from %s: %v\n", w.Name, w.FromProvider, w.Err)
	}
	parts := []string{fmt.Sprintf("%d migrated", len(res.Migrated))}
	if len(res.Failed) > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", len(res.Failed)))
	}
	if len(res.UninstallWarnings) > 0 {
		parts = append(parts, fmt.Sprintf("%d uninstall warning(s)", len(res.UninstallWarnings)))
	}
	if res.SettingsUpdated {
		parts = append(parts, "settings updated")
	}
	fmt.Println("  " + strings.Join(parts, ", "))
}

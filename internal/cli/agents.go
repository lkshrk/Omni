package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/apm"
	"github.com/lkshrk/omni/internal/app"
)

func printAPMResult(cmd *cobra.Command, result apm.Result) {
	fmt.Fprint(cmdOut(cmd), result.Stdout)
	fmt.Fprint(cmd.ErrOrStderr(), result.Stderr)
}

func runAPM(state *rootState, cmd *cobra.Command, args ...string) error {
	result, err := state.app.RunAPM(cmd.Context(), args...)
	printAPMResult(cmd, result)
	return err
}

func newAgentsCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{Use: "agents", Short: "Manage agent dependencies with APM"}
	cmd.AddCommand(
		newAgentsMigrateCmd(state),
		newAPMAgentsSyncCmd(state),
		newAPMAgentsAddCmd(state),
		newAPMAgentsRemoveCmd(state),
		newAPMAgentsUpdateCmd(state),
		newAPMAgentsSearchCmd(state),
		newAPMAgentsAuditCmd(state),
		newAPMAgentsTargetsCmd(state),
		newAPMAgentsOutdatedCmd(state),
		newAPMAgentsPruneCmd(state),
		newAPMAgentsDepsCmd(state),
		newAPMAgentsMarketplaceCmd(state),
	)
	return cmd
}

func printAgentsWarning(cmd *cobra.Command, warning string) {
	if warning != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), "warning:", warning)
	}
}

func newAPMAgentsSyncCmd(state *rootState) *cobra.Command {
	var frozen, dryRun, forceTemplate bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Install the global APM workspace",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := state.app.AgentsSyncAll(cmd.Context(), app.AgentsSyncAllOptions{
				Frozen:        frozen,
				DryRun:        dryRun,
				ForceTemplate: forceTemplate,
				Output: func(stdout, stderr string) {
					fmt.Fprint(cmdOut(cmd), stdout)
					fmt.Fprint(cmd.ErrOrStderr(), stderr)
				},
			})
			printAgentsWarning(cmd, result.Warning)
			return err
		},
	}
	cmd.Flags().BoolVar(&frozen, "frozen", false, "Require apm.yml and apm.lock.yaml to match")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print the install plan without deploying files")
	cmd.Flags().BoolVar(&forceTemplate, "force-template", false, "Overwrite the live manifest with the host template")
	return cmd
}

func newAPMAgentsAddCmd(state *rootState) *cobra.Command {
	var skills []string
	cmd := &cobra.Command{
		Use:   "add <package>",
		Short: "Add and install an APM package",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			apmArgs := []string{"install", "-g", args[0]}
			for _, skill := range skills {
				apmArgs = append(apmArgs, "--skill", skill)
			}
			if err := runAPM(state, cmd, apmArgs...); err != nil {
				return err
			}
			printAgentsTemplateHint(cmd, args[0], false)
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&skills, "skill", nil, "Install only this named skill (repeatable)")
	return cmd
}

func printAgentsTemplateHint(cmd *cobra.Command, spec string, removal bool) {
	out := cmd.ErrOrStderr()
	for _, line := range app.AgentsTemplateHintLines(spec, removal) {
		fmt.Fprintln(out, line)
	}
}

func newAPMAgentsRemoveCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:     "remove <package>...",
		Aliases: []string{"uninstall"},
		Short:   "Remove APM packages and deployed files (no --purge mode)",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := runAPM(state, cmd, append([]string{"uninstall", "-g"}, args...)...); err != nil {
				return err
			}
			printAgentsTemplateHint(cmd, args[0], true)
			return nil
		},
	}
}

func newAPMAgentsUpdateCmd(state *rootState) *cobra.Command {
	return apmLeaf(state, "update", "Update global APM dependencies", cobra.NoArgs, func([]string) []string {
		return []string{"update", "-g", "--yes"}
	})
}

func newAPMAgentsSearchCmd(state *rootState) *cobra.Command {
	return apmLeaf(state, "search <query>", "Search registered APM marketplaces", cobra.ExactArgs(1), func(args []string) []string {
		return []string{"search", args[0]}
	})
}

func newAPMAgentsAuditCmd(state *rootState) *cobra.Command {
	return apmLeaf(state, "audit", "Audit the global APM workspace", cobra.NoArgs, func([]string) []string {
		return []string{"audit", "--ci"}
	})
}

func newAPMAgentsTargetsCmd(state *rootState) *cobra.Command {
	return apmLeaf(state, "targets", "Show resolved APM targets", cobra.NoArgs, func([]string) []string {
		return []string{"targets", "--json"}
	})
}

func newAPMAgentsOutdatedCmd(state *rootState) *cobra.Command {
	return apmLeaf(state, "outdated", "Show outdated global APM dependencies", cobra.NoArgs, func([]string) []string {
		return []string{"outdated", "-g"}
	})
}

func newAPMAgentsPruneCmd(state *rootState) *cobra.Command {
	return apmLeaf(state, "prune", "Remove unused APM dependencies", cobra.NoArgs, func([]string) []string {
		return []string{"prune"}
	})
}

func newAPMAgentsDepsCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{Use: "deps", Short: "Inspect global APM dependencies"}
	cmd.AddCommand(
		apmLeaf(state, "list", "List installed dependencies", cobra.NoArgs, func([]string) []string {
			return []string{"deps", "list", "-g"}
		}),
		apmLeaf(state, "why <package>", "Explain why a dependency is installed", cobra.ExactArgs(1), func(args []string) []string {
			return []string{"deps", "why", "-g", args[0]}
		}),
		apmLeaf(state, "info <package>", "Show resolved package information", cobra.ExactArgs(1), func(args []string) []string {
			return []string{"view", "--global", args[0]}
		}),
	)
	return cmd
}

func newAPMAgentsMarketplaceCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{Use: "marketplace", Short: "Manage APM marketplaces"}
	cmd.AddCommand(
		newAPMAgentsMarketplaceAddCmd(state),
		apmLeaf(state, "list", "List registered marketplaces", cobra.NoArgs, func([]string) []string {
			return []string{"marketplace", "list"}
		}),
		apmLeaf(state, "browse <name>", "Browse a marketplace", cobra.ExactArgs(1), func(args []string) []string {
			return []string{"marketplace", "browse", args[0]}
		}),
		apmLeaf(state, "update [name]", "Refresh marketplace data", cobra.MaximumNArgs(1), func(args []string) []string {
			return append([]string{"marketplace", "update"}, args...)
		}),
		apmLeaf(state, "validate <name>", "Validate a marketplace", cobra.ExactArgs(1), func(args []string) []string {
			return []string{"marketplace", "validate", args[0]}
		}),
		apmLeaf(state, "remove <name>", "Remove a marketplace", cobra.ExactArgs(1), func(args []string) []string {
			return []string{"marketplace", "remove", args[0]}
		}),
	)
	return cmd
}

func newAPMAgentsMarketplaceAddCmd(state *rootState) *cobra.Command {
	var name, ref string
	cmd := &cobra.Command{
		Use:   "add <source>",
		Short: "Register a marketplace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			apmArgs := []string{"marketplace", "add", args[0]}
			if name != "" {
				apmArgs = append(apmArgs, "--name", name)
			}
			if ref != "" {
				apmArgs = append(apmArgs, "--ref", ref)
			}
			return runAPM(state, cmd, apmArgs...)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Marketplace display name")
	cmd.Flags().StringVar(&ref, "ref", "", "Git ref")
	return cmd
}

func apmLeaf(state *rootState, use, short string, validate cobra.PositionalArgs, args func([]string) []string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  validate,
		RunE: func(cmd *cobra.Command, positional []string) error {
			return runAPM(state, cmd, args(positional)...)
		},
	}
}

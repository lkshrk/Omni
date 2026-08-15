package cli

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/apm"
	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func printImportSkillsDiff(w io.Writer, diff app.ImportDiff) {
	fmt.Fprintln(w, app.ImportDiffSummaryText(diff))
	for _, n := range diff.Added {
		fmt.Fprintf(w, "  + %s\n", n)
	}
	for _, n := range diff.Updated {
		fmt.Fprintf(w, "  ~ %s\n", n)
	}
	for _, warning := range diff.Warnings {
		fmt.Fprintf(w, "  ! %s\n", warning)
	}
}

// Warnings, drift and failures come from the flattened report so nothing repeats per feature.
func printAgentsSyncAllResult(w io.Writer, res app.AgentsSyncAllResult, dryRun bool) {
	if res.Output != "" {
		fmt.Fprint(w, res.Output)
		if !strings.HasSuffix(res.Output, "\n") {
			fmt.Fprintln(w)
		}
	}
	if res.Stderr != "" {
		fmt.Fprint(w, res.Stderr)
		if !strings.HasSuffix(res.Stderr, "\n") {
			fmt.Fprintln(w)
		}
	}
	for _, msg := range res.Warnings {
		fmt.Fprintf(w, "warn: %s\n", msg)
	}
	if len(res.Imported.Added) > 0 || len(res.Imported.Updated) > 0 {
		fmt.Fprintf(w, "skills import: %s\n", app.ImportDiffSummaryText(res.Imported))
		for _, n := range res.Imported.Added {
			fmt.Fprintf(w, "  + %s\n", n)
		}
		for _, n := range res.Imported.Updated {
			fmt.Fprintf(w, "  ~ %s\n", n)
		}
	}
	if res.McpAdopted.Adopted > 0 {
		fmt.Fprintf(w, "mcp import: %d claimed\n", res.McpAdopted.Adopted)
	}
	// A server omni declined to claim is the reason the user would look at this output at all; silence here reads as a clean adoption.
	for _, line := range res.McpAdopted.Conflicts {
		fmt.Fprintf(w, "  ! mcp import: %s\n", line)
	}
	for _, line := range res.McpAdopted.Skipped {
		fmt.Fprintf(w, "  ! mcp import: %s\n", line)
	}
	for _, line := range res.McpAdopted.Warnings {
		fmt.Fprintf(w, "  ! mcp import: %s\n", line)
	}
	if res.PluginsAdopted.Adopted > 0 {
		fmt.Fprintf(w, "plugins import: %d claimed\n", res.PluginsAdopted.Adopted)
	}
	if dryRun {
		for _, line := range res.SkillsDryRun {
			fmt.Fprintf(w, "skills: %s\n", line)
		}
		for _, s := range res.Mcp.WouldInstall {
			fmt.Fprintf(w, "mcp: would install %s\n", s)
		}
		for _, s := range res.Mcp.WouldUpdate {
			fmt.Fprintf(w, "mcp: would update %s\n", s)
		}
		for _, p := range res.Plugins.WouldInstall {
			fmt.Fprintf(w, "plugins: would install %s\n", p)
		}
		for _, s := range res.McpAdopted.WouldAdopt {
			fmt.Fprintf(w, "mcp: %s\n", s)
		}
		for _, p := range res.PluginsAdopted.WouldAdopt {
			fmt.Fprintf(w, "plugins: %s\n", p)
		}
	} else {
		fmt.Fprintf(w, "skills: %s\n", app.RestoreSkillsSummaryText(res.Skills))
		for _, s := range res.Mcp.Installed {
			fmt.Fprintf(w, "mcp: installed %s\n", s)
		}
		for _, s := range res.Mcp.Updated {
			fmt.Fprintf(w, "mcp: updated %s\n", s)
		}
		for _, s := range res.Mcp.AlreadyInstalled {
			fmt.Fprintf(w, "mcp: already installed %s\n", s)
		}
		for _, p := range res.Plugins.Installed {
			fmt.Fprintf(w, "plugins: installed %s\n", p)
		}
		for _, p := range res.Plugins.AlreadyInstalled {
			fmt.Fprintf(w, "plugins: already installed %s\n", p)
		}
	}
	for _, s := range res.Skills.ShadowedByPlugin {
		fmt.Fprintf(w, "  skipped (provided by plugin): %s\n", s)
	}
	for _, s := range res.Mcp.ShadowedByPlugin {
		fmt.Fprintf(w, "  skipped (provided by plugin): %s\n", s)
	}
	for _, d := range res.Drift {
		fmt.Fprintf(w, "  ~ drift: %s\n", d)
	}
	for _, e := range res.Errors {
		fmt.Fprintf(w, "  ! %s: %s\n", e.Feature, e.Message)
	}
}

func printSkippedUnavailable(cmd *cobra.Command, skipped []string) {
	for _, s := range skipped {
		fmt.Fprintf(cmdOut(cmd), "  ! %s: skipped, agent CLI not found on PATH\n", s)
	}
}

func newAgentsCmd(state *rootState) *cobra.Command {
	global := true
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "Manage agent packages with APM",
	}
	sync := newAPMAgentsSyncCmd(state, &global)
	cmd.AddCommand(
		sync,
		deprecatedAlias(sync, "restore", nil),
		newAPMAgentsAddCmd(state, &global),
		newAPMAgentsRemoveCmd(state, &global),
		newAPMAgentsUpdateCmd(state, &global),
		newAPMAgentsSearchCmd(state, &global),
	)
	return cmd
}

func apmClient(state *rootState, global bool) *apm.Client {
	scope := apm.Project
	if global {
		scope = apm.Global
	}
	return state.app.APMClient(scope)
}

func printAPMResult(cmd *cobra.Command, result apm.Result) {
	fmt.Fprint(cmdOut(cmd), result.Stdout)
	fmt.Fprint(cmd.ErrOrStderr(), result.Stderr)
}

func migrateAgentsToAPM(cmd *cobra.Command, state *rootState, dryRun bool) error {
	if dryRun {
		return nil
	}
	result, err := state.app.MigrateAgentsToAPM()
	for _, warning := range result.Warnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", warning)
	}
	if result.MigratedPackages > 0 || result.MigratedMCPServers > 0 {
		fmt.Fprintf(cmdOut(cmd), "migrated %d packages and %d MCP servers to %s\n", result.MigratedPackages, result.MigratedMCPServers, result.Path)
	}
	return err
}

func newAPMAgentsSyncCmd(state *rootState, global *bool) *cobra.Command {
	var frozen, dryRun bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Install dependencies from apm.yml",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := state.app.AgentsSyncAll(cmd.Context(), app.AgentsSyncAllOptions{
				Frozen: frozen, DryRun: dryRun,
				Output: func(stdout, stderr string) {
					fmt.Fprint(cmdOut(cmd), stdout)
					fmt.Fprint(cmd.ErrOrStderr(), stderr)
				},
			})
			result.Output, result.Stderr = "", ""
			printAgentsSyncAllResult(cmdOut(cmd), result, dryRun)
			return err
		},
	}
	cmd.Flags().BoolVar(&frozen, "frozen", false, "Require apm.yml and apm.lock.yaml to match")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print the install plan without deploying files")
	return cmd
}

func newAPMAgentsAddCmd(state *rootState, global *bool) *cobra.Command {
	return &cobra.Command{Use: "add <package>...", Short: "Add packages to apm.yml and install them", Args: cobra.MinimumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if err := migrateAgentsToAPM(cmd, state, false); err != nil {
			return err
		}
		result, err := apmClient(state, *global).Add(cmd.Context(), args...)
		printAPMResult(cmd, result)
		return err
	}}
}

func newAPMAgentsRemoveCmd(state *rootState, global *bool) *cobra.Command {
	return &cobra.Command{Use: "remove <package>...", Aliases: []string{"uninstall"}, Short: "Remove packages and their deployed files with APM (no --purge mode)", Args: cobra.MinimumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if err := migrateAgentsToAPM(cmd, state, false); err != nil {
			return err
		}
		result, err := apmClient(state, *global).Uninstall(cmd.Context(), args...)
		printAPMResult(cmd, result)
		return err
	}}
}

func newAPMAgentsUpdateCmd(state *rootState, global *bool) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{Use: "update [package]...", Short: "Update locked APM dependencies", Args: cobra.ArbitraryArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if err := migrateAgentsToAPM(cmd, state, dryRun); err != nil {
			return err
		}
		result, err := apmClient(state, *global).Update(cmd.Context(), dryRun, args...)
		printAPMResult(cmd, result)
		return err
	}}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print the update plan without writing")
	return cmd
}

func newAPMAgentsSearchCmd(state *rootState, global *bool) *cobra.Command {
	return &cobra.Command{Use: "search <query@marketplace>", Aliases: []string{"find"}, Short: "Search a registered APM marketplace", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apmClient(state, *global).Search(cmd.Context(), args[0])
		printAPMResult(cmd, result)
		return err
	}}
}

// Without this the process exits 0 on a partial failure and scripted callers chain onto work that did not happen.
func agentErrsFailure(n int) error {
	if n == 0 {
		return nil
	}
	return fmt.Errorf("%d agent operation(s) failed", n)
}

func newAgentsFindCmd(state *rootState) *cobra.Command {
	var owner string
	cmd := &cobra.Command{
		Use:   "find <query>",
		Short: "Search skills.sh for skill packages",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			results, err := state.app.FindSkillPackages(cmd.Context(), strings.Join(args, " "), owner)
			if err != nil && !app.IsCatalogWarning(err) {
				return err
			}
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", err)
			}
			for _, r := range results {
				fmt.Fprintf(cmdOut(cmd), "%s  (%s)  %s\n", r.Source, r.Skill, r.Installs)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&owner, "owner", "", "limit results to a GitHub owner")
	return cmd
}

func newAgentsSkillsCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Sync, upgrade, and import agent skills",
	}
	sync := newAgentsSyncSkillsCmd(state)
	upgrade := newAgentsUpgradeSkillsCmd(state)
	cmd.AddCommand(
		sync,
		deprecatedAlias(sync, "restore", nil),
		newAgentsImportSkillsCmd(state),
		upgrade,
		deprecatedAlias(upgrade, "update", nil),
		newAgentsResolveSkillsCmd(state),
		newAgentsSkillsStatusCmd(state),
		newAgentsRemoveSkillPackageCmd(state),
		newAgentsUninstallSkillPackageCmd(state),
		newAgentsSkillsGroupCmd(state),
	)
	return cmd
}

func newAgentsResolveSkillsCmd(state *rootState) *cobra.Command {
	var useManaged bool
	var useLocal bool
	var agents []string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "resolve <source>[@skill]",
		Short: "Settle a skill entry whose live content diverged from omni's store",
		Long: "Settle a skill entry another tool took over. --use-managed replaces the " +
			"foreign content with omni's managed link, keeping the displaced copy only " +
			"until the install succeeds. --use-local leaves that content alone and narrows " +
			"the manifest so omni stops managing the entry.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if useManaged == useLocal {
				return fmt.Errorf("choose exactly one of --use-managed or --use-local")
			}
			opts := app.ResolveSkillDriftOptions{
				Source:   args[0],
				Agents:   agents,
				Strategy: app.SkillDriftUseManaged,
				DryRun:   dryRun,
			}
			if useLocal {
				opts.Strategy = app.SkillDriftUseLocal
			}
			if useManaged && !dryRun {
				ok, err := confirmAction(cmd, state, fmt.Sprintf(
					"Replace the foreign skill content for %q with omni's managed version?", args[0]))
				if err != nil || !ok {
					return err
				}
			}
			res, err := state.app.ResolveSkillDrift(cmd.Context(), opts)
			printDriftResolution(cmdOut(cmd), res.Actions, res.Warnings, dryRun)
			return err
		},
	}
	cmd.Flags().BoolVar(&useManaged, "use-managed", false, "Replace the foreign entry with omni's managed content")
	cmd.Flags().BoolVar(&useLocal, "use-local", false, "Keep the foreign entry and stop managing it")
	cmd.Flags().StringArrayVar(&agents, "agent", nil, "Limit the resolution to this agent target (repeatable)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be resolved without making changes")
	return cmd
}

// Renders every resolve verb identically so the three surfaces read the same whichever capability drifted.
func printDriftResolution(w io.Writer, actions, warnings []string, dryRun bool) {
	for _, warning := range warnings {
		fmt.Fprintf(w, "  ! %s\n", warning)
	}
	prefix := ""
	if dryRun {
		prefix = "would "
	}
	for _, action := range actions {
		fmt.Fprintf(w, "%s%s\n", prefix, action)
	}
}

func newAgentsSkillsGroupCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "group <source> <group>...",
		Short: "Set a skill package's full group membership",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			source := args[0]
			groups := args[1:]
			if err := state.app.SetSkillGroups(source, groups, nil, ""); err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "Set %q groups to %s.\n", source, strings.Join(groups, ", "))
			return nil
		},
	}
}

func newAgentsRemoveSkillPackageCmd(state *rootState) *cobra.Command {
	var purge bool
	cmd := &cobra.Command{
		Use:   "remove <source>",
		Short: "Undeclare a skill package from the manifest",
		Long: "Drop a skill package from the manifest. The installed links and store content stay " +
			"on this host, so a later sync of another machine's manifest is unaffected; pass --purge " +
			"to remove the installed entries and unreferenced content too.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if purge {
				// A package declared but never synced here has nothing to uninstall, yet undeclaring must still work.
				switch err := state.app.UninstallSkillPackage(cmd.Context(), args[0]); {
				case err == nil:
					fmt.Fprintf(cmdOut(cmd), "uninstalled %s\n", args[0])
				case app.IsSkillPackageNotInstalled(err):
					fmt.Fprintf(cmdOut(cmd), "nothing installed for %s\n", args[0])
				default:
					return err
				}
			}
			if err := state.app.RemoveSkillPackage(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "removed %s from manifest\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&purge, "purge", false, "also remove the installed agent entries and unreferenced store content")
	return cmd
}

// Not an alias of remove: bare uninstall never touched the manifest, and `remove --purge` is the composed replacement.
func newAgentsUninstallSkillPackageCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:         "uninstall <source>",
		Short:       "Uninstall a skill package from agent skill directories",
		Hidden:      true,
		Annotations: map[string]string{annotationDeprecatedAlias: "remove"},
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deprecationNotice(cmd, "agents skills remove --purge")
			if err := state.app.UninstallSkillPackage(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "uninstalled %s\n", args[0])
			return nil
		},
	}
}

func newAgentsUpgradeSkillsCmd(state *rootState) *cobra.Command {
	var opts app.UpdateSkillsOptions
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Refresh omni's stored skill packages from their sources",
		Long: "Reacquire every manifest skill package from upstream into omni's store, then relink " +
			"the agents that use it. The manifest is unchanged: this moves newer content, not intent.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if opts.DryRun && opts.Check {
				return fmt.Errorf("choose one of --dry-run or --check")
			}
			output, dryRun, err := state.app.UpdateSkills(cmd.Context(), opts)
			if opts.DryRun {
				fmt.Fprintln(cmdOut(cmd), dryRun)
			} else {
				fmt.Fprintln(cmdOut(cmd), output)
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "print native upgrade actions without running them")
	cmd.Flags().BoolVar(&opts.Check, "check", false, "probe each source and report what is outdated, without refreshing")
	return cmd
}

func newAgentsSyncSkillsCmd(state *rootState) *cobra.Command {
	var opts app.RestoreSkillsOptions
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Install the manifest skill set onto this host",
		Long: "Move the manifest's skill packages onto this host: install the missing ones, repair " +
			"entries that drifted into an identical copy, and leave matching ones alone. Adopting " +
			"packages the manifest does not declare runs the other way — see import.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, lines, err := state.app.RestoreSkills(cmd.Context(), opts)
			if err != nil {
				return err
			}
			for _, w := range res.Warnings {
				fmt.Fprintf(cmdOut(cmd), "warn: %s\n", w)
			}
			if opts.DryRun {
				for _, l := range lines {
					fmt.Fprintln(cmdOut(cmd), l)
				}
				for _, s := range res.ShadowedByPlugin {
					fmt.Fprintf(cmdOut(cmd), "  skipped (provided by plugin): %s\n", s)
				}
				return nil
			}
			fmt.Fprintln(cmdOut(cmd), app.RestoreSkillsSummaryText(res))
			for _, f := range res.Failed {
				fmt.Fprintf(cmdOut(cmd), "  ! %s: %s\n", f.Name, f.Message)
			}
			for _, d := range res.Drift {
				fmt.Fprintf(cmdOut(cmd), "  ~ drift: %s\n", d)
			}
			for _, s := range res.ShadowedByPlugin {
				fmt.Fprintf(cmdOut(cmd), "  skipped (provided by plugin): %s\n", s)
			}
			return agentErrsFailure(len(res.Failed))
		},
	}
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "print native sync actions without running them")
	return cmd
}

func newAgentsImportSkillsCmd(state *rootState) *cobra.Command {
	var opts app.ImportSkillsOptions
	cmd := &cobra.Command{
		Use:   "import [<source>]",
		Short: "Adopt skill packages this host already has into the manifest",
		Long: "Move installed-but-undeclared skill packages from this host into the manifest, " +
			"adopting their on-disk content into omni's store. With a source, only that package is " +
			"adopted. Installing what the manifest already declares runs the other way — see sync.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				opts.Source = args[0]
			}
			diff, err := state.app.ImportSkills(cmd.Context(), opts)
			if err != nil {
				return err
			}
			printImportSkillsDiff(cmdOut(cmd), diff)
			return nil
		},
	}
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "show the manifest diff without writing")
	return cmd
}

func newAgentsSkillsStatusCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "status <source>[@skill]",
		Short: "Show one skill package's manifest, store, and per-agent entry state",
		Long: "Report one package's position in all three stores: what the manifest declares, what " +
			"omni's store holds, and what every targeted agent directory actually has, with the next " +
			"step for each entry.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			status, err := state.app.SkillPackageStatus(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			printSkillPackageStatus(cmdOut(cmd), status)
			return nil
		},
	}
}

func printSkillPackageStatus(w io.Writer, s app.SkillPackageStatus) {
	fmt.Fprintf(w, "%s\n", s.Source)
	fmt.Fprintf(w, "  manifest:  %s\n", skillStatusManifestLine(s))
	if s.Ref != "" {
		fmt.Fprintf(w, "  ref:       %s\n", s.Ref)
	}
	if len(s.Groups) > 0 {
		fmt.Fprintf(w, "  groups:    %s\n", strings.Join(s.Groups, ", "))
	}
	if len(s.Selectors) > 0 {
		fmt.Fprintf(w, "  selectors: %s\n", strings.Join(s.Selectors, ", "))
	}
	fmt.Fprintf(w, "  agents:    %s\n", skillStatusAgentsLine(s))
	fmt.Fprintf(w, "  skills:    %s\n", skillStatusListLine(s.Skills))
	fmt.Fprintf(w, "  store:     %s\n", skillStatusStoreLine(s))
	fmt.Fprintf(w, "  updates:   %s\n", skillStatusUpdatesLine(s))
	if len(s.Lockfile) > 0 {
		fmt.Fprintln(w, "  lockfile:")
		for _, e := range s.Lockfile {
			line := "    " + e.Skill
			if e.Ref != "" {
				line += " @ " + e.Ref
			}
			if e.UpdatedAt != "" {
				line += " (updated " + e.UpdatedAt + ")"
			}
			fmt.Fprintln(w, line)
		}
	}
	if len(s.Entries) > 0 {
		fmt.Fprintln(w, "  entries:")
		for _, e := range s.Entries {
			fmt.Fprintf(w, "    %s  %s  %s — %s\n", e.Agent, e.State, e.Path, e.Detail)
			if e.Hint != "" {
				fmt.Fprintf(w, "      -> %s\n", e.Hint)
			}
		}
	}
	for _, hint := range s.Hints {
		fmt.Fprintf(w, "  -> %s\n", hint)
	}
	for _, warning := range s.Warnings {
		fmt.Fprintf(w, "  ! %s\n", warning)
	}
}

func skillStatusManifestLine(s app.SkillPackageStatus) string {
	if s.Managed {
		return "managed"
	}
	return "not in manifest"
}

func skillStatusAgentsLine(s app.SkillPackageStatus) string {
	if len(s.Targets) == 0 {
		return "none"
	}
	line := strings.Join(s.Targets, ", ")
	if s.Managed && len(s.Agents) == 0 {
		line += " (all enabled agents)"
	}
	return line
}

func skillStatusListLine(values []string) string {
	if len(values) == 0 {
		return "none installed"
	}
	return strings.Join(values, ", ")
}

func skillStatusStoreLine(s app.SkillPackageStatus) string {
	if s.ContentHash == "" {
		return s.PackageDir + " (empty)"
	}
	return fmt.Sprintf("%s (%s)", s.PackageDir, s.ContentHash[:min(12, len(s.ContentHash))])
}

func skillStatusUpdatesLine(s app.SkillPackageStatus) string {
	line := "unknown"
	switch s.Outdated {
	case app.SkillOutdatedBehind:
		line = "outdated"
	case app.SkillOutdatedCurrent:
		line = "up to date"
	}
	if !s.OutdatedCheckedAt.IsZero() {
		line += ", checked " + s.OutdatedCheckedAt.Format("2006-01-02")
	} else {
		line += ", never checked"
	}
	if s.Updated != "" {
		line += ", installed " + s.Updated
	}
	return line
}

func newAgentsMcpCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Manage MCP servers in the agent manifest",
	}
	sync := newAgentsMcpSyncCmd(state)
	cmd.AddCommand(
		newAgentsMcpListCmd(state),
		newAgentsMcpAddCmd(state),
		newAgentsMcpRemoveCmd(state),
		sync,
		deprecatedAlias(sync, "restore", nil),
		newAgentsMcpImportCmd(state),
		newAgentsMcpResolveCmd(state),
		newAgentsMcpGroupCmd(state),
	)
	return cmd
}

func newAgentsMcpResolveCmd(state *rootState) *cobra.Command {
	var useManaged bool
	var useLocal bool
	var agents []string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "resolve <name>",
		Short: "Settle an MCP server whose live registration diverged from the manifest",
		Long: "Settle an MCP server whose live registration no longer matches the manifest. " +
			"--use-managed reinstalls the manifest definition through the agent's own CLI, " +
			"discarding what it holds. --use-local adopts the live definition as the new " +
			"manifest intent.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if useManaged == useLocal {
				return fmt.Errorf("choose exactly one of --use-managed or --use-local")
			}
			opts := app.ResolveMcpDriftOptions{
				Name:     args[0],
				Agents:   agents,
				Strategy: app.McpDriftUseManaged,
				DryRun:   dryRun,
			}
			if useLocal {
				opts.Strategy = app.McpDriftUseLocal
			}
			if useManaged && !dryRun {
				ok, err := confirmAction(cmd, state, fmt.Sprintf(
					"Replace the live registration for %q with the manifest definition?", args[0]))
				if err != nil || !ok {
					return err
				}
			}
			res, err := state.app.ResolveMcpDrift(cmd.Context(), opts)
			printDriftResolution(cmdOut(cmd), res.Actions, res.Warnings, dryRun)
			return err
		},
	}
	cmd.Flags().BoolVar(&useManaged, "use-managed", false, "Reinstall the manifest definition on the agent")
	cmd.Flags().BoolVar(&useLocal, "use-local", false, "Adopt the live definition into the manifest")
	cmd.Flags().StringArrayVar(&agents, "agent", nil, "Limit the resolution to this agent target (repeatable)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be resolved without making changes")
	return cmd
}

func newAgentsMcpGroupCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "group <name> <group>...",
		Short: "Set an MCP server's full group membership",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			groups := args[1:]
			if err := state.app.SetMcpGroups(cmd.Context(), name, groups); err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "Set %q groups to %s.\n", name, strings.Join(groups, ", "))
			return nil
		},
	}
}

func newAgentsMcpListCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List managed and unmanaged MCP servers",
		RunE: func(cmd *cobra.Command, _ []string) error {
			managed, unmanaged, err := state.app.McpServerRows(cmd.Context())
			if err != nil {
				return err
			}
			w := cmdOut(cmd)
			for _, row := range managed {
				agentIDs := make([]string, 0, len(row.PerAgentStatus))
				for id := range row.PerAgentStatus {
					agentIDs = append(agentIDs, id)
				}
				sort.Strings(agentIDs)
				agentParts := make([]string, 0, len(agentIDs))
				for _, id := range agentIDs {
					marker := "-"
					switch row.PerAgentStatus[id] {
					case app.McpStatusInstalled:
						marker = "✓"
					case app.McpStatusShadowed:
						marker = "via-plugin"
					case app.McpStatusDrifted:
						marker = "drift"
					}
					agentParts = append(agentParts, fmt.Sprintf("%s(%s)", id, marker))
				}
				line := row.Name + "  " + row.Transport
				if len(agentParts) > 0 {
					line += "  " + strings.Join(agentParts, " ")
				}
				fmt.Fprintln(w, line)
			}
			agentIDs := make([]string, 0, len(unmanaged))
			for id := range unmanaged {
				agentIDs = append(agentIDs, id)
			}
			sort.Strings(agentIDs)
			for _, id := range agentIDs {
				fmt.Fprintf(w, "\n-- unmanaged (%s) --\n", id)
				for _, s := range unmanaged[id] {
					fmt.Fprintf(w, "%s  %s\n", s.Name, s.Transport)
				}
			}
			return nil
		},
	}
}

func newAgentsMcpAddCmd(state *rootState) *cobra.Command {
	var (
		name        string
		transport   string
		command     string
		url         string
		envVars     []string
		envLiteral  []string
		headerSpecs []string
		agents      []string
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Declare an MCP server in the manifest and register it here",
		Long: "Record an MCP server as manifest intent and register it through the targeted agents' " +
			"own CLIs — declaration and one host's convergence in a single step. Other hosts pick it " +
			"up on their next sync.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			switch transport {
			case "stdio":
				if command == "" {
					return fmt.Errorf("--command is required for stdio transport")
				}
				if url != "" {
					return fmt.Errorf("--url is not valid for stdio transport")
				}
				if len(headerSpecs) > 0 {
					return fmt.Errorf("--header is not valid for stdio transport")
				}
			case "http", "sse":
				if url == "" {
					return fmt.Errorf("--url is required for %s transport", transport)
				}
				if command != "" {
					return fmt.Errorf("--command is not valid for %s transport", transport)
				}
			default:
				return fmt.Errorf("--transport must be stdio, http, or sse")
			}
			var envLit map[string]string
			if len(envLiteral) > 0 {
				envLit = make(map[string]string, len(envLiteral))
				for _, kv := range envLiteral {
					k, v, ok := strings.Cut(kv, "=")
					if !ok {
						return fmt.Errorf("--env-literal %q: must be KEY=VALUE", kv)
					}
					if _, dup := envLit[k]; dup {
						return fmt.Errorf("--env-literal %q: duplicate key %q", kv, k)
					}
					envLit[k] = v
				}
			}
			var headers map[string]string
			if len(headerSpecs) > 0 {
				headers = make(map[string]string, len(headerSpecs))
				for _, spec := range headerSpecs {
					name, value, ok := strings.Cut(spec, ":")
					name = strings.TrimSpace(name)
					if !ok || name == "" {
						return fmt.Errorf("--header %q: must be NAME: VALUE", spec)
					}
					if _, dup := headers[name]; dup {
						return fmt.Errorf("--header %q: duplicate name %q", spec, name)
					}
					headers[name] = strings.TrimSpace(value)
				}
			}
			s := config.McpServer{
				Name:       name,
				Transport:  transport,
				Command:    command,
				URL:        url,
				Env:        envVars,
				EnvLiteral: envLit,
				Headers:    headers,
				Agents:     agents,
			}
			res, err := state.app.AddMcpServer(cmd.Context(), s)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "added %s\n", name)
			for _, e := range res.Errors {
				fmt.Fprintf(cmdOut(cmd), "  ! %s/%s: %v\n", e.AgentID, e.ServerName, e.Err)
			}
			printSkippedUnavailable(cmd, res.SkippedUnavailable)
			return agentErrsFailure(len(res.Errors))
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "server name (required)")
	cmd.Flags().StringVar(&transport, "transport", "", "transport type: stdio, http, or sse (required)")
	cmd.Flags().StringVar(&command, "command", "", "command for stdio transport")
	cmd.Flags().StringVar(&url, "url", "", "URL for http/sse transport")
	cmd.Flags().StringArrayVar(&envVars, "env", nil, "env var name to forward (repeatable)")
	cmd.Flags().StringArrayVar(&envLiteral, "env-literal", nil, "env var as KEY=VALUE (repeatable)")
	cmd.Flags().StringArrayVar(&headerSpecs, "header", nil, "HTTP header as NAME: VALUE (repeatable)")
	cmd.Flags().StringArrayVar(&agents, "agents", nil, "target agent IDs (repeatable)")
	return cmd
}

func newAgentsMcpRemoveCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Undeclare an MCP server and unregister it from targeted agents",
		Long: "Drop an MCP server from the manifest and unregister it through the agent CLIs that " +
			"held it. Unlike `agents skills remove`, the live side always goes with it — a server " +
			"only exists as its registration, so there is nothing left to keep and no --purge. " +
			"Adopting a live server instead of dropping it runs the other way — see import.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := state.app.RemoveMcpServer(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "removed %s\n", args[0])
			for _, e := range res.Errors {
				fmt.Fprintf(cmdOut(cmd), "  ! %s/%s: %v\n", e.AgentID, e.ServerName, e.Err)
			}
			printSkippedUnavailable(cmd, res.SkippedUnavailable)
			return agentErrsFailure(len(res.Errors))
		},
	}
}

func newAgentsMcpSyncCmd(state *rootState) *cobra.Command {
	var opts app.RestoreMcpOptions
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Install the manifest MCP servers onto this host",
		Long: "Move the manifest's MCP servers onto this host through each agent's own CLI: register " +
			"the missing ones, update registrations that drifted, and leave matching ones alone. " +
			"Adopting servers the manifest does not declare runs the other way — see import.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, err := state.app.RestoreMcpServers(cmd.Context(), opts)
			if err != nil {
				return err
			}
			w := cmdOut(cmd)
			for _, msg := range res.Warnings {
				fmt.Fprintf(w, "warn: %s\n", msg)
			}
			if opts.DryRun {
				for _, s := range res.WouldInstall {
					fmt.Fprintf(w, "would install: %s\n", s)
				}
				for _, s := range res.WouldUpdate {
					fmt.Fprintf(w, "would update: %s\n", s)
				}
				return nil
			}
			for _, s := range res.Installed {
				fmt.Fprintf(w, "installed: %s\n", s)
			}
			for _, s := range res.Updated {
				fmt.Fprintf(w, "updated: %s\n", s)
			}
			for _, s := range res.AlreadyInstalled {
				fmt.Fprintf(w, "already installed: %s\n", s)
			}
			for _, s := range res.ShadowedByPlugin {
				fmt.Fprintf(w, "skipped (provided by plugin): %s\n", s)
			}
			for _, d := range res.Drift {
				fmt.Fprintf(w, "  ~ drift: %s\n", d)
			}
			for _, e := range res.Errors {
				fmt.Fprintf(w, "  ! %s/%s: %v\n", e.AgentID, e.ServerName, e.Err)
			}
			return agentErrsFailure(len(res.Errors))
		},
	}
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "print what would be installed without running")
	return cmd
}

func newAgentsMcpImportCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "import [<name>]",
		Short: "Adopt an MCP server this host already registers into the manifest",
		Long: "Move a live MCP server registration into the manifest. Without a name it lists what " +
			"each agent holds unmanaged; with one it adopts that server's transport, command or URL " +
			"as the manifest's intent. Installing what the manifest declares runs the other way — " +
			"see sync.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			diff, err := state.app.ImportMcpServers(cmd.Context())
			if err != nil {
				return err
			}
			if len(args) == 1 {
				return importMcpServerByName(cmd, state, diff, args[0])
			}
			w := cmdOut(cmd)
			agentIDs := make([]string, 0, len(diff.Unmanaged))
			for id := range diff.Unmanaged {
				agentIDs = append(agentIDs, id)
			}
			sort.Strings(agentIDs)
			for _, id := range agentIDs {
				fmt.Fprintf(w, "-- unmanaged (%s) --\n", id)
				for _, s := range diff.Unmanaged[id] {
					fmt.Fprintf(w, "%s\n", s.Name)
				}
			}
			return nil
		},
	}
}

func importMcpServerByName(cmd *cobra.Command, state *rootState, diff app.McpImportDiff, name string) error {
	agentIDs := make([]string, 0, len(diff.Unmanaged))
	for id := range diff.Unmanaged {
		agentIDs = append(agentIDs, id)
	}
	sort.Strings(agentIDs)

	var match app.InstalledMcpServer
	found := false
	var matchedAgents []string
	for _, id := range agentIDs {
		for _, s := range diff.Unmanaged[id] {
			if s.Name != name {
				continue
			}
			if !found {
				match = s
				found = true
			} else if s.Transport != match.Transport || s.Command != match.Command || s.URL != match.URL ||
				!maps.Equal(s.Headers, match.Headers) || !maps.Equal(s.EnvLiteral, match.EnvLiteral) {
				return fmt.Errorf("mcp server %q is unmanaged under multiple agents with conflicting configuration; import each manually", name)
			}
			matchedAgents = append(matchedAgents, id)
		}
	}
	if !found {
		return fmt.Errorf("mcp server %q is not unmanaged in any agent CLI", name)
	}

	env, refusals := state.app.McpAdoptCheck(match)
	if len(refusals) > 0 {
		return errors.New(strings.Join(refusals, "; "))
	}

	s := config.McpServer{
		Name:      match.Name,
		Transport: match.Transport,
		Command:   match.Command,
		URL:       match.URL,
		Headers:   match.Headers,
		Env:       env,
		Agents:    matchedAgents,
	}
	res, err := state.app.AddMcpServer(cmd.Context(), s)
	if err != nil {
		return err
	}
	for _, e := range res.Errors {
		fmt.Fprintf(cmdOut(cmd), "  ! %s/%s: %v\n", e.AgentID, e.ServerName, e.Err)
	}
	printSkippedUnavailable(cmd, res.SkippedUnavailable)
	fmt.Fprintf(cmdOut(cmd), "imported %s\n", s.Name)
	return agentErrsFailure(len(res.Errors))
}

func newAgentsPluginsCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugins",
		Short: "Manage agent plugins and their marketplaces",
	}
	sync := newAgentsPluginsSyncCmd(state)
	cmd.AddCommand(
		newAgentsPluginsListCmd(state),
		newAgentsPluginsAddCmd(state),
		newAgentsPluginsRemoveCmd(state),
		sync,
		deprecatedAlias(sync, "restore", nil),
		newAgentsPluginsImportCmd(state),
		newAgentsPluginsResolveCmd(state),
		newAgentsPluginsGroupCmd(state),
		newAgentsPluginsMarketplaceCmd(state),
	)
	return cmd
}

func newAgentsPluginsResolveCmd(state *rootState) *cobra.Command {
	var useManaged bool
	var useLocal bool
	var agents []string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "resolve <name>",
		Short: "Settle a plugin whose live install came from another marketplace",
		Long: "Settle a plugin the agent installed from a marketplace other than the one the " +
			"manifest declares. --use-managed uninstalls that copy and reinstalls from the " +
			"declared marketplace. --use-local repoints the manifest entry at the marketplace " +
			"actually in use, which must already be declared.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if useManaged == useLocal {
				return fmt.Errorf("choose exactly one of --use-managed or --use-local")
			}
			opts := app.ResolvePluginDriftOptions{
				Name:     args[0],
				Agents:   agents,
				Strategy: app.PluginDriftUseManaged,
				DryRun:   dryRun,
			}
			if useLocal {
				opts.Strategy = app.PluginDriftUseLocal
			}
			if useManaged && !dryRun {
				ok, err := confirmAction(cmd, state, fmt.Sprintf(
					"Uninstall the foreign copy of %q and reinstall it from the declared marketplace?", args[0]))
				if err != nil || !ok {
					return err
				}
			}
			res, err := state.app.ResolvePluginDrift(cmd.Context(), opts)
			printDriftResolution(cmdOut(cmd), res.Actions, res.Warnings, dryRun)
			return err
		},
	}
	cmd.Flags().BoolVar(&useManaged, "use-managed", false, "Reinstall from the manifest's marketplace")
	cmd.Flags().BoolVar(&useLocal, "use-local", false, "Repoint the manifest at the installed marketplace")
	cmd.Flags().StringArrayVar(&agents, "agent", nil, "Limit the resolution to this agent target (repeatable)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be resolved without making changes")
	return cmd
}

func newAgentsPluginsGroupCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "group <name> <group>...",
		Short: "Set a plugin's full group membership",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			groups := args[1:]
			if err := state.app.SetPluginGroups(cmd.Context(), name, groups); err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "Set %q groups to %s.\n", name, strings.Join(groups, ", "))
			return nil
		},
	}
}

func newAgentsPluginsListCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List managed and unmanaged plugins",
		RunE: func(cmd *cobra.Command, _ []string) error {
			managed, unmanaged, err := state.app.PluginRows(cmd.Context())
			if err != nil {
				return err
			}
			w := cmdOut(cmd)
			for _, row := range managed {
				agentIDs := make([]string, 0, len(row.PerAgentStatus))
				for id := range row.PerAgentStatus {
					agentIDs = append(agentIDs, id)
				}
				sort.Strings(agentIDs)
				agentParts := make([]string, 0, len(agentIDs))
				for _, id := range agentIDs {
					marker := "✓"
					switch row.PerAgentStatus[id] {
					case app.PluginStatusInstalled:
					case app.PluginStatusDrifted:
						marker = "drift"
					default:
						marker = "-"
					}
					agentParts = append(agentParts, fmt.Sprintf("%s(%s)", id, marker))
				}
				origin := row.Marketplace
				if origin == "" {
					origin = row.Source
				}
				line := row.Name + "  " + origin
				if row.Version != "" {
					line += "  " + row.Version
					if row.Outdated() {
						line += " → " + row.LatestVersion
					}
				}
				if len(agentParts) > 0 {
					line += "  " + strings.Join(agentParts, " ")
				}
				fmt.Fprintln(w, line)
			}
			agentIDs := make([]string, 0, len(unmanaged))
			for id := range unmanaged {
				agentIDs = append(agentIDs, id)
			}
			sort.Strings(agentIDs)
			for _, id := range agentIDs {
				fmt.Fprintf(w, "\n-- unmanaged (%s) --\n", id)
				for _, p := range unmanaged[id] {
					line := p.Name + "  " + p.Marketplace
					if p.Version != "" {
						line += "  " + p.Version
					}
					fmt.Fprintln(w, line)
				}
			}
			return nil
		},
	}
}

func newAgentsPluginsAddCmd(state *rootState) *cobra.Command {
	var (
		name        string
		marketplace string
		source      string
		agents      []string
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Declare a plugin in the manifest and install it here",
		Long: "Record a plugin and its marketplace or direct source as manifest intent and install it through the " +
			"targeted agents' own CLIs — declaration and one host's convergence in a single step. " +
			"Other hosts pick it up on their next sync.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			name = strings.TrimSpace(name)
			marketplace = strings.TrimSpace(marketplace)
			source = strings.TrimSpace(source)
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			if (marketplace == "") == (source == "") {
				return fmt.Errorf("exactly one of --marketplace or --source is required")
			}
			p := config.Plugin{Name: name, Marketplace: marketplace, Source: source, Agents: agents}
			res, err := state.app.AddPlugin(cmd.Context(), p)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "added %s\n", name)
			for _, e := range res.Errors {
				fmt.Fprintf(cmdOut(cmd), "  ! %s/%s: %v\n", e.AgentID, e.Name, e.Err)
			}
			printSkippedUnavailable(cmd, res.SkippedUnavailable)
			return agentErrsFailure(len(res.Errors))
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "plugin name (required)")
	cmd.Flags().StringVar(&marketplace, "marketplace", "", "declared marketplace name")
	cmd.Flags().StringVar(&source, "source", "", "direct plugin source, owner/repo or Git URL")
	cmd.Flags().StringArrayVar(&agents, "agents", nil, "target agent IDs (repeatable)")
	return cmd
}

func newAgentsPluginsRemoveCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Undeclare a plugin and uninstall it from targeted agents",
		Long: "Drop a plugin from the manifest and uninstall it through the agent CLIs that hold it. " +
			"Unlike `agents skills remove`, the live side always goes with it — the agent owns the " +
			"installed copy, so there is no --purge. Adopting an installed plugin instead of " +
			"dropping it runs the other way — see import.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := state.app.RemovePlugin(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "removed %s\n", args[0])
			for _, e := range res.Errors {
				fmt.Fprintf(cmdOut(cmd), "  ! %s/%s: %v\n", e.AgentID, e.Name, e.Err)
			}
			for _, w := range res.Warnings {
				fmt.Fprintf(cmdOut(cmd), "  ~ %s/%s: %v\n", w.AgentID, w.Name, w.Err)
			}
			printSkippedUnavailable(cmd, res.SkippedUnavailable)
			return agentErrsFailure(len(res.Errors))
		},
	}
}

func newAgentsPluginsSyncCmd(state *rootState) *cobra.Command {
	var opts app.RestorePluginOptions
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Install the manifest plugin set onto this host",
		Long: "Move the manifest's plugins onto this host through each agent's own CLI, installing " +
			"the missing ones from their declared marketplace or direct source and leaving matching ones alone. " +
			"Adopting plugins the manifest does not declare runs the other way — see import.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, err := state.app.RestorePlugins(cmd.Context(), opts)
			if err != nil {
				return err
			}
			w := cmdOut(cmd)
			for _, msg := range res.Warnings {
				fmt.Fprintf(w, "warn: %s\n", msg)
			}
			if opts.DryRun {
				for _, p := range res.WouldInstall {
					fmt.Fprintf(w, "would install: %s\n", p)
				}
				return nil
			}
			for _, p := range res.Installed {
				fmt.Fprintf(w, "installed: %s\n", p)
			}
			for _, p := range res.AlreadyInstalled {
				fmt.Fprintf(w, "already installed: %s\n", p)
			}
			for _, d := range res.Drift {
				fmt.Fprintf(w, "  ~ drift: %s\n", d)
			}
			for _, e := range res.Errors {
				fmt.Fprintf(w, "  ! %s/%s: %v\n", e.AgentID, e.Name, e.Err)
			}
			return agentErrsFailure(len(res.Errors))
		},
	}
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "print what would be installed without running")
	return cmd
}

func newAgentsPluginsImportCmd(state *rootState) *cobra.Command {
	var source string
	cmd := &cobra.Command{
		Use:   "import [<name>]",
		Short: "Adopt a plugin this host already installed into the manifest",
		Long: "Move an installed plugin into the manifest. Without a name it lists what each agent " +
			"holds unmanaged; with one it adopts that plugin's name and marketplace as the " +
			"manifest's intent, declaring the marketplace too when the agent reports a real source. " +
			"Installing what the manifest declares runs the other way — see sync.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			diff, err := state.app.ImportPlugins(cmd.Context())
			if err != nil {
				return err
			}
			if len(args) == 1 {
				return importPluginByName(cmd, state, diff, args[0], strings.TrimSpace(source))
			}
			if source != "" {
				return fmt.Errorf("--source requires a plugin name")
			}
			w := cmdOut(cmd)
			agentIDs := make([]string, 0, len(diff.Unmanaged))
			for id := range diff.Unmanaged {
				agentIDs = append(agentIDs, id)
			}
			sort.Strings(agentIDs)
			for _, id := range agentIDs {
				fmt.Fprintf(w, "-- unmanaged (%s) --\n", id)
				for _, p := range diff.Unmanaged[id] {
					fmt.Fprintf(w, "%s\n", p.Name)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&source, "source", "", "direct install source when the agent cannot report one")
	return cmd
}

// An undeclared marketplace is adopted only with a real reported source: a fabricated placeholder would be replayed as a real `marketplace add` on restore.
func importPluginByName(cmd *cobra.Command, state *rootState, diff app.PluginImportDiff, name, directSource string) error {
	agentIDs := make([]string, 0, len(diff.Unmanaged))
	for id := range diff.Unmanaged {
		agentIDs = append(agentIDs, id)
	}
	sort.Strings(agentIDs)

	var match app.InstalledPlugin
	found := false
	var matchedAgents []string
	for _, id := range agentIDs {
		for _, p := range diff.Unmanaged[id] {
			if p.Name != name {
				continue
			}
			if !found {
				match = p
				found = true
			} else if p.Marketplace != match.Marketplace {
				return fmt.Errorf("plugin %q is unmanaged under multiple agents with conflicting marketplaces; import each manually", name)
			}
			matchedAgents = append(matchedAgents, id)
		}
	}
	if !found {
		return fmt.Errorf("plugin %q is not unmanaged in any agent CLI", name)
	}
	if match.Marketplace == "" {
		if directSource == "" {
			return fmt.Errorf("plugin %q has no discoverable install source; pass --source <owner/repo-or-url>", name)
		}
		p := config.Plugin{Name: match.Name, Source: directSource, Agents: matchedAgents}
		res, err := state.app.AddPlugin(cmd.Context(), p)
		if err != nil {
			return err
		}
		for _, e := range res.Errors {
			fmt.Fprintf(cmdOut(cmd), "  ! %s/%s: %v\n", e.AgentID, e.Name, e.Err)
		}
		printSkippedUnavailable(cmd, res.SkippedUnavailable)
		fmt.Fprintf(cmdOut(cmd), "imported %s\n", p.Name)
		return agentErrsFailure(len(res.Errors))
	}

	failed := 0
	marketplaces, err := state.app.Marketplaces()
	if err != nil {
		return err
	}
	declared := false
	for _, m := range marketplaces {
		if m.Name == match.Marketplace {
			declared = true
			break
		}
	}
	if !declared {
		source, ok, err := state.app.FindUndeclaredMarketplace(cmd.Context(), match.Marketplace, matchedAgents)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("plugin %q references undeclared marketplace %q with no discoverable source; declare it first: omni agents plugins marketplace add %s --source <src>", name, match.Marketplace, match.Marketplace)
		}
		mres, err := state.app.AddMarketplace(cmd.Context(), config.Marketplace{Name: match.Marketplace, Source: source, Agents: matchedAgents})
		if err != nil {
			return err
		}
		for _, e := range mres.Errors {
			fmt.Fprintf(cmdOut(cmd), "  ! %s/%s: %v\n", e.AgentID, e.Name, e.Err)
		}
		printSkippedUnavailable(cmd, mres.SkippedUnavailable)
		failed += len(mres.Errors)
	}

	p := config.Plugin{Name: match.Name, Marketplace: match.Marketplace, Agents: matchedAgents}
	res, err := state.app.AddPlugin(cmd.Context(), p)
	if err != nil {
		return err
	}
	for _, e := range res.Errors {
		fmt.Fprintf(cmdOut(cmd), "  ! %s/%s: %v\n", e.AgentID, e.Name, e.Err)
	}
	printSkippedUnavailable(cmd, res.SkippedUnavailable)
	fmt.Fprintf(cmdOut(cmd), "imported %s\n", p.Name)
	return agentErrsFailure(failed + len(res.Errors))
}

func newAgentsPluginsMarketplaceCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "marketplace",
		Short: "Manage plugin marketplaces in the agent manifest",
	}
	cmd.AddCommand(
		newAgentsPluginsMarketplaceListCmd(state),
		newAgentsPluginsMarketplaceAddCmd(state),
		newAgentsPluginsMarketplaceRemoveCmd(state),
		newAgentsPluginsMarketplaceGroupCmd(state),
	)
	return cmd
}

func newAgentsPluginsMarketplaceGroupCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "group <name> <group>...",
		Short: "Set a marketplace's full group membership",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			groups := args[1:]
			if err := state.app.SetMarketplaceGroups(cmd.Context(), name, groups); err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "Set %q groups to %s.\n", name, strings.Join(groups, ", "))
			return nil
		},
	}
}

func newAgentsPluginsMarketplaceListCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List declared marketplaces",
		RunE: func(cmd *cobra.Command, _ []string) error {
			marketplaces, err := state.app.Marketplaces()
			if err != nil {
				return err
			}
			w := cmdOut(cmd)
			for _, m := range marketplaces {
				fmt.Fprintf(w, "%s  %s\n", m.Name, m.Source)
			}
			return nil
		},
	}
}

func newAgentsPluginsMarketplaceAddCmd(state *rootState) *cobra.Command {
	var (
		source string
		agents []string
	)
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Declare a marketplace in the manifest and add it to targeted agents",
		Long: "Record a plugin marketplace as manifest intent and add it through the targeted " +
			"agents' own CLIs, so the plugins declared against it have somewhere to install from.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if source == "" {
				return fmt.Errorf("--source is required")
			}
			m := config.Marketplace{Name: args[0], Source: source, Agents: agents}
			res, err := state.app.AddMarketplace(cmd.Context(), m)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "added %s\n", args[0])
			for _, e := range res.Errors {
				fmt.Fprintf(cmdOut(cmd), "  ! %s/%s: %v\n", e.AgentID, e.Name, e.Err)
			}
			printSkippedUnavailable(cmd, res.SkippedUnavailable)
			return agentErrsFailure(len(res.Errors))
		},
	}
	cmd.Flags().StringVar(&source, "source", "", "marketplace source, owner/repo or URL (required)")
	cmd.Flags().StringArrayVar(&agents, "agents", nil, "target agent IDs (repeatable)")
	return cmd
}

func newAgentsPluginsMarketplaceRemoveCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Undeclare a marketplace from the manifest",
		Long: "Drop a marketplace from the manifest. The agents that already added it keep it, " +
			"because removing a marketplace an agent still installs plugins from would break them; " +
			"remove it with the agent's own CLI when that is what you want.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := state.app.RemoveMarketplace(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "removed %s\n", args[0])
			return nil
		},
	}
}

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/dots"
	"github.com/lkshrk/omni/internal/executor"
)

func newDotsCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dots",
		Short: "Manage dotfile symlinks from a git repo",
		Long: `dots manages config symlinks declared in your settings.json.
Source files live in the dotfiles/ stow subtree inside the configured git repo;
symlinks are created at the target paths. Mutating commands (sync, add, resolve,
delete) require GNU Stow (stow) on PATH.

Set the repo path via 'omni ui' (Dots tab) or settings.dots_repo in settings.json.`,
	}
	cmd.AddCommand(
		newDotsSyncCmd(state),
		newDotsDiscoverCmd(state),
		newDotsAddCmd(state),
		newDotsGroupsCmd(state),
		newDotsVariantCmd(state),
		newDotsDeleteCmd(state),
		newDotsResolveCmd(state),
		newDotsIgnoreCmd(state),
		newDotsUnignoreCmd(state),
		newDotsListCmd(state),
		newDotsStatusCmd(state),
		newDotsHistoryCmd(state),
		newDotsEnableCmd(state),
		newDotsDisableCmd(state),
		newDotsPullCmd(state),
		newDotsCommitCmd(state),
		newDotsPushCmd(state),
		newDotsReminderCmd(state),
		newDotsWatchCmd(state),
		newDotsServicesCmd(state),
	)
	return cmd
}

func newDotsHistoryCmd(state *rootState) *cobra.Command {
	var limit int
	var format string
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Show recent dotfile operation history",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateFormat(format, "table", "json"); err != nil {
				return err
			}
			if limit < 0 {
				return fmt.Errorf("limit must be >= 0")
			}
			entries, err := state.app.RecentDotsHistory(cmd.Context(), limit)
			if err != nil {
				return err
			}
			if format == "json" {
				if entries == nil {
					entries = []app.DotsHistoryEntry{}
				}
				return json.NewEncoder(cmd.OutOrStdout()).Encode(entries)
			}
			if len(entries) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No dots history yet.")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%-19s  %-16s  %-10s  %-18s  %s\n", "TIME", "OPERATION", "STATUS", "ENTRY", "SUMMARY")
			for _, entry := range entries {
				name := entry.Entry
				if strings.TrimSpace(name) == "" {
					name = "-"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%-19s  %-16s  %-10s  %-18s  %s\n",
					entry.Time.Local().Format("2006-01-02 15:04:05"),
					entry.Operation,
					entry.Status,
					name,
					entry.Summary,
				)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum history entries to show")
	cmd.Flags().StringVar(&format, "format", "table", "Output format (table, json)")
	return cmd
}

func ensureDotsStowForCLI(cmd *cobra.Command, state *rootState) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	if state == nil || state.app == nil || state.app.DotsStowInstalled(ctx) {
		return nil
	}
	installMessage := "GNU Stow (stow) is required for dotfile sync. Install stow with your system package manager, then rerun this command."
	if !stdinIsTerminal() {
		return fmt.Errorf("%s", installMessage)
	}
	ok, err := confirmAction(cmd, state, "GNU Stow (stow) is required for dotfile sync. Install stow with the system package manager now?")
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s", installMessage)
	}
	if err := state.app.InstallDotsStow(ctx); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Stow installed.")
	return nil
}

// ─── dots sync ────────────────────────────────────────────────────────────────

func newDotsSyncCmd(state *rootState) *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "sync [name]",
		Short: "Create or repair dotfile symlinks",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireDotsConfigured(state); err != nil {
				return err
			}
			if err := ensureDotsStowForCLI(cmd, state); err != nil {
				return err
			}
			opts := dots.SyncOptions{DryRun: dryRun}
			var (
				ops []dots.Op
				err error
			)
			if len(args) > 0 {
				ops, err = state.app.DotsSyncEntry(cmd.Context(), args[0], opts)
			} else {
				ops, err = state.app.DotsSync(opts)
			}
			printDotOps(cmd, ops, dryRun) // print before returning err so partial results are visible
			return err
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be done without making changes")
	return cmd
}

// ─── dots discover ────────────────────────────────────────────────────────────

func newDotsDiscoverCmd(state *rootState) *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "discover",
		Short: "List untracked dotfile candidates",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireDotsConfigured(state); err != nil {
				return err
			}
			entries, err := state.app.DiscoverUntrackedDotsEntries()
			if err != nil {
				return err
			}
			if format == "json" {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(entries)
			}
			if err := validateFormat(format, "table", "json"); err != nil {
				return err
			}
			if len(entries) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No untracked dotfile candidates found.")
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Discovered untracked dotfile candidates:")
			for _, entry := range entries {
				fmt.Fprintf(cmd.OutOrStdout(), "  %-20s -> %s\n", entry.Name, entry.Path)
			}
			return nil
		},
	}
	addFormatFlag(cmd, &format, "table", "table", "json")
	return cmd
}

// ─── dots add ─────────────────────────────────────────────────────────────────

func newDotsAddCmd(state *rootState) *cobra.Command {
	var name string
	var group string
	var adopt bool
	var ignore []string
	var discovered bool

	cmd := &cobra.Command{
		Use:   "add <path>",
		Short: "Add a config path to dots management",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireDotsConfigured(state); err != nil {
				return err
			}
			if discovered {
				if adopt || name != "" || len(ignore) > 0 {
					return fmt.Errorf("--discovered cannot be combined with --adopt, --name, or --ignore")
				}
				added, err := state.app.DotsAddDiscoveredEntry(args[0], group)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "added discovered dots entry %q -> %s\n", added.Name, added.Path)
				return nil
			}
			if err := ensureDotsStowForCLI(cmd, state); err != nil {
				return err
			}
			ops, err := state.app.DotsAdd(cmd.Context(), args[0], app.DotsAddOptions{
				Name:   name,
				Group:  group,
				Adopt:  adopt,
				Ignore: ignore,
			})
			if err != nil {
				return err
			}
			printDotOps(cmd, ops, false)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Override the inferred entry name")
	cmd.Flags().StringVar(&group, "group", "", "Group to add the entry to (default: current host)")
	cmd.Flags().BoolVar(&adopt, "adopt", false, "Move the existing path into the dots repo")
	cmd.Flags().StringSliceVar(&ignore, "ignore", nil, "Patterns to ignore within this entry")
	cmd.Flags().BoolVar(&discovered, "discovered", false, "Add a discovered candidate to config without adopting files")
	return cmd
}

// ─── dots variant ────────────────────────────────────────────────────────────

func newDotsVariantCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "variant",
		Short: "Manage host-specific dots package variants",
	}
	cmd.AddCommand(
		newDotsVariantListCmd(state),
		newDotsVariantAddCmd(state),
		newDotsVariantRemoveCmd(state),
	)
	return cmd
}

func newDotsVariantListCmd(state *rootState) *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "list <name>",
		Short: "List package variants for a dots entry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireDotsConfigured(state); err != nil {
				return err
			}
			variants, err := state.app.DotsListVariants(args[0])
			if err != nil {
				return err
			}
			if format == "json" {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(variants)
			}
			if err := validateFormat(format, "table", "json"); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%-18s  %-24s  %s\n", "HOST", "PACKAGE", "ACTIVE")
			fmt.Fprintln(cmd.OutOrStdout(), strings.Repeat("─", 54))
			for _, variant := range variants {
				host := variant.Host
				if variant.Default {
					host = "(default)"
				}
				active := ""
				if variant.Active {
					active = "yes"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%-18s  %-24s  %s\n", host, variant.Package, active)
			}
			return nil
		},
	}
	addFormatFlag(cmd, &format, "table", "table", "json")
	return cmd
}

func newDotsVariantAddCmd(state *rootState) *cobra.Command {
	var host string
	var pkgName string
	var sync bool
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a host-specific package variant for a dots entry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireDotsConfigured(state); err != nil {
				return err
			}
			if sync {
				if err := ensureDotsStowForCLI(cmd, state); err != nil {
					return err
				}
			}
			info, ops, err := state.app.DotsAddHostVariant(cmd.Context(), args[0], app.DotsAddVariantOptions{
				Host:    host,
				Package: pkgName,
				Sync:    sync,
			})
			if sync || len(ops) > 0 {
				printDotOps(cmd, ops, false)
			}
			if err != nil {
				return err
			}
			if info.Host != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "added dots variant %q for host %q using package %q\n", info.Name, info.Host, info.Package)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "Host for the variant (default: current host)")
	cmd.Flags().StringVar(&pkgName, "package", "", "Stow package directory (default: <name>@<host>)")
	cmd.Flags().BoolVar(&sync, "sync", false, "Sync immediately when the variant belongs to this host")
	return cmd
}

func newDotsVariantRemoveCmd(state *rootState) *cobra.Command {
	var host string
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a host-specific package variant",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireDotsConfigured(state); err != nil {
				return err
			}
			ok, err := confirmAction(cmd, state, fmt.Sprintf("Remove dots variant %q and its unused repo package?", args[0]))
			if err != nil || !ok {
				return err
			}
			info, err := state.app.DotsRemoveHostVariant(cmd.Context(), args[0], app.DotsRemoveVariantOptions{Host: host})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed dots variant %q for host %q using package %q\n", info.Name, info.Host, info.Package)
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "Host for the variant (default: current host)")
	return cmd
}

// ─── dots groups ──────────────────────────────────────────────────────────────

func newDotsGroupsCmd(state *rootState) *cobra.Command {
	var moveGroup string
	var removeGroups []string

	cmd := &cobra.Command{
		Use:   "groups <name>",
		Short: "Show or move a dots entry's group assignment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireDotsConfigured(state); err != nil {
				return err
			}
			name := args[0]
			moveGroup = strings.TrimSpace(moveGroup)
			removeGroups = normalizeDotsGroupArgs(removeGroups)
			moveChanged := cmd.Flags().Changed("move")
			removeChanged := cmd.Flags().Changed("remove")
			if moveChanged && removeChanged {
				return fmt.Errorf("--move cannot be combined with --remove")
			}
			if moveChanged && moveGroup == "" {
				return fmt.Errorf("--move requires a group")
			}
			if removeChanged && len(removeGroups) == 0 {
				return fmt.Errorf("--remove requires at least one group")
			}

			memberships, err := state.app.DotMembershipMap(cmd.Context())
			if err != nil {
				return err
			}
			current, ok := memberships[name]
			if !ok || len(current) == 0 {
				return fmt.Errorf("dots entry %q not found", name)
			}
			if !moveChanged && !removeChanged {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", name, strings.Join(current, ", "))
				return nil
			}

			var target []string
			if moveChanged {
				target = []string{moveGroup}
			} else {
				target = applyDotsGroupDelta(current, removeGroups)
			}
			if len(target) == 0 {
				return fmt.Errorf("dots entry %q needs at least one group; use dots delete to remove it from management", name)
			}
			if err := updateDotsGroups(state, name, current, target); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "moved %s to group %s\n", name, strings.Join(target, ", "))
			return nil
		},
	}
	cmd.Flags().StringVar(&moveGroup, "move", "", "Move this dots entry to a group")
	cmd.Flags().StringSliceVar(&removeGroups, "remove", nil, "Remove this dots entry from a group")
	return cmd
}

func normalizeDotsGroupArgs(groups []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(groups))
	for _, group := range groups {
		group = strings.TrimSpace(group)
		if group == "" {
			continue
		}
		if seen[group] {
			continue
		}
		seen[group] = true
		out = append(out, group)
	}
	sort.Strings(out)
	return out
}

func applyDotsGroupDelta(current, removeGroups []string) []string {
	set := map[string]bool{}
	for _, group := range current {
		set[group] = true
	}
	for _, group := range removeGroups {
		delete(set, group)
	}
	out := make([]string, 0, len(set))
	for group := range set {
		out = append(out, group)
	}
	sort.Strings(out)
	return out
}

func updateDotsGroups(state *rootState, name string, current, target []string) error {
	if len(target) == 1 {
		currentSet := dotsGroupSet(current)
		if len(currentSet) == 1 && currentSet[target[0]] {
			return nil
		}
		return state.app.MoveDotToGroup(name, target[0])
	}
	currentSet := dotsGroupSet(current)
	targetSet := dotsGroupSet(target)
	for group := range targetSet {
		if !currentSet[group] {
			if err := state.app.MoveDotToGroup(name, group); err != nil {
				return err
			}
		}
	}
	for group := range currentSet {
		if !targetSet[group] {
			if err := state.app.RemoveDotFromGroup(name, group); err != nil {
				return err
			}
		}
	}
	return nil
}

func dotsGroupSet(groups []string) map[string]bool {
	set := make(map[string]bool, len(groups))
	for _, group := range groups {
		set[group] = true
	}
	return set
}

// ─── dots delete ──────────────────────────────────────────────────────────────

func newDotsDeleteCmd(state *rootState) *cobra.Command {
	var keepLocal bool
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a dots entry from management",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireDotsConfigured(state); err != nil {
				return err
			}
			ok, err := confirmAction(cmd, state, fmt.Sprintf("Delete dots entry %q and repo files? keep local: %t", args[0], keepLocal))
			if err != nil || !ok {
				return err
			}
			if err := ensureDotsStowForCLI(cmd, state); err != nil {
				return err
			}
			if err := state.app.DotsDeleteWithOptions(cmd.Context(), args[0], app.DotsDeleteOptions{KeepLocal: keepLocal}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "deleted dots entry %q\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&keepLocal, "keep-local", true, "Keep local files after deleting the repo files")
	return cmd
}

// ─── dots resolve ─────────────────────────────────────────────────────────────

func newDotsResolveCmd(state *rootState) *cobra.Command {
	var useRepo bool
	var useLocal bool
	cmd := &cobra.Command{
		Use:   "resolve <name>",
		Short: "Resolve a dots conflict with an explicit side",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireDotsConfigured(state); err != nil {
				return err
			}
			if useRepo == useLocal {
				return fmt.Errorf("choose exactly one of --use-repo or --use-local")
			}
			strategy := app.DotResolveUseRepo
			prompt := fmt.Sprintf("Replace local %q with the repo version?", args[0])
			if useLocal {
				strategy = app.DotResolveUseLocal
				prompt = fmt.Sprintf("Replace repo version for %q with the local version?", args[0])
			}
			ok, err := confirmAction(cmd, state, prompt)
			if err != nil || !ok {
				return err
			}
			if err := ensureDotsStowForCLI(cmd, state); err != nil {
				return err
			}
			ops, err := state.app.DotsResolveConflict(cmd.Context(), args[0], strategy)
			printDotOps(cmd, ops, false)
			return err
		},
	}
	cmd.Flags().BoolVar(&useRepo, "use-repo", false, "Keep the repo version and replace the local target")
	cmd.Flags().BoolVar(&useLocal, "use-local", false, "Copy the local target into the repo and relink it")
	return cmd
}

// ─── dots ignore ─────────────────────────────────────────────────────────────

func newDotsIgnoreCmd(state *rootState) *cobra.Command {
	var entry bool
	var path string
	cmd := &cobra.Command{
		Use:   "ignore <name> [pattern]",
		Short: "Ignore a dots entry or a path pattern within it",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireDotsConfigured(state); err != nil {
				return err
			}
			if entry || len(args) == 1 {
				if err := state.app.DotsSetEntryIgnored(args[0], path, true); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "ignored dots entry %q\n", args[0])
				return nil
			}
			if err := state.app.DotsAddIgnorePattern(args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "added ignore pattern %q to %q\n", args[1], args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&entry, "entry", false, "Ignore the whole dots entry instead of adding a child pattern")
	cmd.Flags().StringVar(&path, "path", "", "Path for a new ignored discovery candidate")
	return cmd
}

func newDotsUnignoreCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "unignore <name> [pattern]",
		Short: "Include a dots entry or remove one of its ignore patterns",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireDotsConfigured(state); err != nil {
				return err
			}
			if len(args) == 1 {
				if err := state.app.DotsSetEntryIgnored(args[0], "", false); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "included dots entry %q\n", args[0])
				return nil
			}
			if err := state.app.DotsRemoveIgnorePattern(args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed ignore pattern %q from %q\n", args[1], args[0])
			return nil
		},
	}
}

// ─── dots list ────────────────────────────────────────────────────────────────

func newDotsListCmd(state *rootState) *cobra.Command {
	var stateFilter string
	var format string
	cmd := &cobra.Command{
		Use:   "list [name]",
		Short: "List managed dots entries and their symlink health",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireDotsConfigured(state); err != nil {
				return err
			}
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			statuses, err := state.app.QueryDots(app.DotsQueryOptions{Name: name, State: stateFilter})
			if err != nil {
				return err
			}
			if format == "json" {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(statuses)
			}
			if err := validateFormat(format, "table", "json"); err != nil {
				return err
			}
			if len(statuses) == 0 && len(args) == 0 && stateFilter == "" {
				fmt.Fprintln(cmd.OutOrStdout(), "No dots entries configured.")
				fmt.Fprintln(cmd.OutOrStdout(), "Add one with: omni dots add <path>")
				return nil
			}
			if len(statuses) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No dots entries match filters.")
				return nil
			}
			printDotsTable(cmd, statuses)
			return nil
		},
	}
	cmd.Flags().StringVar(&stateFilter, "state", "", "filter by state (synced, missing, broken, conflict, local-only, repo-only, no-source, ignored)")
	addFormatFlag(cmd, &format, "table", "table", "json")
	return cmd
}

// ─── dots status ──────────────────────────────────────────────────────────────

func newDotsStatusCmd(state *rootState) *cobra.Command {
	var stateFilter string
	var format string
	cmd := &cobra.Command{
		Use:   "status [name]",
		Short: "Show dots symlink health and git repo status",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireDotsConfigured(state); err != nil {
				return err
			}
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			result, err := state.app.QueryDotsStatus(cmd.Context(), app.DotsQueryOptions{Name: name, State: stateFilter})
			if err != nil {
				return err
			}
			if format == "json" {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}
			if err := validateFormat(format, "table", "json"); err != nil {
				return err
			}
			printDotsTable(cmd, result.Entries)
			if result.GitStatus != "" {
				fmt.Fprintln(cmd.OutOrStdout())
				fmt.Fprintln(cmd.OutOrStdout(), "Git status:")
				fmt.Fprintln(cmd.OutOrStdout(), result.GitStatus)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "\nGit: working tree clean")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&stateFilter, "state", "", "filter by state (synced, missing, broken, conflict, local-only, repo-only, no-source, ignored)")
	addFormatFlag(cmd, &format, "table", "table", "json")
	return cmd
}

// ─── dots enable / disable ───────────────────────────────────────────────────

func newDotsEnableCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "enable",
		Short: "Enable dotfile sync for this host",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := ensureDotsStowForCLI(cmd, state); err != nil {
				return err
			}
			ops, err := state.app.EnableDotsForHost(cmd.Context())
			printDotOps(cmd, ops, false)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Dots enabled.")
			return nil
		},
	}
}

func newDotsDisableCmd(state *rootState) *cobra.Command {
	var overwrite bool
	var removeLocal bool

	cmd := &cobra.Command{
		Use:   "disable",
		Short: "Disable dotfile sync for this host",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if overwrite && removeLocal {
				return fmt.Errorf("--overwrite and --remove-local cannot be combined")
			}
			ok, err := confirmAction(cmd, state, "Disable dotfile sync for this host?")
			if err != nil || !ok {
				return err
			}
			ops, err := state.app.DisableDotsForHost(cmd.Context(), app.DisableDotsOptions{
				ConflictOverwrite: overwrite,
				KeepExistingLocal: !removeLocal && !overwrite,
				RemoveLocal:       removeLocal,
			})
			printDotOps(cmd, ops, false)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Dots disabled.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "Overwrite conflicting local files with repo copies when disabling")
	cmd.Flags().BoolVar(&removeLocal, "remove-local", false, "Remove local dotfile targets instead of keeping local copies")
	return cmd
}

// ─── dots pull ────────────────────────────────────────────────────────────────

func newDotsPullCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "pull",
		Short: "Pull latest changes from remote and re-sync symlinks",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireDotsConfigured(state); err != nil {
				return err
			}
			if err := ensureDotsStowForCLI(cmd, state); err != nil {
				return err
			}
			ops, err := state.app.DotsPull(cmd.Context())
			fmt.Fprintln(cmd.OutOrStdout(), "Pulled. Re-syncing symlinks...")
			printDotOps(cmd, ops, false) // print before returning err so partial results are visible
			return err
		},
	}
}

// ─── dots push ────────────────────────────────────────────────────────────────

func newDotsPushCmd(state *rootState) *cobra.Command {
	var message string

	cmd := &cobra.Command{
		Use:   "push",
		Short: "Stage, commit, and push all changes in the dots repo",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireDotsConfigured(state); err != nil {
				return err
			}
			if err := state.app.DotsPush(cmd.Context(), message); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Pushed.")
			return nil
		},
	}
	cmd.Flags().StringVarP(&message, "message", "m", "", "Commit message (default: \"dots: update\")")
	return cmd
}

func newDotsCommitCmd(state *rootState) *cobra.Command {
	var message string

	cmd := &cobra.Command{
		Use:   "commit",
		Short: "Stage and commit all changes in the dots repo",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireDotsConfigured(state); err != nil {
				return err
			}
			if err := state.app.DotsCommit(cmd.Context(), message); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Committed.")
			return nil
		},
	}
	cmd.Flags().StringVarP(&message, "message", "m", "", "Commit message (default: \"dots: update\")")
	return cmd
}

// ─── dots reminder ───────────────────────────────────────────────────────────

func newDotsReminderCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reminder",
		Short: "Check or install periodic dotfile sync reminders",
	}
	cmd.AddCommand(
		newDotsReminderCheckCmd(state),
		newDotsReminderRunCmd(state),
		newDotsReminderInstallCmd(state),
		newDotsReminderUninstallCmd(state),
		newDotsReminderStatusCmd(state),
	)
	return cmd
}

func newDotsReminderCheckCmd(state *rootState) *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Check whether dotfiles need attention",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireDotsConfigured(state); err != nil {
				return err
			}
			result, err := state.app.DotsReminder(cmd.Context())
			if err != nil {
				return err
			}
			return printDotsReminderResult(cmd, result, format)
		},
	}
	addFormatFlag(cmd, &format, "text", "text", "json")
	return cmd
}

func newDotsReminderRunCmd(state *rootState) *cobra.Command {
	var notify bool
	var format string
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run one reminder check, optionally sending a desktop notification",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireDotsConfigured(state); err != nil {
				return err
			}
			result, err := state.app.DotsReminder(cmd.Context())
			if err != nil {
				return err
			}
			if notify && result.NeedsReminder {
				if err := notifyDotsReminder(cmd.Context(), result); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: notification: %v\n", err)
				}
			}
			return printDotsReminderResult(cmd, result, format)
		},
	}
	cmd.Flags().BoolVar(&notify, "notify", false, "Send a desktop notification when dotfiles need attention")
	addFormatFlag(cmd, &format, "text", "text", "json")
	return cmd
}

func newDotsReminderInstallCmd(state *rootState) *cobra.Command {
	interval := app.DefaultDotsReminderInterval()
	notify := true
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install a periodic user service for dotfile reminders",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireDotsConfigured(state); err != nil {
				return err
			}
			info, err := state.app.InstallDotsReminderService(cmd.Context(), app.DotsReminderInstallOptions{
				Interval: interval,
				Notify:   notify,
				Activate: true,
			})
			if err != nil {
				return err
			}
			printDotsReminderService(cmd, "Installed dots reminder service.", info)
			return nil
		},
	}
	cmd.Flags().DurationVar(&interval, "interval", interval, "Reminder interval, for example 12h or 24h")
	cmd.Flags().BoolVar(&notify, "notify", notify, "Send desktop notifications from the reminder service")
	return cmd
}

func newDotsReminderUninstallCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Uninstall the periodic dotfile reminder service",
		RunE: func(cmd *cobra.Command, _ []string) error {
			info, err := state.app.UninstallDotsReminderService(cmd.Context())
			if err != nil {
				return err
			}
			printDotsReminderService(cmd, "Uninstalled dots reminder service.", info)
			return nil
		},
	}
}

func newDotsReminderStatusCmd(state *rootState) *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show reminder service install status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			info, err := state.app.DotsReminderServiceStatus()
			if err != nil {
				return err
			}
			if format == "json" {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(info)
			}
			if err := validateFormat(format, "text", "json"); err != nil {
				return err
			}
			printDotsReminderService(cmd, dotsServiceStatusHeader("Dots reminder service", info.Installed, info.Platform), info)
			return nil
		},
	}
	addFormatFlag(cmd, &format, "text", "text", "json")
	return cmd
}

func printDotsReminderResult(cmd *cobra.Command, result *app.DotsReminderResult, format string) error {
	if format == "json" {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}
	if err := validateFormat(format, "text", "json"); err != nil {
		return err
	}
	if result.Disabled {
		fmt.Fprintln(cmd.OutOrStdout(), "Dots are disabled for this host.")
		return nil
	}
	if !result.NeedsReminder {
		fmt.Fprintln(cmd.OutOrStdout(), "Dotfiles are in sync.")
		return nil
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Dotfiles need attention:")
	for _, reason := range result.Reasons {
		fmt.Fprintf(cmd.OutOrStdout(), "  - %s\n", reason.Message)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "\nRun 'omni dots status' for details.")
	return nil
}

func printDotsReminderService(cmd *cobra.Command, header string, info *app.DotsReminderService) {
	fmt.Fprintln(cmd.OutOrStdout(), header)
	fmt.Fprintf(cmd.OutOrStdout(), "Platform: %s\n", info.Platform)
	fmt.Fprintf(cmd.OutOrStdout(), "Interval: %s\n", info.Interval)
	fmt.Fprintf(cmd.OutOrStdout(), "Notifications: %s\n", displaySettingsValue(info.Notify))
	for _, file := range info.Files {
		fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", file)
	}
}

func notifyDotsReminder(ctx context.Context, result *app.DotsReminderResult) error {
	if result == nil || !result.NeedsReminder {
		return nil
	}
	message := "Run omni dots status for details."
	if len(result.Reasons) > 0 {
		message = result.Reasons[0].Message
		if len(result.Reasons) > 1 {
			message += fmt.Sprintf(" (+%d more)", len(result.Reasons)-1)
		}
	}
	exec := executor.New()
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf(`display notification "%s" with title "Omni dotfiles"`, appleScriptString(message))
		_, stderr, err := exec.Run(ctx, "osascript", "-e", script)
		if err != nil {
			return fmt.Errorf("osascript: %w%s", err, stderrSuffix(stderr))
		}
	case "linux":
		_, stderr, err := exec.Run(ctx, "notify-send", "Omni dotfiles", message)
		if err != nil {
			return fmt.Errorf("notify-send: %w%s", err, stderrSuffix(stderr))
		}
	default:
		return fmt.Errorf("desktop notifications are not supported on %s", runtime.GOOS)
	}
	return nil
}

// ─── dots watch ──────────────────────────────────────────────────────────────

func newDotsWatchCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Watch dotfile paths and sync after changes",
	}
	cmd.AddCommand(
		newDotsWatchRunCmd(state),
		newDotsWatchInstallCmd(state),
		newDotsWatchUninstallCmd(state),
		newDotsWatchStatusCmd(state),
	)
	return cmd
}

func newDotsWatchRunCmd(state *rootState) *cobra.Command {
	debounce := app.DefaultDotsWatchDebounce()
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the dotfile watcher in the foreground",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireDotsConfigured(state); err != nil {
				return err
			}
			if err := ensureDotsStowForCLI(cmd, state); err != nil {
				return err
			}
			return state.app.DotsWatch(cmd.Context(), app.DotsWatchOptions{
				Debounce: debounce,
				OnStart: func(start app.DotsWatchStart) {
					fmt.Fprintf(cmd.OutOrStdout(), "Watching %d dotfile path(s).\n", start.WatchedPaths)
				},
				OnSync: func(result app.DotsWatchSyncResult) {
					printDotsWatchSyncResult(cmd, result)
				},
			})
		},
	}
	cmd.Flags().DurationVar(&debounce, "debounce", debounce, "Debounce delay after filesystem changes, for example 2s or 10s")
	return cmd
}

func newDotsWatchInstallCmd(state *rootState) *cobra.Command {
	debounce := app.DefaultDotsWatchDebounce()
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install a user service for automatic dotfile sync",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireDotsConfigured(state); err != nil {
				return err
			}
			if err := ensureDotsStowForCLI(cmd, state); err != nil {
				return err
			}
			info, err := state.app.InstallDotsWatchService(cmd.Context(), app.DotsWatchInstallOptions{
				Debounce: debounce,
				Activate: true,
			})
			if err != nil {
				return err
			}
			printDotsWatchService(cmd, "Installed dots watch service.", info)
			return nil
		},
	}
	cmd.Flags().DurationVar(&debounce, "debounce", debounce, "Debounce delay after filesystem changes, for example 2s or 10s")
	return cmd
}

func newDotsWatchUninstallCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Uninstall the automatic dotfile sync service",
		RunE: func(cmd *cobra.Command, _ []string) error {
			info, err := state.app.UninstallDotsWatchService(cmd.Context())
			if err != nil {
				return err
			}
			printDotsWatchService(cmd, "Uninstalled dots watch service.", info)
			return nil
		},
	}
}

func newDotsWatchStatusCmd(state *rootState) *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show watch service install status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			info, err := state.app.DotsWatchServiceStatus()
			if err != nil {
				return err
			}
			if format == "json" {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(info)
			}
			if err := validateFormat(format, "text", "json"); err != nil {
				return err
			}
			printDotsWatchService(cmd, dotsServiceStatusHeader("Dots watch service", info.Installed, info.Platform), info)
			return nil
		},
	}
	addFormatFlag(cmd, &format, "text", "text", "json")
	return cmd
}

func printDotsWatchService(cmd *cobra.Command, header string, info *app.DotsWatchService) {
	fmt.Fprintln(cmd.OutOrStdout(), header)
	fmt.Fprintf(cmd.OutOrStdout(), "Platform: %s\n", info.Platform)
	fmt.Fprintf(cmd.OutOrStdout(), "Debounce: %s\n", info.Debounce)
	for _, file := range info.Files {
		fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", file)
	}
}

func dotsServiceStatusHeader(name string, installed bool, platform string) string {
	status := "not installed"
	if installed {
		status = "installed"
	}
	return fmt.Sprintf("%s: %s (%s)", name, status, platform)
}

func printDotsWatchSyncResult(cmd *cobra.Command, result app.DotsWatchSyncResult) {
	event := strings.TrimSpace(result.Event.Path)
	if event == "" {
		event = "filesystem change"
	}
	changes, conflicts := countDotWatchOps(result.Ops)
	if result.Err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Dotfile sync failed after %s: %v\n", event, result.Err)
		return
	}
	switch {
	case conflicts > 0:
		fmt.Fprintf(cmd.ErrOrStderr(), "Dotfile sync found %d conflict(s) after %s.\n", conflicts, event)
	case changes > 0:
		fmt.Fprintf(cmd.OutOrStdout(), "Synced dotfiles after %s (%d change(s)).\n", event, changes)
	default:
		fmt.Fprintf(cmd.OutOrStdout(), "Checked dotfiles after %s; no changes.\n", event)
	}
}

// ─── dots services ───────────────────────────────────────────────────────────

func newDotsServicesCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "services",
		Short: "Inspect optional dotfile services",
	}
	cmd.AddCommand(newDotsServicesStatusCmd(state))
	return cmd
}

func newDotsServicesStatusCmd(state *rootState) *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show reminder and watch service status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			status := state.app.DotsServicesStatus()
			if format == "json" {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(status)
			}
			if err := validateFormat(format, "text", "json"); err != nil {
				return err
			}
			printDotsServicesStatus(cmd, status)
			return nil
		},
	}
	addFormatFlag(cmd, &format, "text", "text", "json")
	return cmd
}

func printDotsServicesStatus(cmd *cobra.Command, status app.DotsServicesStatus) {
	fmt.Fprintln(cmd.OutOrStdout(), "Dots services")
	if status.ReminderError != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Reminder: unavailable - %s\n", status.ReminderError)
	} else if status.Reminder != nil {
		installed := "not installed"
		if status.Reminder.Installed {
			installed = "installed"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Reminder: %s (%s)\n", installed, status.Reminder.Platform)
		fmt.Fprintf(cmd.OutOrStdout(), "  Interval: %s\n", status.Reminder.Interval)
		fmt.Fprintf(cmd.OutOrStdout(), "  Notifications: %s\n", displaySettingsValue(status.Reminder.Notify))
		for _, file := range status.Reminder.Files {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", file)
		}
	}
	if status.WatchError != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Watch: unavailable - %s\n", status.WatchError)
	} else if status.Watch != nil {
		installed := "not installed"
		if status.Watch.Installed {
			installed = "installed"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Watch: %s (%s)\n", installed, status.Watch.Platform)
		fmt.Fprintf(cmd.OutOrStdout(), "  Debounce: %s\n", status.Watch.Debounce)
		for _, file := range status.Watch.Files {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", file)
		}
	}
}

func countDotWatchOps(ops []dots.Op) (changes, conflicts int) {
	for _, op := range ops {
		switch op.Kind {
		case dots.OpLink, dots.OpRepair, dots.OpAdopt, dots.OpUnlink, dots.OpUnlinkSkip, dots.OpUnlinkConflict:
			changes++
		case dots.OpConflict:
			conflicts++
		}
	}
	return changes, conflicts
}

func appleScriptString(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `"`, `\"`)
}

func stderrSuffix(stderr string) string {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return ""
	}
	return ": " + stderr
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func requireDotsConfigured(state *rootState) error {
	if !state.app.DotsConfigured() {
		return fmt.Errorf("dots_repo is not configured\n\nSet it via 'omni ui' (Dots tab) or settings.dots_repo in settings.json")
	}
	return nil
}

func printDotOps(cmd *cobra.Command, ops []dots.Op, dryRun bool) {
	conflicts := 0
	changes := 0
	skipped := 0
	for _, op := range ops {
		switch op.Kind {
		case dots.OpSkip:
			if op.Err != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "  - skipped:  %s — %v\n", op.Dst, op.Err)
				skipped++
			}
		case dots.OpLink:
			fmt.Fprintf(cmd.OutOrStdout(), "  ✓ linked:   %s → %s\n", op.Dst, op.Src)
			changes++
		case dots.OpRepair:
			fmt.Fprintf(cmd.OutOrStdout(), "  ✓ repaired: %s\n", op.Dst)
			changes++
		case dots.OpAdopt:
			fmt.Fprintf(cmd.OutOrStdout(), "  ✓ adopted:  %s → %s\n", op.Dst, op.Src)
			changes++
		case dots.OpConflict:
			fmt.Fprintf(cmd.ErrOrStderr(), "  ✗ conflict: %s — %v\n", op.Dst, op.Err)
			conflicts++
		case dots.OpUnlink:
			fmt.Fprintf(cmd.OutOrStdout(), "  ✓ unlinked: %s\n", op.Dst)
			changes++
		case dots.OpUnlinkSkip:
			fmt.Fprintf(cmd.OutOrStdout(), "  - skipped:  %s\n", op.Dst)
		case dots.OpUnlinkConflict:
			fmt.Fprintf(cmd.ErrOrStderr(), "  ✗ unlink conflict: %s\n", op.Dst)
			conflicts++
		case dots.OpDryLink:
			fmt.Fprintf(cmd.OutOrStdout(), "  → would link:   %s\n", op.Dst)
		case dots.OpDryRepair:
			fmt.Fprintf(cmd.OutOrStdout(), "  → would repair: %s\n", op.Dst)
		case dots.OpDryAdopt:
			fmt.Fprintf(cmd.OutOrStdout(), "  → would adopt:  %s\n", op.Dst)
		}
	}
	if dryRun {
		fmt.Fprintln(cmd.OutOrStdout(), "\nDry-run — no changes made.")
		return
	}
	if changes > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "\n%d symlink(s) updated.\n", changes)
	}
	if conflicts > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "%d conflict(s). Choose use repo version or use local version before syncing.\n", conflicts)
	}
	if changes == 0 && conflicts == 0 && skipped > 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No symlinks updated.")
	}
	if changes == 0 && conflicts == 0 && skipped == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "All symlinks up to date.")
	}
}

func printDotsTable(cmd *cobra.Command, statuses []app.DotStatus) {
	for _, section := range dotCLISections(statuses) {
		if len(section.statuses) == 0 {
			continue
		}
		fmt.Fprintln(cmd.OutOrStdout(), section.title)
		printDotsSectionTable(cmd, section.statuses)
		fmt.Fprintln(cmd.OutOrStdout())
	}
}

type dotCLISection struct {
	title    string
	statuses []app.DotStatus
}

func dotCLISections(statuses []app.DotStatus) []dotCLISection {
	sections := []dotCLISection{
		{title: "Conflict"},
		{title: "Out Of Sync"},
		{title: "Synced"},
		{title: "Ignored"},
	}
	for _, status := range statuses {
		state := dotStatusState(status)
		switch state {
		case app.DotStateConflict, app.DotStateUntrackedConflict, app.DotStateAmbiguous:
			sections[0].statuses = append(sections[0].statuses, status)
		case app.DotStateSynced:
			sections[2].statuses = append(sections[2].statuses, status)
		case app.DotStateIgnored, app.DotStateInactive, app.DotStateDisabled:
			sections[3].statuses = append(sections[3].statuses, status)
		default:
			sections[1].statuses = append(sections[1].statuses, status)
		}
	}
	return sections
}

func printDotsSectionTable(cmd *cobra.Command, statuses []app.DotStatus) {
	const (
		nameW    = 20
		targetW  = 36
		stateW   = 18
		actionsW = 28
	)
	header := fmt.Sprintf("%-*s  %-*s  %-*s  %s", nameW, "NAME", targetW, "TARGET", stateW, "STATE", "ACTIONS")
	fmt.Fprintln(cmd.OutOrStdout(), header)
	fmt.Fprintln(cmd.OutOrStdout(), strings.Repeat("─", len(header)))
	for _, s := range statuses {
		state := dotStatusState(s)
		fmt.Fprintf(cmd.OutOrStdout(), "%-*s  %-*s  %s %-*s  %s\n",
			nameW, s.Name,
			targetW, truncateDotsTarget(s.TargetPath, targetW),
			dotStateIcon(state), stateW-2, state,
			truncateDotsActions(s.Actions, actionsW),
		)
	}
}

func dotStatusState(status app.DotStatus) app.DotState {
	if status.State != "" {
		return status.State
	}
	switch status.Health {
	case app.HealthOK:
		return app.DotStateSynced
	case app.HealthMissing:
		return app.DotStateMissing
	case app.HealthConflict:
		return app.DotStateConflict
	case app.HealthNoSource:
		return app.DotStateNoSource
	default:
		return app.DotState(status.Health)
	}
}

func dotStateIcon(state app.DotState) string {
	switch state {
	case app.DotStateSynced:
		return "✓"
	case app.DotStateConflict, app.DotStateUntrackedConflict, app.DotStateAmbiguous:
		return "✗"
	case app.DotStateNoSource:
		return "?"
	case app.DotStateIgnored, app.DotStateInactive, app.DotStateDisabled:
		return "·"
	default:
		return "!"
	}
}

func truncateDotsTarget(path string, width int) string {
	runes := []rune(path)
	if len(runes) <= width {
		return path
	}
	if width <= 1 {
		return string(runes[:width])
	}
	return "…" + string(runes[len(runes)-width+1:])
}

func truncateDotsActions(actions []app.DotAction, width int) string {
	labels := make([]string, 0, len(actions))
	for _, action := range actions {
		labels = append(labels, string(action))
	}
	out := strings.Join(labels, ",")
	runes := []rune(out)
	if len(runes) <= width {
		return out
	}
	if width <= 1 {
		return string(runes[:width])
	}
	return string(runes[:width-1]) + "…"
}

func healthIcon(h app.DotHealth) string {
	switch h {
	case app.HealthOK:
		return "✓"
	case app.HealthMissing:
		return "·"
	case app.HealthConflict:
		return "✗"
	case app.HealthNoSource:
		return "?"
	default:
		return " "
	}
}

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/dots"
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
		newDotsExtractCmd(state),
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
	var useRepo bool
	var useLocal bool

	cmd := &cobra.Command{
		Use:   "sync [name]",
		Short: "Create or repair dotfile symlinks",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireDotsConfigured(state); err != nil {
				return err
			}
			if useRepo && useLocal {
				return fmt.Errorf("choose at most one of --use-repo or --use-local")
			}
			conflictStrategy := ""
			switch {
			case useRepo:
				conflictStrategy = "use_repo"
			case useLocal:
				conflictStrategy = "use_local"
			}
			if conflictStrategy != "" && !dryRun {
				side := "the repo version over local targets"
				if useLocal {
					side = "local content into the repo"
				}
				ok, err := confirmAction(cmd, state, fmt.Sprintf("Force-resolve all dots conflicts by relinking %s?", side))
				if err != nil || !ok {
					return err
				}
			}
			if err := ensureDotsStowForCLI(cmd, state); err != nil {
				return err
			}
			opts := dots.SyncOptions{DryRun: dryRun, ConflictStrategy: conflictStrategy}
			var (
				ops []dots.Op
				err error
			)
			out := cmd.OutOrStdout()
			opts.Progress = func(event dots.SyncProgressEvent) {
				if event.Done || event.Entry == "" {
					return
				}
				fmt.Fprintf(out, "  %s\n", app.DotsSyncProgressLineText(event))
			}
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
	cmd.Flags().BoolVar(&useRepo, "use-repo", false, "Force-resolve every conflict by keeping the repo version")
	cmd.Flags().BoolVar(&useLocal, "use-local", false, "Force-resolve every conflict by adopting the local version")
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
			if !cmd.Flags().Changed("group") {
				if !stdinIsTerminal() {
					return fmt.Errorf("missing assignment target: pass --group <group> for non-interactive add")
				}
				selected, ok := promptText("Add to group?", "")
				if !ok {
					return fmt.Errorf("missing assignment target: pass --group <group>")
				}
				group = selected
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
	cmd.ValidArgsFunction = func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveFilterDirs
	}
	_ = cmd.RegisterFlagCompletionFunc("group", completeGroupNames(state))
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
			fmt.Fprintf(cmdOut(cmd), "%-18s  %-24s  %s\n", "HOST", "PACKAGE", "ACTIVE")
			fmt.Fprintln(cmdOut(cmd), strings.Repeat("─", 54))
			for _, variant := range variants {
				host := variant.Host
				if variant.Default {
					host = "(default)"
				}
				active := ""
				if variant.Active {
					active = "yes"
				}
				fmt.Fprintf(cmdOut(cmd), "%-18s  %-24s  %s\n", host, variant.Package, active)
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
	cmd.ValidArgsFunction = completeDotNames(state)
	_ = cmd.RegisterFlagCompletionFunc("host", completeHostNames(state))
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
	cmd.ValidArgsFunction = completeDotNames(state)
	_ = cmd.RegisterFlagCompletionFunc("host", completeHostNames(state))
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
			removeGroups = app.NormalizeGroupNames(removeGroups)
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

			if !moveChanged && !removeChanged {
				current, err := state.app.DotGroups(name)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", name, strings.Join(current, ", "))
				return nil
			}

			var change app.DotGroupsChange
			var err error
			if moveChanged {
				change, err = state.app.SetDotGroupsWithState(cmd.Context(), name, []string{moveGroup}, nil, "")
			} else {
				change, err = state.app.RemoveDotGroupsWithState(cmd.Context(), name, removeGroups)
			}
			if err != nil {
				return err
			}
			target := change.DotMemberships[name]
			fmt.Fprintf(cmd.OutOrStdout(), "moved %s to group %s\n", name, strings.Join(target, ", "))
			return nil
		},
	}
	cmd.Flags().StringVar(&moveGroup, "move", "", "Move this dots entry to a group")
	cmd.Flags().StringSliceVar(&removeGroups, "remove", nil, "Remove this dots entry from a group")
	cmd.ValidArgsFunction = completeDotNames(state)
	_ = cmd.RegisterFlagCompletionFunc("move", completeGroupNames(state))
	return cmd
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
	cmd.ValidArgsFunction = completeDotNames(state)
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
	cmd.ValidArgsFunction = completeDotNames(state)
	return cmd
}

// ─── dots extract ─────────────────────────────────────────────────────────────

func newDotsExtractCmd(state *rootState) *cobra.Command {
	var group string
	var name string
	cmd := &cobra.Command{
		Use:   "extract <parent> <subpath>",
		Short: "Split a subdirectory of a dots entry into its own entry/group",
		Long: "Extract a subdirectory out of a tracked directory entry into a new dot entry " +
			"so it can belong to a different group than its parent. The parent stops " +
			"managing the subtree (an ignore is added) and the subtree is adopted as a new entry.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireDotsConfigured(state); err != nil {
				return err
			}
			if err := ensureDotsStowForCLI(cmd, state); err != nil {
				return err
			}
			ops, err := state.app.DotsExtract(cmd.Context(), args[0], args[1], app.DotsExtractOptions{
				Name:  name,
				Group: group,
			})
			printDotOps(cmd, ops, false)
			return err
		},
	}
	cmd.Flags().StringVarP(&group, "group", "g", "", "Group for the new entry (default: this host's group; created if missing)")
	cmd.Flags().StringVar(&name, "name", "", "Name for the new entry (default: derived from parent and subpath)")
	cmd.ValidArgsFunction = completeDotNames(state)
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
				if err := state.app.DotsSetEntryIgnoredContext(cmd.Context(), args[0], path, true); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "ignored dots entry %q\n", args[0])
				return nil
			}
			if err := state.app.DotsAddIgnorePatternContext(cmd.Context(), args[0], args[1]); err != nil {
				return err
			}
			ejected, ejectErr := state.app.DotsEjectIgnoredPathsContext(cmd.Context(), args[0], args[1])
			if ejectErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: eject failed: %v\n", ejectErr)
			} else if ejected > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "ejected %d previously synced path(s)\n", ejected)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "added ignore pattern %q to %q\n", args[1], args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&entry, "entry", false, "Ignore the whole dots entry instead of adding a child pattern")
	cmd.Flags().StringVar(&path, "path", "", "Path for a new ignored discovery candidate")
	cmd.ValidArgsFunction = completeDotNames(state)
	return cmd
}

func newDotsUnignoreCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unignore <name> [pattern]",
		Short: "Include a dots entry or remove one of its ignore patterns",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireDotsConfigured(state); err != nil {
				return err
			}
			if len(args) == 1 {
				if err := state.app.DotsSetEntryIgnoredContext(cmd.Context(), args[0], "", false); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "included dots entry %q\n", args[0])
				return nil
			}
			if err := state.app.DotsRemoveIgnorePatternContext(cmd.Context(), args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed ignore pattern %q from %q\n", args[1], args[0])
			return nil
		},
	}
	cmd.ValidArgsFunction = completeDotNames(state)
	return cmd
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
	cmd.ValidArgsFunction = completeDotNames(state)
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

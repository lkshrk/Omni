package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/dots"
	gosync "github.com/lkshrk/omni/internal/sync"
	textutil "github.com/lkshrk/omni/internal/text"
)

func newBootstrapCmd(state *rootState) *cobra.Command {
	var flagImport bool
	var flagNoImport bool
	var flagImportConfig string

	cmd := &cobra.Command{
		Use:     "bootstrap",
		Aliases: []string{"init"},
		Short:   "Bootstrap omni on this machine",
		Args:    cobra.NoArgs,
		Long: `bootstrap guides this machine into a working omni config:
  1. Detects available package managers (brew, bun, pnpm, npm, uv, pip3)
  2. Creates settings.json with sensible defaults
  3. Creates or activates this machine's host config
  4. Optionally imports or syncs tools and dotfiles

Run 'omni bootstrap' on every new machine to reproduce your environment.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			a := state.app
			out := cmdOut(cmd)
			errOut := cmdErr(cmd)

			plan, err := a.BootstrapPlan(ctx)
			if err != nil {
				return fmt.Errorf("detecting providers: %w", err)
			}
			fmt.Fprintln(out, "Detected providers:")
			for _, p := range plan.Providers {
				if p.Available {
					fmt.Fprintf(out, "  ✓ %-12s %s\n", p.Name, p.Description)
				} else {
					fmt.Fprintf(out, "  ✗ %-12s (not available)\n", p.Name)
				}
			}
			if !plan.AnyProviderAvailable {
				fmt.Fprintln(out, "\nWarning: no providers detected. Install brew, npm, bun, or pip first.")
			}
			fmt.Fprintln(out)

			if plan.HasConfig {
				return runExistingConfigBootstrap(cmd, state, a)
			}
			importedConfig, err := maybeImportExistingConfig(cmd.Context(), state, a, flagImportConfig)
			if err != nil {
				return err
			}
			if importedConfig {
				return runExistingConfigBootstrap(cmd, state, a)
			}

			updateQuarantine := promptBootstrapUpdateQuarantine(state)
			applied, err := a.ApplyBootstrap(ctx, app.BootstrapApplyOptions{
				NodeManager:      plan.NodeManager,
				PythonManager:    plan.PythonManager,
				UpdateQuarantine: updateQuarantine,
			})
			if err != nil {
				return err
			}
			if applied.CreatedConfig {
				fmt.Fprintf(out, "✓ Created %s\n", applied.ConfigPath)
			}
			if applied.NodeManager != "" || applied.PythonManager != "" {
				if applied.NodeManager != "" {
					fmt.Fprintf(out, "  node.manager   = %s\n", applied.NodeManager)
				}
				if applied.PythonManager != "" {
					fmt.Fprintf(out, "  python.manager = %s\n", applied.PythonManager)
				}
			}
			if updateQuarantine != "" {
				fmt.Fprintf(out, "  update_quarantine = %s\n", updateQuarantine)
			}
			fmt.Fprintln(out)
			printBootstrapHost(applied.Host)

			// ── 7. Import prompt ──────────────────────────────────────────────
			doImport := flagImport
			if !flagImport && !flagNoImport {
				doImport = promptYesNo(state, "Import currently installed tools?", true)
			}

			if doImport {
				fmt.Fprintln(out, "Scanning installed tools…")
				result, err := a.Import(ctx, app.ImportOptions{})
				if err != nil {
					return fmt.Errorf("importing tools: %w", err)
				}
				if len(result.Added) == 0 {
					fmt.Fprintln(out, "No tools found to import.")
				} else {
					for _, summary := range app.ImportProviderCountSummaries(result, plan.Providers) {
						fmt.Fprintf(out, "  %-12s %s\n", summary.Provider, summary.CountText)
					}
					fmt.Fprintf(out, "\n✓ %s\n", app.ImportSettingsSummaryText(result))
					if stdinIsTerminal() {
						names := make([]string, len(result.Added))
						for i, t := range result.Added {
							names[i] = t.Name
						}
						promptReassignClaimedTools(state, names)
					}
				}
				fmt.Fprintln(out)
			}

			// ── 8. Sync prompt ────────────────────────────────────────────────
			if promptYesNo(state, "Run sync now to install all tools from config?", doImport) {
				if err := runToolSyncSection(ctx, a); err != nil {
					return err
				}
			}

			// ── 9. Dots setup ─────────────────────────────────────────────────
			if err := runDotsInitSection(ctx, a); err != nil {
				// Non-fatal: dots is optional during bootstrap.
				fmt.Fprintf(errOut, "warning: dots setup: %v\n", err)
			}
			if err := markBootstrapComplete(ctx, a); err != nil {
				return err
			}

			// ── 10. Next steps ────────────────────────────────────────────────
			fmt.Fprintln(out, "Next steps:")
			fmt.Fprintln(out, "  omni sync        — install all tools from config on this machine")
			fmt.Fprintln(out, "  omni ui          — explore and manage tools interactively")
			fmt.Fprintln(out, "  omni add <pkg>   — add a tool to the config")
			fmt.Fprintln(out, "  omni dots sync   — create all dotfile symlinks")
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagImport, "import", false, "import installed tools without prompting")
	cmd.Flags().BoolVar(&flagNoImport, "no-import", false, "skip importing installed tools")
	cmd.Flags().StringVar(&flagImportConfig, "import-config", "", "import an existing settings.json before bootstrapping")
	return cmd
}

func promptBootstrapUpdateQuarantine(state *rootState) string {
	if state != nil && state.yes {
		return ""
	}
	if !promptYesNo(nil, "Enable update quarantine for newly released tool versions?", false) {
		return ""
	}
	value, ok := promptText("Update quarantine duration:", "2d")
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func maybeImportExistingConfig(_ context.Context, state *rootState, a *app.App, importPath string) (bool, error) {
	if strings.TrimSpace(importPath) == "" {
		if state != nil && state.yes {
			return false, nil
		}
		if !promptYesNo(nil, "Import an existing settings.json?", false) {
			return false, nil
		}
		path, ok := promptText("Settings file path:", "")
		if !ok || strings.TrimSpace(path) == "" {
			return false, fmt.Errorf("settings import path is required")
		}
		importPath = path
	}
	if err := a.ImportConfigFile(importPath); err != nil {
		return false, fmt.Errorf("importing settings: %w", err)
	}
	fmt.Fprintf(stdOut(), "✓ Imported settings from %s\n\n", importPath)
	return true, nil
}

func runExistingConfigBootstrap(cmd *cobra.Command, state *rootState, a *app.App) error {
	ctx := cmd.Context()
	out := cmdOut(cmd)
	fmt.Fprintf(out, "Config already exists at %s\n", a.ConfigPath)

	active, groups, ok := a.ActiveHostInfo()
	if ok {
		fmt.Fprintf(out, "✓ Host %q is configured with groups: %s\n\n", active, groupList(groups))
	} else {
		active = a.BootstrapHostName()
		fmt.Fprintf(out, "No host config found for %q.\n", active)
		if hasHosts, err := a.HasHostAssignments(); err == nil && hasHosts {
			fmt.Fprintf(out, "To seed this host from another one, run: omni hosts copy <source-host> %s\n", active)
		}
		if !promptYesNo(state, "Create this host now?", true) {
			fmt.Fprintln(out, "Leaving config unchanged.")
			return nil
		}
		if err := ensureHost(a); err != nil {
			return fmt.Errorf("setting up host: %w", err)
		}
		active, groups, ok = a.ActiveHostInfo()
		if ok {
			fmt.Fprintf(out, "✓ Host %q is ready with groups: %s\n\n", active, groupList(groups))
		}
	}

	if promptYesNo(state, "Run sync now to install configured tools?", true) {
		if err := runToolSyncSection(ctx, a); err != nil {
			return err
		}
	}
	if promptYesNo(state, "Run dotfile sync now?", false) {
		if err := runDotsSyncSection(ctx, a); err != nil {
			return err
		}
	}
	if err := markBootstrapComplete(ctx, a); err != nil {
		return err
	}
	fmt.Fprintln(out, "✓ Bootstrap complete.")
	return nil
}

func runToolSyncSection(ctx context.Context, a *app.App) error {
	fmt.Fprintln(stdOut(), "Syncing…")
	syncResult, err := a.Sync(ctx, gosync.SyncOptions{})
	if err != nil {
		return fmt.Errorf("syncing: %w", err)
	}
	if syncResult == nil {
		return fmt.Errorf("syncing: result unavailable")
	}
	for _, w := range syncResult.Warnings {
		fmt.Fprintf(stdErr(), "warning: %s\n", w)
	}
	for _, op := range syncResult.Ops {
		line := app.SyncOperationLine(op, app.SyncOperationLineOptions{BootstrapStyle: true})
		if line != "" {
			fmt.Fprintln(stdOut(), line)
		}
	}
	if summary := app.SummarizeSyncResult(syncResult); summary.Installed > 0 {
		fmt.Fprintf(stdOut(), "\n%s installed.\n", textutil.PluralCount(summary.Installed, "tool", "tools"))
	}
	fmt.Fprintln(stdOut())
	return nil
}

func runDotsSyncSection(ctx context.Context, a *app.App) error {
	configured, err := a.DotsSyncConfigured()
	if err != nil {
		return fmt.Errorf("loading settings: %w", err)
	}
	if !configured {
		fmt.Fprintln(stdOut(), "Dotfile sync is not configured for this host.")
		return nil
	}
	if !a.DotsStowInstalled(ctx) {
		installMessage := "GNU Stow (stow) is required for dotfile sync. Install stow with your system package manager, then rerun bootstrap."
		if !stdinIsTerminal() {
			return fmt.Errorf("%s", installMessage)
		}
		if !promptYesNo(nil, "GNU Stow (stow) is required for dotfile sync. Install stow with the system package manager now?", false) {
			return fmt.Errorf("%s", installMessage)
		}
		if err := a.InstallDotsStow(ctx); err != nil {
			return err
		}
		fmt.Fprintln(stdOut(), "✓ Stow installed.")
	}
	ops, err := a.DotsSyncContext(ctx, dots.SyncOptions{})
	if err != nil {
		return fmt.Errorf("dots sync: %w", err)
	}
	fmt.Fprintf(stdOut(), "✓ %s\n\n", app.DotsSyncedSummaryText(ops))
	return nil
}

func markBootstrapComplete(ctx context.Context, a *app.App) error {
	if err := a.MarkCurrentHostBootstrapCompleted(ctx); err != nil {
		return fmt.Errorf("marking bootstrap complete: %w", err)
	}
	return nil
}

// ensureHost guarantees that this machine has an active host entry.
func ensureHost(a *app.App) error {
	result, err := a.EnsureBootstrapHost()
	if err != nil {
		return err
	}
	printBootstrapHost(result)
	return nil
}

func printBootstrapHost(result app.BootstrapHostResult) {
	if result.Created {
		fmt.Fprintf(stdOut(), "✓ Created host %q\n\n", result.Host)
		return
	}
	fmt.Fprintf(stdOut(), "✓ Using host %q with groups: %s\n\n", result.Host, groupList(result.Groups))
}

// ─── dots bootstrap section ───────────────────────────────────────────────────

// runDotsInitSection runs the interactive dots setup during omni bootstrap.
// It is non-fatal: if the user skips (empty input) or an error occurs the
// caller logs a warning and continues.
func runDotsInitSection(ctx context.Context, a *app.App) error {
	if ctx == nil {
		ctx = context.Background()
	}
	fmt.Fprintln(stdOut(), "─── Dotfiles ───────────────────────────────────────────────────")
	fmt.Fprintln(stdOut())
	fmt.Fprintln(stdOut(), "omni can manage your config symlinks from a git repo.")
	fmt.Fprint(stdOut(), "Dots repo path (leave empty to skip): ")
	line, ok := scanLine()
	if !ok {
		return fmt.Errorf("unexpected EOF reading dots repo path")
	}
	repoPath := strings.TrimSpace(line)
	if repoPath == "" {
		fmt.Fprintln(stdOut(), "Skipping dots setup.")
		fmt.Fprintln(stdOut())
		return nil
	}

	if !a.DotsStowInstalled(ctx) {
		installMessage := "GNU Stow (stow) is required for dotfile sync. Install stow with your system package manager, then rerun bootstrap."
		if !stdinIsTerminal() {
			return fmt.Errorf("%s", installMessage)
		}
		if !promptYesNo(nil, "GNU Stow (stow) is required for dotfile sync. Install stow with the system package manager now?", false) {
			return fmt.Errorf("%s", installMessage)
		}
		if err := a.InstallDotsStow(ctx); err != nil {
			return err
		}
		fmt.Fprintln(stdOut(), "✓ Stow installed.")
	}

	result, err := a.ConfigureDotsRepo(ctx, repoPath)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdOut(), "✓ dots_repo = %s\n\n", repoPath)

	if len(result.Entries) == 0 {
		fmt.Fprintln(stdOut(), "No dotfile entries discovered yet.")
	} else {
		fmt.Fprintln(stdOut(), "Added initial dots entries:")
		for _, e := range result.Entries {
			fmt.Fprintf(stdOut(), "  %-20s → %s\n", e.Name, e.Path)
		}
		fmt.Fprintln(stdOut())
	}

	fmt.Fprintln(stdOut())
	return nil
}

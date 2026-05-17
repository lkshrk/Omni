package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/dots"
	gosync "github.com/lkshrk/omni/internal/sync"
)

func newBootstrapCmd(state *rootState) *cobra.Command {
	var flagImport bool
	var flagNoImport bool

	cmd := &cobra.Command{
		Use:     "bootstrap",
		Aliases: []string{"init"},
		Short:   "Bootstrap omni on this machine",
		Long: `bootstrap guides this machine into a working omni config:
  1. Detects available package managers (brew, bun, pnpm, npm, uv, pip3)
  2. Creates settings.json with sensible defaults
  3. Creates or activates this machine's host config
  4. Optionally imports or syncs tools and dotfiles

Run 'omni bootstrap' on every new machine to reproduce your environment.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			a := state.app

			// ── 1. Detect providers ───────────────────────────────────────────
			provs, err := a.Providers(ctx)
			if err != nil {
				return fmt.Errorf("detecting providers: %w", err)
			}
			fmt.Println("Detected providers:")
			anyAvail := false
			for _, p := range provs {
				if p.Available {
					fmt.Printf("  ✓ %-12s %s\n", p.Name, p.Description)
					anyAvail = true
				} else {
					fmt.Printf("  ✗ %-12s (not available)\n", p.Name)
				}
			}
			if !anyAvail {
				fmt.Println("\nWarning: no providers detected. Install brew, npm, bun, or pip first.")
			}
			fmt.Println()

			// ── 2. Guard: config already exists ──────────────────────────────
			if a.HasConfig() {
				return runExistingConfigBootstrap(cmd, state, a)
			}

			// ── 3. Auto-detect managers ───────────────────────────────────────
			nodeManager := detectManager("bun", "pnpm", "npm")
			pythonManager := detectManager("uv", "pip3", "pip")
			if pythonManager == "pip" {
				pythonManager = "pip3" // settings canonical value
			}

			// ── 4. Create settings.json ───────────────────────────────────────
			if err := a.CreateEmptyConfig(); err != nil {
				return fmt.Errorf("creating config: %w", err)
			}
			fmt.Printf("✓ Created %s\n", a.ConfigPath)

			// ── 5. Save detected settings ─────────────────────────────────────
			var settings config.Settings
			settings.SetEcosystemManager("node", nodeManager)
			settings.SetEcosystemManager("python", pythonManager)
			if nodeManager != "" || pythonManager != "" {
				if err := a.SaveSettings(ctx, settings); err != nil {
					return fmt.Errorf("saving settings: %w", err)
				}
				if nodeManager != "" {
					fmt.Printf("  node.manager   = %s\n", nodeManager)
				}
				if pythonManager != "" {
					fmt.Printf("  python.manager = %s\n", pythonManager)
				}
			}
			fmt.Println()

			// ── 6. Mandatory host setup ───────────────────────────────────────
			if err := ensureHost(a); err != nil {
				return fmt.Errorf("setting up host: %w", err)
			}

			// ── 7. Import prompt ──────────────────────────────────────────────
			doImport := flagImport
			if !flagImport && !flagNoImport {
				doImport = promptYesNo(state, "Import currently installed tools?", true)
			}

			if doImport {
				fmt.Println("Scanning installed tools…")
				result, err := a.Import(ctx, app.ImportOptions{})
				if err != nil {
					return fmt.Errorf("importing tools: %w", err)
				}
				if len(result.Added) == 0 {
					fmt.Println("No tools found to import.")
				} else {
					byProvider := make(map[string]int)
					for _, t := range result.Added {
						byProvider[t.Provider]++
					}
					for _, p := range provs {
						if n := byProvider[p.Name]; n > 0 {
							fmt.Printf("  %-12s %d tool(s)\n", p.Name, n)
						}
					}
					fmt.Printf("\n✓ Imported %d tool(s) into settings.json\n", len(result.Added))
					if stdinIsTerminal() {
						names := make([]string, len(result.Added))
						for i, t := range result.Added {
							names[i] = t.Name
						}
						promptReassignClaimedTools(state, names)
					}
				}
				fmt.Println()
			}

			// ── 8. Sync prompt ────────────────────────────────────────────────
			if promptYesNo(state, "Run sync now to install all tools from config?", doImport) {
				if err := runToolSyncSection(ctx, a); err != nil {
					return err
				}
			}

			// ── 9. Dots setup ─────────────────────────────────────────────────
			if err := runDotsInitSection(a); err != nil {
				// Non-fatal: dots is optional during bootstrap.
				fmt.Fprintf(os.Stderr, "warning: dots setup: %v\n", err)
			}
			if err := markBootstrapComplete(ctx, a); err != nil {
				return err
			}

			// ── 10. Next steps ────────────────────────────────────────────────
			fmt.Println("Next steps:")
			fmt.Println("  omni sync        — install all tools from config on this machine")
			fmt.Println("  omni ui          — explore and manage tools interactively")
			fmt.Println("  omni add <pkg>   — add a tool to the config")
			fmt.Println("  omni dots sync   — create all dotfile symlinks")
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagImport, "import", false, "import installed tools without prompting")
	cmd.Flags().BoolVar(&flagNoImport, "no-import", false, "skip importing installed tools")
	return cmd
}

func runExistingConfigBootstrap(cmd *cobra.Command, state *rootState, a *app.App) error {
	ctx := cmd.Context()
	fmt.Printf("Config already exists at %s\n", a.ConfigPath)

	active, groups, ok := a.ActiveHostInfo()
	if ok {
		fmt.Printf("✓ Host %q is configured with groups: %s\n\n", active, groupList(groups))
	} else {
		active = shortHostnameForCLI()
		fmt.Printf("No host config found for %q.\n", active)
		if info, err := a.HostStatus(); err == nil && info != nil && len(info.Hosts) > 0 {
			fmt.Printf("To seed this host from another one, run: omni hosts copy <source-host> %s\n", active)
		}
		if !promptYesNo(state, "Create this host now?", true) {
			fmt.Println("Leaving config unchanged.")
			return nil
		}
		if err := ensureHost(a); err != nil {
			return fmt.Errorf("setting up host: %w", err)
		}
		active, groups, ok = a.ActiveHostInfo()
		if ok {
			fmt.Printf("✓ Host %q is ready with groups: %s\n\n", active, groupList(groups))
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
	fmt.Println("✓ Bootstrap complete.")
	return nil
}

func runToolSyncSection(ctx context.Context, a *app.App) error {
	fmt.Println("Syncing…")
	syncResult, err := a.Sync(ctx, gosync.SyncOptions{})
	if err != nil {
		return fmt.Errorf("syncing: %w", err)
	}
	if syncResult == nil {
		return fmt.Errorf("syncing: result unavailable")
	}
	for _, w := range syncResult.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}
	for _, op := range syncResult.Ops {
		switch op.Kind {
		case gosync.OpInstall:
			if op.Err != nil {
				fmt.Printf("  ✗ %s/%s: %v\n", op.Tool.Provider, op.Tool.Name, op.Err)
			} else {
				fmt.Printf("  ✓ installed: %s (%s)\n", op.Tool.Name, op.Tool.Provider)
			}
		case gosync.OpAlreadyInstalled:
			fmt.Printf("  ✓ already installed: %s (%s)\n", op.Tool.Name, op.Tool.Provider)
		case gosync.OpProviderUnavailable:
			fmt.Printf("  ! provider unavailable: %s (skipping %s)\n", op.Tool.Provider, op.Tool.Name)
		}
	}
	if n := len(syncResult.Installed()); n > 0 {
		fmt.Printf("\n%d tool(s) installed.\n", n)
	}
	fmt.Println()
	return nil
}

func runDotsSyncSection(ctx context.Context, a *app.App) error {
	settings, err := a.LoadSettings()
	if err != nil {
		return fmt.Errorf("loading settings: %w", err)
	}
	if strings.TrimSpace(settings.DotsRepo) == "" || config.BoolVal(settings.DotsDisabled) {
		fmt.Println("Dotfile sync is not configured for this host.")
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
		fmt.Println("✓ Stow installed.")
	}
	ops, err := a.DotsSyncContext(ctx, dots.SyncOptions{})
	if err != nil {
		return fmt.Errorf("dots sync: %w", err)
	}
	fmt.Printf("✓ Dotfiles synced (%d operation(s)).\n\n", len(ops))
	return nil
}

func markBootstrapComplete(ctx context.Context, a *app.App) error {
	active, _, ok := a.ActiveHostInfo()
	if !ok {
		return nil
	}
	if err := a.MarkHostBootstrapCompleted(ctx, active); err != nil {
		return fmt.Errorf("marking bootstrap complete: %w", err)
	}
	return nil
}

func shortHostnameForCLI() string {
	h := strings.TrimSpace(os.Getenv("OMNI_HOSTNAME"))
	if h == "" {
		host, err := os.Hostname()
		if err != nil {
			return "localhost"
		}
		h = strings.TrimSpace(host)
		if h == "" {
			return "localhost"
		}
	}
	if idx := strings.IndexByte(h, '.'); idx >= 0 {
		return h[:idx]
	}
	return h
}

// detectManager returns the first binary from candidates that is found in PATH.
func detectManager(candidates ...string) string {
	for _, bin := range candidates {
		if _, err := exec.LookPath(bin); err == nil {
			return bin
		}
	}
	return ""
}

// ensureHost guarantees that this machine has an active host entry.
func ensureHost(a *app.App) error {
	active, groups, ok := a.ActiveHostInfo()
	if ok {
		fmt.Printf("✓ Using host %q with groups: %s\n\n", active, groupList(groups))
		return nil
	}

	if err := a.EnsureHost(active); err != nil {
		return fmt.Errorf("creating host: %w", err)
	}
	fmt.Printf("✓ Created host %q\n\n", active)
	return nil
}

// ─── dots bootstrap section ───────────────────────────────────────────────────

// runDotsInitSection runs the interactive dots setup during omni bootstrap.
// It is non-fatal: if the user skips (empty input) or an error occurs the
// caller logs a warning and continues.
func runDotsInitSection(a *app.App) error {
	fmt.Println("─── Dotfiles ───────────────────────────────────────────────────")
	fmt.Println()
	fmt.Println("omni can manage your config symlinks from a git repo.")
	fmt.Print("Dots repo path (leave empty to skip): ")
	line, ok := scanLine()
	if !ok {
		return fmt.Errorf("unexpected EOF reading dots repo path")
	}
	repoPath := strings.TrimSpace(line)
	if repoPath == "" {
		fmt.Println("Skipping dots setup.")
		fmt.Println()
		return nil
	}

	// Expand env vars and ~ and validate.
	expandedRepoPath, err := dots.ExpandPath(repoPath)
	if err != nil {
		return err
	}
	repoPath, err = filepath.Abs(expandedRepoPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(repoPath); err != nil {
		return fmt.Errorf("repo path %q: %w", repoPath, err)
	}

	ctx := context.Background()
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
		fmt.Println("✓ Stow installed.")
	}

	// Save dots_repo via the app API (same path the TUI uses).
	settings, err := a.LoadSettings()
	if err != nil {
		return fmt.Errorf("loading settings: %w", err)
	}
	settings.DotsRepo = repoPath
	settings.DotsDisabled = config.BoolPtr(false)
	if err := a.SaveSettings(ctx, settings); err != nil {
		return fmt.Errorf("saving dots_repo: %w", err)
	}
	fmt.Printf("✓ dots_repo = %s\n\n", repoPath)

	entries, err := a.BootstrapDotsEntries()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Println("No dotfile entries discovered yet.")
	} else {
		fmt.Println("Added initial dots entries:")
		for _, e := range entries {
			fmt.Printf("  %-20s → %s\n", e.Name, e.Path)
		}
		fmt.Println()
	}

	fmt.Println()
	return nil
}

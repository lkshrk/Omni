package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/dots"
	gosync "github.com/lkshrk/omni/internal/sync"
)

func newInitCmd(state *rootState) *cobra.Command {
	var flagImport bool
	var flagNoImport bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Set up omni on this machine",
		Long: `init sets up omni for first use:
  1. Detects available package managers (brew, bun, pnpm, npm, uv, pip3)
  2. Creates settings.json with sensible defaults
  3. Optionally imports your currently installed tools

Run 'omni init' on every new machine to reproduce your environment.`,
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
				fmt.Printf("Config already exists at %s\n", a.ConfigPath)
				fmt.Println("Nothing to do. Run 'omni sync' to apply it or 'omni import' to add more tools.")
				return nil
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

			// ── 6. Mandatory profile setup ───────────────────────────────────────
			if err := ensureProfile(a); err != nil {
				return fmt.Errorf("setting up profile: %w", err)
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
				}
				fmt.Println()
			}

			// ── 8. Sync prompt ────────────────────────────────────────────────
			if promptYesNo(state, "Run sync now to install all tools from config?", doImport) {
				fmt.Println("Syncing…")
				syncResult, err := a.Sync(ctx, gosync.SyncOptions{})
				if err != nil {
					return fmt.Errorf("syncing: %w", err)
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
			}

			// ── 9. Dots setup ─────────────────────────────────────────────────
			if err := runDotsInitSection(a); err != nil {
				// Non-fatal: dots is optional during init.
				fmt.Fprintf(os.Stderr, "warning: dots setup: %v\n", err)
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

// detectManager returns the first binary from candidates that is found in PATH.
func detectManager(candidates ...string) string {
	for _, bin := range candidates {
		if _, err := exec.LookPath(bin); err == nil {
			return bin
		}
	}
	return ""
}

// ensureProfile guarantees that this machine has an active profile mapped.
// Flow:
//  1. Active profile already exists → done.
//  2. Profiles exist but none mapped → offer the list; "0" falls through to create.
//  3. No profiles or user chose to create → prompt for a name (re-prompts until non-empty).
func ensureProfile(a *app.App) error {
	info, err := a.ProfileStatus()
	if err != nil {
		return err
	}
	hostname, _ := os.Hostname()

	// Already mapped — nothing to do.
	if info.Active != "" {
		fmt.Printf("✓ Using profile %q\n\n", info.Active)
		return nil
	}

	// Profiles exist but this hostname isn't mapped → offer to pick one.
	if len(info.Profiles) > 0 {
		names := sortedProfileNames(info.Profiles)
		fmt.Printf("Profiles exist but %q is not mapped.\n", hostname)
		for i, n := range names {
			fmt.Printf("  %d. %-20s %s\n", i+1, n, groupList(info.Profiles[n].Groups))
		}
		fmt.Println("  0. Create new profile")
		fmt.Print("Select [0]: ")
		line, ok := scanLine()
		if !ok {
			return fmt.Errorf("unexpected EOF reading profile selection")
		}
		line = strings.TrimSpace(line)
		var choice int
		fmt.Sscanf(line, "%d", &choice)
		if choice > 0 && choice <= len(names) {
			selected := names[choice-1]
			if err := a.SetHostname(hostname, selected); err != nil {
				return fmt.Errorf("mapping hostname: %w", err)
			}
			fmt.Printf("✓ Mapped %q → profile %q\n\n", hostname, selected)
			return nil
		}
		fmt.Println()
	}

	// Create a new profile — loop until the user supplies a non-empty name.
	var profileName string
	for profileName == "" {
		fmt.Print("Profile name for this machine: ")
		line, ok := scanLine()
		if !ok {
			return fmt.Errorf("unexpected EOF reading profile name")
		}
		profileName = strings.TrimSpace(line)
		if profileName == "" {
			fmt.Println("  Name cannot be empty. Please try again.")
		}
	}
	if err := a.AddProfile(profileName, []string{}); err != nil {
		return fmt.Errorf("creating profile: %w", err)
	}
	if err := a.SetHostname(hostname, profileName); err != nil {
		return fmt.Errorf("mapping hostname: %w", err)
	}
	fmt.Printf("✓ Created profile %q and mapped %q\n\n", profileName, hostname)
	return nil
}

// ─── dots init section ────────────────────────────────────────────────────────

// runDotsInitSection runs the interactive dots setup during omni init.
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
		installMessage := "GNU Stow (stow) is required for dotfile sync. Install stow with your system package manager, then rerun init."
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

// sortedProfileNames returns profile names in alphabetical order.
func sortedProfileNames(profiles map[string]config.Profile) []string {
	names := make([]string, 0, len(profiles))
	for n := range profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

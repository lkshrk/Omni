// Package cli contains all Cobra command definitions.
package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/tui"
)

// rootState is shared across all subcommands via closure.
type rootState struct {
	configPath string
	cacheDir   string
	yes        bool
	app        *app.App
}

var initRootApp = func(ctx context.Context, a *app.App) error {
	return a.Init(ctx)
}

// NewRootCmd builds and returns the root Cobra command tree.
func NewRootCmd() *cobra.Command {
	state := &rootState{}

	root := &cobra.Command{
		Use:     "omni",
		Version: Version,
		Short:   "keep dev tools in sync across machines from a single JSON config",
		Long: `omni keeps your development tools (brew, npm, pip, …) and dotfiles
in sync across machines from a single JSON config file (settings.json).

New machine? Run 'omni bootstrap' to detect providers, create or activate the
host config, and optionally import or sync tools and dotfiles.

Already set up?
  omni reconcile  sync tools, upgrades, dotfiles, and dotfile commits
  omni sync       sync local tools to match config
  omni dots sync  sync dotfile symlinks from repo`,
		SilenceUsage:  true,
		SilenceErrors: true,
		// No subcommand → launch the TUI directly. This makes `omni` alone
		// behave the same as `omni ui` so the binary is self-contained.
		RunE: func(cmd *cobra.Command, _ []string) error {
			model := tui.New(state.app, cmd.Context())
			p := tea.NewProgram(model, tui.ProgramOptions()...)
			if _, err := p.Run(); err != nil {
				return fmt.Errorf("TUI error: %w", err)
			}
			return nil
		},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			// Skip app initialisation for commands that don't need the DB.
			if cmd.Name() == "help" {
				return nil
			}
			if state.configPath == "" {
				p, err := config.DefaultConfigPath()
				if err != nil {
					return fmt.Errorf("resolving default config path: %w", err)
				}
				state.configPath = p
			}
			a := app.New(state.configPath)
			if state.cacheDir != "" {
				a.CacheDir = state.cacheDir
			}
			if commandInChain(cmd, "doctor") {
				if err := a.InitReadOnly(cmd.Context()); err != nil {
					return fmt.Errorf("initialising diagnostics: %w", err)
				}
				state.app = a
				return nil
			}
			if err := initRootApp(cmd.Context(), a); err != nil {
				return fmt.Errorf("initialising app: %w", err)
			}
			state.app = a
			return requireActiveHost(cmd, a)
		},
		PersistentPostRunE: func(_ *cobra.Command, _ []string) error {
			if state.app != nil {
				return state.app.Close()
			}
			return nil
		},
	}

	root.PersistentFlags().StringVar(&state.configPath, "config", "",
		"omni config file path (default: $XDG_CONFIG_HOME/omni/settings.json)")
	root.PersistentFlags().StringVar(&state.cacheDir, "cache-dir", "",
		"omni cache directory for the database (default: $XDG_CACHE_HOME/omni)")
	root.PersistentFlags().BoolVarP(&state.yes, "yes", "y", false,
		"assume yes for confirmation prompts")

	root.AddCommand(
		newBootstrapCmd(state),
		newReconcileCmd(state),
		newListCmd(state),
		newSyncCmd(state),
		newInstallCmd(state),
		newDeleteCmd(state),
		newUpgradeCmd(state),
		newAddCmd(state),
		newImportCmd(state),
		newSearchCmd(state),
		newRefreshCmd(state),
		newDoctorCmd(state),
		newProvidersCmd(state),
		newSettingsCmd(state),
		newConsolidateCmd(state),
		newSwitchCmd(state),
		newToolsCmd(state),
		newGroupsCmd(state),
		newHostsCmd(state),
		newDotsCmd(state),
		newUICmd(state),
	)

	return root
}

// hostExempt lists command names (and their ancestor names) that may run
// without an active host. Checked against the full command chain.
// NOTE: Do NOT add "omni" here — it is the ancestor of every command and would
// exempt the entire CLI from host enforcement.
var hostExempt = map[string]bool{
	"bootstrap":  true,
	"doctor":     true,
	"init":       true, // compatibility alias for bootstrap
	"hosts":      true,
	"dots":       true, // dots commands work independently of tool hosts
	"ui":         true, // TUI handles its own onboarding including host setup
	"version":    true,
	"providers":  true,
	"settings":   true,
	"help":       true,
	"completion": true,
}

func commandInChain(cmd *cobra.Command, name string) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Name() == name {
			return true
		}
	}
	return false
}

// requireActiveHost returns an error when no host is configured for this machine,
// unless the command (or one of its ancestors) is exempt from host checks,
// or the command was invoked with an explicit --group flag.
func requireActiveHost(cmd *cobra.Command, a *app.App) error {
	// The bare `omni` root command (no subcommand) launches the TUI, which
	// handles host setup internally. Skip enforcement here so the TUI can
	// present its own onboarding flow when no host is configured.
	// NOTE: we check cmd.Parent() == nil rather than adding "omni" to
	// hostExempt, because the ancestor walk would then exempt every subcommand.
	if cmd.Parent() == nil {
		return nil
	}
	for c := cmd; c != nil; c = c.Parent() {
		if hostExempt[c.Name()] {
			return nil
		}
		// An explicit --group flag targets a concrete group and does not need
		// active-host expansion.
		if f := c.Flags().Lookup("group"); f != nil && f.Changed {
			return nil
		}
	}
	return a.RequireActiveHost()
}

// Execute runs the root command with a signal-aware context.
// Pressing Ctrl+C (SIGINT) or sending SIGTERM cancels the context, which
// propagates cancellation to child processes via the context passed to RunE.
func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	root := NewRootCmd()
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		printProviderErrorAdvice(os.Stderr, err)
		os.Exit(1)
	}
}

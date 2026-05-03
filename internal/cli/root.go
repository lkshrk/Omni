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

New machine? Run 'omni init' to detect providers, create the config, and
optionally import your currently installed tools.

Already set up?
  omni sync       sync local tools to match config
  omni dots sync  sync dotfile symlinks from repo`,
		SilenceUsage:  true,
		SilenceErrors: true,
		// No subcommand → launch the TUI directly. This makes `omni` alone
		// behave the same as `omni ui` so the binary is self-contained.
		RunE: func(cmd *cobra.Command, _ []string) error {
			model := tui.New(state.app, cmd.Context())
			p := tea.NewProgram(model)
			if _, err := p.Run(); err != nil {
				return fmt.Errorf("TUI error: %w", err)
			}
			return nil
		},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			// Skip init for commands that don't need the DB.
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
			if err := initRootApp(cmd.Context(), a); err != nil {
				return fmt.Errorf("initialising app: %w", err)
			}
			state.app = a
			return requireProfile(cmd, a)
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
		newInitCmd(state),
		newListCmd(state),
		newSyncCmd(state),
		newInstallCmd(state),
		newDeleteCmd(state),
		newUpgradeCmd(state),
		newAddCmd(state),
		newImportCmd(state),
		newSearchCmd(state),
		newProvidersCmd(state),
		newSettingsCmd(state),
		newConsolidateCmd(state),
		newSwitchCmd(state),
		newToolsCmd(state),
		newGroupsCmd(state),
		newProfileCmd(state),
		newDotsCmd(state),
		newUICmd(state),
	)

	return root
}

// profileExempt lists command names (and their ancestor names) that may run
// without an active profile. Checked against the full command chain.
// NOTE: Do NOT add "omni" here — it is the ancestor of every command and would
// exempt the entire CLI from profile enforcement.
var profileExempt = map[string]bool{
	"init":       true,
	"profile":    true,
	"dots":       true, // dots commands work independently of tool profiles
	"ui":         true, // TUI handles its own onboarding including profile setup
	"version":    true,
	"providers":  true,
	"settings":   true,
	"help":       true,
	"completion": true,
}

// requireProfile returns an error when no profile is mapped to this machine,
// unless the command (or one of its ancestors) is exempt from profile checks,
// or the command was invoked with an explicit --profile flag (which directly
// selects the profile to use, bypassing hostname resolution).
func requireProfile(cmd *cobra.Command, a *app.App) error {
	// The bare `omni` root command (no subcommand) launches the TUI, which
	// handles profile setup internally. Skip enforcement here so the TUI can
	// present its own onboarding flow when no profile is configured.
	// NOTE: we check cmd.Parent() == nil rather than adding "omni" to
	// profileExempt, because the ancestor walk would then exempt every subcommand.
	if cmd.Parent() == nil {
		return nil
	}
	for c := cmd; c != nil; c = c.Parent() {
		if profileExempt[c.Name()] {
			return nil
		}
		// An explicit --profile flag means the caller knows which profile to
		// use — skip the hostname requirement.
		if f := c.Flags().Lookup("profile"); f != nil && f.Changed {
			return nil
		}
	}
	return a.RequireActiveProfile()
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
		os.Exit(1)
	}
}

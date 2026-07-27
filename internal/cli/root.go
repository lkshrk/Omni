package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/profile"
	textutil "github.com/lkshrk/omni/internal/text"
	"github.com/lkshrk/omni/internal/tui"
)

type rootState struct {
	configPath string
	cacheDir   string
	yes        bool
	app        *app.App
}

var initRootApp = func(ctx context.Context, a *app.App) error {
	return a.Init(ctx)
}

func NewRootCmd() *cobra.Command {
	state := &rootState{}

	root := &cobra.Command{
		Use:     "omni",
		Version: Version(),
		Short:   "keep dev tools in sync across machines from a single JSON config",
		Long: `omni keeps your development tools (brew, npm, pip, …) and dotfiles
in sync across machines from a single JSON config file (settings.json).

New machine? Run 'omni bootstrap' to detect providers, create or activate the
host config, and optionally import or sync tools and dotfiles.

Already set up?
  omni reconcile      sync tools, upgrades, dotfiles, and dotfile commits
  omni tools sync     sync local tools to match config
  omni dots sync      sync dotfile symlinks from repo`,
		SilenceUsage:  true,
		SilenceErrors: true,
		// No subcommand launches the TUI so bare `omni` behaves like `omni ui`.
		RunE: func(cmd *cobra.Command, _ []string) error {
			defer profile.Start("cli.tui.run")()
			model := tui.New(cmd.Context(), state.app)
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
				stop := profile.Start("cli.default_config_path")
				p, err := config.DefaultConfigPath()
				stop()
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
				stop := profile.Start("cli.app_init_read_only")
				if err := a.InitReadOnly(cmd.Context()); err != nil {
					stop()
					return fmt.Errorf("initialising diagnostics: %w", err)
				}
				stop()
				state.app = a
				return nil
			}
			stop := profile.Start("cli.app_init")
			if err := initRootApp(cmd.Context(), a); err != nil {
				stop()
				return fmt.Errorf("initialising app: %w", err)
			}
			stop()
			state.app = a
			stop = profile.Start("cli.require_active_host")
			err := requireActiveHost(cmd, a)
			stop()
			return err
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

	symbols := textutil.SymbolsFromEnv()
	root.SetOut(symbols.Writer(os.Stdout))
	root.SetErr(symbols.Writer(os.Stderr))

	root.AddCommand(
		newBootstrapCmd(state),
		newReconcileCmd(state),
		newDoctorCmd(state),
		newSettingsCmd(state),
		newTraceCmd(state),
		newToolsCmd(state),
		newGroupsCmd(state),
		newHostsCmd(state),
		newDotsCmd(state),
		newUICmd(state),
		newAgentsCmd(state),
	)

	return root
}

// Checked against the full command chain, so never add "omni": it is every command's ancestor and would exempt the whole CLI.
var hostExempt = map[string]bool{
	"bootstrap":  true,
	"doctor":     true,
	"init":       true, // compatibility alias for bootstrap
	"hosts":      true,
	"dots":       true, // dots commands work independently of tool hosts
	"ui":         true, // TUI handles its own onboarding including host setup
	"version":    true,
	"settings":   true,
	"trace":      true,
	"help":       true,
	"completion": true,
	"agents":     true,
}

func commandInChain(cmd *cobra.Command, name string) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Name() == name {
			return true
		}
	}
	return false
}

func requireActiveHost(cmd *cobra.Command, a *app.App) error {
	// The bare root launches the TUI, which onboards hosts itself; checked via Parent() because hostExempt would exempt every subcommand.
	if cmd.Parent() == nil {
		return nil
	}
	for c := cmd; c != nil; c = c.Parent() {
		if hostExempt[c.Name()] {
			return nil
		}
		// An explicit --group targets a concrete group and needs no active-host expansion.
		if f := c.Flags().Lookup("group"); f != nil && f.Changed {
			return nil
		}
		// Only install's --force opts out of host enforcement; elsewhere --force is command-local.
		if commandInChain(cmd, "install") {
			if f := c.Flags().Lookup("force"); f != nil && f.Changed {
				return nil
			}
		}
	}
	return a.RequireActiveHost()
}

func Execute() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	var signalExitCode atomic.Int32

	// Force-exit on signal so a blocking stdin read cannot prevent shutdown; under the TUI Ctrl+C arrives as a stdin byte, so this never fires there.
	go func() {
		sig := <-signals
		exitCode := 1
		if signalValue, ok := sig.(syscall.Signal); ok {
			exitCode = 128 + int(signalValue)
		}
		signalExitCode.Store(int32(exitCode))
		cancel()
		fmt.Fprintln(os.Stderr)
		os.Exit(exitCode)
	}()

	root := NewRootCmd()
	if err := root.ExecuteContext(ctx); err != nil {
		// Preserve failure status when the canceled operation's error beats the force-exit goroutine.
		if ctx.Err() != nil {
			exitCode := int(signalExitCode.Load())
			if exitCode == 0 {
				exitCode = 1
			}
			os.Exit(exitCode)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		printProviderErrorAdvice(os.Stderr, err)
		os.Exit(1)
	}
}

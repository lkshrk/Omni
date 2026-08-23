package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lkshrk/omni/internal/apm"
	"github.com/lkshrk/omni/internal/executor"
)

type AgentsSyncAllOptions struct {
	DryRun   bool
	Frozen   bool
	Progress func(string)
	Output   func(stdout, stderr string)
}

type AgentsSyncAllResult struct {
	Output string
	Stderr string
}

func (a *App) APMClient(scope apm.Scope) *apm.Client {
	return apm.New(a.fallbackExecutor(), scope)
}

func (a *App) commandAvailable(name string) bool {
	if checker, ok := a.fallbackExecutor().(interface{ CommandAvailable(string) bool }); ok {
		return checker.CommandAvailable(name)
	}
	return executor.CommandAvailable(name)
}

func (a *App) APMAvailable() bool { return a.commandAvailable("apm") }

func errAPMNotInstalled() error {
	return fmt.Errorf("%w: %s", apm.ErrNotInstalled, apm.InstallHint)
}

// RunAPM delegates lifecycle serialization and mutation safety to APM.
func (a *App) RunAPM(ctx context.Context, args ...string) (apm.Result, error) {
	if !a.APMAvailable() {
		return apm.Result{}, errAPMNotInstalled()
	}
	if err := a.requirePinnedAPM(ctx); err != nil {
		return apm.Result{}, err
	}
	return a.APMClient(apm.Global).Run(ctx, args...)
}

// AgentsSyncAll delegates the complete lifecycle to one APM install.
func (a *App) AgentsSyncAll(ctx context.Context, opts AgentsSyncAllOptions) (AgentsSyncAllResult, error) {
	dir, err := apm.GlobalWorkspaceDir()
	if err != nil {
		return AgentsSyncAllResult{}, err
	}
	if _, err := os.Stat(filepath.Join(dir, "apm.yml")); err != nil {
		if os.IsNotExist(err) {
			workspace, workspaceErr := os.Stat(dir)
			switch {
			case os.IsNotExist(workspaceErr):
				return AgentsSyncAllResult{}, nil
			case workspaceErr != nil:
				return AgentsSyncAllResult{}, fmt.Errorf("inspect global APM workspace: %w", workspaceErr)
			case !workspace.IsDir():
				return AgentsSyncAllResult{}, fmt.Errorf("inspect global APM workspace: %s is not a directory", dir)
			}
			return AgentsSyncAllResult{}, nil
		}
		return AgentsSyncAllResult{}, fmt.Errorf("inspect global APM manifest: %w", err)
	}
	if opts.Progress != nil {
		opts.Progress("Installing agent packages with APM...")
	}
	args := []string{"install", "-g"}
	if opts.Frozen {
		args = append(args, "--frozen")
	}
	if opts.DryRun {
		args = append(args, "--dry-run")
	}
	result, err := a.RunAPM(ctx, args...)
	res := AgentsSyncAllResult{Output: result.Stdout, Stderr: result.Stderr}
	if opts.Output != nil {
		opts.Output(result.Stdout, result.Stderr)
	}
	return res, err
}

// AgentsUpdateAll delegates update and deployment to APM's global workspace.
func (a *App) AgentsUpdateAll(ctx context.Context, dryRun bool) (apm.Result, []string, error) {
	args := []string{"update", "-g", "--yes"}
	if dryRun {
		args = append(args, "--dry-run")
	}
	result, err := a.RunAPM(ctx, args...)
	return result, nil, err
}

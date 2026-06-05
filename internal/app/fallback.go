package app

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/executor"
)

const fallbackInstalledWithGitHub = "gh"

func (a *App) SetFallbackExecutor(exec executor.Executor) {
	a.fallbackExec = exec
}

// SaveToolFallback stores a global fallback recipe for an existing logical tool.
// It only mutates settings.json; install/sync actions decide later whether to use it.
func (a *App) SaveToolFallback(_ context.Context, name string, fallback config.FallbackSpec) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("tool name is required")
	}
	return a.withConfig(func(cfg *config.RootConfig) error {
		if cfg.Tools == nil {
			return fmt.Errorf("tool %q not found", name)
		}
		spec, ok := cfg.Tools[name]
		if !ok {
			return fmt.Errorf("tool %q not found", name)
		}
		spec.Fallback = &fallback
		cfg.Tools[name] = spec
		return nil
	})
}

func (a *App) InstallToolFallback(ctx context.Context, name string) error {
	spec, fallback, err := a.configuredFallback(name)
	if err != nil {
		return err
	}
	if err := a.runFallbackCommand(ctx, name, "install", fallback.Commands.Install); err != nil {
		_ = a.setToolFallbackStatus(name, config.FallbackStatusFailed)
		return err
	}
	installed, err := a.CheckToolFallback(ctx, name)
	if err != nil {
		_ = a.setToolFallbackStatus(name, config.FallbackStatusFailed)
		return err
	}
	if !installed {
		_ = a.setToolFallbackStatus(name, config.FallbackStatusFailed)
		return fmt.Errorf("fallback install verification failed for %s: check command did not pass", name)
	}
	if err := a.setToolFallbackStatus(name, config.FallbackStatusVerified); err != nil {
		return err
	}
	return a.readDB().Upsert(ctx, &database.ToolCache{
		Name:          name,
		Provider:      spec.Provider,
		Package:       fallbackPackage(name, spec),
		Installed:     true,
		InstalledWith: fallbackInstalledWith(fallback),
		Version:       sql.NullString{},
		LastChecked:   time.Now(),
	})
}

func (a *App) CheckToolFallback(ctx context.Context, name string) (bool, error) {
	_, fallback, err := a.configuredFallback(name)
	if err != nil {
		return false, err
	}
	command := strings.TrimSpace(fallback.Commands.Check)
	if command == "" {
		return false, fmt.Errorf("fallback %s: missing check command", name)
	}
	_, _, err = a.fallbackExecutor().Run(ctx, "sh", "-c", command)
	return err == nil, nil
}

func (a *App) UpgradeToolFallback(ctx context.Context, name string) error {
	_, fallback, err := a.configuredFallback(name)
	if err != nil {
		return err
	}
	command := fallback.Commands.Upgrade
	if strings.TrimSpace(command) == "" {
		command = fallback.Commands.Install
	}
	return a.runFallbackCommand(ctx, name, "upgrade", command)
}

func (a *App) UninstallToolFallback(ctx context.Context, name string) error {
	spec, fallback, err := a.configuredFallback(name)
	if err != nil {
		return err
	}
	if strings.TrimSpace(fallback.Commands.Uninstall) == "" {
		return fmt.Errorf("fallback uninstall is not available for %s", name)
	}
	if err := a.runFallbackCommand(ctx, name, "uninstall", fallback.Commands.Uninstall); err != nil {
		return err
	}
	return a.readDB().Delete(ctx, name, spec.Provider, fallbackPackage(name, spec))
}

func (a *App) configuredFallback(name string) (config.ToolSpec, *config.FallbackSpec, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return config.ToolSpec{}, nil, fmt.Errorf("tool name is required")
	}
	cfg, err := a.loadConfig()
	if err != nil {
		return config.ToolSpec{}, nil, fmt.Errorf("loading config: %w", err)
	}
	spec, ok := cfg.Tools[name]
	if !ok {
		return config.ToolSpec{}, nil, fmt.Errorf("tool %q not found", name)
	}
	if spec.Fallback == nil {
		return config.ToolSpec{}, nil, fmt.Errorf("tool %q has no fallback", name)
	}
	return spec, spec.Fallback, nil
}

func (a *App) runFallbackCommand(ctx context.Context, name, action, command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return fmt.Errorf("fallback %s: missing %s command", name, action)
	}
	_, stderr, err := a.fallbackExecutor().Run(ctx, "sh", "-c", command)
	if err != nil {
		return fmt.Errorf("fallback %s %s: %w (stderr: %s)", name, action, err, strings.TrimSpace(stderr))
	}
	return nil
}

func (a *App) fallbackExecutor() executor.Executor {
	if a.fallbackExec != nil {
		return a.fallbackExec
	}
	return executor.New()
}

func (a *App) setToolFallbackStatus(name, status string) error {
	return a.withConfig(func(cfg *config.RootConfig) error {
		spec, ok := cfg.Tools[name]
		if !ok || spec.Fallback == nil {
			return nil
		}
		spec.Fallback.Status = status
		cfg.Tools[name] = spec
		return nil
	})
}

func fallbackInstalledWith(fallback *config.FallbackSpec) string {
	if fallback != nil && fallback.Source.Type == config.FallbackSourceGitHub {
		return fallbackInstalledWithGitHub
	}
	return "fallback"
}

func fallbackPackage(name string, spec config.ToolSpec) string {
	if strings.TrimSpace(spec.Package) != "" {
		return spec.Package
	}
	return name
}

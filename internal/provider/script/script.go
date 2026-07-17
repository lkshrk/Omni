// Package script provides a provider that installs a tool by running a
// user-authored shell command. It fills coverage gaps where no OS package
// manager carries a tool.
package script

import (
	"context"
	"fmt"
	"strings"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/version"
)

const (
	optInstall   = "install"
	optDetect    = "detect"
	optCheck     = "check"
	optUninstall = "uninstall"
	optUpgrade   = "upgrade"
	optVersion   = "version"
	optLatest    = "latest"
)

// Provider runs shell commands declared in a tool's Options map.
type Provider struct {
	exec executor.Executor
}

// New returns a script Provider that runs commands through exec.
func New(exec executor.Executor) *Provider {
	return &Provider{exec: exec}
}

func (p *Provider) Name() string { return "script" }

func (p *Provider) Description() string { return "Run a user-authored install command" }

// Available reports whether a POSIX shell can run a trivial command.
func (p *Provider) Available(ctx context.Context) (bool, error) {
	_, _, err := p.exec.Run(ctx, "sh", "-c", "exit 0")
	return err == nil, nil
}

// Install runs the tool's required "install" command.
func (p *Provider) Install(ctx context.Context, t provider.Tool) error {
	cmd := strings.TrimSpace(t.Options[optInstall])
	if cmd == "" {
		return fmt.Errorf("script %s: missing %q option", t.Name, optInstall)
	}
	return p.run(ctx, t.Name, optInstall, cmd)
}

// Uninstall runs the optional "uninstall" command; no-op when unset.
func (p *Provider) Uninstall(ctx context.Context, t provider.Tool) error {
	cmd := strings.TrimSpace(t.Options[optUninstall])
	if cmd == "" {
		return nil
	}
	return p.run(ctx, t.Name, optUninstall, cmd)
}

// Upgrade runs the optional "upgrade" command, falling back to Install.
func (p *Provider) Upgrade(ctx context.Context, t provider.Tool) error {
	if cmd := strings.TrimSpace(t.Options[optUpgrade]); cmd != "" {
		return p.run(ctx, t.Name, optUpgrade, cmd)
	}
	return p.Install(ctx, t)
}

// IsInstalled probes "check" (exit-code) then "detect" (command -v).
func (p *Provider) IsInstalled(ctx context.Context, t provider.Tool) (bool, string, error) {
	if check := strings.TrimSpace(t.Options[optCheck]); check != "" {
		_, _, err := p.exec.Run(ctx, "sh", "-c", check)
		if err != nil {
			return false, "", nil
		}
		version, err := p.version(ctx, t)
		return true, version, err
	}
	if detect := strings.TrimSpace(t.Options[optDetect]); detect != "" {
		// Pass detect as $1 so shell metacharacters in the value cannot escape
		// the command -v invocation. "detect" is a binary name, not a shell snippet.
		_, _, err := p.exec.Run(ctx, "sh", "-c", `command -v "$1"`, "--", detect)
		if err != nil {
			return false, "", nil
		}
		version, err := p.version(ctx, t)
		return true, version, err
	}
	return false, "", nil
}

func (p *Provider) version(ctx context.Context, t provider.Tool) (string, error) {
	cmd := strings.TrimSpace(t.Options[optVersion])
	if cmd == "" {
		return "", nil
	}
	return p.scalarCommand(ctx, t.Name, optVersion, cmd)
}

// ListInstalled is unsupported for the script provider.
func (p *Provider) ListInstalled(context.Context) ([]provider.InstalledTool, error) {
	return nil, nil
}

// CheckOutdated compares the installed version with the configured latest
// version command. Script authors are responsible for returning both values in
// the same canonical format.
func (p *Provider) CheckOutdated(ctx context.Context, t provider.Tool, currentVersion string) (string, bool, bool, error) {
	cmd := strings.TrimSpace(t.Options[optLatest])
	if cmd == "" {
		return "", false, false, nil
	}
	if strings.TrimSpace(currentVersion) == "" {
		var err error
		currentVersion, err = p.version(ctx, t)
		if err != nil {
			return "", false, true, err
		}
		if currentVersion == "" {
			return "", false, true, fmt.Errorf("script %s latest: installed version is unknown", t.Name)
		}
	}
	latest, err := p.scalarCommand(ctx, t.Name, optLatest, cmd)
	if err != nil {
		return "", false, true, err
	}
	outdated, comparable := version.Newer(latest, currentVersion)
	if !comparable {
		return "", false, true, fmt.Errorf("script %s latest: versions %q and %q are not comparable numeric releases", t.Name, latest, currentVersion)
	}
	return latest, outdated, true, nil
}

func (p *Provider) scalarCommand(ctx context.Context, toolName, action, cmd string) (string, error) {
	stdout, stderr, err := p.exec.Run(ctx, "sh", "-c", cmd)
	if err != nil {
		return "", fmt.Errorf("script %s %s: %w (stderr: %s)", toolName, action, err, strings.TrimSpace(stderr))
	}
	value := strings.TrimSpace(stdout)
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return "", fmt.Errorf("script %s %s must output exactly one non-empty line", toolName, action)
	}
	return value, nil
}

func (p *Provider) run(ctx context.Context, toolName, action, cmd string) error {
	_, stderr, err := p.exec.Run(ctx, "sh", "-c", cmd)
	if err != nil {
		return fmt.Errorf("script %s %s: %w (stderr: %s)", toolName, action, err, strings.TrimSpace(stderr))
	}
	return nil
}

var _ provider.Provider = (*Provider)(nil)
var _ provider.ToolOutdatedChecker = (*Provider)(nil)

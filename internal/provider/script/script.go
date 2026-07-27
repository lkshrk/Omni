// Package script installs via a user-authored shell command, covering tools no OS package manager carries.
package script

import (
	"context"
	"fmt"
	"strings"
	"time"

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
	// Literal version a recipe recorded at materialization time; not a command.
	optRecordedVersion = "recorded_version"
	// Executable to probe when nothing else reports a version; the logical tool name may differ.
	optBin = "bin"
)

const (
	versionProbeTimeout = 5 * time.Second
	// A banner is a line or two; the cap stops a binary that streams on an unknown flag from filling memory.
	versionProbeOutputLimit = 64 << 10
)

type Provider struct {
	exec executor.Executor
}

func New(exec executor.Executor) *Provider {
	return &Provider{exec: exec}
}

func (p *Provider) Name() string { return "script" }

func (p *Provider) Description() string { return "Run a user-authored install command" }

func (p *Provider) Available(ctx context.Context) (bool, error) {
	_, _, err := p.exec.Run(ctx, "sh", "-c", "exit 0")
	return err == nil, nil
}

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
		// detect is a binary name, not a snippet: pass it as $1 so metacharacters cannot escape command -v.
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
		if recorded := strings.TrimSpace(t.Options[optRecordedVersion]); recorded != "" {
			return recorded, nil
		}
		return p.probeVersion(ctx, t), nil
	}
	return p.scalarCommand(ctx, t.Name, optVersion, cmd)
}

// Probe a confirmed installation only as a last resort; missing or unparseable output degrades to "".
func (p *Provider) probeVersion(ctx context.Context, t provider.Tool) string {
	binary := strings.TrimSpace(t.Options[optBin])
	if binary == "" {
		binary = strings.TrimSpace(t.Name)
	}
	if binary == "" {
		return ""
	}
	// A tool that does not know --version can print a whole usage screen or hang; neither may stall a refresh.
	ctx, cancel := context.WithTimeout(ctx, versionProbeTimeout)
	defer cancel()
	ctx = executor.WithOutputLimit(ctx, versionProbeOutputLimit)
	// Pass the binary as $1 to prevent shell injection and close stdin so prompts fail immediately.
	stdout, stderr, err := p.exec.Run(ctx, "sh", "-c", `"$1" --version </dev/null`, "--", binary)
	if err != nil {
		return ""
	}
	// stderr is only a fallback: a tool that does not understand --version prints its usage screen there.
	if v := version.Extract(stdout); v != "" {
		return v
	}
	return version.Extract(stderr)
}

func (p *Provider) ListInstalled(context.Context) ([]provider.InstalledTool, error) {
	return nil, nil
}

// CheckOutdated — Script authors must return the installed and latest versions in the same canonical format.
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
			return "", false, true, fmt.Errorf("script %s latest: %w; set options.version", t.Name, provider.ErrInstalledVersionUnknown)
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

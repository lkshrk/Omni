// Package aptrepo installs packages from a configured apt repository.
package aptrepo

import (
	"context"
	"fmt"
	"strings"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

const (
	optSetup    = "setup"
	optPackages = "packages"
	optCheck    = "check"
	optUpgrade  = "upgrade"
)

// Provider configures an apt repository and installs packages from it.
type Provider struct {
	exec executor.Executor
}

// New returns an apt_repo Provider.
func New(exec executor.Executor) *Provider {
	return &Provider{exec: exec}
}

func (p *Provider) Name() string        { return "apt_repo" }
func (p *Provider) Description() string { return "Configure an apt repository and install packages" }

func (p *Provider) Available(ctx context.Context) (bool, error) {
	_, _, err := p.exec.Run(ctx, "apt-get", "--version")
	return err == nil, nil
}

func (p *Provider) Install(ctx context.Context, tool provider.Tool) error {
	if err := p.runSetup(ctx, tool); err != nil {
		return err
	}
	return p.installPackages(ctx, tool, false)
}

func (p *Provider) Uninstall(ctx context.Context, tool provider.Tool) error {
	pkgs := packageList(tool)
	if len(pkgs) == 0 {
		return fmt.Errorf("apt_repo %s: missing %q option", tool.Name, optPackages)
	}
	rawCmd, rawArgs := provider.PrivilegedCommand("apt-get", append([]string{"remove", "-y"}, pkgs...)...)
	return provider.RunCmd(ctx, p.exec, "apt-get remove "+strings.Join(pkgs, " "), rawCmd, rawArgs...)
}

func (p *Provider) Upgrade(ctx context.Context, tool provider.Tool) error {
	if cmd := strings.TrimSpace(tool.Options[optUpgrade]); cmd != "" {
		return p.run(ctx, tool.Name, optUpgrade, cmd)
	}
	if err := p.runSetup(ctx, tool); err != nil {
		return err
	}
	return p.installPackages(ctx, tool, true)
}

func (p *Provider) IsInstalled(ctx context.Context, tool provider.Tool) (bool, string, error) {
	if check := strings.TrimSpace(tool.Options[optCheck]); check != "" {
		_, _, err := p.exec.Run(ctx, "sh", "-c", check)
		return err == nil, "", nil
	}
	pkgs := packageList(tool)
	if len(pkgs) == 0 {
		return false, "", nil
	}
	for _, pkg := range pkgs {
		stdout, _, err := p.exec.Run(ctx, "dpkg-query", "--showformat=${Version}", "--show", pkg)
		if err != nil || strings.TrimSpace(stdout) == "" {
			return false, "", nil
		}
	}
	return true, "", nil
}

func (p *Provider) ListInstalled(context.Context) ([]provider.InstalledTool, error) {
	return nil, nil
}

func (p *Provider) runSetup(ctx context.Context, tool provider.Tool) error {
	cmd := strings.TrimSpace(tool.Options[optSetup])
	if cmd == "" {
		return fmt.Errorf("apt_repo %s: missing %q option", tool.Name, optSetup)
	}
	return p.run(ctx, tool.Name, optSetup, cmd)
}

func (p *Provider) installPackages(ctx context.Context, tool provider.Tool, upgrade bool) error {
	pkgs := packageList(tool)
	if len(pkgs) == 0 {
		return fmt.Errorf("apt_repo %s: missing %q option", tool.Name, optPackages)
	}
	args := []string{"install", "-y"}
	if upgrade {
		args = []string{"install", "--only-upgrade", "-y"}
	}
	args = append(args, pkgs...)
	rawCmd, rawArgs := provider.PrivilegedCommand("apt-get", args...)
	action := "install"
	if upgrade {
		action = "upgrade"
	}
	return provider.RunCmd(ctx, p.exec, "apt-get "+action+" "+strings.Join(pkgs, " "), rawCmd, rawArgs...)
}

func packageList(tool provider.Tool) []string {
	raw := strings.TrimSpace(tool.Options[optPackages])
	if raw == "" {
		raw = strings.TrimSpace(tool.Package)
	}
	if raw == "" {
		return nil
	}
	return strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
}

func (p *Provider) run(ctx context.Context, toolName, action, cmd string) error {
	// setup writes under /etc/apt and runs apt-get update; both need root.
	rawCmd, rawArgs := provider.PrivilegedCommand("sh", "-c", cmd)
	_, stderr, err := p.exec.Run(ctx, rawCmd, rawArgs...)
	if err != nil {
		return fmt.Errorf("apt_repo %s %s: %w (stderr: %s)", toolName, action, err, strings.TrimSpace(stderr))
	}
	return nil
}

var _ provider.Provider = (*Provider)(nil)

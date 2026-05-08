// Package dnf implements the dnf (Fedora/RHEL) provider.
package dnf

import (
	"context"
	"fmt"
	"strings"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/provider/rpm"
)

// Provider implements the dnf package manager.
type Provider struct {
	exec executor.Executor
}

// New creates a dnf Provider.
func New(exec executor.Executor) *Provider {
	return &Provider{exec: exec}
}

func (p *Provider) Name() string        { return "dnf" }
func (p *Provider) Description() string { return "dnf — Fedora/RHEL package manager" }

func (p *Provider) Available(ctx context.Context) (bool, error) {
	_, _, err := p.exec.Run(ctx, "dnf", "--version")
	if err != nil {
		return false, nil
	}
	return true, nil
}

func (p *Provider) Install(ctx context.Context, tool provider.Tool) error {
	pkg := tool.EffectivePackage()
	cmd, args := provider.PrivilegedCommand("dnf", "install", "-y", pkg)
	return provider.RunCmd(ctx, p.exec, "dnf install "+pkg, cmd, args...)
}

func (p *Provider) Uninstall(ctx context.Context, tool provider.Tool) error {
	pkg := tool.EffectivePackage()
	cmd, args := provider.PrivilegedCommand("dnf", "remove", "-y", pkg)
	return provider.RunCmd(ctx, p.exec, "dnf remove "+pkg, cmd, args...)
}

func (p *Provider) Upgrade(ctx context.Context, tool provider.Tool) error {
	pkg := tool.EffectivePackage()
	cmd, args := provider.PrivilegedCommand("dnf", "upgrade", "-y", pkg)
	return provider.RunCmd(ctx, p.exec, "dnf upgrade "+pkg, cmd, args...)
}

func (p *Provider) PrivilegePlan(_ context.Context, action provider.PrivilegeAction, tool provider.Tool) (provider.PrivilegePlan, error) {
	return provider.SystemPrivilegePlan(p.Name(), action, tool), nil
}

func (p *Provider) IsInstalled(ctx context.Context, tool provider.Tool) (bool, string, error) {
	return rpm.IsInstalled(ctx, p.exec, tool.EffectivePackage())
}

func (p *Provider) ListInstalled(ctx context.Context) ([]provider.InstalledTool, error) {
	stdout, _, err := p.exec.Run(ctx, "dnf", "repoquery", "--userinstalled", "--queryformat", "%{name}\t%{evr}\n")
	if err != nil {
		return nil, fmt.Errorf("dnf repoquery --userinstalled: %w", err)
	}
	var tools []provider.InstalledTool
	for _, line := range strings.Split(stdout, "\n") {
		name, version := rpm.ParseListLine(line)
		if name == "" {
			continue
		}
		tools = append(tools, provider.InstalledTool{
			Tool:    provider.Tool{Name: name, Provider: p.Name()},
			Version: version,
		})
	}
	return tools, nil
}

// InstalledMap returns user-installed dnf packages as lowercase-name→version.
// Implements provider.BulkChecker.
func (p *Provider) InstalledMap(ctx context.Context) (map[string]string, error) {
	tools, err := p.ListInstalled(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(tools))
	for _, tool := range tools {
		m[strings.ToLower(tool.Name)] = tool.Version
	}
	return m, nil
}

// BulkDescribe fetches summaries for multiple tools via a single `dnf info` call.
// Implements provider.BulkDescriber.
func (p *Provider) BulkDescribe(ctx context.Context, tools []provider.Tool) (map[string]string, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	args := make([]string, 0, len(tools)+1)
	args = append(args, "info")
	for _, t := range tools {
		args = append(args, t.EffectivePackage())
	}
	stdout, _, err := p.exec.Run(ctx, "dnf", args...)
	if err != nil {
		return nil, fmt.Errorf("dnf info: %w", err)
	}
	return rpm.ParseInfoSummaries(stdout), nil
}

// Describe fetches a one-line summary via `dnf info`.
func (p *Provider) Describe(ctx context.Context, tool provider.Tool) (string, error) {
	stdout, _, err := p.exec.Run(ctx, "dnf", "info", tool.EffectivePackage())
	if err != nil {
		return "", fmt.Errorf("dnf info %s: %w", tool.EffectivePackage(), err)
	}
	return rpm.ParseInfoSummary(stdout), nil
}

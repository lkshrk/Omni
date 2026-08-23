package dnf

import (
	"context"
	"strings"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/provider/rpm"
)

type Provider struct {
	exec executor.Executor
}

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
	rawCmd, rawArgs, _ := p.PrivilegeCommand(provider.PrivilegeActionInstall, tool)
	cmd, args := provider.PrivilegedCommand(rawCmd, rawArgs...)
	return provider.RunCmd(ctx, p.exec, "dnf install "+pkg, cmd, args...)
}

func (p *Provider) Uninstall(ctx context.Context, tool provider.Tool) error {
	pkg := tool.EffectivePackage()
	rawCmd, rawArgs, _ := p.PrivilegeCommand(provider.PrivilegeActionUninstall, tool)
	cmd, args := provider.PrivilegedCommand(rawCmd, rawArgs...)
	return provider.RunCmd(ctx, p.exec, "dnf remove "+pkg, cmd, args...)
}

func (p *Provider) Upgrade(ctx context.Context, tool provider.Tool) error {
	pkg := tool.EffectivePackage()
	rawCmd, rawArgs, _ := p.PrivilegeCommand(provider.PrivilegeActionUpgrade, tool)
	cmd, args := provider.PrivilegedCommand(rawCmd, rawArgs...)
	return provider.RunCmd(ctx, p.exec, "dnf upgrade "+pkg, cmd, args...)
}

func (p *Provider) PrivilegePlan(_ context.Context, action provider.PrivilegeAction, tool provider.Tool) (provider.PrivilegePlan, error) {
	return provider.SystemPrivilegePlan(p.Name(), action, tool), nil
}

func (p *Provider) PrivilegeCommand(action provider.PrivilegeAction, tool provider.Tool) (string, []string, bool) {
	pkg := tool.EffectivePackage()
	switch action {
	case provider.PrivilegeActionInstall:
		return "dnf", []string{"install", "-y", pkg}, true
	case provider.PrivilegeActionUpgrade:
		return "dnf", []string{"upgrade", "-y", pkg}, true
	default:
		return "dnf", []string{"remove", "-y", pkg}, true
	}
}

func (p *Provider) IsInstalled(ctx context.Context, tool provider.Tool) (bool, string, error) {
	return rpm.IsInstalled(ctx, p.exec, tool.EffectivePackage())
}

func (p *Provider) ListInstalled(ctx context.Context) ([]provider.InstalledTool, error) {
	stdout, stderr, err := p.exec.Run(ctx, "dnf", "repoquery", "--userinstalled", "--queryformat", "%{name}\t%{evr}\n")
	if err != nil {
		return nil, executor.WrapError(err, "dnf repoquery --userinstalled", stdout, stderr)
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

// BulkDescribe fetches summaries for multiple installed tools from the local RPM DB.
func (p *Provider) BulkDescribe(ctx context.Context, tools []provider.Tool) (map[string]string, error) {
	return rpm.Summaries(ctx, p.exec, tools)
}

// Describe fetches a one-line summary from the local RPM DB.
func (p *Provider) Describe(ctx context.Context, tool provider.Tool) (string, error) {
	return rpm.Summary(ctx, p.exec, tool.EffectivePackage())
}

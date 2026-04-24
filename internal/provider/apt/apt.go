// Package apt implements the apt (Debian/Ubuntu) provider.
package apt

import (
	"context"
	"fmt"
	"strings"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

// Provider implements the apt package manager.
type Provider struct {
	exec executor.Executor
}

// New creates an apt Provider.
func New(exec executor.Executor) *Provider {
	return &Provider{exec: exec}
}

func (p *Provider) Name() string        { return "apt" }
func (p *Provider) Description() string { return "apt — Debian/Ubuntu package manager" }

func (p *Provider) Available(ctx context.Context) (bool, error) {
	_, _, err := p.exec.Run(ctx, "apt-get", "--version")
	if err != nil {
		return false, nil
	}
	return true, nil
}

func (p *Provider) Install(ctx context.Context, tool provider.Tool) error {
	pkg := tool.EffectivePackage()
	return provider.RunCmd(ctx, p.exec, "apt-get install "+pkg, "apt-get", "install", "-y", pkg)
}

func (p *Provider) Uninstall(ctx context.Context, tool provider.Tool) error {
	pkg := tool.EffectivePackage()
	return provider.RunCmd(ctx, p.exec, "apt-get remove "+pkg, "apt-get", "remove", "-y", pkg)
}

func (p *Provider) Upgrade(ctx context.Context, tool provider.Tool) error {
	pkg := tool.EffectivePackage()
	return provider.RunCmd(ctx, p.exec, "apt-get upgrade "+pkg, "apt-get", "install", "--only-upgrade", "-y", pkg)
}

func (p *Provider) IsInstalled(ctx context.Context, tool provider.Tool) (bool, string, error) {
	stdout, _, err := p.exec.Run(ctx, "dpkg-query", "--showformat=${Version}", "--show", tool.EffectivePackage())
	if err != nil {
		return false, "", nil // non-zero exit → not installed
	}
	version := strings.TrimSpace(stdout)
	if version == "" {
		return false, "", nil
	}
	return true, version, nil
}

func (p *Provider) ListInstalled(ctx context.Context) ([]provider.InstalledTool, error) {
	manual, err := p.manualPackages(ctx)
	if err != nil {
		return nil, err
	}
	stdout, _, err := p.exec.Run(ctx, "dpkg-query", "-W", "-f=${Package}\\t${Version}\\t${db:Status-Abbrev}\\n")
	if err != nil {
		return nil, fmt.Errorf("dpkg-query list: %w", err)
	}
	var tools []provider.InstalledTool
	for _, line := range strings.Split(stdout, "\n") {
		name, version, ok := parseAPTListLine(line)
		if !ok {
			continue
		}
		if !manual[strings.ToLower(name)] {
			continue
		}
		tools = append(tools, provider.InstalledTool{
			Tool:    provider.Tool{Name: name, Provider: "apt"},
			Version: version,
		})
	}
	return tools, nil
}

// InstalledMap returns explicitly installed apt packages as lowercase-name→version.
// Implements provider.BulkChecker.
func (p *Provider) InstalledMap(ctx context.Context) (map[string]string, error) {
	manual, err := p.manualPackages(ctx)
	if err != nil {
		return nil, err
	}
	stdout, _, err := p.exec.Run(ctx, "dpkg-query", "-W", "-f=${Package}\\t${Version}\\t${db:Status-Abbrev}\\n")
	if err != nil {
		return nil, fmt.Errorf("dpkg-query list: %w", err)
	}
	m := make(map[string]string)
	for _, line := range strings.Split(stdout, "\n") {
		name, version, ok := parseAPTListLine(line)
		if !ok {
			continue
		}
		if !manual[strings.ToLower(name)] {
			continue
		}
		m[strings.ToLower(name)] = version
	}
	return m, nil
}

func (p *Provider) manualPackages(ctx context.Context) (map[string]bool, error) {
	stdout, _, err := p.exec.Run(ctx, "apt-mark", "showmanual")
	if err != nil {
		return nil, fmt.Errorf("apt-mark showmanual: %w", err)
	}
	m := make(map[string]bool)
	for _, line := range strings.Split(stdout, "\n") {
		name := strings.ToLower(strings.TrimSpace(line))
		if name != "" {
			m[name] = true
		}
	}
	return m, nil
}

// Describe fetches a one-line description via `apt-cache show`.
func (p *Provider) Describe(ctx context.Context, tool provider.Tool) (string, error) {
	stdout, _, err := p.exec.Run(ctx, "apt-cache", "show", tool.EffectivePackage())
	if err != nil {
		return "", fmt.Errorf("apt-cache show %s: %w", tool.EffectivePackage(), err)
	}
	return parseAPTDescription(stdout), nil
}

// BulkDescribe fetches descriptions for multiple tools via a single `apt-cache show` call.
// Implements provider.BulkDescriber.
func (p *Provider) BulkDescribe(ctx context.Context, tools []provider.Tool) (map[string]string, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	args := make([]string, 0, len(tools)+1)
	args = append(args, "show")
	for _, t := range tools {
		args = append(args, t.EffectivePackage())
	}
	stdout, _, err := p.exec.Run(ctx, "apt-cache", args...)
	if err != nil {
		return nil, fmt.Errorf("apt-cache show: %w", err)
	}
	return parseAPTBulkDescriptions(stdout), nil
}

// parseAPTBulkDescriptions extracts name→description from multi-package apt-cache show output.
// Prefers locale-specific "Description-xx:" over the generic "Description:".
func parseAPTBulkDescriptions(output string) map[string]string {
	m := make(map[string]string)
	generic := make(map[string]string)
	var currentPkg string
	for _, line := range strings.Split(output, "\n") {
		key, val, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if val == "" {
			continue
		}
		switch {
		case key == "Package":
			currentPkg = val
		case currentPkg == "":
			// no package context yet
		case strings.HasPrefix(key, "Description-"):
			if _, seen := m[currentPkg]; !seen {
				m[currentPkg] = val
			}
		case key == "Description":
			if _, seen := generic[currentPkg]; !seen {
				generic[currentPkg] = val
			}
		}
	}
	for name, desc := range generic {
		if _, ok := m[name]; !ok {
			m[name] = desc
		}
	}
	return m
}

// parseAPTDescription extracts the short description from apt-cache show output.
// Prefers locale-specific "Description-xx:" over the generic "Description:".
func parseAPTDescription(output string) string {
	var generic string
	for _, line := range strings.Split(output, "\n") {
		key, val, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if val == "" {
			continue
		}
		if strings.HasPrefix(key, "Description-") {
			return val
		}
		if key == "Description" && generic == "" {
			generic = val
		}
	}
	return generic
}

// parseAPTListLine parses one dpkg-query output row.
// Expected format: "ripgrep\t14.1.1-1\tii " → ("ripgrep", "14.1.1-1", true)
// Returns ok=false for non-installed packages (status != "ii") or malformed lines.
func parseAPTListLine(line string) (name, version string, ok bool) {
	fields := strings.Split(line, "\t")
	if len(fields) < 3 {
		return "", "", false
	}
	if !strings.HasPrefix(strings.TrimSpace(fields[2]), "ii") {
		return "", "", false
	}
	return strings.TrimSpace(fields[0]), strings.TrimSpace(fields[1]), true
}

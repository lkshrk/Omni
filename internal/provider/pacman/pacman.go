// Package pacman implements the pacman (Arch Linux) provider.
package pacman

import (
	"context"
	"fmt"
	"strings"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

// Provider implements the pacman package manager.
type Provider struct {
	exec executor.Executor
}

// New creates a pacman Provider.
func New(exec executor.Executor) *Provider {
	return &Provider{exec: exec}
}

func (p *Provider) Name() string        { return "pacman" }
func (p *Provider) Description() string { return "pacman — Arch Linux package manager" }

func (p *Provider) Available(ctx context.Context) (bool, error) {
	_, _, err := p.exec.Run(ctx, "pacman", "--version")
	if err != nil {
		return false, nil
	}
	return true, nil
}

func (p *Provider) Install(ctx context.Context, tool provider.Tool) error {
	pkg := tool.EffectivePackage()
	return provider.RunCmd(ctx, p.exec, "pacman -S "+pkg, "pacman", "-S", "--noconfirm", pkg)
}

func (p *Provider) Uninstall(ctx context.Context, tool provider.Tool) error {
	pkg := tool.EffectivePackage()
	return provider.RunCmd(ctx, p.exec, "pacman -R "+pkg, "pacman", "-R", "--noconfirm", pkg)
}

func (p *Provider) Upgrade(ctx context.Context, tool provider.Tool) error {
	// pacman has no upgrade-single-package command; -S reinstalls the latest version.
	pkg := tool.EffectivePackage()
	return provider.RunCmd(ctx, p.exec, "pacman -S "+pkg, "pacman", "-S", "--noconfirm", pkg)
}

func (p *Provider) IsInstalled(ctx context.Context, tool provider.Tool) (bool, string, error) {
	stdout, _, err := p.exec.Run(ctx, "pacman", "-Q", tool.EffectivePackage())
	if err != nil {
		return false, "", nil // non-zero exit → not installed
	}
	_, version := parsePacmanQLine(strings.TrimSpace(stdout))
	return true, version, nil
}

func (p *Provider) ListInstalled(ctx context.Context) ([]provider.InstalledTool, error) {
	explicit, err := p.explicitPackages(ctx)
	if err != nil {
		return nil, err
	}
	stdout, _, err := p.exec.Run(ctx, "pacman", "-Q")
	if err != nil {
		return nil, fmt.Errorf("pacman -Q: %w", err)
	}
	var tools []provider.InstalledTool
	for _, line := range strings.Split(stdout, "\n") {
		name, version := parsePacmanQLine(line)
		if name == "" {
			continue
		}
		if !explicit[strings.ToLower(name)] {
			continue
		}
		tools = append(tools, provider.InstalledTool{
			Tool:    provider.Tool{Name: name, Provider: "pacman"},
			Version: version,
		})
	}
	return tools, nil
}

// InstalledMap returns explicitly installed pacman packages as lowercase-name→version.
// Implements provider.BulkChecker.
func (p *Provider) InstalledMap(ctx context.Context) (map[string]string, error) {
	explicit, err := p.explicitPackages(ctx)
	if err != nil {
		return nil, err
	}
	stdout, _, err := p.exec.Run(ctx, "pacman", "-Q")
	if err != nil {
		return nil, fmt.Errorf("pacman -Q: %w", err)
	}
	m := make(map[string]string)
	for _, line := range strings.Split(stdout, "\n") {
		name, version := parsePacmanQLine(line)
		if name == "" {
			continue
		}
		if !explicit[strings.ToLower(name)] {
			continue
		}
		m[strings.ToLower(name)] = version
	}
	return m, nil
}

func (p *Provider) explicitPackages(ctx context.Context) (map[string]bool, error) {
	stdout, _, err := p.exec.Run(ctx, "pacman", "-Qqe")
	if err != nil {
		return nil, fmt.Errorf("pacman -Qqe: %w", err)
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

// BulkDescribe fetches descriptions for multiple tools via a single `pacman -Si` call.
// Implements provider.BulkDescriber.
func (p *Provider) BulkDescribe(ctx context.Context, tools []provider.Tool) (map[string]string, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	args := make([]string, 0, len(tools)+1)
	args = append(args, "-Si")
	for _, t := range tools {
		args = append(args, t.EffectivePackage())
	}
	stdout, _, err := p.exec.Run(ctx, "pacman", args...)
	if err != nil {
		return nil, fmt.Errorf("pacman -Si: %w", err)
	}
	return parsePacmanBulkDescriptions(stdout), nil
}

// parsePacmanBulkDescriptions extracts name→description from multi-package `pacman -Si` output.
func parsePacmanBulkDescriptions(output string) map[string]string {
	m := make(map[string]string)
	var currentName string
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
		switch key {
		case "Name":
			currentName = val
		case "Description":
			if currentName != "" {
				if _, exists := m[currentName]; !exists {
					m[currentName] = val
				}
			}
		}
	}
	return m
}

// Describe fetches a one-line description via `pacman -Si`.
func (p *Provider) Describe(ctx context.Context, tool provider.Tool) (string, error) {
	stdout, _, err := p.exec.Run(ctx, "pacman", "-Si", tool.EffectivePackage())
	if err != nil {
		return "", fmt.Errorf("pacman -Si %s: %w", tool.EffectivePackage(), err)
	}
	return parsePacmanDescription(stdout), nil
}

// parsePacmanDescription extracts the Description field from `pacman -Si` output.
func parsePacmanDescription(output string) string {
	for _, line := range strings.Split(output, "\n") {
		key, val, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		if strings.TrimSpace(key) == "Description" {
			return strings.TrimSpace(val)
		}
	}
	return ""
}

// parsePacmanQLine parses one `pacman -Q` output row.
// Expected format: "ripgrep 15.1.0-2" → ("ripgrep", "15.1.0-2")
func parsePacmanQLine(line string) (name, version string) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return "", ""
	}
	return fields[0], fields[1]
}

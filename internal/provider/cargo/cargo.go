package cargo

import (
	"context"
	"fmt"
	"strings"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

type Provider struct {
	exec executor.Executor
}

func New(exec executor.Executor) *Provider { return &Provider{exec: exec} }

func (p *Provider) Name() string        { return "cargo" }
func (p *Provider) Description() string { return "cargo — Rust binary crate installer" }

func (p *Provider) Available(ctx context.Context) (bool, error) {
	_, _, err := p.exec.Run(ctx, "cargo", "--version")
	return err == nil, nil
}

func (p *Provider) Install(ctx context.Context, tool provider.Tool) error {
	pkg := tool.EffectivePackage()
	return provider.RunCmd(ctx, p.exec, "cargo install "+pkg, "cargo", "install", pkg)
}

func (p *Provider) Uninstall(ctx context.Context, tool provider.Tool) error {
	pkg := tool.EffectivePackage()
	return provider.RunCmd(ctx, p.exec, "cargo uninstall "+pkg, "cargo", "uninstall", pkg)
}

func (p *Provider) Upgrade(ctx context.Context, tool provider.Tool) error {
	return p.Install(ctx, tool)
}

func (p *Provider) IsInstalled(ctx context.Context, tool provider.Tool) (bool, string, error) {
	installed, err := p.InstalledMap(ctx)
	if err != nil {
		return false, "", err
	}
	version, ok := installed[strings.ToLower(installedPackageName(tool.EffectivePackage()))]
	return ok, version, nil
}

func installedPackageName(spec string) string {
	name, _, versioned := strings.Cut(spec, "@")
	if versioned && name != "" {
		return name
	}
	return spec
}

func (p *Provider) ListInstalled(ctx context.Context) ([]provider.InstalledTool, error) {
	installed, err := p.installed(ctx)
	if err != nil {
		return nil, err
	}
	tools := make([]provider.InstalledTool, 0, len(installed))
	for _, crate := range installed {
		tools = append(tools, provider.InstalledTool{
			Tool:    provider.Tool{Name: crate.name, Provider: p.Name(), Package: crate.name},
			Version: crate.version,
		})
	}
	return tools, nil
}

func (p *Provider) InstalledMap(ctx context.Context) (map[string]string, error) {
	installed, err := p.installed(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(installed))
	for _, crate := range installed {
		result[strings.ToLower(crate.name)] = crate.version
	}
	return result, nil
}

func (p *Provider) installed(ctx context.Context) ([]installedCrate, error) {
	stdout, _, err := p.exec.Run(ctx, "cargo", "install", "--list")
	if err != nil {
		return nil, fmt.Errorf("cargo install --list: %w", err)
	}
	return parseInstalled(stdout), nil
}

type installedCrate struct {
	name    string
	version string
}

// No machine-readable list format: package headers are unindented "name vVERSION:" lines.
func parseInstalled(output string) []installedCrate {
	var crates []installedCrate
	for _, line := range strings.Split(output, "\n") {
		if line == "" || line[0] == ' ' || line[0] == '\t' {
			continue
		}
		fields := strings.Fields(strings.TrimSuffix(line, ":"))
		if len(fields) < 2 || !strings.HasPrefix(fields[1], "v") || len(fields[1]) == 1 {
			continue
		}
		crates = append(crates, installedCrate{name: fields[0], version: strings.TrimPrefix(fields[1], "v")})
	}
	return crates
}

func (p *Provider) Search(ctx context.Context, query string) ([]provider.SearchResult, error) {
	stdout, _, err := p.exec.Run(ctx, "cargo", "search", "--color", "never", "--limit", "20", query)
	if err != nil {
		return nil, fmt.Errorf("cargo search %s: %w", query, err)
	}
	return parseSearch(stdout), nil
}

func parseSearch(output string) []provider.SearchResult {
	var results []provider.SearchResult
	for _, line := range strings.Split(output, "\n") {
		name, rest, ok := strings.Cut(line, " = ")
		if !ok {
			continue
		}
		version, description, _ := strings.Cut(rest, " # ")
		version = strings.TrimSpace(version)
		if len(version) < 2 || version[0] != '"' || version[len(version)-1] != '"' {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		results = append(results, provider.SearchResult{
			Name:           name,
			Version:        strings.Trim(version, "\""),
			Description:    strings.TrimSpace(description),
			Provider:       "cargo",
			SourceProvider: "cargo",
		})
	}
	return results
}

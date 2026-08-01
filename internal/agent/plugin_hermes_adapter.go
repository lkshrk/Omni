package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lkshrk/omni/internal/config"
)

type hermesPluginAdapter struct {
	exec func(context.Context, string, ...string) (string, string, error)
}

func NewHermesPluginAdapter(
	execFn func(context.Context, string, ...string) (string, string, error),
	_ func(string) (string, bool),
) PluginAdapter {
	return &hermesPluginAdapter{exec: execFn}
}

func (a *hermesPluginAdapter) ID() string { return "hermes-agent" }

func (a *hermesPluginAdapter) SupportsMarketplaces() bool  { return false }
func (a *hermesPluginAdapter) SupportsDirectSources() bool { return true }

func (a *hermesPluginAdapter) Available() bool {
	_, err := lookPath("hermes")
	return err == nil
}

func (a *hermesPluginAdapter) InstallPlugin(ctx context.Context, p config.Plugin) error {
	_, stderr, err := a.exec(ctx, "hermes", "plugins", "install", p.Source, "--enable")
	if err != nil {
		return fmt.Errorf("hermes plugins install %s: %w: %s", p.Source, err, stderr)
	}
	return nil
}

func (a *hermesPluginAdapter) RemovePlugin(ctx context.Context, p config.Plugin) error {
	_, stderr, err := a.exec(ctx, "hermes", "plugins", "remove", p.Name)
	if err != nil {
		return fmt.Errorf("hermes plugins remove %s: %w: %s", p.Name, err, stderr)
	}
	return nil
}

func (a *hermesPluginAdapter) UpdatePlugin(ctx context.Context, name, _ string) error {
	_, stderr, err := a.exec(ctx, "hermes", "plugins", "update", name)
	if err != nil {
		return fmt.Errorf("hermes plugins update %s: %w: %s", name, err, stderr)
	}
	return nil
}

type hermesPluginListEntry struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Source      string `json:"source"`
}

func (a *hermesPluginAdapter) ListPlugins(ctx context.Context) ([]InstalledPlugin, error) {
	stdout, stderr, err := a.exec(ctx, "hermes", "plugins", "list", "--user", "--no-bundled", "--json")
	if err != nil {
		return nil, fmt.Errorf("hermes plugins list: %w: %s", err, stderr)
	}
	if strings.HasPrefix(strings.TrimSpace(stdout), "No plugins installed.") {
		return []InstalledPlugin{}, nil
	}
	var entries []hermesPluginListEntry
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		return nil, fmt.Errorf("hermes plugins list: parse json: %w", err)
	}
	if entries == nil {
		return nil, fmt.Errorf("hermes plugins list: parse json: expected array, got null")
	}
	plugins := make([]InstalledPlugin, 0, len(entries))
	for _, entry := range entries {
		if entry.Source != "user" && entry.Source != "git" {
			continue
		}
		plugins = append(plugins, InstalledPlugin{
			Name:    entry.Name,
			Version: entry.Version,
		})
	}
	return plugins, nil
}

func (a *hermesPluginAdapter) ListMarketplaces(context.Context) ([]InstalledMarketplace, error) {
	return []InstalledMarketplace{}, nil
}

func (a *hermesPluginAdapter) AddMarketplace(context.Context, config.Marketplace) error {
	return fmt.Errorf("hermes has no plugin marketplaces")
}

func (a *hermesPluginAdapter) UpdateMarketplaces(context.Context) error {
	return fmt.Errorf("hermes has no plugin marketplaces")
}

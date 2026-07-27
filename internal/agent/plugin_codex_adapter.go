package agent

import (
	"context"
	"fmt"

	"github.com/lkshrk/omni/internal/config"
)

type codexPluginAdapter struct {
	exec      func(context.Context, string, ...string) (string, string, error)
	lookupEnv func(string) (string, bool)
}

func NewCodexPluginAdapter(
	execFn func(context.Context, string, ...string) (string, string, error),
	lookupEnv func(string) (string, bool),
) PluginAdapter {
	return &codexPluginAdapter{exec: execFn, lookupEnv: lookupEnv}
}

func (a *codexPluginAdapter) ID() string { return "codex" }

func (a *codexPluginAdapter) Available() bool {
	_, err := lookPath("codex")
	return err == nil
}

func (a *codexPluginAdapter) InstallPlugin(ctx context.Context, p config.Plugin) error {
	id := p.Name + "@" + p.Marketplace
	_, stderr, err := a.exec(ctx, "codex", "plugin", "add", id)
	if err != nil {
		return fmt.Errorf("codex plugin add %s: %w: %s", id, err, stderr)
	}
	return nil
}

// Unlike claude, `codex plugin remove` has no confirmation flag at all, so none is passed.
func (a *codexPluginAdapter) RemovePlugin(ctx context.Context, p config.Plugin) error {
	id := p.Name + "@" + p.Marketplace
	_, stderr, err := a.exec(ctx, "codex", "plugin", "remove", id)
	if err != nil {
		return fmt.Errorf("codex plugin remove %s: %w: %s", id, err, stderr)
	}
	return nil
}

// The codex plugin CLI has no update subcommand, so this never shells out.
func (a *codexPluginAdapter) UpdatePlugin(_ context.Context, _, _ string) error {
	return fmt.Errorf("codex has no plugin update command")
}

// Source only: codex derives its own name from it, which may not match config.Marketplace.Name.
func (a *codexPluginAdapter) AddMarketplace(ctx context.Context, m config.Marketplace) error {
	_, stderr, err := a.exec(ctx, "codex", "plugin", "marketplace", "add", m.Source)
	if err != nil {
		return fmt.Errorf("codex plugin marketplace add %s: %w: %s", m.Source, err, stderr)
	}
	return nil
}

// Unlike claude, codex reports name and marketplaceName separately, and version is nullable for available-but-uninstalled entries.
type codexPluginListEntry struct {
	Name            string  `json:"name"`
	MarketplaceName string  `json:"marketplaceName"`
	Version         *string `json:"version"`
}

// Always wrapped in {installed, available}, even without --available, unlike claude's bare-array default.
type codexPluginListResponse struct {
	Installed *[]codexPluginListEntry `json:"installed"`
	Available *[]codexPluginListEntry `json:"available"`
}

func (a *codexPluginAdapter) ListPlugins(ctx context.Context) ([]InstalledPlugin, error) {
	stdout, stderr, err := a.exec(ctx, "codex", "plugin", "list", "--json")
	if err != nil {
		return nil, fmt.Errorf("codex plugin list: %w: %s", err, stderr)
	}
	var resp *codexPluginListResponse
	installed, isArray, err := decodeCLIList[codexPluginListEntry](stdout, &resp)
	if err != nil {
		return nil, fmt.Errorf("codex plugin list: parse json: %w", err)
	}
	// The bare-array form carries only installed entries, so no latest versions can be derived.
	var available []codexPluginListEntry
	if !isArray {
		if resp == nil {
			return nil, fmt.Errorf("codex plugin list: parse json: expected object, got null")
		}
		if resp.Installed == nil || resp.Available == nil {
			return nil, fmt.Errorf("codex plugin list: parse json: expected installed and available arrays")
		}
		installed, available = *resp.Installed, *resp.Available
	}
	latestByIdentity := make(map[string]string, len(available))
	for _, e := range available {
		if e.Version != nil && *e.Version != "" {
			latestByIdentity[e.Name+"@"+e.MarketplaceName] = *e.Version
		}
	}
	plugins := make([]InstalledPlugin, 0, len(installed))
	for _, e := range installed {
		version := ""
		if e.Version != nil {
			version = *e.Version
		}
		plugins = append(plugins, InstalledPlugin{
			Name:          e.Name,
			Marketplace:   e.MarketplaceName,
			Version:       version,
			LatestVersion: latestByIdentity[e.Name+"@"+e.MarketplaceName],
		})
	}
	return plugins, nil
}

// The re-addable source lives in MarketplaceSource.Source, not the owner-derived Name, and Root's mtime is the only last-update signal.
type codexMarketplaceListEntry struct {
	Name              string `json:"name"`
	Root              string `json:"root"`
	MarketplaceSource struct {
		Source string `json:"source"`
	} `json:"marketplaceSource"`
}

type codexMarketplaceListResponse struct {
	Marketplaces *[]codexMarketplaceListEntry `json:"marketplaces"`
}

func (a *codexPluginAdapter) ListMarketplaces(ctx context.Context) ([]InstalledMarketplace, error) {
	stdout, stderr, err := a.exec(ctx, "codex", "plugin", "marketplace", "list", "--json")
	if err != nil {
		return nil, fmt.Errorf("codex plugin marketplace list: %w: %s", err, stderr)
	}
	var resp *codexMarketplaceListResponse
	entries, isArray, err := decodeCLIList[codexMarketplaceListEntry](stdout, &resp)
	if err != nil {
		return nil, fmt.Errorf("codex plugin marketplace list: parse json: %w", err)
	}
	if !isArray {
		if resp == nil {
			return nil, fmt.Errorf("codex plugin marketplace list: parse json: expected object, got null")
		}
		if resp.Marketplaces == nil {
			return nil, fmt.Errorf("codex plugin marketplace list: parse json: expected marketplaces array")
		}
		entries = *resp.Marketplaces
	}
	out := make([]InstalledMarketplace, 0, len(entries))
	for _, m := range entries {
		out = append(out, InstalledMarketplace{Name: m.Name, Source: m.MarketplaceSource.Source, UpdatedAt: dirModTime(m.Root)})
	}
	return out, nil
}

// `codex plugin marketplace upgrade` with no name upgrades every marketplace.
func (a *codexPluginAdapter) UpdateMarketplaces(ctx context.Context) error {
	_, stderr, err := a.exec(ctx, "codex", "plugin", "marketplace", "upgrade")
	if err != nil {
		return fmt.Errorf("codex plugin marketplace upgrade: %w: %s", err, stderr)
	}
	return nil
}

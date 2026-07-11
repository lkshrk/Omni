package app

import (
	"context"
	"encoding/json"
	"fmt"
	osExec "os/exec"

	"github.com/lkshrk/omni/internal/config"
)

type codexPluginAdapter struct {
	exec      func(context.Context, string, ...string) (string, string, error)
	lookupEnv func(string) (string, bool)
}

// NewCodexPluginAdapter returns a PluginAdapter that delegates to the codex CLI.
func NewCodexPluginAdapter(
	execFn func(context.Context, string, ...string) (string, string, error),
	lookupEnv func(string) (string, bool),
) PluginAdapter {
	return &codexPluginAdapter{exec: execFn, lookupEnv: lookupEnv}
}

func (a *codexPluginAdapter) ID() string { return "codex" }

func (a *codexPluginAdapter) Available() bool {
	_, err := osExec.LookPath("codex")
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

// RemovePlugin uninstalls by the full name@marketplace identity, matching
// InstallPlugin's identity form. Unlike claude, the probe transcript shows
// `codex plugin remove` has no confirmation flag at all, so none is passed.
func (a *codexPluginAdapter) RemovePlugin(ctx context.Context, p config.Plugin) error {
	id := p.Name + "@" + p.Marketplace
	_, stderr, err := a.exec(ctx, "codex", "plugin", "remove", id)
	if err != nil {
		return fmt.Errorf("codex plugin remove %s: %w: %s", id, err, stderr)
	}
	return nil
}

// UpdatePlugin is not supported: `codex plugin update --help` reports
// "unrecognized subcommand 'update'" — the codex plugin CLI has no update
// command, so this never shells out.
func (a *codexPluginAdapter) UpdatePlugin(_ context.Context, _, _ string) error {
	return fmt.Errorf("codex has no plugin update command")
}

// AddMarketplace declares a marketplace by source only — like claude, codex
// derives its own name from the source (the GitHub owner segment, per the
// probe transcript), which may not match config.Marketplace.Name. omni never
// issues `plugin marketplace remove`; see PluginAdapter's doc comment.
func (a *codexPluginAdapter) AddMarketplace(ctx context.Context, m config.Marketplace) error {
	_, stderr, err := a.exec(ctx, "codex", "plugin", "marketplace", "add", m.Source)
	if err != nil {
		return fmt.Errorf("codex plugin marketplace add %s: %w: %s", m.Source, err, stderr)
	}
	return nil
}

// codexPluginListEntry mirrors one element of the installed/available arrays
// returned by `codex plugin list --json`, per the probe transcript in
// docs/superpowers/research/2026-07-02-plugin-cli-probe.md. Unlike claude,
// codex reports name and marketplaceName as separate fields rather than a
// combined id, and version is nullable for available-but-uninstalled entries.
type codexPluginListEntry struct {
	Name            string  `json:"name"`
	MarketplaceName string  `json:"marketplaceName"`
	Version         *string `json:"version"`
}

// codexPluginListResponse is always wrapped in {installed, available}, even
// without --available, unlike claude's bare-array default (probe Deviation 3).
type codexPluginListResponse struct {
	Installed []codexPluginListEntry `json:"installed"`
	Available []codexPluginListEntry `json:"available"`
}

func (a *codexPluginAdapter) ListPlugins(ctx context.Context) ([]InstalledPlugin, error) {
	stdout, stderr, err := a.exec(ctx, "codex", "plugin", "list", "--json")
	if err != nil {
		return nil, fmt.Errorf("codex plugin list: %w: %s", err, stderr)
	}
	var resp codexPluginListResponse
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		return nil, fmt.Errorf("codex plugin list: parse json: %w", err)
	}
	latestByIdentity := make(map[string]string, len(resp.Available))
	for _, e := range resp.Available {
		if e.Version != nil && *e.Version != "" {
			latestByIdentity[e.Name+"@"+e.MarketplaceName] = *e.Version
		}
	}
	plugins := make([]InstalledPlugin, 0, len(resp.Installed))
	for _, e := range resp.Installed {
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

// codexMarketplaceListEntry mirrors one element of `codex plugin marketplace
// list --json`'s marketplaces array, per the probe transcript. The real,
// re-addable source string lives in MarketplaceSource.Source (a full git
// URL), distinct from the owner-derived top-level Name (see AddMarketplace).
// Root is the on-disk clone directory; its mtime is the only last-update
// signal available, since this JSON carries no date field.
type codexMarketplaceListEntry struct {
	Name              string `json:"name"`
	Root              string `json:"root"`
	MarketplaceSource struct {
		Source string `json:"source"`
	} `json:"marketplaceSource"`
}

type codexMarketplaceListResponse struct {
	Marketplaces []codexMarketplaceListEntry `json:"marketplaces"`
}

func (a *codexPluginAdapter) ListMarketplaces(ctx context.Context) ([]InstalledMarketplace, error) {
	stdout, stderr, err := a.exec(ctx, "codex", "plugin", "marketplace", "list", "--json")
	if err != nil {
		return nil, fmt.Errorf("codex plugin marketplace list: %w: %s", err, stderr)
	}
	var resp codexMarketplaceListResponse
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		return nil, fmt.Errorf("codex plugin marketplace list: parse json: %w", err)
	}
	out := make([]InstalledMarketplace, 0, len(resp.Marketplaces))
	for _, m := range resp.Marketplaces {
		out = append(out, InstalledMarketplace{Name: m.Name, Source: m.MarketplaceSource.Source, UpdatedAt: dirModTime(m.Root)})
	}
	return out, nil
}

// UpdateMarketplaces refreshes every configured Git marketplace snapshot
// (`codex plugin marketplace upgrade` with no name upgrades all, per --help).
func (a *codexPluginAdapter) UpdateMarketplaces(ctx context.Context) error {
	_, stderr, err := a.exec(ctx, "codex", "plugin", "marketplace", "upgrade")
	if err != nil {
		return fmt.Errorf("codex plugin marketplace upgrade: %w: %s", err, stderr)
	}
	return nil
}

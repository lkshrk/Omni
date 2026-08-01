package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/lkshrk/omni/internal/config"
)

type grokPluginAdapter struct {
	exec      func(context.Context, string, ...string) (string, string, error)
	lookupEnv func(string) (string, bool)
}

func NewGrokPluginAdapter(
	execFn func(context.Context, string, ...string) (string, string, error),
	lookupEnv func(string) (string, bool),
) PluginAdapter {
	return &grokPluginAdapter{exec: execFn, lookupEnv: lookupEnv}
}

func (a *grokPluginAdapter) ID() string { return "grok" }

func (a *grokPluginAdapter) SupportsMarketplaces() bool  { return true }
func (a *grokPluginAdapter) SupportsDirectSources() bool { return false }

func (a *grokPluginAdapter) Available() bool {
	_, err := lookPath("grok")
	return err == nil
}

func (a *grokPluginAdapter) InstallPlugin(ctx context.Context, p config.Plugin) error {
	source, err := a.resolveInstallSource(ctx, p)
	if err != nil {
		return err
	}
	_, stderr, err := a.exec(ctx, "grok", "plugin", "install", source, "--trust")
	if err != nil {
		return fmt.Errorf("grok plugin install %s: %w: %s", source, err, stderr)
	}
	return nil
}

// --confirm is required off a TTY, which omni always is.
func (a *grokPluginAdapter) RemovePlugin(ctx context.Context, p config.Plugin) error {
	_, stderr, err := a.exec(ctx, "grok", "plugin", "uninstall", p.Name, "--confirm")
	if err != nil {
		return fmt.Errorf("grok plugin uninstall %s: %w: %s", p.Name, err, stderr)
	}
	return nil
}

func (a *grokPluginAdapter) UpdatePlugin(ctx context.Context, name, _ string) error {
	_, stderr, err := a.exec(ctx, "grok", "plugin", "update", name)
	if err != nil {
		return fmt.Errorf("grok plugin update %s: %w: %s", name, err, stderr)
	}
	return nil
}

func (a *grokPluginAdapter) AddMarketplace(ctx context.Context, m config.Marketplace) error {
	_, stderr, err := a.exec(ctx, "grok", "plugin", "marketplace", "add", m.Source)
	if err != nil {
		return fmt.Errorf("grok plugin marketplace add %s: %w: %s", m.Source, err, stderr)
	}
	return nil
}

type grokPluginListEntry struct {
	Status      string  `json:"status"`
	Name        string  `json:"name"`
	Version     *string `json:"version"`
	Marketplace string  `json:"marketplace"`
}

func (a *grokPluginAdapter) ListPlugins(ctx context.Context) ([]InstalledPlugin, error) {
	if stdout, _, err := a.exec(ctx, "grok", "plugin", "list", "--json", "--available"); err == nil {
		return parseGrokPluginList(stdout, true)
	}
	stdout, stderr, err := a.exec(ctx, "grok", "plugin", "list", "--json")
	if err != nil {
		return nil, fmt.Errorf("grok plugin list: %w: %s", err, stderr)
	}
	return parseGrokPluginList(stdout, false)
}

func parseGrokPluginList(out string, hasAvailable bool) ([]InstalledPlugin, error) {
	var entries []grokPluginListEntry
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		return nil, fmt.Errorf("grok plugin list: parse json: %w", err)
	}
	if entries == nil {
		return nil, fmt.Errorf("grok plugin list: parse json: expected array, got null")
	}
	latestByIdentity := make(map[string]string)
	if hasAvailable {
		for _, e := range entries {
			if e.Status != "available" {
				continue
			}
			if e.Version != nil && *e.Version != "" {
				latestByIdentity[grokPluginIdentity(e.Name, e.Marketplace)] = *e.Version
			}
		}
	}
	plugins := make([]InstalledPlugin, 0)
	for _, e := range entries {
		if hasAvailable && e.Status == "available" {
			continue
		}
		if hasAvailable && e.Status != "" && e.Status != "installed" {
			continue
		}
		version := ""
		if e.Version != nil {
			version = *e.Version
		}
		identity := grokPluginIdentity(e.Name, e.Marketplace)
		plugins = append(plugins, InstalledPlugin{
			Name:          e.Name,
			Marketplace:   e.Marketplace,
			Version:       version,
			LatestVersion: latestByIdentity[identity],
		})
	}
	return plugins, nil
}

func grokPluginIdentity(name, marketplace string) string {
	return name + "@" + marketplace
}

type grokMarketplaceListEntry struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Source struct {
		URL string `json:"url"`
	} `json:"source"`
}

func (a *grokPluginAdapter) ListMarketplaces(ctx context.Context) ([]InstalledMarketplace, error) {
	entries, err := a.listMarketplaceEntries(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]InstalledMarketplace, 0, len(entries))
	for _, m := range entries {
		out = append(out, InstalledMarketplace{
			Name:   m.Name,
			Source: grokMarketplaceSource(m),
		})
	}
	return out, nil
}

func (a *grokPluginAdapter) listMarketplaceEntries(ctx context.Context) ([]grokMarketplaceListEntry, error) {
	stdout, stderr, err := a.exec(ctx, "grok", "plugin", "marketplace", "list", "--json")
	if err != nil {
		return nil, fmt.Errorf("grok plugin marketplace list: %w: %s", err, stderr)
	}
	var entries []grokMarketplaceListEntry
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		return nil, fmt.Errorf("grok plugin marketplace list: parse json: %w", err)
	}
	if entries == nil {
		return nil, fmt.Errorf("grok plugin marketplace list: parse json: expected array, got null")
	}
	return entries, nil
}

func (a *grokPluginAdapter) UpdateMarketplaces(ctx context.Context) error {
	_, stderr, err := a.exec(ctx, "grok", "plugin", "marketplace", "update")
	if err != nil {
		return fmt.Errorf("grok plugin marketplace update: %w: %s", err, stderr)
	}
	return nil
}

// Maps a name+marketplace manifest entry to grok's plugin@qualifier install form, where the qualifier is the marketplace's owner/repo shorthand.
func (a *grokPluginAdapter) resolveInstallSource(ctx context.Context, p config.Plugin) (string, error) {
	entries, err := a.listMarketplaceEntries(ctx)
	if err != nil {
		return "", err
	}
	for _, m := range entries {
		if m.Name != p.Marketplace {
			continue
		}
		if q := grokMarketplaceQualifier(m); q != "" {
			return p.Name + "@" + q, nil
		}
		break
	}
	if strings.Contains(p.Marketplace, "/") {
		return p.Name + "@" + p.Marketplace, nil
	}
	return "", fmt.Errorf("grok marketplace %q not found for plugin %q", p.Marketplace, p.Name)
}

func grokMarketplaceSource(m grokMarketplaceListEntry) string {
	if q := grokMarketplaceQualifier(m); q != "" {
		return q
	}
	return m.Source.URL
}

// The owner/repo shorthand grok accepts in plugin install pins.
func grokMarketplaceQualifier(m grokMarketplaceListEntry) string {
	raw := strings.TrimSpace(m.Source.URL)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "/") && !strings.Contains(raw, "://") && !strings.HasPrefix(raw, "git@") {
		return strings.TrimSuffix(raw, ".git")
	}
	if strings.HasPrefix(raw, "git@github.com:") {
		return strings.TrimSuffix(strings.TrimPrefix(raw, "git@github.com:"), ".git")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	path := strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), ".git")
	if u.Host == "github.com" && strings.Count(path, "/") == 1 {
		return path
	}
	return ""
}

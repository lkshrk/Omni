package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type PluginStatus string

const (
	PluginStatusInstalled        PluginStatus = "installed"
	PluginStatusMissing          PluginStatus = "missing"
	PluginStatusUnmanaged        PluginStatus = "unmanaged"
	PluginStatusAgentUnavailable PluginStatus = "agent-unavailable"
)

// PluginRow is a display row for the TUI plugin chip and CLI list.
type PluginRow struct {
	Name           string
	Marketplace    string
	Groups         []string
	Agents         []string
	PerAgentStatus map[string]PluginStatus
	Version        string
	LatestVersion  string
	Sha            string
	LatestSha      string
	Description    string
}

// marketplaceManifestEntry mirrors one element of plugins[] in a
// marketplace's .claude-plugin/marketplace.json. Version is absent on most
// real marketplace entries (e.g. superpowers, caveman) — only some carry it
// (e.g. the lsp-suite's security-guidance at "2.0.6") — so an empty Version
// here is the common case, not an error.
type marketplaceManifestEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
}

// marketplaceManifest mirrors the top-level shape of marketplace.json.
type marketplaceManifest struct {
	Plugins []marketplaceManifestEntry `json:"plugins"`
}

// pluginManifestEntry reads a plugin's manifest entry by name from a
// marketplace's manifest.json. A missing manifest file or a plugin name
// absent from it is not an error — it just yields a zero-value entry.
func pluginManifestEntry(manifestPath, name string) marketplaceManifestEntry {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return marketplaceManifestEntry{}
	}
	var manifest marketplaceManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return marketplaceManifestEntry{}
	}
	for _, p := range manifest.Plugins {
		if p.Name == name {
			return p
		}
	}
	return marketplaceManifestEntry{}
}

// pluginMarketplaceManifestPath builds the on-disk path to a marketplace's
// manifest.json given the marketplace name.
func pluginMarketplaceManifestPath(home, marketplace string) string {
	return filepath.Join(home, ".claude", "plugins", "marketplaces", marketplace, ".claude-plugin", "marketplace.json")
}

// looksLikeGitSha reports whether s is plausibly a git commit sha rather than
// a semantic version: all hex digits, no dot, and long enough to not be a
// coincidental short number. Used to gate the sha-prefix fallback in
// Outdated() to only the plugins that actually version themselves this way
// (e.g. caveman-commit's "25d22f864ad6"), so it never fires for ordinary
// semantic versions.
func looksLikeGitSha(s string) bool {
	if len(s) < 7 || strings.Contains(s, ".") {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// Outdated reports whether an update is available.
//
// The marketplace catalog's source.sha (LatestSha) is the marketplace repo's
// current commit, not the sha of the version a client would actually
// install — comparing it against the installed commit sha produces false
// positives for plugins that are genuinely up to date but happened to be
// installed from an earlier commit of an unchanged release (verified live:
// superpowers 6.1.1 flagged outdated forever despite being latest). So sha
// comparison is never used as a general outdated signal.
//
// Rules, in order:
//  1. Both Version and LatestVersion (marketplace manifest version) known:
//     compare them directly.
//  2. Version itself looks like a git sha (plugins that version themselves
//     by commit, e.g. caveman-commit's "25d22f864ad6", with no manifest
//     version to compare against): outdated when LatestSha does not have
//     Version as a prefix.
//  3. No usable signal: not outdated (never guess).
func (r PluginRow) Outdated() bool {
	if r.Version != "" && r.LatestVersion != "" {
		return r.Version != r.LatestVersion
	}
	if looksLikeGitSha(r.Version) && r.LatestSha != "" {
		return !strings.HasPrefix(r.LatestSha, r.Version)
	}
	return false
}

// PluginRows returns managed rows (from manifest) with per-adapter status,
// and unmanaged entries (from ListPlugins()) not present in the manifest,
// keyed by agent ID. Mirrors McpServerRows.
func (a *App) PluginRows(ctx context.Context) (managed []PluginRow, unmanaged map[string][]InstalledPlugin, err error) {
	cfg, loadErr := a.loadConfig()
	if loadErr != nil {
		return nil, nil, loadErr
	}
	home, homeErr := os.UserHomeDir()
	if homeErr != nil {
		return nil, nil, homeErr
	}
	installedByAgent := make(map[string]map[string]InstalledPlugin)
	unmanaged = make(map[string][]InstalledPlugin)
	for _, adapter := range a.pluginAdapters() {
		if !adapter.Available() {
			continue
		}
		listed, listErr := adapter.ListPlugins(ctx)
		if listErr != nil {
			continue
		}
		byName := make(map[string]InstalledPlugin, len(listed))
		for _, p := range listed {
			byName[p.Name] = p
		}
		installedByAgent[adapter.ID()] = byName
	}
	manifestNames := make(map[string]struct{}, len(cfg.Agents.Plugins))
	for _, p := range cfg.Agents.Plugins {
		manifestNames[p.Name] = struct{}{}
		entry := pluginManifestEntry(pluginMarketplaceManifestPath(home, p.Marketplace), p.Name)
		row := PluginRow{
			Name:           p.Name,
			Marketplace:    p.Marketplace,
			Groups:         pluginGroupsForName(cfg, p.Name),
			Agents:         append([]string(nil), p.Agents...),
			PerAgentStatus: make(map[string]PluginStatus),
			Description:    entry.Description,
			LatestVersion:  entry.Version,
		}
		for _, adapter := range a.pluginAdapters() {
			if !adapter.Available() {
				row.PerAgentStatus[adapter.ID()] = PluginStatusAgentUnavailable
				continue
			}
			byName, ok := installedByAgent[adapter.ID()]
			if !ok {
				row.PerAgentStatus[adapter.ID()] = PluginStatusMissing
				continue
			}
			if plg, found := byName[p.Name]; found {
				row.PerAgentStatus[adapter.ID()] = PluginStatusInstalled
				if row.Version == "" {
					row.Version = plg.Version
				}
				if row.LatestVersion == "" {
					row.LatestVersion = plg.LatestVersion
				}
				if row.Sha == "" {
					row.Sha = plg.Sha
				}
				if row.LatestSha == "" {
					row.LatestSha = plg.LatestSha
				}
			} else {
				row.PerAgentStatus[adapter.ID()] = PluginStatusMissing
			}
		}
		managed = append(managed, row)
	}
	for _, adapter := range a.pluginAdapters() {
		byName, ok := installedByAgent[adapter.ID()]
		if !ok {
			continue
		}
		for name, plg := range byName {
			if _, inManifest := manifestNames[name]; !inManifest {
				unmanaged[adapter.ID()] = append(unmanaged[adapter.ID()], plg)
			}
		}
	}
	a.cacheAgentsRowsSectionBestEffort(ctx, agentsRowsCachePluginsKey, cachedPluginRows{Rows: managed, Unmanaged: unmanaged})
	return managed, unmanaged, nil
}

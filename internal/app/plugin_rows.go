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
	// PathOutdated mirrors InstalledPlugin.PathOutdated — see its doc
	// comment. Set from the first adapter that reports a non-nil value.
	PathOutdated *bool
	Description  string
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
// install — comparing it against the installed commit sha directly produces
// false positives for plugins that are genuinely up to date but happened to
// be installed from an earlier commit of an unchanged release (verified
// live: superpowers 6.1.1 flagged outdated forever despite being latest). So
// raw repo-HEAD sha comparison is never used as a general outdated signal;
// PathOutdated (below) is the precise replacement for the common versionless
// case, computed per-plugin-subdirectory rather than per-repo.
//
// Rules, in order:
//  1. Both Version and LatestVersion (marketplace manifest version) known:
//     compare them directly.
//  2. PathOutdated known (adapter compared the plugin's own source path's
//     last-touched commit at HEAD against the same query at the installed
//     commit): trust it directly.
//  3. Version itself looks like a git sha (plugins that version themselves
//     by commit, e.g. caveman-commit's "25d22f864ad6", with no manifest
//     version to compare against): outdated when LatestSha does not have
//     Version as a prefix.
//  4. No usable signal: not outdated (never guess).
func (r PluginRow) Outdated() bool {
	return pluginOutdated(r.Version, r.LatestVersion, r.LatestSha, r.PathOutdated)
}

// pluginOutdated is the single "is an update available" verdict shared by
// PluginRow and InstalledPlugin. See PluginRow.Outdated for the rule rationale.
// It deliberately never compares Sha against LatestSha directly (repo-HEAD sha
// drift is not a reliable update signal).
func pluginOutdated(version, latestVersion, latestSha string, pathOutdated *bool) bool {
	if version != "" && latestVersion != "" {
		return version != latestVersion
	}
	if pathOutdated != nil {
		return *pathOutdated
	}
	if looksLikeGitSha(version) && latestSha != "" {
		return !strings.HasPrefix(latestSha, version)
	}
	return false
}

// PluginUpdateKind classifies how a plugin row's version cell should render.
type PluginUpdateKind int

const (
	PluginUpToDate        PluginUpdateKind = iota // no update; render the plain version
	PluginVersionUpgrade                          // authoritative version pair, newer available: Current→Latest
	PluginShaDrift                                // no version pair; installed commit differs from source HEAD (informational only, not an "update available" claim)
	PluginUpdateAvailable                         // outdated via PathOutdated / sha-prefix rule, nothing displayable to compare
)

// PluginUpdate is the display-ready update verdict for a plugin row. It is the
// single source both the status mark (via Outdated) and the version cell
// render from, so the two can never disagree.
type PluginUpdate struct {
	Kind    PluginUpdateKind
	Current string
	Latest  string
}

// pluginUpdateDisplay projects the version-comparison fields into a display
// verdict. An authoritative Version/LatestVersion pair wins when present: if it
// agrees, the row is up to date and any sha drift is suppressed (this is the
// superpowers-6.1.1 false positive the Outdated doc comment warns about — a
// current release installed from an earlier repo commit). Sha drift is shown
// only for plugins that carry no comparable version pair.
func pluginUpdateDisplay(version, latestVersion, sha, latestSha string, pathOutdated *bool) PluginUpdate {
	if version != "" && latestVersion != "" {
		if version != latestVersion {
			return PluginUpdate{Kind: PluginVersionUpgrade, Current: version, Latest: latestVersion}
		}
		return PluginUpdate{Kind: PluginUpToDate}
	}
	if sha != "" && latestSha != "" && sha != latestSha {
		return PluginUpdate{Kind: PluginShaDrift, Current: sha, Latest: latestSha}
	}
	if pluginOutdated(version, latestVersion, latestSha, pathOutdated) {
		return PluginUpdate{Kind: PluginUpdateAvailable}
	}
	return PluginUpdate{Kind: PluginUpToDate}
}

// Update returns the display-ready update verdict for the managed plugin row.
func (r PluginRow) Update() PluginUpdate {
	return pluginUpdateDisplay(r.Version, r.LatestVersion, r.Sha, r.LatestSha, r.PathOutdated)
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
				if row.PathOutdated == nil {
					row.PathOutdated = plg.PathOutdated
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

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lkshrk/omni/internal/config"
)

type PluginStatus string

const (
	PluginStatusInstalled        PluginStatus = "installed"
	PluginStatusMissing          PluginStatus = "missing"
	PluginStatusUnmanaged        PluginStatus = "unmanaged"
	PluginStatusAgentUnavailable PluginStatus = "agent-unavailable"
	// Restore matches on the full identity, so it would neither notice nor converge this on its own.
	PluginStatusDrifted PluginStatus = "drifted"
)

const pluginDriftRemedy = `"omni agents plugins resolve" (--use-managed / --use-local)`

// An adapter reporting no marketplace cannot contradict anything; treating unknown as a mismatch would flag every plugin forever.
func pluginMarketplaceDrifted(declared, live string) bool {
	return live != "" && live != declared
}

// A name can be installed from more than one marketplace, so the declared identity wins; matching on name alone made the drift verdict depend on the adapter's listing order.
func pluginForRow(listed []InstalledPlugin, declared config.Plugin) (InstalledPlugin, bool) {
	for _, plg := range listed {
		if plg.Name == declared.Name && !pluginMarketplaceDrifted(declared.Marketplace, plg.Marketplace) {
			return plg, true
		}
	}
	return foreignPluginCopy(listed, declared)
}

// The copy that blocks the declared identity: same name, a marketplace that contradicts the manifest.
func foreignPluginCopy(listed []InstalledPlugin, declared config.Plugin) (InstalledPlugin, bool) {
	for _, plg := range listed {
		if plg.Name == declared.Name && pluginMarketplaceDrifted(declared.Marketplace, plg.Marketplace) {
			return plg, true
		}
	}
	return InstalledPlugin{}, false
}

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
	// Set from the first adapter that reports a non-nil value.
	PathOutdated      *bool
	Description       string
	Drifted           bool
	DriftMarketplaces map[string]string
}

// Version is absent on most real marketplace entries, so an empty Version here is the common case, not an error.
type marketplaceManifestEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
}

type marketplaceManifest struct {
	Plugins []marketplaceManifestEntry `json:"plugins"`
}

// A missing manifest file or an absent plugin name is not an error; it yields a zero-value entry.
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

func pluginMarketplaceManifestPath(home, marketplace string) string {
	return filepath.Join(home, ".claude", "plugins", "marketplaces", marketplace, ".claude-plugin", "marketplace.json")
}

// Gates the sha-prefix fallback to plugins that actually version themselves by commit.
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

// Outdated — Repo-HEAD sha is never a general outdated signal: a current release installed from an earlier commit reads as outdated forever.
func (r PluginRow) Outdated() bool {
	return pluginOutdated(r.Version, r.LatestVersion, r.LatestSha, r.PathOutdated)
}

// Never compares Sha against LatestSha directly: repo-HEAD sha drift is not a reliable update signal.
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

type PluginUpdateKind int

const (
	PluginUpToDate        PluginUpdateKind = iota // no update; render the plain version
	PluginVersionUpgrade                          // authoritative version pair, newer available: Current→Latest
	PluginShaDrift                                // no version pair; installed commit differs from source HEAD (informational only, not an "update available" claim)
	PluginUpdateAvailable                         // outdated via PathOutdated / sha-prefix rule, nothing displayable to compare
)

// PluginUpdate — The single source both the status mark and the version cell render from, so the two cannot disagree.
type PluginUpdate struct {
	Kind    PluginUpdateKind
	Current string
	Latest  string
}

// An authoritative version pair wins and suppresses sha drift, which is only shown when no pair exists.
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

func (r PluginRow) Update() PluginUpdate {
	return pluginUpdateDisplay(r.Version, r.LatestVersion, r.Sha, r.LatestSha, r.PathOutdated)
}

// PluginRows — Unmanaged entries come from ListPlugins keyed by agent ID. Mirrors McpServerRows.
func (a *App) PluginRows(ctx context.Context) (managed []PluginRow, unmanaged map[string][]InstalledPlugin, err error) {
	cfg, loadErr := a.loadConfig()
	if loadErr != nil {
		return nil, nil, loadErr
	}
	home, homeErr := os.UserHomeDir()
	if homeErr != nil {
		return nil, nil, homeErr
	}
	installedByAgent := make(map[string][]InstalledPlugin)
	unmanaged = make(map[string][]InstalledPlugin)
	for _, adapter := range a.pluginAdapters() {
		if !adapter.Available() {
			continue
		}
		listed, listErr := adapter.ListPlugins(ctx)
		if listErr != nil {
			return nil, nil, fmt.Errorf("list plugins for %s: %w", adapter.ID(), listErr)
		}
		installedByAgent[adapter.ID()] = listed
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
			// Restore and "plugins resolve" both ignore untargeted adapters, so reporting drift there would be a finding no verb can clear.
			if !pluginTargetsAdapter(p, adapter.ID()) {
				continue
			}
			if !adapter.Available() {
				row.PerAgentStatus[adapter.ID()] = PluginStatusAgentUnavailable
				continue
			}
			listed, ok := installedByAgent[adapter.ID()]
			if !ok {
				row.PerAgentStatus[adapter.ID()] = PluginStatusMissing
				continue
			}
			if plg, found := pluginForRow(listed, p); found {
				if pluginMarketplaceDrifted(p.Marketplace, plg.Marketplace) {
					row.PerAgentStatus[adapter.ID()] = PluginStatusDrifted
					row.Drifted = true
					if row.DriftMarketplaces == nil {
						row.DriftMarketplaces = make(map[string]string)
					}
					row.DriftMarketplaces[adapter.ID()] = plg.Marketplace
					continue
				}
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
		for _, plg := range installedByAgent[adapter.ID()] {
			if _, inManifest := manifestNames[plg.Name]; !inManifest {
				unmanaged[adapter.ID()] = append(unmanaged[adapter.ID()], plg)
			}
		}
	}
	a.cacheAgentsRowsSectionBestEffort(ctx, agentsRowsCachePluginsKey, cachedPluginRows{Rows: managed, Unmanaged: unmanaged})
	return managed, unmanaged, nil
}

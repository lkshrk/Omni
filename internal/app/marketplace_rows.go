package app

import (
	"context"
	"time"
)

// MarketplaceRow is a display row for the TUI marketplace chip.
type MarketplaceRow struct {
	Name           string
	Source         string
	Groups         []string
	Agents         []string
	PerAgentStatus map[string]PluginStatus
	UpdatedAt      time.Time
}

// MarketplaceRows returns managed rows (from the manifest) with per-adapter
// status, and unmanaged entries (from ListMarketplaces()) not present in the
// manifest, keyed by agent ID. Mirrors PluginRows.
func (a *App) MarketplaceRows(ctx context.Context) (managed []MarketplaceRow, unmanaged map[string][]InstalledMarketplace, err error) {
	cfg, loadErr := a.loadConfig()
	if loadErr != nil {
		return nil, nil, loadErr
	}
	installedByAgent := make(map[string]map[string]InstalledMarketplace)
	unmanaged = make(map[string][]InstalledMarketplace)
	for _, adapter := range a.pluginAdapters() {
		if !adapter.Available() {
			continue
		}
		listed, listErr := adapter.ListMarketplaces(ctx)
		if listErr != nil {
			continue
		}
		byName := make(map[string]InstalledMarketplace, len(listed))
		for _, mk := range listed {
			byName[mk.Name] = mk
		}
		installedByAgent[adapter.ID()] = byName
	}
	manifestNames := make(map[string]struct{}, len(cfg.Agents.Marketplaces))
	for _, mk := range cfg.Agents.Marketplaces {
		manifestNames[mk.Name] = struct{}{}
		row := MarketplaceRow{
			Name:           mk.Name,
			Source:         mk.Source,
			Groups:         marketplaceGroupsForName(cfg, mk.Name),
			Agents:         append([]string(nil), mk.Agents...),
			PerAgentStatus: make(map[string]PluginStatus),
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
			if installed, found := byName[mk.Name]; found {
				row.PerAgentStatus[adapter.ID()] = PluginStatusInstalled
				if row.UpdatedAt.IsZero() {
					row.UpdatedAt = installed.UpdatedAt
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
		for name, mk := range byName {
			if _, inManifest := manifestNames[name]; !inManifest {
				unmanaged[adapter.ID()] = append(unmanaged[adapter.ID()], mk)
			}
		}
	}
	a.cacheAgentsRowsSectionBestEffort(ctx, agentsRowsCacheMarketplacesKey, cachedMarketplaceRows{Rows: managed, Unmanaged: unmanaged})
	return managed, unmanaged, nil
}

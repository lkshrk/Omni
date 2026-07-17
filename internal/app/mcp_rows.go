package app

import (
	"context"
)

// McpStatus is the per-adapter install state of a manifest server.
type McpStatus string

const (
	McpStatusInstalled        McpStatus = "installed"
	McpStatusMissing          McpStatus = "missing"
	McpStatusAgentUnavailable McpStatus = "agent-unavailable"
	// McpStatusShadowed means the server is absent from the agent's own
	// config but an installed plugin of the same name provides it (plugins
	// are plugin-scoped and never appear in an agent's user-scope config —
	// see installedPluginNames), so it is not a real gap and must render and
	// restore differently than McpStatusMissing.
	McpStatusShadowed McpStatus = "shadowed"
)

// McpServerRow is a display row for the TUI mcp tab and CLI list.
type McpServerRow struct {
	Name           string
	Transport      string
	Command        string
	URL            string
	Version        string
	Groups         []string
	Agents         []string
	PerAgentStatus map[string]McpStatus
	// ShadowedByPlugin is true when an installed plugin of the same name
	// provides this server on at least one adapter (see PerAgentStatus's
	// McpStatusShadowed). The manifest entry is kept, not hidden — it's
	// declared intent — but RestoreMcpServers skips installing it.
	ShadowedByPlugin bool
}

// McpServerRows returns managed rows (from manifest) with per-adapter status,
// and unmanaged entries (from List()) not present in the manifest, keyed by
// agent ID. Unmanaged entries whose name matches an installed plugin on that
// agent are suppressed (see installedPluginNames) — the plugin manages them,
// so offering to claim them into the manifest would create a duplicate.
// Manifest entries shadowed by a plugin are kept but flagged via
// ShadowedByPlugin/McpStatusShadowed rather than hidden.
func (a *App) McpServerRows(ctx context.Context) (managed []McpServerRow, unmanaged map[string][]InstalledMcpServer, err error) {
	cfg, loadErr := a.loadConfig()
	if loadErr != nil {
		return nil, nil, loadErr
	}
	installedByAgent := make(map[string]map[string]InstalledMcpServer)
	unmanaged = make(map[string][]InstalledMcpServer)
	for _, adapter := range a.mcpAdapters() {
		if !adapter.Available() {
			continue
		}
		listed, listErr := adapter.List(ctx)
		if listErr != nil {
			continue
		}
		byName := make(map[string]InstalledMcpServer, len(listed))
		for _, s := range listed {
			byName[s.Name] = s
		}
		installedByAgent[adapter.ID()] = byName
	}
	// display-only builder: shadow warnings surface via RestoreSkills
	pluginNames, _ := installedPluginNames(ctx, a)
	manifestNames := make(map[string]struct{}, len(cfg.Agents.McpServers))
	for _, s := range cfg.Agents.McpServers {
		manifestNames[s.Name] = struct{}{}
		row := McpServerRow{
			Name:           s.Name,
			Transport:      s.Transport,
			Command:        s.Command,
			URL:            s.URL,
			Version:        extractMcpPinnedVersion(s.Command),
			Groups:         mcpGroupsForName(cfg, s.Name),
			Agents:         append([]string(nil), s.Agents...),
			PerAgentStatus: make(map[string]McpStatus),
		}
		for _, adapter := range a.mcpAdapters() {
			if !adapter.Available() {
				row.PerAgentStatus[adapter.ID()] = McpStatusAgentUnavailable
				continue
			}
			byName, ok := installedByAgent[adapter.ID()]
			if _, found := byName[s.Name]; ok && found {
				row.PerAgentStatus[adapter.ID()] = McpStatusInstalled
				continue
			}
			if pluginShadowsName(pluginNames, adapter.ID(), s.Name) {
				row.PerAgentStatus[adapter.ID()] = McpStatusShadowed
				row.ShadowedByPlugin = true
				continue
			}
			row.PerAgentStatus[adapter.ID()] = McpStatusMissing
		}
		managed = append(managed, row)
	}
	for _, adapter := range a.mcpAdapters() {
		byName, ok := installedByAgent[adapter.ID()]
		if !ok {
			continue
		}
		for name, srv := range byName {
			if _, inManifest := manifestNames[name]; inManifest {
				continue
			}
			if pluginShadowsName(pluginNames, adapter.ID(), name) {
				continue
			}
			unmanaged[adapter.ID()] = append(unmanaged[adapter.ID()], srv)
		}
	}
	return managed, unmanaged, nil
}

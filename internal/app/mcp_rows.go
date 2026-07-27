package app

import (
	"context"
	"fmt"

	"github.com/lkshrk/omni/internal/agent"
)

type McpStatus string

const (
	McpStatusInstalled        McpStatus = "installed"
	McpStatusMissing          McpStatus = "missing"
	McpStatusAgentUnavailable McpStatus = "agent-unavailable"
	// Plugin-provided servers never appear in an agent's user-scope config, so this is not a real gap.
	McpStatusShadowed McpStatus = "shadowed"
	// Restore leaves it alone, so it needs an explicit decision rather than another converge pass.
	McpStatusDrifted McpStatus = "drifted"
)

// Carried by every mcp drift report so the verb that settles it travels with the finding.
const mcpDriftRemedy = `"omni agents mcp resolve" (--use-managed / --use-local)`

type McpServerRow struct {
	Name           string
	Transport      string
	Command        string
	URL            string
	Version        string
	Groups         []string
	Agents         []string
	PerAgentStatus map[string]McpStatus
	// Kept, not hidden, because it is declared intent; RestoreMcpServers skips installing it.
	ShadowedByPlugin bool
	Drifted          bool
	// So a report can say what diverged instead of only that something did.
	DriftFields map[string][]string
	// Adopting the live side otherwise asks the user to accept a value they cannot see.
	DriftLive map[string]InstalledMcpServer
}

// McpServerRows — Unmanaged entries matching an installed plugin are suppressed; manifest entries are flagged instead of hidden.
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
			return nil, nil, fmt.Errorf("list mcp servers for %s: %w", adapter.ID(), listErr)
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
			Version:        agent.ExtractMcpPinnedVersion(s.Command),
			Groups:         mcpGroupsForName(cfg, s.Name),
			Agents:         append([]string(nil), s.Agents...),
			PerAgentStatus: make(map[string]McpStatus),
		}
		for _, adapter := range a.mcpAdapters() {
			// Restore and "mcp resolve" both ignore untargeted adapters, so reporting drift there would be a finding no verb can clear.
			if !serverTargetsAdapter(s, adapter.ID()) {
				continue
			}
			if !adapter.Available() {
				row.PerAgentStatus[adapter.ID()] = McpStatusAgentUnavailable
				continue
			}
			byName, ok := installedByAgent[adapter.ID()]
			if live, found := byName[s.Name]; ok && found {
				if fields := mcpIdentityDrift(s, live); len(fields) > 0 {
					row.PerAgentStatus[adapter.ID()] = McpStatusDrifted
					row.Drifted = true
					if row.DriftFields == nil {
						row.DriftFields = make(map[string][]string)
						row.DriftLive = make(map[string]InstalledMcpServer)
					}
					row.DriftFields[adapter.ID()] = fields
					row.DriftLive[adapter.ID()] = live
					continue
				}
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
	a.cacheAgentsRowsSectionBestEffort(ctx, agentsRowsCacheMcpKey, cachedMcpRows{Rows: managed, Unmanaged: unmanaged})
	return managed, unmanaged, nil
}

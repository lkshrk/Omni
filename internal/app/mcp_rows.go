package app

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

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

// Carried instead wherever APM owns the registration, which no omni verb rewrites.
const mcpAPMDriftRemedy = `"omni agents sync"`

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
	// Agents APM deploys this server to. Global MCP has no per-server scoping, so this is the whole set of
	// this host's APM targets whenever the server reaches APM at all.
	APMAgents []string
	// APM agents the config never declared but APM reaches anyway; empty means declared and deployed agree.
	UndeclaredAPMAgents []string
}

// APMManaged — drift on these agents is a report, not a task: APM rewrites them on the next sync.
func (r McpServerRow) APMManaged(agentID string) bool {
	return slices.Contains(r.APMAgents, agentID)
}

// DriftAdvice names what diverged and the verb that settles it. APM-owned registrations get the sync that
// reconciles them rather than a resolve verb that cannot reach them.
func (r McpServerRow) DriftAdvice(agentID string) string {
	fields := r.DriftFields[agentID]
	if len(fields) == 0 {
		return ""
	}
	if r.APMManaged(agentID) {
		return fmt.Sprintf("drifted on %s; the next %s reconciles it", strings.Join(fields, ", "), mcpAPMDriftRemedy)
	}
	return fmt.Sprintf("drifted on %s; resolve with %s", strings.Join(fields, ", "), mcpDriftRemedy)
}

// NativeDriftAgents lists the drifted agents "omni agents mcp resolve" can actually settle.
func (r McpServerRow) NativeDriftAgents() []string {
	var out []string
	for id := range r.DriftFields {
		if !r.APMManaged(id) {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// McpServerRows — Unmanaged entries matching an installed plugin are suppressed; manifest entries are flagged instead of hidden.
func (a *App) McpServerRows(ctx context.Context) (managed []McpServerRow, unmanaged map[string][]InstalledMcpServer, err error) {
	cfg, loadErr := a.loadConfig()
	if loadErr != nil {
		return nil, nil, loadErr
	}
	apmAgentIDs := a.apmMcpAgentIDs(cfg)
	apmDeployed, apmErr := a.apmDeployedMcpServers(apmAgentIDs)
	if apmErr != nil {
		return nil, nil, apmErr
	}
	apmVersions, versionErr := a.apmMcpLockVersions()
	if versionErr != nil {
		return nil, nil, versionErr
	}
	claudeState := a.claudeStateForAPMAgents(apmAgentIDs)
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
			Version:        mcpRowVersion(apmVersions[s.Name], s.Command),
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
					row.markDrift(adapter.ID(), fields, live)
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
		// One entry declaring any APM agent reaches every one of them, so the declared list is reported as
		// intent while the effective deployment is what the row shows.
		if slices.ContainsFunc(apmAgentIDs, func(id string) bool { return serverTargetsAdapter(s, id) }) {
			row.APMAgents = append([]string(nil), apmAgentIDs...)
			for _, agentID := range apmAgentIDs {
				if len(s.Agents) > 0 && !slices.Contains(s.Agents, agentID) {
					row.UndeclaredAPMAgents = append(row.UndeclaredAPMAgents, agentID)
				}
				live, present := claudeState[s.Name]
				switch {
				// Only claude's own config states what is deployed; APM's lockfile records presence alone.
				case agentID == claudeAgentID && present:
					if fields := mcpIdentityDrift(s, live); len(fields) > 0 {
						row.markDrift(agentID, fields, live)
						continue
					}
					row.PerAgentStatus[agentID] = McpStatusInstalled
				case apmDeployed[agentID][s.Name]:
					row.PerAgentStatus[agentID] = McpStatusInstalled
				case pluginShadowsName(pluginNames, agentID, s.Name):
					row.PerAgentStatus[agentID] = McpStatusShadowed
					row.ShadowedByPlugin = true
				default:
					row.PerAgentStatus[agentID] = McpStatusMissing
				}
			}
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
	if claudeUnmanaged := claudeUnmanagedMcpServers(claudeState, manifestNames, pluginNames); len(claudeUnmanaged) > 0 {
		unmanaged[claudeAgentID] = append(unmanaged[claudeAgentID], claudeUnmanaged...)
	}
	a.cacheAgentsRowsSectionBestEffort(ctx, agentsRowsCacheMcpKey, cachedMcpRows{Rows: managed, Unmanaged: unmanaged})
	return managed, unmanaged, nil
}

func (r *McpServerRow) markDrift(agentID string, fields []string, live InstalledMcpServer) {
	r.PerAgentStatus[agentID] = McpStatusDrifted
	r.Drifted = true
	if r.DriftFields == nil {
		r.DriftFields = make(map[string][]string)
		r.DriftLive = make(map[string]InstalledMcpServer)
	}
	r.DriftFields[agentID] = fields
	r.DriftLive[agentID] = live
}

// The locked version describes what APM installed; the manifest pin is all there is to go on before a
// first install, and neither existing means the row has no version to show.
func mcpRowVersion(locked, command string) string {
	if locked != "" {
		return locked
	}
	return agent.ExtractMcpPinnedVersion(command)
}

func claudeUnmanagedMcpServers(
	state map[string]InstalledMcpServer,
	manifestNames map[string]struct{},
	pluginNames map[string]map[string]bool,
) []InstalledMcpServer {
	names := make([]string, 0, len(state))
	for name := range state {
		if _, inManifest := manifestNames[name]; inManifest {
			continue
		}
		if pluginShadowsName(pluginNames, claudeAgentID, name) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]InstalledMcpServer, 0, len(names))
	for _, name := range names {
		out = append(out, state[name])
	}
	return out
}

package app

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/config"
)

type McpDriftStrategy string

const (
	McpDriftUseManaged McpDriftStrategy = "use_managed"
	McpDriftUseLocal   McpDriftStrategy = "use_local"
)

// ResolveMcpDriftOptions — Agents defaults to every agent the server is drifted on.
type ResolveMcpDriftOptions struct {
	Name     string
	Agents   []string
	Strategy McpDriftStrategy
	DryRun   bool
}

type McpDriftResolution struct {
	Name     string
	Agents   []string
	Actions  []string
	Warnings []string
}

// ResolveMcpDrift — UseLocal can adopt the live definition because an MCP server is pure configuration, unlike a skill package whose content upstream owns.
func (a *App) ResolveMcpDrift(ctx context.Context, opts ResolveMcpDriftOptions) (McpDriftResolution, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return McpDriftResolution{}, err
	}
	if err := a.requireMcpEnabled(cfg); err != nil {
		return McpDriftResolution{}, err
	}
	name := strings.TrimSpace(opts.Name)
	target := findMcpServer(cfg.Agents.McpServers, name)
	if target == nil {
		return McpDriftResolution{}, fmt.Errorf("mcp server %q is not in this host's manifest", name)
	}
	live, synced, err := a.mcpRegistrationsByState(ctx, *target)
	if err != nil {
		return McpDriftResolution{}, err
	}
	selected, err := driftedMcpResolveTargets(*target, a.mcpAdapters(), live, opts.Agents)
	if err != nil {
		return McpDriftResolution{}, err
	}
	res := McpDriftResolution{Name: name, Agents: selected}
	if opts.Strategy == McpDriftUseLocal {
		return a.resolveMcpDriftUseLocal(res, *target, selected, live, synced, opts.DryRun)
	}
	return a.resolveMcpDriftUseManaged(ctx, res, *target, selected, live, opts.DryRun)
}

// The in-sync side matters as much as the drifted one: adopting a local definition would push every agent that already matches the manifest into drift.
func (a *App) mcpRegistrationsByState(ctx context.Context, target config.McpServer) (drifted, synced map[string]InstalledMcpServer, err error) {
	drifted = make(map[string]InstalledMcpServer)
	synced = make(map[string]InstalledMcpServer)
	for _, adapter := range a.mcpAdapters() {
		if !serverTargetsAdapter(target, adapter.ID()) || !adapter.Available() {
			continue
		}
		installed, listErr := adapter.List(ctx)
		if listErr != nil {
			return nil, nil, fmt.Errorf("list mcp servers for %s: %w", adapter.ID(), listErr)
		}
		previous, present := installedMcpServer(installed, target.Name)
		if !present {
			continue
		}
		if len(mcpIdentityDrift(target, previous)) == 0 {
			synced[adapter.ID()] = previous
			continue
		}
		drifted[adapter.ID()] = previous
	}
	return drifted, synced, nil
}

func driftedMcpResolveTargets(
	target config.McpServer,
	adapters []McpAdapter,
	live map[string]InstalledMcpServer,
	requested []string,
) ([]string, error) {
	if len(requested) == 0 {
		drifted := make([]string, 0, len(live))
		for id := range live {
			drifted = append(drifted, id)
		}
		if len(drifted) == 0 {
			return nil, fmt.Errorf("mcp server %s is not drifted on any target agent", target.Name)
		}
		sort.Strings(drifted)
		return drifted, nil
	}
	known := make(map[string]bool, len(adapters))
	for _, adapter := range adapters {
		known[adapter.ID()] = true
	}
	var out []string
	for _, id := range requested {
		if !known[id] || !serverTargetsAdapter(target, id) {
			return nil, fmt.Errorf("mcp server %s does not target agent %q", target.Name, id)
		}
		if _, drifted := live[id]; !drifted {
			return nil, fmt.Errorf("mcp server %s is not drifted on %s", target.Name, id)
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (a *App) resolveMcpDriftUseManaged(
	ctx context.Context,
	res McpDriftResolution,
	target config.McpServer,
	selected []string,
	live map[string]InstalledMcpServer,
	dryRun bool,
) (McpDriftResolution, error) {
	byID := make(map[string]McpAdapter)
	for _, adapter := range a.mcpAdapters() {
		byID[adapter.ID()] = adapter
	}
	for _, id := range selected {
		res.Actions = append(res.Actions, fmt.Sprintf(
			"reinstall %s on %s from the manifest (%s replaced)",
			target.Name, id, strings.Join(mcpIdentityDrift(target, live[id]), ", ")))
	}
	if dryRun {
		return res, nil
	}
	for _, id := range selected {
		adapter, ok := byID[id]
		if !ok {
			res.Warnings = append(res.Warnings, fmt.Sprintf("agent %s is no longer registered, skipping", id))
			continue
		}
		if err := updateMcpServer(ctx, adapter, target, live[id]); err != nil {
			return res, fmt.Errorf("resolving %s on %s: %w", target.Name, id, err)
		}
	}
	return res, nil
}

// Identity fields only: the live side cannot report the manifest's Env names, so a wholesale copy would drop the secret wiring.
func (a *App) resolveMcpDriftUseLocal(
	res McpDriftResolution,
	target config.McpServer,
	selected []string,
	live, synced map[string]InstalledMcpServer,
	dryRun bool,
) (McpDriftResolution, error) {
	adopted := live[selected[0]]
	for _, id := range selected[1:] {
		if mcpDefinitionsConflict(live[id], adopted) {
			return res, fmt.Errorf(
				"mcp server %q is drifted differently on %s; resolve one agent at a time with --agent",
				target.Name, strings.Join(selected, ", "))
		}
	}
	// One manifest entry carries the identity for every target agent, so adopting here would only move the drift onto the agents that still match — and the next run would move it back.
	inSync := make([]string, 0, len(synced))
	for id := range synced {
		inSync = append(inSync, id)
	}
	sort.Strings(inSync)
	if len(inSync) > 0 {
		return res, fmt.Errorf(
			"mcp server %q already matches the manifest on %s; adopting %s's definition would drift %s instead — "+
				"push the manifest out with \"--use-managed\", or give the agents separate manifest entries",
			target.Name, strings.Join(inSync, ", "), strings.Join(selected, ", "), strings.Join(inSync, ", "))
	}
	updated := target
	updated.Transport = adopted.Transport
	updated.Command = adopted.Command
	updated.URL = adopted.URL
	res.Actions = append(res.Actions, fmt.Sprintf(
		"adopt %s's live definition on %s into the manifest (%s)",
		target.Name, strings.Join(selected, ", "), strings.Join(mcpIdentityDrift(target, adopted), ", ")))
	if dryRun {
		return res, nil
	}
	err := a.withConfig(func(c *config.RootConfig) error {
		if findMcpServer(c.Agents.McpServers, target.Name) == nil {
			return fmt.Errorf("mcp server %q not found", target.Name)
		}
		c.Agents.McpServers = upsertMcpServer(c.Agents.McpServers, updated)
		return nil
	})
	return res, err
}

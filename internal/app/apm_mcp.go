package app

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lkshrk/omni/internal/agent"
	"github.com/lkshrk/omni/internal/apm"
	"github.com/lkshrk/omni/internal/config"
)

// APM targets that have an MCP client adapter (apm_cli.factory.ClientFactory, 0.28.0). Selecting a target outside this set turns an MCP-bearing manifest into an exit-1 install rather than a benign skip.
var apmMcpCapableTargets = map[string]bool{
	"antigravity": true,
	"claude":      true,
	"codex":       true,
	"copilot":     true,
	"cursor":      true,
	"gemini":      true,
	"hermes":      true,
	"intellij":    true,
	"kiro":        true,
	"opencode":    true,
	"vscode":      true,
	"windsurf":    true,
}

// Codex drags its server set into every other target's config, and APM's hermes adapter writes nothing on removal, orphaning the server and its credential permanently.
var nativeOnlyMcpAgents = map[string]bool{
	"codex":        true,
	"hermes-agent": true,
}

// apmMcpPlan — Servers deploy to every entry in Targets; APM has no per-server scoping at global scope.
type apmMcpPlan struct {
	Servers []config.McpServer
	Targets []string
	// EnvRefs are the variables the deployed servers resolve at runtime, scrubbed from the install's environment so APM cannot bake their values into an agent config.
	EnvRefs    []string
	AgentIDs   []string
	NativeIDs  []string
	Warnings   []string
	NativeWork bool
	Skip       bool
}

func (a *App) resolveAPMMcpPlan(cfg *config.RootConfig) apmMcpPlan {
	var plan apmMcpPlan
	if config.BoolVal(a.effectiveSettings(cfg).McpDisabled) {
		plan.Skip = true
		plan.Warnings = append(plan.Warnings, "mcp servers are disabled for this host, skipping sync")
		return plan
	}

	apmAgentIDs, targets, nativeIDs, unservedIDs := a.partitionMcpAgents(cfg)
	plan.AgentIDs, plan.Targets, plan.NativeIDs = apmAgentIDs, targets, nativeIDs

	_, ignored, _, _ := a.AgentsIgnoreSet(cfg)
	for _, server := range resolveMcpServers(cfg, currentMachineGroupName()) {
		if ignored[server.Name] {
			continue
		}
		if slices.ContainsFunc(nativeIDs, func(id string) bool { return serverTargetsAdapter(server, id) }) {
			plan.NativeWork = true
		}
		if !slices.ContainsFunc(apmAgentIDs, func(id string) bool { return serverTargetsAdapter(server, id) }) {
			continue
		}
		plan.Servers = append(plan.Servers, server)
		if w := mcpScopeDivergenceWarning(server, apmAgentIDs); w != "" {
			plan.Warnings = append(plan.Warnings, w)
		}
	}
	if len(unservedIDs) > 0 && (len(plan.Servers) > 0 || plan.NativeWork) {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf(
			"%s cannot receive mcp servers: APM has no mcp client for it and omni no longer registers them natively",
			strings.Join(unservedIDs, ", ")))
	}

	plan.EnvRefs = mcpEnvRefs(plan.Servers)
	// Advisory only: the scrubbed install deploys the placeholder either way, and the variable is the agent's to resolve at runtime.
	if unset := a.unsetEnvRefs(plan.EnvRefs); len(unset) > 0 {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf(
			"mcp servers reference variables this shell does not set: %s; the agent must have them at runtime",
			strings.Join(unset, ", ")))
	}
	return plan
}

// APM substitutes both forms, in every field it serializes into an agent config.
var mcpEnvRefPattern = regexp.MustCompile(`\$\{(?:env:)?([A-Za-z_][A-Za-z0-9_]*)\}`)

// mcpEnvRefs collects every variable the deployed servers resolve, across the name-list and each field carrying an inline reference.
func mcpEnvRefs(servers []config.McpServer) []string {
	var names []string
	add := func(name string) {
		if name = strings.TrimSpace(name); name != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	for _, server := range servers {
		for _, name := range server.Env {
			add(name)
		}
		values := slices.Concat([]string{server.URL},
			slices.Collect(maps.Values(server.EnvLiteral)), slices.Collect(maps.Values(server.Headers)))
		for _, value := range values {
			for _, match := range mcpEnvRefPattern.FindAllStringSubmatch(value, -1) {
				add(match[1])
			}
		}
	}
	sort.Strings(names)
	return names
}

func (a *App) unsetEnvRefs(names []string) []string {
	var unset []string
	for _, name := range names {
		if strings.TrimSpace(a.lookupEnv(name)) == "" {
			unset = append(unset, name)
		}
	}
	return unset
}

// partitionMcpAgents splits this host's agents into the ones APM drives, the ones on a native adapter, and the ones left without any MCP path.
func (a *App) partitionMcpAgents(cfg *config.RootConfig) (apmAgentIDs, targets, nativeIDs, unservedIDs []string) {
	native := make(map[string]bool)
	for _, adapter := range a.mcpAdapters() {
		native[adapter.ID()] = true
	}
	for _, id := range a.effectiveAgentTargets(cfg) {
		mapped, _ := migrateAPMTargets([]string{id})
		if !nativeOnlyMcpAgents[id] && len(mapped) > 0 && apmMcpCapableTargets[mapped[0]] {
			apmAgentIDs = append(apmAgentIDs, id)
			if !slices.Contains(targets, mapped[0]) {
				targets = append(targets, mapped[0])
			}
			continue
		}
		if native[id] {
			nativeIDs = append(nativeIDs, id)
			continue
		}
		unservedIDs = append(unservedIDs, id)
	}
	sort.Strings(apmAgentIDs)
	sort.Strings(targets)
	sort.Strings(nativeIDs)
	sort.Strings(unservedIDs)
	return apmAgentIDs, targets, nativeIDs, unservedIDs
}

func (a *App) apmMcpAgentIDs(cfg *config.RootConfig) []string {
	apmAgentIDs, _, _, _ := a.partitionMcpAgents(cfg)
	return apmAgentIDs
}

func mcpScopeDivergenceWarning(server config.McpServer, apmAgentIDs []string) string {
	if len(server.Agents) == 0 {
		return ""
	}
	var undeclared []string
	for _, id := range apmAgentIDs {
		if !slices.Contains(server.Agents, id) {
			undeclared = append(undeclared, id)
		}
	}
	if len(undeclared) == 0 {
		return ""
	}
	return fmt.Sprintf("server %q declares agents %s but APM deploys it to %s as well: global MCP has no per-server scoping",
		server.Name, strings.Join(server.Agents, ", "), strings.Join(undeclared, ", "))
}

// APM splits an invocation into command plus args, and env name-lists become ${env:VAR} references that APM resolves from the sync process environment at install time.
func apmMcpDependencyOf(server config.McpServer) apmMcpDependency {
	dep := apmMcpDependency{Name: server.Name, Transport: server.Transport}
	if urlMcpTransport(server.Transport) {
		dep.URL = server.URL
	} else if fields := strings.Fields(server.Command); len(fields) > 0 {
		dep.Command, dep.Args = fields[0], fields[1:]
	}
	if len(server.Env) > 0 || len(server.EnvLiteral) > 0 {
		dep.Env = make(map[string]string, len(server.Env)+len(server.EnvLiteral))
		for _, name := range server.Env {
			dep.Env[name] = "${env:" + name + "}"
		}
		maps.Copy(dep.Env, server.EnvLiteral)
	}
	if len(server.Headers) > 0 {
		dep.Headers = maps.Clone(server.Headers)
	}
	return dep
}

func apmMcpDependencies(servers []config.McpServer) []apmMcpDependency {
	deps := make([]apmMcpDependency, 0, len(servers))
	for _, server := range servers {
		deps = append(deps, apmMcpDependencyOf(server))
	}
	return deps
}

// restoreMcpPlaceholders is the backstop behind the scrubbed install: a variable that reached APM another way
// is resolved into the agent config, and only claude's is one omni can rewrite. Warnings name the keys, never the values.
func (a *App) restoreMcpPlaceholders(plan apmMcpPlan) ([]string, error) {
	if !slices.Contains(plan.AgentIDs, claudeAgentID) {
		return nil, nil
	}
	want := make(map[string]mcpPlaceholders, len(plan.Servers))
	for _, server := range plan.Servers {
		dep := apmMcpDependencyOf(server)
		want[server.Name] = mcpPlaceholders{headers: dep.Headers, env: dep.Env}
	}
	restored, err := a.claudeStateRestoreMcpPlaceholders(want)
	if err != nil || len(restored) == 0 {
		return nil, err
	}
	warnings := make([]string, 0, len(restored))
	for _, entry := range restored {
		warnings = append(warnings, fmt.Sprintf(
			"%s/%s was deployed with the variable expanded; restored the reference so the value stays out of the config",
			claudeAgentID, entry))
	}
	return warnings, nil
}

// apmDeployedMcpServers — the lockfile is absent whenever an install never succeeded, so a missing file reads as "nothing deployed" rather than an error.
func (a *App) apmDeployedMcpServers(agentIDs []string) (map[string]map[string]bool, error) {
	if len(agentIDs) == 0 {
		return nil, nil
	}
	manifestPath, err := a.globalAPMManifestPath()
	if err != nil {
		return nil, err
	}
	lock, err := readAPMMcpLock(manifestPath)
	if err != nil {
		return nil, err
	}
	byAgent := make(map[string]map[string]bool, len(agentIDs))
	for _, id := range agentIDs {
		mapped, _ := migrateAPMTargets([]string{id})
		if len(mapped) == 0 {
			continue
		}
		names := make(map[string]bool, len(lock.McpTargetServers[mapped[0]]))
		for _, name := range lock.McpTargetServers[mapped[0]] {
			names[name] = true
		}
		byAgent[id] = names
	}
	return byAgent, nil
}

// apmMcpLock mirrors the MCP section of apm.lock.yaml. Entries under mcp_configs carry whatever the resolver settled on, which is the only place a registry server's version is ever written down.
type apmMcpLock struct {
	McpServers       []any                       `yaml:"mcp_servers"`
	McpConfigs       map[string]apmLockMcpConfig `yaml:"mcp_configs"`
	McpTargetServers map[string][]string         `yaml:"mcp_target_servers"`
}

type apmLockMcpConfig struct {
	Version string   `yaml:"version"`
	Command string   `yaml:"command"`
	Args    []string `yaml:"args"`
}

// resolvedVersion — the locked version where the resolver recorded one, else the pin the deployed command carries; both describe what APM installed rather than what the config asked for.
func (c apmLockMcpConfig) resolvedVersion() string {
	if v := strings.TrimSpace(c.Version); v != "" {
		return v
	}
	return agent.ExtractMcpPinnedVersion(strings.Join(append([]string{c.Command}, c.Args...), " "))
}

func readAPMMcpLock(manifestPath string) (apmMcpLock, error) {
	var lock apmMcpLock
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(manifestPath), "apm.lock.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return lock, nil
		}
		return lock, fmt.Errorf("reading APM lockfile: %w", err)
	}
	if err := yaml.Unmarshal(raw, &lock); err != nil {
		return lock, fmt.Errorf("parsing APM lockfile: %w", err)
	}
	return lock, nil
}

// APM's first-contact idempotence check is name-based, so without this record a diverging entry omni wrote earlier is locked in as if it matched.
func apmMcpLockHasState(manifestPath string) (bool, error) {
	lock, err := readAPMMcpLock(manifestPath)
	if err != nil {
		return false, err
	}
	return len(lock.McpServers) > 0 || len(lock.McpConfigs) > 0 || len(lock.McpTargetServers) > 0, nil
}

// apmMcpLockVersions reports the version APM resolved for each deployed server, keyed by server name.
func (a *App) apmMcpLockVersions() (map[string]string, error) {
	manifestPath, err := a.globalAPMManifestPath()
	if err != nil {
		return nil, err
	}
	lock, err := readAPMMcpLock(manifestPath)
	if err != nil {
		return nil, err
	}
	versions := make(map[string]string, len(lock.McpConfigs))
	for name, cfg := range lock.McpConfigs {
		if v := cfg.resolvedVersion(); v != "" {
			versions[name] = v
		}
	}
	return versions, nil
}

// normalizeMcpForAPM clears earlier native registrations so the install writes every entry fresh instead of adopting a stale one.
func (a *App) normalizeMcpForAPM(ctx context.Context, plan apmMcpPlan, dryRun bool) ([]string, error) {
	byID := make(map[string]McpAdapter, len(plan.AgentIDs))
	for _, adapter := range a.mcpAdapters() {
		if slices.Contains(plan.AgentIDs, adapter.ID()) {
			byID[adapter.ID()] = adapter
		}
	}
	var actions []string
	for _, id := range plan.AgentIDs {
		adapter, ok := byID[id]
		if !ok {
			cleared, err := a.normalizeAdapterlessMcp(id, plan, dryRun)
			actions = append(actions, cleared...)
			if err != nil {
				return actions, err
			}
			continue
		}
		if !adapter.Available() {
			continue
		}
		installed, err := adapter.List(ctx)
		if err != nil {
			return actions, fmt.Errorf("list mcp servers for %s: %w", id, err)
		}
		for _, server := range plan.Servers {
			if !mcpServerListed(installed, server.Name) {
				continue
			}
			actions = append(actions, fmt.Sprintf("%s/%s: clearing the natively installed server so APM writes it fresh", id, server.Name))
			if dryRun {
				continue
			}
			if err := adapter.Remove(ctx, server.Name); err != nil {
				return actions, fmt.Errorf("clearing %s from %s before the APM install: %w", server.Name, id, err)
			}
		}
	}
	return actions, nil
}

// Only claude qualifies: it is the one adapterless APM-driven agent omni ever wrote MCP entries to.
func (a *App) normalizeAdapterlessMcp(agentID string, plan apmMcpPlan, dryRun bool) ([]string, error) {
	if agentID != claudeAgentID {
		return nil, nil
	}
	state := a.claudeStateForAPMAgents(plan.AgentIDs)
	names := make([]string, 0, len(plan.Servers))
	for _, server := range plan.Servers {
		if _, ok := state[server.Name]; ok {
			names = append(names, server.Name)
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	actions := make([]string, 0, len(names))
	for _, name := range names {
		actions = append(actions, fmt.Sprintf("%s/%s: clearing the natively installed server so APM writes it fresh", agentID, name))
	}
	if dryRun {
		return actions, nil
	}
	if _, err := a.claudeStateRemoveMcpServers(names); err != nil {
		return actions, fmt.Errorf("clearing %s from %s before the APM install: %w", strings.Join(names, ", "), agentID, err)
	}
	return actions, nil
}

// apmDrivesMcpServer reports whether the generated manifest, rather than a surviving native adapter, owns this server's deployment on this host.
func (a *App) apmDrivesMcpServer(cfg *config.RootConfig, server config.McpServer) bool {
	return slices.ContainsFunc(a.apmMcpAgentIDs(cfg), func(id string) bool { return serverTargetsAdapter(server, id) })
}

// APM has no removal verb, so pruning a narrowed server means reinstalling the surface without it; retired names an entry the config no longer declares.
func (a *App) convergeAPMMcpSurface(ctx context.Context, retired string) error {
	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	plan := a.resolveAPMMcpPlan(cfg)
	if plan.Skip {
		return nil
	}
	manifestPath, err := a.globalAPMManifestPath()
	if err != nil {
		return err
	}
	preState, err := a.snapshotAPMState(plan.Targets)
	if err != nil {
		return err
	}
	converged, err := a.convergeAPMManifest(cfg, manifestPath, apmConvergePlan{
		Mcp:        apmMcpDependencies(plan.Servers),
		SyncMcp:    true,
		RetiredMcp: retired,
	})
	if err != nil {
		return err
	}
	if len(plan.Targets) == 0 {
		return nil
	}
	if !a.APMAvailable() {
		return errAPMNotInstalled()
	}
	_, reverted, err := a.installAPMSurface(ctx, a.APMClient(apm.Global), apm.SurfaceMcp, plan.Targets, &preState,
		apm.InstallOptions{ScrubEnv: plan.EnvRefs})
	if err != nil {
		if len(reverted) > 0 {
			return fmt.Errorf("%w\n%s", err, strings.Join(reverted, "\n"))
		}
		return err
	}
	if err := advanceAPMApplied(manifestPath, apm.SurfaceMcp, converged.PendingOwned.Mcp); err != nil {
		return err
	}
	// This verb reports through its error only, so a restored placeholder is silent; the rewrite is what keeps the value off disk.
	_, err = a.restoreMcpPlaceholders(plan)
	return err
}

func managedMcpIdentities(cfg *config.RootConfig) map[string]bool {
	managed := make(map[string]bool, len(cfg.Agents.McpServers))
	for _, server := range cfg.Agents.McpServers {
		managed[strings.TrimSpace(server.Name)] = true
	}
	return managed
}

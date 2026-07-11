package app

import (
	"context"
	"fmt"
	"os"

	"github.com/lkshrk/omni/internal/config"
)

// RestoreMcpOptions controls a restore run.
type RestoreMcpOptions struct {
	DryRun bool
}

// RestoreMcpResult summarizes a restore run.
type RestoreMcpResult struct {
	Installed        []string
	AlreadyInstalled []string
	Skipped          []string // servers not targeted at a given adapter
	// ShadowedByPlugin lists "adapterID/name" servers skipped because an
	// installed plugin of the same name already provides them — a user-scope
	// duplicate of a plugin-provided server is harm, not repair (see
	// McpStatusShadowed).
	ShadowedByPlugin []string
	WouldInstall     []string // populated only when DryRun=true; servers that would be installed
	Warnings         []string
	Errors           []McpServerError
}

// McpServerError is a non-fatal per-server failure during restore.
type McpServerError struct {
	AgentID    string
	ServerName string
	Err        error
}

func (e McpServerError) Error() string {
	return fmt.Sprintf("agent %s / server %s: %v", e.AgentID, e.ServerName, e.Err)
}

// McpImportDiff lists servers detected by agent CLIs not present in the manifest.
type McpImportDiff struct {
	Unmanaged map[string][]InstalledMcpServer // keyed by agent ID
}

// WithMcpAdapters replaces the production adapter list for testing.
func WithMcpAdapters(adapters []McpAdapter) func(*App) {
	return func(a *App) { a.testMcpAdapters = adapters }
}

func (a *App) mcpAdapters() []McpAdapter {
	if a.testMcpAdapters != nil {
		return a.testMcpAdapters
	}
	exec := a.fallbackExecutor().Run
	return []McpAdapter{
		NewClaudeCodeMcpAdapter(exec, os.LookupEnv),
		NewCodexMcpAdapter(exec, os.LookupEnv),
		NewGrokMcpAdapter(exec, os.LookupEnv),
	}
}

// serverTargetsAdapter reports whether s should be applied to adapterID.
// An empty Agents list means all adapters.
func serverTargetsAdapter(s config.McpServer, adapterID string) bool {
	if len(s.Agents) == 0 {
		return true
	}
	for _, id := range s.Agents {
		if id == adapterID {
			return true
		}
	}
	return false
}

// mcpServerListed reports whether name appears in an adapter's List() output.
func mcpServerListed(installed []InstalledMcpServer, name string) bool {
	for _, s := range installed {
		if s.Name == name {
			return true
		}
	}
	return false
}

// resolveMcpServers returns the servers active for groupName.
// Servers with no GroupConfig.McpServers membership restore on all hosts.
// Servers listed in a GroupConfig.McpServers only restore when that group is active.
func resolveMcpServers(cfg *config.RootConfig, groupName string) []config.McpServer {
	groupedNames := make(map[string]struct{})
	activeNames := make(map[string]struct{})
	for _, g := range cfg.Groups {
		if g == nil {
			continue
		}
		for _, name := range g.McpServers {
			groupedNames[name] = struct{}{}
			if g.Name == groupName {
				activeNames[name] = struct{}{}
			}
		}
	}
	var out []config.McpServer
	for _, s := range cfg.Agents.McpServers {
		if _, grouped := groupedNames[s.Name]; !grouped {
			out = append(out, s)
			continue
		}
		if _, active := activeNames[s.Name]; active {
			out = append(out, s)
		}
	}
	return out
}

// RestoreMcpServers installs manifest MCP servers into each targeted agent CLI.
func (a *App) RestoreMcpServers(ctx context.Context, opts RestoreMcpOptions) (RestoreMcpResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return RestoreMcpResult{}, err
	}
	if err := a.requireAgentsEnabled(cfg); err != nil {
		return RestoreMcpResult{}, err
	}
	if config.BoolVal(a.effectiveSettings(cfg).McpDisabled) {
		return RestoreMcpResult{Warnings: []string{"mcp servers are disabled for this host, skipping restore"}}, nil
	}
	servers := resolveMcpServers(cfg, currentMachineGroupName())
	pluginNames := installedPluginNames(ctx, a)
	var res RestoreMcpResult
	for _, adapter := range a.mcpAdapters() {
		if !adapter.Available() {
			res.Warnings = append(res.Warnings, fmt.Sprintf("agent %s not available, skipping", adapter.ID()))
			continue
		}
		installed, listErr := adapter.List(ctx)
		if listErr != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("agent %s: list mcp servers failed, attempting installs: %v", adapter.ID(), listErr))
			installed = nil
		}
		alreadyInstalled := make(map[string]struct{}, len(installed))
		for _, is := range installed {
			alreadyInstalled[is.Name] = struct{}{}
		}
		for _, s := range servers {
			if !serverTargetsAdapter(s, adapter.ID()) {
				res.Skipped = append(res.Skipped, adapter.ID()+"/"+s.Name)
				continue
			}
			if _, present := alreadyInstalled[s.Name]; present {
				res.AlreadyInstalled = append(res.AlreadyInstalled, adapter.ID()+"/"+s.Name)
				continue
			}
			if pluginShadowsName(pluginNames, adapter.ID(), s.Name) {
				res.ShadowedByPlugin = append(res.ShadowedByPlugin, adapter.ID()+"/"+s.Name)
				continue
			}
			if opts.DryRun {
				res.WouldInstall = append(res.WouldInstall, adapter.ID()+"/"+s.Name)
				continue
			}
			if addErr := adapter.Add(ctx, s); addErr != nil {
				res.Errors = append(res.Errors, McpServerError{AgentID: adapter.ID(), ServerName: s.Name, Err: addErr})
				continue
			}
			res.Installed = append(res.Installed, adapter.ID()+"/"+s.Name)
		}
	}
	return res, nil
}

// AddMcpResult reports per-adapter outcomes of an AddMcpServer call. The manifest
// write itself is unconditional on adapter outcome: it is the source of intent, so a
// live-install failure on one adapter must not block persisting the add.
type AddMcpResult struct {
	Errors           []McpServerError
	AlreadyInstalled []string
}

// RemoveMcpResult reports per-adapter outcomes of a RemoveMcpServer call.
type RemoveMcpResult struct {
	Errors []McpServerError
}

// AddMcpServer registers a server in the manifest and installs it via targeted agent CLIs.
// Every targeted, available adapter is attempted regardless of earlier adapter failures;
// the manifest is upserted afterward so live-install failures never leave the manifest
// out of sync with the caller's intent (see RemoveMcpServer for the mirrored remove case).
// An adapter that already reports s.Name present is skipped rather than re-added: neither
// `claude mcp add` nor `codex mcp add` is documented as idempotent on an existing name, and
// this path is reachable with an already-installed adapter whenever a caller targets an
// unmanaged server found on multiple agents (e.g. claiming it from the TUI/CLI unmanaged list).
func (a *App) AddMcpServer(ctx context.Context, s config.McpServer) (AddMcpResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return AddMcpResult{}, err
	}
	if err := a.requireMcpEnabled(cfg); err != nil {
		return AddMcpResult{}, err
	}
	var res AddMcpResult
	for _, adapter := range a.mcpAdapters() {
		if !serverTargetsAdapter(s, adapter.ID()) || !adapter.Available() {
			continue
		}
		if installed, listErr := adapter.List(ctx); listErr == nil {
			if mcpServerListed(installed, s.Name) {
				res.AlreadyInstalled = append(res.AlreadyInstalled, adapter.ID()+"/"+s.Name)
				continue
			}
		}
		if addErr := adapter.Add(ctx, s); addErr != nil {
			res.Errors = append(res.Errors, McpServerError{AgentID: adapter.ID(), ServerName: s.Name, Err: addErr})
		}
	}
	if err := a.withConfig(func(c *config.RootConfig) error {
		c.Agents.McpServers = upsertMcpServer(c.Agents.McpServers, s)
		return nil
	}); err != nil {
		return res, fmt.Errorf("installed %s but failed to save to manifest (re-run to persist): %w", s.Name, err)
	}
	return res, nil
}

// RemoveMcpServer unregisters a manifest server and removes it from targeted agent CLIs.
// Returns an error if the server is not in the manifest. As with AddMcpServer, every
// targeted adapter is attempted and the manifest deletion happens regardless of
// individual adapter failures.
func (a *App) RemoveMcpServer(ctx context.Context, name string) (RemoveMcpResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return RemoveMcpResult{}, err
	}
	if err := a.requireMcpEnabled(cfg); err != nil {
		return RemoveMcpResult{}, err
	}
	target := findMcpServer(cfg.Agents.McpServers, name)
	if target == nil {
		return RemoveMcpResult{}, fmt.Errorf("mcp server %q not found in manifest; omni only removes servers it added", name)
	}
	var res RemoveMcpResult
	for _, adapter := range a.mcpAdapters() {
		if !serverTargetsAdapter(*target, adapter.ID()) || !adapter.Available() {
			continue
		}
		if removeErr := adapter.Remove(ctx, name); removeErr != nil {
			res.Errors = append(res.Errors, McpServerError{AgentID: adapter.ID(), ServerName: name, Err: removeErr})
		}
	}
	if err := a.withConfig(func(c *config.RootConfig) error {
		c.Agents.McpServers = deleteMcpServer(c.Agents.McpServers, name)
		return nil
	}); err != nil {
		return res, fmt.Errorf("removed %s from agents but failed to update manifest (re-run to persist): %w", name, err)
	}
	return res, nil
}

// SetMcpServerAgents re-targets an existing manifest server's Agents list, installing
// it on newly-selected adapters and removing it from deselected ones. Adapters whose
// targeting is unchanged are left untouched. Per-adapter tolerant like AddMcpServer.
func (a *App) SetMcpServerAgents(ctx context.Context, name string, agents []string) (AddMcpResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return AddMcpResult{}, err
	}
	if err := a.requireMcpEnabled(cfg); err != nil {
		return AddMcpResult{}, err
	}
	target := findMcpServer(cfg.Agents.McpServers, name)
	if target == nil {
		return AddMcpResult{}, fmt.Errorf("mcp server %q not found in manifest", name)
	}
	updated := *target
	updated.Agents = agents

	var res AddMcpResult
	for _, adapter := range a.mcpAdapters() {
		if !adapter.Available() {
			continue
		}
		wasTargeted := serverTargetsAdapter(*target, adapter.ID())
		nowTargeted := serverTargetsAdapter(updated, adapter.ID())
		switch {
		case nowTargeted && !wasTargeted:
			if addErr := adapter.Add(ctx, updated); addErr != nil {
				res.Errors = append(res.Errors, McpServerError{AgentID: adapter.ID(), ServerName: name, Err: addErr})
			}
		case wasTargeted && !nowTargeted:
			if removeErr := adapter.Remove(ctx, name); removeErr != nil {
				res.Errors = append(res.Errors, McpServerError{AgentID: adapter.ID(), ServerName: name, Err: removeErr})
			}
		}
	}
	if err := a.withConfig(func(c *config.RootConfig) error {
		c.Agents.McpServers = upsertMcpServer(c.Agents.McpServers, updated)
		return nil
	}); err != nil {
		return res, fmt.Errorf("updated agents for %s but failed to save to manifest (re-run to persist): %w", name, err)
	}
	return res, nil
}

// McpServerByName returns the manifest entry for name, for callers (e.g. the
// TUI's per-agent install action) that need the full config.McpServer rather
// than the lossy McpServerRow projection (which drops Env/EnvLiteral).
func (a *App) McpServerByName(name string) (config.McpServer, bool, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return config.McpServer{}, false, err
	}
	target := findMcpServer(cfg.Agents.McpServers, name)
	if target == nil {
		return config.McpServer{}, false, nil
	}
	return *target, true, nil
}

func findMcpServer(servers []config.McpServer, name string) *config.McpServer {
	for i := range servers {
		if servers[i].Name == name {
			cp := servers[i]
			return &cp
		}
	}
	return nil
}

// ImportMcpServers returns servers installed in agent CLIs that are not in the manifest.
func (a *App) ImportMcpServers(ctx context.Context) (McpImportDiff, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return McpImportDiff{}, err
	}
	if err := a.requireMcpEnabled(cfg); err != nil {
		return McpImportDiff{}, err
	}
	managed := make(map[string]struct{}, len(cfg.Agents.McpServers))
	for _, s := range cfg.Agents.McpServers {
		managed[s.Name] = struct{}{}
	}
	diff := McpImportDiff{Unmanaged: make(map[string][]InstalledMcpServer)}
	for _, adapter := range a.mcpAdapters() {
		if !adapter.Available() {
			continue
		}
		listed, listErr := adapter.List(ctx)
		if listErr != nil {
			continue
		}
		for _, srv := range listed {
			if _, ok := managed[srv.Name]; !ok {
				diff.Unmanaged[adapter.ID()] = append(diff.Unmanaged[adapter.ID()], srv)
			}
		}
	}
	return diff, nil
}

// AdoptUnmanagedMcpServers folds ImportMcpServers' unmanaged results into the
// manifest by name only (Transport/Command/URL come from agent list output,
// which is the only shape available). This is manifest bookkeeping only,
// distinct from AddMcpServer: it never calls an agent CLI, since the servers
// are already installed there.
func (a *App) AdoptUnmanagedMcpServers(ctx context.Context) (int, error) {
	diff, err := a.ImportMcpServers(ctx)
	if err != nil {
		return 0, err
	}
	var adopted int
	err = a.withConfig(func(c *config.RootConfig) error {
		managed := make(map[string]struct{}, len(c.Agents.McpServers))
		for _, s := range c.Agents.McpServers {
			managed[s.Name] = struct{}{}
		}
		for agentID, servers := range diff.Unmanaged {
			for _, srv := range servers {
				if _, ok := managed[srv.Name]; ok {
					continue
				}
				managed[srv.Name] = struct{}{}
				c.Agents.McpServers = append(c.Agents.McpServers, config.McpServer{
					Name:      srv.Name,
					Transport: srv.Transport,
					Command:   srv.Command,
					URL:       srv.URL,
					Agents:    []string{agentID},
				})
				adopted++
			}
		}
		return nil
	})
	return adopted, err
}

func upsertMcpServer(servers []config.McpServer, s config.McpServer) []config.McpServer {
	for i := range servers {
		if servers[i].Name == s.Name {
			servers[i] = s
			return servers
		}
	}
	return append(servers, s)
}

func deleteMcpServer(servers []config.McpServer, name string) []config.McpServer {
	out := servers[:0]
	for _, s := range servers {
		if s.Name != name {
			out = append(out, s)
		}
	}
	return out
}

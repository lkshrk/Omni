package app

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"sort"
	"time"

	"github.com/lkshrk/omni/internal/config"
)

// RestoreMcpOptions controls a restore run.
type RestoreMcpOptions struct {
	DryRun bool
}

// RestoreMcpResult summarizes a restore run.
type RestoreMcpResult struct {
	Installed        []string
	Updated          []string
	AlreadyInstalled []string
	Skipped          []string // servers not targeted at a given adapter
	// ShadowedByPlugin lists "adapterID/name" servers skipped because an
	// installed plugin of the same name already provides them — a user-scope
	// duplicate of a plugin-provided server is harm, not repair (see
	// McpStatusShadowed).
	ShadowedByPlugin []string
	WouldInstall     []string // populated only when DryRun=true; servers that would be installed
	WouldUpdate      []string // populated only when DryRun=true; installed servers whose headers differ
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

func installedMcpServer(installed []InstalledMcpServer, name string) (InstalledMcpServer, bool) {
	for _, s := range installed {
		if s.Name == name {
			return s, true
		}
	}
	return InstalledMcpServer{}, false
}

func mcpHeadersDiffer(desired config.McpServer, installed InstalledMcpServer) bool {
	return installed.HeadersKnown && !maps.Equal(desired.Headers, installed.Headers)
}

func updateMcpServer(ctx context.Context, adapter McpAdapter, desired config.McpServer, previous InstalledMcpServer) error {
	if err := adapter.Remove(ctx, desired.Name); err != nil {
		return fmt.Errorf("remove before header update: %w", err)
	}
	if err := adapter.Add(ctx, desired); err != nil {
		rollback := config.McpServer{
			Name:       previous.Name,
			Transport:  previous.Transport,
			Command:    previous.Command,
			URL:        previous.URL,
			Headers:    previous.Headers,
			EnvLiteral: previous.EnvLiteral,
		}
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		if rollbackErr := adapter.Add(rollbackCtx, rollback); rollbackErr != nil {
			return fmt.Errorf("add updated headers: %w; restore previous registration: %v", err, rollbackErr)
		}
		return fmt.Errorf("add updated headers: %w (previous registration restored)", err)
	}
	return nil
}

// resolveMcpServers returns the servers active for hostname.
// Servers with no GroupConfig.McpServers membership restore on all hosts.
// Servers listed in a GroupConfig.McpServers restore when that group is
// active on hostname — either the host's own group or one of the groups
// assigned to it via cfg.Hosts, mirroring resolveSkillPackages.
func resolveMcpServers(cfg *config.RootConfig, hostname string) []config.McpServer {
	activeHostNames, _ := activeHostGroupNames(cfg, hostname)
	activeHostSet := make(map[string]struct{}, len(activeHostNames))
	for _, n := range activeHostNames {
		activeHostSet[n] = struct{}{}
	}

	groupedNames := make(map[string]struct{})
	activeNames := make(map[string]struct{})
	for _, g := range cfg.Groups {
		if g == nil {
			continue
		}
		for _, name := range g.McpServers {
			groupedNames[name] = struct{}{}
			if _, active := activeHostSet[g.BaseName()]; active {
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
	pluginNames, shadowWarnings := installedPluginNames(ctx, a)
	var res RestoreMcpResult
	res.Warnings = append(res.Warnings, shadowWarnings...)
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
		alreadyInstalled := make(map[string]InstalledMcpServer, len(installed))
		for _, is := range installed {
			alreadyInstalled[is.Name] = is
		}
		for _, s := range servers {
			if !serverTargetsAdapter(s, adapter.ID()) {
				res.Skipped = append(res.Skipped, adapter.ID()+"/"+s.Name)
				continue
			}
			if previous, present := alreadyInstalled[s.Name]; present {
				if !mcpHeadersDiffer(s, previous) {
					res.AlreadyInstalled = append(res.AlreadyInstalled, adapter.ID()+"/"+s.Name)
					continue
				}
				if opts.DryRun {
					res.WouldUpdate = append(res.WouldUpdate, adapter.ID()+"/"+s.Name)
					continue
				}
				if updateErr := updateMcpServer(ctx, adapter, s, previous); updateErr != nil {
					res.Errors = append(res.Errors, McpServerError{AgentID: adapter.ID(), ServerName: s.Name, Err: updateErr})
					continue
				}
				res.Updated = append(res.Updated, adapter.ID()+"/"+s.Name)
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
	Updated          []string
	// SkippedUnavailable lists adapter/server pairs whose agent CLI is not on
	// PATH: normal on multi-host manifests, an actionable warning on explicit installs.
	SkippedUnavailable []string
}

// RemoveMcpResult reports per-adapter outcomes of a RemoveMcpServer call.
type RemoveMcpResult struct {
	Errors             []McpServerError
	SkippedUnavailable []string
}

// AddMcpServer registers a server in the manifest and installs it via targeted agent CLIs.
// Every targeted, available adapter is attempted regardless of earlier adapter failures;
// the manifest is upserted afterward so live-install failures never leave the manifest
// out of sync with the caller's intent (see RemoveMcpServer for the mirrored remove case).
// An adapter that already reports s.Name present is skipped when its reported headers
// match, or replaced with rollback when they differ.
func (a *App) AddMcpServer(ctx context.Context, s config.McpServer) (AddMcpResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return AddMcpResult{}, err
	}
	if err := a.requireMcpEnabled(cfg); err != nil {
		return AddMcpResult{}, err
	}
	preview := &config.RootConfig{Agents: config.AgentsConfig{McpServers: []config.McpServer{s}}}
	if errs := fatalValidationErrors(config.ValidateRoot(preview, config.ProviderValidation{})); len(errs) > 0 {
		return AddMcpResult{}, config.ValidationErrors(errs)
	}
	var res AddMcpResult
	for _, adapter := range a.mcpAdapters() {
		if !serverTargetsAdapter(s, adapter.ID()) {
			continue
		}
		// listed check first: adopting an already-present server must not demand the CLI
		if installed, listErr := adapter.List(ctx); listErr == nil {
			if previous, present := installedMcpServer(installed, s.Name); present {
				if !mcpHeadersDiffer(s, previous) {
					res.AlreadyInstalled = append(res.AlreadyInstalled, adapter.ID()+"/"+s.Name)
					continue
				}
				if !adapter.Available() {
					res.SkippedUnavailable = append(res.SkippedUnavailable, adapter.ID()+"/"+s.Name)
					continue
				}
				if updateErr := updateMcpServer(ctx, adapter, s, previous); updateErr != nil {
					res.Errors = append(res.Errors, McpServerError{AgentID: adapter.ID(), ServerName: s.Name, Err: updateErr})
				} else {
					res.Updated = append(res.Updated, adapter.ID()+"/"+s.Name)
				}
				continue
			}
		}
		if !adapter.Available() {
			res.SkippedUnavailable = append(res.SkippedUnavailable, adapter.ID()+"/"+s.Name)
			continue
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
		if !serverTargetsAdapter(*target, adapter.ID()) {
			continue
		}
		if installed, listErr := adapter.List(ctx); listErr == nil && !mcpServerListed(installed, name) {
			continue
		}
		if !adapter.Available() {
			res.SkippedUnavailable = append(res.SkippedUnavailable, adapter.ID()+"/"+name)
			continue
		}
		if removeErr := adapter.Remove(ctx, name); removeErr != nil {
			res.Errors = append(res.Errors, McpServerError{AgentID: adapter.ID(), ServerName: name, Err: removeErr})
		}
	}
	if err := a.withConfig(func(c *config.RootConfig) error {
		c.Agents.McpServers = deleteMcpServer(c.Agents.McpServers, name)
		setMcpGroupsInConfig(c, name, map[string]struct{}{})
		return nil
	}); err != nil {
		return res, fmt.Errorf("removed %s from agents but failed to update manifest (re-run to persist): %w", name, err)
	}
	return res, nil
}

// SetMcpServerAgents re-targets an existing manifest server's Agents list and
// reconciles every selected adapter, installing it when missing and removing it when
// deselected. Per-adapter tolerant like AddMcpServer.
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
		wasTargeted := serverTargetsAdapter(*target, adapter.ID())
		nowTargeted := serverTargetsAdapter(updated, adapter.ID())
		switch {
		case nowTargeted:
			if installed, listErr := adapter.List(ctx); listErr == nil {
				if previous, present := installedMcpServer(installed, name); present {
					if !mcpHeadersDiffer(updated, previous) {
						res.AlreadyInstalled = append(res.AlreadyInstalled, adapter.ID()+"/"+name)
						continue
					}
					if !adapter.Available() {
						res.SkippedUnavailable = append(res.SkippedUnavailable, adapter.ID()+"/"+name)
						continue
					}
					if updateErr := updateMcpServer(ctx, adapter, updated, previous); updateErr != nil {
						res.Errors = append(res.Errors, McpServerError{AgentID: adapter.ID(), ServerName: name, Err: updateErr})
					} else {
						res.Updated = append(res.Updated, adapter.ID()+"/"+name)
					}
					continue
				}
			}
			if !adapter.Available() {
				res.SkippedUnavailable = append(res.SkippedUnavailable, adapter.ID()+"/"+name)
				continue
			}
			if addErr := adapter.Add(ctx, updated); addErr != nil {
				res.Errors = append(res.Errors, McpServerError{AgentID: adapter.ID(), ServerName: name, Err: addErr})
			}
		case wasTargeted && !nowTargeted:
			if !adapter.Available() {
				continue
			}
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
// manifest by name using the transport, command, URL, and headers reported by
// agent CLIs. Identical same-name registrations are adopted once and targeted
// at their union of agents; conflicting registrations abort without mutation.
// This is manifest bookkeeping only, distinct from AddMcpServer: it never calls
// an agent CLI, since the servers are already installed there.
func (a *App) AdoptUnmanagedMcpServers(ctx context.Context) (int, error) {
	diff, err := a.ImportMcpServers(ctx)
	if err != nil {
		return 0, err
	}
	type candidate struct {
		server InstalledMcpServer
		agents []string
	}
	candidates := make(map[string]candidate)
	agentIDs := make([]string, 0, len(diff.Unmanaged))
	for agentID := range diff.Unmanaged {
		agentIDs = append(agentIDs, agentID)
	}
	sort.Strings(agentIDs)
	for _, agentID := range agentIDs {
		for _, srv := range diff.Unmanaged[agentID] {
			current, exists := candidates[srv.Name]
			if !exists {
				candidates[srv.Name] = candidate{server: srv, agents: []string{agentID}}
				continue
			}
			if srv.Transport != current.server.Transport || srv.Command != current.server.Command || srv.URL != current.server.URL || !maps.Equal(srv.Headers, current.server.Headers) {
				return 0, fmt.Errorf("mcp server %q is unmanaged under multiple agents with conflicting configuration", srv.Name)
			}
			if !slices.Contains(current.agents, agentID) {
				current.agents = append(current.agents, agentID)
				candidates[srv.Name] = current
			}
		}
	}
	names := make([]string, 0, len(candidates))
	for name := range candidates {
		names = append(names, name)
	}
	sort.Strings(names)
	var adopted int
	err = a.withConfig(func(c *config.RootConfig) error {
		managed := make(map[string]struct{}, len(c.Agents.McpServers))
		for _, s := range c.Agents.McpServers {
			managed[s.Name] = struct{}{}
		}
		for _, name := range names {
			if _, ok := managed[name]; ok {
				continue
			}
			entry := candidates[name]
			managed[name] = struct{}{}
			c.Agents.McpServers = append(c.Agents.McpServers, config.McpServer{
				Name:      entry.server.Name,
				Transport: entry.server.Transport,
				Command:   entry.server.Command,
				URL:       entry.server.URL,
				Headers:   entry.server.Headers,
				Agents:    entry.agents,
			})
			adopted++
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

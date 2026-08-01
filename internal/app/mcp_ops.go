package app

import (
	"context"
	"fmt"
	"maps"
	"net/url"
	"os"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lkshrk/omni/internal/agent"
	"github.com/lkshrk/omni/internal/config"
)

type RestoreMcpOptions struct {
	DryRun bool
}

type RestoreMcpResult struct {
	Installed        []string
	Updated          []string
	AlreadyInstalled []string
	Skipped          []string // servers not targeted at a given adapter
	// A user-scope duplicate of a plugin-provided server is harm, not repair.
	ShadowedByPlugin []string
	WouldInstall     []string // populated only when DryRun=true; servers that would be installed
	WouldUpdate      []string // populated only when DryRun=true; installed servers whose headers differ
	// One user-facing line per pair whose live identity differs from the manifest's.
	Drift    []string
	Warnings []string
	Errors   []McpServerError
}

type McpServerError struct {
	AgentID    string
	ServerName string
	Err        error
}

func (e McpServerError) Error() string {
	return fmt.Sprintf("agent %s / server %s: %v", e.AgentID, e.ServerName, e.Err)
}

type McpImportDiff struct {
	Unmanaged map[string][]InstalledMcpServer // keyed by agent ID
}

func WithMcpAdapters(adapters []McpAdapter) func(*App) {
	return func(a *App) {
		var option agent.Option
		if adapters != nil {
			option = agent.WithMcpAdapters(adapters)
		}
		a.setAgentTargetOption(agentTargetsMcpOverride, option)
	}
}

func (a *App) initAgentTargets() {
	options := make([]agent.Option, 0, len(a.agentTargetOptions)+2)
	var overrides uint8
	for _, option := range a.agentTargetOptions {
		overrides |= option.capability
		options = append(options, option.option)
	}
	exec := func(ctx context.Context, name string, args ...string) (string, string, error) {
		return a.fallbackExecutor().Run(ctx, name, args...)
	}
	if overrides&agentTargetsMcpOverride == 0 {
		options = append(options, agent.WithDefaultMcpAdapters(exec, os.LookupEnv))
	}
	if overrides&agentTargetsPluginOverride == 0 {
		options = append(options, agent.WithDefaultPluginAdapters(exec, os.LookupEnv))
	}
	a.agentTargets = mustAgentRegistry(agent.NewRegistry(options...))
}

func (a *App) mcpAdapters() []McpAdapter {
	return a.agentRegistry().McpAdapters()
}

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

// Headers derive from rotating env vars and secrets, so the manifest stays authoritative and restore converges silently.
func mcpHeadersDiffer(desired config.McpServer, installed InstalledMcpServer) bool {
	return installed.HeadersKnown && !maps.Equal(desired.Headers, installed.Headers)
}

// Env is never identity: agents report one merged map, or none, so a faithful comparison is impossible.
// A transport the agent never reported is missing information of the same kind, so it is not evidence of
// a difference either. The guard is on whether the live side knows the value, never on the pair itself:
// http against a reported sse stays real drift. Without it, a cli that omits the field strands the server
// on the drift branch, where restore returns early and a rotated header token stops converging for good.
func mcpIdentityDrift(desired config.McpServer, installed InstalledMcpServer) []string {
	var fields []string
	if !installed.TransportInferred && desired.Transport != installed.Transport {
		fields = append(fields, "transport")
	}
	if normalizeMcpCommand(desired.Command) != normalizeMcpCommand(installed.Command) {
		fields = append(fields, "command")
	}
	if desired.URL != installed.URL {
		fields = append(fields, "url")
	}
	return fields
}

// Every Add splits the command with strings.Fields, so spacing alone can never be a real difference.
func normalizeMcpCommand(command string) string {
	return strings.Join(strings.Fields(command), " ")
}

func urlMcpTransport(transport string) bool {
	return transport == "http" || transport == "sse"
}

// Only for comparing two agents' reports of the same server, never for drift against the manifest:
// adapters normalize a url server's flavour differently, so http against sse across two CLIs is a
// disagreement between reporters rather than evidence of two different servers. Drift keeps the strict
// comparison, since there the manifest states what the transport is meant to be.
func mcpReportsDisagreeOnTransport(a, b string) bool {
	if urlMcpTransport(a) && urlMcpTransport(b) {
		return false
	}
	return a != b
}

func mcpIdentityDriftLine(agentID, name string, fields []string) string {
	return fmt.Sprintf(
		"%s/%s: drifted on %s (%s differ from the manifest; left untouched); resolve with %s",
		agentID, name, agentID, strings.Join(fields, ", "), mcpDriftRemedy)
}

func updateMcpServer(ctx context.Context, adapter McpAdapter, desired config.McpServer, previous InstalledMcpServer) error {
	if updater, ok := adapter.(agent.McpInPlaceUpdater); ok {
		if err := updater.UpdateMcpServer(ctx, desired); err != nil {
			return fmt.Errorf("update in place: %w", err)
		}
		return nil
	}
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

// Ungrouped servers restore everywhere; grouped ones restore when the group is active, mirroring resolveSkillPackages.
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

func (a *App) RestoreMcpServers(ctx context.Context, opts RestoreMcpOptions) (RestoreMcpResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return RestoreMcpResult{}, err
	}
	if err := a.requireAgentsEnabled(cfg); err != nil {
		return RestoreMcpResult{}, err
	}
	if config.BoolVal(a.effectiveSettings(cfg).McpDisabled) {
		return RestoreMcpResult{Warnings: []string{"mcp servers are disabled for this host, skipping sync"}}, nil
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
				if fields := mcpIdentityDrift(s, previous); len(fields) > 0 {
					res.Drift = append(res.Drift, mcpIdentityDriftLine(adapter.ID(), s.Name, fields))
					continue
				}
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

// AddMcpResult — The manifest write is unconditional: it is the source of intent, so a live-install failure must not block it.
type AddMcpResult struct {
	Errors           []McpServerError
	AlreadyInstalled []string
	Updated          []string
	// Normal on multi-host manifests, an actionable warning on explicit installs.
	SkippedUnavailable []string
}

type RemoveMcpResult struct {
	Errors             []McpServerError
	SkippedUnavailable []string
}

// AddMcpServer — Every targeted adapter is attempted and the manifest is upserted afterward, so failures never desync intent.
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

// RemoveMcpServer — Every targeted adapter is attempted and the manifest deletion happens regardless of individual failures.
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

// SetMcpServerAgents — Per-adapter tolerant like AddMcpServer.
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

// McpServerByName — The full config.McpServer, unlike the McpServerRow projection which drops Env and EnvLiteral.
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

// McpAdoptResult — Conflicts and skips are data, not failure: one contested name must not stop the rest of a claim pass.
type McpAdoptResult struct {
	Adopted   int
	Conflicts []string
	Skipped   []string
	// No adopt path populates this: a resolved value on any surface refuses outright, so there is no
	// longer such a thing as a server claimed with an advisory. Kept because internal/tui still drains it.
	Warnings []string
	// Populated only on a dry run; one preview line per server that would be claimed.
	WouldAdopt []string
}

// Two agents describing a name differently give omni no single definition, so it stays unmanaged.
func mcpAdoptCandidates(unmanaged map[string][]InstalledMcpServer) (map[string]mcpAdoptCandidate, []string) {
	candidates := make(map[string]mcpAdoptCandidate)
	conflicted := make(map[string]bool)
	agentIDs := make([]string, 0, len(unmanaged))
	for agentID := range unmanaged {
		agentIDs = append(agentIDs, agentID)
	}
	sort.Strings(agentIDs)
	for _, agentID := range agentIDs {
		for _, srv := range unmanaged[agentID] {
			current, exists := candidates[srv.Name]
			if !exists {
				candidates[srv.Name] = mcpAdoptCandidate{server: srv, agents: []string{agentID}}
				continue
			}
			if mcpDefinitionsConflict(srv, current.server) {
				conflicted[srv.Name] = true
				continue
			}
			if !slices.Contains(current.agents, agentID) {
				current.agents = append(current.agents, agentID)
				candidates[srv.Name] = current
			}
		}
	}
	names := make([]string, 0, len(conflicted))
	for name := range conflicted {
		delete(candidates, name)
		names = append(names, name)
	}
	sort.Strings(names)
	lines := make([]string, 0, len(names))
	for _, name := range names {
		lines = append(lines, fmt.Sprintf(
			"mcp server %q is unmanaged under multiple agents with conflicting configuration; declare it manually", name))
	}
	return candidates, lines
}

type mcpAdoptCandidate struct {
	server InstalledMcpServer
	agents []string
}

// A surface an adapter cannot report is missing information, not a competing definition: codex never
// reports env at all, so comparing it against claude's marked the same server conflicting on both and
// left it unadoptable everywhere. Only surfaces both sides actually described are compared.
func mcpDefinitionsConflict(a, b InstalledMcpServer) bool {
	switch {
	case mcpReportsDisagreeOnTransport(a.Transport, b.Transport):
		return true
	case normalizeMcpCommand(a.Command) != normalizeMcpCommand(b.Command):
		return true
	case a.URL != b.URL:
		return true
	case a.HeadersKnown && b.HeadersKnown && !maps.Equal(a.Headers, b.Headers):
		return true
	case len(a.EnvLiteral) > 0 && len(b.EnvLiteral) > 0 && !maps.Equal(a.EnvLiteral, b.EnvLiteral):
		return true
	}
	return false
}

// Whether a resolved value is a credential cannot be decided from its shape: a name list missed
// "X-Auth: alice:s3cr3t!" and an entropy rule refused "X-Workspace: engineering-platform-team-us", so
// no guess is made and every resolved header is refused alike. That `agents mcp add --header` accepts
// any literal unchecked is not a reason to warn instead: adoption happens to the user, so a value they
// never typed must never be copied silently, while the manual door is the consented path. How an adapter
// happened to report the header decides nothing — a header spelled inside a command line refuses too.
func mcpHeaderPassthrough(name string, headers map[string]string) (refusal string) {
	var resolved []string
	for header, value := range headers {
		if value == "" || valueDefersToEnvironment(value) {
			continue
		}
		resolved = append(resolved, header)
	}
	if len(resolved) == 0 {
		return ""
	}
	sort.Strings(resolved)
	// An adapter parses these names out of another CLI's report, so a name can be payload-shaped too.
	for i, header := range resolved {
		resolved[i] = safeNameLabel(header, fmt.Sprintf("#%d", i+1))
	}
	return fmt.Sprintf(
		"mcp server %q sets header(s) %s to a resolved value; omni will not copy that into settings.json, so declare the server manually with ${ENV_VAR} references",
		name, strings.Join(resolved, ", "))
}

// A query string or userinfo on an mcp endpoint is overwhelmingly an api key, so both are refused unless they defer to the environment; a bare url carries nothing and still adopts.
func mcpURLPassthrough(name, rawURL string) (refusal string) {
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Sprintf("mcp server %q has a url omni cannot parse; declare it manually", name)
	}
	if u.User != nil {
		return fmt.Sprintf(
			"mcp server %q embeds credentials in its url; omni will not copy that into settings.json, so declare the server manually with ${ENV_VAR} references",
			name)
	}
	// A fragment is never sent to the server, but adoption's concern is what lands in a committed file.
	if u.Fragment != "" && !valueDefersToEnvironment(u.Fragment) {
		return fmt.Sprintf(
			"mcp server %q carries a resolved value in its url fragment; omni will not copy that into settings.json, so declare the server manually with ${ENV_VAR} references",
			name)
	}
	// url.Values.Query() discards pairs it cannot decode, which would drop an unparseable secret silently into the adopted url; take the error instead of the lossy result.
	if _, err := url.ParseQuery(u.RawQuery); err != nil {
		return fmt.Sprintf("mcp server %q has a url query omni cannot parse; declare it manually", name)
	}
	var resolved []string
	for position, pair := range strings.Split(u.RawQuery, "&") {
		if pair == "" {
			continue
		}
		rawKey, rawValue, hasValue := strings.Cut(pair, "=")
		key, keyErr := url.QueryUnescape(rawKey)
		value, valueErr := url.QueryUnescape(rawValue)
		if keyErr != nil || valueErr != nil {
			return fmt.Sprintf("mcp server %q has a url query omni cannot parse; declare it manually", name)
		}
		ordinal := fmt.Sprintf("#%d", position+1)
		// "?sk-live-…" is a valueless pair whose key is the credential itself, so an empty value only
		// means "carries nothing" when the pair actually spelled one out — and the key can never be
		// echoed here, being the payload by construction.
		if !hasValue {
			if key != "" && !valueDefersToEnvironment(key) {
				resolved = append(resolved, ordinal)
			}
			continue
		}
		if value == "" || valueDefersToEnvironment(value) {
			continue
		}
		resolved = append(resolved, safeNameLabel(key, ordinal))
	}
	if len(resolved) == 0 {
		return ""
	}
	sort.Strings(resolved)
	return fmt.Sprintf(
		"mcp server %q sets url parameter(s) %s to a resolved value; omni will not copy that into settings.json, so declare the server manually with ${ENV_VAR} references",
		name, strings.Join(resolved, ", "))
}

// argv has no schema, so only the carriers a credential actually travels in are recognised: an inline
// assignment, a url with userinfo, the value after a credential-named flag, and a header spec. Labels
// name the key or the argument position and never the value, so a refusal cannot leak the secret.
//
// The two passes never consume each other's tokens. An earlier repair scanned once and let a header spec
// swallow every following token, so appending a benign "--header X-Trace: 1" to any command disabled
// credential detection for the rest of argv. Header extraction is additional signal, never a substitute.
func scanMcpCommand(command string) []string {
	fields := strings.Fields(command)
	return append(scanArgvCredentials(fields), scanArgvHeaderSpecs(fields)...)
}

func scanArgvCredentials(fields []string) []string {
	var resolved []string
	for i, token := range fields {
		// Before the assignment split: a dsn such as postgresql://u:p@host/db?sslmode=require would otherwise cut into a "key" that still holds the password, and the refusal echoes the key.
		if tokenEmbedsURLCredentials(token) {
			resolved = append(resolved, fmt.Sprintf("argument %d", i+1))
			continue
		}
		if key, value, ok := strings.Cut(token, "="); ok && !strings.HasPrefix(token, "=") {
			if value != "" && !valueDefersToEnvironment(value) {
				resolved = append(resolved, safeKeyLabel(key, fmt.Sprintf("argument %d", i+1)))
			}
			continue
		}
		if credentialFlagToken(token) && i+1 < len(fields) && !valueDefersToEnvironment(fields[i+1]) {
			resolved = append(resolved, token)
		}
	}
	return resolved
}

// "--header Authorization: Bearer …" is the standard remote-auth recipe for stdio clients. Adoption
// copies the whole command string into settings.json, so a header spelled there is refused like any
// other command-line credential; warning instead would advise the user about a secret it had already
// written to the committed file. Adapters flatten a command into one string, so argv boundaries are
// already gone: a spec runs from its flag to the next flag. An empty value carries no secret and adopts
// like the reported-header and url surfaces, but only when the spec ran to the end of argv: a spec cut
// short by a following flag, as in "--header X-Api-Key: -sk-live-…", reads as valueless only because the
// value itself looked like a flag, so that one is refused.
func scanArgvHeaderSpecs(fields []string) []string {
	var resolved []string
	for i, token := range fields {
		flag, inline, _ := strings.Cut(token, "=")
		if !headerFlagToken(flag) {
			continue
		}
		end := i + 1
		// getopt hands the token right after a value-taking flag to that flag whatever it starts with, so
		// "-H -Authorization: Bearer …" sends a header named "-Authorization". Reading that dash as the
		// next flag produced an empty spec and adopted the secret verbatim.
		if inline == "" && end < len(fields) {
			end++
		}
		for end < len(fields) && !strings.HasPrefix(fields[end], "-") {
			end++
		}
		spec := strings.TrimSpace(inline + " " + strings.Join(fields[i+1:end], " "))
		header, value, ok := strings.Cut(spec, ":")
		if header = strings.TrimSpace(header); !ok || header == "" || valueDefersToEnvironment(value) {
			continue
		}
		// The carve-outs below decide from the value, so a spec that carries no value at all adopted whatever its name spelled — and "--header sk-live-…:" puts the secret in the name.
		if !conventionalKeyShape(header, false) {
			resolved = append(resolved, fmt.Sprintf("argument %d", i+1))
			continue
		}
		if strings.TrimSpace(value) == "" && end == len(fields) {
			continue
		}
		// "docker -H unix:///var/run/docker.sock" names a daemon socket, not a header: a name followed by "//" is url syntax, and no header value begins that way.
		if strings.HasPrefix(value, "//") {
			continue
		}
		resolved = append(resolved, safeNameLabel(header, fmt.Sprintf("argument %d", i+1)))
	}
	return resolved
}

func mcpCommandPassthrough(name, command string) (refusal string) {
	resolved := scanMcpCommand(command)
	if len(resolved) == 0 {
		return ""
	}
	sort.Strings(resolved)
	return fmt.Sprintf(
		"mcp server %q passes %s as a resolved value on its command line; omni will not copy that into settings.json, so declare the server manually with ${ENV_VAR} references",
		name, strings.Join(slices.Compact(resolved), ", "))
}

// "-h" is host or help far more often than header (mysql-mcp -h db.example.com:3306 -u root), so only curl's capital short form counts as the abbreviation.
func headerFlagToken(token string) bool {
	if token == "-H" {
		return true
	}
	if !strings.HasPrefix(token, "-") {
		return false
	}
	switch strings.ToLower(strings.TrimLeft(token, "-")) {
	case "header", "headers":
		return true
	}
	return false
}

// A key can itself be the payload — "?sk-live-…" has no value at all, and a dsn cut on "=" leaves the
// password in the key — and these strings go straight into a user-facing message. So the key is echoed
// only when it spells a conventional parameter or header name: ascii words with no digits, joined by one
// kind of separator, and — for a bare hyphenated key — the per-word capitals a header name carries. That
// last clause is what an opaque vendor token lacks: "sk-live-abc" and "xoxb-…" are lowercase hyphenated,
// while "X-Api-Key" is not, and no vendor issues underscore- or dot-segmented tokens. A dashed flag is
// exempt because "--access-token" is argv syntax a value never has.
//
// As a label this is only a display guard, so an unconventional-looking name costs a less specific
// message and nothing else. One caller reads the same shape as a verdict: an argv header spec carrying
// no value adopts on its name alone, so there the unconventional name refuses instead, and the cost of
// a false positive is a lowercase-kebab header spelled with no value having to be declared by hand. The
// reasoning must not be carried to values, where guessing produced both the "X-Auth" bypass and the
// "akita-jp" false refusal.
func safeKeyLabel(key, fallback string) string {
	return keyLabel(key, fallback, true)
}

// A url query key and a header name are never spelled with a leading dash, so the flag exemption must not
// reach them: it would let "?-sk-live-abc=1" print the token that "?sk-live-abc=1" already withholds.
func safeNameLabel(key, fallback string) string {
	return keyLabel(key, fallback, false)
}

func keyLabel(key, fallback string, argv bool) string {
	if !conventionalKeyShape(key, argv) {
		return fallback
	}
	return key
}

func conventionalKeyShape(key string, argv bool) bool {
	if key == "" || utf8.RuneCountInString(key) > 24 {
		return false
	}
	name := strings.TrimLeft(key, "-")
	flag := len(name) < len(key)
	if name == "" || (flag && !argv) {
		return false
	}
	var separator rune
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r == '-', r == '_', r == '.':
			if separator != 0 && separator != r {
				return false
			}
			separator = r
		default:
			return false
		}
	}
	words := []string{name}
	if separator != 0 {
		words = strings.Split(name, string(separator))
	}
	for _, word := range words {
		if word == "" {
			return false
		}
		if separator == '-' && !flag && (word[0] < 'A' || word[0] > 'Z') {
			return false
		}
	}
	return true
}

func tokenEmbedsURLCredentials(token string) bool {
	if !strings.Contains(token, "://") {
		return false
	}
	u, err := url.Parse(token)
	return err == nil && u.User != nil
}

// Suffix rather than substring matching so "--author" and "--signal" stay ordinary flags. This can only
// add refusals: a flag it misses still has to clear the structural checks, so a gap here is not a hole.
func credentialFlagToken(token string) bool {
	if !strings.HasPrefix(token, "-") {
		return false
	}
	flag := strings.ToLower(strings.TrimSuffix(strings.TrimLeft(token, "-"), "="))
	for _, marker := range []string{"key", "token", "secret", "password", "passwd", "credential", "auth", "bearer", "jwt"} {
		if strings.HasSuffix(flag, marker) {
			return true
		}
	}
	return false
}

// McpAdoptCheck — Vets a server an agent CLI reported before it enters the portable manifest: returns
// the env var names proven to be ambient passthrough, and one refusal per surface whose resolved value
// the user would never see being copied. Every surface the manifest copies is covered alike — headers,
// url, command line, and env — because the verdict belongs to the value, not to the surface that
// carried it or the adapter that happened to report it.
func (a *App) McpAdoptCheck(s InstalledMcpServer) (env []string, refusals []string) {
	env, envRefusal := mcpEnvPassthrough(s.Name, s.EnvLiteral, a.lookupEnv)
	for _, refusal := range []string{
		mcpHeaderPassthrough(s.Name, s.Headers),
		mcpURLPassthrough(s.Name, s.URL),
		mcpCommandPassthrough(s.Name, s.Command),
		envRefusal,
	} {
		if refusal != "" {
			refusals = append(refusals, refusal)
		}
	}
	if len(refusals) > 0 {
		return nil, refusals
	}
	return env, nil
}

// A value equal to what the ambient environment holds demonstrably came from there, so the name alone restores it; anything else is a literal the manifest must not carry.
func mcpEnvPassthrough(name string, envLiteral map[string]string, lookupEnv func(string) string) (env []string, refusal string) {
	var literal []string
	for key, value := range envLiteral {
		// An unset variable also reads as "", so an empty value can never prove a passthrough.
		if value != "" && lookupEnv(key) == value {
			env = append(env, key)
			continue
		}
		literal = append(literal, key)
	}
	if len(literal) > 0 {
		sort.Strings(literal)
		// An adapter parses these names out of another CLI's report, so a name can be payload-shaped too.
		for i, key := range literal {
			literal[i] = safeNameLabel(key, fmt.Sprintf("#%d", i+1))
		}
		return nil, fmt.Sprintf(
			"mcp server %q sets env var(s) %s to a value that is not in the environment; omni will not copy that into settings.json, so declare the server manually",
			name, strings.Join(literal, ", "))
	}
	sort.Strings(env)
	return env, ""
}

// "Bearer ${TOKEN}" is the ordinary shape of an mcp auth header, so requiring the whole value to be a
// reference refused the very form the refusal message asks for. Merely containing one is not enough
// either: "session=<secret>; theme=${THEME}" would launder a resolved credential past the carve-out.
// So every reference is removed and the remainder must be structure — the value defers to the
// environment only when nothing is left that could carry content.
// agent.exactEnvReference stays whole-value on purpose: codex's env_http_headers can only hold a name.
func valueDefersToEnvironment(value string) bool {
	remainder, found := stripEnvReferences(value)
	return found && remainderIsStructural(remainder)
}

func stripEnvReferences(value string) (remainder string, found bool) {
	var out strings.Builder
	for i := 0; i < len(value); {
		if strings.HasPrefix(value[i:], "${") {
			if end := strings.IndexByte(value[i+2:], '}'); end >= 0 && validEnvReferenceName(value[i+2:i+2+end]) {
				found = true
				i += 2 + end + 1
				continue
			}
		}
		out.WriteByte(value[i])
		i++
	}
	return out.String(), found
}

// Shape, not content: whether the leftovers look secret is never asked, only whether they could hold
// anything at all. Every credential encoding in use — hex, base64, jwt, uuid, vendor prefixes — spells
// itself in ascii alphanumerics, so a remainder with none of those beyond the fixed scheme words carries
// no value. Non-ascii is refused outright rather than reasoned about.
func remainderIsStructural(remainder string) bool {
	for _, r := range remainder {
		if r >= utf8.RuneSelf {
			return false
		}
	}
	notAlphanumeric := func(r rune) bool {
		return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
	}
	for _, word := range strings.FieldsFunc(remainder, notAlphanumeric) {
		// Digit-bearing schemes (SCRAM-SHA-256, iso-kam3-*) are left out on purpose: admitting them means
		// admitting bare digit runs, and refusing those is what keeps hex and base64 chunks out.
		switch strings.ToLower(word) {
		case "bearer", "basic", "token", "digest", "dpop", "negotiate", "ntlm", "oauth", "apikey":
		default:
			return false
		}
	}
	return true
}

func validEnvReferenceName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

type mcpAdoptOptions struct {
	DryRun bool
}

// AdoptUnmanagedMcpServers — Manifest bookkeeping only: never calls an agent CLI, since the servers are already installed there.
func (a *App) AdoptUnmanagedMcpServers(ctx context.Context) (McpAdoptResult, error) {
	return a.adoptUnmanagedMcpServers(ctx, mcpAdoptOptions{})
}

func (a *App) adoptUnmanagedMcpServers(ctx context.Context, opts mcpAdoptOptions) (McpAdoptResult, error) {
	diff, err := a.ImportMcpServers(ctx)
	if err != nil {
		return McpAdoptResult{}, err
	}
	candidates, conflicts := mcpAdoptCandidates(diff.Unmanaged)
	names := make([]string, 0, len(candidates))
	for name := range candidates {
		names = append(names, name)
	}
	sort.Strings(names)
	res := McpAdoptResult{Conflicts: conflicts}
	claim := func(c *config.RootConfig) error {
		managed := make(map[string]struct{}, len(c.Agents.McpServers))
		for _, s := range c.Agents.McpServers {
			managed[s.Name] = struct{}{}
		}
		for _, name := range names {
			if _, ok := managed[name]; ok {
				continue
			}
			entry := candidates[name]
			env, refusals := a.McpAdoptCheck(entry.server)
			if len(refusals) > 0 {
				res.Skipped = append(res.Skipped, refusals...)
				continue
			}
			managed[name] = struct{}{}
			if opts.DryRun {
				res.WouldAdopt = append(res.WouldAdopt, fmt.Sprintf(
					"would claim mcp server %q on %s", name, strings.Join(entry.agents, ", ")))
				continue
			}
			c.Agents.McpServers = append(c.Agents.McpServers, config.McpServer{
				Name:      entry.server.Name,
				Transport: entry.server.Transport,
				Command:   entry.server.Command,
				URL:       entry.server.URL,
				Headers:   entry.server.Headers,
				Env:       env,
				Agents:    entry.agents,
			})
			res.Adopted++
		}
		return nil
	}
	if opts.DryRun {
		cfg, err := a.loadConfig()
		if err != nil {
			return res, err
		}
		return res, claim(cfg)
	}
	return res, a.withConfig(claim)
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

package app

import (
	"context"
	"fmt"
	"os"
	"slices"

	"github.com/lkshrk/omni/internal/config"
)

type RestorePluginOptions struct {
	DryRun bool
}

type RestorePluginResult struct {
	Installed        []string
	AlreadyInstalled []string
	Skipped          []string
	WouldInstall     []string
	Warnings         []string
	Errors           []PluginError
}

type PluginError struct {
	AgentID string
	Name    string
	Err     error
}

func (e PluginError) Error() string {
	return fmt.Sprintf("agent %s / plugin %s: %v", e.AgentID, e.Name, e.Err)
}

type PluginImportDiff struct {
	Unmanaged map[string][]InstalledPlugin
}

func WithPluginAdapters(adapters []PluginAdapter) func(*App) {
	return func(a *App) { a.testPluginAdapters = adapters }
}

func (a *App) pluginAdapters() []PluginAdapter {
	if a.testPluginAdapters != nil {
		return a.testPluginAdapters
	}
	exec := a.fallbackExecutor().Run
	return []PluginAdapter{
		NewClaudeCodePluginAdapter(exec, os.LookupEnv),
		NewCodexPluginAdapter(exec, os.LookupEnv),
		NewGrokPluginAdapter(exec, os.LookupEnv),
	}
}

func pluginTargetsAdapter(p config.Plugin, adapterID string) bool {
	if len(p.Agents) == 0 {
		return true
	}
	for _, id := range p.Agents {
		if id == adapterID {
			return true
		}
	}
	return false
}

// resolvePlugins returns the plugins active for groupName, mirroring
// resolveMcpServers: ungrouped plugins restore everywhere; group-listed
// plugins restore only when that group is active.
func resolvePlugins(cfg *config.RootConfig, groupName string) []config.Plugin {
	groupedNames := make(map[string]struct{})
	activeNames := make(map[string]struct{})
	for _, g := range cfg.Groups {
		if g == nil {
			continue
		}
		for _, name := range g.Plugins {
			groupedNames[name] = struct{}{}
			if g.Name == groupName {
				activeNames[name] = struct{}{}
			}
		}
	}
	var out []config.Plugin
	for _, p := range cfg.Agents.Plugins {
		if _, grouped := groupedNames[p.Name]; !grouped {
			out = append(out, p)
			continue
		}
		if _, active := activeNames[p.Name]; active {
			out = append(out, p)
		}
	}
	return out
}

// pluginIdentity is the key used to match a manifest plugin against an
// adapter's ListPlugins output: name alone is not unique across marketplaces.
func pluginIdentity(name, marketplace string) string {
	return name + "\x00" + marketplace
}

// pluginListed reports whether name/marketplace appears in an adapter's
// ListPlugins() output.
func pluginListed(installed []InstalledPlugin, name, marketplace string) bool {
	want := pluginIdentity(name, marketplace)
	for _, ip := range installed {
		if pluginIdentity(ip.Name, ip.Marketplace) == want {
			return true
		}
	}
	return false
}

func findMarketplace(marketplaces []config.Marketplace, name string) *config.Marketplace {
	for i := range marketplaces {
		if marketplaces[i].Name == name {
			cp := marketplaces[i]
			return &cp
		}
	}
	return nil
}

// ensureMarketplace adds m to adapter if adapter does not already report it
// present. Errors are returned for the caller to collect, never fatal to the
// batch.
func ensureMarketplace(ctx context.Context, adapter PluginAdapter, m config.Marketplace) error {
	existing, err := adapter.ListMarketplaces(ctx)
	if err != nil {
		return err
	}
	if marketplaceListed(existing, m.Name) {
		return nil
	}
	return adapter.AddMarketplace(ctx, m)
}

func marketplaceListed(existing []InstalledMarketplace, name string) bool {
	for _, im := range existing {
		if im.Name == name {
			return true
		}
	}
	return false
}

// RestorePlugins installs manifest plugins into each targeted agent CLI,
// adding any marketplace a plugin needs before installing the plugin itself.
// Every already-configured marketplace on an adapter is also refreshed first
// (best-effort, collected as a warning on failure) — plugin installs and any
// future update pass both read from the marketplace's local clone, so a
// stale clone must never be left in place ahead of them.
func (a *App) RestorePlugins(ctx context.Context, opts RestorePluginOptions) (RestorePluginResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return RestorePluginResult{}, err
	}
	if err := a.requireAgentsEnabled(cfg); err != nil {
		return RestorePluginResult{}, err
	}
	if config.BoolVal(a.effectiveSettings(cfg).PluginsDisabled) {
		return RestorePluginResult{Warnings: []string{"plugins are disabled for this host, skipping restore"}}, nil
	}
	plugins := resolvePlugins(cfg, currentMachineGroupName())
	var res RestorePluginResult
	for _, adapter := range a.pluginAdapters() {
		if !adapter.Available() {
			res.Warnings = append(res.Warnings, fmt.Sprintf("agent %s not available, skipping", adapter.ID()))
			continue
		}
		if !opts.DryRun {
			if refreshErr := adapter.UpdateMarketplaces(ctx); refreshErr != nil {
				res.Warnings = append(res.Warnings, fmt.Sprintf("agent %s: refresh marketplaces failed, continuing: %v", adapter.ID(), refreshErr))
			}
		}
		installed, listErr := adapter.ListPlugins(ctx)
		if listErr != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("agent %s: list plugins failed, attempting installs: %v", adapter.ID(), listErr))
			installed = nil
		}
		alreadyInstalled := make(map[string]struct{}, len(installed))
		for _, ip := range installed {
			alreadyInstalled[pluginIdentity(ip.Name, ip.Marketplace)] = struct{}{}
		}
		addedMarketplace := make(map[string]struct{})
		for _, p := range plugins {
			if !pluginTargetsAdapter(p, adapter.ID()) {
				res.Skipped = append(res.Skipped, adapter.ID()+"/"+p.Name)
				continue
			}
			if _, present := alreadyInstalled[pluginIdentity(p.Name, p.Marketplace)]; present {
				res.AlreadyInstalled = append(res.AlreadyInstalled, adapter.ID()+"/"+p.Name)
				continue
			}
			if opts.DryRun {
				res.WouldInstall = append(res.WouldInstall, adapter.ID()+"/"+p.Name)
				continue
			}
			if _, done := addedMarketplace[p.Marketplace]; !done {
				if m := findMarketplace(cfg.Agents.Marketplaces, p.Marketplace); m != nil {
					if mErr := ensureMarketplace(ctx, adapter, *m); mErr != nil {
						res.Errors = append(res.Errors, PluginError{AgentID: adapter.ID(), Name: p.Name, Err: fmt.Errorf("marketplace %s: %w", m.Name, mErr)})
						continue
					}
				}
				addedMarketplace[p.Marketplace] = struct{}{}
			}
			if installErr := adapter.InstallPlugin(ctx, p); installErr != nil {
				res.Errors = append(res.Errors, PluginError{AgentID: adapter.ID(), Name: p.Name, Err: installErr})
				continue
			}
			res.Installed = append(res.Installed, adapter.ID()+"/"+p.Name)
		}
	}
	return res, nil
}

type AddPluginResult struct {
	Errors           []PluginError
	AlreadyInstalled []string
	// SkippedUnavailable lists adapter/name pairs whose agent CLI is not on
	// PATH: normal on multi-host manifests, an actionable warning on explicit installs.
	SkippedUnavailable []string
}

type UpdatePluginResult struct {
	Errors             []PluginError
	SkippedUnavailable []string
}

// UpdateMarketplacesResult collects per-adapter refresh failures. Errors are
// data, not fatal: one adapter's refresh failure never stops the others.
type UpdateMarketplacesResult struct {
	Errors []PluginError
}

// UpdateMarketplaces refreshes every configured marketplace on every
// available plugin adapter. Callers that are about to run UpdatePlugins for
// at least one outdated plugin should skip this and rely on UpdatePlugins'
// own up-front refresh instead of doing it twice.
func (a *App) UpdateMarketplaces(ctx context.Context) (UpdateMarketplacesResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return UpdateMarketplacesResult{}, err
	}
	if err := a.requirePluginsEnabled(cfg); err != nil {
		return UpdateMarketplacesResult{}, err
	}
	var res UpdateMarketplacesResult
	for _, adapter := range a.pluginAdapters() {
		if !adapter.Available() {
			continue
		}
		if refreshErr := adapter.UpdateMarketplaces(ctx); refreshErr != nil {
			res.Errors = append(res.Errors, PluginError{AgentID: adapter.ID(), Name: "marketplaces", Err: refreshErr})
		}
	}
	return res, nil
}

// UpdatePlugin updates a manifest plugin on every targeted, available adapter.
func (a *App) UpdatePlugin(ctx context.Context, name string) (UpdatePluginResult, error) {
	return a.UpdatePlugins(ctx, []string{name}, nil)
}

// UpdatePlugins updates several manifest plugins. Each adapter's marketplace
// snapshot is refreshed once up front — a plugin update installs from the
// marketplace's local clone, so a stale clone would make the update install a
// stale version even though the CLI call succeeds; refreshing per plugin
// would repeat the same update-all CLI call N times. One adapter's failure
// (marketplace refresh or plugin update) does not stop the others; errors are
// collected as data on the result, mirroring AddPluginResult. progress, when
// non-nil, is called with each plugin name before its update runs.
func (a *App) UpdatePlugins(ctx context.Context, names []string, progress func(name string)) (UpdatePluginResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return UpdatePluginResult{}, err
	}
	if err := a.requirePluginsEnabled(cfg); err != nil {
		return UpdatePluginResult{}, err
	}
	targets := make([]*config.Plugin, 0, len(names))
	for _, name := range names {
		target := findPlugin(cfg.Agents.Plugins, name)
		if target == nil {
			return UpdatePluginResult{}, fmt.Errorf("plugin %q not found in manifest; omni only updates plugins it added", name)
		}
		targets = append(targets, target)
	}
	var res UpdatePluginResult
	adapters := a.pluginAdapters()
	for _, adapter := range adapters {
		if !adapter.Available() {
			continue
		}
		targeted := slices.ContainsFunc(targets, func(t *config.Plugin) bool {
			return pluginTargetsAdapter(*t, adapter.ID())
		})
		if !targeted {
			continue
		}
		if refreshErr := adapter.UpdateMarketplaces(ctx); refreshErr != nil {
			res.Errors = append(res.Errors, PluginError{AgentID: adapter.ID(), Name: "marketplaces", Err: fmt.Errorf("refresh marketplaces: %w", refreshErr)})
		}
	}
	for i, target := range targets {
		if progress != nil {
			progress(names[i])
		}
		for _, adapter := range adapters {
			if !pluginTargetsAdapter(*target, adapter.ID()) {
				continue
			}
			if !adapter.Available() {
				res.SkippedUnavailable = append(res.SkippedUnavailable, adapter.ID()+"/"+names[i])
				continue
			}
			if updateErr := adapter.UpdatePlugin(ctx, names[i], target.Marketplace); updateErr != nil {
				res.Errors = append(res.Errors, PluginError{AgentID: adapter.ID(), Name: names[i], Err: updateErr})
			}
		}
	}
	return res, nil
}

type RemovePluginResult struct {
	Errors             []PluginError
	SkippedUnavailable []string
}

// AddPlugin validates the marketplace ref, upserts the manifest, then
// installs on each target adapter. Manifest write is unconditional on
// adapter outcome (manifest = intent), mirroring AddMcpServer. An adapter
// that already lists p.Name/p.Marketplace installed is skipped rather than
// re-installed, mirroring AddMcpServer's skip: neither `claude plugins
// install` nor `codex plugin add` is documented as idempotent on an already-
// installed identity, and this path is reachable with an already-installed
// adapter whenever a caller targets an unmanaged plugin found on multiple
// agents (e.g. claiming it from the TUI/CLI unmanaged list).
func (a *App) AddPlugin(ctx context.Context, p config.Plugin) (AddPluginResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return AddPluginResult{}, err
	}
	if err := a.requirePluginsEnabled(cfg); err != nil {
		return AddPluginResult{}, err
	}
	m := findMarketplace(cfg.Agents.Marketplaces, p.Marketplace)
	if m == nil {
		return AddPluginResult{}, fmt.Errorf("plugin %q references unknown marketplace %q; declare it first", p.Name, p.Marketplace)
	}
	var res AddPluginResult
	for _, adapter := range a.pluginAdapters() {
		if !pluginTargetsAdapter(p, adapter.ID()) {
			continue
		}
		// listed check first: adopting an already-present plugin must not demand the CLI
		if installed, listErr := adapter.ListPlugins(ctx); listErr == nil && pluginListed(installed, p.Name, p.Marketplace) {
			res.AlreadyInstalled = append(res.AlreadyInstalled, adapter.ID()+"/"+p.Name)
			continue
		}
		if !adapter.Available() {
			res.SkippedUnavailable = append(res.SkippedUnavailable, adapter.ID()+"/"+p.Name)
			continue
		}
		if mErr := ensureMarketplace(ctx, adapter, *m); mErr != nil {
			res.Errors = append(res.Errors, PluginError{AgentID: adapter.ID(), Name: p.Name, Err: fmt.Errorf("marketplace %s: %w", m.Name, mErr)})
			continue
		}
		if installErr := adapter.InstallPlugin(ctx, p); installErr != nil {
			res.Errors = append(res.Errors, PluginError{AgentID: adapter.ID(), Name: p.Name, Err: installErr})
		}
	}
	if err := a.withConfig(func(c *config.RootConfig) error {
		c.Agents.Plugins = upsertPlugin(c.Agents.Plugins, p)
		return nil
	}); err != nil {
		return res, fmt.Errorf("installed %s but failed to save to manifest (re-run to persist): %w", p.Name, err)
	}
	return res, nil
}

// AddMarketplace validates uniqueness, upserts the manifest, then adds the
// marketplace on each target adapter. Manifest write is unconditional.
func (a *App) AddMarketplace(ctx context.Context, m config.Marketplace) (AddPluginResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return AddPluginResult{}, err
	}
	if err := a.requirePluginsEnabled(cfg); err != nil {
		return AddPluginResult{}, err
	}
	var res AddPluginResult
	for _, adapter := range a.pluginAdapters() {
		targeted := len(m.Agents) == 0
		for _, id := range m.Agents {
			if id == adapter.ID() {
				targeted = true
			}
		}
		if !targeted {
			continue
		}
		if existing, listErr := adapter.ListMarketplaces(ctx); listErr == nil && marketplaceListed(existing, m.Name) {
			continue
		}
		if !adapter.Available() {
			res.SkippedUnavailable = append(res.SkippedUnavailable, adapter.ID()+"/"+m.Name)
			continue
		}
		if mErr := ensureMarketplace(ctx, adapter, m); mErr != nil {
			res.Errors = append(res.Errors, PluginError{AgentID: adapter.ID(), Name: m.Name, Err: mErr})
		}
	}
	if err := a.withConfig(func(c *config.RootConfig) error {
		c.Agents.Marketplaces = upsertMarketplace(c.Agents.Marketplaces, m)
		return nil
	}); err != nil {
		return res, fmt.Errorf("added marketplace %s but failed to save to manifest (re-run to persist): %w", m.Name, err)
	}
	return res, nil
}

// RemovePlugin uninstalls from each target adapter (tolerant), then deletes
// the manifest entry. Marketplaces are never touched by this call.
func (a *App) RemovePlugin(ctx context.Context, name string) (RemovePluginResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return RemovePluginResult{}, err
	}
	if err := a.requirePluginsEnabled(cfg); err != nil {
		return RemovePluginResult{}, err
	}
	target := findPlugin(cfg.Agents.Plugins, name)
	if target == nil {
		return RemovePluginResult{}, fmt.Errorf("plugin %q not found in manifest; omni only removes plugins it added", name)
	}
	var res RemovePluginResult
	for _, adapter := range a.pluginAdapters() {
		if !pluginTargetsAdapter(*target, adapter.ID()) {
			continue
		}
		if installed, listErr := adapter.ListPlugins(ctx); listErr == nil && !pluginListed(installed, target.Name, target.Marketplace) {
			continue
		}
		if !adapter.Available() {
			res.SkippedUnavailable = append(res.SkippedUnavailable, adapter.ID()+"/"+name)
			continue
		}
		if removeErr := adapter.RemovePlugin(ctx, *target); removeErr != nil {
			res.Errors = append(res.Errors, PluginError{AgentID: adapter.ID(), Name: name, Err: removeErr})
		}
	}
	if err := a.withConfig(func(c *config.RootConfig) error {
		c.Agents.Plugins = deletePlugin(c.Agents.Plugins, name)
		return nil
	}); err != nil {
		return res, fmt.Errorf("removed %s from agents but failed to update manifest (re-run to persist): %w", name, err)
	}
	return res, nil
}

// RemoveMarketplace deletes only the manifest entry. omni never removes a
// marketplace from an agent CLI — it may still serve hand-installed plugins
// omni does not know about.
func (a *App) RemoveMarketplace(name string) error {
	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	if err := a.requirePluginsEnabled(cfg); err != nil {
		return err
	}
	if findMarketplace(cfg.Agents.Marketplaces, name) == nil {
		return fmt.Errorf("marketplace %q not found in manifest", name)
	}
	var blocking []string
	for _, p := range cfg.Agents.Plugins {
		if p.Marketplace == name {
			blocking = append(blocking, p.Name)
		}
	}
	if len(blocking) > 0 {
		return fmt.Errorf("marketplace %q is still referenced by plugins %v; remove them first", name, blocking)
	}
	return a.withConfig(func(c *config.RootConfig) error {
		c.Agents.Marketplaces = deleteMarketplace(c.Agents.Marketplaces, name)
		return nil
	})
}

// SetPluginAgents re-targets an existing manifest plugin's Agents list and
// reconciles every selected adapter, installing it when missing and removing it when
// deselected.
func (a *App) SetPluginAgents(ctx context.Context, name string, agents []string) (AddPluginResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return AddPluginResult{}, err
	}
	if err := a.requirePluginsEnabled(cfg); err != nil {
		return AddPluginResult{}, err
	}
	target := findPlugin(cfg.Agents.Plugins, name)
	if target == nil {
		return AddPluginResult{}, fmt.Errorf("plugin %q not found in manifest", name)
	}
	updated := *target
	updated.Agents = agents
	m := findMarketplace(cfg.Agents.Marketplaces, target.Marketplace)

	var res AddPluginResult
	for _, adapter := range a.pluginAdapters() {
		wasTargeted := pluginTargetsAdapter(*target, adapter.ID())
		nowTargeted := pluginTargetsAdapter(updated, adapter.ID())
		switch {
		case nowTargeted:
			if installed, listErr := adapter.ListPlugins(ctx); listErr == nil && pluginListed(installed, updated.Name, updated.Marketplace) {
				res.AlreadyInstalled = append(res.AlreadyInstalled, adapter.ID()+"/"+name)
				continue
			}
			if !adapter.Available() {
				res.SkippedUnavailable = append(res.SkippedUnavailable, adapter.ID()+"/"+name)
				continue
			}
			if m != nil {
				if mErr := ensureMarketplace(ctx, adapter, *m); mErr != nil {
					res.Errors = append(res.Errors, PluginError{AgentID: adapter.ID(), Name: name, Err: mErr})
					continue
				}
			}
			if installErr := adapter.InstallPlugin(ctx, updated); installErr != nil {
				res.Errors = append(res.Errors, PluginError{AgentID: adapter.ID(), Name: name, Err: installErr})
			}
		case wasTargeted && !nowTargeted:
			if !adapter.Available() {
				continue
			}
			if removeErr := adapter.RemovePlugin(ctx, *target); removeErr != nil {
				res.Errors = append(res.Errors, PluginError{AgentID: adapter.ID(), Name: name, Err: removeErr})
			}
		}
	}
	if err := a.withConfig(func(c *config.RootConfig) error {
		c.Agents.Plugins = upsertPlugin(c.Agents.Plugins, updated)
		return nil
	}); err != nil {
		return res, fmt.Errorf("updated agents for %s but failed to save to manifest (re-run to persist): %w", name, err)
	}
	return res, nil
}

// PluginByName returns the manifest entry for name, mirroring McpServerByName
// so callers needing the full config.Plugin (not the PluginRow projection)
// can fetch it directly.
func (a *App) PluginByName(name string) (config.Plugin, bool, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return config.Plugin{}, false, err
	}
	target := findPlugin(cfg.Agents.Plugins, name)
	if target == nil {
		return config.Plugin{}, false, nil
	}
	return *target, true, nil
}

func findPlugin(plugins []config.Plugin, name string) *config.Plugin {
	for i := range plugins {
		if plugins[i].Name == name {
			cp := plugins[i]
			return &cp
		}
	}
	return nil
}

// ImportPlugins returns plugins installed in agent CLIs that are not in the
// manifest. Callers adopt identity only (name + marketplace); see AddPlugin.
func (a *App) ImportPlugins(ctx context.Context) (PluginImportDiff, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return PluginImportDiff{}, err
	}
	if err := a.requirePluginsEnabled(cfg); err != nil {
		return PluginImportDiff{}, err
	}
	managed := make(map[string]struct{}, len(cfg.Agents.Plugins))
	for _, p := range cfg.Agents.Plugins {
		managed[p.Name] = struct{}{}
	}
	diff := PluginImportDiff{Unmanaged: make(map[string][]InstalledPlugin)}
	for _, adapter := range a.pluginAdapters() {
		if !adapter.Available() {
			continue
		}
		listed, listErr := adapter.ListPlugins(ctx)
		if listErr != nil {
			continue
		}
		for _, plg := range listed {
			if _, ok := managed[plg.Name]; !ok {
				diff.Unmanaged[adapter.ID()] = append(diff.Unmanaged[adapter.ID()], plg)
			}
		}
	}
	return diff, nil
}

// AdoptUnmanagedPlugins folds ImportPlugins' unmanaged results into the
// manifest by identity only. This is manifest bookkeeping, distinct from
// AddPlugin: it never calls an agent CLI, since the plugins are already
// installed there. A plugin whose reported marketplace has no matching
// declared config.Marketplace is skipped rather than adopted with a dangling
// reference (see config.Plugin doc comment); the caller should surface that
// count if it wants to report skips.
func (a *App) AdoptUnmanagedPlugins(ctx context.Context) (adopted, skipped int, err error) {
	diff, err := a.ImportPlugins(ctx)
	if err != nil {
		return 0, 0, err
	}
	err = a.withConfig(func(c *config.RootConfig) error {
		managed := make(map[string]struct{}, len(c.Agents.Plugins))
		for _, p := range c.Agents.Plugins {
			managed[pluginIdentity(p.Name, p.Marketplace)] = struct{}{}
		}
		for agentID, plugins := range diff.Unmanaged {
			for _, plg := range plugins {
				key := pluginIdentity(plg.Name, plg.Marketplace)
				if _, ok := managed[key]; ok {
					continue
				}
				if findMarketplace(c.Agents.Marketplaces, plg.Marketplace) == nil {
					skipped++
					continue
				}
				managed[key] = struct{}{}
				c.Agents.Plugins = append(c.Agents.Plugins, config.Plugin{
					Name:        plg.Name,
					Marketplace: plg.Marketplace,
					Agents:      []string{agentID},
				})
				adopted++
			}
		}
		return nil
	})
	return adopted, skipped, err
}

// Marketplaces returns the declared manifest marketplaces, for read-only CLI
// display.
func (a *App) Marketplaces() ([]config.Marketplace, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	return cfg.Agents.Marketplaces, nil
}

// FindUndeclaredMarketplace looks up a real, re-addable source string for an
// undeclared marketplace via the given agent adapters' ListMarketplaces, for
// import adoption. It returns ok=false when no targeted adapter reports the
// marketplace, or reports it with an empty Source — omni must never fabricate
// a source value: an unreadable source is a hard "declare it first" case, not
// a placeholder.
func (a *App) FindUndeclaredMarketplace(ctx context.Context, name string, agentIDs []string) (source string, ok bool, err error) {
	for _, adapter := range a.pluginAdapters() {
		if !adapter.Available() || !pluginTargetsAdapter(config.Plugin{Agents: agentIDs}, adapter.ID()) {
			continue
		}
		existing, listErr := adapter.ListMarketplaces(ctx)
		if listErr != nil {
			continue
		}
		for _, im := range existing {
			if im.Name == name && im.Source != "" {
				return im.Source, true, nil
			}
		}
	}
	return "", false, nil
}

func upsertPlugin(plugins []config.Plugin, p config.Plugin) []config.Plugin {
	for i := range plugins {
		if plugins[i].Name == p.Name {
			plugins[i] = p
			return plugins
		}
	}
	return append(plugins, p)
}

func deletePlugin(plugins []config.Plugin, name string) []config.Plugin {
	out := plugins[:0]
	for _, p := range plugins {
		if p.Name != name {
			out = append(out, p)
		}
	}
	return out
}

func upsertMarketplace(marketplaces []config.Marketplace, m config.Marketplace) []config.Marketplace {
	for i := range marketplaces {
		if marketplaces[i].Name == m.Name {
			marketplaces[i] = m
			return marketplaces
		}
	}
	return append(marketplaces, m)
}

func deleteMarketplace(marketplaces []config.Marketplace, name string) []config.Marketplace {
	out := marketplaces[:0]
	for _, m := range marketplaces {
		if m.Name != name {
			out = append(out, m)
		}
	}
	return out
}

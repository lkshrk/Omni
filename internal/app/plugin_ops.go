package app

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/agent"
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
	// One user-facing line per pair the agent has installed from another marketplace.
	Drift    []string
	Warnings []string
	Errors   []PluginError
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
	// Neither managed nor claimable: the manifest already owns the name, so they need a resolution.
	Drifted map[string][]InstalledPlugin
}

func WithPluginAdapters(adapters []PluginAdapter) func(*App) {
	return func(a *App) {
		var option agent.Option
		if adapters != nil {
			option = agent.WithPluginAdapters(adapters)
		}
		a.setAgentTargetOption(agentTargetsPluginOverride, option)
	}
}

func (a *App) pluginAdapters() []PluginAdapter {
	return a.agentRegistry().PluginAdapters()
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

// Ungrouped plugins restore everywhere; grouped ones restore when the group is active on the host.
func resolvePlugins(cfg *config.RootConfig, hostname string) []config.Plugin {
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
		for _, name := range g.Plugins {
			groupedNames[name] = struct{}{}
			if _, active := activeHostSet[g.BaseName()]; active {
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

// Name alone is not unique across marketplaces.
func pluginIdentity(name, marketplace string) string {
	return name + "\x00" + marketplace
}

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

// Errors are returned for the caller to collect, never fatal to the batch.
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

// RestorePlugins — Marketplaces are refreshed first: installs read from the local clone, so a stale one must never precede them.
func (a *App) RestorePlugins(ctx context.Context, opts RestorePluginOptions) (RestorePluginResult, error) {
	return a.restorePlugins(ctx, opts, true)
}

// RestorePluginsPreRefreshed — For callers that just refreshed marketplaces themselves.
func (a *App) RestorePluginsPreRefreshed(ctx context.Context, opts RestorePluginOptions) (RestorePluginResult, error) {
	return a.restorePlugins(ctx, opts, false)
}

func (a *App) restorePlugins(ctx context.Context, opts RestorePluginOptions, refreshMarketplaces bool) (RestorePluginResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return RestorePluginResult{}, err
	}
	if err := a.requireAgentsEnabled(cfg); err != nil {
		return RestorePluginResult{}, err
	}
	if config.BoolVal(a.effectiveSettings(cfg).PluginsDisabled) {
		return RestorePluginResult{Warnings: []string{"plugins are disabled for this host, skipping sync"}}, nil
	}
	plugins := resolvePlugins(cfg, currentMachineGroupName())
	var res RestorePluginResult
	for _, adapter := range a.pluginAdapters() {
		if !adapter.Available() {
			res.Warnings = append(res.Warnings, fmt.Sprintf("agent %s not available, skipping", adapter.ID()))
			continue
		}
		if !opts.DryRun && refreshMarketplaces {
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
			_, present := alreadyInstalled[pluginIdentity(p.Name, p.Marketplace)]
			// The identity match would otherwise read a foreign-marketplace copy as not installed.
			if live, found := foreignPluginCopy(installed, p); !present && found {
				res.Drift = append(res.Drift, fmt.Sprintf(
					"%s/%s: drifted on %s (installed from %s, manifest declares %s; left untouched); resolve with %s",
					adapter.ID(), p.Name, adapter.ID(), live.Marketplace, p.Marketplace, pluginDriftRemedy))
				continue
			}
			if opts.DryRun {
				if present {
					res.AlreadyInstalled = append(res.AlreadyInstalled, adapter.ID()+"/"+p.Name)
				} else {
					res.WouldInstall = append(res.WouldInstall, adapter.ID()+"/"+p.Name)
				}
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
			if present {
				res.AlreadyInstalled = append(res.AlreadyInstalled, adapter.ID()+"/"+p.Name)
				continue
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
	// Normal on multi-host manifests, an actionable warning on explicit installs.
	SkippedUnavailable []string
}

type UpdatePluginResult struct {
	Errors             []PluginError
	SkippedUnavailable []string
}

// UpdateMarketplacesResult — Errors are data, not fatal: one adapter's refresh failure never stops the others.
type UpdateMarketplacesResult struct {
	Errors []PluginError
}

// UpdateMarketplaces — Callers that follow up with plugin updates should use UpdatePluginsPreRefreshed.
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

func (a *App) UpdatePlugin(ctx context.Context, name string) (UpdatePluginResult, error) {
	return a.UpdatePlugins(ctx, []string{name}, nil)
}

// UpdatePlugins — Each adapter's marketplace is refreshed once up front: a stale clone would install a stale version.
func (a *App) UpdatePlugins(ctx context.Context, names []string, progress func(name string)) (UpdatePluginResult, error) {
	return a.updatePlugins(ctx, names, progress, true)
}

// UpdatePluginsPreRefreshed — For callers that just ran UpdateMarketplaces themselves.
func (a *App) UpdatePluginsPreRefreshed(ctx context.Context, names []string, progress func(name string)) (UpdatePluginResult, error) {
	return a.updatePlugins(ctx, names, progress, false)
}

func (a *App) updatePlugins(ctx context.Context, names []string, progress func(name string), refreshMarketplaces bool) (UpdatePluginResult, error) {
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
	if refreshMarketplaces {
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
	// Presence was unverifiable, so the failure is reported without blocking the manifest delete.
	Warnings []PluginError
}

// AddPlugin — An adapter already listing the identity is skipped: neither agent CLI is documented as idempotent there.
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
		installed, listErr := adapter.ListPlugins(ctx)
		alreadyInstalled := listErr == nil && pluginListed(installed, p.Name, p.Marketplace)
		if !adapter.Available() {
			if alreadyInstalled {
				res.AlreadyInstalled = append(res.AlreadyInstalled, adapter.ID()+"/"+p.Name)
			} else {
				res.SkippedUnavailable = append(res.SkippedUnavailable, adapter.ID()+"/"+p.Name)
			}
			continue
		}
		if mErr := ensureMarketplace(ctx, adapter, *m); mErr != nil {
			res.Errors = append(res.Errors, PluginError{AgentID: adapter.ID(), Name: p.Name, Err: fmt.Errorf("marketplace %s: %w", m.Name, mErr)})
			continue
		}
		if alreadyInstalled {
			res.AlreadyInstalled = append(res.AlreadyInstalled, adapter.ID()+"/"+p.Name)
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

// AddMarketplace — The manifest write is unconditional.
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

// RemovePlugin — Marketplaces are never touched by this call.
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
		installed, listErr := adapter.ListPlugins(ctx)
		if listErr == nil && !pluginListed(installed, target.Name, target.Marketplace) {
			continue
		}
		if !adapter.Available() {
			res.SkippedUnavailable = append(res.SkippedUnavailable, adapter.ID()+"/"+name)
			continue
		}
		if removeErr := adapter.RemovePlugin(ctx, *target); removeErr != nil {
			if listErr != nil {
				// Presence was unverifiable, so report a warning rather than a hard error, but never swallow it.
				res.Warnings = append(res.Warnings, PluginError{AgentID: adapter.ID(), Name: name, Err: fmt.Errorf("uninstall unverified (listing failed: %v): %w", listErr, removeErr)})
				continue
			}
			res.Errors = append(res.Errors, PluginError{AgentID: adapter.ID(), Name: name, Err: removeErr})
		}
	}
	if err := a.withConfig(func(c *config.RootConfig) error {
		c.Agents.Plugins = deletePlugin(c.Agents.Plugins, name)
		setPluginGroupsInConfig(c, name, map[string]struct{}{})
		return nil
	}); err != nil {
		return res, fmt.Errorf("removed %s from agents but failed to update manifest (re-run to persist): %w", name, err)
	}
	return res, nil
}

// RemoveMarketplace — omni never removes a marketplace from an agent CLI: it may still serve hand-installed plugins.
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
		setMarketplaceGroupsInConfig(c, name, map[string]struct{}{})
		return nil
	})
}

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

// PluginByName — The full config.Plugin, unlike the PluginRow projection.
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

// ImportPlugins — Matching is on the full identity, so an install from an undeclared marketplace is drift rather than claimable.
func (a *App) ImportPlugins(ctx context.Context) (PluginImportDiff, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return PluginImportDiff{}, err
	}
	if err := a.requirePluginsEnabled(cfg); err != nil {
		return PluginImportDiff{}, err
	}
	managedNames := make(map[string]bool, len(cfg.Agents.Plugins))
	managedIdentities := make(map[string]bool, len(cfg.Agents.Plugins))
	for _, p := range cfg.Agents.Plugins {
		managedNames[p.Name] = true
		managedIdentities[pluginIdentity(p.Name, p.Marketplace)] = true
	}
	diff := PluginImportDiff{
		Unmanaged: make(map[string][]InstalledPlugin),
		Drifted:   make(map[string][]InstalledPlugin),
	}
	for _, adapter := range a.pluginAdapters() {
		if !adapter.Available() {
			continue
		}
		listed, listErr := adapter.ListPlugins(ctx)
		if listErr != nil {
			continue
		}
		for _, plg := range listed {
			switch {
			case !managedNames[plg.Name]:
				diff.Unmanaged[adapter.ID()] = append(diff.Unmanaged[adapter.ID()], plg)
			// An exact identity match is claimed, whichever other marketplaces also carry the name.
			case plg.Marketplace != "" && !managedIdentities[pluginIdentity(plg.Name, plg.Marketplace)]:
				diff.Drifted[adapter.ID()] = append(diff.Drifted[adapter.ID()], plg)
			}
		}
	}
	return diff, nil
}

// AdoptUnmanagedPlugins — Manifest bookkeeping only; a plugin whose marketplace is undeclared is skipped rather than adopted with a dangling reference.
func (a *App) AdoptUnmanagedPlugins(ctx context.Context) (PluginAdoptResult, error) {
	return a.adoptUnmanagedPlugins(ctx, false)
}

func (a *App) adoptUnmanagedPlugins(ctx context.Context, dryRun bool) (PluginAdoptResult, error) {
	diff, err := a.ImportPlugins(ctx)
	if err != nil {
		return PluginAdoptResult{}, err
	}
	type candidate struct {
		plugin InstalledPlugin
		agents []string
	}
	candidates := make(map[string]candidate)
	agentIDs := make([]string, 0, len(diff.Unmanaged))
	for agentID := range diff.Unmanaged {
		agentIDs = append(agentIDs, agentID)
	}
	sort.Strings(agentIDs)
	var identities []string
	for _, agentID := range agentIDs {
		for _, plg := range diff.Unmanaged[agentID] {
			key := pluginIdentity(plg.Name, plg.Marketplace)
			current, exists := candidates[key]
			if !exists {
				candidates[key] = candidate{plugin: plg, agents: []string{agentID}}
				identities = append(identities, key)
				continue
			}
			if !slices.Contains(current.agents, agentID) {
				current.agents = append(current.agents, agentID)
				candidates[key] = current
			}
		}
	}
	sort.Strings(identities)
	var res PluginAdoptResult
	claim := func(c *config.RootConfig) error {
		managed := make(map[string]struct{}, len(c.Agents.Plugins))
		for _, p := range c.Agents.Plugins {
			managed[pluginIdentity(p.Name, p.Marketplace)] = struct{}{}
		}
		for _, key := range identities {
			cand := candidates[key]
			if _, ok := managed[key]; ok {
				continue
			}
			if findMarketplace(c.Agents.Marketplaces, cand.plugin.Marketplace) == nil {
				res.Skipped = append(res.Skipped, undeclaredMarketplaceSkip(cand.plugin.Name, cand.plugin.Marketplace))
				continue
			}
			managed[key] = struct{}{}
			if dryRun {
				res.WouldAdopt = append(res.WouldAdopt, fmt.Sprintf("would claim plugin %q from %s on %s",
					cand.plugin.Name, cand.plugin.Marketplace, strings.Join(cand.agents, ", ")))
				continue
			}
			c.Agents.Plugins = append(c.Agents.Plugins, config.Plugin{
				Name:        cand.plugin.Name,
				Marketplace: cand.plugin.Marketplace,
				Agents:      cand.agents,
			})
			res.Adopted++
		}
		return nil
	}
	if dryRun {
		cfg, err := a.loadConfig()
		if err != nil {
			return res, err
		}
		return res, claim(cfg)
	}
	return res, a.withConfig(claim)
}

// PluginAdoptResult — Skips are data, not failure: one undeclared marketplace must not stop the rest of a claim pass.
type PluginAdoptResult struct {
	Adopted int
	Skipped []string
	// Populated only on a dry run; one preview line per plugin that would be claimed.
	WouldAdopt []string
}

// Shared by adoption and use-local resolution so both name the same missing declaration.
func undeclaredMarketplaceSkip(name, marketplace string) string {
	return fmt.Sprintf(
		"plugin %q comes from marketplace %q, which is not declared; add it with \"omni agents plugins marketplace add\" first",
		name, marketplace)
}

func (a *App) Marketplaces() ([]config.Marketplace, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	return cfg.Agents.Marketplaces, nil
}

// FindUndeclaredMarketplace — Never fabricates a source: an unreadable one is a hard declare-it-first case, not a placeholder.
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

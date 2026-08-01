package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/lkshrk/omni/internal/config"
)

type PluginDriftStrategy string

const (
	PluginDriftUseManaged PluginDriftStrategy = "use_managed"
	PluginDriftUseLocal   PluginDriftStrategy = "use_local"
)

// ResolvePluginDriftOptions — Agents defaults to every agent the plugin is drifted on.
type ResolvePluginDriftOptions struct {
	Name     string
	Agents   []string
	Strategy PluginDriftStrategy
	DryRun   bool
}

type PluginDriftResolution struct {
	Name     string
	Agents   []string
	Actions  []string
	Warnings []string
}

// ResolvePluginDrift — UseLocal works only for a marketplace omni already knows: a manifest plugin may not reference an undeclared one.
func (a *App) ResolvePluginDrift(ctx context.Context, opts ResolvePluginDriftOptions) (PluginDriftResolution, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return PluginDriftResolution{}, err
	}
	if err := a.requirePluginsEnabled(cfg); err != nil {
		return PluginDriftResolution{}, err
	}
	name := strings.TrimSpace(opts.Name)
	target := findPlugin(cfg.Agents.Plugins, name)
	if target == nil {
		return PluginDriftResolution{}, fmt.Errorf("plugin %q is not in this host's manifest", name)
	}
	live, err := a.driftedPluginInstalls(ctx, *target)
	if err != nil {
		return PluginDriftResolution{}, err
	}
	selected, err := driftedPluginResolveTargets(*target, a.pluginAdapters(), live, opts.Agents)
	if err != nil {
		return PluginDriftResolution{}, err
	}
	res := PluginDriftResolution{Name: name, Agents: selected}
	if opts.Strategy == PluginDriftUseLocal {
		return a.resolvePluginDriftUseLocal(res, *target, selected, live, opts.DryRun)
	}
	return a.resolvePluginDriftUseManaged(ctx, res, cfg, *target, selected, live, opts.DryRun)
}

func (a *App) driftedPluginInstalls(ctx context.Context, target config.Plugin) (map[string]InstalledPlugin, error) {
	out := make(map[string]InstalledPlugin)
	for _, adapter := range a.pluginAdapters() {
		if !pluginTargetsAdapter(target, adapter.ID()) || !adapterSupportsPlugin(adapter, target) || !adapter.Available() {
			continue
		}
		listed, listErr := adapter.ListPlugins(ctx)
		if listErr != nil {
			return nil, fmt.Errorf("list plugins for %s: %w", adapter.ID(), listErr)
		}
		for _, plg := range listed {
			if plg.Name != target.Name {
				continue
			}
			if pluginMarketplaceDrifted(target.Marketplace, plg.Marketplace) {
				out[adapter.ID()] = plg
			}
			break
		}
	}
	return out, nil
}

func driftedPluginResolveTargets(
	target config.Plugin,
	adapters []PluginAdapter,
	live map[string]InstalledPlugin,
	requested []string,
) ([]string, error) {
	if len(requested) == 0 {
		drifted := make([]string, 0, len(live))
		for id := range live {
			drifted = append(drifted, id)
		}
		if len(drifted) == 0 {
			return nil, fmt.Errorf("plugin %s is not drifted on any target agent", target.Name)
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
		if !known[id] || !pluginTargetsAdapter(target, id) {
			return nil, fmt.Errorf("plugin %s does not target agent %q", target.Name, id)
		}
		if _, drifted := live[id]; !drifted {
			return nil, fmt.Errorf("plugin %s is not drifted on %s", target.Name, id)
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (a *App) resolvePluginDriftUseManaged(
	ctx context.Context,
	res PluginDriftResolution,
	cfg *config.RootConfig,
	target config.Plugin,
	selected []string,
	live map[string]InstalledPlugin,
	dryRun bool,
) (PluginDriftResolution, error) {
	byID := make(map[string]PluginAdapter)
	for _, adapter := range a.pluginAdapters() {
		byID[adapter.ID()] = adapter
	}
	for _, id := range selected {
		res.Actions = append(res.Actions, fmt.Sprintf(
			"reinstall %s on %s from %s (replacing the copy from %s)",
			target.Name, id, target.Marketplace, live[id].Marketplace))
	}
	if dryRun {
		return res, nil
	}
	market := findMarketplace(cfg.Agents.Marketplaces, target.Marketplace)
	var errs []error
	for _, id := range selected {
		adapter, ok := byID[id]
		if !ok {
			res.Warnings = append(res.Warnings, fmt.Sprintf("agent %s is no longer registered, skipping", id))
			continue
		}
		// The marketplace is prepared before the uninstall so an unreachable one cannot leave the agent with no copy at all.
		if market != nil {
			if err := ensureMarketplace(ctx, adapter, *market); err != nil {
				errs = append(errs, fmt.Errorf("%s: marketplace %s: %w", id, market.Name, err))
				continue
			}
		}
		foreign := config.Plugin{Name: target.Name, Marketplace: live[id].Marketplace, Agents: target.Agents}
		if err := adapter.RemovePlugin(ctx, foreign); err != nil {
			errs = append(errs, fmt.Errorf("%s: uninstall the copy from %s: %w", id, foreign.Marketplace, err))
			continue
		}
		if err := adapter.InstallPlugin(ctx, target); err != nil {
			errs = append(errs, reinstallPreviousPlugin(ctx, adapter, id, target, foreign, err))
		}
	}
	if len(errs) > 0 {
		return res, fmt.Errorf("resolving %s: %w", target.Name, errors.Join(errs...))
	}
	return res, nil
}

// Leaving the agent with no copy is worse than unresolved drift, so the removed plugin goes back even when ctx is already cancelled.
func reinstallPreviousPlugin(
	ctx context.Context,
	adapter PluginAdapter,
	id string,
	target, previous config.Plugin,
	cause error,
) error {
	restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancel()
	if restoreErr := adapter.InstallPlugin(restoreCtx, previous); restoreErr != nil {
		return fmt.Errorf("%s: install from %s: %w; restore the copy from %s: %v",
			id, target.Marketplace, cause, previous.Marketplace, restoreErr)
	}
	return fmt.Errorf("%s: install from %s: %w (the copy from %s was restored)",
		id, target.Marketplace, cause, previous.Marketplace)
}

// Refuses an undeclared marketplace: no restore could ever satisfy a dangling reference.
func (a *App) resolvePluginDriftUseLocal(
	res PluginDriftResolution,
	target config.Plugin,
	selected []string,
	live map[string]InstalledPlugin,
	dryRun bool,
) (PluginDriftResolution, error) {
	adopted := live[selected[0]].Marketplace
	for _, id := range selected[1:] {
		if live[id].Marketplace != adopted {
			return res, fmt.Errorf(
				"plugin %q is installed from different marketplaces on %s; resolve one agent at a time with --agent",
				target.Name, strings.Join(selected, ", "))
		}
	}
	cfg, err := a.loadConfig()
	if err != nil {
		return res, err
	}
	if findMarketplace(cfg.Agents.Marketplaces, adopted) == nil {
		return res, errors.New(undeclaredMarketplaceSkip(target.Name, adopted))
	}
	updated := target
	updated.Marketplace = adopted
	res.Actions = append(res.Actions, fmt.Sprintf(
		"repoint %s in the manifest from %s to %s (installed on %s)",
		target.Name, target.Marketplace, adopted, strings.Join(selected, ", ")))
	if dryRun {
		return res, nil
	}
	return res, a.withConfig(func(c *config.RootConfig) error {
		if findPlugin(c.Agents.Plugins, target.Name) == nil {
			return fmt.Errorf("plugin %q not found", target.Name)
		}
		c.Agents.Plugins = upsertPlugin(c.Agents.Plugins, updated)
		return nil
	})
}

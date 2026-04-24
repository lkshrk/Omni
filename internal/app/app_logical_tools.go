package app

import (
	"context"
	"fmt"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
)

// SetTool upserts the default install spec for a logical tool. It does not add
// the tool to any group; callers must explicitly assign memberships.
func (a *App) SetTool(name, providerName, packageName, installWith string) error {
	if name == "" {
		return fmt.Errorf("tool name is required")
	}
	if providerName == "" {
		return fmt.Errorf("provider is required")
	}
	if !a.knownProvider(providerName) {
		return fmt.Errorf("unknown provider %q", providerName)
	}
	if !a.knownEcosystemProvider(providerName) {
		return fmt.Errorf("provider %q is not an ecosystem provider", providerName)
	}
	if err := a.validateInstallWith(providerName, installWith); err != nil {
		return err
	}

	return a.withConfig(func(cfg *config.RootConfig) error {
		if cfg.Tools == nil {
			cfg.Tools = make(map[string]config.ToolSpec)
		}
		spec := cfg.Tools[name]
		spec.Provider = providerName
		spec.Package = packageName
		spec.InstallWith = installWith
		cfg.Tools[name] = spec
		return nil
	})
}

// RemoveLogicalTool deletes a logical tool spec and all group memberships for it.
func (a *App) RemoveLogicalTool(ctx context.Context, name string) error {
	if name == "" {
		return fmt.Errorf("tool name is required")
	}

	if err := a.withConfig(func(cfg *config.RootConfig) error {
		changed := false
		if _, ok := cfg.Tools[name]; ok {
			delete(cfg.Tools, name)
			changed = true
		}
		for _, g := range cfg.Groups {
			if filterToolMemberships(g, name) {
				changed = true
			}
			if filterGroupIgnore(g, name) {
				changed = true
			}
		}
		if !changed {
			return errSkipSave
		}
		return nil
	}); err != nil {
		return err
	}

	return a.deleteCachedLogicalTools(ctx, []string{name})
}

// AddToolToGroup appends a logical tool membership to a group. The logical tool
// must already exist in RootConfig.Tools.
func (a *App) AddToolToGroup(name, groupName string) error {
	if name == "" {
		return fmt.Errorf("tool name is required")
	}

	return a.withConfig(func(cfg *config.RootConfig) error {
		if _, ok := cfg.Tools[name]; !ok {
			return fmt.Errorf("logical tool %q not found; run 'omni tools set %s --provider <ecosystem-provider>' first", name, name)
		}
		group := ensureGroupInConfig(cfg, groupName)
		if containsToolMembership(group.Tools, name) {
			return errSkipSave
		}
		group.Tools = append(group.Tools, config.ToolEntry{Name: name})
		return nil
	})
}

// RemoveToolFromGroup removes a logical tool membership from a group. The tool
// spec is left intact; use RemoveLogicalTool to delete the spec itself.
func (a *App) RemoveToolFromGroup(name, groupName string) error {
	if name == "" {
		return fmt.Errorf("tool name is required")
	}

	return a.withConfig(func(cfg *config.RootConfig) error {
		group := findGroupInConfig(cfg, groupName)
		if group == nil {
			return fmt.Errorf("group %q not found", groupName)
		}
		if !filterToolMemberships(group, name) {
			return errSkipSave
		}
		return nil
	})
}

func (a *App) SetToolIgnore(name string, ignored bool) error {
	if name == "" {
		return fmt.Errorf("tool name is required")
	}

	return a.withConfig(func(cfg *config.RootConfig) error {
		spec, ok := cfg.Tools[name]
		if !ok {
			return fmt.Errorf("logical tool %q not found", name)
		}
		spec.Ignore = ignored
		cfg.Tools[name] = spec
		return nil
	})
}

func (a *App) SetGroupIgnore(groupName, name string, ignored bool) error {
	if name == "" {
		return fmt.Errorf("tool name is required")
	}

	return a.withConfig(func(cfg *config.RootConfig) error {
		if _, ok := cfg.Tools[name]; !ok {
			return fmt.Errorf("logical tool %q not found", name)
		}
		group := findGroupInConfig(cfg, groupName)
		if group == nil {
			return fmt.Errorf("group %q not found", groupName)
		}
		if ignored {
			for _, existing := range group.Ignore {
				if existing == name {
					return errSkipSave
				}
			}
			group.Ignore = append(group.Ignore, name)
		} else {
			filterGroupIgnore(group, name)
		}
		return nil
	})
}

func (a *App) SetToolHostInstallSpec(name, providerName, packageName, installWith string) error {
	return a.setToolInstallSpec(name, shortHostname(currentHostname()), providerName, packageName, installWith)
}

func (a *App) SetToolDefaultInstallSpec(name, providerName, packageName, installWith string) error {
	return a.setToolInstallSpec(name, "", providerName, packageName, installWith)
}

func (a *App) setToolInstallSpec(name, host, providerName, packageName, installWith string) error {
	if name == "" {
		return fmt.Errorf("tool name is required")
	}
	targetProvider, targetInstallWith := a.logicalInstallTarget(installWith)
	if providerName != "" {
		targetProvider = providerName
		targetInstallWith = installWith
	}
	if targetProvider == "" {
		return fmt.Errorf("provider is required")
	}
	if !a.knownEcosystemProvider(targetProvider) {
		return fmt.Errorf("provider %q is not an ecosystem provider", targetProvider)
	}
	if err := a.validateInstallWith(targetProvider, targetInstallWith); err != nil {
		return err
	}

	return a.withConfig(func(cfg *config.RootConfig) error {
		spec, ok := cfg.Tools[name]
		if !ok {
			return fmt.Errorf("logical tool %q not found", name)
		}
		if packageName == "" {
			packageName = spec.Package
		}
		if packageName == name {
			packageName = ""
		}
		install := config.ToolInstallSpec{Provider: targetProvider, Package: packageName, InstallWith: targetInstallWith, Options: spec.Options}
		if host != "" {
			if spec.Hosts == nil {
				spec.Hosts = make(map[string]config.ToolInstallSpec)
			}
			spec.Hosts[host] = install
		} else {
			spec.Provider = install.Provider
			spec.Package = install.Package
			spec.InstallWith = install.InstallWith
			spec.Options = install.Options
		}
		cfg.Tools[name] = spec
		return nil
	})
}

func (a *App) knownProvider(providerName string) bool {
	if a.registry != nil {
		if _, ok := a.registry.Get(providerName); ok {
			return true
		}
	}
	for _, known := range provider.BuiltinKnownNames() {
		if providerName == known {
			return true
		}
	}
	return false
}

func (a *App) knownEcosystemProvider(providerName string) bool {
	if a.registry != nil && a.registry.IsEcosystemProvider(providerName) {
		return true
	}
	return provider.BuiltinIsEcosystem(providerName)
}

func (a *App) validateInstallWith(providerName, installWith string) error {
	if installWith == "" {
		return nil
	}
	validation := a.providerValidation()
	known := make(map[string]struct{}, len(validation.Known))
	for _, name := range validation.Known {
		known[name] = struct{}{}
	}
	if len(known) > 0 {
		if _, ok := known[installWith]; !ok {
			return fmt.Errorf("unknown concrete provider/manager %q", installWith)
		}
	}
	for _, ecosystem := range validation.Ecosystems {
		if installWith == ecosystem {
			return fmt.Errorf("install_with %q must be a concrete provider or manager", installWith)
		}
	}
	if ecosystem := validation.ConcreteEcosystems[installWith]; ecosystem != "" && ecosystem != providerName {
		return fmt.Errorf("install_with %q belongs to ecosystem %q, not %q", installWith, ecosystem, providerName)
	}
	return nil
}

func filterToolMemberships(group *config.GroupConfig, name string) bool {
	filtered := group.Tools[:0]
	changed := false
	for _, tool := range group.Tools {
		if tool.Name == name {
			changed = true
			continue
		}
		filtered = append(filtered, tool)
	}
	group.Tools = filtered
	return changed
}

func filterGroupIgnore(group *config.GroupConfig, name string) bool {
	filtered := group.Ignore[:0]
	changed := false
	for _, ignored := range group.Ignore {
		if ignored == name {
			changed = true
			continue
		}
		filtered = append(filtered, ignored)
	}
	group.Ignore = filtered
	return changed
}

func (a *App) deleteCachedLogicalTools(ctx context.Context, names []string) error {
	if ctx == nil || len(names) == 0 || a.readDB() == nil {
		return nil
	}
	nameSet := make(map[string]struct{}, len(names))
	for _, name := range names {
		nameSet[name] = struct{}{}
	}
	cached, err := a.readDB().List(ctx)
	if err != nil {
		return err
	}
	for _, t := range cached {
		if _, ok := nameSet[t.Name]; !ok {
			continue
		}
		if err := a.readDB().Delete(ctx, t.Name, t.Provider, t.Package); err != nil {
			return err
		}
	}
	return nil
}

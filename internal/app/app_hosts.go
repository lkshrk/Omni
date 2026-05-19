package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
)

// ActiveHostInfo returns the current host group and reusable group assignments.
// The physical host group is included first when the host is configured.
func (a *App) ActiveHostInfo() (hostname string, groups []string, ok bool) {
	cfg, err := a.loadConfig()
	if err != nil {
		return "", nil, false
	}
	hostname = currentMachineGroupName()
	names, found := activeHostGroupNames(cfg, hostname)
	if !found {
		return hostname, nil, false
	}
	return hostname, names, true
}

type HostInfo struct {
	Hosts  map[string]config.HostAssignment
	Active string
}

func (a *App) HostStatus() (*HostInfo, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	return a.hostStatusFromConfig(cfg), nil
}

func (a *App) hostStatusFromConfig(cfg *config.RootConfig) *HostInfo {
	hosts := make(map[string]config.HostAssignment, len(cfg.Hosts))
	for host, groups := range cfg.Hosts {
		hosts[host] = config.HostAssignment{Groups: append([]string(nil), groups...)}
	}
	active := currentMachineGroupName()
	if _, ok := hosts[active]; !ok {
		active = ""
	} else {
		host := hosts[active]
		host.Ignore = append([]string(nil), cfg.Ignore.Tools...)
		hosts[active] = host
	}
	return &HostInfo{Hosts: hosts, Active: active}
}

func (a *App) repairCurrentHostEntry() error {
	hostname := currentMachineGroupName()
	if hostname == "" {
		return nil
	}
	return a.withConfig(func(cfg *config.RootConfig) error {
		if _, ok := cfg.Hosts[hostname]; ok {
			return nil
		}
		if len(cfg.Hosts) > 0 {
			return nil
		}
		group := findGroupInConfig(cfg, hostname)
		if group == nil {
			return nil
		}
		if !group.IsHost() {
			group.Special = "host"
		}
		if cfg.Hosts == nil {
			cfg.Hosts = make(map[string][]string)
		}
		cfg.Hosts[hostname] = []string{}
		return nil
	})
}

// EnsureHost creates the physical special hostname group and a host assignment
// entry. Reusable groups remain opt-in and are not added implicitly.
func (a *App) EnsureHost(hostname string) error {
	hostname = strings.TrimSpace(machineGroupName(hostname))
	if hostname == "" {
		return fmt.Errorf("hostname is required")
	}
	return a.withConfig(func(cfg *config.RootConfig) error {
		if _, err := ensureHostGroupInConfig(cfg, hostname); err != nil {
			return err
		}
		if cfg.Hosts == nil {
			cfg.Hosts = make(map[string][]string)
		}
		if _, ok := cfg.Hosts[hostname]; !ok {
			cfg.Hosts[hostname] = []string{}
		}
		return nil
	})
}

func (a *App) RenameHost(oldName, newName string) error {
	oldName = strings.TrimSpace(machineGroupName(oldName))
	newName = strings.TrimSpace(machineGroupName(newName))
	if oldName == "" || newName == "" {
		return fmt.Errorf("hostname is required")
	}
	if oldName == newName {
		return nil
	}
	return a.withConfig(func(cfg *config.RootConfig) error {
		groups, ok := cfg.Hosts[oldName]
		if !ok {
			return fmt.Errorf("host %q not found", oldName)
		}
		if _, exists := cfg.Hosts[newName]; exists {
			return fmt.Errorf("host %q already exists", newName)
		}
		if findGroupInConfig(cfg, newName) != nil {
			return fmt.Errorf("group %q already exists", newName)
		}
		if oldGroup := findGroupInConfig(cfg, oldName); oldGroup != nil {
			if !oldGroup.IsHost() {
				return fmt.Errorf("group %q already exists and is not a host group", oldName)
			}
			oldGroup.Name = newName
			oldGroup.Special = "host"
		} else if _, err := ensureHostGroupInConfig(cfg, newName); err != nil {
			return err
		}
		if err := moveHostScopedConfig(cfg, oldName, newName); err != nil {
			return err
		}
		cfg.Hosts[newName] = append([]string(nil), groups...)
		delete(cfg.Hosts, oldName)
		return nil
	})
}

// CopyHostConfig copies reusable group assignments and host-scoped settings
// from sourceName to targetName. The target's protected host group is ensured
// but source host-local tool/dot memberships are not duplicated.
func (a *App) CopyHostConfig(sourceName, targetName string) error {
	sourceName = strings.TrimSpace(machineGroupName(sourceName))
	targetName = strings.TrimSpace(machineGroupName(targetName))
	if sourceName == "" || targetName == "" {
		return fmt.Errorf("hostname is required")
	}
	if sourceName == targetName {
		return a.EnsureHost(targetName)
	}
	return a.withConfig(func(cfg *config.RootConfig) error {
		sourceGroups, ok := cfg.Hosts[sourceName]
		if !ok {
			return fmt.Errorf("host %q not found", sourceName)
		}
		if _, err := ensureHostGroupInConfig(cfg, targetName); err != nil {
			return err
		}
		if cfg.Hosts == nil {
			cfg.Hosts = make(map[string][]string)
		}
		groups, err := reusableHostGroupsInConfig(cfg, targetName, sourceGroups)
		if err != nil {
			return err
		}
		cfg.Hosts[targetName] = groups
		copyHostSettings(cfg, sourceName, targetName)
		copyToolHostOverrides(cfg, sourceName, targetName)
		copyDotHostVariants(cfg, sourceName, targetName)
		return nil
	})
}

func copyHostSettings(cfg *config.RootConfig, sourceName, targetName string) {
	if cfg.HostSettings == nil {
		return
	}
	settings, ok := cfg.HostSettings[sourceName]
	if !ok {
		delete(cfg.HostSettings, targetName)
		return
	}
	cfg.HostSettings[targetName] = cloneHostSettings(settings)
}

func cloneHostSettings(settings config.Settings) config.Settings {
	settings.DisabledProviders = append([]string(nil), settings.DisabledProviders...)
	if settings.DotsDisabled != nil {
		dotsDisabled := *settings.DotsDisabled
		settings.DotsDisabled = &dotsDisabled
	}
	if settings.Ecosystems != nil {
		ecosystems := make(map[string]config.EcosystemSettings, len(settings.Ecosystems))
		for name, eco := range settings.Ecosystems {
			eco.Priority = append([]string(nil), eco.Priority...)
			ecosystems[name] = eco
		}
		settings.Ecosystems = ecosystems
	}
	return settings
}

func copyToolHostOverrides(cfg *config.RootConfig, sourceName, targetName string) {
	for name, spec := range cfg.Tools {
		if spec.Hosts == nil {
			continue
		}
		delete(spec.Hosts, targetName)
		if install, ok := spec.Hosts[sourceName]; ok {
			spec.Hosts[targetName] = cloneToolInstallSpec(install)
		}
		if len(spec.Hosts) == 0 {
			spec.Hosts = nil
		}
		cfg.Tools[name] = spec
	}
}

func cloneToolInstallSpec(spec config.ToolInstallSpec) config.ToolInstallSpec {
	if spec.Options != nil {
		options := make(map[string]string, len(spec.Options))
		for key, value := range spec.Options {
			options[key] = value
		}
		spec.Options = options
	}
	return spec
}

func copyDotHostVariants(cfg *config.RootConfig, sourceName, targetName string) {
	for _, group := range cfg.Groups {
		if group == nil || group.IsHost() {
			continue
		}
		for i, dot := range group.Dots {
			if dot.Hosts == nil {
				continue
			}
			delete(dot.Hosts, targetName)
			if variant, ok := dot.Hosts[sourceName]; ok {
				dot.Hosts[targetName] = variant
			}
			if len(dot.Hosts) == 0 {
				dot.Hosts = nil
			}
			group.Dots[i] = dot
		}
	}
}

func moveHostScopedConfig(cfg *config.RootConfig, oldName, newName string) error {
	if cfg.HostSettings != nil {
		if settings, ok := cfg.HostSettings[oldName]; ok {
			if _, exists := cfg.HostSettings[newName]; exists {
				return fmt.Errorf("host settings for %q already exist", newName)
			}
			cfg.HostSettings[newName] = settings
			delete(cfg.HostSettings, oldName)
		}
	}
	for name, spec := range cfg.Tools {
		if spec.Hosts == nil {
			continue
		}
		install, ok := spec.Hosts[oldName]
		if !ok {
			continue
		}
		if _, exists := spec.Hosts[newName]; exists {
			return fmt.Errorf("tool %q already has host override for %q", name, newName)
		}
		spec.Hosts[newName] = install
		delete(spec.Hosts, oldName)
		cfg.Tools[name] = spec
	}
	for _, group := range cfg.Groups {
		if group == nil {
			continue
		}
		for i, dot := range group.Dots {
			if dot.Hosts == nil {
				continue
			}
			variant, ok := dot.Hosts[oldName]
			if !ok {
				continue
			}
			if _, exists := dot.Hosts[newName]; exists {
				return fmt.Errorf("dotfile %q already has host variant for %q", dot.Name, newName)
			}
			dot.Hosts[newName] = variant
			delete(dot.Hosts, oldName)
			group.Dots[i] = dot
		}
	}
	return nil
}

func removeHostScopedConfig(cfg *config.RootConfig, hostname string) {
	delete(cfg.HostSettings, hostname)
	for name, spec := range cfg.Tools {
		if spec.Hosts == nil {
			continue
		}
		delete(spec.Hosts, hostname)
		if len(spec.Hosts) == 0 {
			spec.Hosts = nil
		}
		cfg.Tools[name] = spec
	}
	for _, group := range cfg.Groups {
		if group == nil {
			continue
		}
		for i, dot := range group.Dots {
			if dot.Hosts == nil {
				continue
			}
			delete(dot.Hosts, hostname)
			if len(dot.Hosts) == 0 {
				dot.Hosts = nil
			}
			group.Dots[i] = dot
		}
	}
}

func (a *App) SetHostGroups(hostname string, groups []string) error {
	hostname = strings.TrimSpace(machineGroupName(hostname))
	if hostname == "" {
		return fmt.Errorf("hostname is required")
	}
	return a.withConfig(func(cfg *config.RootConfig) error {
		if _, err := ensureHostGroupInConfig(cfg, hostname); err != nil {
			return err
		}
		if cfg.Hosts == nil {
			cfg.Hosts = make(map[string][]string)
		}
		filtered, err := reusableHostGroupsInConfig(cfg, hostname, groups)
		if err != nil {
			return err
		}
		cfg.Hosts[hostname] = filtered
		return nil
	})
}

func reusableHostGroupsInConfig(cfg *config.RootConfig, hostname string, groups []string) ([]string, error) {
	seen := make(map[string]struct{}, len(groups))
	filtered := make([]string, 0, len(groups))
	for _, group := range groups {
		group = strings.TrimSpace(group)
		if group == "" || group == hostname {
			continue
		}
		if _, ok := seen[group]; ok {
			continue
		}
		seen[group] = struct{}{}
		if g := findGroupInConfig(cfg, group); g == nil {
			return nil, fmt.Errorf("group %q not found", group)
		} else if g.IsHost() {
			return nil, fmt.Errorf("host group %q cannot be assigned to another host", group)
		}
		filtered = append(filtered, group)
	}
	return filtered, nil
}

func (a *App) AddGroupToHost(hostname, group string) error {
	hostname = strings.TrimSpace(machineGroupName(hostname))
	group = strings.TrimSpace(group)
	if hostname == "" {
		return fmt.Errorf("hostname is required")
	}
	if group == "" {
		return fmt.Errorf("group is required")
	}
	if group == hostname {
		return a.EnsureHost(hostname)
	}
	return a.withConfig(func(cfg *config.RootConfig) error {
		if _, err := ensureHostGroupInConfig(cfg, hostname); err != nil {
			return err
		}
		if cfg.Hosts == nil {
			cfg.Hosts = make(map[string][]string)
		}
		if g := findGroupInConfig(cfg, group); g == nil {
			return fmt.Errorf("group %q not found", group)
		} else if g.IsHost() {
			return fmt.Errorf("host group %q cannot be assigned to another host", group)
		}
		if slices.Contains(cfg.Hosts[hostname], group) {
			return errSkipSave
		}
		cfg.Hosts[hostname] = append(cfg.Hosts[hostname], group)
		return nil
	})
}

func (a *App) RemoveGroupFromHost(hostname, group string) error {
	hostname = strings.TrimSpace(machineGroupName(hostname))
	group = strings.TrimSpace(group)
	if hostname == "" {
		return fmt.Errorf("hostname is required")
	}
	return a.withConfig(func(cfg *config.RootConfig) error {
		groups, ok := cfg.Hosts[hostname]
		if !ok {
			return errSkipSave
		}
		filtered := groups[:0]
		for _, g := range groups {
			if g != group {
				filtered = append(filtered, g)
			}
		}
		cfg.Hosts[hostname] = filtered
		return nil
	})
}

func (a *App) RemoveHost(hostname string) error {
	hostname = strings.TrimSpace(machineGroupName(hostname))
	if hostname == "" {
		return fmt.Errorf("hostname is required")
	}
	return a.withConfig(func(cfg *config.RootConfig) error {
		delete(cfg.Hosts, hostname)
		removeHostScopedConfig(cfg, hostname)
		filtered := cfg.Groups[:0]
		for _, group := range cfg.Groups {
			if group != nil && group.BaseName() == hostname && group.IsHost() {
				continue
			}
			filtered = append(filtered, group)
		}
		cfg.Groups = filtered
		return nil
	})
}

func (a *App) RequireActiveHost() error {
	cfg, err := a.loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	hostname := currentMachineGroupName()
	if _, ok := activeHostGroupNames(cfg, hostname); !ok {
		return fmt.Errorf("no host configuration for %q - run 'omni bootstrap' to set one up", hostname)
	}
	return nil
}

// syncOrphansToMachineGroup discovers locally installed tools that are not
// covered by any of activeGroups and appends them to the special host group.
func (a *App) syncOrphansToMachineGroup(ctx context.Context, activeGroups []*config.GroupConfig) error {
	resolvedEcosystems := a.ResolvedEcosystemProviders(ctx)
	revEcosystem := make(map[string]string)
	for eco, concrete := range resolvedEcosystems {
		revEcosystem[concrete] = eco
	}

	covered := make(map[string]struct{})
	coveredNames := make(map[string]struct{})
	for _, g := range activeGroups {
		for _, t := range g.Tools {
			coveredNames[t.Name] = struct{}{}
		}
	}

	machineGroup := currentMachineGroupName()

	return a.withConfig(func(cfg *config.RootConfig) error {
		hg, err := ensureHostGroupInConfig(cfg, machineGroup)
		if err != nil {
			return err
		}
		for _, t := range hg.Tools {
			coveredNames[t.Name] = struct{}{}
		}

		var orphans []config.ToolEntry
		// Best-effort: per-provider errors are skipped so one bad provider
		// doesn't prevent discovering orphans from the rest.
		_ = a.forEachAvailable(ctx, func(p provider.Provider) error { //nolint:errcheck // best-effort orphan scan
			if a.registry.ImportSkipsProvider(p.Name()) {
				return nil
			}
			installed, err := p.ListInstalled(ctx)
			if err != nil {
				return nil
			}
			for _, t := range installed {
				configProvider := a.searchResultConfigProvider(p.Name())
				if eco, ok := revEcosystem[p.Name()]; ok {
					configProvider = eco
				}
				installWith := configInstallWithForConcreteProvider(configProvider, p.Name(), resolvedEcosystems)
				key := configProvider + "\x00" + t.Name
				if _, ok := covered[key]; ok {
					continue
				}
				if _, ok := coveredNames[t.Name]; ok {
					continue
				}
				if cfg.Tools == nil {
					cfg.Tools = make(map[string]config.ToolSpec)
				}
				spec := cfg.Tools[t.Name]
				spec.Provider = configProvider
				spec.InstallWith = installWith
				cfg.Tools[t.Name] = spec
				orphans = append(orphans, config.ToolEntry{Name: t.Name})
				covered[key] = struct{}{}
				coveredNames[t.Name] = struct{}{}
			}
			return nil
		})

		if len(orphans) == 0 {
			return errSkipSave
		}
		hg.Tools = append(hg.Tools, orphans...)
		return nil
	})
}

// CheckSatisfiedGroups returns reusable group names that are not active for the
// host and whose every tool is recorded as installed.
func (a *App) CheckSatisfiedGroups(ctx context.Context, activeGroupNames []string) ([]string, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}

	cached, err := a.readDB().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading tool cache: %w", err)
	}
	installedSet := make(map[string]struct{}, len(cached))
	for _, c := range cached {
		if c.Installed {
			installedSet[c.Name+"\x00"+c.Provider] = struct{}{}
			installedSet[NewToolKey(c.Name, c.Provider, c.Package).String()] = struct{}{}
		}
	}

	activeSet := make(map[string]struct{}, len(activeGroupNames))
	for _, n := range activeGroupNames {
		activeSet[n] = struct{}{}
	}

	var satisfied []string
	for _, g := range cfg.Groups {
		if g == nil || g.IsHost() {
			continue
		}
		if _, active := activeSet[g.BaseName()]; active {
			continue
		}
		if len(g.Tools) == 0 {
			continue
		}
		tools, _ := a.resolvedToolEntries(ctx, cfg, []*config.GroupConfig{g})
		allInstalled := true
		for _, t := range tools {
			if _, ok := installedSet[resolvedToolKey(t)]; !ok {
				if _, ok := installedSet[t.Name+"\x00"+t.Provider]; ok {
					continue
				}
				allInstalled = false
				break
			}
		}
		if allInstalled {
			satisfied = append(satisfied, g.BaseName())
		}
	}
	return satisfied, nil
}

func (a *App) CreateGroup(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("group name cannot be empty")
	}
	return a.withConfig(func(cfg *config.RootConfig) error {
		for _, g := range cfg.Groups {
			if g.BaseName() == name {
				return fmt.Errorf("group %q already exists", name)
			}
		}
		cfg.Groups = append(cfg.Groups, &config.GroupConfig{Name: name})
		return nil
	})
}

func (a *App) RenameGroup(oldName, newName string) error {
	newName = strings.TrimSpace(newName)
	if oldName == "" {
		return fmt.Errorf("group name cannot be empty")
	}
	if newName == "" {
		return fmt.Errorf("group name cannot be empty")
	}
	if oldName == newName {
		return nil
	}
	return a.withConfig(func(cfg *config.RootConfig) error {
		for _, g := range cfg.Groups {
			if g.BaseName() == newName {
				return fmt.Errorf("group %q already exists", newName)
			}
		}
		found := false
		for _, g := range cfg.Groups {
			if g.BaseName() == oldName {
				if g.IsHost() {
					return fmt.Errorf("host group %q cannot be renamed", oldName)
				}
				g.Name = newName
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("group %q not found", oldName)
		}
		for host, groups := range cfg.Hosts {
			for i, name := range groups {
				if name == oldName {
					cfg.Hosts[host][i] = newName
				}
			}
		}
		return nil
	})
}

type DeleteGroupOptions struct {
	MoveTo      string
	DeleteTools bool
}

func (a *App) GroupContentCounts(name string) (tools, dots int, err error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, 0, fmt.Errorf("group name cannot be empty")
	}
	cfg, err := a.loadConfig()
	if err != nil {
		return 0, 0, err
	}
	group := findGroupInConfig(cfg, name)
	if group == nil {
		return 0, 0, fmt.Errorf("group %q not found", name)
	}
	return len(group.Tools), len(group.Dots), nil
}

func (a *App) DeleteGroup(ctx context.Context, name string, opts DeleteGroupOptions) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("group name cannot be empty")
	}
	if opts.MoveTo != "" && opts.DeleteTools {
		return fmt.Errorf("choose either MoveTo or DeleteTools, not both")
	}
	if opts.MoveTo == name {
		return fmt.Errorf("move target must differ from deleted group %q", name)
	}

	var deletedLogicalTools []string
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		var deletedTools []config.ToolEntry
		var deletedDots []config.DotEntry
		found := false
		newGroups := make([]*config.GroupConfig, 0, len(cfg.Groups))
		for _, g := range cfg.Groups {
			if g.BaseName() != name {
				newGroups = append(newGroups, g)
				continue
			}
			if g.IsHost() {
				return fmt.Errorf("host group %q cannot be deleted", name)
			}
			found = true
			deletedTools = append(deletedTools, g.Tools...)
			deletedDots = append(deletedDots, g.Dots...)
		}
		if !found {
			return errSkipSave
		}
		if len(deletedTools) > 0 && opts.MoveTo == "" && !opts.DeleteTools {
			return fmt.Errorf("group %q contains tools; provide MoveTo or DeleteTools", name)
		}
		if len(deletedDots) > 0 && opts.MoveTo == "" {
			return fmt.Errorf("group %q contains dots; provide MoveTo", name)
		}
		if opts.DeleteTools {
			for _, tool := range deletedTools {
				if err := a.rejectProviderToolDelete(tool.Name); err != nil {
					return err
				}
			}
		}

		cfg.Groups = newGroups
		if opts.DeleteTools {
			for _, tool := range deletedTools {
				delete(cfg.Tools, tool.Name)
				deletedLogicalTools = append(deletedLogicalTools, tool.Name)
				removeGlobalToolIgnore(cfg, tool.Name)
			}
		} else if opts.MoveTo != "" {
			dst := ensureGroupInConfig(cfg, opts.MoveTo)
			if machineGroupName(opts.MoveTo) == currentMachineGroupName() {
				var err error
				dst, err = ensureHostGroupInConfig(cfg, opts.MoveTo)
				if err != nil {
					return err
				}
			}
			for _, tool := range deletedTools {
				if !containsToolMembership(dst.Tools, tool.Name) {
					dst.Tools = append(dst.Tools, tool)
				}
			}
			for _, dot := range deletedDots {
				if !containsDotMembership(dst.Dots, dot.Name) {
					dst.Dots = append(dst.Dots, dot)
				}
			}
		}

		for host, groups := range cfg.Hosts {
			filtered := groups[:0]
			for _, gname := range groups {
				if gname != name {
					filtered = append(filtered, gname)
				}
			}
			cfg.Hosts[host] = filtered
		}
		return nil
	}); err != nil {
		return err
	}
	return a.deleteCachedLogicalTools(ctx, deletedLogicalTools)
}

// ClaimFromMachineGroup assigns groupName to the current host and removes from
// the host group any tools that are now covered by that reusable group.
func (a *App) ClaimFromMachineGroup(groupName string) error {
	hostname := currentMachineGroupName()
	return a.withConfig(func(cfg *config.RootConfig) error {
		if cfg.Hosts == nil {
			cfg.Hosts = make(map[string][]string)
		}
		if _, err := ensureHostGroupInConfig(cfg, hostname); err != nil {
			return err
		}
		if !slices.Contains(cfg.Hosts[hostname], groupName) {
			cfg.Hosts[hostname] = append(cfg.Hosts[hostname], groupName)
		}

		claimedGroup := findGroupInConfig(cfg, groupName)
		if claimedGroup == nil || len(claimedGroup.Tools) == 0 {
			return nil
		}
		covered := make(map[string]struct{}, len(claimedGroup.Tools))
		for _, t := range claimedGroup.Tools {
			covered[t.Name] = struct{}{}
		}
		hg := findGroupInConfig(cfg, hostname)
		if hg == nil {
			return nil
		}
		remaining := hg.Tools[:0]
		for _, t := range hg.Tools {
			if _, ok := covered[t.Name]; !ok {
				remaining = append(remaining, t)
			}
		}
		hg.Tools = remaining
		return nil
	})
}

func (a *App) SetGlobalToolIgnore(name string, ignored bool) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("tool name is required")
	}
	return a.withConfig(func(cfg *config.RootConfig) error {
		if ignored {
			if _, ok := cfg.Tools[name]; !ok {
				return fmt.Errorf("logical tool %q not found", name)
			}
			if slices.Contains(cfg.Ignore.Tools, name) {
				return errSkipSave
			}
			cfg.Ignore.Tools = append(cfg.Ignore.Tools, name)
			return nil
		}
		if !removeGlobalToolIgnore(cfg, name) {
			return errSkipSave
		}
		return nil
	})
}

func removeGlobalToolIgnore(cfg *config.RootConfig, name string) bool {
	filtered := cfg.Ignore.Tools[:0]
	changed := false
	for _, ignored := range cfg.Ignore.Tools {
		if ignored == name {
			changed = true
			continue
		}
		filtered = append(filtered, ignored)
	}
	cfg.Ignore.Tools = filtered
	return changed
}

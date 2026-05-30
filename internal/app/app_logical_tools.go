package app

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

// ClearInstallOverrideResult describes the provider override removed from a
// logical tool.
type ClearInstallOverrideResult struct {
	Name        string
	Provider    string
	Package     string
	InstallWith string
	Scope       string
}

// NormalizedInstallOverride describes a no-op provider override removed from a
// logical tool config.
type NormalizedInstallOverride struct {
	Name        string
	Provider    string
	InstallWith string
	Scope       string
	Host        string
}

// NormalizeInstallOverridesOptions controls which no-op install_with values are
// removed from logical tool config.
type NormalizeInstallOverridesOptions struct {
	IncludeDefaults    bool
	IncludeCurrentHost bool
	DryRun             bool
}

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

func (a *App) SetToolQuarantine(name, quarantine string) error {
	if name == "" {
		return fmt.Errorf("tool name is required")
	}
	if quarantine != "" && !strings.EqualFold(quarantine, toolQuarantineExempt) {
		if _, err := parseQuarantineDuration(quarantine); err != nil {
			return err
		}
	}
	return a.withConfig(func(cfg *config.RootConfig) error {
		if cfg.Tools == nil {
			return fmt.Errorf("logical tool %q not found", name)
		}
		spec, ok := cfg.Tools[name]
		if !ok {
			return fmt.Errorf("logical tool %q not found", name)
		}
		spec.Quarantine = quarantine
		cfg.Tools[name] = spec
		return nil
	})
}

// RemoveLogicalTool deletes a logical tool spec and all group memberships for it.
func (a *App) RemoveLogicalTool(ctx context.Context, name string) error {
	if name == "" {
		return fmt.Errorf("tool name is required")
	}
	if err := a.rejectProviderToolDelete(name); err != nil {
		return err
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
		}
		if removeGlobalToolIgnore(cfg, name) {
			changed = true
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

// MoveToolToGroup makes groupName the logical tool's owning group. The logical
// tool must already exist in RootConfig.Tools.
func (a *App) MoveToolToGroup(name, groupName string) error {
	if name == "" {
		return fmt.Errorf("tool name is required")
	}
	groupName = compatibilityGroupName(groupName)

	return a.withConfig(func(cfg *config.RootConfig) error {
		return moveToolToGroupInConfig(cfg, name, groupName)
	})
}

// RemoveToolFromGroup removes a logical tool membership from a group. The tool
// spec is left intact; use RemoveLogicalTool to delete the spec itself.
func (a *App) RemoveToolFromGroup(name, groupName string) error {
	if name == "" {
		return fmt.Errorf("tool name is required")
	}
	groupName = compatibilityGroupName(groupName)

	return a.withConfig(func(cfg *config.RootConfig) error {
		return removeToolFromGroupInConfig(cfg, name, groupName)
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

type ToolIgnoreScopeKind string

const (
	ToolIgnoreScopeTool  ToolIgnoreScopeKind = "tool"
	ToolIgnoreScopeGroup ToolIgnoreScopeKind = "group"
	ToolIgnoreScopeHost  ToolIgnoreScopeKind = "host"
)

type ToolIgnoreScopeChange struct {
	Kind    ToolIgnoreScopeKind
	Group   string
	Ignored bool
}

type ToolIgnoreScopesChange struct {
	Ignored          bool
	HostScopeChanged bool
	Tools            []*database.ToolCache
	ScopeDisplay     *ToolScopeDisplayState
}

func (a *App) SetToolIgnoreScopesWithState(ctx context.Context, name string, changes []ToolIgnoreScopeChange) (*ToolIgnoreScopesChange, error) {
	hostChanged := false
	for _, change := range changes {
		var err error
		switch change.Kind {
		case ToolIgnoreScopeTool:
			err = a.SetToolIgnore(name, change.Ignored)
		case ToolIgnoreScopeGroup:
			err = a.SetGroupIgnore(change.Group, name, change.Ignored)
		case ToolIgnoreScopeHost:
			hostChanged = true
			err = a.SetGlobalToolIgnore(name, change.Ignored)
		default:
			err = fmt.Errorf("unknown ignore scope %q", change.Kind)
		}
		if err != nil {
			return nil, err
		}
	}

	tools, err := a.ListTools(ctx, "")
	if err != nil {
		return nil, err
	}
	var hostIgnore []string
	info, err := a.HostStatus()
	if err != nil {
		return nil, err
	}
	if info != nil && info.Active != "" {
		if host, ok := info.Hosts[info.Active]; ok {
			hostIgnore = host.Ignore
		}
	}
	scopeDisplay, err := a.ToolScopeDisplayStateWithFallback(ctx, hostIgnore)
	if err != nil {
		return nil, err
	}
	return &ToolIgnoreScopesChange{
		Ignored:          scopeDisplay.IgnoreLabels[name] != "",
		HostScopeChanged: hostChanged,
		Tools:            tools,
		ScopeDisplay:     scopeDisplay,
	}, nil
}

func (a *App) SetGroupIgnore(groupName, name string, ignored bool) error {
	groupName = compatibilityGroupName(groupName)
	if groupName != "" {
		cfg, err := a.loadConfig()
		if err != nil {
			return err
		}
		if findGroupInConfig(cfg, groupName) == nil {
			return fmt.Errorf("group %q not found", groupName)
		}
	}
	return a.SetGlobalToolIgnore(name, ignored)
}

func (a *App) SetToolHostInstallSpec(name, providerName, packageName, installWith string) error {
	return a.setToolInstallSpec(name, shortHostname(currentHostname()), providerName, packageName, installWith)
}

func (a *App) SetToolDefaultInstallSpec(name, providerName, packageName, installWith string) error {
	return a.setToolInstallSpec(name, "", providerName, packageName, installWith)
}

type ToolProviderScopeKind string

const (
	ToolProviderScopeHost      ToolProviderScopeKind = "provider-host"
	ToolProviderScopeTool      ToolProviderScopeKind = "provider-tool"
	ToolProviderScopeEcosystem ToolProviderScopeKind = "provider-ecosystem"
)

type ToolProviderScopeOptions struct {
	Kind         ToolProviderScopeKind
	ProviderName string
	Package      string
	InstallWith  string
}

type ToolProviderScopeChoice struct {
	Kind   ToolProviderScopeKind
	Label  string
	Detail string
}

type ToolProviderScopeChange struct {
	Tools        []*database.ToolCache
	ScopeDisplay *ToolScopeDisplayState
}

func DefaultToolProviderScopeChoices(t *database.ToolCache) []ToolProviderScopeChoice {
	return toolProviderScopeChoices(t, provider.BuiltinEcosystemFor, provider.BuiltinIsEcosystem)
}

func (a *App) ToolProviderScopeChoices(t *database.ToolCache) []ToolProviderScopeChoice {
	return toolProviderScopeChoices(t, a.providerEcosystem, a.knownEcosystemProvider)
}

func toolProviderScopeChoices(
	t *database.ToolCache,
	ecosystemFor func(string) (string, bool),
	isEcosystem func(string) bool,
) []ToolProviderScopeChoice {
	if t == nil || t.InstalledWith == "" {
		return []ToolProviderScopeChoice{{Kind: ToolProviderScopeHost, Label: "installed provider unknown", Detail: "refresh first"}}
	}
	choices := []ToolProviderScopeChoice{
		{Kind: ToolProviderScopeHost, Label: "this tool on this host", Detail: t.InstalledWith},
		{Kind: ToolProviderScopeTool, Label: "this tool everywhere", Detail: t.InstalledWith},
	}
	if ecosystem, ok := ecosystemFor(t.Provider); ok && isEcosystem(ecosystem) {
		choices = append(choices, ToolProviderScopeChoice{
			Kind:   ToolProviderScopeEcosystem,
			Label:  ecosystem + " manager on this host",
			Detail: t.InstalledWith,
		})
	}
	return choices
}

func (a *App) SetToolProviderScopeWithState(ctx context.Context, name string, opts ToolProviderScopeOptions) (*ToolProviderScopeChange, error) {
	if opts.InstallWith == "" {
		return nil, fmt.Errorf("installed provider is unknown")
	}
	var err error
	switch opts.Kind {
	case ToolProviderScopeHost:
		err = a.SetToolHostInstallSpec(name, opts.ProviderName, opts.Package, opts.InstallWith)
	case ToolProviderScopeTool:
		err = a.SetToolDefaultInstallSpec(name, opts.ProviderName, opts.Package, opts.InstallWith)
	case ToolProviderScopeEcosystem:
		err = a.PinEcosystemForHost(ctx, opts.ProviderName, opts.InstallWith)
	default:
		err = fmt.Errorf("unknown provider scope %q", opts.Kind)
	}
	if err != nil {
		return nil, err
	}
	tools, err := a.ListTools(ctx, "")
	if err != nil {
		return nil, err
	}
	scopeDisplay, err := a.ToolScopeDisplayState(ctx)
	if err != nil {
		return nil, err
	}
	if scopeDisplay.ToolProviderPins == nil {
		scopeDisplay.ToolProviderPins = make(map[string]string)
	}
	scopeDisplay.ToolProviderPins[name] = opts.InstallWith
	return &ToolProviderScopeChange{Tools: tools, ScopeDisplay: scopeDisplay}, nil
}

// ClearToolInstallOverride removes the effective install_with override for a
// logical tool. Host-specific overrides take precedence over the default tool
// spec, matching resolveInstallSpec.
func (a *App) ClearToolInstallOverride(ctx context.Context, name, providerName string) (ClearInstallOverrideResult, error) {
	if name == "" {
		return ClearInstallOverrideResult{}, fmt.Errorf("tool name is required")
	}
	var result ClearInstallOverrideResult
	err := a.withConfig(func(cfg *config.RootConfig) error {
		spec, ok := cfg.Tools[name]
		if !ok {
			return fmt.Errorf("logical tool %q not found", name)
		}
		install := a.resolveInstallSpec(ctx, name, spec)
		if providerName != "" && !installSpecMatchesProvider(install, providerName) {
			return fmt.Errorf("tool %q with provider %q not found in config", name, providerName)
		}

		hostnames := currentHostnames()
		var clearedHost string
		for _, host := range hostnames {
			hostSpec, ok := spec.Hosts[host]
			if !ok {
				continue
			}
			if hostSpec.InstallWith == "" {
				break
			}
			result = ClearInstallOverrideResult{
				Name:        name,
				Provider:    hostSpec.Provider,
				Package:     hostSpec.EffectivePackage(name),
				InstallWith: hostSpec.InstallWith,
				Scope:       "host",
			}
			hostSpec.InstallWith = ""
			spec.Hosts[host] = hostSpec
			clearedHost = host
			break
		}

		if spec.InstallWith != "" {
			if result.Name == "" {
				result = ClearInstallOverrideResult{
					Name:        name,
					Provider:    spec.Provider,
					Package:     spec.DefaultInstallSpec().EffectivePackage(name),
					InstallWith: spec.InstallWith,
					Scope:       "tool",
				}
			}
			spec.InstallWith = ""
		}
		if result.Name == "" {
			return fmt.Errorf("provider override for %q not found", name)
		}
		if clearedHost != "" {
			hostSpec := spec.Hosts[clearedHost]
			if sameToolInstallSpec(hostSpec, spec.DefaultInstallSpec()) {
				delete(spec.Hosts, clearedHost)
			}
			if len(spec.Hosts) == 0 {
				spec.Hosts = nil
			}
		}
		cfg.Tools[name] = spec
		return nil
	})
	if err != nil {
		return ClearInstallOverrideResult{}, err
	}
	return result, nil
}

// NormalizeHostDefaultInstallOverrides removes current-host install_with values
// that only restate the currently resolved ecosystem default. Global defaults
// are left intact because they may be intentional for other hosts.
func (a *App) NormalizeHostDefaultInstallOverrides(ctx context.Context) ([]NormalizedInstallOverride, error) {
	return a.NormalizeDefaultInstallOverrides(ctx, NormalizeInstallOverridesOptions{IncludeCurrentHost: true})
}

// NormalizeDefaultInstallOverrides removes install_with values that only
// restate the currently resolved ecosystem default.
func (a *App) NormalizeDefaultInstallOverrides(ctx context.Context, opts NormalizeInstallOverridesOptions) ([]NormalizedInstallOverride, error) {
	if !opts.IncludeDefaults && !opts.IncludeCurrentHost {
		return nil, nil
	}
	hosts := []string(nil)
	if opts.IncludeCurrentHost {
		hosts = currentHostnames()
	}
	resolvedEcosystems := a.ResolvedEcosystemProviders(ctx)
	var normalized []NormalizedInstallOverride

	if opts.DryRun {
		cfg, err := a.loadConfig()
		if err != nil {
			return nil, err
		}
		normalized = normalizeDefaultInstallOverridesInConfig(cfg, opts, hosts, resolvedEcosystems)
		sortNormalizedInstallOverrides(normalized)
		return normalized, nil
	}

	err := a.withConfig(func(cfg *config.RootConfig) error {
		normalized = normalizeDefaultInstallOverridesInConfig(cfg, opts, hosts, resolvedEcosystems)
		if len(normalized) == 0 {
			return errSkipSave
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortNormalizedInstallOverrides(normalized)
	return normalized, nil
}

func normalizeDefaultInstallOverridesInConfig(cfg *config.RootConfig, opts NormalizeInstallOverridesOptions, hosts []string, resolvedEcosystems map[string]string) []NormalizedInstallOverride {
	if cfg == nil {
		return nil
	}
	var normalized []NormalizedInstallOverride
	for name, spec := range cfg.Tools {
		changed := false
		if opts.IncludeDefaults && spec.InstallWith != "" &&
			configInstallWithForConcreteProvider(spec.Provider, spec.InstallWith, resolvedEcosystems) == "" {
			normalized = append(normalized, NormalizedInstallOverride{
				Name:        name,
				Provider:    spec.Provider,
				InstallWith: spec.InstallWith,
				Scope:       "tool",
			})
			if !opts.DryRun {
				spec.InstallWith = ""
				changed = true
			}
		}

		if opts.IncludeCurrentHost && len(hosts) > 0 && len(spec.Hosts) > 0 {
			for _, host := range hosts {
				hostSpec, ok := spec.Hosts[host]
				if !ok || hostSpec.InstallWith == "" {
					continue
				}
				if configInstallWithForConcreteProvider(hostSpec.Provider, hostSpec.InstallWith, resolvedEcosystems) != "" {
					continue
				}
				normalized = append(normalized, NormalizedInstallOverride{
					Name:        name,
					Provider:    hostSpec.Provider,
					InstallWith: hostSpec.InstallWith,
					Scope:       "host",
					Host:        host,
				})
				if opts.DryRun {
					continue
				}
				hostSpec.InstallWith = ""
				if sameToolInstallSpec(hostSpec, spec.DefaultInstallSpec()) {
					delete(spec.Hosts, host)
				} else {
					spec.Hosts[host] = hostSpec
				}
				changed = true
			}
		}

		if opts.DryRun || !changed {
			continue
		}
		if len(spec.Hosts) == 0 {
			spec.Hosts = nil
		}
		cfg.Tools[name] = spec
	}
	return normalized
}

func sortNormalizedInstallOverrides(overrides []NormalizedInstallOverride) {
	sort.Slice(overrides, func(i, j int) bool {
		left, right := overrides[i], overrides[j]
		if left.Name != right.Name {
			return left.Name < right.Name
		}
		if left.Scope != right.Scope {
			return left.Scope < right.Scope
		}
		if left.Host != right.Host {
			return left.Host < right.Host
		}
		if left.Provider != right.Provider {
			return left.Provider < right.Provider
		}
		return left.InstallWith < right.InstallWith
	})
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

func currentHostnames() []string {
	host := currentHostname()
	short := shortHostname(host)
	if short != "" && short != host {
		return []string{host, short}
	}
	if host == "" {
		return nil
	}
	return []string{host}
}

func sameToolInstallSpec(a, b config.ToolInstallSpec) bool {
	return a.Provider == b.Provider &&
		a.Package == b.Package &&
		a.InstallWith == b.InstallWith &&
		maps.Equal(a.Options, b.Options)
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

func containsDotMembership(dots []config.DotEntry, name string) bool {
	for _, dot := range dots {
		if dot.Name == name {
			return true
		}
	}
	return false
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

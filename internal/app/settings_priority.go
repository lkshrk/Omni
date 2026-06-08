package app

import (
	"context"
	"fmt"
	"slices"
	"sort"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
)

// ─── Provider priority & ecosystem membership ───────────────────────────────

type SettingsProviderSummary struct {
	Enabled int
	Total   int
}

// AvailableConcreteProviderSet reports which concrete providers are available on
// this host, for rendering the provider-priority editor (unavailable entries are
// shown greyed but remain orderable).
func (a *App) AvailableConcreteProviderSet(ctx context.Context) map[string]bool {
	out := make(map[string]bool)
	if a.registry == nil {
		return out
	}
	for _, p := range a.availableProviders(ctx) {
		out[p.Name()] = true
	}
	return out
}

func (a *App) setProviderEnabled(disabled []string, providerName string, enabled bool) ([]string, error) {
	if err := a.validateDisablableProvider(providerName); err != nil {
		return nil, err
	}
	if enabled {
		return removeString(disabled, providerName), nil
	}
	if slices.Contains(disabled, providerName) {
		return append([]string(nil), disabled...), nil
	}
	return append(append([]string(nil), disabled...), providerName), nil
}

func removeString(values []string, value string) []string {
	out := make([]string, 0, len(values))
	for _, item := range values {
		if item != value {
			out = append(out, item)
		}
	}
	return out
}

// DefaultConcreteProviderPriorityDraft returns the provider-priority editor draft
// using the builtin concrete provider set, for callers without a registry.
func DefaultConcreteProviderPriorityDraft(priority []string) []string {
	return systemProviderPriorityDraft(priority, provider.BuiltinConcreteProviderPriorityNames())
}

func systemProviderPriorityDraft(priority, options []string) []string {
	if len(priority) == 0 {
		return append([]string(nil), options...)
	}
	draft := filterSystemProviderPriority(priority, options)
	if len(draft) == 0 {
		return append([]string(nil), options...)
	}
	for _, name := range options {
		if !slices.Contains(draft, name) {
			draft = append(draft, name)
		}
	}
	return draft
}

// ConcreteProviderPriorityOptions returns the concrete providers eligible for
// the host priority list, ordered by catalog DisplayOrder, plus any registered
// concretes not already covered.
func (a *App) ConcreteProviderPriorityOptions() []string {
	opts := provider.BuiltinConcreteProviderPriorityNames()
	for _, eco := range []string{provider.EcosystemSystem, provider.EcosystemNode, provider.EcosystemPython} {
		for _, name := range a.ConcreteProviderNamesForEcosystem(eco) {
			// Skip aliases (e.g. pip3 → pip): they would duplicate their canonical
			// provider, which is already in the builtin list.
			if config.NormalizeConcreteProvider(name) != name {
				continue
			}
			if !slices.Contains(opts, name) {
				opts = append(opts, name)
			}
		}
	}
	return opts
}

// ConcreteProviderPriorityDraft returns the editable draft: the saved priority
// order with any missing known concrete providers appended so all are
// reorderable in the UI.
func (a *App) ConcreteProviderPriorityDraft(priority []string) []string {
	return systemProviderPriorityDraft(priority, a.ConcreteProviderPriorityOptions())
}

func (a *App) filterConcreteProviderPriority(priority []string) []string {
	return filterSystemProviderPriority(priority, a.ConcreteProviderPriorityOptions())
}

// EffectiveEcosystemManager returns the settings manager value (e.g. "bun",
// "pip3") for the top-ranked, non-disabled concrete provider of eco in the host
// provider-priority order, falling back to the first non-disabled builtin member.
// Returns "" when the ecosystem has no usable concrete. The manager is derived
// from provider_priority — the single source of truth — never stored.
func EffectiveEcosystemManager(settings config.Settings, eco string) string {
	concrete := chosenEcosystemConcrete(settings, eco)
	if concrete == "" {
		return ""
	}
	return managerSettingValue(eco, concrete)
}

func chosenEcosystemConcrete(settings config.Settings, eco string) string {
	disabled := make(map[string]struct{}, len(settings.DisabledProviders))
	for _, n := range settings.DisabledProviders {
		disabled[n] = struct{}{}
	}
	members := provider.BuiltinConcreteProvidersForEcosystem(eco)
	memberSet := make(map[string]struct{}, len(members))
	for _, m := range members {
		memberSet[m] = struct{}{}
	}
	for _, name := range settings.ProviderPriority {
		if _, ok := memberSet[name]; !ok {
			continue
		}
		if _, off := disabled[name]; off {
			continue
		}
		return name
	}
	// Fall back to the builtin default priority order (e.g. bun before npm, uv
	// before pip) rather than the alphabetical member list.
	for _, name := range provider.BuiltinConcreteProviderPriorityNames() {
		if _, ok := memberSet[name]; !ok {
			continue
		}
		if _, off := disabled[name]; off {
			continue
		}
		return name
	}
	return ""
}

// SystemInstallPriority returns the system providers from provider_priority in
// order, with any missing builtin system providers appended — the order used to
// resolve which system package manager installs an unscoped tool.
func SystemInstallPriority(settings config.Settings) []string {
	defaults := provider.BuiltinSystemProviderPriorityNames()
	out := filterSystemProviderPriority(settings.ProviderPriority, defaults)
	for _, name := range defaults {
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

// migrateLegacyEcosystemManager folds the legacy ecosystems block — per-ecosystem
// node/python managers and the system priority list — into provider_priority
// (the single source of truth), then drops the block. Keeps pre-existing configs
// working after the ecosystems settings were removed.
func migrateLegacyEcosystemManager(s *config.Settings) {
	if s == nil || len(s.Ecosystems) == 0 {
		return
	}
	promote := func(name string) {
		canonical := config.NormalizeConcreteProvider(name)
		if canonical == "" {
			canonical = name
		}
		s.ProviderPriority = promoteEcosystemConcrete(s.ProviderPriority, canonical)
	}
	for _, eco := range []string{provider.EcosystemPython, provider.EcosystemNode} {
		if m := s.Ecosystems[eco].Manager; m != "" {
			promote(m)
		}
	}
	// Promote in reverse so the first legacy system entry ends up first.
	sysPriority := s.Ecosystems[provider.EcosystemSystem].Priority
	for i := len(sysPriority) - 1; i >= 0; i-- {
		promote(sysPriority[i])
	}
	s.Ecosystems = nil
}

// promoteEcosystemConcrete returns provider_priority with concrete moved to the
// front so it becomes the effective manager for its ecosystem. concrete must be
// a canonical concrete provider name (config.NormalizeConcreteProvider). A blank
// concrete is a no-op.
func promoteEcosystemConcrete(priority []string, concrete string) []string {
	if concrete == "" {
		return priority
	}
	out := make([]string, 0, len(priority)+1)
	out = append(out, concrete)
	for _, p := range priority {
		if p != concrete {
			out = append(out, p)
		}
	}
	return out
}

func managerSettingValue(ecosystem, concrete string) string {
	if opt, ok := provider.BuiltinManagerOption(ecosystem, concrete); ok && opt.SettingsValue != "" {
		return opt.SettingsValue
	}
	return concrete
}

func filterSystemProviderPriority(priority, options []string) []string {
	out := make([]string, 0, len(priority))
	seen := make(map[string]struct{}, len(priority))
	valid := make(map[string]struct{}, len(options))
	for _, name := range options {
		valid[name] = struct{}{}
	}
	for _, name := range priority {
		if _, ok := seen[name]; ok {
			continue
		}
		if _, ok := valid[name]; !ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

// filterDisablableProviders keeps only valid (concrete or family) provider
// names, de-duplicated, preserving order.
func (a *App) filterDisablableProviders(names []string) []string {
	out := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if _, ok := seen[name]; ok {
			continue
		}
		if a.validateDisablableProvider(name) != nil {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

// validateDisablableProvider accepts the names that may appear in
// disabled_providers: any concrete provider (the priority-model toggle target)
// or, for backward compatibility, an ecosystem family name.
func (a *App) validateDisablableProvider(name string) error {
	if a.IsEcosystemProvider(name) {
		return nil
	}
	if a.registry != nil {
		if _, ok := a.registry.Get(name); ok {
			return nil
		}
	}
	if slices.Contains(provider.BuiltinConcreteProviderPriorityNames(), name) {
		return nil
	}
	return fmt.Errorf("%q is not a known provider", name)
}

// expandToConcreteProviders converts any ecosystem family name to its concrete
// members, passing concrete names through unchanged (de-duplicated, order
// preserved).
func expandToConcreteProviders(names []string) []string {
	out := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	add := func(n string) {
		if n == "" {
			return
		}
		if _, ok := seen[n]; ok {
			return
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	for _, name := range names {
		if provider.BuiltinIsEcosystem(name) {
			for _, c := range provider.BuiltinConcreteProvidersForEcosystem(name) {
				add(c)
			}
			continue
		}
		add(name)
	}
	return out
}

func (a *App) EcosystemProviderNames() []string {
	if a.registry != nil {
		if names := a.registry.EcosystemNames(); len(names) > 0 {
			return names
		}
	}
	return DefaultEcosystemProviderNames()
}

func DefaultEcosystemProviderNames() []string {
	return provider.BuiltinEcosystemNames()
}

func (a *App) IsEcosystemProvider(name string) bool {
	return a.knownEcosystemProvider(name)
}

func (a *App) ConcreteProviderNamesForEcosystem(ecosystem string) []string {
	names := a.providersForEcosystem(ecosystem)
	delete(names, ecosystem)
	out := make([]string, 0, len(names))
	for name := range names {
		if a.IsEcosystemProvider(name) {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func DefaultSettingsProviderSummary(settings config.Settings) SettingsProviderSummary {
	return settingsProviderSummary(settings, DefaultEcosystemProviderNames())
}

func (a *App) SettingsProviderSummary(settings config.Settings) SettingsProviderSummary {
	return settingsProviderSummary(settings, a.EcosystemProviderNames())
}

func settingsProviderSummary(settings config.Settings, providers []string) SettingsProviderSummary {
	providerSet := make(map[string]struct{}, len(providers))
	for _, name := range providers {
		if name == "" {
			continue
		}
		providerSet[name] = struct{}{}
	}
	disabled := make(map[string]struct{}, len(settings.DisabledProviders))
	for _, name := range settings.DisabledProviders {
		if _, ok := providerSet[name]; ok {
			disabled[name] = struct{}{}
		}
	}
	return SettingsProviderSummary{
		Enabled: len(providerSet) - len(disabled),
		Total:   len(providerSet),
	}
}

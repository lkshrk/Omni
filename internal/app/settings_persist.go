package app

import (
	"context"
	"fmt"
	"maps"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
)

// ─── Settings persistence ───────────────────────────────────────────────────

func cloneEcosystemSettings(in map[string]config.EcosystemSettings) map[string]config.EcosystemSettings {
	if in == nil {
		return nil
	}
	out := make(map[string]config.EcosystemSettings, len(in))
	for name, eco := range in {
		eco.Priority = append([]string(nil), eco.Priority...)
		out[name] = eco
	}
	return out
}

func cloneSettingsStringSlice(in []string) []string {
	if in == nil {
		return nil
	}
	return append([]string{}, in...)
}

func cloneSettingsStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}

type hostSettingsPatch struct {
	Ecosystems        map[string]config.EcosystemSettings `json:"ecosystems,omitempty"`
	DotsRepo          string                              `json:"dots_repo,omitempty"`
	DotsDisabled      *bool                               `json:"dots_disabled,omitempty"`
	DisabledProviders *[]string                           `json:"disabled_providers,omitempty"`
	ProviderPriority  []string                            `json:"provider_priority,omitempty"`
	AgentsDisabled    *bool                               `json:"agents_disabled,omitempty"`
	SkillsDisabled    *bool                               `json:"skills_disabled,omitempty"`
	McpDisabled       *bool                               `json:"mcp_disabled,omitempty"`
	PluginsDisabled   *bool                               `json:"plugins_disabled,omitempty"`
	AgentsUse         *[]string                           `json:"agents_use,omitempty"`
	Providers         *[]config.ProviderEntry             `json:"providers,omitempty"`
}

func cloneBoolPtr(v *bool) *bool {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

func hostSettingsPatchDoc(in map[string]config.Settings) map[string]hostSettingsPatch {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]hostSettingsPatch, len(in))
	for host, settings := range in {
		patch := hostSettingsPatch{
			Ecosystems:       cloneEcosystemSettings(settings.Ecosystems),
			DotsRepo:         settings.DotsRepo,
			ProviderPriority: append([]string(nil), settings.ProviderPriority...),
			DotsDisabled:     cloneBoolPtr(settings.DotsDisabled),
			AgentsDisabled:   cloneBoolPtr(settings.AgentsDisabled),
			SkillsDisabled:   cloneBoolPtr(settings.SkillsDisabled),
			McpDisabled:      cloneBoolPtr(settings.McpDisabled),
			PluginsDisabled:  cloneBoolPtr(settings.PluginsDisabled),
		}
		if settings.DisabledProviders != nil {
			disabledProviders := append([]string{}, settings.DisabledProviders...)
			patch.DisabledProviders = &disabledProviders
		}
		if settings.AgentsUse != nil {
			agentsUse := append([]string{}, settings.AgentsUse...)
			patch.AgentsUse = &agentsUse
		}
		if settings.Providers != nil {
			providers := append([]config.ProviderEntry{}, settings.Providers...)
			patch.Providers = &providers
		}
		out[host] = patch
	}
	return out
}

func (a *App) patchConfigLocked(patch func(*config.RootConfig) (any, error)) error {
	a.configMu.Lock()
	defer a.configMu.Unlock()

	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	doc, err := patch(cfg)
	if err != nil {
		return err
	}
	return config.Patch(a.ConfigPath, doc)
}

// SaveSettings writes settings to settings.json, splitting global and host-specific fields.
// Global fields (AutoImport, DotsGit) are written to the top-level "settings" block.
// Host-specific fields (Ecosystems, DotsRepo, DisabledProviders, ProviderPriority)
// are written to host_settings[shortHostname].
//
// Uses config.Patch for both keys so any unknown top-level keys in settings.json
// (e.g. user-added custom keys) are preserved exactly as-is.
func (a *App) SaveSettings(_ context.Context, s config.Settings) error {
	hostname := shortHostname(currentHostname())

	// Patch both top-level keys in one atomic write.
	// Only AutoImport and DotsGit are global; host_settings uses a narrower
	// patch type so global fields cannot leak into per-machine entries.
	type patchDoc struct {
		Settings     config.Settings              `json:"settings"`
		HostSettings map[string]hostSettingsPatch `json:"host_settings,omitempty"`
	}
	return a.patchConfigLocked(func(cfg *config.RootConfig) (any, error) {
		// Read existing host_settings so other machines' entries are not lost
		// when we write back the full host_settings map.
		if cfg.HostSettings == nil {
			cfg.HostSettings = make(map[string]config.Settings)
		}
		hs := cfg.HostSettings[hostname]
		hs.Ecosystems = cloneEcosystemSettings(s.Ecosystems)
		hs.DotsRepo = normalisePath(s.DotsRepo)
		hs.DotsDisabled = s.DotsDisabled // *bool: nil means "not configured"
		hs.DisabledProviders = cloneSettingsStringSlice(s.DisabledProviders)
		hs.ProviderPriority = cloneSettingsStringSlice(s.ProviderPriority)
		cfg.HostSettings[hostname] = hs

		return patchDoc{
			Settings: config.Settings{
				AutoImport:               s.AutoImport,
				UpdateQuarantine:         s.UpdateQuarantine,
				ProviderUpdateQuarantine: cloneSettingsStringMap(s.ProviderUpdateQuarantine),
				DotsGit:                  s.DotsGit,
			},
			HostSettings: hostSettingsPatchDoc(cfg.HostSettings),
		}, nil
	})
}

// SaveDisabledProviders sets which providers are disabled on this machine,
// persisted to host_settings[shortHostname].disabled_providers as CONCRETE
// provider names. Family names (system/node/python) passed by legacy callers
// (e.g. the bootstrap provider step) are expanded to their concrete members so
// the stored list always matches the concrete provider-priority model.
func (a *App) SaveDisabledProviders(_ context.Context, disabled []string) error {
	concrete := expandToConcreteProviders(disabled)
	for _, name := range concrete {
		if err := a.validateDisablableProvider(name); err != nil {
			return err
		}
	}
	return a.patchCurrentHostSettings(func(hs *config.Settings) error {
		hs.DisabledProviders = concrete
		return nil
	})
}

// SaveDotsDisabled sets the per-machine dots_disabled flag in host_settings.
// All other settings are preserved.
func (a *App) SaveDotsDisabled(_ context.Context, disabled bool) error {
	return a.patchCurrentHostSettings(func(hs *config.Settings) error {
		hs.DotsDisabled = config.BoolPtr(disabled)
		return nil
	})
}

func (a *App) PinEcosystemForHost(_ context.Context, ecosystem, concrete string) error {
	return a.patchCurrentHostSettings(func(hs *config.Settings) error {
		switch ecosystem {
		case provider.EcosystemNode, provider.EcosystemPython:
			if _, ok := provider.BuiltinManagerOption(ecosystem, concrete); !ok {
				return fmt.Errorf("%q is not a manager for ecosystem %q", concrete, ecosystem)
			}
			canonical := config.NormalizeConcreteProvider(concrete)
			if canonical == "" {
				canonical = concrete
			}
			hs.ProviderPriority = promoteEcosystemConcrete(hs.ProviderPriority, canonical)
		case provider.EcosystemSystem:
			if eco, ok := provider.BuiltinEcosystemFor(concrete); !ok || eco != provider.EcosystemSystem || provider.BuiltinIsEcosystem(concrete) {
				return fmt.Errorf("%q is not a system provider", concrete)
			}
			canonical := config.NormalizeConcreteProvider(concrete)
			if canonical == "" {
				canonical = concrete
			}
			hs.ProviderPriority = promoteEcosystemConcrete(hs.ProviderPriority, canonical)
		default:
			return fmt.Errorf("unknown provider family %q", ecosystem)
		}
		return nil
	})
}

func (a *App) patchCurrentHostSettings(mutator func(*config.Settings) error) error {
	hostname := shortHostname(currentHostname())
	type patchDoc struct {
		HostSettings map[string]hostSettingsPatch `json:"host_settings,omitempty"`
	}
	return a.patchConfigLocked(func(cfg *config.RootConfig) (any, error) {
		if cfg.HostSettings == nil {
			cfg.HostSettings = make(map[string]config.Settings)
		}
		hs := cfg.HostSettings[hostname]
		if err := mutator(&hs); err != nil {
			return nil, err
		}
		cfg.HostSettings[hostname] = hs
		return patchDoc{HostSettings: hostSettingsPatchDoc(cfg.HostSettings)}, nil
	})
}

// ResetSettings replaces the settings block with zero-value defaults,
// preserving all other data (groups, hosts, etc.).
func (a *App) ResetSettings(ctx context.Context) error {
	return a.SaveSettings(ctx, config.Settings{})
}

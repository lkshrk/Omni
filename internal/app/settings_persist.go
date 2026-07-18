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

// patchConfigLocked mutates the config under the config lock and persists it
// through the include-safe write seam so settings and host_settings land in the
// fragment that owns them instead of being force-written into the main file.
// It deliberately skips validation (providers=nil): a settings write must not
// fail on an unrelated invalid tool elsewhere in the config.
func (a *App) patchConfigLocked(mutate func(*config.RootConfig) error) error {
	a.configMu.Lock()
	defer a.configMu.Unlock()
	return config.WriteConfig(a.ConfigPath, a.loadConfig, nil, mutate)
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

	// Only AutoImport and DotsGit are global; the host-specific fields go to
	// host_settings[hostname] so global fields cannot leak into per-machine
	// entries. The seam diffs and routes each changed key to its owner.
	return a.patchConfigLocked(func(cfg *config.RootConfig) error {
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

		// Replace the global settings block with just the global fields, matching
		// the historical whole-key write (other global fields reset to zero).
		cfg.Settings = config.Settings{
			AutoImport:               s.AutoImport,
			UpdateQuarantine:         s.UpdateQuarantine,
			ProviderUpdateQuarantine: cloneSettingsStringMap(s.ProviderUpdateQuarantine),
			DotsGit:                  s.DotsGit,
		}
		return nil
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
	return a.patchConfigLocked(func(cfg *config.RootConfig) error {
		if cfg.HostSettings == nil {
			cfg.HostSettings = make(map[string]config.Settings)
		}
		hs := cfg.HostSettings[hostname]
		if err := mutator(&hs); err != nil {
			return err
		}
		cfg.HostSettings[hostname] = hs
		return nil
	})
}

// ResetSettings replaces the settings block with zero-value defaults,
// preserving all other data (groups, hosts, etc.).
func (a *App) ResetSettings(ctx context.Context) error {
	return a.SaveSettings(ctx, config.Settings{})
}

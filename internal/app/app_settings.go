package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	osExec "os/exec"
	"sort"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

// ─── Settings ─────────────────────────────────────────────────────────────────

// LoadSettings returns the effective settings for this machine.
// Global settings are merged with host-specific overrides from host_settings[shortHostname].
// Returns zero-value Settings when the file does not exist.
func (a *App) LoadSettings() (config.Settings, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return config.Settings{}, err
	}
	return a.effectiveSettings(cfg), nil
}

func (a *App) QuerySettings(key string) (map[string]any, error) {
	settings, err := a.LoadSettings()
	if err != nil {
		return nil, err
	}
	values := map[string]any{
		"auto_import":          settings.AutoImport,
		"node.manager":         settings.EcosystemManager(provider.EcosystemNode),
		"python.manager":       settings.EcosystemManager(provider.EcosystemPython),
		"system.priority":      settings.EcosystemPriority(provider.EcosystemSystem),
		"dots_repo":            settings.DotsRepo,
		"dots_disabled":        settings.DotsDisabled,
		"dots_git.auto_commit": settings.DotsGit.AutoCommit,
		"dots_git.auto_push":   settings.DotsGit.AutoPush,
		"disabled_providers":   settings.DisabledProviders,
	}
	if key == "" {
		return values, nil
	}
	canonical := CanonicalSettingKey(key)
	if _, ok := values[canonical]; !ok {
		return nil, fmt.Errorf("unknown setting %q", key)
	}
	return map[string]any{canonical: values[canonical]}, nil
}

func CanonicalSettingKey(key string) string {
	return strings.ReplaceAll(key, "-", "_")
}

func (a *App) EcosystemProviderNames() []string {
	if a.registry != nil {
		if names := a.registry.EcosystemNames(); len(names) > 0 {
			return names
		}
	}
	return provider.BuiltinEcosystemNames()
}

func (a *App) IsEcosystemProvider(name string) bool {
	return a.knownEcosystemProvider(name)
}

func (a *App) ManagerNames(ecosystem string) []string {
	return a.managerNames(ecosystem)
}

func (a *App) IsManagerForEcosystem(ecosystem, manager string) bool {
	_, ok := a.managerOption(ecosystem, manager)
	return ok
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

func (a *App) IsConcreteProviderForEcosystem(ecosystem, name string) bool {
	if a.IsEcosystemProvider(name) {
		return false
	}
	if a.registry != nil {
		if meta, ok := a.registry.Metadata(name); ok && meta.Kind == provider.ProviderKindConcrete && meta.Ecosystem == ecosystem {
			return true
		}
	}
	if eco, ok := provider.BuiltinEcosystemFor(name); ok && eco == ecosystem && !provider.BuiltinIsEcosystem(name) {
		return true
	}
	return false
}

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

type hostSettingsPatch struct {
	Ecosystems        map[string]config.EcosystemSettings `json:"ecosystems,omitempty"`
	DotsRepo          string                              `json:"dots_repo,omitempty"`
	DotsDisabled      *bool                               `json:"dots_disabled,omitempty"`
	DisabledProviders *[]string                           `json:"disabled_providers,omitempty"`
}

func hostSettingsPatchDoc(in map[string]config.Settings) map[string]hostSettingsPatch {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]hostSettingsPatch, len(in))
	for host, settings := range in {
		patch := hostSettingsPatch{
			Ecosystems: cloneEcosystemSettings(settings.Ecosystems),
			DotsRepo:   settings.DotsRepo,
		}
		if settings.DotsDisabled != nil {
			dotsDisabled := *settings.DotsDisabled
			patch.DotsDisabled = &dotsDisabled
		}
		if settings.DisabledProviders != nil {
			disabledProviders := append([]string{}, settings.DisabledProviders...)
			patch.DisabledProviders = &disabledProviders
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
// Host-specific fields (Ecosystems, DotsRepo, DisabledProviders) are written to
// host_settings[shortHostname].
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
		cfg.HostSettings[hostname] = hs

		return patchDoc{
			Settings: config.Settings{
				AutoImport: s.AutoImport,
				DotsGit:    s.DotsGit,
			},
			HostSettings: hostSettingsPatchDoc(cfg.HostSettings),
		}, nil
	})
}

// SaveDisabledProviders sets which ecosystem providers are disabled on this machine.
// Persisted to host_settings[shortHostname].disabled_providers.
// All other host settings and global settings are preserved.
func (a *App) SaveDisabledProviders(_ context.Context, disabled []string) error {
	hostname := shortHostname(currentHostname())

	type patchDoc struct {
		HostSettings map[string]hostSettingsPatch `json:"host_settings,omitempty"`
	}
	return a.patchConfigLocked(func(cfg *config.RootConfig) (any, error) {
		if cfg.HostSettings == nil {
			cfg.HostSettings = make(map[string]config.Settings)
		}
		hs := cfg.HostSettings[hostname]
		hs.DisabledProviders = append([]string{}, disabled...)
		cfg.HostSettings[hostname] = hs

		return patchDoc{HostSettings: hostSettingsPatchDoc(cfg.HostSettings)}, nil
	})
}

// SaveDotsDisabled sets the per-machine dots_disabled flag in host_settings.
// All other settings are preserved.
func (a *App) SaveDotsDisabled(_ context.Context, disabled bool) error {
	hostname := shortHostname(currentHostname())

	type patchDoc struct {
		HostSettings map[string]hostSettingsPatch `json:"host_settings,omitempty"`
	}
	return a.patchConfigLocked(func(cfg *config.RootConfig) (any, error) {
		if cfg.HostSettings == nil {
			cfg.HostSettings = make(map[string]config.Settings)
		}
		hs := cfg.HostSettings[hostname]
		hs.DotsDisabled = config.BoolPtr(disabled)
		cfg.HostSettings[hostname] = hs

		return patchDoc{HostSettings: hostSettingsPatchDoc(cfg.HostSettings)}, nil
	})
}

// SaveNodeManager sets the per-machine node ecosystem manager in host_settings.
// All other settings are preserved.
func (a *App) SaveNodeManager(_ context.Context, manager string) error {
	hostname := shortHostname(currentHostname())

	type patchDoc struct {
		HostSettings map[string]hostSettingsPatch `json:"host_settings,omitempty"`
	}
	return a.patchConfigLocked(func(cfg *config.RootConfig) (any, error) {
		if cfg.HostSettings == nil {
			cfg.HostSettings = make(map[string]config.Settings)
		}
		hs := cfg.HostSettings[hostname]
		hs.SetEcosystemManager(provider.EcosystemNode, manager)
		cfg.HostSettings[hostname] = hs

		return patchDoc{HostSettings: hostSettingsPatchDoc(cfg.HostSettings)}, nil
	})
}

func (a *App) PinEcosystemForHost(_ context.Context, ecosystem, concrete string) error {
	hostname := shortHostname(currentHostname())

	type patchDoc struct {
		HostSettings map[string]hostSettingsPatch `json:"host_settings,omitempty"`
	}
	return a.patchConfigLocked(func(cfg *config.RootConfig) (any, error) {
		if cfg.HostSettings == nil {
			cfg.HostSettings = make(map[string]config.Settings)
		}
		hs := cfg.HostSettings[hostname]
		switch ecosystem {
		case provider.EcosystemNode, provider.EcosystemPython:
			if _, ok := provider.BuiltinManagerOption(ecosystem, concrete); !ok {
				return nil, fmt.Errorf("%q is not a manager for ecosystem %q", concrete, ecosystem)
			}
			hs.SetEcosystemManager(ecosystem, concrete)
		case provider.EcosystemSystem:
			if eco, ok := provider.BuiltinEcosystemFor(concrete); !ok || eco != provider.EcosystemSystem || provider.BuiltinIsEcosystem(concrete) {
				return nil, fmt.Errorf("%q is not a system provider", concrete)
			}
			priority := hs.EcosystemPriority(provider.EcosystemSystem)
			if len(priority) == 0 {
				priority = provider.BuiltinSystemProviderPriorityNames()
			}
			next := []string{concrete}
			for _, name := range priority {
				if name != concrete {
					next = append(next, name)
				}
			}
			hs.SetEcosystemPriority(provider.EcosystemSystem, next)
		default:
			return nil, fmt.Errorf("unknown ecosystem provider %q", ecosystem)
		}
		cfg.HostSettings[hostname] = hs

		return patchDoc{HostSettings: hostSettingsPatchDoc(cfg.HostSettings)}, nil
	})
}

// ResetSettings replaces the settings block with zero-value defaults,
// preserving all other data (groups, profiles, etc.).
func (a *App) ResetSettings(ctx context.Context) error {
	return a.SaveSettings(ctx, config.Settings{})
}

// ResetCache closes the current DB connection, deletes the DB file, and
// reopens + re-migrates it so the user starts with a clean cache.
//
// The entire Close → nil → Open → assign cycle is performed under dbMu write
// lock so no concurrent goroutine can observe a nil a.db or operate on a
// partially-reset connection. Callers that read a.db should use a.readDB().
func (a *App) ResetCache(ctx context.Context) error {
	a.dbMu.Lock()
	defer a.dbMu.Unlock()

	if a.db != nil {
		if err := a.db.Close(); err != nil {
			return fmt.Errorf("reset cache: close db: %w", err)
		}
		a.db = nil
	}
	if err := os.Remove(a.DBPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reset cache: remove db: %w", err)
	}
	db, err := database.Open(a.DBPath)
	if err != nil {
		return fmt.Errorf("reset cache: open db: %w", err)
	}
	if err := db.Migrate(ctx); err != nil {
		return fmt.Errorf("reset cache: migrate db: %w", err)
	}
	a.db = db
	return nil
}

// EffectiveManagers returns the binary names that the python and node
// ecosystem providers would actually use right now, honouring settings hints and
// falling back to PATH probing in preference order.
//
// Returns "" for either ecosystem when no suitable binary is found on PATH.
func (a *App) EffectiveManagers() (pythonBin, nodeBin string) {
	s, _ := a.LoadSettings()

	pythonBin = probeFirst(s.EcosystemManager(provider.EcosystemPython), a.managerNames(provider.EcosystemPython))
	nodeBin = probeFirst(s.EcosystemManager(provider.EcosystemNode), a.managerNames(provider.EcosystemNode))
	return pythonBin, nodeBin
}

// ResolvedEcosystemProviders returns a map of ecosystem provider name → concrete
// provider name for every ecosystem provider in the registry that implements
// provider.ConcreteResolver and is currently available.
// Used by the TUI to render labels like "system(brew)".
func (a *App) ResolvedEcosystemProviders(ctx context.Context) map[string]string {
	ecos := a.registry.EcosystemProviders()
	type resolved struct {
		name     string
		concrete string
	}
	out := make([]resolved, len(ecos))
	g, gctx := errgroup.WithContext(ctx)
	for i, p := range ecos {
		i, p := i, p
		g.Go(func() error {
			cr, ok := p.(provider.ConcreteResolver)
			if !ok {
				return nil
			}
			concrete, err := cr.ResolvedName(gctx)
			if err == nil && concrete != "" {
				out[i] = resolved{name: p.Name(), concrete: concrete}
			}
			return nil
		})
	}
	_ = g.Wait()
	result := make(map[string]string, len(out))
	for _, r := range out {
		if r.name != "" {
			result[r.name] = r.concrete
		}
	}
	return result
}

// AllAvailableManagers returns ALL binary names found on PATH for each ecosystem,
// in priority order. Unlike EffectiveManagers (which returns the single preferred
// binary), this is used by the setup wizard to display every available manager.
func (a *App) AllAvailableManagers() (pythonBins, nodeBins []string) {
	s, _ := a.LoadSettings()
	pythonBins = probeAll(s.EcosystemManager(provider.EcosystemPython), a.managerNames(provider.EcosystemPython))
	nodeBins = probeAll(s.EcosystemManager(provider.EcosystemNode), a.managerNames(provider.EcosystemNode))
	return pythonBins, nodeBins
}

// probeFirst returns hint if it is a non-empty string and exists on PATH.
// Otherwise it returns the first candidate from the priority list that is
// found on PATH, or "" if none are available.
func probeFirst(hint string, priority []string) string {
	if hint != "" {
		if _, err := osExec.LookPath(hint); err == nil {
			return hint
		}
	}
	for _, bin := range priority {
		if _, err := osExec.LookPath(bin); err == nil {
			return bin
		}
	}
	return ""
}

// probeAll returns all candidate binaries from priority found on PATH, in order.
// If hint is non-empty and on PATH it is included first (deduplicated).
func probeAll(hint string, priority []string) []string {
	seen := make(map[string]bool)
	var found []string
	add := func(bin string) {
		if bin != "" && !seen[bin] {
			if _, err := osExec.LookPath(bin); err == nil {
				seen[bin] = true
				found = append(found, bin)
			}
		}
	}
	add(hint)
	for _, bin := range priority {
		add(bin)
	}
	return found
}

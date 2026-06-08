package config

import (
	"encoding/json"
	"fmt"

	"github.com/lkshrk/omni/internal/provider"
)

// expandFamilyDisabledProviders converts family-level disabled provider names
// ("system"/"node"/"python") into their concrete members (e.g. node → bun,
// pnpm, npm), leaving concrete names untouched. Order is preserved and
// duplicates removed. Idempotent: a list already containing only concrete names
// is returned unchanged (aside from de-duplication).
func expandFamilyDisabledProviders(names []string) []string {
	if len(names) == 0 {
		return names
	}
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
	for _, n := range names {
		if provider.BuiltinIsEcosystem(n) {
			for _, concrete := range provider.BuiltinConcreteProvidersForEcosystem(n) {
				add(concrete)
			}
			continue
		}
		add(n)
	}
	return out
}

// migrateConfigV8ToV9 expands family-level disabled_providers into concrete
// providers in both global and per-host settings, so disabling is expressed
// purely in concrete terms under the provider-priority model.
func migrateConfigV8ToV9(cfg *RootConfig) error {
	cfg.Settings.DisabledProviders = expandFamilyDisabledProviders(cfg.Settings.DisabledProviders)
	for host, s := range cfg.HostSettings {
		s.DisabledProviders = expandFamilyDisabledProviders(s.DisabledProviders)
		cfg.HostSettings[host] = s
	}
	cfg.Version = 9
	return nil
}

func migrateRawConfigV8ToV9(raw map[string]json.RawMessage) error {
	if err := transformRawDisabledProviders(raw, "settings"); err != nil {
		return err
	}
	if hsRaw, ok := raw["host_settings"]; ok && !isJSONNull(hsRaw) {
		var hosts map[string]json.RawMessage
		if err := json.Unmarshal(hsRaw, &hosts); err != nil {
			return fmt.Errorf("parsing host_settings: %w", err)
		}
		for host := range hosts {
			if err := transformRawDisabledProviders(hosts, host); err != nil {
				return err
			}
		}
		out, err := json.Marshal(hosts)
		if err != nil {
			return fmt.Errorf("encoding host_settings: %w", err)
		}
		raw["host_settings"] = out
	}
	raw["version"] = json.RawMessage(`9`)
	return nil
}

// transformRawDisabledProviders rewrites the disabled_providers array inside the
// settings object stored at key in container, preserving every other key.
func transformRawDisabledProviders(container map[string]json.RawMessage, key string) error {
	objRaw, ok := container[key]
	if !ok || isJSONNull(objRaw) {
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(objRaw, &obj); err != nil {
		return fmt.Errorf("parsing %s: %w", key, err)
	}
	dpRaw, ok := obj["disabled_providers"]
	if !ok || isJSONNull(dpRaw) {
		return nil
	}
	var names []string
	if err := json.Unmarshal(dpRaw, &names); err != nil {
		return fmt.Errorf("parsing %s.disabled_providers: %w", key, err)
	}
	expanded, err := json.Marshal(expandFamilyDisabledProviders(names))
	if err != nil {
		return fmt.Errorf("encoding %s.disabled_providers: %w", key, err)
	}
	obj["disabled_providers"] = expanded
	out, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("encoding %s: %w", key, err)
	}
	container[key] = out
	return nil
}

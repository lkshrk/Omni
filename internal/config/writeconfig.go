package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrSkipSave aborts a WriteConfig mutation cleanly when nothing needs to change.
var ErrSkipSave = errors.New("config: skip save")

// WriteConfig — The single safe seam for editing settings in place: only changed top-level keys are written, each routed to its owning fragment with stale copies nulled so removed entries cannot resurrect on the next load.
func WriteConfig(path string, load func() (*RootConfig, error), providers *ProviderValidation, mutate func(*RootConfig) error) error {
	cfg, err := load()
	if err != nil {
		return err
	}
	before, err := snapshotTopLevel(cfg)
	if err != nil {
		return err
	}
	if err := mutate(cfg); err != nil {
		if errors.Is(err, ErrSkipSave) {
			return nil
		}
		return err
	}
	if providers != nil {
		if errs := fatalValidationErrors(ValidateRoot(cfg, *providers)); len(errs) > 0 {
			return ValidationErrors(errs)
		}
	}
	after, err := snapshotTopLevel(cfg)
	if err != nil {
		return err
	}
	diff := make(map[string]json.RawMessage)
	for k, v := range after {
		if k == "$schema" {
			continue
		}
		if !bytes.Equal(before[k], v) {
			diff[k] = v
		}
	}
	for k := range before {
		if k == "$schema" {
			continue
		}
		if _, ok := after[k]; !ok {
			diff[k] = json.RawMessage(`null`)
		}
	}
	if len(diff) == 0 {
		return nil
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating config directory: %w", err)
		}
	}
	return PatchRawRouted(path, diff)
}

// Fallback-pathed and warn-level errors are advisory and must not block a write; `omni doctor` reports the full set.
func fatalValidationErrors(errs []ValidationError) []ValidationError {
	fatal := make([]ValidationError, 0, len(errs))
	for _, e := range errs {
		if strings.Contains(e.Path, ".fallback") || e.Warn {
			continue
		}
		fatal = append(fatal, e)
	}
	return fatal
}

// Pointer fields keep an explicitly-empty list distinguishable from an absent one, and bar non-overridable Settings fields from a host_settings write.
type hostSettingsProjection struct {
	Ecosystems        map[string]EcosystemSettings `json:"ecosystems,omitempty"`
	DotsRepo          string                       `json:"dots_repo,omitempty"`
	DotsDisabled      *bool                        `json:"dots_disabled,omitempty"`
	DisabledProviders *[]string                    `json:"disabled_providers,omitempty"`
	ProviderPriority  []string                     `json:"provider_priority,omitempty"`
	AgentsDisabled    *bool                        `json:"agents_disabled,omitempty"`
	SkillsDisabled    *bool                        `json:"skills_disabled,omitempty"`
	McpDisabled       *bool                        `json:"mcp_disabled,omitempty"`
	PluginsDisabled   *bool                        `json:"plugins_disabled,omitempty"`
	AgentsUse         *[]string                    `json:"agents_use,omitempty"`
	Providers         *[]ProviderEntry             `json:"providers,omitempty"`
}

// Reads values only, so the projection needs no deep copy.
func projectHostSettings(in map[string]Settings) map[string]hostSettingsProjection {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]hostSettingsProjection, len(in))
	for host, s := range in {
		p := hostSettingsProjection{
			Ecosystems:       s.Ecosystems,
			DotsRepo:         s.DotsRepo,
			ProviderPriority: s.ProviderPriority,
			DotsDisabled:     s.DotsDisabled,
			AgentsDisabled:   s.AgentsDisabled,
			SkillsDisabled:   s.SkillsDisabled,
			McpDisabled:      s.McpDisabled,
			PluginsDisabled:  s.PluginsDisabled,
		}
		if s.DisabledProviders != nil {
			p.DisabledProviders = &s.DisabledProviders
		}
		if s.AgentsUse != nil {
			p.AgentsUse = &s.AgentsUse
		}
		if s.Providers != nil {
			p.Providers = &s.Providers
		}
		out[host] = p
	}
	return out
}

// Marshals the whole *RootConfig rather than a hand-mirrored struct so a newly added top-level field can never be silently dropped on save.
func snapshotTopLevel(cfg *RootConfig) (map[string]json.RawMessage, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	out := make(map[string]json.RawMessage)
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	delete(out, "$include")
	if len(cfg.HostSettings) > 0 {
		hs, err := json.Marshal(projectHostSettings(cfg.HostSettings))
		if err != nil {
			return nil, err
		}
		out["host_settings"] = hs
	} else {
		delete(out, "host_settings")
	}
	return out, nil
}

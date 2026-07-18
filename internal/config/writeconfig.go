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

// ErrSkipSave is returned by a WriteConfig mutation to abort the write with no
// error: the mutation inspected the config and decided nothing needs to change.
var ErrSkipSave = errors.New("config: skip save")

// WriteConfig is the single safe seam for editing settings in place. Callers
// describe *what* to change via mutate; WriteConfig owns *how* to persist it
// safely:
//
//	load → snapshot top-level keys → mutate → validate → diff → route → write
//
// load is supplied by the caller so app-layer load migrations run before the
// snapshot; passing config.Load directly is the include-only default. providers
// enables semantic validation of the mutated config — pass nil to skip it (the
// caller has no provider registry yet). Only the top-level keys that actually
// changed are written, each routed to the fragment that owns it, with stale
// copies in non-owner files nulled so removed entries cannot resurrect on the
// next load. That include invariant lives here, never in the caller.
//
// A mutate that returns ErrSkipSave aborts the write cleanly; any other error
// propagates unchanged.
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

// fatalValidationErrors drops fallback-pathed and warn-level validation errors:
// a fallback only matters when actually used, and warn-level errors are
// advisory. Neither should block a write. `omni doctor` reports the full set.
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

// hostSettingsProjection is the host-overridable subset of Settings, serialized
// with pointer fields so an explicitly-empty list is distinguishable from an
// absent one. Snapshotting through this projection keeps host_settings diffs
// byte-stable and prevents non-overridable Settings fields from leaking into a
// host_settings write.
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

// projectHostSettings maps each host's Settings to its overridable projection.
// It reads values only (snapshotting never mutates), so no deep copy is needed.
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

// snapshotTopLevel marshals the config to a per-top-level-key raw map so two
// snapshots can be diffed key-by-key. It marshals the whole *RootConfig rather
// than a hand-mirrored struct, so a newly added top-level field participates in
// the diff automatically and can never be silently dropped on save. Two keys
// need special handling:
//   - $include is a load-time merge directive, stripped before save.
//   - host_settings goes through the host-overridable projection so
//     non-overridable Settings fields never leak into a host_settings write and
//     the diff stays byte-stable.
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

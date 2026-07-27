package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
)

// LoadSettings — Global settings merged with host_settings[shortHostname]; zero value when the file does not exist.
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
		"auto_import":                settings.AutoImport,
		"update_quarantine":          settings.UpdateQuarantine,
		"provider_update_quarantine": settings.ProviderUpdateQuarantine,
		"dots_repo":                  settings.DotsRepo,
		"dots_disabled":              settings.DotsDisabled,
		"agents_disabled":            settings.AgentsDisabled,
		"dots_git.auto_commit":       settings.DotsGit.AutoCommit,
		"dots_git.auto_push":         settings.DotsGit.AutoPush,
		"disabled_providers":         settings.DisabledProviders,
		"provider_priority":          settings.ProviderPriority,
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

type SettingsChangeKind string

const (
	SettingsChangeSetValue            SettingsChangeKind = "set_value"
	SettingsChangeToggleBool          SettingsChangeKind = "toggle_bool"
	SettingsChangeSetProviderPriority SettingsChangeKind = "set_provider_priority"
	SettingsChangeSetProvider         SettingsChangeKind = "set_provider"
)

type SettingsChange struct {
	Kind      SettingsChangeKind
	Key       string
	Value     string
	Provider  string
	Ecosystem string
	Priority  []string
	Disabled  []string
	Enabled   bool
}

type SettingsActionID string

const (
	SettingsActionToggleAutoImport SettingsActionID = "toggle-auto-import"
	SettingsActionToggleDotsCommit SettingsActionID = "toggle-dots-commit"
	SettingsActionToggleDotsPush   SettingsActionID = "toggle-dots-push"
)

type SettingsChangeResult struct {
	Key               string
	DisabledProviders []string
}

func SetSettingValue(key, value string) SettingsChange {
	return SettingsChange{Kind: SettingsChangeSetValue, Key: CanonicalSettingKey(key), Value: value}
}

func ToggleSettingBool(key string) SettingsChange {
	return SettingsChange{Kind: SettingsChangeToggleBool, Key: CanonicalSettingKey(key)}
}

// SetProviderLayout — The TUI priority editor commits reorder and enable/disable together.
func SetProviderLayout(priority, disabled []string) SettingsChange {
	return SettingsChange{
		Kind:     SettingsChangeSetProviderPriority,
		Priority: append([]string(nil), priority...),
		Disabled: append([]string(nil), disabled...),
	}
}

func SetSettingsProviderEnabled(providerName string, enabled bool) SettingsChange {
	return SettingsChange{Kind: SettingsChangeSetProvider, Provider: providerName, Enabled: enabled}
}

func SettingsChangeForAction(action SettingsActionID) (SettingsChange, error) {
	switch action {
	case SettingsActionToggleAutoImport:
		return ToggleSettingBool("auto_import"), nil
	case SettingsActionToggleDotsCommit:
		return ToggleSettingBool("dots_git.auto_commit"), nil
	case SettingsActionToggleDotsPush:
		return ToggleSettingBool("dots_git.auto_push"), nil
	default:
		return SettingsChange{}, fmt.Errorf("unknown settings action %q", action)
	}
}

func SettingsActionAvailable(settings config.Settings, action SettingsActionID) bool {
	switch action {
	case SettingsActionToggleDotsCommit:
		return !settings.DotsGit.AutoPush
	default:
		return true
	}
}

func (a *App) SetSetting(ctx context.Context, key, value string) (string, error) {
	result, err := a.SaveSettingsChange(ctx, SetSettingValue(key, value))
	if err != nil {
		return "", err
	}
	return result.Key, nil
}

func (a *App) DisableProvider(ctx context.Context, providerName string) ([]string, error) {
	result, err := a.SaveSettingsChange(ctx, SetSettingsProviderEnabled(providerName, false))
	if err != nil {
		return nil, err
	}
	return result.DisabledProviders, nil
}

func (a *App) EnableProvider(ctx context.Context, providerName string) ([]string, error) {
	result, err := a.SaveSettingsChange(ctx, SetSettingsProviderEnabled(providerName, true))
	if err != nil {
		return nil, err
	}
	return result.DisabledProviders, nil
}

func (a *App) SaveSettingsChange(ctx context.Context, change SettingsChange) (SettingsChangeResult, error) {
	_, result, err := a.SaveSettingsChanges(ctx, change)
	return result, err
}

func (a *App) SaveSettingsChanges(ctx context.Context, changes ...SettingsChange) (config.Settings, SettingsChangeResult, error) {
	settings, err := a.LoadSettings()
	if err != nil {
		return config.Settings{}, SettingsChangeResult{}, err
	}
	var result SettingsChangeResult
	for _, change := range changes {
		settings, result, err = a.ApplySettingsChange(ctx, settings, change)
		if err != nil {
			return config.Settings{}, SettingsChangeResult{}, err
		}
	}
	if len(changes) == 0 {
		return settings, result, nil
	}
	if err := a.SaveSettings(ctx, settings); err != nil {
		return config.Settings{}, SettingsChangeResult{}, err
	}
	return settings, result, nil
}

func (a *App) ApplySettingsChange(_ context.Context, settings config.Settings, change SettingsChange) (config.Settings, SettingsChangeResult, error) {
	result := SettingsChangeResult{}
	switch change.Kind {
	case SettingsChangeSetValue:
		key, err := a.applySettingValue(&settings, change.Key, change.Value)
		if err != nil {
			return config.Settings{}, SettingsChangeResult{}, err
		}
		result.Key = key
	case SettingsChangeToggleBool:
		key, err := applyToggleSettingBool(&settings, change.Key)
		if err != nil {
			return config.Settings{}, SettingsChangeResult{}, err
		}
		result.Key = key
	case SettingsChangeSetProviderPriority:
		settings.ProviderPriority = a.filterConcreteProviderPriority(change.Priority)
		if change.Disabled != nil {
			settings.DisabledProviders = a.filterDisablableProviders(change.Disabled)
			result.DisabledProviders = append([]string(nil), settings.DisabledProviders...)
		}
		result.Key = "provider_priority"
	case SettingsChangeSetProvider:
		disabled, err := a.setProviderEnabled(settings.DisabledProviders, change.Provider, change.Enabled)
		if err != nil {
			return config.Settings{}, SettingsChangeResult{}, err
		}
		settings.DisabledProviders = disabled
		result.DisabledProviders = append([]string(nil), disabled...)
	default:
		return config.Settings{}, SettingsChangeResult{}, fmt.Errorf("unknown settings change %q", change.Kind)
	}
	return settings, result, nil
}

func (a *App) applySettingValue(settings *config.Settings, key, value string) (string, error) {
	canonical := CanonicalSettingKey(key)
	if providerName, ok := strings.CutPrefix(canonical, "provider_update_quarantine."); ok {
		if providerName == "" {
			return "", fmt.Errorf("missing provider for %q", key)
		}
		if _, err := parseQuarantineDuration(value); err != nil {
			return "", err
		}
		if settings.ProviderUpdateQuarantine == nil {
			settings.ProviderUpdateQuarantine = make(map[string]string)
		}
		settings.ProviderUpdateQuarantine[providerName] = value
		return canonical, nil
	}
	switch canonical {
	case "auto_import":
		parsed, err := parseSettingBool(canonical, value)
		if err != nil {
			return "", err
		}
		settings.AutoImport = parsed
	case "update_quarantine":
		if _, err := parseQuarantineDuration(value); err != nil {
			return "", err
		}
		settings.UpdateQuarantine = value
	case "provider_priority":
		settings.ProviderPriority = a.filterConcreteProviderPriority(splitCommaList(value))
	case provider.EcosystemNode + ".manager", provider.EcosystemPython + ".manager", provider.EcosystemSystem + ".priority":
		return "", fmt.Errorf("%q is derived from provider_priority and is no longer settable; set provider_priority instead (e.g. omni settings set provider_priority brew,bun,uv)", canonical)
	case "dots_repo":
		settings.DotsRepo = value
	case "dots_git.auto_commit":
		parsed, err := parseSettingBool(canonical, value)
		if err != nil {
			return "", err
		}
		settings.DotsGit.AutoCommit = parsed
	case "dots_git.auto_push":
		parsed, err := parseSettingBool(canonical, value)
		if err != nil {
			return "", err
		}
		settings.DotsGit.AutoPush = parsed
	default:
		return "", fmt.Errorf("unknown setting %q", key)
	}
	return canonical, nil
}

func applyToggleSettingBool(settings *config.Settings, key string) (string, error) {
	canonical := CanonicalSettingKey(key)
	switch canonical {
	case "auto_import":
		settings.AutoImport = !settings.AutoImport
	case "dots_git.auto_commit":
		settings.DotsGit.AutoCommit = !settings.DotsGit.AutoCommit
	case "dots_git.auto_push":
		settings.DotsGit.AutoPush = !settings.DotsGit.AutoPush
	default:
		return "", fmt.Errorf("unknown boolean setting %q", key)
	}
	return canonical, nil
}

func parseSettingBool(key, value string) (bool, error) {
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", key)
	}
	return parsed, nil
}

func splitCommaList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, t)
		}
	}
	return out
}

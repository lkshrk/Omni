package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/executor"
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
		"auto_import":                settings.AutoImport,
		"update_quarantine":          settings.UpdateQuarantine,
		"provider_update_quarantine": settings.ProviderUpdateQuarantine,
		"node.manager":               settings.EcosystemManager(provider.EcosystemNode),
		"python.manager":             settings.EcosystemManager(provider.EcosystemPython),
		"system.priority":            settings.EcosystemPriority(provider.EcosystemSystem),
		"dots_repo":                  settings.DotsRepo,
		"dots_disabled":              settings.DotsDisabled,
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
	SettingsChangeSetValue          SettingsChangeKind = "set_value"
	SettingsChangeToggleBool        SettingsChangeKind = "toggle_bool"
	SettingsChangeSetSystemPriority SettingsChangeKind = "set_system_priority"
	SettingsChangeToggleProvider    SettingsChangeKind = "toggle_provider"
	SettingsChangeSetProvider       SettingsChangeKind = "set_provider"
	SettingsChangeCycleManager      SettingsChangeKind = "cycle_manager"
)

type SettingsChange struct {
	Kind      SettingsChangeKind
	Key       string
	Value     string
	Provider  string
	Ecosystem string
	Priority  []string
	Enabled   bool
}

type SettingsActionID string

const (
	SettingsActionToggleAutoImport     SettingsActionID = "toggle-auto-import"
	SettingsActionToggleSystemProvider SettingsActionID = "toggle-system-provider"
	SettingsActionToggleNodeProvider   SettingsActionID = "toggle-node-provider"
	SettingsActionTogglePythonProvider SettingsActionID = "toggle-python-provider"
	SettingsActionCycleNodeManager     SettingsActionID = "cycle-node-manager"
	SettingsActionCyclePythonManager   SettingsActionID = "cycle-python-manager"
	SettingsActionToggleDotsCommit     SettingsActionID = "toggle-dots-commit"
	SettingsActionToggleDotsPush       SettingsActionID = "toggle-dots-push"
)

type SettingsManagerOption struct {
	Value       string
	Label       string
	Description string
}

type SettingsProviderSummary struct {
	Enabled int
	Total   int
}

type SettingsProviderState struct {
	SystemPriority []string
	SystemEnabled  bool
	NodeEnabled    bool
	PythonEnabled  bool
	NodeManager    string
	PythonManager  string
}

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

func SetSystemPriority(priority []string) SettingsChange {
	return SettingsChange{Kind: SettingsChangeSetSystemPriority, Priority: append([]string(nil), priority...)}
}

func ToggleSettingsProvider(providerName string) SettingsChange {
	return SettingsChange{Kind: SettingsChangeToggleProvider, Provider: providerName}
}

func SetSettingsProviderEnabled(providerName string, enabled bool) SettingsChange {
	return SettingsChange{Kind: SettingsChangeSetProvider, Provider: providerName, Enabled: enabled}
}

func CycleSettingsManager(ecosystem string) SettingsChange {
	return SettingsChange{Kind: SettingsChangeCycleManager, Ecosystem: ecosystem}
}

func SettingsChangeForAction(action SettingsActionID) (SettingsChange, error) {
	switch action {
	case SettingsActionToggleAutoImport:
		return ToggleSettingBool("auto_import"), nil
	case SettingsActionToggleSystemProvider:
		return ToggleSettingsProvider(provider.EcosystemSystem), nil
	case SettingsActionToggleNodeProvider:
		return ToggleSettingsProvider(provider.EcosystemNode), nil
	case SettingsActionTogglePythonProvider:
		return ToggleSettingsProvider(provider.EcosystemPython), nil
	case SettingsActionCycleNodeManager:
		return CycleSettingsManager(provider.EcosystemNode), nil
	case SettingsActionCyclePythonManager:
		return CycleSettingsManager(provider.EcosystemPython), nil
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
	case SettingsChangeSetSystemPriority:
		settings.SetEcosystemPriority(provider.EcosystemSystem, a.filterSystemPriority(change.Priority))
		result.Key = provider.EcosystemSystem + ".priority"
	case SettingsChangeToggleProvider:
		disabled, err := a.toggleDisabledProvider(settings.DisabledProviders, change.Provider)
		if err != nil {
			return config.Settings{}, SettingsChangeResult{}, err
		}
		settings.DisabledProviders = disabled
		result.DisabledProviders = append([]string(nil), disabled...)
	case SettingsChangeSetProvider:
		disabled, err := a.setProviderEnabled(settings.DisabledProviders, change.Provider, change.Enabled)
		if err != nil {
			return config.Settings{}, SettingsChangeResult{}, err
		}
		settings.DisabledProviders = disabled
		result.DisabledProviders = append([]string(nil), disabled...)
	case SettingsChangeCycleManager:
		manager, err := a.nextSettingManager(change.Ecosystem, settings.EcosystemManager(change.Ecosystem))
		if err != nil {
			return config.Settings{}, SettingsChangeResult{}, err
		}
		settings.SetEcosystemManager(change.Ecosystem, manager)
		result.Key = change.Ecosystem + ".manager"
	default:
		return config.Settings{}, SettingsChangeResult{}, fmt.Errorf("unknown settings change %q", change.Kind)
	}
	return settings, result, nil
}

func (a *App) applySettingValue(settings *config.Settings, key, value string) (string, error) {
	canonical := CanonicalSettingKey(key)
	if strings.HasPrefix(canonical, "provider_update_quarantine.") {
		providerName := strings.TrimPrefix(canonical, "provider_update_quarantine.")
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
	case provider.EcosystemNode + ".manager":
		manager, err := a.parseSettingManager(provider.EcosystemNode, value)
		if err != nil {
			return "", err
		}
		settings.SetEcosystemManager(provider.EcosystemNode, manager)
	case provider.EcosystemPython + ".manager":
		manager, err := a.parseSettingManager(provider.EcosystemPython, value)
		if err != nil {
			return "", err
		}
		settings.SetEcosystemManager(provider.EcosystemPython, manager)
	case provider.EcosystemSystem + ".priority":
		priority, err := a.parseSystemPriority(value)
		if err != nil {
			return "", err
		}
		settings.SetEcosystemPriority(provider.EcosystemSystem, priority)
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

func (a *App) toggleDisabledProvider(disabled []string, providerName string) ([]string, error) {
	if err := a.validateEcosystemProvider(providerName); err != nil {
		return nil, err
	}
	if slices.Contains(disabled, providerName) {
		return removeString(disabled, providerName), nil
	}
	return append(append([]string(nil), disabled...), providerName), nil
}

func (a *App) setProviderEnabled(disabled []string, providerName string, enabled bool) ([]string, error) {
	if err := a.validateEcosystemProvider(providerName); err != nil {
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

func (a *App) nextSettingManager(ecosystem, current string) (string, error) {
	options := settingsManagerValues(a.managerOptions(ecosystem))
	if len(options) == 0 {
		return "", fmt.Errorf("unknown ecosystem %q", ecosystem)
	}
	if current == "" {
		return options[0], nil
	}
	if opt, ok := a.managerOption(ecosystem, current); ok {
		current = settingManagerValue(opt)
	}
	for i, option := range options {
		if current == option {
			if i+1 < len(options) {
				return options[i+1], nil
			}
			return "", nil
		}
	}
	return "", nil
}

func DefaultSystemProviderPriorityOptions() []string {
	return provider.BuiltinSystemProviderPriorityNames()
}

func DefaultSystemProviderPriorityDraft(priority []string) []string {
	return systemProviderPriorityDraft(priority, DefaultSystemProviderPriorityOptions())
}

func DefaultSystemProviderPriorityDisplay(priority []string) []string {
	return filterSystemProviderPriority(priority, DefaultSystemProviderPriorityOptions())
}

func (a *App) SystemProviderPriorityOptions() []string {
	defaults := DefaultSystemProviderPriorityOptions()
	for _, name := range a.ConcreteProviderNamesForEcosystem(provider.EcosystemSystem) {
		if !slices.Contains(defaults, name) {
			defaults = append(defaults, name)
		}
	}
	return defaults
}

func (a *App) SystemProviderPriorityDraft(priority []string) []string {
	return systemProviderPriorityDraft(priority, a.SystemProviderPriorityOptions())
}

func (a *App) SystemProviderPriorityDisplay(priority []string) []string {
	return filterSystemProviderPriority(priority, a.SystemProviderPriorityOptions())
}

func (a *App) filterSystemPriority(priority []string) []string {
	return a.SystemProviderPriorityDisplay(priority)
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

func parseSettingBool(key, value string) (bool, error) {
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", key)
	}
	return parsed, nil
}

func (a *App) parseSettingManager(ecosystem, value string) (string, error) {
	if value == "auto" || value == "default" {
		return "", nil
	}
	if opt, ok := a.managerOption(ecosystem, value); ok {
		return settingManagerValue(opt), nil
	}
	return "", fmt.Errorf("%q is not a manager for ecosystem %q (supported: %s)", value, ecosystem, strings.Join(a.ManagerNames(ecosystem), ", "))
}

func (a *App) parseSystemPriority(value string) ([]string, error) {
	raw := strings.Split(value, ",")
	priority := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, part := range raw {
		name := strings.TrimSpace(part)
		if name == "" {
			return nil, fmt.Errorf("system.priority contains an empty provider")
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("system.priority contains duplicate provider %q", name)
		}
		if !a.IsConcreteProviderForEcosystem(provider.EcosystemSystem, name) {
			return nil, fmt.Errorf("%q is not a concrete provider for ecosystem %q (supported: %s)", name, provider.EcosystemSystem, strings.Join(a.ConcreteProviderNamesForEcosystem(provider.EcosystemSystem), ", "))
		}
		seen[name] = struct{}{}
		priority = append(priority, name)
	}
	return priority, nil
}

func (a *App) validateEcosystemProvider(name string) error {
	if a.IsEcosystemProvider(name) {
		return nil
	}
	return fmt.Errorf("%q is not a provider family (supported: %s)", name, strings.Join(a.EcosystemProviderNames(), ", "))
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

func DefaultSettingsProviderSummary(settings config.Settings) SettingsProviderSummary {
	return settingsProviderSummary(settings, DefaultEcosystemProviderNames())
}

func (a *App) SettingsProviderSummary(settings config.Settings) SettingsProviderSummary {
	return settingsProviderSummary(settings, a.EcosystemProviderNames())
}

func DefaultSettingsProviderHelp(ecosystem string) string {
	return settingsProviderHelp(ecosystem, defaultSettingsProviderHelpLabels(ecosystem))
}

func (a *App) SettingsProviderHelp(ecosystem string) string {
	if a == nil {
		return DefaultSettingsProviderHelp(ecosystem)
	}
	switch ecosystem {
	case provider.EcosystemSystem:
		return settingsProviderHelp(ecosystem, a.SystemProviderPriorityOptions())
	case provider.EcosystemNode, provider.EcosystemPython:
		return settingsProviderHelp(ecosystem, settingsManagerOptionLabels(a.SettingsManagerOptions(ecosystem)))
	default:
		return ""
	}
}

func SettingsProviderStateFrom(settings config.Settings) SettingsProviderState {
	return SettingsProviderState{
		SystemPriority: append([]string(nil), settings.EcosystemPriority(provider.EcosystemSystem)...),
		SystemEnabled:  !slices.Contains(settings.DisabledProviders, provider.EcosystemSystem),
		NodeEnabled:    !slices.Contains(settings.DisabledProviders, provider.EcosystemNode),
		PythonEnabled:  !slices.Contains(settings.DisabledProviders, provider.EcosystemPython),
		NodeManager:    settings.EcosystemManager(provider.EcosystemNode),
		PythonManager:  settings.EcosystemManager(provider.EcosystemPython),
	}
}

func (a *App) ManagerNames(ecosystem string) []string {
	return a.managerNames(ecosystem)
}

func DefaultSettingsManagerOptions(ecosystem string) []SettingsManagerOption {
	return settingsManagerOptions(provider.BuiltinManagerOptions(ecosystem))
}

func (a *App) SettingsManagerOptions(ecosystem string) []SettingsManagerOption {
	return settingsManagerOptions(a.managerOptions(ecosystem))
}

func DefaultSettingsManagerHelp(ecosystem string) string {
	return settingsManagerHelp(ecosystem, DefaultSettingsManagerOptions(ecosystem))
}

func (a *App) SettingsManagerHelp(ecosystem string) string {
	if a == nil {
		return DefaultSettingsManagerHelp(ecosystem)
	}
	return settingsManagerHelp(ecosystem, a.SettingsManagerOptions(ecosystem))
}

func DefaultSetupNodeManagerOptions() []SettingsManagerOption {
	return DefaultSettingsManagerOptions(provider.EcosystemNode)
}

func (a *App) SetupNodeManagerOptions() []SettingsManagerOption {
	return a.SettingsManagerOptions(provider.EcosystemNode)
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

func cloneSettingsStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
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

func defaultSettingsProviderHelpLabels(ecosystem string) []string {
	switch ecosystem {
	case provider.EcosystemSystem:
		return DefaultSystemProviderPriorityOptions()
	case provider.EcosystemNode, provider.EcosystemPython:
		return settingsManagerOptionLabels(DefaultSettingsManagerOptions(ecosystem))
	default:
		return nil
	}
}

func settingsProviderHelp(ecosystem string, labels []string) string {
	if ecosystem == "" || len(labels) == 0 {
		return ""
	}
	return fmt.Sprintf("Track %s tools on this machine (%s).", ecosystem, strings.Join(labels, "/"))
}

func settingsManagerOptions(in []provider.ManagerOption) []SettingsManagerOption {
	values := settingsManagerValues(in)
	if len(values) == 0 {
		return nil
	}
	out := []SettingsManagerOption{{Value: "", Label: "auto", Description: settingsManagerDescription("auto")}}
	labelsByValue := make(map[string]string, len(in))
	for _, opt := range in {
		if opt.Alias {
			continue
		}
		labelsByValue[settingManagerValue(opt)] = opt.Name
	}
	for _, value := range values {
		label := labelsByValue[value]
		out = append(out, SettingsManagerOption{Value: value, Label: label, Description: settingsManagerDescription(label)})
	}
	return out
}

func settingsManagerDescription(name string) string {
	switch name {
	case "auto":
		return "use the provider's preferred available manager"
	case "bun":
		return "fast runtime + package manager"
	case "pnpm":
		return "disk-efficient, workspace-native"
	case "npm":
		return "bundled with every Node.js install"
	case "uv":
		return "fast Python tool manager"
	case "pip3", "pip":
		return "Python package installer"
	default:
		return "available manager"
	}
}

func settingsManagerHelp(ecosystem string, options []SettingsManagerOption) string {
	labels := settingsManagerOptionLabels(options)
	if len(labels) == 0 {
		return ""
	}
	preference := labels[0] + " preferred"
	for _, label := range labels[1:] {
		preference += ", then " + label
	}
	return fmt.Sprintf("%s (auto = %s).", settingsManagerHelpSubject(ecosystem), preference)
}

func settingsManagerHelpSubject(ecosystem string) string {
	switch ecosystem {
	case provider.EcosystemNode:
		return "JS package manager"
	case provider.EcosystemPython:
		return "Python tool manager"
	default:
		if ecosystem == "" {
			return "Manager"
		}
		return strings.ToUpper(ecosystem[:1]) + ecosystem[1:] + " manager"
	}
}

func settingsManagerOptionLabels(options []SettingsManagerOption) []string {
	labels := make([]string, 0, len(options))
	for _, opt := range options {
		if opt.Value == "" || opt.Label == "auto" || opt.Label == "" {
			continue
		}
		labels = append(labels, opt.Label)
	}
	return labels
}

func settingsManagerValues(in []provider.ManagerOption) []string {
	values := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, opt := range in {
		if opt.Alias {
			continue
		}
		value := settingManagerValue(opt)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values
}

func settingManagerValue(opt provider.ManagerOption) string {
	if opt.SettingsValue != "" {
		return opt.SettingsValue
	}
	return opt.Name
}

type hostSettingsPatch struct {
	Ecosystems        map[string]config.EcosystemSettings `json:"ecosystems,omitempty"`
	DotsRepo          string                              `json:"dots_repo,omitempty"`
	DotsDisabled      *bool                               `json:"dots_disabled,omitempty"`
	DisabledProviders *[]string                           `json:"disabled_providers,omitempty"`
	ProviderPriority  []string                            `json:"provider_priority,omitempty"`
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

// SaveDisabledProviders sets which provider families are disabled on this machine.
// Persisted to host_settings[shortHostname].disabled_providers.
// All other host settings and global settings are preserved.
func (a *App) SaveDisabledProviders(_ context.Context, disabled []string) error {
	for _, name := range disabled {
		if err := a.validateEcosystemProvider(name); err != nil {
			return err
		}
	}
	return a.patchCurrentHostSettings(func(hs *config.Settings) error {
		hs.DisabledProviders = append([]string{}, disabled...)
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

// SaveNodeManager sets the per-machine node ecosystem manager in host_settings.
// All other settings are preserved.
func (a *App) SaveNodeManager(_ context.Context, manager string) error {
	parsed := ""
	var err error
	if manager != "" {
		parsed, err = a.parseSettingManager(provider.EcosystemNode, manager)
		if err != nil {
			return err
		}
	}
	return a.patchCurrentHostSettings(func(hs *config.Settings) error {
		hs.SetEcosystemManager(provider.EcosystemNode, parsed)
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
			hs.SetEcosystemManager(ecosystem, concrete)
		case provider.EcosystemSystem:
			if eco, ok := provider.BuiltinEcosystemFor(concrete); !ok || eco != provider.EcosystemSystem || provider.BuiltinIsEcosystem(concrete) {
				return fmt.Errorf("%q is not a system provider", concrete)
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
// provider families would actually use right now, honouring settings hints and
// falling back to PATH probing in preference order.
//
// Returns "" for either ecosystem when no suitable binary is found on PATH.
func (a *App) EffectiveManagers() (pythonBin, nodeBin string) {
	s, _ := a.LoadSettings()
	return a.effectiveManagersFromSettings(s)
}

func (a *App) effectiveManagersFromSettings(s config.Settings) (pythonBin, nodeBin string) {
	pythonBin = probeFirst(s.EcosystemManager(provider.EcosystemPython), a.managerNames(provider.EcosystemPython))
	nodeBin = probeFirst(s.EcosystemManager(provider.EcosystemNode), a.managerNames(provider.EcosystemNode))
	return pythonBin, nodeBin
}

// ResolvedEcosystemProviders returns a map of provider-family name → concrete
// provider name for every provider family in the registry that implements
// provider.ConcreteResolver and is currently available.
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
	// Goroutines return nil unconditionally; Wait only fails on panic recovery.
	if err := g.Wait(); err != nil {
		return nil
	}
	result := make(map[string]string, len(out))
	for _, r := range out {
		if r.name != "" {
			result[r.name] = r.concrete
		}
	}
	return result
}

func (a *App) EffectiveSystemManager(ctx context.Context) string {
	return a.ResolvedEcosystemProviders(ctx)[provider.EcosystemSystem]
}

// AllAvailableManagers returns ALL binary names found on PATH for each ecosystem,
// in priority order. Unlike EffectiveManagers (which returns the single preferred
// binary), this is used by the setup wizard to display every available manager.
func (a *App) AllAvailableManagers() (pythonBins, nodeBins []string) {
	s, _ := a.LoadSettings()
	return a.allAvailableManagersFromSettings(s)
}

func (a *App) allAvailableManagersFromSettings(s config.Settings) (pythonBins, nodeBins []string) {
	pythonBins = probeAll(s.EcosystemManager(provider.EcosystemPython), a.managerNames(provider.EcosystemPython))
	nodeBins = probeAll(s.EcosystemManager(provider.EcosystemNode), a.managerNames(provider.EcosystemNode))
	return pythonBins, nodeBins
}

type SetupProviderOption struct {
	Name    string
	Label   string
	Enabled bool
}

func (a *App) SetupProviderOptions(ctx context.Context, settings config.Settings) []SetupProviderOption {
	pythonBins, nodeBins := a.allAvailableManagersFromSettings(settings)
	return SetupProviderOptionsFromManagers(a.ResolvedEcosystemProviders(ctx), pythonBins, nodeBins, settings)
}

func SetupProviderOptionsFromManagers(metaMap map[string]string, allPyBins, allNodeBins []string, settings config.Settings) []SetupProviderOption {
	managerLabel := func(meta string, bins []string) string {
		if len(bins) == 0 {
			return meta
		}
		return meta + "(" + strings.Join(bins, " • ") + ")"
	}

	type entry struct {
		name  string
		label string
	}
	entries := []entry{
		{provider.EcosystemSystem, provider.EcosystemSystem},
		{provider.EcosystemNode, managerLabel(provider.EcosystemNode, allNodeBins)},
		{provider.EcosystemPython, managerLabel(provider.EcosystemPython, allPyBins)},
	}
	if concrete := metaMap[provider.EcosystemSystem]; concrete != "" {
		entries[0].label = provider.EcosystemSystem + "(" + concrete + ")"
	}

	rows := make([]SetupProviderOption, 0, len(entries))
	for _, e := range entries {
		isEnabled := !slices.Contains(settings.DisabledProviders, e.name)
		rows = append(rows, SetupProviderOption{Name: e.name, Label: e.label, Enabled: isEnabled})
	}
	return rows
}

func SetupProviderEnabled(options []SetupProviderOption, name string) bool {
	for _, option := range options {
		if option.Name == name && option.Enabled {
			return true
		}
	}
	return false
}

func SetupDisabledProviders(options []SetupProviderOption) []string {
	var disabled []string
	for _, option := range options {
		if !option.Enabled {
			disabled = append(disabled, option.Name)
		}
	}
	return disabled
}

func SetupNodeProviderEnabled(options []SetupProviderOption) bool {
	return SetupProviderEnabled(options, provider.EcosystemNode)
}

// probeFirst returns hint if it is a non-empty string and exists on PATH.
// Otherwise it returns the first candidate from the priority list that is
// found on PATH, or "" if none are available.
func probeFirst(hint string, priority []string) string {
	if hint != "" {
		if managerAvailable(hint) {
			return hint
		}
	}
	for _, bin := range priority {
		if managerAvailable(bin) {
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
			if managerAvailable(bin) {
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

func managerAvailable(bin string) bool {
	resolved, _ := executor.ResolveCommand(bin)
	return resolved != bin
}

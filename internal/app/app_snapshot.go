package app

import (
	"context"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/profile"
	"github.com/lkshrk/omni/internal/provider"
)

// StartupSnapshot groups the read-heavy app state the TUI needs for its first
// render. Config-derived fields are built from one settings.json load.
type StartupSnapshot struct {
	Tools                  []*database.ToolCache
	Discovered             []*database.ToolCache
	Settings               config.Settings
	Taps                   []string
	Groups                 []*config.GroupConfig
	GroupNames             []string
	HostInfo               *HostInfo
	ToolMemberships        map[string][]string
	ToolGroups             map[string]string
	DotMemberships         map[string][]string
	IgnoreLabels           map[string]string
	GlobalIgnoredTools     []string
	ToolIgnores            map[string]bool
	GroupIgnores           map[string]map[string]bool
	ToolProviderPins       map[string]string
	ToolFallbacks          map[string]config.FallbackSpec
	EffectivePythonManager string
	EffectiveNodeManager   string
	EffectiveSystemManager string
	EcosystemProviders     map[string]string
	StowInstalled          bool
	DotsReminderService    *DotsReminderService
	DotsReminderServiceErr error
	DotsWatchService       *DotsWatchService
	DotsWatchServiceErr    error
	DotsConfigured         bool
	DotsSyncAvailability   DotsSyncAvailability
	DotsHistory            []DotsHistoryEntry
	DotsHistoryErr         error
	DotsState              *DotsState
	SetupProviders         []SetupProviderOption
	EcosystemProviderNames []string
}

type ToolScopeState struct {
	GlobalIgnoredTools []string
	HostIgnoredTools   []string
	ToolIgnores        map[string]bool
	ToolProviderPins   map[string]string
	ToolFallbacks      map[string]config.FallbackSpec
}

type ToolScopeDisplayState struct {
	IgnoreLabels     map[string]string
	ToolIgnores      map[string]bool
	GroupIgnores     map[string]map[string]bool
	ToolProviderPins map[string]string
	ToolFallbacks    map[string]config.FallbackSpec
}

type ToolGroupState struct {
	GroupNames      []string
	ToolGroups      map[string]string
	ToolMemberships map[string][]string
	HostInfo        *HostInfo
}

func (a *App) StartupSnapshot(ctx context.Context) (*StartupSnapshot, error) {
	defer profile.Start("app.startup.total")()

	stop := profile.Start("app.startup.load_config")
	cfg, err := a.loadConfig()
	stop()
	if err != nil {
		return nil, err
	}

	stop = profile.Start("app.startup.ecosystem_providers")
	ecosystemProviders := a.ResolvedEcosystemProviders(ctx)
	stop()

	stop = profile.Start("app.startup.list_tools")
	tools, err := a.listToolsFromConfig(ctx, cfg, "", ecosystemProviders)
	stop()
	if err != nil {
		return nil, err
	}

	stop = profile.Start("app.startup.config_state")
	settings := a.effectiveSettings(cfg)
	hostInfo := a.hostStatusFromConfig(cfg)
	stop()

	stop = profile.Start("app.startup.resolve_tools")
	resolved, _ := a.resolveTools(ctx, cfg, a.currentToolGroups(cfg))
	toolMemberships := toolMembershipMapFromResolved(resolved)
	toolGroupState := a.toolGroupStateFromConfigAndMemberships(cfg, toolMemberships, hostInfo)
	toolScopeState := a.toolScopeStateFromConfig(cfg)
	toolScopeDisplayState := BuildToolScopeDisplayState(toolScopeState)
	stop()

	stop = profile.Start("app.startup.effective_managers")
	pythonBin, nodeBin := a.effectiveManagersFromSettings(settings)
	stop()

	stop = profile.Start("app.startup.available_managers")
	allPyBins, allNodeBins := a.allAvailableManagersFromSettings(settings)
	setupProviders := SetupProviderOptionsFromManagers(ecosystemProviders, allPyBins, allNodeBins, settings)
	stop()

	stop = profile.Start("app.startup.dots_reminder_status")
	dotsReminderService, dotsReminderServiceErr := a.DotsReminderServiceStatus()
	stop()

	stop = profile.Start("app.startup.dots_watch_status")
	dotsWatchService, dotsWatchServiceErr := a.DotsWatchServiceStatus()
	stop()

	stop = profile.Start("app.startup.dots_history")
	dotsHistory, dotsHistoryErr := a.RecentDotsHistory(ctx, 3)
	stop()

	stop = profile.Start("app.startup.dots_state")
	dotsState, err := a.CachedDotsState(ctx)
	stop()
	if err != nil {
		return nil, err
	}

	stop = profile.Start("app.startup.list_discovered")
	discovered, _ := a.listDiscoveredFromConfig(ctx, cfg, ecosystemProviders)
	stop()

	stop = profile.Start("app.startup.stow_installed")
	stowInstalled := a.DotsStowInstalled(ctx)
	stop()

	stop = profile.Start("app.startup.ecosystem_provider_names")
	ecosystemProviderNames := a.EcosystemProviderNames()
	stop()

	return &StartupSnapshot{
		Tools:                  tools,
		Discovered:             discovered,
		Settings:               settings,
		Taps:                   collectTaps(cfg.Groups),
		Groups:                 append([]*config.GroupConfig(nil), cfg.Groups...),
		GroupNames:             toolGroupState.GroupNames,
		HostInfo:               hostInfo,
		ToolMemberships:        toolMemberships,
		ToolGroups:             toolGroupState.ToolGroups,
		DotMemberships:         dotMembershipMapFromConfig(cfg),
		IgnoreLabels:           toolScopeDisplayState.IgnoreLabels,
		GlobalIgnoredTools:     toolScopeState.GlobalIgnoredTools,
		ToolIgnores:            toolScopeDisplayState.ToolIgnores,
		GroupIgnores:           toolScopeDisplayState.GroupIgnores,
		ToolProviderPins:       toolScopeDisplayState.ToolProviderPins,
		ToolFallbacks:          toolScopeDisplayState.ToolFallbacks,
		EffectivePythonManager: pythonBin,
		EffectiveNodeManager:   nodeBin,
		EffectiveSystemManager: ecosystemProviders[provider.EcosystemSystem],
		EcosystemProviders:     ecosystemProviders,
		StowInstalled:          stowInstalled,
		DotsReminderService:    dotsReminderService,
		DotsReminderServiceErr: dotsReminderServiceErr,
		DotsWatchService:       dotsWatchService,
		DotsWatchServiceErr:    dotsWatchServiceErr,
		DotsConfigured:         DotsConfiguredInSettings(settings),
		DotsSyncAvailability:   DotsSyncAvailabilityInSettings(settings),
		DotsHistory:            dotsHistory,
		DotsHistoryErr:         dotsHistoryErr,
		DotsState:              dotsState,
		SetupProviders:         setupProviders,
		EcosystemProviderNames: ecosystemProviderNames,
	}, nil
}

func (a *App) CreateEmptyConfigStartupState(ctx context.Context) (*StartupSnapshot, error) {
	if err := a.CreateEmptyConfig(); err != nil {
		return nil, err
	}
	return a.StartupSnapshot(ctx)
}

func (a *App) ToolGroupState(ctx context.Context) (*ToolGroupState, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	memberships := a.toolMembershipMapFromConfig(ctx, cfg)
	hostInfo := a.hostStatusFromConfig(cfg)
	return a.toolGroupStateFromConfigAndMemberships(cfg, memberships, hostInfo), nil
}

func (a *App) toolGroupStateFromConfigAndMemberships(cfg *config.RootConfig, memberships map[string][]string, hostInfo *HostInfo) *ToolGroupState {
	return &ToolGroupState{
		GroupNames:      reusableGroupNames(cfg.Groups),
		ToolGroups:      ToolGroupLabelsForHost(memberships, hostInfo, currentMachineGroupName()),
		ToolMemberships: memberships,
		HostInfo:        hostInfo,
	}
}

func (a *App) ToolScopeState(_ context.Context) (*ToolScopeState, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	return a.toolScopeStateFromConfig(cfg), nil
}

func (a *App) ToolScopeDisplayState(ctx context.Context) (*ToolScopeDisplayState, error) {
	state, err := a.ToolScopeState(ctx)
	if err != nil {
		return nil, err
	}
	return BuildToolScopeDisplayState(state), nil
}

func (a *App) ToolScopeDisplayStateWithFallback(ctx context.Context, fallbackHostIgnore []string) (*ToolScopeDisplayState, error) {
	state, err := a.ToolScopeState(ctx)
	if err != nil {
		return nil, err
	}
	return buildToolScopeDisplayStateWithFallback(state, fallbackHostIgnore), nil
}

func BuildToolScopeDisplayState(state *ToolScopeState) *ToolScopeDisplayState {
	return buildToolScopeDisplayStateWithFallback(state, nil)
}

func buildToolScopeDisplayStateWithFallback(state *ToolScopeState, fallbackHostIgnore []string) *ToolScopeDisplayState {
	if state == nil {
		state = &ToolScopeState{}
	}
	if len(state.HostIgnoredTools) == 0 && len(fallbackHostIgnore) > 0 {
		stateCopy := *state
		stateCopy.HostIgnoredTools = append([]string(nil), fallbackHostIgnore...)
		state = &stateCopy
	}
	return &ToolScopeDisplayState{
		IgnoreLabels:     ignoreLabelsFromScopeState(state),
		ToolIgnores:      toolIgnoresFromScopeState(state.ToolIgnores),
		GroupIgnores:     groupIgnoresFromScopeState(state.GlobalIgnoredTools),
		ToolProviderPins: toolProviderPinsFromScopeState(state.ToolProviderPins),
		ToolFallbacks:    toolFallbacksFromScopeState(state.ToolFallbacks),
	}
}

func ignoreLabelsFromScopeState(state *ToolScopeState) map[string]string {
	labels := make(map[string]string)
	for _, name := range state.HostIgnoredTools {
		if name != "" {
			labels[name] = "global"
		}
	}
	for _, name := range state.GlobalIgnoredTools {
		if name != "" {
			labels[name] = "global"
		}
	}
	for name, ignored := range state.ToolIgnores {
		if ignored {
			labels[name] = "tool"
		}
	}
	return labels
}

func toolIgnoresFromScopeState(toolIgnores map[string]bool) map[string]bool {
	toolIgnoreCopy := make(map[string]bool, len(toolIgnores))
	for name, ignored := range toolIgnores {
		if ignored {
			toolIgnoreCopy[name] = true
		}
	}
	return toolIgnoreCopy
}

func groupIgnoresFromScopeState(globalIgnoredTools []string) map[string]map[string]bool {
	groupIgnores := make(map[string]map[string]bool)
	for _, name := range globalIgnoredTools {
		if name == "" {
			continue
		}
		if groupIgnores[name] == nil {
			groupIgnores[name] = make(map[string]bool)
		}
		groupIgnores[name]["global"] = true
	}
	return groupIgnores
}

func toolProviderPinsFromScopeState(pins map[string]string) map[string]string {
	pinCopy := make(map[string]string, len(pins))
	for name, pin := range pins {
		if pin != "" {
			pinCopy[name] = pin
		}
	}
	return pinCopy
}

func toolFallbacksFromScopeState(fallbacks map[string]config.FallbackSpec) map[string]config.FallbackSpec {
	out := make(map[string]config.FallbackSpec, len(fallbacks))
	for name, fallback := range fallbacks {
		if name != "" {
			out[name] = fallback
		}
	}
	return out
}

func (a *App) toolScopeStateFromConfig(cfg *config.RootConfig) *ToolScopeState {
	hostInfo := a.hostStatusFromConfig(cfg)
	var hostIgnored []string
	if hostInfo != nil && hostInfo.Active != "" {
		if host, ok := hostInfo.Hosts[hostInfo.Active]; ok {
			hostIgnored = append([]string(nil), host.Ignore...)
		}
	}
	toolIgnores, toolProviderPins, toolFallbacks := a.toolScopeFromConfig(cfg)
	return &ToolScopeState{
		GlobalIgnoredTools: append([]string(nil), cfg.Ignore.Tools...),
		HostIgnoredTools:   hostIgnored,
		ToolIgnores:        toolIgnores,
		ToolProviderPins:   toolProviderPins,
		ToolFallbacks:      toolFallbacks,
	}
}

func (a *App) toolScopeFromConfig(cfg *config.RootConfig) (map[string]bool, map[string]string, map[string]config.FallbackSpec) {
	toolIgnores := make(map[string]bool)
	pins := make(map[string]string)
	fallbacks := make(map[string]config.FallbackSpec)
	shortHost := shortHostname(currentHostname())
	for name, spec := range cfg.Tools {
		if spec.Ignore {
			toolIgnores[name] = true
		}
		if spec.Fallback != nil {
			fallbacks[name] = *spec.Fallback
		}
		if hostSpec, ok := spec.Hosts[shortHost]; ok {
			if hostSpec.InstallWith != "" {
				pins[name] = hostSpec.InstallWith
			}
		} else if spec.InstallWith != "" {
			pins[name] = spec.InstallWith
		}
	}
	return toolIgnores, pins, fallbacks
}

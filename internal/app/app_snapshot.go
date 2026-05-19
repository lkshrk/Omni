package app

import (
	"context"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/profile"
)

// StartupSnapshot groups the read-heavy app state the TUI needs for its first
// render. Config-derived fields are built from one settings.json load.
type StartupSnapshot struct {
	Tools                  []*database.ToolCache
	Discovered             []*database.ToolCache
	Settings               config.Settings
	Taps                   []string
	Groups                 []*config.GroupConfig
	HostInfo               *HostInfo
	ToolMemberships        map[string][]string
	DotMemberships         map[string][]string
	GlobalIgnoredTools     []string
	ToolIgnores            map[string]bool
	ToolProviderPins       map[string]string
	EffectivePythonManager string
	EffectiveNodeManager   string
	AllPythonManagers      []string
	AllNodeManagers        []string
	EcosystemProviders     map[string]string
	StowInstalled          bool
	DotsReminderService    *DotsReminderService
	DotsReminderServiceErr error
	DotsWatchService       *DotsWatchService
	DotsWatchServiceErr    error
	DotsHistory            []DotsHistoryEntry
	DotsHistoryErr         error
	BootstrapRequired      bool
	EcosystemProviderNames []string
	ConfiguredProviders    []string
	ProviderToolCounts     map[string]int
}

func (a *App) StartupSnapshot(ctx context.Context) (*StartupSnapshot, error) {
	defer profile.Start("app.startup.total")()

	stop := profile.Start("app.startup.load_config")
	cfg, err := a.loadConfig()
	stop()
	if err != nil {
		return nil, err
	}

	stop = profile.Start("app.startup.list_tools")
	tools, err := a.ListTools(ctx, "")
	stop()
	if err != nil {
		return nil, err
	}

	stop = profile.Start("app.startup.config_state")
	settings := a.effectiveSettings(cfg)
	hostInfo := a.hostStatusFromConfig(cfg)
	bootstrapRequired := false
	stop()
	if hostInfo != nil && hostInfo.Active != "" {
		stop = profile.Start("app.startup.host_bootstrap")
		completed, err := a.HostBootstrapCompleted(ctx, hostInfo.Active)
		stop()
		if err != nil {
			return nil, err
		}
		bootstrapRequired = !completed
	}

	stop = profile.Start("app.startup.resolve_tools")
	resolved, _ := a.resolveTools(ctx, cfg, cfg.Groups)
	providerToolCounts := a.configuredProviderToolCountsFromResolved(resolved)
	toolIgnores, toolProviderPins := a.toolScopeFromConfig(cfg)
	stop()

	stop = profile.Start("app.startup.effective_managers")
	pythonBin, nodeBin := a.effectiveManagersFromSettings(settings)
	stop()

	stop = profile.Start("app.startup.available_managers")
	allPyBins, allNodeBins := a.allAvailableManagersFromSettings(settings)
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

	stop = profile.Start("app.startup.list_discovered")
	discovered, _ := a.ListDiscovered(ctx)
	stop()

	stop = profile.Start("app.startup.ecosystem_providers")
	ecosystemProviders := a.ResolvedEcosystemProviders(ctx)
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
		HostInfo:               hostInfo,
		ToolMemberships:        toolMembershipMapFromResolved(resolved),
		DotMemberships:         dotMembershipMapFromConfig(cfg),
		GlobalIgnoredTools:     append([]string(nil), cfg.Ignore.Tools...),
		ToolIgnores:            toolIgnores,
		ToolProviderPins:       toolProviderPins,
		EffectivePythonManager: pythonBin,
		EffectiveNodeManager:   nodeBin,
		AllPythonManagers:      allPyBins,
		AllNodeManagers:        allNodeBins,
		EcosystemProviders:     ecosystemProviders,
		StowInstalled:          stowInstalled,
		DotsReminderService:    dotsReminderService,
		DotsReminderServiceErr: dotsReminderServiceErr,
		DotsWatchService:       dotsWatchService,
		DotsWatchServiceErr:    dotsWatchServiceErr,
		DotsHistory:            dotsHistory,
		DotsHistoryErr:         dotsHistoryErr,
		BootstrapRequired:      bootstrapRequired,
		EcosystemProviderNames: ecosystemProviderNames,
		ConfiguredProviders:    configuredProvidersFromCounts(providerToolCounts),
		ProviderToolCounts:     providerToolCounts,
	}, nil
}

func (a *App) toolScopeFromConfig(cfg *config.RootConfig) (map[string]bool, map[string]string) {
	toolIgnores := make(map[string]bool)
	pins := make(map[string]string)
	shortHost := shortHostname(currentHostname())
	for name, spec := range cfg.Tools {
		if spec.Ignore {
			toolIgnores[name] = true
		}
		if hostSpec, ok := spec.Hosts[shortHost]; ok {
			if hostSpec.InstallWith != "" {
				pins[name] = hostSpec.InstallWith
			}
		} else if spec.InstallWith != "" {
			pins[name] = spec.InstallWith
		}
	}
	return toolIgnores, pins
}

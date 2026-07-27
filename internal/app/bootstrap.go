package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/dots"
	"github.com/lkshrk/omni/internal/provider"
)

const bootstrapCompleteValue = "complete"

type BootstrapPlan struct {
	Providers            []ProviderInfo
	AnyProviderAvailable bool
	HasConfig            bool
	NodeManager          string
	PythonManager        string
	HostName             string
}

type BootstrapApplyOptions struct {
	NodeManager      string
	PythonManager    string
	UpdateQuarantine string
}

type BootstrapHostResult struct {
	Host    string
	Groups  []string
	Created bool
}

type BootstrapApplyResult struct {
	CreatedConfig bool
	ConfigPath    string
	NodeManager   string
	PythonManager string
	Host          BootstrapHostResult
}

type SetupHostResult struct {
	Host BootstrapHostResult
	Info *HostInfo
}

type SetupActivationHost struct {
	Host   string
	Groups []string
}

type SetupImportResult struct {
	Added    int
	HostInfo *HostInfo
}

type HostConfigCopyResult struct {
	Source string
	Target string
	Info   *HostInfo
}

type HostGroupsResult struct {
	Target string
	Groups []string
	Info   *HostInfo
}

type DotsRepoSetupResult struct {
	RepoPath string
	Entries  []config.DotEntry
}

func (a *App) BootstrapPlan(ctx context.Context) (BootstrapPlan, error) {
	providers, err := a.Providers(ctx)
	if err != nil {
		return BootstrapPlan{}, err
	}
	anyProviderAvailable := false
	for _, p := range providers {
		if p.Available {
			anyProviderAvailable = true
			break
		}
	}
	return BootstrapPlan{
		Providers:            providers,
		AnyProviderAvailable: anyProviderAvailable,
		HasConfig:            a.HasConfig(),
		NodeManager:          a.bootstrapManager(provider.EcosystemNode),
		PythonManager:        a.bootstrapManager(provider.EcosystemPython),
		HostName:             currentMachineGroupName(),
	}, nil
}

func (a *App) ApplyBootstrap(ctx context.Context, opts BootstrapApplyOptions) (BootstrapApplyResult, error) {
	createdConfig := !a.HasConfig()
	if err := a.CreateEmptyConfig(); err != nil {
		return BootstrapApplyResult{}, fmt.Errorf("creating config: %w", err)
	}

	if opts.UpdateQuarantine != "" {
		if _, err := parseQuarantineDuration(opts.UpdateQuarantine); err != nil {
			return BootstrapApplyResult{}, fmt.Errorf("update quarantine: %w", err)
		}
	}

	if opts.NodeManager != "" || opts.PythonManager != "" || opts.UpdateQuarantine != "" {
		var settings config.Settings
		for _, mgr := range []string{opts.PythonManager, opts.NodeManager} {
			canonical := config.NormalizeConcreteProvider(mgr)
			if canonical == "" {
				canonical = mgr
			}
			settings.ProviderPriority = promoteEcosystemConcrete(settings.ProviderPriority, canonical)
		}
		settings.UpdateQuarantine = opts.UpdateQuarantine
		if err := a.SaveSettings(ctx, settings); err != nil {
			return BootstrapApplyResult{}, fmt.Errorf("saving settings: %w", err)
		}
	}

	host, err := a.EnsureBootstrapHost()
	if err != nil {
		return BootstrapApplyResult{}, err
	}
	return BootstrapApplyResult{
		CreatedConfig: createdConfig,
		ConfigPath:    a.ConfigPath,
		NodeManager:   opts.NodeManager,
		PythonManager: opts.PythonManager,
		Host:          host,
	}, nil
}

func (a *App) SetupImport(ctx context.Context, disabled []string) (SetupImportResult, error) {
	importResult, err := a.Import(ctx, ImportOptions{SkipEcosystemProviders: disabled})
	if err != nil {
		return SetupImportResult{}, err
	}
	if len(disabled) > 0 {
		if err := a.SaveDisabledProviders(ctx, disabled); err != nil {
			return SetupImportResult{}, err
		}
	}
	hostInfo, err := a.HostStatus()
	if err != nil {
		return SetupImportResult{}, err
	}
	added := 0
	if importResult != nil {
		added = len(importResult.Added)
	}
	return SetupImportResult{Added: added, HostInfo: hostInfo}, nil
}

func (a *App) BootstrapHostName() string {
	return DefaultSetupHostName()
}

func DefaultSetupHostName() string {
	return currentMachineGroupName()
}

func SetupActivationHostSummary(info *HostInfo) SetupActivationHost {
	summary := SetupActivationHost{Host: DefaultSetupHostName()}
	if info == nil || info.Active == "" {
		return summary
	}
	summary.Host = info.Active
	if assignment, ok := info.Hosts[summary.Host]; ok {
		summary.Groups = append([]string(nil), assignment.Groups...)
	}
	return summary
}

func (a *App) EnsureBootstrapHost() (BootstrapHostResult, error) {
	active, groups, ok := a.ActiveHostInfo()
	if ok {
		return BootstrapHostResult{Host: active, Groups: groups}, nil
	}

	host := currentMachineGroupName()
	if err := a.EnsureHost(host); err != nil {
		return BootstrapHostResult{}, fmt.Errorf("creating host: %w", err)
	}
	active, groups, ok = a.ActiveHostInfo()
	if ok {
		return BootstrapHostResult{Host: active, Groups: groups, Created: true}, nil
	}
	return BootstrapHostResult{Host: host, Created: true}, nil
}

func (a *App) EnsureSetupHost(name string) (SetupHostResult, error) {
	if strings.TrimSpace(name) == "" {
		return SetupHostResult{}, fmt.Errorf("hostname is required")
	}
	host, err := a.EnsureBootstrapHost()
	if err != nil {
		return SetupHostResult{}, err
	}
	info, err := a.HostStatus()
	if err != nil {
		return SetupHostResult{}, err
	}
	return SetupHostResult{Host: host, Info: info}, nil
}

func (a *App) CopyHostConfigToCurrentHost(source string) (HostConfigCopyResult, error) {
	target := currentMachineGroupName()
	if err := a.CopyHostConfig(source, target); err != nil {
		return HostConfigCopyResult{}, err
	}
	info, err := a.HostStatus()
	if err != nil {
		return HostConfigCopyResult{}, err
	}
	return HostConfigCopyResult{Source: source, Target: target, Info: info}, nil
}

func (a *App) SetCurrentHostGroups(groups []string) (HostGroupsResult, error) {
	target := currentMachineGroupName()
	if err := a.SetHostGroups(target, groups); err != nil {
		return HostGroupsResult{}, err
	}
	info, err := a.HostStatus()
	if err != nil {
		return HostGroupsResult{}, err
	}
	return HostGroupsResult{Target: target, Groups: append([]string(nil), groups...), Info: info}, nil
}

func (a *App) CopyHostGroupsToCurrentHost(source string) (HostGroupsResult, error) {
	info, err := a.HostStatus()
	if err != nil {
		return HostGroupsResult{}, err
	}
	sourceHost, ok := info.Hosts[source]
	if !ok {
		return HostGroupsResult{}, fmt.Errorf("host %q not found", source)
	}
	return a.SetCurrentHostGroups(sourceHost.Groups)
}

func (a *App) ConfigureDotsRepo(ctx context.Context, path string) (DotsRepoSetupResult, error) {
	repoPath, err := normalizeDotsRepoSetupPath(path)
	if err != nil {
		return DotsRepoSetupResult{}, err
	}
	settings, err := a.LoadSettings()
	if err != nil {
		return DotsRepoSetupResult{}, fmt.Errorf("loading settings: %w", err)
	}
	settings.DotsRepo = repoPath
	settings.DotsDisabled = config.BoolPtr(false)
	if err := a.SaveSettings(ctx, settings); err != nil {
		return DotsRepoSetupResult{}, fmt.Errorf("saving dots repo: %w", err)
	}
	savedSettings, err := a.LoadSettings()
	if err != nil {
		return DotsRepoSetupResult{}, fmt.Errorf("loading saved settings: %w", err)
	}
	entries, err := a.BootstrapDotsEntries()
	if err != nil {
		return DotsRepoSetupResult{}, fmt.Errorf("bootstrap dots entries: %w", err)
	}
	return DotsRepoSetupResult{RepoPath: savedSettings.DotsRepo, Entries: append([]config.DotEntry(nil), entries...)}, nil
}

func normalizeDotsRepoSetupPath(path string) (string, error) {
	expanded, err := dots.ExpandPath(path)
	if err != nil {
		return "", err
	}
	repoPath, err := filepath.Abs(expanded)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(repoPath); err != nil {
		return "", fmt.Errorf("repo path %q: %w", repoPath, err)
	}
	return repoPath, nil
}

func (a *App) bootstrapManager(ecosystem string) string {
	manager := probeFirst("", a.managerNames(ecosystem))
	if manager == "" {
		return ""
	}
	if opt, ok := a.managerOption(ecosystem, manager); ok && opt.SettingsValue != "" {
		return opt.SettingsValue
	}
	return manager
}

func (a *App) HostBootstrapCompleted(ctx context.Context, host string) (bool, error) {
	host = strings.TrimSpace(machineGroupName(host))
	if host == "" {
		return false, fmt.Errorf("hostname is required")
	}
	db := a.readDB()
	if db == nil {
		return false, fmt.Errorf("database is not initialised")
	}
	value, err := db.GetState(ctx, a.bootstrapStateKey(host))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return value == bootstrapCompleteValue, nil
}

func (a *App) MarkHostBootstrapCompleted(ctx context.Context, host string) error {
	host = strings.TrimSpace(machineGroupName(host))
	if host == "" {
		return fmt.Errorf("hostname is required")
	}
	db := a.readDB()
	if db == nil {
		return fmt.Errorf("database is not initialised")
	}
	return db.SetState(ctx, a.bootstrapStateKey(host), bootstrapCompleteValue)
}

func (a *App) MarkCurrentHostBootstrapCompleted(ctx context.Context) error {
	active, _, ok := a.ActiveHostInfo()
	if !ok {
		return nil
	}
	return a.MarkHostBootstrapCompleted(ctx, active)
}

func (a *App) bootstrapStateKey(host string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(a.ConfigPath)))
	return fmt.Sprintf("bootstrap.completed.%s.%x", host, sum[:16])
}

package app

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

type SwitchResult struct {
	Name             string
	FromProvider     string
	ToProvider       string
	Package          string
	UninstallWarning error
}

type providerRepairTarget struct {
	name          string
	configProv    string
	installedWith string
}

func (a *App) Switch(ctx context.Context, name, fromProvider, toProvider string) (*SwitchResult, error) {
	if !a.knownProvider(fromProvider) {
		return nil, fmt.Errorf("unknown provider %q", fromProvider)
	}
	targetProvider, targetInstallWith, opProvider, toProv, err := a.switchTarget(toProvider)
	if err != nil {
		return nil, err
	}

	// Resolved outside withConfig: the route planner reads config itself and would deadlock under its lock.
	var authored config.ToolInstallSpec
	if spec, found, specErr := a.toolSpecSnapshot(name); specErr == nil && found {
		authored = a.planInstallRoute(ctx, name, spec, nil).ConfiguredInstall
	}

	var (
		result  *SwitchResult
		install config.ToolInstallSpec
		pkg     string
		tgtTool provider.Tool
		ver     string
	)
	err = a.withConfig(func(cfg *config.RootConfig) error {
		spec, ok := cfg.Tools[name]
		if !ok {
			return fmt.Errorf("tool %q with provider %q not found in config", name, fromProvider)
		}
		install = a.resolveInstallSpec(ctx, name, spec)
		if !installSpecMatchesProvider(install, fromProvider) {
			return fmt.Errorf("tool %q with provider %q not found in config", name, fromProvider)
		}
		pkg = install.EffectivePackage(name)

		tgtTool = provider.Tool{Name: name, Provider: opProvider, Package: pkg, Options: install.Options}
		if err := installWithProvider(ctx, toProv, tgtTool, targetInstallWith); err != nil {
			return fmt.Errorf("installing via %s: %w", toProvider, err)
		}
		var verifyErr error
		ver, verifyErr = verifyInstalledAfterInstall(ctx, toProv, tgtTool, targetInstallWith, opProvider)
		if verifyErr != nil {
			return verifyErr
		}

		result = &SwitchResult{
			Name:         name,
			FromProvider: fromProvider,
			ToProvider:   toProvider,
			Package:      pkg,
		}

		// Skip uninstall when from == to: it would undo the install (cross-backend migration within one provider).
		if fromProvider != toProvider {
			if uninstallErr := a.uninstallInstallSpec(ctx, install, name, opProvider, targetInstallWith); uninstallErr != nil {
				result.UninstallWarning = uninstallErr
			}
		}

		candidate := config.ToolInstallSpec{
			Provider: targetProvider,
			Package:  pkg,
			Options:  cloneOptionMap(install.Options),
		}
		// A recipe and its source describe the tool, not the provider. Writing back the materialized
		// options while dropping them would flatten a recipe-backed entry into a bare provider row, so a
		// reinstall that lands on the same provider keeps what the entry was authored with.
		if authored.Provider == targetProvider && authored.Recipe != nil {
			candidate.Options = cloneOptionMap(authored.Options)
			candidate.Bin = authored.Bin
			candidate.BinDir = authored.BinDir
			candidate.Source = authored.Source
			candidate.Recipe = authored.Recipe
		}
		setDefaultToolProviderCandidate(&spec, candidate)
		cfg.Tools[name] = spec
		return nil
	})
	if err != nil {
		if result == nil {
			return nil, err
		}
		return result, err
	}

	installedOwner := installedWithForOperation(ctx, toProv, opProvider, targetInstallWith)
	if err := a.readDB().Upsert(ctx, &database.ToolCache{
		Name:          name,
		Provider:      targetProvider,
		Package:       pkg,
		Installed:     true,
		InstalledWith: installedOwner,
		Version:       sql.NullString{String: ver, Valid: ver != ""},
		LastChecked:   time.Now(),
	}); err != nil {
		return result, fmt.Errorf("upserting switch cache for %s/%s: %w", targetProvider, name, err)
	}
	if install.Provider != targetProvider {
		if err := a.readDB().Delete(ctx, name, install.Provider, pkg); err != nil {
			return result, fmt.Errorf("deleting old switch cache for %s/%s: %w", fromProvider, name, err)
		}
	}
	if fromProvider != install.Provider && fromProvider != targetProvider {
		if err := a.readDB().Delete(ctx, name, fromProvider, pkg); err != nil {
			return result, fmt.Errorf("deleting old switch cache for %s/%s: %w", fromProvider, name, err)
		}
	}

	return result, nil
}

func (a *App) switchTarget(toProvider string) (targetProvider, targetInstallWith, opProvider string, prov provider.Provider, err error) {
	targetProvider, targetInstallWith = a.logicalInstallTarget(toProvider)
	opProvider = targetProvider
	if targetInstallWith != "" {
		if manager, ok := a.managerOption(targetProvider, targetInstallWith); ok {
			var resolved provider.Provider
			resolved, opProvider, err = a.managerOperationProvider(targetProvider, manager)
			if err != nil {
				return "", "", "", nil, err
			}
			return targetProvider, targetInstallWith, opProvider, resolved, nil
		}
		opProvider = toProvider
	}
	prov, ok := a.registry.Get(opProvider)
	if !ok {
		return "", "", "", nil, fmt.Errorf("unknown provider %q", toProvider)
	}
	return targetProvider, targetInstallWith, opProvider, prov, nil
}

// Implemented by providers that must remove a package from a specific concrete backend during cross-backend migration.
type oldEnvCleaner interface {
	UninstallFrom(ctx context.Context, tool provider.Tool, binary string) error
}

// MigrateInstallation — installedWith may be a raw binary (uv, pip3) rather than a registered provider name; configProv always is one.
func (a *App) MigrateInstallation(ctx context.Context, name, installedWith, configProv string) (res *SwitchResult, err error) {
	// Every branch below reinstalls without going through App.Install, so an unpinned recipe records the
	// release it landed on here; callers refresh installed state afterwards and read it back from config.
	defer func() {
		spec, found, snapshotErr := a.toolSpecSnapshot(name)
		if snapshotErr != nil || !found {
			return
		}
		_, recordErr := a.recordConfiguredGitHubRecipeVersion(ctx, name, a.planInstallRoute(ctx, name, spec, nil).ConfiguredInstall)
		if recordErr != nil && err == nil {
			err = recordErr
		}
	}()
	_, installedIsRegistered := a.registry.Get(installedWith)

	// Config already records configProv, so reinstall via it and remove from installedWith without a rewrite.
	if installedIsRegistered && installedWith != configProv {
		return a.migrateWrongProvider(ctx, name, installedWith, configProv)
	}

	if !installedIsRegistered {
		return a.migrateSameProviderBackend(ctx, name, installedWith, configProv)
	}
	from := installedWith
	result, err := a.Switch(ctx, name, from, configProv)
	if err != nil {
		return nil, err
	}

	// Switch skipped the uninstall when only the concrete backend changed, so clean the old env here.
	if from == configProv && installedWith != "" {
		if prov, ok := a.registry.Get(configProv); ok {
			if cleaner, ok := prov.(oldEnvCleaner); ok {
				tgt := provider.Tool{Name: name, Provider: configProv, Package: result.Package}
				// Best-effort: the tool may already be absent from the old env.
				if cleanErr := cleaner.UninstallFrom(ctx, tgt, installedWith); cleanErr != nil {
					result.UninstallWarning = cleanErr
				}
			}
		}
	}

	return result, nil
}

type ProviderRepairStateResult struct {
	Result *SwitchResult
	Tools  []*ToolView
}

func (a *App) SwitchWithState(ctx context.Context, name, fromProvider, toProvider string) (*ProviderRepairStateResult, error) {
	result, err := a.Switch(ctx, name, fromProvider, toProvider)
	if err != nil {
		return &ProviderRepairStateResult{Result: result}, err
	}
	return a.providerRepairState(ctx, result)
}

func (a *App) ApplyProviderSolutionWithState(ctx context.Context, name, fromProvider string, solution provider.ErrorSolution) (*ProviderRepairStateResult, error) {
	target := solution.TargetProvider
	if target == "" {
		return nil, fmt.Errorf("missing target provider")
	}
	return a.SwitchWithState(ctx, name, fromProvider, target)
}

func FirstApplicableProviderSolution(actionErr *provider.ActionError) (provider.ErrorSolution, bool) {
	idx := FirstApplicableProviderSolutionIndex(actionErr)
	if idx < 0 {
		return provider.ErrorSolution{}, false
	}
	return actionErr.Solutions[idx], true
}

func FirstApplicableProviderSolutionIndex(actionErr *provider.ActionError) int {
	if actionErr == nil {
		return -1
	}
	for i, solution := range actionErr.Solutions {
		if solution.Action == provider.ErrorSolutionActionSwitchProvider && solution.TargetProvider != "" {
			return i
		}
	}
	return -1
}

func (a *App) MigrateInstallationWithState(ctx context.Context, name, installedWith, configProv string) (*ProviderRepairStateResult, error) {
	result, err := a.MigrateInstallation(ctx, name, installedWith, configProv)
	if err != nil {
		return &ProviderRepairStateResult{Result: result}, err
	}
	return a.providerRepairState(ctx, result)
}

func (a *App) providerRepairState(ctx context.Context, result *SwitchResult) (*ProviderRepairStateResult, error) {
	state := &ProviderRepairStateResult{Result: result}
	if err := a.RefreshInstalled(ctx, nil); err != nil {
		return state, fmt.Errorf("refresh installed: %w", err)
	}
	if err := a.RefreshDescriptions(ctx, 0); err != nil {
		return state, fmt.Errorf("refresh descriptions: %w", err)
	}
	var err error
	state.Tools, err = a.ListToolsForView(ctx, "")
	if err != nil {
		return state, fmt.Errorf("list tools: %w", err)
	}
	return state, nil
}

func (a *App) migrateSameProviderBackend(ctx context.Context, name, installedWith, configProv string) (*SwitchResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	spec, ok := cfg.Tools[name]
	if !ok {
		return nil, fmt.Errorf("tool %q with provider %q not found in config", name, configProv)
	}
	install := a.resolveInstallSpec(ctx, name, spec)
	if !installSpecMatchesProvider(install, configProv) {
		return nil, fmt.Errorf("tool %q with provider %q not found in config", name, configProv)
	}
	pkg := install.EffectivePackage(name)
	opProvider := a.installOperationProviderName(install)
	toProv, ok := a.registry.Get(opProvider)
	if !ok {
		return nil, fmt.Errorf("unknown provider %q", opProvider)
	}

	tgtTool := provider.Tool{Name: name, Provider: opProvider, Package: pkg, Options: install.Options}
	if err := installWithProvider(ctx, toProv, tgtTool, install.InstallWith); err != nil {
		return nil, fmt.Errorf("installing via %s: %w", opProvider, err)
	}
	installedOwner := installedWithForOperation(ctx, toProv, opProvider, install.InstallWith)
	result := &SwitchResult{
		Name:         name,
		FromProvider: configProv,
		ToProvider:   opProvider,
		Package:      pkg,
	}
	ver, err := verifyInstalledAfterInstall(ctx, toProv, tgtTool, install.InstallWith, opProvider)
	if err != nil {
		return result, err
	}

	if installedWith != "" && installedWith != installedOwner {
		if prov, ok := a.registry.Get(configProv); ok {
			if cleaner, ok := prov.(oldEnvCleaner); ok {
				tgt := provider.Tool{Name: name, Provider: configProv, Package: pkg}
				if cleanErr := cleaner.UninstallFrom(ctx, tgt, installedWith); cleanErr != nil {
					result.UninstallWarning = cleanErr
				}
			}
		}
	}

	if err := a.readDB().Upsert(ctx, &database.ToolCache{
		Name:          name,
		Provider:      install.Provider,
		Package:       pkg,
		Installed:     true,
		InstalledWith: installedOwner,
		Version:       sql.NullString{String: ver, Valid: ver != ""},
		LastChecked:   time.Now(),
	}); err != nil {
		return result, fmt.Errorf("upserting migration cache for %s/%s: %w", install.Provider, name, err)
	}
	return result, nil
}

// ReinstallWithDefault — configProv is optional and only needed to disambiguate duplicate tool names.
func (a *App) ReinstallWithDefault(ctx context.Context, name, configProv string) (*SwitchResult, error) {
	target, err := a.providerRepairTarget(ctx, name, configProv)
	if err != nil {
		return nil, err
	}
	result, err := a.MigrateInstallation(ctx, target.name, target.installedWith, target.configProv)
	if err != nil {
		return result, err
	}
	if err := a.RefreshInstalled(ctx, nil); err != nil {
		return result, fmt.Errorf("refreshing installed state after reinstall: %w", err)
	}
	if err := a.RefreshDescriptions(ctx, 0); err != nil {
		return result, fmt.Errorf("refreshing descriptions after reinstall: %w", err)
	}
	return result, nil
}

func (a *App) ReinstallWithDefaultAfterClearingInstallOverride(ctx context.Context, name, configProv string) (*SwitchResult, ClearInstallOverrideResult, error) {
	target, err := a.providerRepairTarget(ctx, name, configProv)
	if err != nil {
		return nil, ClearInstallOverrideResult{}, err
	}
	originalSpec, found, err := a.toolSpecSnapshot(name)
	if err != nil {
		return nil, ClearInstallOverrideResult{}, err
	}
	cleared, err := a.ClearToolInstallOverride(ctx, name, target.configProv)
	if err != nil {
		return nil, ClearInstallOverrideResult{}, err
	}
	result, err := a.MigrateInstallation(ctx, target.name, target.installedWith, target.configProv)
	if err != nil {
		if restoreErr := a.restoreToolSpecSnapshot(name, originalSpec, found); restoreErr != nil {
			return result, cleared, fmt.Errorf("%w (restore provider override failed: %v)", err, restoreErr)
		}
		return result, cleared, err
	}
	if err := a.RefreshInstalled(ctx, nil); err != nil {
		return result, cleared, fmt.Errorf("refreshing installed state after reinstall: %w", err)
	}
	if err := a.RefreshDescriptions(ctx, 0); err != nil {
		return result, cleared, fmt.Errorf("refreshing descriptions after reinstall: %w", err)
	}
	return result, cleared, nil
}

type ClearProviderOverrideStateResult struct {
	Result       *SwitchResult
	Cleared      ClearInstallOverrideResult
	FromProvider string
	ToProvider   string
	Tools        []*ToolView
	ScopeDisplay *ToolScopeDisplayState
}

func (a *App) ClearProviderOverrideWithState(ctx context.Context, name, configProv, installedWith string) (*ClearProviderOverrideStateResult, error) {
	state := &ClearProviderOverrideStateResult{
		FromProvider: installedWith,
		ToProvider:   configProv,
	}
	var err error
	if installedWith != "" {
		state.Result, state.Cleared, err = a.ReinstallWithDefaultAfterClearingInstallOverride(ctx, name, configProv)
	} else {
		state.Cleared, err = a.ClearToolInstallOverride(ctx, name, configProv)
		if err == nil {
			if refreshErr := a.RefreshInstalled(ctx, nil); refreshErr != nil {
				err = fmt.Errorf("refresh installed: %w", refreshErr)
			}
		}
	}
	if state.Result != nil {
		state.FromProvider = state.Result.FromProvider
		state.ToProvider = state.Result.ToProvider
	} else if state.FromProvider == "" {
		state.FromProvider = state.Cleared.InstallWith
	}
	if err != nil {
		return state, err
	}
	state.Tools, err = a.ListToolsForView(ctx, "")
	if err != nil {
		return state, fmt.Errorf("list tools: %w", err)
	}
	state.ScopeDisplay, err = a.ToolScopeDisplayState(ctx)
	if err != nil {
		return state, err
	}
	return state, nil
}

func (a *App) toolSpecSnapshot(name string) (config.ToolSpec, bool, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return config.ToolSpec{}, false, fmt.Errorf("loading config: %w", err)
	}
	spec, ok := cfg.Tools[name]
	return spec, ok, nil
}

func (a *App) restoreToolSpecSnapshot(name string, spec config.ToolSpec, found bool) error {
	return a.withConfig(func(cfg *config.RootConfig) error {
		if found {
			if cfg.Tools == nil {
				cfg.Tools = make(map[string]config.ToolSpec)
			}
			cfg.Tools[name] = spec
		} else if cfg.Tools != nil {
			delete(cfg.Tools, name)
		}
		return nil
	})
}

func (a *App) providerRepairTarget(ctx context.Context, name, configProv string) (providerRepairTarget, error) {
	configuredProvider := configProv
	if configuredProvider == "" {
		if cfg, err := a.loadConfig(); err == nil {
			if spec, ok := cfg.Tools[name]; ok {
				install := a.resolveInstallSpec(ctx, name, spec)
				configuredProvider = install.Provider
			}
		}
	}
	tools, err := a.ListTools(ctx, "")
	if err != nil {
		return providerRepairTarget{}, fmt.Errorf("listing tools: %w", err)
	}
	var matches []*database.ToolCache
	for _, t := range tools {
		if t.Name != name {
			continue
		}
		if configuredProvider != "" && t.Provider != configuredProvider {
			continue
		}
		matches = append(matches, t)
	}
	if len(matches) == 0 {
		raw, err := a.readDB().List(ctx)
		if err != nil {
			return providerRepairTarget{}, fmt.Errorf("listing raw cached tools: %w", err)
		}
		for _, t := range raw {
			if t.Name == name && t.Installed {
				matches = append(matches, t)
			}
		}
		if len(matches) == 0 {
			if configuredProvider != "" {
				return providerRepairTarget{}, fmt.Errorf("tool %q with provider %q not found in cache", name, configuredProvider)
			}
			return providerRepairTarget{}, fmt.Errorf("tool %q not found in cache", name)
		}
	}
	if len(matches) > 1 {
		return providerRepairTarget{}, fmt.Errorf("tool %q has multiple cached providers; pass --provider to choose one", name)
	}
	t := matches[0]
	if t.InstalledWith == "" {
		return providerRepairTarget{}, fmt.Errorf("installed provider for %q is unknown; refresh installed state first", name)
	}
	targetProvider := configuredProvider
	if targetProvider == "" {
		targetProvider = t.Provider
	}
	return providerRepairTarget{name: t.Name, configProv: targetProvider, installedWith: t.InstalledWith}, nil
}

// The config entry is already correct: install via configProv, remove from installedWith, no rewrite.
func (a *App) migrateWrongProvider(ctx context.Context, name, installedWith, configProv string) (*SwitchResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	spec, ok := cfg.Tools[name]
	if !ok {
		return nil, fmt.Errorf("tool %q with provider %q not found in config", name, configProv)
	}
	install := a.resolveInstallSpec(ctx, name, spec)
	if !installSpecMatchesProvider(install, configProv) {
		return nil, fmt.Errorf("tool %q with provider %q not found in config", name, configProv)
	}
	pkg := install.EffectivePackage(name)

	opProvider := a.installOperationProviderName(install)
	toProv, ok := a.registry.Get(opProvider)
	if !ok {
		return nil, fmt.Errorf("unknown provider %q", opProvider)
	}

	tgtTool := provider.Tool{Name: name, Provider: opProvider, Package: pkg, Options: install.Options}
	if err := installWithProvider(ctx, toProv, tgtTool, install.InstallWith); err != nil {
		return nil, fmt.Errorf("installing via %s: %w", opProvider, err)
	}

	result := &SwitchResult{
		Name:         name,
		FromProvider: installedWith,
		ToProvider:   opProvider,
		Package:      pkg,
	}
	ver, err := verifyInstalledAfterInstall(ctx, toProv, tgtTool, install.InstallWith, opProvider)
	if err != nil {
		return result, err
	}

	// Skip when clearing an override revealed the same concrete owner as the ecosystem default.
	installedOwner := installedWithForOperation(ctx, toProv, opProvider, install.InstallWith)
	if installedWith == installedOwner {
		result.FromProvider = ""
	} else {
		if fromProv, ok := a.registry.Get(installedWith); ok {
			srcTool := provider.Tool{Name: name, Provider: installedWith, Package: pkg}
			if uninstallErr := fromProv.Uninstall(ctx, srcTool); uninstallErr != nil {
				result.UninstallWarning = uninstallErr
			}
		}
	}

	if err := a.readDB().Upsert(ctx, &database.ToolCache{
		Name:          name,
		Provider:      install.Provider,
		Package:       pkg,
		Installed:     true,
		InstalledWith: installedOwner,
		Version:       sql.NullString{String: ver, Valid: ver != ""},
		LastChecked:   time.Now(),
	}); err != nil {
		return result, fmt.Errorf("upserting migration cache for %s/%s: %w", install.Provider, name, err)
	}
	if installedWith != install.Provider && installedWith != installedOwner {
		if err := a.readDB().Delete(ctx, name, installedWith, pkg); err != nil {
			return result, fmt.Errorf("deleting old migration cache for %s/%s: %w", installedWith, name, err)
		}
	}
	if configProv != install.Provider && configProv != installedWith {
		if err := a.readDB().Delete(ctx, name, configProv, pkg); err != nil {
			return result, fmt.Errorf("deleting old migration cache for %s/%s: %w", configProv, name, err)
		}
	}
	if install.InstallWith != "" && install.InstallWith != install.Provider && install.InstallWith != installedWith {
		if err := a.readDB().Delete(ctx, name, install.InstallWith, pkg); err != nil {
			return result, fmt.Errorf("deleting old migration cache for %s/%s: %w", install.InstallWith, name, err)
		}
	}
	return result, nil
}

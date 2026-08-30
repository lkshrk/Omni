package app

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

type ConsolidateTool struct {
	Name         string
	FromProvider string
	Package      string
}

type ConsolidateFailure struct {
	ConsolidateTool
	Err error
}

type ConsolidateResult struct {
	Ecosystem         string
	Manager           string
	Migrated          []ConsolidateTool
	Failed            []ConsolidateFailure
	UninstallWarnings []ConsolidateFailure
	SettingsUpdated   bool
}

type ConsolidateStateResult struct {
	Result *ConsolidateResult
	Tools  []*ToolView
	State  *ToolGroupState
}

type EcosystemMigration struct {
	Ecosystem string
	Manager   string
}

func (a *App) ConsolidateOptions() []EcosystemMigration {
	var opts []EcosystemMigration
	for _, eco := range a.consolidatableEcosystems() {
		if !a.anyProviderForEcosystem(eco) {
			continue
		}
		for _, mgr := range a.managerOptions(eco) {
			if _, _, err := a.managerOperationProvider(eco, mgr); err != nil {
				continue
			}
			opts = append(opts, EcosystemMigration{Ecosystem: eco, Manager: mgr.Name})
		}
	}
	return opts
}

// ConsolidateToProvider — Install failures are non-fatal and collected in result.Failed.
func (a *App) ConsolidateToProvider(ctx context.Context, targetProvider string, dryRun bool, progress func(string)) (*ConsolidateResult, error) {
	configProvider, targetInstallWith := a.logicalInstallTarget(targetProvider)
	targetOpProvider := configProvider
	if targetInstallWith != "" {
		targetOpProvider = targetInstallWith
	}
	tgtProv, ok := a.registry.Get(targetOpProvider)
	if !ok {
		return nil, fmt.Errorf("unknown provider %q", targetProvider)
	}

	result := &ConsolidateResult{Manager: targetProvider}
	err := a.withConfig(func(cfg *config.RootConfig) error {
		for name, spec := range cfg.Tools {
			install := spec.DefaultInstallSpec()
			if install.Provider == configProvider && install.InstallWith == targetInstallWith {
				continue
			}
			ct := ConsolidateTool{
				Name:         name,
				FromProvider: install.Provider,
				Package:      install.EffectivePackage(name),
			}
			if dryRun {
				result.Migrated = append(result.Migrated, ct)
				continue
			}
			if progress != nil {
				progress(fmt.Sprintf("migrating %s (%s → %s)…", name, install.Provider, targetProvider))
			}
			tgtTool := provider.Tool{
				Name:     name,
				Provider: targetOpProvider,
				Package:  install.EffectivePackage(name),
				Options:  install.Options,
			}
			if err := installWithProvider(ctx, tgtProv, tgtTool, targetInstallWith); err != nil {
				result.Failed = append(result.Failed, ConsolidateFailure{ConsolidateTool: ct, Err: err})
				continue
			}
			_, err := verifyInstalledAfterInstall(ctx, tgtProv, tgtTool, targetInstallWith, targetOpProvider)
			if err != nil {
				result.Failed = append(result.Failed, ConsolidateFailure{ConsolidateTool: ct, Err: err})
				continue
			}
			if ue := a.uninstallInstallSpec(ctx, install, name, targetOpProvider, targetInstallWith); ue != nil {
				result.UninstallWarnings = append(result.UninstallWarnings, ConsolidateFailure{ConsolidateTool: ct, Err: ue})
			}
			installedOwner := installedWithForOperation(ctx, tgtProv, targetOpProvider, targetInstallWith)
			if err := a.readDB().Upsert(ctx, &database.ToolCache{
				Name:          name,
				Provider:      configProvider,
				Package:       install.EffectivePackage(name),
				Installed:     true,
				InstalledWith: installedOwner,
				LastChecked:   time.Now(),
			}); err != nil {
				return fmt.Errorf("upserting consolidate cache for %s/%s: %w", configProvider, name, err)
			}
			if install.Provider != configProvider {
				if err := a.readDB().Delete(ctx, name, install.Provider, install.EffectivePackage(name)); err != nil {
					return fmt.Errorf("deleting old consolidate cache for %s/%s: %w", install.Provider, name, err)
				}
			}
			if install.InstallWith != "" && install.InstallWith != install.Provider && install.InstallWith != configProvider {
				if err := a.readDB().Delete(ctx, name, install.InstallWith, install.EffectivePackage(name)); err != nil {
					return fmt.Errorf("deleting old consolidate cache for %s/%s: %w", install.InstallWith, name, err)
				}
			}
			setDefaultToolProviderCandidate(&spec, config.ToolInstallSpec{
				Provider:    configProvider,
				Package:     install.EffectivePackage(name),
				InstallWith: targetInstallWith,
				Options:     install.Options,
			})
			spec.Provider = ""
			spec.Package = ""
			spec.InstallWith = ""
			spec.Options = nil
			cfg.Tools[name] = spec
			result.Migrated = append(result.Migrated, ct)
		}
		if dryRun {
			return errSkipSave
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	return result, nil
}

func (a *App) installOperationProviderName(install config.ToolInstallSpec) string {
	if install.InstallWith != "" {
		if _, ok := a.registry.Get(install.InstallWith); ok {
			return install.InstallWith
		}
	}
	return install.Provider
}

func (a *App) managerOperationProvider(ecosystem string, manager provider.ManagerOption) (provider.Provider, string, error) {
	opProvider := manager.Provider
	if opProvider == "" {
		opProvider = manager.Name
	}
	if _, ok := a.registry.Get(opProvider); !ok {
		opProvider = ecosystem
	}
	prov, ok := a.registry.Get(opProvider)
	if !ok {
		return nil, "", fmt.Errorf("provider %q not registered", opProvider)
	}
	return prov, opProvider, nil
}

func (a *App) uninstallInstallSpec(ctx context.Context, install config.ToolInstallSpec, name, targetOpProvider, targetInstallWith string) error {
	srcProvName := a.installOperationProviderName(install)
	srcProv, ok := a.registry.Get(srcProvName)
	if !ok {
		return nil
	}
	if srcProvName == targetOpProvider && (install.InstallWith == "" || install.InstallWith == targetInstallWith || install.InstallWith == targetOpProvider) {
		return nil
	}
	srcTool := provider.Tool{Name: name, Provider: srcProvName, Package: install.EffectivePackage(name)}
	return uninstallWithProvider(ctx, srcProv, srcTool, install.InstallWith)
}

func (a *App) ConsolidatePlan(ctx context.Context, ecosystem, manager string) (*ConsolidateResult, error) {
	return a.runConsolidate(ctx, ecosystem, manager, true, nil)
}

func (a *App) Consolidate(ctx context.Context, ecosystem, manager string, progress func(string)) (*ConsolidateResult, error) {
	return a.runConsolidate(ctx, ecosystem, manager, false, progress)
}

func (a *App) ConsolidateWithState(ctx context.Context, ecosystem, manager string, progress func(string)) (*ConsolidateStateResult, error) {
	result, err := a.Consolidate(ctx, ecosystem, manager, progress)
	state, stateErr := a.toolGroupMutationState(ctx)
	if stateErr != nil {
		return nil, stateErr
	}
	return &ConsolidateStateResult{Result: result, Tools: state.Tools, State: state.State}, err
}

func (a *App) runConsolidate(ctx context.Context, ecosystem, manager string, dryRun bool, progress func(string)) (*ConsolidateResult, error) {
	if !a.knownEcosystemProvider(ecosystem) {
		return nil, fmt.Errorf("unknown ecosystem %q (supported: %s)", ecosystem, strings.Join(a.consolidatableEcosystems(), ", "))
	}
	spec, ok := a.managerOption(ecosystem, manager)
	if !ok {
		return nil, fmt.Errorf("unknown manager %q for ecosystem %q (supported: %s)",
			manager, ecosystem, strings.Join(a.managerNames(ecosystem), ", "))
	}

	tgtProv, targetOpProvider, err := a.managerOperationProvider(ecosystem, spec)
	if err != nil {
		return nil, err
	}
	targetConcrete := config.NormalizeConcreteProvider(spec.SettingsValue)
	if targetConcrete == "" {
		targetConcrete = config.NormalizeConcreteProvider(manager)
	}

	result := &ConsolidateResult{Ecosystem: ecosystem, Manager: manager}
	var cacheCommits []consolidateCacheCommit
	err = a.withConfig(func(cfg *config.RootConfig) error {
		settings := a.effectiveSettings(cfg)
		availability := make(map[string]bool)
		for name, toolSpec := range cfg.Tools {
			source := a.resolveInstallSpecWithSettings(ctx, name, toolSpec, availability, settings)
			sourceManager := source.InstallWith
			if sourceManager == "" {
				sourceManager = source.Provider
			}
			sourceEcosystem, ok := a.providerEcosystem(sourceManager)
			if !ok || sourceEcosystem != ecosystem {
				continue
			}
			target, updatedSpec := consolidateTargetSpec(toolSpec, source, sourceManager, targetConcrete)

			ct := ConsolidateTool{
				Name:         name,
				FromProvider: sourceManager,
				Package:      target.EffectivePackage(name),
			}

			if dryRun {
				if sourceManager != targetConcrete {
					result.Migrated = append(result.Migrated, ct)
				}
				continue
			}

			if sourceManager != targetConcrete && progress != nil {
				progress(fmt.Sprintf("migrating %s (%s → %s)…", name, sourceManager, manager))
			}

			tgtTool := provider.Tool{
				Name:     name,
				Provider: targetOpProvider,
				Package:  target.EffectivePackage(name),
				Options:  target.Options,
			}
			if sourceManager != targetConcrete {
				if err := installWithProvider(ctx, tgtProv, tgtTool, manager); err != nil {
					result.Failed = append(result.Failed, ConsolidateFailure{ConsolidateTool: ct, Err: err})
					continue
				}
			}
			ver, err := verifyInstalledAfterInstall(ctx, tgtProv, tgtTool, manager, targetOpProvider)
			if err != nil {
				result.Failed = append(result.Failed, ConsolidateFailure{ConsolidateTool: ct, Err: err})
				continue
			}
			if sourceManager != targetConcrete {
				if ue := a.uninstallInstallSpec(ctx, source, name, targetOpProvider, manager); ue != nil {
					result.UninstallWarnings = append(result.UninstallWarnings, ConsolidateFailure{ConsolidateTool: ct, Err: ue})
				}
				result.Migrated = append(result.Migrated, ct)
			}
			cfg.Tools[name] = updatedSpec
			cacheCommits = append(cacheCommits, consolidateCacheCommit{
				Name: name, Ecosystem: ecosystem, Provider: targetConcrete,
				Package: target.EffectivePackage(name), Version: ver,
			})
		}

		if dryRun {
			return errSkipSave
		}
		if spec.SettingsValue != "" {
			hostname := shortHostname(currentHostname())
			if cfg.HostSettings == nil {
				cfg.HostSettings = make(map[string]config.Settings)
			}
			hs := cfg.HostSettings[hostname]
			if EffectiveEcosystemManager(hs, ecosystem) != spec.SettingsValue {
				canonical := config.NormalizeConcreteProvider(spec.SettingsValue)
				if canonical == "" {
					canonical = spec.SettingsValue
				}
				hs.ProviderPriority = promoteEcosystemConcrete(hs.ProviderPriority, canonical)
				cfg.HostSettings[hostname] = hs
				result.SettingsUpdated = true
			}
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	if !dryRun {
		for _, commit := range cacheCommits {
			if err := a.reconcileConsolidatedCache(ctx, commit); err != nil {
				return result, err
			}
		}
	}
	return result, nil
}

type consolidateCacheCommit struct {
	Name, Ecosystem, Provider, Package, Version string
}

func consolidateTargetSpec(toolSpec config.ToolSpec, source config.ToolInstallSpec, sourceManager, targetConcrete string) (config.ToolInstallSpec, config.ToolSpec) {
	for _, candidate := range toolSpec.Providers {
		if config.NormalizeConcreteProvider(candidate.Provider) == targetConcrete {
			return candidate, toolSpec
		}
	}

	target := cloneToolInstallSpec(source)
	target.Provider = targetConcrete
	target.InstallWith = ""
	if len(toolSpec.Providers) == 0 {
		source.Provider = config.NormalizeConcreteProvider(sourceManager)
		source.InstallWith = ""
		toolSpec.Providers = append(toolSpec.Providers, cloneToolInstallSpec(source))
	}
	toolSpec.Providers = append(toolSpec.Providers, cloneToolInstallSpec(target))
	toolSpec.Provider = ""
	toolSpec.Package = ""
	toolSpec.InstallWith = ""
	toolSpec.Options = nil
	return target, toolSpec
}

func (a *App) reconcileConsolidatedCache(ctx context.Context, commit consolidateCacheCommit) error {
	db := a.readDB()
	rows, err := db.List(ctx)
	if err != nil {
		return fmt.Errorf("listing consolidate cache for %s: %w", commit.Name, err)
	}
	for _, row := range rows {
		rowEcosystem, ok := a.providerEcosystem(row.Provider)
		if row.Name != commit.Name || !ok || rowEcosystem != commit.Ecosystem || (row.Provider == commit.Provider && row.Package == commit.Package) {
			continue
		}
		if err := db.Delete(ctx, row.Name, row.Provider, row.Package); err != nil {
			return fmt.Errorf("deleting old consolidate cache for %s/%s: %w", row.Provider, row.Name, err)
		}
	}
	if err := db.Upsert(ctx, &database.ToolCache{
		Name: commit.Name, Provider: commit.Provider, Package: commit.Package,
		Installed: true, InstalledWith: commit.Provider, Tracked: true,
		Version: sql.NullString{String: commit.Version, Valid: commit.Version != ""}, LastChecked: time.Now(),
	}); err != nil {
		return fmt.Errorf("upserting consolidate cache for %s/%s: %w", commit.Provider, commit.Name, err)
	}
	return nil
}

func (a *App) consolidatableEcosystems() []string {
	seen := make(map[string]struct{})
	var ecosystems []string
	for _, eco := range provider.BuiltinEcosystemNames() {
		if len(provider.BuiltinManagerOptions(eco)) == 0 {
			continue
		}
		seen[eco] = struct{}{}
		ecosystems = append(ecosystems, eco)
	}
	if a.registry != nil {
		for _, eco := range a.registry.EcosystemNames() {
			if len(a.registry.ManagerOptions(eco)) == 0 {
				continue
			}
			if _, ok := seen[eco]; ok {
				continue
			}
			seen[eco] = struct{}{}
			ecosystems = append(ecosystems, eco)
		}
	}
	sort.Strings(ecosystems)
	return ecosystems
}

func (a *App) managerOptions(ecosystem string) []provider.ManagerOption {
	if a.registry != nil {
		if opts := a.registry.ManagerOptions(ecosystem); len(opts) > 0 {
			return opts
		}
	}
	return provider.BuiltinManagerOptions(ecosystem)
}

func (a *App) managerOption(ecosystem, manager string) (provider.ManagerOption, bool) {
	if a.registry != nil {
		if opt, ok := a.registry.ManagerOption(ecosystem, manager); ok {
			return opt, true
		}
	}
	return provider.BuiltinManagerOption(ecosystem, manager)
}

func (a *App) managerNames(ecosystem string) []string {
	opts := a.managerOptions(ecosystem)
	names := make([]string, 0, len(opts))
	for _, opt := range opts {
		names = append(names, opt.Name)
	}
	return names
}

func (a *App) anyProviderForEcosystem(ecosystem string) bool {
	if a.registry == nil {
		return false
	}
	for _, name := range a.registry.Names() {
		if eco, ok := a.providerEcosystem(name); ok && eco == ecosystem {
			return true
		}
	}
	return false
}

func (a *App) providersForEcosystem(ecosystem string) map[string]struct{} {
	srcSet := map[string]struct{}{ecosystem: {}}
	if a.registry != nil {
		for _, name := range a.registry.Names() {
			if eco, ok := a.providerEcosystem(name); ok && eco == ecosystem {
				srcSet[name] = struct{}{}
			}
		}
	}
	for name, eco := range provider.BuiltinConcreteEcosystems() {
		if eco == ecosystem {
			srcSet[name] = struct{}{}
		}
	}
	return srcSet
}

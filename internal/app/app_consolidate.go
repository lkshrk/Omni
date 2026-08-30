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
	if !dryRun {
		a.installedStateMu.Lock()
		defer a.installedStateMu.Unlock()
		release, lockErr := a.lockInstalledStateFile(false)
		if lockErr != nil {
			return result, lockErr
		}
		defer release()
	}
	a.configMu.Lock()
	defer a.configMu.Unlock()
	cfg, err := a.loadConfig()
	if err != nil {
		return result, err
	}
	proposed := cloneConsolidateConfig(cfg)
	settings := a.effectiveSettings(cfg)
	availability := make(map[string]bool)
	var plans []ecosystemConsolidatePlan
	for name, toolSpec := range cfg.Tools {
		source := a.resolveInstallSpecWithSettings(ctx, name, toolSpec, availability, settings)
		sourceOwner := source.InstallWith
		if sourceOwner == "" {
			sourceOwner = source.Provider
		}
		sourceConcrete := config.NormalizeConcreteProvider(sourceOwner)
		if sourceConcrete == "" {
			sourceConcrete = sourceOwner
		}
		sourceEcosystem, ok := a.providerEcosystem(sourceConcrete)
		if !ok || sourceEcosystem != ecosystem {
			continue
		}
		target, updatedSpec := consolidateTargetSpec(toolSpec, source, sourceConcrete, targetConcrete)
		plan := ecosystemConsolidatePlan{
			Name: name, Ecosystem: ecosystem, Source: source, SourceOwner: sourceOwner, SourceConcrete: sourceConcrete,
			Target: target, TargetConcrete: targetConcrete, UpdatedSpec: updatedSpec,
			TargetTool: provider.Tool{Name: name, Provider: targetOpProvider, Package: target.EffectivePackage(name), Options: target.Options},
		}
		plan.Change = ConsolidateTool{Name: name, FromProvider: sourceConcrete, Package: target.EffectivePackage(name)}
		plan.NeedsMigration = sourceConcrete != targetConcrete
		plans = append(plans, plan)
		proposed.Tools[name] = updatedSpec
		if dryRun && plan.NeedsMigration {
			result.Migrated = append(result.Migrated, plan.Change)
		}
	}

	settingsUpdated := applyConsolidateManagerSetting(a, proposed, ecosystem, spec.SettingsValue)
	if a.registry != nil {
		if errs := fatalValidationErrors(config.ValidateRoot(proposed, a.providerValidation())); len(errs) > 0 {
			return result, config.ValidationErrors(errs)
		}
	}
	if dryRun {
		return result, nil
	}

	for i := range plans {
		plan := &plans[i]
		if plan.NeedsMigration {
			if progress != nil {
				progress(fmt.Sprintf("migrating %s (%s → %s)…", plan.Name, plan.SourceConcrete, manager))
			}
			if err := installWithProvider(ctx, tgtProv, plan.TargetTool, manager); err != nil {
				result.Failed = append(result.Failed, ConsolidateFailure{ConsolidateTool: plan.Change, Err: err})
				continue
			}
			plan.InstalledTarget = true
		}
		plan.Version, err = verifyInstalledAfterInstall(ctx, tgtProv, plan.TargetTool, manager, targetOpProvider)
		if err != nil {
			result.Failed = append(result.Failed, ConsolidateFailure{ConsolidateTool: plan.Change, Err: err})
		}
	}
	if len(result.Failed) > 0 {
		if cleanupErr := cleanupConsolidateTargets(ctx, tgtProv, manager, plans); cleanupErr != nil {
			return result, fmt.Errorf("rolling back consolidate targets after install failure: %w", cleanupErr)
		}
		return result, nil
	}

	var providers *config.ProviderValidation
	if a.registry != nil {
		pv := a.providerValidation()
		providers = &pv
	}
	if err := config.WriteConfig(a.ConfigPath, a.loadConfig, providers, func(current *config.RootConfig) error {
		*current = *proposed
		return nil
	}); err != nil {
		if cleanupErr := cleanupConsolidateTargets(ctx, tgtProv, manager, plans); cleanupErr != nil {
			return result, fmt.Errorf("saving consolidate config: %w (target cleanup failed: %v)", err, cleanupErr)
		}
		return result, fmt.Errorf("saving consolidate config: %w", err)
	}
	result.SettingsUpdated = settingsUpdated

	for i := range plans {
		plan := &plans[i]
		sourceRemoved := false
		if plan.NeedsMigration {
			if uninstallErr := a.uninstallInstallSpec(ctx, plan.Source, plan.Name, targetOpProvider, manager); uninstallErr != nil {
				result.UninstallWarnings = append(result.UninstallWarnings, ConsolidateFailure{ConsolidateTool: plan.Change, Err: uninstallErr})
			} else {
				sourceRemoved = true
			}
			result.Migrated = append(result.Migrated, plan.Change)
		}
		if err := a.reconcileConsolidatedCache(ctx, *plan, sourceRemoved); err != nil {
			return result, err
		}
	}
	return result, nil
}

type ecosystemConsolidatePlan struct {
	Name, Ecosystem, SourceOwner, SourceConcrete, TargetConcrete, Version string
	Source, Target                                                        config.ToolInstallSpec
	UpdatedSpec                                                           config.ToolSpec
	TargetTool                                                            provider.Tool
	Change                                                                ConsolidateTool
	NeedsMigration, InstalledTarget                                       bool
}

func cloneConsolidateConfig(cfg *config.RootConfig) *config.RootConfig {
	cloned := *cfg
	cloned.Tools = make(map[string]config.ToolSpec, len(cfg.Tools))
	for name, tool := range cfg.Tools {
		cloned.Tools[name] = tool
	}
	cloned.HostSettings = make(map[string]config.Settings, len(cfg.HostSettings))
	for host, settings := range cfg.HostSettings {
		cloned.HostSettings[host] = settings
	}
	return &cloned
}

func applyConsolidateManagerSetting(a *App, cfg *config.RootConfig, ecosystem, settingsValue string) bool {
	if settingsValue == "" || EffectiveEcosystemManager(a.effectiveSettings(cfg), ecosystem) == settingsValue {
		return false
	}
	hostname := shortHostname(currentHostname())
	hs := cfg.HostSettings[hostname]
	canonical := config.NormalizeConcreteProvider(settingsValue)
	if canonical == "" {
		canonical = settingsValue
	}
	hs.ProviderPriority = promoteEcosystemConcrete(hs.ProviderPriority, canonical)
	cfg.HostSettings[hostname] = hs
	return true
}

func consolidateTargetSpec(toolSpec config.ToolSpec, source config.ToolInstallSpec, sourceConcrete, targetConcrete string) (config.ToolInstallSpec, config.ToolSpec) {
	for _, candidate := range toolSpec.Providers {
		if config.NormalizeConcreteProvider(candidate.Provider) == targetConcrete {
			return candidate, toolSpec
		}
	}

	target := cloneToolInstallSpec(source)
	target.Provider = targetConcrete
	target.InstallWith = ""
	if len(toolSpec.Providers) == 0 {
		source.Provider = sourceConcrete
		source.InstallWith = ""
		toolSpec.Providers = append(toolSpec.Providers, cloneToolInstallSpec(source))
		if sourceConcrete == targetConcrete {
			target = source
		}
	}
	if sourceConcrete != targetConcrete {
		toolSpec.Providers = append(toolSpec.Providers, cloneToolInstallSpec(target))
	}
	toolSpec.Provider = ""
	toolSpec.Package = ""
	toolSpec.InstallWith = ""
	toolSpec.Options = nil
	return target, toolSpec
}

func cleanupConsolidateTargets(ctx context.Context, target provider.Provider, manager string, plans []ecosystemConsolidatePlan) error {
	var firstErr error
	for i := len(plans) - 1; i >= 0; i-- {
		if !plans[i].InstalledTarget {
			continue
		}
		if err := uninstallWithProvider(ctx, target, plans[i].TargetTool, manager); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (a *App) reconcileConsolidatedCache(ctx context.Context, plan ecosystemConsolidatePlan, sourceRemoved bool) error {
	db := a.readDB()
	rows, err := db.List(ctx)
	if err != nil {
		return fmt.Errorf("listing consolidate cache for %s: %w", plan.Name, err)
	}
	if err := db.Upsert(ctx, &database.ToolCache{
		Name: plan.Name, Provider: plan.TargetConcrete, Package: plan.Target.EffectivePackage(plan.Name),
		Installed: true, InstalledWith: plan.TargetConcrete, Tracked: true,
		Version: sql.NullString{String: plan.Version, Valid: plan.Version != ""}, LastChecked: time.Now(),
	}); err != nil {
		return fmt.Errorf("upserting consolidate cache for %s/%s: %w", plan.TargetConcrete, plan.Name, err)
	}
	if !plan.NeedsMigration {
		return nil
	}
	sourceRows := consolidateSourceCacheRows(rows, plan)
	if sourceRemoved {
		for _, row := range sourceRows {
			if err := db.Delete(ctx, row.Name, row.Provider, row.Package); err != nil {
				return fmt.Errorf("deleting removed consolidate source cache for %s/%s: %w", row.Provider, row.Name, err)
			}
		}
		return nil
	}

	installed, version := true, ""
	if sourceProvider, ok := a.registry.Get(a.installOperationProviderName(plan.Source)); ok {
		sourceTool := provider.Tool{Name: plan.Name, Provider: sourceProvider.Name(), Package: plan.Source.EffectivePackage(plan.Name), Options: plan.Source.Options}
		if actual, actualVersion, checkErr := installedWithProvider(ctx, sourceProvider, sourceTool, plan.Source.InstallWith); checkErr == nil {
			installed, version = actual, actualVersion
		}
	}
	if len(sourceRows) == 0 {
		sourceRows = []*database.ToolCache{{Name: plan.Name, Provider: plan.SourceConcrete, Package: plan.Source.EffectivePackage(plan.Name), InstalledWith: plan.SourceOwner}}
	}
	for _, row := range sourceRows {
		row.Installed = installed
		row.Tracked = false
		row.LastChecked = time.Now()
		if version != "" {
			row.Version = sql.NullString{String: version, Valid: true}
		}
		if err := db.Upsert(ctx, row); err != nil {
			return fmt.Errorf("refreshing failed-uninstall source cache for %s/%s: %w", row.Provider, row.Name, err)
		}
		if err := db.MarkUntracked(ctx, row.Name, row.Provider, row.Package); err != nil {
			return err
		}
	}
	return nil
}

func consolidateSourceCacheRows(rows []*database.ToolCache, plan ecosystemConsolidatePlan) []*database.ToolCache {
	packageName := plan.Source.EffectivePackage(plan.Name)
	var matches []*database.ToolCache
	for _, row := range rows {
		if row.Name != plan.Name || row.Package != packageName {
			continue
		}
		owner := config.NormalizeConcreteProvider(row.InstalledWith)
		if row.Provider == plan.Source.Provider || row.Provider == plan.SourceConcrete || (row.Provider == plan.Ecosystem && owner == plan.SourceConcrete) {
			matches = append(matches, row)
		}
	}
	return matches
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

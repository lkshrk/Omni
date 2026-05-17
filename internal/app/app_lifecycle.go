package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
	isync "github.com/lkshrk/omni/internal/sync"
)

// ─── Sync ─────────────────────────────────────────────────────────────────────

// Sync syncs taps first, then tools. When AutoImport is enabled it also runs
// Import so newly installed tools are captured in the config.
// opts.Group restricts the sync to one named group.
// When opts.Group is empty, the active host's special group plus assigned
// reusable groups are synced.
func (a *App) Sync(ctx context.Context, opts isync.SyncOptions) (*isync.SyncResult, error) {
	if !opts.DryRun {
		if err := a.refreshInstalledIfStale(ctx, opts.Progress); err != nil {
			return nil, fmt.Errorf("refreshing installed state: %w", err)
		}
	}
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	groups := cfg.Groups

	var activeGroups []*config.GroupConfig

	switch {
	case opts.Group != "":
		groups = filterGroups(groups, opts.Group)
		if len(groups) == 0 {
			return nil, fmt.Errorf("group %q not found", opts.Group)
		}
	default:
		hostname := currentMachineGroupName()
		effective, active, ok := effectiveHostGroups(cfg, groups, hostname)
		if !ok {
			return nil, fmt.Errorf("no host configuration for %q - run 'omni bootstrap' to set one up", hostname)
		}
		groups = effective
		activeGroups = active
	}

	// Resolve once and derive both the entry list (for the syncer) and the
	// detailed view (for tap collection) from the same pass.
	resolvedDetailed, warnings := a.resolveTools(ctx, cfg, groups)
	resolvedTools := make([]config.ToolEntry, 0, len(resolvedDetailed))
	for _, t := range resolvedDetailed {
		resolvedTools = append(resolvedTools, t.entry)
	}
	taps := collectResolvedTaps(resolvedDetailed)
	if err = a.syncTaps(ctx, taps, opts.DryRun); err != nil {
		return nil, err
	}

	// Build a flat config view for the syncer; logical tools are deduplicated
	// by the resolver across group memberships.
	flatCfg := &config.Config{Tools: resolvedTools, Settings: cfg.Settings}

	opts.IgnoreList = cfg.Ignore.Tools

	// Construct syncer per call so Sync always sees the current *database.DB,
	// avoiding stale references after ResetCache rotates the connection.
	result, err := isync.New(a.registry, a.readDB()).Sync(ctx, flatCfg, opts)
	if err != nil {
		return result, err
	}
	result.Warnings = append(result.Warnings, warnings...)

	if !opts.DryRun {
		if opts.Group == "" {
			// Collect installed tools not covered by the active host and
			// append them to the machine group so nothing is lost.
			if err := a.syncOrphansToMachineGroup(ctx, activeGroups); err != nil {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("syncing orphans to machine group: %v", err))
			}

			// Report non-active groups that are now fully installed so the CLI
			// can prompt the user to add them to the host.
			activeNames := groupBaseNames(activeGroups)
			if satisfied, e := a.CheckSatisfiedGroups(ctx, activeNames); e == nil {
				result.SatisfiedGroups = satisfied
			}
		} else if cfg.Settings.AutoImport {
			if _, err := a.Import(ctx, ImportOptions{Group: currentMachineGroupName()}); err != nil {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("auto-importing machine group: %v", err))
			}
		}
	}
	return result, nil
}

func (a *App) SyncAll(ctx context.Context, opts SyncAllOptions) (*SyncAllResult, error) {
	discovered := opts.Discovered
	if discovered == nil {
		if opts.DryRun {
			var err error
			discovered, err = a.previewDiscovered(ctx)
			if err != nil {
				return nil, fmt.Errorf("previewing discovered tools: %w", err)
			}
		} else {
			if err := a.RefreshDiscovered(ctx); err != nil {
				return nil, fmt.Errorf("refreshing discovered tools: %w", err)
			}
			var err error
			discovered, err = a.ListDiscovered(ctx)
			if err != nil {
				return nil, fmt.Errorf("listing discovered tools: %w", err)
			}
		}
	}

	if !opts.DryRun {
		if err := a.EnsureHost(currentMachineGroupName()); err != nil {
			return nil, err
		}
	}
	var normalized []NormalizedInstallOverride
	if !opts.DryRun {
		var err error
		normalized, err = a.NormalizeHostDefaultInstallOverrides(ctx)
		if err != nil {
			return nil, fmt.Errorf("normalizing default provider overrides: %w", err)
		}
		if len(normalized) > 0 && opts.Progress != nil {
			opts.Progress(fmt.Sprintf("normalized %d default provider overrides…", len(normalized)))
		}
	}
	claimGroup := opts.Group
	if claimGroup == "" {
		claimGroup = currentMachineGroupName()
	}
	claimedNames, claimFailures, claimErr := a.claimDiscoveredTools(ctx, discovered, claimGroup, opts)
	syncResult, syncErr := a.Sync(ctx, isync.SyncOptions{
		DryRun:         opts.DryRun,
		Progress:       opts.Progress,
		ToolProgress:   opts.ToolProgress,
		SkipPrivileged: opts.SkipPrivileged,
	})
	failures := append([]BulkToolError(nil), claimFailures...)
	if syncResult != nil {
		for _, op := range syncResult.Failed() {
			failures = append(failures, bulkToolErrorFromError(op.Tool.Name, op.Tool.Provider, op.Err))
		}
	}
	return &SyncAllResult{SyncResult: syncResult, ClaimedNames: claimedNames, NormalizedProviderOverrides: normalized, Failures: failures}, errors.Join(claimErr, syncErr)
}

func bulkToolErrorFromError(name, providerName string, err error) BulkToolError {
	failure := BulkToolError{Name: name, Provider: providerName}
	if err != nil {
		failure.Message = err.Error()
		if actionErr, ok := provider.ActionErrorFrom(err); ok {
			failure.ActionError = actionErr
		}
	}
	return failure
}

func (a *App) claimDiscoveredTools(ctx context.Context, discovered []*database.ToolCache, groupName string, opts SyncAllOptions) ([]string, []BulkToolError, error) {
	var claimed []string
	var failures []BulkToolError
	var errs []error
	resolvedEcosystems := a.ResolvedEcosystemProviders(ctx)
	for _, t := range discovered {
		if t == nil || !t.Installed || t.Name == "" || t.Provider == "" {
			continue
		}
		if opts.Progress != nil {
			if opts.DryRun {
				opts.Progress("would claim " + t.Name + "…")
			} else {
				opts.Progress("claiming " + t.Name + "…")
			}
		}
		configProvider := a.searchResultConfigProvider(t.Provider)
		installWith := configInstallWithForConcreteProvider(configProvider, t.InstalledWith, resolvedEcosystems)
		if installWith == "" && t.InstalledWith == "" {
			installWith = configInstallWithForConcreteProvider(configProvider, t.Provider, resolvedEcosystems)
		}
		tool := provider.Tool{Name: t.Name, Provider: configProvider, Package: t.Package}
		if tool.Package == "" {
			tool.Package = t.Name
		}
		if opts.ToolProgress != nil {
			opts.ToolProgress(isync.ProgressEvent{Tool: tool, Message: "Adding " + t.Name + " to config…"})
		}
		if opts.DryRun {
			claimed = append(claimed, t.Name)
			if opts.ToolProgress != nil {
				opts.ToolProgress(isync.ProgressEvent{Tool: tool, Message: "Would add " + t.Name + " to config", Done: true})
			}
			continue
		}
		if err := a.Add(ctx, configProvider, tool.Package, t.Name, groupName, installWith); err != nil {
			if opts.ToolProgress != nil {
				opts.ToolProgress(isync.ProgressEvent{Tool: tool, Message: "Failed adding " + t.Name + " to config", Err: err, Done: true})
			}
			failures = append(failures, bulkToolErrorFromError(t.Name, configProvider, err))
			errs = append(errs, fmt.Errorf("claim %s: %w", t.Name, err))
			continue
		}
		if opts.ToolProgress != nil {
			opts.ToolProgress(isync.ProgressEvent{Tool: tool, Message: "Added " + t.Name + " to config", Done: true})
		}
		claimed = append(claimed, t.Name)
	}
	return claimed, failures, errors.Join(errs...)
}

// ─── Install / Uninstall / Upgrade ───────────────────────────────────────────

// resolveProvider returns the first available provider from priority.
// Falls back to the registry/catalog install priority when priority is empty.
func (a *App) resolveProvider(ctx context.Context, priority []string) (string, error) {
	if len(priority) == 0 {
		if a.registry != nil {
			priority = a.registry.DefaultInstallProviderNames()
		}
		if len(priority) == 0 {
			priority = provider.BuiltinDefaultInstallProviderNames()
		}
	}
	for _, name := range priority {
		p, ok := a.registry.Get(name)
		if !ok {
			continue
		}
		avail, err := p.Available(ctx)
		if err != nil || !avail {
			continue
		}
		return name, nil
	}
	return "", fmt.Errorf("no available provider found in priority list %v", priority)
}

// ResolveProvider returns the first available provider from priority,
// falling back to the built-in default order when priority is empty.
func (a *App) ResolveProvider(ctx context.Context, priority []string) (string, error) {
	return a.resolveProvider(ctx, priority)
}

func (a *App) Install(ctx context.Context, name, providerName string) error {
	if t, opProvider, ok, err := a.configuredOperationTool(ctx, name, providerName); err != nil {
		return err
	} else if ok {
		prov, ok := a.registry.Get(opProvider)
		if !ok {
			return fmt.Errorf("unknown provider %q", opProvider)
		}
		avail, err := prov.Available(ctx)
		if err != nil {
			return err
		}
		if !avail {
			return fmt.Errorf("provider %q is not available on this system", opProvider)
		}
		tool := a.operationTool(t, opProvider)
		if err := installWithProvider(ctx, prov, tool, t.InstallWith); err != nil {
			a.recordPrivilegeError(ctx, t.Name, t.Provider, t.EffectivePackage(), err)
			return err
		}
		ver, err := verifyInstalledAfterInstall(ctx, prov, tool, t.InstallWith, opProvider)
		if err != nil {
			return err
		}
		installedWith := installedWithForOperation(ctx, prov, opProvider, t.InstallWith)
		return a.readDB().Upsert(ctx, &database.ToolCache{
			Name:          t.Name,
			Provider:      t.Provider,
			Package:       t.EffectivePackage(),
			Installed:     true,
			InstalledWith: installedWith,
			Version:       sql.NullString{String: ver, Valid: ver != ""},
			LastChecked:   time.Now(),
		})
	}

	if providerName == "" {
		settings, _ := a.LoadSettings()
		resolved, err := a.resolveProvider(ctx, settings.EcosystemPriority("system"))
		if err != nil {
			return err
		}
		providerName = resolved
	}
	prov, ok := a.registry.Get(providerName)
	if !ok {
		return fmt.Errorf("unknown provider %q", providerName)
	}
	avail, err := prov.Available(ctx)
	if err != nil {
		return err
	}
	if !avail {
		return fmt.Errorf("provider %q is not available on this system", providerName)
	}
	t := provider.Tool{Name: name, Provider: providerName, Package: name}
	if err := prov.Install(ctx, t); err != nil {
		a.recordPrivilegeError(ctx, name, providerName, name, err)
		return err
	}
	ver, err := verifyInstalledAfterInstall(ctx, prov, t, "", providerName)
	if err != nil {
		return err
	}
	installedWith := installedWithForOperation(ctx, prov, providerName, "")
	return a.readDB().Upsert(ctx, &database.ToolCache{
		Name:          name,
		Provider:      providerName,
		Package:       name,
		Installed:     true,
		InstalledWith: installedWith,
		Version:       sql.NullString{String: ver, Valid: ver != ""},
		LastChecked:   time.Now(),
	})
}

func (a *App) configuredOperationTool(ctx context.Context, name, providerName string) (config.ToolEntry, string, bool, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return config.ToolEntry{}, "", false, fmt.Errorf("loading config: %w", err)
	}
	tools, _ := a.resolvedToolEntries(ctx, cfg, cfg.Groups)
	var matches []config.ToolEntry
	for _, t := range tools {
		if t.Name != name {
			continue
		}
		if providerName != "" && !a.installSpecMatchesProvider(ctx, config.ToolInstallSpec{Provider: t.Provider, InstallWith: t.InstallWith}, providerName) {
			continue
		}
		matches = append(matches, t)
	}
	if len(matches) == 0 {
		return config.ToolEntry{}, "", false, nil
	}
	if len(matches) > 1 && providerName == "" {
		return config.ToolEntry{}, "", false, fmt.Errorf("tool %q has multiple resolved providers; pass --provider", name)
	}
	t := matches[0]
	opProvider := a.operationProviderName(t)
	return t, opProvider, true, nil
}

func installedWithForOperation(ctx context.Context, prov provider.Provider, opProvider, installWith string) string {
	if installWith != "" {
		return installWith
	}
	if cr, ok := prov.(provider.ConcreteResolver); ok {
		if concrete, err := cr.ResolvedName(ctx); err == nil {
			return concrete
		}
		return ""
	}
	return opProvider
}

func verifyInstalledAfterInstall(ctx context.Context, prov provider.Provider, tool provider.Tool, installWith, opProvider string) (string, error) {
	installed, ver, err := installedWithProvider(ctx, prov, tool, installWith)
	if err != nil {
		return "", fmt.Errorf("checking installed status after install for %s/%s: %w", opProvider, tool.Name, err)
	}
	if !installed {
		return "", fmt.Errorf("install verification failed for %s/%s: not installed after install", opProvider, tool.Name)
	}
	return ver, nil
}

func (a *App) lifecycleProvider(providerName, installedWith string) (provider.Provider, string, string, bool) {
	if installedWith != "" {
		if owner, ok := a.registry.Get(installedWith); ok {
			return owner, installedWith, "", true
		}
	}
	prov, ok := a.registry.Get(providerName)
	return prov, providerName, installedWith, ok
}

func (a *App) Uninstall(ctx context.Context, name, providerName string) error {
	_, ok := a.registry.Get(providerName)
	if !ok {
		return fmt.Errorf("unknown provider %q", providerName)
	}
	var pkg string
	installedWith := ""
	if configured, _, found, err := a.configuredOperationTool(ctx, name, providerName); err != nil {
		return err
	} else if found {
		if providerName != "" && providerName != configured.Provider && configured.InstallWith == "" {
			installedWith = providerName
		}
		providerName = configured.Provider
		pkg = configured.EffectivePackage()
		if configured.InstallWith != "" {
			installedWith = configured.InstallWith
		}
	} else {
		var err error
		pkg, err = a.configuredPackageForTool(ctx, name, providerName)
		if err != nil {
			return err
		}
	}
	if cached, err := a.readDB().Get(ctx, name, providerName, pkg); err == nil {
		pkg = cached.Package
		if cached.InstalledWith != "" {
			installedWith = cached.InstalledWith
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("load cached install owner for %s/%s: %w", providerName, name, err)
	}
	prov, opProvider, manager, ok := a.lifecycleProvider(providerName, installedWith)
	if !ok {
		return fmt.Errorf("unknown provider %q", providerName)
	}
	t := provider.Tool{Name: name, Provider: opProvider, Package: pkg}
	if err := uninstallWithProvider(ctx, prov, t, manager); err != nil {
		a.recordPrivilegeError(ctx, name, providerName, pkg, err)
		return err
	}
	if err := a.removeToolFromConfig(name, providerName); err != nil {
		return err
	}
	return a.readDB().Delete(ctx, name, providerName, pkg)
}

// RemoveToolFromConfig removes a configured tool without calling a package
// manager. Use for tools that are configured but not installed locally.
func (a *App) RemoveToolFromConfig(ctx context.Context, name, providerName string) error {
	pkg, err := a.configuredPackageForTool(ctx, name, providerName)
	if err != nil {
		return err
	}
	if err := a.removeToolFromConfig(name, providerName); err != nil {
		return err
	}
	return a.readDB().Delete(ctx, name, providerName, pkg)
}

func (a *App) configuredPackageForTool(ctx context.Context, name, providerName string) (string, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return "", fmt.Errorf("loading config: %w", err)
	}
	tools, _ := a.resolvedToolEntries(ctx, cfg, cfg.Groups)
	if tool, ok := resolvedToolByName(tools, name, providerName); ok {
		return tool.EffectivePackage(), nil
	}
	if _, idx := findToolInGroups(cfg.Groups, name, providerName); idx == -1 {
		return name, nil
	}
	if spec, ok := cfg.Tools[name]; ok {
		install := a.resolveInstallSpec(ctx, name, spec)
		return install.EffectivePackage(name), nil
	}
	return name, nil
}

func (a *App) removeToolFromConfig(name, providerName string) error {
	return a.withConfig(func(cfg *config.RootConfig) error {
		changed := false
		for _, g := range cfg.Groups {
			filtered := g.Tools[:0]
			for _, tool := range g.Tools {
				if tool.Name == name && (tool.Provider == "" || providerName == "" || tool.Provider == providerName) {
					changed = true
					continue
				}
				filtered = append(filtered, tool)
			}
			g.Tools = filtered
		}
		if _, ok := cfg.Tools[name]; ok {
			delete(cfg.Tools, name)
			changed = true
		}
		if !changed {
			return errSkipSave
		}
		return nil
	})
}

// Upgrade upgrades a single tool in-place.
func (a *App) Upgrade(ctx context.Context, name, providerName string) error {
	_, ok := a.registry.Get(providerName)
	if !ok {
		return fmt.Errorf("unknown provider %q", providerName)
	}

	var pkg string
	installedWith := ""
	if configured, _, found, err := a.configuredOperationTool(ctx, name, providerName); err != nil {
		return err
	} else if found {
		if providerName != "" && providerName != configured.Provider && configured.InstallWith == "" {
			installedWith = providerName
		}
		providerName = configured.Provider
		pkg = configured.EffectivePackage()
		if configured.InstallWith != "" {
			installedWith = configured.InstallWith
		}
	} else {
		var err error
		pkg, err = a.configuredPackageForTool(ctx, name, providerName)
		if err != nil {
			return err
		}
	}
	cached, err := a.readDB().Get(ctx, name, providerName, pkg)
	if err == nil {
		pkg = cached.Package
		if cached.InstalledWith != "" {
			installedWith = cached.InstalledWith
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("load cached install owner for %s/%s: %w", providerName, name, err)
	}

	prov, opProvider, manager, ok := a.lifecycleProvider(providerName, installedWith)
	if !ok {
		return fmt.Errorf("unknown provider %q", providerName)
	}
	t := provider.Tool{Name: name, Provider: opProvider, Package: pkg}
	if err := upgradeTool(ctx, prov, t, manager); err != nil {
		a.recordPrivilegeError(ctx, name, providerName, pkg, err)
		return err
	}
	installed, ver, err := isInstalledTool(ctx, prov, t, manager)
	if err != nil {
		return fmt.Errorf("verify %s after upgrade: %w", name, err)
	}
	if !installed {
		return fmt.Errorf("verify %s after upgrade: not installed", name)
	}
	if err := a.readDB().Upsert(ctx, &database.ToolCache{
		Name:          name,
		Provider:      providerName,
		Package:       pkg,
		Installed:     true,
		InstalledWith: installedWithForLifecycle(opProvider, manager),
		Version:       sql.NullString{String: ver, Valid: ver != ""},
		LastChecked:   time.Now(),
	}); err != nil {
		return fmt.Errorf("update cache after upgrade: %w", err)
	}
	if err := a.readDB().UpdateOutdated(ctx, name, providerName, pkg, false, ""); err != nil {
		return fmt.Errorf("clear outdated after upgrade: %w", err)
	}
	return nil
}

func installedWithForLifecycle(opProvider, manager string) string {
	if manager != "" {
		return manager
	}
	return opProvider
}

func upgradeTool(ctx context.Context, prov provider.Provider, tool provider.Tool, installedWith string) error {
	if installedWith != "" && installedWith != tool.Provider {
		if up, ok := prov.(provider.ManagerUpgrader); ok {
			return up.UpgradeWithManager(ctx, tool, installedWith)
		}
	}
	return prov.Upgrade(ctx, tool)
}

func isInstalledTool(ctx context.Context, prov provider.Provider, tool provider.Tool, installedWith string) (bool, string, error) {
	if installedWith != "" && installedWith != tool.Provider {
		if checker, ok := prov.(provider.ManagerInstalledChecker); ok {
			return checker.IsInstalledWithManager(ctx, tool, installedWith)
		}
	}
	return prov.IsInstalled(ctx, tool)
}

// UpgradeAll upgrades every outdated tool in the DB.
// progress is called with a status string before each upgrade (may be nil).
// All upgrades are attempted; per-tool errors are joined and returned together.
func (a *App) UpgradeAll(ctx context.Context, progress func(string)) error {
	_, err := a.UpgradeAllDetailed(ctx, progress, nil)
	return err
}

func (a *App) UpgradeAllDetailed(ctx context.Context, progress func(string), toolProgress func(isync.ProgressEvent)) (*UpgradeAllResult, error) {
	return a.UpgradeAllDetailedWithOptions(ctx, progress, toolProgress, UpgradeAllOptions{})
}

func (a *App) UpgradeAllDetailedWithOptions(ctx context.Context, progress func(string), toolProgress func(isync.ProgressEvent), opts UpgradeAllOptions) (*UpgradeAllResult, error) {
	if err := a.refreshOutdatedIfStale(ctx, progress); err != nil {
		return nil, fmt.Errorf("refreshing outdated state: %w", err)
	}
	tools, err := a.readDB().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing tools: %w", err)
	}
	result := &UpgradeAllResult{}
	var errs []error
	for _, t := range tools {
		if !t.Installed || !t.Outdated {
			continue
		}
		if progress != nil {
			progress("upgrading " + t.Name + "…")
		}
		tool := provider.Tool{Name: t.Name, Provider: t.Provider, Package: t.Package}
		if tool.Package == "" {
			tool.Package = t.Name
		}
		if toolProgress != nil {
			toolProgress(isync.ProgressEvent{Tool: tool, Message: "Upgrading " + t.Name + "…"})
		}
		if opts.SkipPrivileged {
			if plan, planErr := a.ToolPrivilegePlan(ctx, t, provider.PrivilegeActionUpgrade); planErr == nil && plan.RequiresPrivilege() {
				err := fmt.Errorf("requires sudo: %s", privilegeReason(plan))
				if toolProgress != nil {
					toolProgress(isync.ProgressEvent{Tool: tool, Message: "Admin approval needed for " + t.Name, Err: err, Done: true})
				}
				result.Failures = append(result.Failures, BulkToolError{
					Name:     t.Name,
					Provider: t.Provider,
					Message:  err.Error(),
				})
				if markErr := a.readDB().MarkPrivilegeRequired(ctx, t.Name, t.Provider, t.Package, string(plan.Requirement), plan.Reason); markErr != nil {
					err = fmt.Errorf("%w (failed to record privilege requirement: %v)", err, markErr)
				}
				errs = append(errs, fmt.Errorf("%s: %w", t.Name, err))
				continue
			}
		}
		if err := a.Upgrade(ctx, t.Name, t.Provider); err != nil {
			if toolProgress != nil {
				toolProgress(isync.ProgressEvent{Tool: tool, Message: "Failed upgrading " + t.Name, Err: err, Done: true})
			}
			result.Failures = append(result.Failures, bulkToolErrorFromError(t.Name, t.Provider, err))
			errs = append(errs, fmt.Errorf("%s: %w", t.Name, err))
			continue
		}
		if toolProgress != nil {
			toolProgress(isync.ProgressEvent{Tool: tool, Message: "Upgraded " + t.Name, Done: true})
		}
		result.Upgraded = append(result.Upgraded, t.Name)
	}
	return result, errors.Join(errs...)
}

// ─── Config helpers ───────────────────────────────────────────────────────────

// HasConfig reports whether settings.json exists.
func (a *App) HasConfig() bool {
	_, err := os.Stat(a.ConfigPath)
	return err == nil
}

// CreateEmptyConfig writes an empty settings.json (noop if already exists).
func (a *App) CreateEmptyConfig() error {
	if a.HasConfig() {
		return nil
	}
	return config.Save(a.ConfigPath, &config.RootConfig{})
}

// LoadTaps returns the union of all taps declared across all groups.
func (a *App) LoadTaps() ([]string, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	return collectTaps(cfg.Groups), nil
}

// Groups returns all GroupConfigs from settings.json in display order.
func (a *App) Groups(_ context.Context) ([]*config.GroupConfig, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	return append([]*config.GroupConfig(nil), cfg.Groups...), nil
}

// ─── Add ──────────────────────────────────────────────────────────────────────

// Add appends a tool to the named group (empty = current host group).
// For brew tap packages like "hashicorp/tap/terraform", the tap is auto-added.
func (a *App) Add(ctx context.Context, providerName, pkg, name, groupName, installWith string) error {
	if providerName == "" {
		return fmt.Errorf("provider is required")
	}
	if !a.knownProvider(providerName) {
		return fmt.Errorf("unknown provider %q", providerName)
	}
	if !a.knownEcosystemProvider(providerName) {
		return fmt.Errorf("provider %q is not an ecosystem provider", providerName)
	}
	if err := a.validateInstallWith(providerName, installWith); err != nil {
		return err
	}

	if name == "" {
		name = pkg
	}
	if groupName == "" {
		groupName = currentMachineGroupName()
	}

	if err := a.withConfig(func(cfg *config.RootConfig) error {
		if _, err := ensureHostGroupInConfig(cfg, currentMachineGroupName()); err != nil {
			return err
		}
		for _, existing := range cfg.Groups {
			if existing.BaseName() != groupName {
				filterToolMemberships(existing, name)
			}
		}
		gc := ensureGroupInConfig(cfg, groupName)
		if cfg.Tools == nil {
			cfg.Tools = make(map[string]config.ToolSpec)
		}
		spec := cfg.Tools[name]
		spec.Provider = providerName
		spec.Package = pkg
		spec.InstallWith = installWith
		cfg.Tools[name] = spec
		if !containsToolMembership(gc.Tools, name) {
			gc.Tools = append(gc.Tools, config.ToolEntry{Name: name})
		}
		if a.providerSupportsTaps(providerName, installWith) {
			if tap := tapFromPackage(pkg); tap != "" && !slices.Contains(gc.Taps, tap) {
				spec := cfg.Tools[name]
				if !slices.Contains(spec.Taps, tap) {
					spec.Taps = append(spec.Taps, tap)
					cfg.Tools[name] = spec
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}
	// Promote any existing orphan DB row to config-tracked so the UI reflects
	// the claim immediately without waiting for the next full loadTools cycle.
	// No-op if the row doesn't exist yet.
	return a.readDB().MarkTracked(ctx, name, providerName, pkg)
}

func (a *App) providerSupportsTaps(providerName, installWith string) bool {
	if installWith != "" {
		if a.registry != nil {
			if meta, ok := a.registry.Metadata(installWith); ok && meta.SupportsTaps {
				return true
			}
		}
		return provider.BuiltinMetadata(installWith).SupportsTaps
	}
	if a.registry != nil {
		if meta, ok := a.registry.Metadata(providerName); ok && meta.SupportsTaps {
			return true
		}
	}
	if provider.BuiltinMetadata(providerName).SupportsTaps {
		return true
	}
	return false
}

// currentHostname returns the machine's hostname for host matching.
// OMNI_HOSTNAME overrides os.Hostname() — useful for tests and containers.
func currentHostname() string {
	if h := os.Getenv("OMNI_HOSTNAME"); h != "" {
		return h
	}
	h, _ := os.Hostname()
	return h
}

// shortHostname returns the first label of hostname (strips domain suffix).
// "macbook.corp.local" → "macbook", "macbook" → "macbook".
func shortHostname(hostname string) string {
	if hostname == "" {
		return "localhost"
	}
	if idx := strings.IndexByte(hostname, '.'); idx != -1 {
		return hostname[:idx]
	}
	return hostname
}

// tapFromPackage extracts "owner/repo" from a tap-qualified package path.
// "hashicorp/tap/terraform" → "hashicorp/tap", "git" → ""
func tapFromPackage(pkg string) string {
	parts := strings.Split(pkg, "/")
	if len(parts) == 3 {
		return parts[0] + "/" + parts[1]
	}
	return ""
}

func containsToolMembership(tools []config.ToolEntry, name string) bool {
	for _, t := range tools {
		if t.Name == name {
			return true
		}
	}
	return false
}

package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"os"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/executor"
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

	if !opts.DryRun {
		if err := a.addMissingProviderMatchesForGroups(ctx, cfg, groups, opts.Provider, opts.AllowWeak, opts.Progress); err != nil {
			return nil, err
		}
		cfg, err = a.loadConfig()
		if err != nil {
			return nil, err
		}
	}

	// Resolve once and derive both the entry list (for the syncer) and the
	// detailed view (for tap collection) from the same pass.
	resolvedDetailed, warnings := a.resolveTools(ctx, cfg, groups, false)

	// For tools whose every configured provider is unavailable on this host,
	// attempt a high-confidence provider search across currently available
	// providers before giving up. This handles the case where a tool spec only
	// lists a provider that is absent on the current platform (e.g. brew on
	// Linux) but another provider can supply the same tool.
	if !opts.DryRun {
		if searched, searchWarnings := a.addProviderSearchForUnavailableTools(ctx, cfg, groups, resolvedDetailed, opts.Provider, opts.Progress); searched {
			cfg, err = a.loadConfig()
			if err != nil {
				return nil, err
			}
			resolvedDetailed, warnings = a.resolveTools(ctx, cfg, groups, false)
			warnings = append(warnings, searchWarnings...)
		} else {
			warnings = append(warnings, searchWarnings...)
		}
	}

	var fallbackOps []isync.SyncOp
	resolvedDetailed, fallbackOps = a.syncNativeUnavailableFallbacks(ctx, resolvedDetailed, opts)
	resolvedTools := make([]config.ToolEntry, 0, len(resolvedDetailed))
	for _, t := range resolvedDetailed {
		resolvedTools = append(resolvedTools, t.entry)
	}
	taps := collectResolvedTaps(resolvedDetailed)
	if err = a.syncTaps(ctx, taps, opts.DryRun); err != nil {
		return nil, err
	}

	opts.IgnoreList = cfg.Ignore.Tools

	// Build a flat config view for the syncer; logical tools are deduplicated
	// by the resolver across group memberships.
	flatCfg := &config.Config{Tools: resolvedTools, Settings: cfg.Settings}

	// Two-pass provider-first sync:
	//   Pass 1 — install bootstrap providers (Settings.Providers) first,
	//             unconditionally, with no prune / group / provider filter.
	//   Pass 2 — install the rest with the caller's original options; the
	//             union of group tools + provider tools ensures bootstrap
	//             providers are not pruned as orphans.
	providerTools := a.buildProviderTools(ctx, cfg)

	var result *isync.SyncResult
	if len(providerTools) > 0 {
		pass1Opts := opts
		pass1Opts.Prune = false
		pass1Opts.Group = ""
		pass1Opts.Provider = ""
		pass1Opts.RetryFailed = false
		providerCfg := &config.Config{Tools: providerTools, Settings: cfg.Settings}
		pass1, perr := isync.New(a.registry, a.readDB()).Sync(ctx, providerCfg, pass1Opts)
		if perr != nil {
			return nil, perr
		}
		result = pass1

		// A manager installed by a script in pass 1 may land in a dir not yet on
		// PATH; refresh from the login shell so pass-2 dependents can find it.
		refreshPathAfterScriptInstalls(ctx, a.newExecutor(), pass1, func(p string) {
			if err := os.Setenv("PATH", p); err != nil {
				// best effort: pass-2 dependents fall back to the existing PATH
				return
			}
		})

		// Pass 2: union keeps bootstrap providers in the desired set so they
		// are not pruned when the user runs --prune.
		pass2Tools := unionToolEntries(resolvedTools, providerTools)
		flatCfg = &config.Config{Tools: pass2Tools, Settings: cfg.Settings}
	}

	pass2, syncErr := isync.New(a.registry, a.readDB()).Sync(ctx, flatCfg, opts)
	if syncErr != nil {
		return nil, syncErr
	}

	if result == nil {
		// No provider tools — single-pass behaviour (no providers configured).
		result = pass2
	} else {
		result = mergeProviderResults(result, pass2, providerTools)
	}
	result.Ops = append(result.Ops, fallbackOps...)
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

func (a *App) addMissingProviderMatchesForGroups(ctx context.Context, cfg *config.RootConfig, groups []*config.GroupConfig, providerFilter string, allowWeak bool, progress func(string)) error {
	if cfg == nil {
		return nil
	}
	seen := make(map[string]struct{})
	for _, group := range groups {
		if group == nil {
			continue
		}
		for _, tool := range group.Tools {
			name := strings.TrimSpace(tool.Name)
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			spec, ok := cfg.Tools[name]
			if !ok || len(spec.Providers) > 0 || spec.Fallback != nil {
				continue
			}
			// Skip re-searching registries for a tool whose discovery recently
			// found no usable match (within the TTL).
			if a.recentProviderSearchMiss(ctx, name) {
				continue
			}
			result, err := a.AddProviderMatches(ctx, name, providerFilter, ProviderMatchOptions{AllowWeak: allowWeak})
			if err != nil {
				if errors.Is(err, ErrProviderDiscoveryNoHighConfidence) {
					a.recordProviderSearchMiss(ctx, name)
					continue
				}
				if errors.Is(err, ErrProviderDiscoveryNotConfigured) ||
					errors.Is(err, ErrProviderDiscoveryAlreadyConfigured) {
					continue
				}
				return err
			}
			if len(result.Added) > 0 {
				a.clearProviderSearchMiss(ctx, name)
				if progress != nil {
					progress(providerMatchProgressText(name, result.Added))
				}
			}
		}
	}
	return nil
}

func providerMatchProgressText(name string, added []config.ToolInstallSpec) string {
	if len(added) == 0 {
		return ""
	}
	parts := make([]string, 0, len(added))
	for _, spec := range added {
		parts = append(parts, spec.Provider+"/"+spec.EffectivePackage(name))
	}
	if len(parts) == 1 {
		return fmt.Sprintf("matched provider: %s -> %s", name, parts[0])
	}
	return fmt.Sprintf("matched providers: %s -> %s", name, strings.Join(parts, ", "))
}

func (a *App) syncNativeUnavailableFallbacks(ctx context.Context, tools []resolvedTool, opts isync.SyncOptions) ([]resolvedTool, []isync.SyncOp) {
	filtered := make([]resolvedTool, 0, len(tools))
	var ops []isync.SyncOp
	for _, resolved := range tools {
		entry := resolved.entry
		if resolved.route.Kind == installRouteNative {
			filtered = append(filtered, resolved)
			continue
		}
		tool := provider.Tool{Name: entry.Name, Provider: entry.Provider, Package: entry.EffectivePackage(), Options: entry.Options}
		// Capture the original route kind before any promotion so the RetryFailed
		// guard below can distinguish a tool that genuinely came in as
		// installRouteFallbackEligible (and should respect RetryFailed) from one
		// that was just promoted from installRouteUnavailable (and must not be sent
		// back to the native syncer, which cannot install it).
		originalRouteKind := resolved.route.Kind
		if resolved.route.Kind == installRouteUnavailable {
			// When every skip is provider-unavailable and a fallback is
			// configured, treat the tool as fallback-eligible rather than
			// immediately failing. This covers the case where a tool spec lists
			// only a provider that is absent on the current platform (e.g. brew
			// on Linux) and no available native provider was found by search.
			if resolved.route.FallbackConfigured && allInstallRouteSkipsAreProviderUnavailable(resolved.route.Skipped, len(resolved.route.Skipped)) {
				// Fall through to the fallback handling below by rewriting the
				// route kind in our local copy so the subsequent code can handle it.
				resolved = resolvedTool{
					entry:       resolved.entry,
					memberships: resolved.memberships,
					taps:        resolved.taps,
					route: installRoute{
						Kind:               installRouteFallbackEligible,
						Install:            resolved.route.Install,
						Skipped:            resolved.route.Skipped,
						FallbackConfigured: true,
					},
				}
			} else if resolved.route.FallbackConfigured {
				ops = append(ops, isync.SyncOp{Tool: tool, Kind: isync.OpFailed, Err: errors.New(installRouteUnavailableMessage(entry.Name, resolved.route))})
				continue
			} else {
				filtered = append(filtered, resolved)
				continue
			}
		}
		// RetryFailed sends fallback-eligible tools back to the native syncer so
		// they get a fresh attempt. Skip this for tools promoted from
		// installRouteUnavailable: their configured providers are absent on this
		// system, so the native syncer cannot help them regardless of retry state.
		if opts.RetryFailed && originalRouteKind != installRouteUnavailable && !a.cachedFailureExists(ctx, entry) {
			filtered = append(filtered, resolved)
			continue
		}
		fallbackUsable, fallbackErr := a.automaticFallbackUsableForTool(entry.Name, opts.RetryFailed)
		if fallbackErr != nil {
			ops = append(ops, isync.SyncOp{Tool: tool, Kind: isync.OpFailed, Err: fallbackErr})
			continue
		}
		if !fallbackUsable {
			ops = append(ops, isync.SyncOp{Tool: tool, Kind: isync.OpFailed, Err: errors.New(a.fallbackUnavailableMessage(entry.Name, resolved.route))})
			continue
		}
		if opts.DryRun {
			ops = append(ops, isync.SyncOp{Tool: tool, Kind: isync.OpInstall})
			continue
		}
		if installed, version := a.fallbackInstalledCached(ctx, entry); installed {
			ops = append(ops, isync.SyncOp{Tool: tool, Kind: isync.OpAlreadyInstalled, Version: version})
			continue
		}
		if opts.Progress != nil {
			opts.Progress("installing " + entry.Name + " with fallback…")
		}
		if opts.ToolProgress != nil {
			opts.ToolProgress(isync.ProgressEvent{Tool: tool, Message: "Installing " + entry.Name + " with fallback…"})
		}
		if err := a.InstallToolFallback(ctx, entry.Name); err != nil {
			ops = append(ops, isync.SyncOp{Tool: tool, Kind: isync.OpFailed, Err: err})
			if markErr := a.readDB().MarkFailed(ctx, entry.Name, entry.Provider, entry.EffectivePackage(), err.Error()); markErr != nil {
				ops[len(ops)-1].Err = fmt.Errorf("%w (failed to record fallback failure: %v)", err, markErr)
			}
			if opts.ToolProgress != nil {
				opts.ToolProgress(isync.ProgressEvent{Tool: tool, Message: "Failed installing " + entry.Name + " with fallback", Err: err, Done: true})
			}
			continue
		}
		ops = append(ops, isync.SyncOp{Tool: tool, Kind: isync.OpInstall})
		if opts.ToolProgress != nil {
			opts.ToolProgress(isync.ProgressEvent{Tool: tool, Message: "Installed " + entry.Name + " with fallback", Done: true})
		}
	}
	return filtered, ops
}

func (a *App) cachedFailureExists(ctx context.Context, entry config.ToolEntry) bool {
	cached, err := a.readDB().Get(ctx, entry.Name, entry.Provider, entry.EffectivePackage())
	return err == nil && cached != nil && cached.FailedAt != nil
}

func (a *App) fallbackInstalledCached(ctx context.Context, entry config.ToolEntry) (bool, string) {
	cached, err := a.readDB().Get(ctx, entry.Name, entry.Provider, entry.EffectivePackage())
	if err != nil || cached == nil || !cached.Installed || !fallbackLifecycleOwner(cached.InstalledWith) {
		return false, ""
	}
	installed, err := a.CheckToolFallback(ctx, entry.Name)
	if err != nil || !installed {
		return false, ""
	}
	if cached.Version.Valid {
		return true, cached.Version.String
	}
	return true, ""
}

func installRouteUnavailableMessage(name string, route installRoute) string {
	if len(route.Skipped) == 1 && route.Skipped[0].Reason == installRouteSkipPackageUnavailable {
		skip := route.Skipped[0]
		msg := fmt.Sprintf("native package %s is unavailable from %s", skip.Install.EffectivePackage(name), skip.Install.Provider)
		if strings.TrimSpace(skip.Detail) != "" {
			msg += ": " + strings.TrimSpace(skip.Detail)
		}
		return msg
	}
	parts := make([]string, 0, len(route.Skipped))
	for _, skip := range route.Skipped {
		pkg := skip.Install.EffectivePackage(name)
		switch skip.Reason {
		case installRouteSkipPackageUnavailable:
			part := fmt.Sprintf("%s/%s unavailable", skip.Install.Provider, pkg)
			if strings.TrimSpace(skip.Detail) != "" {
				part += ": " + strings.TrimSpace(skip.Detail)
			}
			parts = append(parts, part)
		case installRouteSkipProviderUnavailable:
			parts = append(parts, fmt.Sprintf("%s/%s provider unavailable", skip.Install.Provider, pkg))
		case installRouteSkipProviderDisabled:
			parts = append(parts, fmt.Sprintf("%s/%s provider disabled on this host", skip.Install.Provider, pkg))
		default:
			parts = append(parts, fmt.Sprintf("%s/%s unavailable", skip.Install.Provider, pkg))
		}
	}
	if len(parts) == 0 {
		return fmt.Sprintf("no native install route is available for %s", name)
	}
	return fmt.Sprintf("native install candidates unavailable for %s: %s", name, strings.Join(parts, "; "))
}

func (a *App) fallbackUnavailableMessage(name string, route installRoute) string {
	msg := installRouteUnavailableMessage(name, route)
	_, fallback, err := a.configuredFallback(name)
	if err == nil && fallback != nil && fallback.Status == config.FallbackStatusUnsupported {
		return msg + "; fallback is unsupported on this platform; edit fallback before retrying"
	}
	return msg + "; edit fallback before retrying"
}

func (a *App) SyncWithState(ctx context.Context, opts isync.SyncOptions) (*SyncStateResult, error) {
	result, err := a.Sync(ctx, opts)
	state, stateErr := a.toolGroupMutationState(ctx)
	if stateErr != nil {
		return nil, stateErr
	}
	return &SyncStateResult{Result: result, Tools: state.Tools, State: state.State}, err
}

func (a *App) SyncAll(ctx context.Context, opts SyncAllOptions) (*SyncAllResult, error) {
	discovered := toolCachesFromView(opts.Discovered)
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
			views, err := a.ListDiscovered(ctx)
			if err != nil {
				return nil, fmt.Errorf("listing discovered tools: %w", err)
			}
			discovered = toolCachesFromView(views)
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
		AllowWeak:      opts.AllowWeak,
	})
	failures := append([]BulkToolError(nil), claimFailures...)
	if syncResult != nil {
		for _, op := range syncResult.Failed() {
			failures = append(failures, bulkToolErrorFromError(op.Tool.Name, op.Tool.Provider, op.Err))
		}
	}
	return &SyncAllResult{SyncResult: syncResult, ClaimedNames: claimedNames, NormalizedProviderOverrides: normalized, Failures: failures}, errors.Join(claimErr, syncErr)
}

func (a *App) SyncAllWithState(ctx context.Context, opts SyncAllOptions) (*SyncAllStateResult, error) {
	result, err := a.SyncAll(ctx, opts)
	state, stateErr := a.toolGroupMutationState(ctx)
	if stateErr != nil {
		return nil, stateErr
	}
	return &SyncAllStateResult{Result: result, Tools: state.Tools, State: state.State}, err
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
	ignored := a.ignoredToolSetBestEffort()
	resolvedEcosystems := a.ResolvedEcosystemProviders(ctx)
	var pending []discoveredClaim
	for _, t := range discovered {
		if t == nil || !t.Installed || t.Name == "" || t.Provider == "" {
			continue
		}
		if toolNameIgnored(ignored, t.Name) {
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
		claim := discoveredClaim{
			name:           t.Name,
			configProvider: configProvider,
			pkg:            tool.Package,
			installWith:    installWith,
			tool:           tool,
		}
		if err := a.validateDiscoveredClaim(claim); err != nil {
			if opts.ToolProgress != nil {
				opts.ToolProgress(isync.ProgressEvent{Tool: tool, Message: "Failed adding " + t.Name + " to config", Err: err, Done: true})
			}
			failures = append(failures, bulkToolErrorFromError(t.Name, configProvider, err))
			errs = append(errs, fmt.Errorf("claim %s: %w", t.Name, err))
			continue
		}
		if opts.DryRun {
			if opts.ToolProgress != nil {
				opts.ToolProgress(isync.ProgressEvent{Tool: tool, Message: "Adding " + t.Name + " to config…"})
			}
			claimed = append(claimed, t.Name)
			if opts.ToolProgress != nil {
				opts.ToolProgress(isync.ProgressEvent{Tool: tool, Message: "Would add " + t.Name + " to config", Done: true})
			}
			continue
		}
		pending = append(pending, claim)
	}
	if len(pending) == 0 || opts.DryRun {
		return claimed, failures, errors.Join(errs...)
	}
	if err := a.addDiscoveredClaimsToConfig(groupName, pending); err != nil {
		for _, claim := range pending {
			if opts.ToolProgress != nil {
				opts.ToolProgress(isync.ProgressEvent{Tool: claim.tool, Message: "Failed adding " + claim.name + " to config", Err: err, Done: true})
			}
			failures = append(failures, bulkToolErrorFromError(claim.name, claim.configProvider, err))
			errs = append(errs, fmt.Errorf("claim %s: %w", claim.name, err))
		}
		return claimed, failures, errors.Join(errs...)
	}
	tracked := make([]database.TrackedTool, 0, len(pending))
	for _, claim := range pending {
		tracked = append(tracked, database.TrackedTool{
			Name:     claim.name,
			Provider: claim.configProvider,
			Package:  claim.pkg,
		})
	}
	if err := a.readDB().MarkTrackedBatch(ctx, tracked); err != nil {
		for _, claim := range pending {
			if opts.ToolProgress != nil {
				opts.ToolProgress(isync.ProgressEvent{Tool: claim.tool, Message: "Failed adding " + claim.name + " to config", Err: err, Done: true})
			}
			failures = append(failures, bulkToolErrorFromError(claim.name, claim.configProvider, err))
			errs = append(errs, fmt.Errorf("claim %s: %w", claim.name, err))
		}
		return claimed, failures, errors.Join(errs...)
	}
	for _, claim := range pending {
		if opts.ToolProgress != nil {
			opts.ToolProgress(isync.ProgressEvent{Tool: claim.tool, Message: "Adding " + claim.name + " to config…"})
			opts.ToolProgress(isync.ProgressEvent{Tool: claim.tool, Message: "Added " + claim.name + " to config", Done: true})
		}
		claimed = append(claimed, claim.name)
	}
	return claimed, failures, errors.Join(errs...)
}

type discoveredClaim struct {
	name           string
	configProvider string
	pkg            string
	installWith    string
	tool           provider.Tool
}

// ClaimInstallWith returns the install_with value to record when claiming a
// single discovered/orphan tool, mirroring the bulk-claim resolution so a
// TUI/CLI single claim pins the same concrete manager (e.g. bun) as
// `tools sync --all`. Returns "" when no explicit pin is needed.
func (a *App) ClaimInstallWith(ctx context.Context, configProvider, installedWith string) string {
	resolved := a.ResolvedEcosystemProviders(ctx)
	installWith := configInstallWithForConcreteProvider(configProvider, installedWith, resolved)
	if installWith == "" && installedWith == "" {
		installWith = configInstallWithForConcreteProvider(configProvider, configProvider, resolved)
	}
	return installWith
}

func (a *App) validateDiscoveredClaim(claim discoveredClaim) error {
	if claim.configProvider == "" {
		return fmt.Errorf("provider is required")
	}
	if !a.knownProvider(claim.configProvider) {
		return fmt.Errorf("unknown provider %q", claim.configProvider)
	}
	if a.knownEcosystemProvider(claim.configProvider) {
		return fmt.Errorf("provider %q must be concrete", claim.configProvider)
	}
	return nil
}

func (a *App) addDiscoveredClaimsToConfig(groupName string, claims []discoveredClaim) error {
	if len(claims) == 0 {
		return nil
	}
	if groupName == "" {
		groupName = currentMachineGroupName()
	}
	return a.withConfig(func(cfg *config.RootConfig) error {
		if _, err := ensureHostGroupInConfig(cfg, currentMachineGroupName()); err != nil {
			return err
		}
		for _, existing := range cfg.Groups {
			if existing.BaseName() == groupName {
				continue
			}
			for _, claim := range claims {
				filterToolMemberships(existing, claim.name)
			}
		}
		gc := ensureGroupInConfig(cfg, groupName)
		if cfg.Tools == nil {
			cfg.Tools = make(map[string]config.ToolSpec)
		}
		for _, claim := range claims {
			if err := a.validateDiscoveredClaim(claim); err != nil {
				return err
			}
			spec := cfg.Tools[claim.name]
			providerName := claim.installWith
			if providerName == "" {
				providerName = claim.configProvider
			}
			if providerName == "pip3" {
				providerName = "pip"
			}
			setToolProviderCandidate(&spec, config.ToolInstallSpec{
				Provider: providerName,
				Package:  claim.pkg,
			})
			cfg.Tools[claim.name] = spec
			if !containsToolMembership(gc.Tools, claim.name) {
				gc.Tools = append(gc.Tools, config.ToolEntry{Name: claim.name})
			}
			if a.providerSupportsTaps(claim.configProvider, claim.installWith) {
				if tap := tapFromPackage(claim.pkg); tap != "" && !slices.Contains(gc.Taps, tap) {
					spec := cfg.Tools[claim.name]
					if !slices.Contains(spec.Taps, tap) {
						spec.Taps = append(spec.Taps, tap)
						cfg.Tools[claim.name] = spec
					}
				}
			}
		}
		return nil
	})
}

// ─── Install / Uninstall / Upgrade ───────────────────────────────────────────

// resolveProvider returns the first available provider from priority.
// Falls back to the registry/catalog install priority when priority is empty.
func (a *App) resolveProvider(ctx context.Context, priority []string, disabledProviders ...[]string) (string, error) {
	if len(priority) == 0 {
		if a.registry != nil {
			priority = a.registry.DefaultInstallProviderNames()
		}
		if len(priority) == 0 {
			priority = provider.BuiltinDefaultInstallProviderNames()
		}
	}
	disabled := disabledProviderSet(disabledProviders...)
	for _, name := range priority {
		if disabled[name] {
			continue
		}
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

func (a *App) DefaultInstallProvider(ctx context.Context) (string, error) {
	settings, err := a.LoadSettings()
	if err != nil {
		return "", fmt.Errorf("loading settings: %w", err)
	}
	resolved, err := a.resolveProvider(ctx, defaultProviderPriority(settings), settings.DisabledProviders)
	if err != nil {
		return "", fmt.Errorf("no provider available; use --provider to specify one")
	}
	return resolved, nil
}

func (a *App) Install(ctx context.Context, name, providerName string) error {
	if ignored, err := a.configuredToolIgnored(name); err != nil {
		return err
	} else if ignored {
		return fmt.Errorf("tool %q is ignored", name)
	}
	if resolved, opProvider, ok, err := a.configuredOperationResolvedTool(ctx, name, providerName); err != nil {
		return err
	} else if ok {
		t := resolved.entry
		ctx = traceReasonDetail(ctx, "installing", t.Name, opProvider, t.InstallWith)
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
		switch resolved.route.Kind {
		case installRouteFallbackEligible:
			fallbackUsable, err := a.automaticFallbackUsableForTool(t.Name, false)
			if err != nil {
				return err
			}
			if fallbackUsable {
				return a.InstallToolFallback(traceReason(ctx, "installing fallback", t.Name, "gh"), t.Name)
			}
			return errors.New(a.fallbackUnavailableMessage(t.Name, resolved.route))
		case installRouteUnavailable:
			return errors.New(a.fallbackUnavailableMessage(t.Name, resolved.route))
		}
		if err := installWithProvider(ctx, prov, tool, t.InstallWith); err != nil {
			a.recordPrivilegeError(ctx, t.Name, t.Provider, t.EffectivePackage(), err)
			return err
		}
		ver, err := verifyInstalledAfterInstall(ctx, prov, tool, t.InstallWith, opProvider)
		if err != nil {
			return err
		}
		installedWith := installedWithForOperation(ctx, prov, opProvider, t.InstallWith)
		if err := a.readDB().Upsert(ctx, &database.ToolCache{
			Name:          t.Name,
			Provider:      t.Provider,
			Package:       t.EffectivePackage(),
			Installed:     true,
			InstalledWith: installedWith,
			Version:       sql.NullString{String: ver, Valid: ver != ""},
			LastChecked:   time.Now(),
		}); err != nil {
			return err
		}
		return a.enrichToolGitFromInstalledProviderMetadata(ctx, t, prov, installedWith)
	}

	if hasSpec, err := a.configuredToolHasSpec(name); err != nil {
		return err
	} else if hasSpec {
		return a.unmatchedConfiguredProviderError(ctx, name, providerName)
	}

	if providerName == "" {
		settings, _ := a.LoadSettings()
		resolved, err := a.resolveProvider(ctx, defaultProviderPriority(settings), settings.DisabledProviders)
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
	ctx = traceReason(ctx, "installing", name, providerName)
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

func defaultProviderPriority(settings config.Settings) []string {
	if len(settings.ProviderPriority) > 0 {
		return append([]string(nil), settings.ProviderPriority...)
	}
	return nil
}

func disabledProviderSet(groups ...[]string) map[string]bool {
	disabled := make(map[string]bool)
	for _, names := range groups {
		for _, name := range names {
			if name == "" {
				continue
			}
			disabled[name] = true
			ecosystem, ok := provider.BuiltinEcosystemFor(name)
			if !ok || ecosystem != name {
				continue
			}
			for concrete, concreteEcosystem := range provider.BuiltinConcreteEcosystems() {
				if concreteEcosystem == ecosystem {
					disabled[concrete] = true
				}
			}
		}
	}
	return disabled
}

func (a *App) InstallWithState(ctx context.Context, name, providerName string) (*ToolGroupMutationState, error) {
	return a.toolGroupMutationStateAfter(ctx, func() error {
		return a.Install(ctx, name, providerName)
	})
}

func (a *App) InstallHighConfidenceProviderMatchesWithState(ctx context.Context, name, providerFilter string) (*ToolGroupMutationState, error) {
	return a.toolGroupMutationStateAfter(ctx, func() error {
		_, err := a.InstallHighConfidenceProviderMatches(ctx, name, providerFilter)
		return err
	})
}

func (a *App) configuredOperationTool(ctx context.Context, name, providerName string) (config.ToolEntry, string, bool, error) {
	resolved, opProvider, ok, err := a.configuredOperationResolvedTool(ctx, name, providerName)
	if err != nil || !ok {
		return config.ToolEntry{}, opProvider, ok, err
	}
	return resolved.entry, opProvider, true, nil
}

func (a *App) configuredOperationResolvedTool(ctx context.Context, name, providerName string) (resolvedTool, string, bool, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return resolvedTool{}, "", false, fmt.Errorf("loading config: %w", err)
	}
	tools, _ := a.currentResolvedTools(ctx, cfg)
	var matches []resolvedTool
	for _, t := range tools {
		if t.entry.Name != name {
			continue
		}
		if providerName != "" && !a.installSpecMatchesProvider(ctx, config.ToolInstallSpec{Provider: t.entry.Provider, InstallWith: t.entry.InstallWith}, providerName) {
			continue
		}
		matches = append(matches, t)
	}
	if len(matches) == 0 {
		// currentResolvedTools only carries the single auto-picked candidate per
		// tool (planInstallRoute stops at the first available one), so an
		// explicit --provider request for a different configured candidate
		// (e.g. a secondary "bun" entry when brew is the auto-picked default)
		// would otherwise find nothing here. Search every configured candidate
		// before concluding the tool has no match for providerName.
		if providerName != "" {
			if spec, ok := cfg.Tools[name]; ok {
				if route, found := a.findConfiguredInstallRoute(ctx, name, spec, providerName); found {
					entry := spec.ToToolEntry(name, route.Install)
					resolved := resolvedTool{entry: entry, taps: append([]string(nil), spec.Taps...), route: route}
					opProvider := a.operationProviderName(entry)
					return resolved, opProvider, true, nil
				}
			}
		}
		return resolvedTool{}, "", false, nil
	}
	if len(matches) > 1 && providerName == "" {
		return resolvedTool{}, "", false, fmt.Errorf("tool %q has multiple resolved providers; pass --provider", name)
	}
	resolved := matches[0]
	opProvider := a.operationProviderName(resolved.entry)
	return resolved, opProvider, true, nil
}

// configuredToolHasSpec reports whether name has any logical spec in config,
// regardless of whether it's ignored, host-restricted, or group-membership
// filtered out of currentResolvedTools. Used to gate the literal-name install
// fallback: that fallback must only fire for tools with zero configured
// entries, never for a configured tool whose requested provider just failed
// to match.
func (a *App) configuredToolHasSpec(name string) (bool, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return false, fmt.Errorf("loading config: %w", err)
	}
	_, ok := cfg.Tools[name]
	return ok, nil
}

func (a *App) unmatchedConfiguredProviderError(ctx context.Context, name, providerName string) error {
	cfg, err := a.loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	spec, ok := cfg.Tools[name]
	if !ok {
		return fmt.Errorf("tool %q is not configured", name)
	}
	candidates := spec.Providers
	if len(candidates) == 0 {
		candidates = []config.ToolInstallSpec{spec.DefaultInstallSpec()}
	}
	available := make([]string, 0, len(candidates))
	for _, c := range candidates {
		available = append(available, fmt.Sprintf("%s (package %q)", c.Provider, c.EffectivePackage(name)))
	}
	if providerName == "" {
		return fmt.Errorf("tool %q has no install candidate for the current host; available providers: %s", name, strings.Join(available, ", "))
	}
	return fmt.Errorf("tool %q is not configured with a candidate for provider %q; available providers: %s", name, providerName, strings.Join(available, ", "))
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
	if err := a.rejectProviderToolDelete(name); err != nil {
		return err
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
	if fallbackLifecycleOwner(installedWith) {
		if err := a.UninstallToolFallback(traceReason(ctx, "removing fallback", name, "gh"), name); err != nil {
			return err
		}
		return a.removeToolFromConfig(name, providerName)
	}
	prov, opProvider, manager, ok := a.lifecycleProvider(providerName, installedWith)
	if !ok {
		return fmt.Errorf("unknown provider %q", providerName)
	}
	ctx = traceReasonDetail(ctx, "removing", name, opProvider, manager)
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

func (a *App) UninstallWithState(ctx context.Context, name, providerName string) (*ToolGroupMutationState, error) {
	return a.toolGroupMutationStateAfter(ctx, func() error {
		return a.Uninstall(ctx, name, providerName)
	})
}

// RemoveToolFromConfig removes a configured tool without calling a package
// manager. Use for tools that are configured but not installed locally.
func (a *App) RemoveToolFromConfig(ctx context.Context, name, providerName string) error {
	if err := a.rejectProviderToolDelete(name); err != nil {
		return err
	}
	cacheProvider, pkg, err := a.configuredCacheIdentityForTool(ctx, name, providerName)
	if err != nil {
		return err
	}
	if err := a.removeToolFromConfig(name, providerName); err != nil {
		return err
	}
	if err := a.readDB().Delete(ctx, name, cacheProvider, pkg); err != nil {
		return err
	}
	if providerName != "" && providerName != cacheProvider {
		return a.readDB().Delete(ctx, name, providerName, pkg)
	}
	return nil
}

func (a *App) RemoveToolFromConfigWithState(ctx context.Context, name, providerName string) (*ToolGroupMutationState, error) {
	return a.toolGroupMutationStateAfter(ctx, func() error {
		return a.RemoveToolFromConfig(ctx, name, providerName)
	})
}

func (a *App) configuredCacheIdentityForTool(ctx context.Context, name, providerName string) (string, string, error) {
	if providerName != "" {
		if configured, opProvider, found, err := a.configuredOperationTool(ctx, name, providerName); err != nil {
			return "", "", err
		} else if found {
			return opProvider, configured.EffectivePackage(), nil
		}
		if configured, opProvider, found, err := a.configuredOperationTool(ctx, name, ""); err != nil {
			return "", "", err
		} else if found {
			return opProvider, configured.EffectivePackage(), nil
		}
	}
	pkg, err := a.configuredPackageForTool(ctx, name, providerName)
	if err != nil {
		return "", "", err
	}
	return providerName, pkg, nil
}

func (a *App) configuredPackageForTool(ctx context.Context, name, providerName string) (string, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return "", fmt.Errorf("loading config: %w", err)
	}
	tools, _ := a.currentResolvedToolEntries(ctx, cfg)
	if tool, ok := resolvedToolByName(tools, name, providerName); ok {
		return tool.EffectivePackage(), nil
	}
	if _, idx := findToolInGroups(cfg.Groups, name, providerName); idx == -1 {
		return name, nil
	}
	if spec, ok := cfg.Tools[name]; ok {
		install := a.resolveInstallSpecWithSettings(ctx, name, spec, nil, a.effectiveSettings(cfg))
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
	return a.UpgradeWithOptions(ctx, name, providerName, UpgradeOptions{})
}

// UpgradeInstalled upgrades a single installed tool using the local cache owner.
func (a *App) UpgradeInstalled(ctx context.Context, name string) error {
	return a.UpgradeInstalledWithOptions(ctx, name, UpgradeOptions{})
}

func (a *App) UpgradeInstalledWithOptions(ctx context.Context, name string, opts UpgradeOptions) error {
	target, err := a.installedUpgradeTarget(ctx, name)
	if err != nil {
		return err
	}
	return a.UpgradeWithOptions(ctx, target.Name, target.Provider, opts)
}

func (a *App) installedUpgradeTarget(ctx context.Context, name string) (*database.ToolCache, error) {
	tools, err := a.readDB().List(ctx)
	if err != nil {
		return nil, err
	}
	var matches []*database.ToolCache
	for _, t := range tools {
		if t == nil || t.Name != name || !t.Installed {
			continue
		}
		matches = append(matches, t)
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("tool %q is not installed; run 'omni tools refresh' if the cache is stale", name)
	case 1:
		return matches[0], nil
	default:
		owners := make([]string, 0, len(matches))
		for _, t := range matches {
			owner := t.Provider
			if t.InstalledWith != "" {
				owner += "/" + t.InstalledWith
			}
			owners = append(owners, owner)
		}
		return nil, fmt.Errorf("tool %q has multiple installed providers: %s", name, strings.Join(owners, ", "))
	}
}

func (a *App) UpgradeWithOptions(ctx context.Context, name, providerName string, opts UpgradeOptions) error {
	_, ok := a.registry.Get(providerName)
	if !ok {
		return fmt.Errorf("unknown provider %q", providerName)
	}
	if ignored, err := a.configuredToolIgnored(name); err != nil {
		return err
	} else if ignored {
		return fmt.Errorf("tool %q is ignored", name)
	}

	var pkg string
	installedWith := ""
	var configuredOptions map[string]string
	var configuredResolved *resolvedTool
	if resolved, _, found, err := a.configuredOperationResolvedTool(ctx, name, providerName); err != nil {
		return err
	} else if found {
		configured := resolved.entry
		configuredResolved = &resolved
		configuredOptions = configured.Options
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
	if fallbackLifecycleOwner(installedWith) {
		return a.UpgradeToolFallback(traceReason(ctx, "upgrading fallback", name, "gh"), name)
	}
	if configuredResolved != nil && cached != nil && cached.Outdated && cached.LatestVersion.Valid {
		configuredOptions, err = a.hydrateConfiguredGitHubUpgradeOptions(ctx, name, *configuredResolved, cached.LatestVersion.String)
		if err != nil {
			return err
		}
	}
	if !opts.Force && cached != nil {
		cfg, cfgErr := a.loadConfig()
		if cfgErr != nil {
			return cfgErr
		}
		decision, decisionErr := a.updateQuarantineDecision(ctx, cfg, cached, time.Now())
		if decisionErr != nil {
			return decisionErr
		}
		if decision.Blocked {
			return quarantineBlockedError(name, decision)
		}
	}

	prov, opProvider, manager, ok := a.lifecycleProvider(providerName, installedWith)
	if !ok {
		return fmt.Errorf("unknown provider %q", providerName)
	}
	ctx = traceReasonDetail(ctx, "upgrading", name, opProvider, manager)
	t := provider.Tool{Name: name, Provider: opProvider, Package: pkg, Options: configuredOptions}
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
	installedOwner := installedWithForLifecycle(opProvider, manager)
	if err := a.readDB().Upsert(ctx, &database.ToolCache{
		Name:          name,
		Provider:      providerName,
		Package:       pkg,
		Installed:     true,
		InstalledWith: installedOwner,
		Version:       sql.NullString{String: ver, Valid: ver != ""},
		LastChecked:   time.Now(),
	}); err != nil {
		return fmt.Errorf("update cache after upgrade: %w", err)
	}
	if err := a.refreshOutdatedAfterUpgrade(ctx, name, providerName, pkg, installedOwner); err != nil {
		return fmt.Errorf("refresh outdated after upgrade: %w", err)
	}
	return nil
}

func (a *App) hydrateConfiguredGitHubUpgradeOptions(ctx context.Context, name string, resolved resolvedTool, latestTag string) (map[string]string, error) {
	configured := cloneToolInstallSpec(resolved.route.ConfiguredInstall)
	if configured.Source == nil || configured.Source.Type != config.FallbackSourceGitHub || configured.Recipe == nil || configured.Recipe.Type != config.FallbackRecipeGitHubReleaseAsset {
		return resolved.entry.Options, nil
	}
	if strings.TrimSpace(configured.Options["install"]) != "" || strings.TrimSpace(configured.Recipe.TagName) != "" || strings.TrimSpace(configured.Options["release_tag"]) != "" {
		return resolved.entry.Options, nil
	}
	recipe := *configured.Recipe
	recipe.TagName = strings.TrimSpace(latestTag)
	if recipe.TagName == "" {
		return resolved.entry.Options, nil
	}
	release, err := a.fetchLatestGitHubReleaseCached(ctx, nil, configured.Source.Owner, configured.Source.Repo)
	if err != nil {
		return nil, fmt.Errorf("resolving GitHub upgrade for %s at %s: %w", name, recipe.TagName, err)
	}
	if strings.TrimSpace(release.TagName) != recipe.TagName {
		return nil, fmt.Errorf("GitHub latest release for %s changed from %s to %s; refresh updates before upgrading", name, recipe.TagName, strings.TrimSpace(release.TagName))
	}
	asset, ok := configuredGitHubReleaseAsset(name, configured, release)
	if !ok {
		return nil, fmt.Errorf("GitHub release %s for %s does not contain configured asset %q", recipe.TagName, name, recipe.AssetPattern)
	}
	recipe.AssetID = strings.TrimSpace(asset.ID.String())
	recipe.AssetName = asset.Name
	recipe.AssetDownloadURL = asset.BrowserDownloadURL
	configured.Recipe = &recipe
	settings, _ := a.LoadSettings()
	materialized, err := config.MaterializeInstallSpec(name, configured, strings.TrimSpace(settings.FallbackBinDir))
	if err != nil {
		return nil, fmt.Errorf("materializing GitHub upgrade for %s at %s: %w", name, recipe.TagName, err)
	}
	return materialized.Options, nil
}

func (a *App) UpgradeWithState(ctx context.Context, name, providerName string) (*ToolGroupMutationState, error) {
	return a.toolGroupMutationStateAfter(ctx, func() error {
		return a.Upgrade(ctx, name, providerName)
	})
}

func (a *App) refreshOutdatedAfterUpgrade(ctx context.Context, name, providerName, pkg, installedWith string) error {
	lookupProvider := a.outdatedLookupProvider(&database.ToolCache{
		Name:          name,
		Provider:      providerName,
		Package:       pkg,
		InstalledWith: installedWith,
	})
	if lookupProvider == "" {
		return nil
	}
	return a.RefreshProviderOutdated(ctx, lookupProvider, false)
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
	if err := a.refreshOutdatedIfStale(ctx, nil); err != nil {
		return nil, fmt.Errorf("refreshing outdated state: %w", err)
	}
	tools, err := a.readDB().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing tools: %w", err)
	}
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	tools = filterIgnoredToolCaches(tools, a.ignoredToolSetBestEffort())
	if !opts.Force {
		a.annotateUpdateQuarantine(ctx, cfg, tools)
		a.annotateSelfUpdatingCasks(tools)
	}
	result := &UpgradeAllResult{}
	var errs []error
	for _, t := range tools {
		if !t.Installed || !t.Outdated {
			continue
		}
		targetVersion := toolTargetVersion(t)
		displayName := ToolNameWithVersion(t.Name, targetVersion)
		tool := provider.Tool{Name: t.Name, Provider: t.Provider, Package: t.Package}
		if tool.Package == "" {
			tool.Package = t.Name
		}
		if !opts.Force && t.UpdateBlocked != "" {
			if progress != nil {
				progress("skipping " + displayName + " (update quarantined)…")
			}
			result.Quarantined = append(result.Quarantined, QuarantinedUpdate{
				Name:         t.Name,
				Provider:     t.Provider,
				Package:      t.Package,
				Version:      targetVersion,
				Reason:       t.UpdateBlocked,
				BlockedUntil: t.UpdateBlockedUntil,
			})
			if toolProgress != nil {
				toolProgress(isync.ProgressEvent{Tool: tool, Message: "Skipped upgrading " + t.Name + ": update quarantined", TargetVersion: targetVersion, Done: true})
			}
			continue
		}
		if progress != nil {
			progress("upgrading " + displayName + "…")
		}
		if toolProgress != nil {
			toolProgress(isync.ProgressEvent{Tool: tool, Message: "Upgrading " + t.Name + "…", TargetVersion: targetVersion})
		}
		if opts.SkipPrivileged {
			if plan, planErr := a.ToolPrivilegePlan(ctx, toolViewFromCache(t), provider.PrivilegeActionUpgrade); planErr == nil && plan.RequiresPrivilege() {
				err := fmt.Errorf("requires sudo: %s", privilegeReason(plan))
				if toolProgress != nil {
					toolProgress(isync.ProgressEvent{Tool: tool, Message: "Admin approval needed for " + t.Name, TargetVersion: targetVersion, Err: err, Done: true})
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
		// upgradeToolTimeout caps a single tool upgrade. One hung package-manager
		// must not stall every remaining tool in the loop.
		const upgradeToolTimeout = 10 * time.Minute
		upgradeCtx, upgradeCancel := context.WithTimeout(ctx, upgradeToolTimeout)
		upgradeErr := a.UpgradeWithOptions(upgradeCtx, t.Name, t.Provider, UpgradeOptions{Force: opts.Force})
		upgradeCancel()
		if err := upgradeErr; err != nil {
			if isUnupgradeableManagerSelf(t, err) {
				if progress != nil {
					progress("skipping " + displayName + " (externally managed; cannot self-upgrade)…")
				}
				if toolProgress != nil {
					toolProgress(isync.ProgressEvent{Tool: tool, Message: "Skipped " + t.Name + " (externally managed; cannot self-upgrade)", TargetVersion: targetVersion, Done: true})
				}
				result.Skipped = append(result.Skipped, bulkToolErrorFromError(t.Name, t.Provider, err))
				continue
			}
			if isBrewManualInstallerCaskUpgrade(t, err) {
				if progress != nil {
					progress("skipping " + displayName + " (manual cask upgrade)…")
				}
				if toolProgress != nil {
					toolProgress(isync.ProgressEvent{Tool: tool, Message: "Skipped " + t.Name + " (manual cask upgrade)", TargetVersion: targetVersion, Done: true})
				}
				result.Skipped = append(result.Skipped, bulkToolErrorFromError(t.Name, t.Provider, err))
				continue
			}
			if toolProgress != nil {
				toolProgress(isync.ProgressEvent{Tool: tool, Message: "Failed upgrading " + t.Name, TargetVersion: targetVersion, Err: err, Done: true})
			}
			result.Failures = append(result.Failures, bulkToolErrorFromError(t.Name, t.Provider, err))
			errs = append(errs, fmt.Errorf("%s: %w", t.Name, err))
			continue
		}
		if toolProgress != nil {
			toolProgress(isync.ProgressEvent{Tool: tool, Message: "Upgraded " + t.Name, TargetVersion: targetVersion, Done: true})
		}
		result.Upgraded = append(result.Upgraded, t.Name)
	}
	return result, errors.Join(errs...)
}

// isUnupgradeableManagerSelf reports whether an upgrade failure is a package
// manager that cannot upgrade itself in an externally managed Python (PEP 668).
// Switching pip to uv does not apply to the pip package itself, so this is a
// graceful skip rather than a failure.
func isUnupgradeableManagerSelf(t *database.ToolCache, err error) bool {
	if t == nil || !provider.HasErrorCode(err, provider.ErrorExternallyManagedPython) {
		return false
	}
	pkg := t.Package
	if pkg == "" {
		pkg = t.Name
	}
	return pkg == "pip" && config.NormalizeConcreteProvider(t.Provider) == "pip"
}

func isBrewManualInstallerCaskUpgrade(t *database.ToolCache, err error) bool {
	if t == nil || err == nil {
		return false
	}
	if config.NormalizeConcreteProvider(t.Provider) != "brew" && config.NormalizeConcreteProvider(t.InstalledWith) != "brew" {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not upgrading") &&
		strings.Contains(msg, "installer manual") &&
		strings.Contains(msg, "cask")
}

func toolTargetVersion(tool *database.ToolCache) string {
	if tool == nil || !tool.LatestVersion.Valid {
		return ""
	}
	return strings.TrimSpace(tool.LatestVersion.String)
}

func (a *App) UpgradeAllDetailedWithState(ctx context.Context, progress func(string), toolProgress func(isync.ProgressEvent), opts UpgradeAllOptions) (*UpgradeAllStateResult, error) {
	result, err := a.UpgradeAllDetailedWithOptions(ctx, progress, toolProgress, opts)
	state, stateErr := a.toolGroupMutationState(ctx)
	if stateErr != nil {
		return nil, stateErr
	}
	return &UpgradeAllStateResult{Result: result, Tools: state.Tools, State: state.State}, err
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

type GroupSummary struct {
	Name        string
	Description string
	ToolCount   int
}

func (a *App) GroupSummaries(ctx context.Context) ([]GroupSummary, error) {
	groups, err := a.Groups(ctx)
	if err != nil {
		return nil, err
	}
	summaries := make([]GroupSummary, 0, len(groups))
	for _, group := range groups {
		summaries = append(summaries, GroupSummary{
			Name:        group.GroupName(),
			Description: group.Description,
			ToolCount:   len(group.Tools),
		})
	}
	return summaries, nil
}

// ─── Add ──────────────────────────────────────────────────────────────────────

type AddToolOptions struct {
	ProviderName string
	Package      string
	Name         string
	GroupName    string
	InstallWith  string
	Options      map[string]string
	AssignHosts  []string
}

// Add appends a tool to the named group (empty = current host group).
// For brew tap packages like "hashicorp/tap/terraform", the tap is auto-added.
func (a *App) Add(ctx context.Context, providerName, pkg, name, groupName, installWith string, optionMaps ...map[string]string) error {
	if providerName == "" {
		return fmt.Errorf("provider is required")
	}
	if name == "" {
		name = pkg
	}
	if groupName == "" {
		groupName = currentMachineGroupName()
	}
	var options map[string]string
	if first, ok := firstOptionMap(optionMaps); ok {
		options = first
	}
	entry, err := a.providerEntryFromLegacyArgs(providerName, pkg, installWith, options)
	if err != nil {
		return err
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
		setToolProviderCandidate(&spec, entry)
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
	if err := a.enrichToolGitFromCachedMetadata(ctx, name, entry.Provider, entry.Package); err != nil {
		return err
	}
	// Promote any existing orphan DB row to config-tracked so the UI reflects
	// the claim immediately without waiting for the next full loadTools cycle.
	// No-op if the row doesn't exist yet.
	return a.readDB().MarkTracked(ctx, name, entry.Provider, entry.Package)
}

func (a *App) AddWithState(ctx context.Context, opts AddToolOptions) (*ToolGroupMutationState, error) {
	name, pkg := addToolNameAndPackage(opts)
	if err := a.Add(ctx, opts.ProviderName, pkg, name, opts.GroupName, opts.InstallWith, optionMapArg(opts.Options)...); err != nil {
		return nil, err
	}
	return a.toolGroupStateAfterHostAssignment(ctx, opts.GroupName, opts.AssignHosts)
}

func (a *App) InstallAndAddWithState(ctx context.Context, opts AddToolOptions) (*ToolGroupMutationState, error) {
	name, pkg := addToolNameAndPackage(opts)
	if err := a.installForAdd(ctx, opts); err != nil {
		return nil, err
	}
	if err := a.Add(ctx, opts.ProviderName, pkg, name, opts.GroupName, opts.InstallWith, optionMapArg(opts.Options)...); err != nil {
		return nil, fmt.Errorf("installed %s but config save failed: %w", name, err)
	}
	hostErr := a.assignToolGroupHosts(opts.GroupName, opts.AssignHosts)
	state, stateErr := a.toolGroupMutationState(ctx)
	if stateErr != nil {
		return nil, errors.Join(hostErr, stateErr)
	}
	if hostErr != nil {
		return state, fmt.Errorf("installed %s and added to config but host update failed: %w", name, hostErr)
	}
	return state, nil
}

func (a *App) installForAdd(ctx context.Context, opts AddToolOptions) error {
	name, pkg := addToolNameAndPackage(opts)
	if opts.InstallWith == "" && opts.Options == nil && pkg == name {
		return a.Install(ctx, name, opts.ProviderName)
	}
	providerName := opts.ProviderName
	if ignored, err := a.configuredToolIgnored(name); err != nil {
		return err
	} else if ignored {
		return fmt.Errorf("tool %q is ignored", name)
	}
	if providerName == "" {
		settings, _ := a.LoadSettings()
		resolved, err := a.resolveProvider(ctx, SystemInstallPriority(settings))
		if err != nil {
			return err
		}
		providerName = resolved
	}
	if !a.knownProvider(providerName) {
		return fmt.Errorf("unknown provider %q", providerName)
	}
	if err := a.validateInstallWith(providerName, opts.InstallWith); err != nil {
		return err
	}
	prov, opProvider, manager, ok := a.lifecycleProvider(providerName, opts.InstallWith)
	if !ok {
		return fmt.Errorf("unknown provider %q", providerName)
	}
	avail, err := prov.Available(ctx)
	if err != nil {
		return err
	}
	if !avail {
		return fmt.Errorf("provider %q is not available on this system", opProvider)
	}
	tool := provider.Tool{
		Name:     name,
		Provider: opProvider,
		Package:  pkg,
		Options:  cloneOptionMap(opts.Options),
	}
	if err := installWithProvider(ctx, prov, tool, manager); err != nil {
		a.recordPrivilegeError(ctx, name, providerName, pkg, err)
		return err
	}
	ver, err := verifyInstalledAfterInstall(ctx, prov, tool, manager, opProvider)
	if err != nil {
		return err
	}
	installedWith := installedWithForOperation(ctx, prov, opProvider, manager)
	return a.readDB().Upsert(ctx, &database.ToolCache{
		Name:          name,
		Provider:      providerName,
		Package:       pkg,
		Installed:     true,
		InstalledWith: installedWith,
		Version:       sql.NullString{String: ver, Valid: ver != ""},
		LastChecked:   time.Now(),
	})
}

func addToolNameAndPackage(opts AddToolOptions) (string, string) {
	name := opts.Name
	if name == "" {
		name = opts.Package
	}
	pkg := opts.Package
	if pkg == "" {
		pkg = name
	}
	return name, pkg
}

func optionMapArg(options map[string]string) []map[string]string {
	if options == nil {
		return nil
	}
	return []map[string]string{options}
}

func firstOptionMap(optionMaps []map[string]string) (map[string]string, bool) {
	if len(optionMaps) == 0 {
		return nil, false
	}
	return optionMaps[0], true
}

func cloneOptionMap(options map[string]string) map[string]string {
	if len(options) == 0 {
		return nil
	}
	return maps.Clone(options)
}

func (a *App) toolGroupStateAfterHostAssignment(ctx context.Context, groupName string, hosts []string) (*ToolGroupMutationState, error) {
	err := a.assignToolGroupHosts(groupName, hosts)
	state, stateErr := a.toolGroupMutationState(ctx)
	if stateErr != nil {
		return nil, errors.Join(err, stateErr)
	}
	return state, err
}

func (a *App) assignToolGroupHosts(groupName string, hosts []string) error {
	groupName = strings.TrimSpace(groupName)
	if groupName == "" {
		return nil
	}
	targets := make([]string, 0, len(hosts)+1)
	targets = append(targets, currentMachineGroupName())
	targets = append(targets, hosts...)
	seen := make(map[string]struct{}, len(targets))
	for _, host := range targets {
		host = strings.TrimSpace(machineGroupName(host))
		if host == "" {
			continue
		}
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		if err := a.AddGroupToHost(host, groupName); err != nil {
			return err
		}
	}
	return nil
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
// Hostnames are always lower-cased so they compare and key consistently
// regardless of how the OS or env reports their case.
func currentHostname() string {
	if h := strings.TrimSpace(os.Getenv("OMNI_HOSTNAME")); h != "" {
		return strings.ToLower(h)
	}
	h, _ := os.Hostname()
	return strings.ToLower(strings.TrimSpace(h))
}

// shortHostname returns the first label of hostname (strips domain suffix),
// lower-cased. "MacBook.corp.local" → "macbook", "macbook" → "macbook".
func shortHostname(hostname string) string {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
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

// ─── Provider-first helpers ───────────────────────────────────────────────────

// buildProviderTools resolves Settings.Providers (via effectiveSettings, which
// applies host overrides) into syncer tool entries in list order, reusing the
// install-spec resolver so host overrides, variants, and availability caching
// all apply.
func (a *App) buildProviderTools(ctx context.Context, cfg *config.RootConfig) []config.ToolEntry {
	settings := a.effectiveSettings(cfg)
	providers := settings.Providers
	if len(providers) == 0 {
		return nil
	}
	availability := make(map[string]bool)
	tools := make([]config.ToolEntry, 0, len(providers))
	for _, p := range providers {
		spec := p.ToToolSpec()
		install := a.resolveInstallSpecWithSettings(ctx, p.Name, spec, availability, settings)
		tools = append(tools, spec.ToToolEntry(p.Name, install))
	}
	return tools
}

// unionToolEntries returns base followed by any extra entries whose Name is
// not already present in base. Name alone is used for deduplication because
// provider-tool names are unique (each provider has exactly one name) and
// the provider/package fields may differ between the resolved group entry and
// the provider-tools entry (the provider-tool entry wins via base).
func unionToolEntries(base, extra []config.ToolEntry) []config.ToolEntry {
	if len(extra) == 0 {
		out := make([]config.ToolEntry, len(base))
		copy(out, base)
		return out
	}
	seen := make(map[string]struct{}, len(base)+len(extra))
	out := make([]config.ToolEntry, 0, len(base)+len(extra))
	for _, t := range base {
		if _, ok := seen[t.Name]; !ok {
			seen[t.Name] = struct{}{}
			out = append(out, t)
		}
	}
	for _, t := range extra {
		if _, ok := seen[t.Name]; !ok {
			seen[t.Name] = struct{}{}
			out = append(out, t)
		}
	}
	return out
}

// mergeProviderResults concatenates pass-1 (provider) ops with pass-2 (group)
// ops, dropping any pass-2 op whose tool name matches a bootstrap provider
// (pass-1 already owns that identity). Warnings from both passes are
// concatenated; SatisfiedGroups comes from pass 2 (group membership context).
func mergeProviderResults(pass1, pass2 *isync.SyncResult, providerTools []config.ToolEntry) *isync.SyncResult {
	// Index provider names; dedup on Name only (see unionToolEntries rationale).
	providerNames := make(map[string]struct{}, len(providerTools))
	for _, t := range providerTools {
		providerNames[t.Name] = struct{}{}
	}
	merged := &isync.SyncResult{}
	merged.Ops = append(merged.Ops, pass1.Ops...)
	for _, op := range pass2.Ops {
		if _, ok := providerNames[op.Tool.Name]; ok {
			// Pass 1 already recorded this provider's outcome; skip the duplicate.
			continue
		}
		merged.Ops = append(merged.Ops, op)
	}
	merged.Warnings = append(merged.Warnings, pass1.Warnings...)
	merged.Warnings = append(merged.Warnings, pass2.Warnings...)
	// SatisfiedGroups is populated by the caller after merge; pass2's value (nil here) is a placeholder.
	merged.SatisfiedGroups = pass2.SatisfiedGroups
	return merged
}

// refreshPathAfterScriptInstalls re-reads the login shell's PATH and applies it
// via setenv when pass 1 installed a script-provider tool, so pass-2 dependents
// see a manager the script placed in a dir not yet on the process PATH.
func refreshPathAfterScriptInstalls(ctx context.Context, exec executor.Executor, pass1 *isync.SyncResult, setenv func(string)) {
	if runtime.GOOS == "windows" || !pass1InstalledScriptTool(pass1) {
		return
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	// printf is POSIX; `echo -n` prints a literal "-n" under dash (/bin/sh).
	stdout, _, err := exec.Run(ctx, shell, "-lic", `printf '%s' "$PATH"`)
	if err != nil {
		return
	}
	if newPath := lastNonEmptyLine(stdout); newPath != "" {
		setenv(newPath)
	}
}

func pass1InstalledScriptTool(pass1 *isync.SyncResult) bool {
	if pass1 == nil {
		return false
	}
	for _, op := range pass1.Ops {
		if op.Kind == isync.OpInstall && op.Tool.Provider == "script" {
			return true
		}
	}
	return false
}

func lastNonEmptyLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
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

// addProviderSearchForUnavailableTools searches available providers for
// high-confidence matches for tools whose every configured provider is
// unavailable on the current host. It is called during Sync after the initial
// addMissingProviderMatchesForGroups pass (which only searches tools with no
// providers configured at all). This handles the codex-on-Linux shape where
// the tool spec has exactly one provider (brew) that is absent, but another
// provider (node/npm) can supply the same package.
//
// Returns (searched bool, warnings []string). searched is true when at least
// one tool had a high-confidence alternative found and persisted, signalling
// that the caller should reload config and re-resolve before continuing.
func (a *App) addProviderSearchForUnavailableTools(
	ctx context.Context,
	cfg *config.RootConfig,
	groups []*config.GroupConfig,
	resolved []resolvedTool,
	providerFilter string,
	progress func(string),
) (bool, []string) {
	if cfg == nil {
		return false, nil
	}

	// Build a name set of tools that are resolved as provider-unavailable with
	// all configured providers unavailable (not package-unavailable).
	unavailableNames := make(map[string]struct{})
	for _, rt := range resolved {
		if rt.route.Kind != installRouteUnavailable {
			continue
		}
		skips := rt.route.Skipped
		if len(skips) == 0 {
			continue
		}
		if !allInstallRouteSkipsAreProviderUnavailable(skips, len(skips)) {
			continue
		}
		// Only act when the tool actually has configured providers (this function
		// is the complement to addMissingProviderMatchesForGroups, not a
		// replacement for it).
		spec, ok := cfg.Tools[rt.entry.Name]
		if !ok || len(spec.Providers) == 0 {
			continue
		}
		unavailableNames[rt.entry.Name] = struct{}{}
	}
	if len(unavailableNames) == 0 {
		return false, nil
	}

	// Deduplicate across group memberships so each tool is searched once.
	var warnings []string
	searched := false
	seen := make(map[string]struct{})
	for _, group := range groups {
		if group == nil {
			continue
		}
		for _, tool := range group.Tools {
			name := strings.TrimSpace(tool.Name)
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			if _, ok := unavailableNames[name]; !ok {
				continue
			}
			// Skip if a recent search already found nothing (TTL-guarded).
			if a.recentProviderSearchMiss(ctx, name) {
				continue
			}
			spec, ok := cfg.Tools[name]
			if !ok {
				continue
			}
			// Report which providers were skipped so the user understands why
			// we are attempting a broader search (I5).
			if progress != nil {
				skippedProviders := make([]string, 0, len(spec.Providers))
				for _, p := range spec.Providers {
					skippedProviders = append(skippedProviders, p.Provider)
				}
				progress(fmt.Sprintf("provider unavailable for %s (%s); searching available providers…",
					name, strings.Join(skippedProviders, ", ")))
			}
			matches, matchErr := a.ProviderMatches(ctx, name, spec, providerFilter)
			if matchErr != nil {
				warnings = append(warnings, fmt.Sprintf("provider search for %s: %v", name, matchErr))
				if len(matches) == 0 {
					a.recordProviderSearchMiss(ctx, name)
					continue
				}
			}
			// Only persist high-confidence matches (A4: no weak matches silently).
			var highConf []config.ToolInstallSpec
			for _, m := range matches {
				if m.Confidence == ProviderMatchHigh {
					highConf = append(highConf, config.ToolInstallSpec{
						Provider: m.Provider,
						Package:  m.Name,
						Options:  cloneOptionMap(m.Options),
					})
				}
			}
			if len(highConf) == 0 {
				a.recordProviderSearchMiss(ctx, name)
				continue
			}
			// Persist the alternative providers in priority order (I2). Prepend
			// them as additional candidates so that: (a) the existing configured
			// providers are preserved in the spec (they may become available on a
			// future run), (b) the available alternatives are tried first on this
			// host.
			if err := a.withConfig(func(c *config.RootConfig) error {
				s, ok := c.Tools[name]
				if !ok {
					return fmt.Errorf("tool %q not found", name)
				}
				// Avoid duplicates: only add candidates not already present.
				existing := make(map[string]struct{}, len(s.Providers))
				for _, p := range s.Providers {
					existing[p.Provider] = struct{}{}
				}
				newProviders := make([]config.ToolInstallSpec, 0, len(highConf)+len(s.Providers))
				for _, hc := range highConf {
					if _, dup := existing[hc.Provider]; !dup {
						newProviders = append(newProviders, hc)
					}
				}
				s.Providers = append(newProviders, s.Providers...)
				c.Tools[name] = s
				return nil
			}); err != nil {
				warnings = append(warnings, fmt.Sprintf("persisting provider search result for %s: %v", name, err))
				continue
			}
			a.clearProviderSearchMiss(ctx, name)
			searched = true
			if progress != nil {
				parts := make([]string, 0, len(highConf))
				for _, hc := range highConf {
					parts = append(parts, hc.Provider+"/"+hc.EffectivePackage(name))
				}
				progress(fmt.Sprintf("found alternative provider for %s: %s",
					name, strings.Join(parts, ", ")))
			}
		}
	}
	return searched, warnings
}

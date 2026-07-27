package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/profile"
	"github.com/lkshrk/omni/internal/provider"
)

type ToolListState string

const (
	ToolStateInstalled       ToolListState = "installed"
	ToolStateMissing         ToolListState = "missing"
	ToolStateOutdated        ToolListState = "outdated"
	ToolStateQuarantined     ToolListState = "quarantined"
	ToolStateBlockedMetadata ToolListState = "blocked-metadata"
	ToolStateSelfUpdates     ToolListState = "self-updates"
	ToolStateIgnored         ToolListState = "ignored"
	ToolStateUnclaimed       ToolListState = "unclaimed"
	ToolStateOutOfSync       ToolListState = "out-of-sync"
	ToolStateFailed          ToolListState = "failed"
)

type ProviderMatchConfidence string

const (
	ProviderMatchNone ProviderMatchConfidence = "none"
	ProviderMatchWeak ProviderMatchConfidence = "weak"
	ProviderMatchHigh ProviderMatchConfidence = "high"
)

type ProviderMatch struct {
	provider.SearchResult
	Confidence ProviderMatchConfidence
}

type ProviderMatchInstallResult struct {
	Matches   []ProviderMatch
	Added     []config.ToolInstallSpec
	Installed config.ToolInstallSpec
	SearchErr error
}

type ProviderMatchOptions struct {
	AllowWeak bool
}

var (
	ErrProviderDiscoveryNotConfigured     = errors.New("provider discovery requires configured tool")
	ErrProviderDiscoveryAlreadyConfigured = errors.New("provider discovery already configured")
	ErrProviderDiscoveryNoHighConfidence  = errors.New("no high-confidence provider match")
)

type RefreshInstalledProgressEvent struct {
	Provider      string
	ProviderLabel string
	Name          string
	Index         int
	Total         int
}

type RefreshDiscoveredProgressEvent struct {
	Provider string
	Index    int
	Total    int
}

type ToolListOptions struct {
	Provider string
	Group    string
	Host     string
	Name     string
	State    string
}

type ToolListItem struct {
	Tool  *database.ToolCache
	Group string
	State ToolListState
}

func (a *App) ListTools(ctx context.Context, providerFilter string) ([]*database.ToolCache, error) {
	return a.listToolsForDisplay(ctx, providerFilter, false)
}

// ListToolsForView — Includes ignored tools so they can appear in the Ignored section.
func (a *App) ListToolsForView(ctx context.Context, providerFilter string) ([]*ToolView, error) {
	tools, err := a.listToolsForDisplay(ctx, providerFilter, true)
	if err != nil {
		return nil, err
	}
	return toolViewsFromCache(tools), nil
}

func (a *App) listToolsForDisplay(ctx context.Context, providerFilter string, includeIgnored bool) ([]*database.ToolCache, error) {
	cfg, cfgErr := a.loadConfig()
	ecosystemProviders := map[string]string(nil)
	if cfgErr == nil {
		ecosystemProviders = a.ResolvedEcosystemProviders(ctx)
	}
	tools, err := a.listToolsFromConfig(ctx, cfg, providerFilter, ecosystemProviders, includeIgnored)
	if err != nil {
		return nil, err
	}
	if cfgErr != nil {
		return filterIgnoredToolCaches(tools, nil), nil
	}
	a.annotateUpdateQuarantine(ctx, cfg, tools)
	a.annotateSelfUpdatingCasks(tools)
	return tools, nil
}

func configuredProvidersFromCounts(counts map[string]int) []string {
	providers := make([]string, 0, len(counts))
	for p := range counts {
		providers = append(providers, p)
	}
	return providers
}

func (a *App) configuredProviderToolCountsFromResolved(tools []resolvedTool) map[string]int {
	counts := make(map[string]int)
	for _, t := range tools {
		providerName := a.operationProviderName(t.entry)
		if providerName != "" {
			counts[providerName]++
		}
	}
	return counts
}

func (a *App) ToolGroupMap(ctx context.Context) (map[string]string, error) {
	memberships, err := a.ToolMembershipMap(ctx)
	if err != nil {
		return nil, err
	}
	groups := make(map[string]string, len(memberships))
	for key, names := range memberships {
		if len(names) > 0 {
			groups[key] = names[0]
		}
	}
	return groups, nil
}

func (a *App) ToolMembershipMap(ctx context.Context) (map[string][]string, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	return a.toolMembershipMapFromConfig(ctx, cfg), nil
}

func (a *App) toolMembershipMapFromConfig(ctx context.Context, cfg *config.RootConfig) map[string][]string {
	resolved, _ := a.resolveTools(ctx, cfg, cfg.Groups, false)
	return toolMembershipMapFromResolved(resolved)
}

func toolMembershipMapFromResolved(resolved []resolvedTool) map[string][]string {
	groups := make(map[string][]string, len(resolved))
	for _, t := range resolved {
		if len(t.memberships) == 0 {
			continue
		}
		groups[t.entry.Name+"\x00"+t.entry.Provider] = t.memberships
	}
	return groups
}

func (a *App) QueryTools(ctx context.Context, opts ToolListOptions) ([]ToolListItem, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	resolvedProviders := a.ResolvedEcosystemProviders(ctx)
	tools, err := a.listToolsFromConfig(ctx, cfg, "", resolvedProviders, false)
	if err != nil {
		return nil, err
	}
	a.annotateUpdateQuarantine(ctx, cfg, tools)
	a.annotateSelfUpdatingCasks(tools)
	if opts.Provider != "" {
		filtered := tools[:0]
		for _, tool := range tools {
			if a.cacheToolMatchesProvider(tool, opts.Provider) {
				filtered = append(filtered, tool)
			}
		}
		tools = filtered
	}
	groups := cfg.Groups
	if opts.Host != "" {
		var ok bool
		groups, _, ok = effectiveHostGroups(cfg, groups, opts.Host)
		if !ok {
			return nil, fmt.Errorf("host %q is not configured", opts.Host)
		}
	}

	groupByTool := make(map[string]string)
	allowed := make(map[string]struct{})
	groupExists := opts.Group == ""
	resolved, _ := a.resolveTools(ctx, cfg, groups, false)
	for _, g := range groups {
		groupName := g.BaseName()
		if groupName == opts.Group {
			groupExists = true
		}
	}
	for _, entry := range resolved {
		key := resolvedToolKey(entry.entry)
		if _, exists := groupByTool[key]; !exists && len(entry.memberships) > 0 {
			groupByTool[key] = entry.memberships[0]
		}
		for _, groupName := range entry.memberships {
			if opts.Group == "" || groupName == opts.Group {
				allowed[key] = struct{}{}
			}
		}
	}
	if !groupExists {
		return nil, fmt.Errorf("group %q not found", opts.Group)
	}

	ignoreSet := ignoredToolSet(cfg)
	stateFilter, err := normalizeToolState(opts.State)
	if err != nil {
		return nil, err
	}

	var items []ToolListItem
	for _, t := range tools {
		key := NewToolKey(t.Name, t.Provider, t.Package).String()
		groupName := groupByTool[key]
		if opts.Host != "" {
			if _, ok := allowed[key]; !ok {
				continue
			}
		}
		if opts.Group != "" {
			if _, ok := allowed[key]; !ok {
				continue
			}
			groupName = opts.Group
		}
		if opts.Name != "" && !strings.EqualFold(t.Name, opts.Name) {
			continue
		}
		state := classifyToolState(t, ignoreSet, resolvedProviders)
		if stateFilter != "" && !toolStateMatches(state, stateFilter) {
			continue
		}
		items = append(items, ToolListItem{Tool: t, Group: groupName, State: state})
	}
	return items, nil
}

func (a *App) cacheToolMatchesProvider(tool *database.ToolCache, providerName string) bool {
	if providerName == "" || tool.Provider == providerName || tool.InstalledWith == providerName {
		return true
	}
	return false
}

func (a *App) cachedInstalledOwners(ctx context.Context) (map[string]string, error) {
	cached, err := a.readDB().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing cached install owners: %w", err)
	}
	owners := make(map[string]string)
	for _, t := range cached {
		if t == nil || !t.Installed || t.InstalledWith == "" {
			continue
		}
		if _, ok := a.registry.Get(t.InstalledWith); !ok && !fallbackLifecycleOwner(t.InstalledWith) {
			continue
		}
		owners[NewToolKey(t.Name, t.Provider, t.Package).String()] = t.InstalledWith
	}
	return owners, nil
}

func ignoredToolSet(cfg *config.RootConfig) map[string]struct{} {
	if cfg == nil {
		return nil
	}
	size := len(cfg.Ignore.Tools)
	for _, spec := range cfg.Tools {
		if spec.Ignore {
			size++
		}
	}
	if size == 0 {
		return nil
	}
	ignored := make(map[string]struct{}, size)
	for _, name := range cfg.Ignore.Tools {
		if name != "" {
			ignored[name] = struct{}{}
		}
	}
	for name, spec := range cfg.Tools {
		if spec.Ignore && name != "" {
			ignored[name] = struct{}{}
		}
	}
	return ignored
}

func toolNameIgnored(ignored map[string]struct{}, name string) bool {
	_, ok := ignored[name]
	return ok
}

func filterIgnoredToolCaches(tools []*database.ToolCache, ignored map[string]struct{}) []*database.ToolCache {
	if len(ignored) == 0 || len(tools) == 0 {
		return tools
	}
	filtered := make([]*database.ToolCache, 0, len(tools))
	for _, t := range tools {
		if t == nil || toolNameIgnored(ignored, t.Name) {
			continue
		}
		filtered = append(filtered, t)
	}
	return filtered
}

func (a *App) ignoredToolSetBestEffort() map[string]struct{} {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil
	}
	return ignoredToolSet(cfg)
}

func (a *App) configuredToolIgnored(name string) (bool, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return false, fmt.Errorf("loading config: %w", err)
	}
	return toolNameIgnored(ignoredToolSet(cfg), name), nil
}

func toolEntryLookupKeys(t config.ToolEntry) []string {
	return provider.PackageLookupKeys(t.Name, t.EffectivePackage())
}

func toolCacheLookupKeys(t *database.ToolCache) []string {
	pkg := t.Package
	if pkg == "" {
		pkg = t.Name
	}
	return provider.PackageLookupKeys(t.Name, pkg)
}

func refreshContextErr(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return ctx.Err()
}

func (a *App) refreshWithCachedOwner(ctx context.Context, t config.ToolEntry, keys []string, owner string, installedMaps map[string]map[string]string, metadataMaps map[string]map[string]provider.InstalledMetadata) (*database.ToolCache, *database.MetadataUpdate, bool, error) {
	if fallbackLifecycleOwner(owner) {
		installed, err := a.CheckToolFallback(ctx, t.Name)
		if err != nil {
			if ctxErr := refreshContextErr(ctx, err); ctxErr != nil {
				return nil, nil, true, ctxErr
			}
			if errors.Is(err, errFallbackNotConfigured) {
				return nil, nil, false, nil
			}
			return nil, nil, true, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, nil, true, err
		}
		if installed {
			return nil, nil, true, nil
		}
		return installedOwnerUpsert(t, owner, false, ""), nil, true, nil
	}
	ownerProv, ok := a.registry.Get(owner)
	if !ok {
		return nil, nil, false, nil
	}
	available, err := ownerProv.Available(ctx)
	if err != nil || !available {
		return nil, nil, false, nil
	}
	var (
		installed bool
		ver       string
	)
	if metadataMaps != nil {
		if m, ok := metadataMaps[owner]; ok {
			entry, installed := provider.LookupInstalledMetadata(m, keys)
			return installedOwnerUpsertWithMetadata(t, owner, installed, entry), installedSourceMetadataUpdatePtr(t, entry), true, nil
		}
	}
	if installedMaps != nil {
		if m, ok := installedMaps[owner]; ok {
			ver, installed = provider.LookupString(m, keys)
			return installedOwnerUpsert(t, owner, installed, ver), nil, true, nil
		}
	}
	if mbc, ok := ownerProv.(provider.MetadataBulkChecker); ok {
		if m, err := mbc.InstalledMetadataMap(ctx); err == nil {
			if metadataMaps != nil {
				metadataMaps[owner] = m
			}
			entry, installed := provider.LookupInstalledMetadata(m, keys)
			return installedOwnerUpsertWithMetadata(t, owner, installed, entry), installedSourceMetadataUpdatePtr(t, entry), true, nil
		}
	}
	if bc, ok := ownerProv.(provider.BulkChecker); ok {
		if m, err := bc.InstalledMap(ctx); err == nil {
			if installedMaps != nil {
				installedMaps[owner] = m
			}
			ver, installed = provider.LookupString(m, keys)
			return installedOwnerUpsert(t, owner, installed, ver), nil, true, nil
		}
	}
	tool := provider.Tool{Name: t.Name, Provider: owner, Package: t.EffectivePackage(), Options: t.Options}
	installed, ver, err = ownerProv.IsInstalled(ctx, tool)
	if err != nil {
		return nil, nil, true, nil
	}
	return installedOwnerUpsert(t, owner, installed, ver), nil, true, nil
}

func installedOwnerUpsert(t config.ToolEntry, owner string, installed bool, ver string) *database.ToolCache {
	return &database.ToolCache{
		Name:          t.Name,
		Provider:      t.Provider,
		Package:       t.EffectivePackage(),
		Installed:     installed,
		InstalledWith: owner,
		Version:       sql.NullString{String: ver, Valid: ver != ""},
		LastChecked:   time.Now(),
	}
}

func installedOwnerUpsertWithMetadata(t config.ToolEntry, owner string, installed bool, entry provider.InstalledMetadata) *database.ToolCache {
	upsert := installedOwnerUpsert(t, owner, installed, entry.Version)
	applyPrivilegeMetadata(upsert, entry.Privilege)
	return upsert
}

func resolvedProviderConcreteName(ctx context.Context, p provider.Provider) string {
	if cr, ok := p.(provider.ConcreteResolver); ok {
		if concrete, err := cr.ResolvedName(ctx); err == nil {
			return concrete
		}
	}
	return ""
}

func applyPrivilegeMetadata(upsert *database.ToolCache, plan provider.PrivilegePlan) {
	if upsert == nil || !plan.RequiresPrivilege() {
		return
	}
	now := time.Now()
	upsert.Privilege = string(plan.Requirement)
	upsert.PrivilegeReason = sql.NullString{String: plan.Reason, Valid: plan.Reason != ""}
	upsert.PrivilegeAt = &now
}

func installedSourceMetadataUpdate(t config.ToolEntry, entry provider.InstalledMetadata) (database.MetadataUpdate, bool) {
	hasSource := strings.TrimSpace(entry.Source.Type) != ""
	hasKind := strings.TrimSpace(entry.ArtifactKind) != ""
	if !hasSource && !hasKind {
		return database.MetadataUpdate{}, false
	}
	return database.MetadataUpdate{
		Name:         t.Name,
		Provider:     t.Provider,
		Package:      t.EffectivePackage(),
		SourceType:   strings.TrimSpace(entry.Source.Type),
		SourceOwner:  strings.TrimSpace(entry.Source.Owner),
		SourceRepo:   strings.TrimSpace(entry.Source.Repo),
		SourceURL:    strings.TrimSpace(entry.Source.URL),
		ArtifactKind: strings.TrimSpace(entry.ArtifactKind),
		SelfUpdates:  entry.SelfUpdates,
	}, true
}

func installedSourceMetadataUpdatePtr(t config.ToolEntry, entry provider.InstalledMetadata) *database.MetadataUpdate {
	update, ok := installedSourceMetadataUpdate(t, entry)
	if !ok {
		return nil
	}
	return &update
}

func installedMapFromMetadata(metadata map[string]provider.InstalledMetadata) map[string]string {
	m := make(map[string]string, len(metadata))
	for name, entry := range metadata {
		m[name] = entry.Version
	}
	return m
}

func classifyToolState(t *database.ToolCache, ignoreSet map[string]struct{}, resolved map[string]string) ToolListState {
	if t == nil {
		return ToolStateMissing
	}
	if t.FailedAt != nil {
		return ToolStateFailed
	}
	_, ignored := ignoreSet[t.Name]
	classification := ClassifyToolView(toolViewFromCache(t), ToolClassificationContext{
		Ignored:                ignored,
		EffectiveSystemManager: resolved[provider.EcosystemSystem],
		EffectivePythonManager: resolved[provider.EcosystemPython],
		EffectiveNodeManager:   resolved[provider.EcosystemNode],
	})
	if classification.Section == ToolViewSectionIgnored {
		return ToolStateIgnored
	}
	if classification.Section == ToolViewSectionUpdates {
		return ToolStateOutdated
	}
	if t.UpdateBlocked == UpdateBlockSelfUpdates {
		return ToolStateSelfUpdates
	}
	if t.UpdateBlocked == UpdateBlockMetadataMissing {
		return ToolStateBlockedMetadata
	}
	if classification.Section == ToolViewSectionQuarantined {
		return ToolStateQuarantined
	}
	switch classification.SyncStatus {
	case ToolSyncUnclaimed:
		return ToolStateUnclaimed
	case ToolSyncMissing:
		return ToolStateMissing
	case ToolSyncWrongProvider, ToolSyncNvmManaged:
		return ToolStateOutOfSync
	}
	if classification.Section == ToolViewSectionInstalled {
		return ToolStateInstalled
	}
	return ToolStateMissing
}

func normalizeToolState(raw string) (ToolListState, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return "", nil
	case "installed":
		return ToolStateInstalled, nil
	case "missing":
		return ToolStateMissing, nil
	case "outdated", "update", "updates":
		return ToolStateOutdated, nil
	case "quarantined", "quarantine":
		return ToolStateQuarantined, nil
	case "blocked-metadata", "metadata-blocked", "metadata":
		return ToolStateBlockedMetadata, nil
	case "ignored", "ignore":
		return ToolStateIgnored, nil
	case "unclaimed", "orphan", "orphans":
		return ToolStateUnclaimed, nil
	case "out-of-sync", "outofsync", "sync":
		return ToolStateOutOfSync, nil
	case "failed", "failure", "failures":
		return ToolStateFailed, nil
	default:
		return "", fmt.Errorf("unknown tool state %q", raw)
	}
}

func toolBinaryName(name string, spec config.ToolSpec) string {
	if spec.Fallback != nil && strings.TrimSpace(spec.Fallback.Binary) != "" {
		return strings.TrimSpace(spec.Fallback.Binary)
	}
	return name
}

func executableInstalledOnPath(binaryName string) bool {
	if binaryName == "" {
		return false
	}
	_, err := lookPath(binaryName)
	return err == nil
}

// Package is in the key so a failure on one variant does not suppress PATH detection for another.
func failedToolKey(name, provider, pkg string) string {
	pkg = strings.TrimSpace(pkg)
	if pkg == "" {
		pkg = name
	}
	return name + "\x00" + provider + "\x00" + pkg
}

// nil means the set could not be loaded, treated as all-failed so PATH detection stays suppressed.
func toolHasActiveFailure(t config.ToolEntry, failedTools map[string]struct{}) bool {
	if failedTools == nil {
		return true // fail-safe: suppress PATH detection when set could not be loaded
	}
	_, ok := failedTools[failedToolKey(t.Name, t.Provider, t.EffectivePackage())]
	return ok
}

// Returns nil on error; callers must treat nil as all-failed, since an empty map would enable PATH detection.
func (a *App) loadFailedToolSet(ctx context.Context) (map[string]struct{}, error) {
	failed, err := a.readDB().ListFailed(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading failed tool set for PATH detection: %w", err)
	}
	set := make(map[string]struct{}, len(failed))
	for _, f := range failed {
		set[failedToolKey(f.Name, f.Provider, f.Package)] = struct{}{}
	}
	return set, nil
}

// Writes on a positive hit, or a negative one that corrects a stale installed row.
func executableDetectSingleTool(t config.ToolEntry, cfg *config.RootConfig, failedTools map[string]struct{}, cachedInstalled bool) (*database.ToolCache, bool) {
	if toolHasActiveFailure(t, failedTools) {
		return nil, false
	}
	spec := cfg.Tools[t.Name]
	// A configured fallback has its own status lifecycle (gh?/gh/gh!) that PATH detection must not override.
	if spec.Fallback != nil && spec.Fallback.Status != "" {
		return nil, false
	}
	onPath := executableInstalledOnPath(toolBinaryName(t.Name, spec))
	if !onPath && !cachedInstalled {
		return nil, false
	}
	return &database.ToolCache{
		Name:          t.Name,
		Provider:      t.Provider,
		Package:       t.EffectivePackage(),
		Installed:     onPath,
		InstalledWith: "", // no concrete manager claim — avoids wrong-provider classification
		LastChecked:   time.Now(),
	}, true
}

func (a *App) executableDetectProviderTools(ctx context.Context, cfg *config.RootConfig, tools []config.ToolEntry, failedTools map[string]struct{}) error {
	// Lets stale positive rows be cleared when a previously-detected binary disappears from PATH.
	cachedRows, err := a.readDB().ListByProvider(ctx, tools[0].Provider)
	if err != nil {
		cachedRows = nil // best-effort; treat all as not-cached
	}
	cachedInstalled := make(map[string]bool, len(cachedRows))
	for _, r := range cachedRows {
		cachedInstalled[failedToolKey(r.Name, r.Provider, r.Package)] = r.Installed
	}

	var upserts []*database.ToolCache
	for _, t := range tools {
		was := cachedInstalled[failedToolKey(t.Name, t.Provider, t.EffectivePackage())]
		if row, ok := executableDetectSingleTool(t, cfg, failedTools, was); ok {
			upserts = append(upserts, row)
		}
	}
	if len(upserts) == 0 {
		return nil
	}
	if err := a.readDB().UpsertBatch(ctx, upserts); err != nil {
		return fmt.Errorf("upserting executable-detected tools: %w", err)
	}
	return nil
}

func toolStateMatches(state, filter ToolListState) bool {
	if state == filter {
		return true
	}
	return filter == ToolStateOutOfSync && (state == ToolStateMissing || state == ToolStateUnclaimed)
}

// Probes each available provider's best bulk capability in parallel and merges results in registry order.
func (a *App) scanInstalledBulkMaps(ctx context.Context, available []provider.Provider, scanProgress *refreshInstalledScanProgress) refreshLookupMaps {
	defer profile.Start("app.refresh.installed.bulk_maps")()
	bulkMaps := refreshLookupMaps{
		multiMaps:       make(map[string]map[string]provider.InstalledEntry),
		installedMaps:   make(map[string]map[string]string),
		metadataMaps:    make(map[string]map[string]provider.InstalledMetadata),
		concreteForBulk: make(map[string]string),
	}

	type providerBulkResult struct {
		name          string
		multiMap      map[string]provider.InstalledEntry // non-nil → MultiManagerBulkChecker
		metadataMap   map[string]provider.InstalledMetadata
		installedMap  map[string]string
		installedWith string
	}

	// Caps each package-manager subprocess so a hung one cannot block wg.Wait indefinitely.
	const refreshProviderTimeout = 2 * time.Minute

	results := make([]providerBulkResult, len(available))
	var wg sync.WaitGroup
	for i, p := range available {
		i, p := i, p
		wg.Add(1)
		go func() {
			defer wg.Done()
			provCtx, provCancel := context.WithTimeout(ctx, refreshProviderTimeout)
			defer provCancel()
			ctx := provCtx
			res := providerBulkResult{name: p.Name()}
			scan, scanErr := provider.ProbeBulkInstalled(ctx, p)
			var m map[string]string
			switch scan.Kind {
			case provider.BulkInstalledByManager:
				if scanErr != nil {
					fmt.Fprintf(os.Stderr, "warning: omni: bulk scan %s: %v\n", p.Name(), scanErr)
				} else {
					res.multiMap = scan.ByManager
				}
				results[i] = res
				return
			case provider.BulkInstalledMetadata:
				if scanErr != nil {
					fmt.Fprintf(os.Stderr, "warning: omni: bulk scan %s: %v\n", p.Name(), scanErr)
					results[i] = res
					return
				}
				res.metadataMap = scan.Metadata
				m = installedMapFromMetadata(scan.Metadata)
			case provider.BulkInstalledSimple:
				if scanErr != nil {
					fmt.Fprintf(os.Stderr, "warning: omni: bulk scan %s: %v\n", p.Name(), scanErr)
					results[i] = res
					return
				}
				m = scan.Installed
			default:
				results[i] = res
				return
			}
			installedWith := p.Name()
			if cr, ok := p.(provider.ConcreteResolver); ok {
				if name, cached := scanProgress.resolvedProviderName(p.Name()); cached {
					installedWith = name
				} else if name, resolveErr := cr.ResolvedName(ctx); resolveErr == nil && name != "" {
					installedWith = name
				} else {
					installedWith = "" // prefer unknown over stale ecosystem name
				}
			}
			res.installedMap = m
			res.installedWith = installedWith
			results[i] = res
		}()
	}
	wg.Wait()

	// Merge results in registry order so output remains deterministic.
	for _, res := range results {
		if res.name == "" {
			continue
		}
		scanProgress.emitProvider(res.name)
		if res.multiMap != nil {
			bulkMaps.multiMaps[res.name] = res.multiMap
			continue
		}
		if res.metadataMap != nil {
			bulkMaps.metadataMaps[res.name] = res.metadataMap
			bulkMaps.installedMaps[res.name] = res.installedMap
			bulkMaps.concreteForBulk[res.name] = res.installedWith
			continue
		}
		if res.installedMap != nil {
			bulkMaps.installedMaps[res.name] = res.installedMap
			bulkMaps.concreteForBulk[res.name] = res.installedWith
		}
	}
	return bulkMaps
}

func (a *App) RefreshInstalled(ctx context.Context, progress func(string)) error {
	defer profile.Start("app.refresh.installed.total")()

	stop := profile.Start("app.refresh.installed.load_config")
	cfg, err := a.loadConfig()
	stop()
	if err != nil {
		return err
	}
	stop = profile.Start("app.refresh.installed.resolve_tools")
	tools, _ := a.currentResolvedToolEntries(ctx, cfg)
	stop()
	if len(tools) == 0 {
		stop = profile.Start("app.refresh.installed.reconcile")
		err := a.reconcileResolvedTools(context.WithoutCancel(ctx), tools)
		stop()
		return err
	}
	stop = profile.Start("app.refresh.installed.cached_owners")
	cachedOwners, err := a.cachedInstalledOwners(ctx)
	stop()
	if err != nil {
		return err
	}

	// Resolved once and reused for both the scan total and the loop, avoiding two Available passes.
	stop = profile.Start("app.refresh.installed.available_providers")
	available := a.availableProviders(ctx)
	stop()
	scanProgress := a.newRefreshInstalledScanProgress(ctx, progress, tools, available)

	bulkMaps := a.scanInstalledBulkMaps(ctx, available, scanProgress)

	stop = profile.Start("app.refresh.installed.capture_empty")
	if captured, gitByTool := captureEmptyProviderInstalls(ctx, a, tools, available, bulkMaps.multiMaps, bulkMaps.installedMaps, bulkMaps.metadataMaps, bulkMaps.concreteForBulk); len(captured) > 0 {
		if updated, err := a.persistCapturedProviders(cfg, captured, gitByTool); err != nil {
			stop()
			return err
		} else if updated != nil {
			cfg = updated
			tools, _ = a.currentResolvedToolEntries(ctx, cfg)
		}
	}
	stop()

	upserts, metadataUpdates, err := a.buildInstalledUpserts(ctx, cfg, tools, cachedOwners, bulkMaps, scanProgress)
	if err != nil {
		return err
	}
	return a.persistInstalledResults(ctx, tools, upserts, metadataUpdates)
}

// Uses the pre-computed bulk maps where possible, falling back to a per-tool IsInstalled or PATH probe.
func (a *App) buildInstalledUpserts(ctx context.Context, cfg *config.RootConfig, tools []config.ToolEntry, cachedOwners map[string]string, bulkMaps refreshLookupMaps, scanProgress *refreshInstalledScanProgress) ([]*database.ToolCache, []database.MetadataUpdate, error) {
	defer profile.Start("app.refresh.installed.resolve_installed")()
	// nil on error — treated as "all failed" to protect retry-failed state.
	failedTools, err := a.loadFailedToolSet(ctx)
	if err != nil {
		failedTools = nil
	}
	// Built once here to avoid per-tool DB round-trips when clearing stale rows.
	allCached, cacheErr := a.readDB().List(ctx)
	cachedInstalledByKey := make(map[string]bool, len(allCached))
	if cacheErr == nil {
		for _, r := range allCached {
			cachedInstalledByKey[failedToolKey(r.Name, r.Provider, r.Package)] = r.Installed
		}
	}
	upserts := make([]*database.ToolCache, 0, len(tools))
	metadataUpdates := make([]database.MetadataUpdate, 0)
	for _, t := range tools {
		keys := toolEntryLookupKeys(t)

		opProvider := a.operationProviderName(t)
		if owner := cachedOwners[resolvedToolKey(t)]; owner != "" && owner != opProvider && t.InstallWith == "" {
			upsert, metadataUpdate, handled, err := a.refreshWithCachedOwner(ctx, t, keys, owner, bulkMaps.installedMaps, bulkMaps.metadataMaps)
			if err != nil {
				return nil, nil, err
			}
			if handled {
				if upsert == nil {
					continue
				}
				// A stale cached owner must not block rediscovery on the resolved route provider.
				if upsert.Installed {
					upserts = append(upserts, upsert)
					if metadataUpdate != nil {
						metadataUpdates = append(metadataUpdates, *metadataUpdate)
					}
					continue
				}
			}
		}
		if mm, hasMulti := bulkMaps.multiMaps[opProvider]; hasMulti && t.InstallWith == "" {
			entry := provider.LookupInstalledEntry(mm, keys) // zero InstalledEntry if tool not found in any backend
			upserts = append(upserts, &database.ToolCache{
				Name:          t.Name,
				Provider:      t.Provider,
				Package:       t.EffectivePackage(),
				Installed:     entry.ConcreteManager != "",
				InstalledWith: entry.ConcreteManager,
				Version:       sql.NullString{String: entry.Version, Valid: entry.Version != ""},
				LastChecked:   time.Now(),
			})
			continue
		}

		if m, hasBulk := bulkMaps.installedMaps[opProvider]; hasBulk && !(t.InstallWith != "" && opProvider == t.Provider) {
			ver, installed := provider.LookupString(m, keys)
			installedWith := bulkMaps.concreteForBulk[opProvider]
			if !installed {
				if altVer, altInstalled, altWith := a.lookupAlternateConfiguredInstall(cfg, t, keys, bulkMaps.multiMaps, bulkMaps.installedMaps, bulkMaps.concreteForBulk); altInstalled {
					ver, installed, installedWith = altVer, true, altWith
				}
			}
			upsert := &database.ToolCache{
				Name:          t.Name,
				Provider:      t.Provider,
				Package:       t.EffectivePackage(),
				Installed:     installed,
				InstalledWith: installedWith,
				Version:       sql.NullString{String: ver, Valid: ver != ""},
				LastChecked:   time.Now(),
			}
			if metadata, ok := provider.LookupInstalledMetadata(bulkMaps.metadataMaps[opProvider], keys); ok {
				applyPrivilegeMetadata(upsert, metadata.Privilege)
				if update, ok := installedSourceMetadataUpdate(t, metadata); ok {
					metadataUpdates = append(metadataUpdates, update)
				}
			}
			upserts = append(upserts, upsert)
			continue
		}
		p, ok := a.registry.Get(opProvider)
		if !ok {
			// Provider not registered — probe PATH as a last resort.
			was := cachedInstalledByKey[failedToolKey(t.Name, t.Provider, t.EffectivePackage())]
			if row, detected := executableDetectSingleTool(t, cfg, failedTools, was); detected {
				upserts = append(upserts, row)
			}
			continue
		}
		avail, err := p.Available(ctx)
		if err != nil {
			continue
		}
		if !avail {
			// Provider unavailable on this host (e.g. brew on Linux) — probe PATH as a last resort.
			was := cachedInstalledByKey[failedToolKey(t.Name, t.Provider, t.EffectivePackage())]
			if row, detected := executableDetectSingleTool(t, cfg, failedTools, was); detected {
				upserts = append(upserts, row)
			}
			continue
		}
		scanProgress.emitProvider(opProvider)
		installed, ver, err := a.isInstalledWithEntry(ctx, p, opProvider, t)
		if err != nil {
			continue
		}
		installedWith := installedWithForOperation(ctx, p, opProvider, t.InstallWith)
		upserts = append(upserts, &database.ToolCache{
			Name:          t.Name,
			Provider:      t.Provider,
			Package:       t.EffectivePackage(),
			Installed:     installed,
			InstalledWith: installedWith,
			Version:       sql.NullString{String: ver, Valid: ver != ""},
			LastChecked:   time.Now(),
		})
	}
	return upserts, metadataUpdates, nil
}

// Runs on a non-cancellable context so a cancelled refresh still commits the scan work it completed.
func (a *App) persistInstalledResults(ctx context.Context, tools []config.ToolEntry, upserts []*database.ToolCache, metadataUpdates []database.MetadataUpdate) error {
	defer profile.Start("app.refresh.installed.write_reconcile")()
	writeCtx := context.WithoutCancel(ctx)
	if err := a.readDB().UpsertBatch(writeCtx, upserts); err != nil {
		return fmt.Errorf("upserting installed status: %w", err)
	}
	if err := a.readDB().UpsertMetadataBatch(writeCtx, metadataUpdates); err != nil {
		return fmt.Errorf("upserting installed metadata: %w", err)
	}
	if err := a.enrichToolGitFromMetadataUpdates(writeCtx, metadataUpdates); err != nil {
		return fmt.Errorf("updating tool git metadata: %w", err)
	}
	return a.reconcileResolvedTools(writeCtx, tools)
}

type refreshInstalledScanProgress struct {
	progress         func(string)
	index            int
	total            int
	labelsByProvider map[string][]string
	emitted          map[string]bool
	// rcMu guards resolvedConcrete: written single-goroutine during setup, read concurrently by bulk-scan goroutines.
	rcMu             sync.RWMutex
	resolvedConcrete map[string]string
}

func (a *App) newRefreshInstalledScanProgress(ctx context.Context, progress func(string), tools []config.ToolEntry, available []provider.Provider) *refreshInstalledScanProgress {
	scan := &refreshInstalledScanProgress{progress: progress}
	if progress == nil {
		return scan
	}
	availableByName := make(map[string]provider.Provider, len(available))
	for _, p := range available {
		availableByName[p.Name()] = p
	}
	scan.labelsByProvider = make(map[string][]string)
	scan.emitted = make(map[string]bool)
	scan.resolvedConcrete = make(map[string]string)
	seenLabels := make(map[string]bool)
	for _, t := range tools {
		opProvider := a.operationProviderName(t)
		if opProvider == "" {
			continue
		}
		p := availableByName[opProvider]
		if p == nil {
			continue
		}
		label := a.refreshInstalledScanLabel(ctx, t, opProvider, p, scan.resolvedConcrete)
		if label == "" || seenLabels[label] {
			continue
		}
		seenLabels[label] = true
		scan.labelsByProvider[opProvider] = append(scan.labelsByProvider[opProvider], label)
		scan.total++
	}
	return scan
}

func (p *refreshInstalledScanProgress) emitProvider(providerName string) {
	if p == nil || p.progress == nil {
		return
	}
	for _, label := range p.labelsByProvider[providerName] {
		if p.emitted[label] {
			continue
		}
		p.emitted[label] = true
		p.index++
		p.progress(RefreshProviderScanProgressText(label, p.index, p.total))
	}
}

func (p *refreshInstalledScanProgress) resolvedProviderName(providerName string) (string, bool) {
	if p == nil || p.resolvedConcrete == nil {
		return "", false
	}
	p.rcMu.RLock()
	defer p.rcMu.RUnlock()
	name, ok := p.resolvedConcrete[providerName]
	return name, ok
}

func (a *App) refreshInstalledScanLabel(ctx context.Context, t config.ToolEntry, opProvider string, p provider.Provider, resolvedConcrete map[string]string) string {
	if t.Provider != "" && t.InstallWith != "" && t.InstallWith != t.Provider && a.registry.IsEcosystemProvider(t.Provider) {
		return ProviderScanDisplayLabel(t.Provider, t.InstallWith)
	}
	if concrete, ok := resolvedConcrete[opProvider]; ok {
		return ProviderScanDisplayLabel(opProvider, concrete)
	}
	if cr, ok := p.(provider.ConcreteResolver); ok {
		concrete, err := cr.ResolvedName(ctx)
		if err != nil {
			resolvedConcrete[opProvider] = ""
			return opProvider
		}
		resolvedConcrete[opProvider] = concrete
		return ProviderScanDisplayLabel(opProvider, concrete)
	}
	return opProvider
}

// RefreshProviderInstalled — Scoped to one provider so callers can run providers in parallel; cancellation errors are returned so state does not silently go stale.
func (a *App) RefreshProviderInstalled(ctx context.Context, provName string) error {
	return a.RefreshProviderInstalledWithProgress(ctx, provName, nil)
}

// ctx should be non-cancellable so a cancelled refresh still commits the rows it resolved.
func (a *App) flushProviderInstalledUpserts(ctx context.Context, provName string, upserts []*database.ToolCache, metadataUpdates []database.MetadataUpdate) error {
	if err := a.readDB().UpsertBatch(ctx, upserts); err != nil {
		return fmt.Errorf("upserting installed status for %s: %w", provName, err)
	}
	if err := a.readDB().UpsertMetadataBatch(ctx, metadataUpdates); err != nil {
		return fmt.Errorf("upserting metadata for %s: %w", provName, err)
	}
	if err := a.enrichToolGitFromMetadataUpdates(ctx, metadataUpdates); err != nil {
		return fmt.Errorf("updating tool git metadata for %s: %w", provName, err)
	}
	return nil
}

func (a *App) RefreshProviderInstalledWithProgress(ctx context.Context, provName string, progress func(RefreshInstalledProgressEvent)) error {
	defer profile.Start("app.refresh.installed.provider." + provName)()

	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	tools, _ := a.currentResolvedToolEntries(ctx, cfg)
	cachedOwners, err := a.cachedInstalledOwners(ctx)
	if err != nil {
		return err
	}

	var provTools []config.ToolEntry
	for _, t := range tools {
		if a.operationProviderName(t) == provName {
			provTools = append(provTools, t)
		}
	}
	if len(provTools) == 0 {
		return nil
	}

	// nil on error — treated as "all failed" to protect retry-failed state.
	provFailedTools, err := a.loadFailedToolSet(ctx)
	if err != nil {
		provFailedTools = nil
	}

	p, ok := a.registry.Get(provName)
	if !ok {
		// Provider not registered here, so probe PATH and avoid showing present tools as out-of-sync.
		return a.executableDetectProviderTools(context.WithoutCancel(ctx), cfg, provTools, provFailedTools)
	}
	avail, err := p.Available(ctx)
	if err != nil {
		return refreshContextErr(ctx, err)
	}
	if !avail {
		// Provider registered but unavailable here (e.g. brew on Linux), so probe PATH as a last resort.
		return a.executableDetectProviderTools(context.WithoutCancel(ctx), cfg, provTools, provFailedTools)
	}
	providerLabel := ProviderScanDisplayLabel(provName, resolvedProviderConcreteName(ctx, p))
	emitTool := func(index int, t config.ToolEntry) {
		if progress == nil {
			return
		}
		progress(RefreshInstalledProgressEvent{
			Provider:      provName,
			ProviderLabel: providerLabel,
			Name:          t.Name,
			Index:         index + 1,
			Total:         len(provTools),
		})
	}
	writeCtx := context.WithoutCancel(ctx)
	upserts := make([]*database.ToolCache, 0, len(provTools))
	metadataUpdates := make([]database.MetadataUpdate, 0)
	flushUpserts := func() error {
		return a.flushProviderInstalledUpserts(writeCtx, provName, upserts, metadataUpdates)
	}
	ownerInstalledMaps := make(map[string]map[string]string)
	ownerMetadataMaps := make(map[string]map[string]provider.InstalledMetadata)
	lookupMaps := &refreshLookupMaps{
		installedMaps:   ownerInstalledMaps,
		metadataMaps:    ownerMetadataMaps,
		concreteForBulk: make(map[string]string),
	}

	if mbc, ok := p.(provider.MultiManagerBulkChecker); ok {
		entries, err := mbc.InstalledByManager(ctx)
		if err != nil {
			return refreshContextErr(ctx, err)
		}
		for i, t := range provTools {
			emitTool(i, t)
			keys := toolEntryLookupKeys(t)
			if owner := cachedOwners[resolvedToolKey(t)]; owner != "" && owner != provName && t.InstallWith == "" {
				upsert, metadataUpdate, handled, err := a.refreshWithCachedOwner(ctx, t, keys, owner, ownerInstalledMaps, ownerMetadataMaps)
				if err != nil {
					return err
				}
				if handled {
					if useCachedOwnerUpsert(upsert, metadataUpdate, &upserts, &metadataUpdates) {
						continue
					}
				}
			}
			if t.InstallWith != "" && a.operationProviderName(t) == t.Provider {
				installed, ver, err := a.isInstalledWithEntry(ctx, p, provName, t)
				if err != nil {
					continue
				}
				upserts = append(upserts, &database.ToolCache{
					Name:          t.Name,
					Provider:      t.Provider,
					Package:       t.EffectivePackage(),
					Installed:     installed,
					InstalledWith: t.InstallWith,
					Version:       sql.NullString{String: ver, Valid: ver != ""},
					LastChecked:   time.Now(),
				})
				continue
			}
			entry := provider.LookupInstalledEntry(entries, keys)
			installed := entry.ConcreteManager != ""
			upserts = append(upserts, &database.ToolCache{
				Name:          t.Name,
				Provider:      t.Provider,
				Package:       t.EffectivePackage(),
				Installed:     installed,
				InstalledWith: entry.ConcreteManager,
				Version:       sql.NullString{String: entry.Version, Valid: entry.Version != ""},
				LastChecked:   time.Now(),
			})
		}
		return flushUpserts()
	}

	// On error fall through to the per-tool path so a partial failure does not skip all tools.
	if mbc, ok := p.(provider.MetadataBulkChecker); ok {
		if metadata, err := mbc.InstalledMetadataMap(ctx); err == nil {
			m := installedMapFromMetadata(metadata)
			installedWith := p.Name()
			if cr, ok := p.(provider.ConcreteResolver); ok {
				if name, resolveErr := cr.ResolvedName(ctx); resolveErr == nil && name != "" {
					installedWith = name
				} else {
					installedWith = ""
				}
			}
			if installedWith != "" {
				ownerMetadataMaps[installedWith] = metadata
				ownerInstalledMaps[installedWith] = m
				lookupMaps.concreteForBulk[installedWith] = installedWith
			}
			for i, t := range provTools {
				emitTool(i, t)
				keys := toolEntryLookupKeys(t)
				if owner := cachedOwners[resolvedToolKey(t)]; owner != "" && owner != provName && t.InstallWith == "" {
					upsert, metadataUpdate, handled, err := a.refreshWithCachedOwner(ctx, t, keys, owner, ownerInstalledMaps, ownerMetadataMaps)
					if err != nil {
						return err
					}
					if handled {
						if useCachedOwnerUpsert(upsert, metadataUpdate, &upserts, &metadataUpdates) {
							continue
						}
					}
				}
				ver, installed, resolvedWith := a.resolveInstalledBulkLookup(ctx, cfg, t, keys, m, installedWith, lookupMaps)
				upsert := &database.ToolCache{
					Name:          t.Name,
					Provider:      t.Provider,
					Package:       t.EffectivePackage(),
					Installed:     installed,
					InstalledWith: resolvedWith,
					Version:       sql.NullString{String: ver, Valid: ver != ""},
					LastChecked:   time.Now(),
				}
				if entry, ok := provider.LookupInstalledMetadata(metadata, keys); ok {
					applyPrivilegeMetadata(upsert, entry.Privilege)
					if update, ok := installedSourceMetadataUpdate(t, entry); ok {
						metadataUpdates = append(metadataUpdates, update)
					}
				}
				upserts = append(upserts, upsert)
			}
			return flushUpserts()
		} else if err := refreshContextErr(ctx, err); err != nil {
			return err
		}
	}
	if bc, ok := p.(provider.BulkChecker); ok {
		if m, err := bc.InstalledMap(ctx); err == nil {
			installedWith := p.Name()
			if cr, ok := p.(provider.ConcreteResolver); ok {
				if name, resolveErr := cr.ResolvedName(ctx); resolveErr == nil && name != "" {
					installedWith = name
				} else {
					installedWith = ""
				}
			}
			if installedWith != "" {
				ownerInstalledMaps[installedWith] = m
				lookupMaps.concreteForBulk[installedWith] = installedWith
			}
			for i, t := range provTools {
				emitTool(i, t)
				keys := toolEntryLookupKeys(t)
				if owner := cachedOwners[resolvedToolKey(t)]; owner != "" && owner != provName && t.InstallWith == "" {
					upsert, metadataUpdate, handled, err := a.refreshWithCachedOwner(ctx, t, keys, owner, ownerInstalledMaps, ownerMetadataMaps)
					if err != nil {
						return err
					}
					if handled {
						if useCachedOwnerUpsert(upsert, metadataUpdate, &upserts, &metadataUpdates) {
							continue
						}
					}
				}
				ver, installed, resolvedWith := a.resolveInstalledBulkLookup(ctx, cfg, t, keys, m, installedWith, lookupMaps)
				upserts = append(upserts, &database.ToolCache{
					Name:          t.Name,
					Provider:      t.Provider,
					Package:       t.EffectivePackage(),
					Installed:     installed,
					InstalledWith: resolvedWith,
					Version:       sql.NullString{String: ver, Valid: ver != ""},
					LastChecked:   time.Now(),
				})
			}
			return flushUpserts()
		} else if err := refreshContextErr(ctx, err); err != nil {
			return err
		}
	}

	for i, t := range provTools {
		emitTool(i, t)
		if owner := cachedOwners[resolvedToolKey(t)]; owner != "" && owner != provName && t.InstallWith == "" {
			upsert, metadataUpdate, handled, err := a.refreshWithCachedOwner(ctx, t, toolEntryLookupKeys(t), owner, ownerInstalledMaps, ownerMetadataMaps)
			if err != nil {
				return err
			}
			if handled {
				if useCachedOwnerUpsert(upsert, metadataUpdate, &upserts, &metadataUpdates) {
					continue
				}
			}
		}
		installed, ver, err := a.isInstalledWithEntry(ctx, p, provName, t)
		if err != nil {
			if err := refreshContextErr(ctx, err); err != nil {
				return err
			}
			continue
		}
		installedWith := installedWithForOperation(ctx, p, provName, t.InstallWith)
		upserts = append(upserts, &database.ToolCache{
			Name:          t.Name,
			Provider:      t.Provider,
			Package:       t.EffectivePackage(),
			Installed:     installed,
			InstalledWith: installedWith,
			Version:       sql.NullString{String: ver, Valid: ver != ""},
			LastChecked:   time.Now(),
		})
	}
	return flushUpserts()
}

// RefreshDiscovered — Config-tracked rows are never overwritten; providers that error are silently skipped.
func (a *App) RefreshDiscovered(ctx context.Context) error {
	return a.RefreshDiscoveredWithProgress(ctx, nil)
}

func (a *App) RefreshDiscoveredWithProgress(ctx context.Context, progress func(RefreshDiscoveredProgressEvent)) error {
	defer profile.Start("app.refresh.discovered.total")()

	stop := profile.Start("app.refresh.discovered.load_config")
	cfg, err := a.loadConfig()
	stop()
	if err != nil {
		return err
	}

	cutoff := time.Now()
	stop = profile.Start("app.refresh.discovered.scan")
	discovered := a.discoverUntrackedInstalled(ctx, cfg, progress)
	stop()

	writeCtx := context.WithoutCancel(ctx)
	stop = profile.Start("app.refresh.discovered.write")
	if err := a.readDB().UpsertDiscoveredBatch(writeCtx, discovered); err != nil {
		stop()
		return fmt.Errorf("upserting discovered tools: %w", err)
	}
	stop()

	stop = profile.Start("app.refresh.discovered.prune")
	if err := a.readDB().PruneDiscovered(writeCtx, cutoff); err != nil {
		stop()
		return fmt.Errorf("pruning discovered tools: %w", err)
	}
	stop()
	return nil
}

func isSystemInventoryProvider(name string) bool {
	switch name {
	case "apt", "dnf", "pacman", "apk", "zypper":
		return true
	default:
		return false
	}
}

func (a *App) previewDiscovered(ctx context.Context) ([]*database.ToolCache, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	discovered := a.discoverUntrackedInstalled(ctx, cfg, nil)
	out := make([]*database.ToolCache, 0, len(discovered))
	for _, d := range discovered {
		out = append(out, &database.ToolCache{
			Name:          d.Name,
			Provider:      d.Provider,
			Package:       d.Name,
			Installed:     true,
			InstalledWith: d.InstalledWith,
			Version:       sql.NullString{String: d.Version, Valid: d.Version != ""},
			Tracked:       false,
		})
	}
	return out, nil
}

func (a *App) discoverCLIToolSets(ctx context.Context) map[string]map[string]bool {
	cliSets := make(map[string]map[string]bool)
	for _, prov := range a.availableProviders(ctx) {
		cp, ok := prov.(provider.CLIToolProvider)
		if !ok {
			continue
		}
		set, err := cp.CLIToolSet(ctx)
		if err != nil {
			// Fail closed with an empty set: omitting the entry would fall back to allow-all.
			cliSets[prov.Name()] = map[string]bool{}
			continue
		}
		if set == nil {
			// namedProvider.CLIToolSet returns (nil, nil) when the wrapped provider does not implement it at all.
			continue
		}
		cliSets[prov.Name()] = set
	}
	return cliSets
}

func discoverCLIToolAllowed(cliSets map[string]map[string]bool, providerName, toolName string) bool {
	cliSet, ok := cliSets[providerName]
	if !ok {
		return true
	}
	return cliSet[strings.ToLower(toolName)]
}

func (a *App) discoverUntrackedInstalled(ctx context.Context, cfg *config.RootConfig, progress func(RefreshDiscoveredProgressEvent)) []database.DiscoveredUpsert {
	configuredNames := make(map[string]struct{})
	for name := range cfg.Tools {
		configuredNames[name] = struct{}{}
	}
	for name := range ignoredToolSet(cfg) {
		configuredNames[name] = struct{}{}
	}

	// Discovered tools get the family name while scope checks use the resolved concrete manager.
	stop := profile.Start("app.refresh.discovered.resolve_ecosystems")
	ecosystemProviders := a.ResolvedEcosystemProviders(ctx)
	revEcosystem := reverseEcosystemProviders(ecosystemProviders)
	stop()
	scope := a.discoveryProviderScope(ctx, cfg, ecosystemProviders)
	if scope.empty() {
		return nil
	}

	// Providers are processed serially after the availability pass, so no lock is needed.
	var discovered []database.DiscoveredUpsert
	// Per-provider errors are skipped so one bad provider does not prevent discovering the rest.
	disabled := make(map[string]struct{})
	for _, name := range a.effectiveSettings(cfg).DisabledProviders {
		disabled[name] = struct{}{}
	}
	providers := make([]provider.Provider, 0)
	stop = profile.Start("app.refresh.discovered.available_providers")
	for _, p := range a.availableProviders(ctx) {
		// A global pip environment mixes applications with libraries, so it is never authoritative for discovery.
		if p.Name() == "pip" {
			continue
		}
		if _, off := disabled[p.Name()]; off {
			continue // provider disabled on this host: do not discover under it
		}
		if !a.registry.ImportSkipsProvider(p.Name()) && a.discoveryProviderAllowed(p, revEcosystem, scope) {
			providers = append(providers, p)
		}
	}
	stop()
	cliSets := a.discoverCLIToolSets(ctx)
	for i, p := range providers {
		if progress != nil {
			progress(RefreshDiscoveredProgressEvent{Provider: p.Name(), Index: i + 1, Total: len(providers)})
		}

		configProvider := discoveryConfigProvider(p.Name(), revEcosystem, scope)

		// Probe all backends to get per-tool concrete-manager attribution (pnpm vs npm vs bun).
		if mbc, ok := p.(provider.MultiManagerBulkChecker); ok {
			entries, err := mbc.InstalledByManager(ctx)
			if err != nil {
				return nil
			}
			for name, entry := range entries {
				if _, ok := configuredNames[name]; ok {
					continue
				}
				if !discoverCLIToolAllowed(cliSets, p.Name(), name) {
					continue
				}
				if !scope.allowsDiscovered(configProvider, entry.ConcreteManager) {
					continue
				}
				discovered = append(discovered, database.DiscoveredUpsert{
					Name:          name,
					Provider:      configProvider,
					InstalledWith: entry.ConcreteManager,
					Version:       entry.Version,
				})
			}
			continue
		}

		installed, err := p.ListInstalled(ctx)
		if err != nil {
			continue // best-effort: skip erroring providers
		}
		var systemBaseline map[string]bool
		if isSystemInventoryProvider(p.Name()) {
			systemBaseline, err = a.systemInventoryBaseline(ctx, p.Name(), installed)
			if err != nil {
				continue // best-effort: skip providers whose baseline can't be read/recorded
			}
		}
		for _, t := range installed {
			if _, ok := configuredNames[t.Name]; ok {
				continue // already in config; skip
			}
			if systemBaseline != nil && systemBaseline[t.Name] {
				// Predates the host's recorded baseline, so not a package the user just installed.
				continue
			}
			if !discoverCLIToolAllowed(cliSets, p.Name(), t.Name) {
				continue
			}
			if !scope.allowsDiscovered(configProvider, p.Name()) {
				continue
			}
			discovered = append(discovered, database.DiscoveredUpsert{
				Name:          t.Name,
				Provider:      configProvider,
				InstalledWith: p.Name(),
				Version:       t.Version,
			})
		}
	}
	return collapseSharedStoreDuplicates(discovered, ecosystemProviders)
}

func systemInventoryBaselineStateKey(providerName string) string {
	return "system-inventory-baseline:" + providerName
}

// System PMs report every image-baked package as manually installed, so the first observation becomes the baseline.
func (a *App) systemInventoryBaseline(ctx context.Context, providerName string, installed []provider.InstalledTool) (map[string]bool, error) {
	key := systemInventoryBaselineStateKey(providerName)
	stored, err := a.readDB().GetState(ctx, key)
	if err == nil {
		return systemInventoryBaselineSet(stored), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	names := make([]string, 0, len(installed))
	for _, t := range installed {
		names = append(names, t.Name)
	}
	sort.Strings(names)
	if err := a.readDB().SetState(ctx, key, strings.Join(names, "\n")); err != nil {
		return nil, err
	}
	return systemInventoryBaselineSet(strings.Join(names, "\n")), nil
}

func systemInventoryBaselineSet(stored string) map[string]bool {
	set := make(map[string]bool)
	for _, name := range strings.Split(stored, "\n") {
		if name = strings.TrimSpace(name); name != "" {
			set[name] = true
		}
	}
	return set
}

func (a *App) ListDiscovered(ctx context.Context) ([]*ToolView, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	discovered, err := a.listDiscoveredFromConfig(ctx, cfg, a.ResolvedEcosystemProviders(ctx))
	if err != nil {
		return nil, err
	}
	return toolViewsFromCache(discovered), nil
}

type ToolDisplaySnapshot struct {
	Tools                  []*ToolView
	Discovered             []*ToolView
	EffectiveSystemManager string
}

func (a *App) ToolDisplaySnapshot(ctx context.Context) (*ToolDisplaySnapshot, error) {
	tools, err := a.ListToolsForView(ctx, "")
	if err != nil {
		return nil, err
	}
	discovered, err := a.ListDiscovered(ctx)
	if err != nil {
		return nil, err
	}
	return &ToolDisplaySnapshot{
		Tools:                  tools,
		Discovered:             discovered,
		EffectiveSystemManager: a.EffectiveSystemManager(ctx),
	}, nil
}

func (a *App) listDiscoveredFromConfig(ctx context.Context, cfg *config.RootConfig, ecosystemProviders map[string]string) ([]*database.ToolCache, error) {
	discovered, err := a.readDB().ListDiscovered(ctx)
	if err != nil {
		return nil, err
	}
	scope := a.discoveryProviderScope(ctx, cfg, ecosystemProviders)
	discovered = filterDiscoveredByScope(discovered, scope)
	return filterIgnoredToolCaches(discovered, ignoredToolSet(cfg)), nil
}

func (a *App) listToolsFromConfig(ctx context.Context, cfg *config.RootConfig, providerFilter string, ecosystemProviders map[string]string, includeIgnored bool) ([]*database.ToolCache, error) {
	var (
		tools []*database.ToolCache
		err   error
	)
	if providerFilter != "" {
		tools, err = a.readDB().ListByProvider(ctx, providerFilter)
	} else {
		tools, err = a.readDB().List(ctx)
	}
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return tools, nil
	}
	// Only tools with no cache state at all get a synthesized not-installed row.
	cachedNames := make(map[string]struct{}, len(tools))
	for _, tc := range tools {
		if tc != nil {
			cachedNames[tc.Name] = struct{}{}
		}
	}
	configured, authoritative := a.configuredToolCacheKeys(ctx, cfg, includeIgnored)
	scope := a.discoveryProviderScope(ctx, cfg, ecosystemProviders)
	tools = filterToolCachesByConfigAndScope(tools, configured, authoritative, scope)
	if !includeIgnored {
		tools = filterIgnoredToolCaches(tools, ignoredToolSet(cfg))
	}
	return a.appendConfigLedRows(ctx, cfg, tools, cachedNames, includeIgnored), nil
}

// Configured tools with no cache row are synthesized as not-installed so they surface as needs-sync instead of vanishing.
func (a *App) appendConfigLedRows(ctx context.Context, cfg *config.RootConfig, tools []*database.ToolCache, cachedNames map[string]struct{}, includeIgnored bool) []*database.ToolCache {
	// A name already represented by any cache row must not be re-synthesized.
	present := make(map[string]struct{}, len(tools)+len(cachedNames))
	for name := range cachedNames {
		present[name] = struct{}{}
	}
	for _, tc := range tools {
		if tc == nil {
			continue
		}
		present[tc.Name] = struct{}{}
	}
	groups, _ := a.currentToolGroupsWithAuthority(cfg)
	entries, _ := a.resolvedToolEntries(ctx, cfg, groups, includeIgnored)
	for _, e := range entries {
		if _, ok := present[e.Name]; ok {
			continue
		}
		present[e.Name] = struct{}{}
		tools = append(tools, &database.ToolCache{
			Name:     e.Name,
			Provider: e.Provider,
			Package:  e.EffectivePackage(),
			Tracked:  true,
		})
	}
	if includeIgnored {
		ignored := ignoredToolSet(cfg)
		settings := a.effectiveSettings(cfg)
		for name := range ignored {
			if _, ok := present[name]; ok {
				continue
			}
			spec, ok := cfg.Tools[name]
			if !ok {
				continue
			}
			install := a.resolveInstallSpecWithSettings(ctx, name, spec, nil, settings)
			entry := spec.ToToolEntry(name, install)
			present[name] = struct{}{}
			tools = append(tools, &database.ToolCache{
				Name:     entry.Name,
				Provider: entry.Provider,
				Package:  entry.EffectivePackage(),
				Tracked:  true,
			})
		}
	}
	return tools
}

func (a *App) configuredToolCacheKeys(ctx context.Context, cfg *config.RootConfig, includeIgnored bool) (map[string]struct{}, bool) {
	groups, _ := a.currentToolGroupsWithAuthority(cfg)
	entries, _ := a.resolvedToolEntries(ctx, cfg, groups, includeIgnored)
	keys := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		keys[resolvedToolKey(entry)] = struct{}{}
		if entry.InstallWith != "" && entry.InstallWith != entry.Provider {
			keys[NewToolKey(entry.Name, entry.InstallWith, entry.EffectivePackage()).String()] = struct{}{}
		}
	}
	return keys, len(cfg.Tools) > 0 || len(keys) > 0
}

type discoveryScope struct {
	providers map[string]struct{}
	managers  map[string]struct{}
}

func (s discoveryScope) empty() bool {
	return len(s.providers) == 0 && len(s.managers) == 0
}

func (s discoveryScope) hasProvider(name string) bool {
	_, ok := s.providers[name]
	return ok
}

func (s discoveryScope) hasManager(name string) bool {
	_, ok := s.managers[name]
	return ok
}

func (s discoveryScope) allowsDiscovered(configProvider, installedWith string) bool {
	if s.empty() {
		return false
	}
	if configProvider != "" && !s.hasProvider(configProvider) && !s.hasManager(configProvider) {
		return false
	}
	if installedWith == "" {
		return false
	}
	if installedWith == configProvider {
		return true
	}
	return s.hasManager(installedWith) || s.hasProvider(installedWith)
}

func (a *App) discoveryProviderScope(ctx context.Context, cfg *config.RootConfig, ecosystemProviders map[string]string) discoveryScope {
	scope := discoveryScope{
		providers: make(map[string]struct{}),
		managers:  make(map[string]struct{}),
	}
	add := func(t config.ToolEntry) {
		if t.Provider != "" {
			scope.providers[t.Provider] = struct{}{}
		}
		if t.InstallWith != "" {
			scope.managers[t.InstallWith] = struct{}{}
		}
		if opProvider := a.operationProviderName(t); opProvider != "" && opProvider != t.Provider {
			scope.managers[opProvider] = struct{}{}
		}
		if t.InstallWith == "" {
			if concrete := ecosystemProviders[t.Provider]; concrete != "" {
				scope.managers[concrete] = struct{}{}
			}
		}
	}
	seen := make(map[string]struct{})
	for _, group := range a.currentToolGroups(cfg) {
		if group == nil {
			continue
		}
		for _, membership := range group.Tools {
			if membership.Name == "" {
				continue
			}
			if _, ok := seen[membership.Name]; ok {
				continue
			}
			seen[membership.Name] = struct{}{}
			spec, ok := cfg.Tools[membership.Name]
			if !ok {
				continue
			}
			for _, install := range discoveryScopeInstallSpecs(spec) {
				add(spec.ToToolEntry(membership.Name, install))
			}
		}
	}
	return scope
}

type refreshLookupMaps struct {
	multiMaps       map[string]map[string]provider.InstalledEntry
	installedMaps   map[string]map[string]string
	metadataMaps    map[string]map[string]provider.InstalledMetadata
	concreteForBulk map[string]string
}

func (a *App) ensureProviderBulkSnapshot(ctx context.Context, providerName string, maps *refreshLookupMaps) {
	if maps == nil || providerName == "" {
		return
	}
	if maps.installedMaps != nil {
		if _, ok := maps.installedMaps[providerName]; ok {
			return
		}
	}
	if maps.multiMaps != nil {
		if _, ok := maps.multiMaps[providerName]; ok {
			return
		}
	}
	p, ok := a.registry.Get(providerName)
	if !ok {
		return
	}
	if maps.multiMaps == nil {
		maps.multiMaps = make(map[string]map[string]provider.InstalledEntry)
	}
	if maps.installedMaps == nil {
		maps.installedMaps = make(map[string]map[string]string)
	}
	if maps.metadataMaps == nil {
		maps.metadataMaps = make(map[string]map[string]provider.InstalledMetadata)
	}
	if maps.concreteForBulk == nil {
		maps.concreteForBulk = make(map[string]string)
	}
	scan, err := provider.ProbeBulkInstalled(ctx, p)
	switch scan.Kind {
	case provider.BulkInstalledByManager:
		if err == nil {
			maps.multiMaps[providerName] = scan.ByManager
		}
	case provider.BulkInstalledMetadata:
		if err == nil {
			maps.metadataMaps[providerName] = scan.Metadata
			maps.installedMaps[providerName] = installedMapFromMetadata(scan.Metadata)
			maps.concreteForBulk[providerName] = resolvedProviderConcreteName(ctx, p)
		}
	case provider.BulkInstalledSimple:
		if err == nil {
			maps.installedMaps[providerName] = scan.Installed
			maps.concreteForBulk[providerName] = resolvedProviderConcreteName(ctx, p)
		}
	}
}

func (a *App) ensureAlternateProviderBulkSnapshots(ctx context.Context, cfg *config.RootConfig, t config.ToolEntry, maps *refreshLookupMaps) {
	if cfg == nil {
		return
	}
	spec, ok := cfg.Tools[t.Name]
	if !ok {
		return
	}
	for _, candidate := range discoveryScopeInstallSpecs(spec) {
		alt := strings.TrimSpace(candidate.Provider)
		if alt == "" || alt == t.Provider {
			continue
		}
		a.ensureProviderBulkSnapshot(ctx, alt, maps)
	}
}

func (a *App) resolveInstalledBulkLookup(
	ctx context.Context,
	cfg *config.RootConfig,
	t config.ToolEntry,
	keys []string,
	routeMap map[string]string,
	routeInstalledWith string,
	maps *refreshLookupMaps,
) (version string, installed bool, installedWith string) {
	version, installed = provider.LookupString(routeMap, keys)
	installedWith = routeInstalledWith
	if installed {
		return version, true, installedWith
	}
	a.ensureAlternateProviderBulkSnapshots(ctx, cfg, t, maps)
	if altVer, altInstalled, altWith := a.lookupAlternateConfiguredInstall(cfg, t, keys, maps.multiMaps, maps.installedMaps, maps.concreteForBulk); altInstalled {
		return altVer, true, altWith
	}
	return version, false, installedWith
}

func useCachedOwnerUpsert(upsert *database.ToolCache, metadataUpdate *database.MetadataUpdate, upserts *[]*database.ToolCache, metadataUpdates *[]database.MetadataUpdate) bool {
	if upsert == nil {
		return true
	}
	if !upsert.Installed {
		return false
	}
	*upserts = append(*upserts, upsert)
	if metadataUpdate != nil {
		*metadataUpdates = append(*metadataUpdates, *metadataUpdate)
	}
	return true
}

func (a *App) lookupAlternateConfiguredInstall(
	cfg *config.RootConfig,
	t config.ToolEntry,
	keys []string,
	multiMaps map[string]map[string]provider.InstalledEntry,
	installedMaps map[string]map[string]string,
	concreteForBulk map[string]string,
) (version string, installed bool, installedWith string) {
	if cfg == nil {
		return "", false, ""
	}
	spec, ok := cfg.Tools[t.Name]
	if !ok {
		return "", false, ""
	}
	routeProvider := t.Provider
	for _, candidate := range discoveryScopeInstallSpecs(spec) {
		alt := strings.TrimSpace(candidate.Provider)
		if alt == "" || alt == routeProvider {
			continue
		}
		if mm, ok := multiMaps[alt]; ok {
			entry := provider.LookupInstalledEntry(mm, keys)
			if entry.ConcreteManager != "" {
				return entry.Version, true, entry.ConcreteManager
			}
		}
		if m, ok := installedMaps[alt]; ok {
			if ver, found := provider.LookupString(m, keys); found {
				with := alt
				if concrete := concreteForBulk[alt]; concrete != "" {
					with = concrete
				}
				return ver, true, with
			}
		}
	}
	return "", false, ""
}

func discoveryScopeInstallSpecs(spec config.ToolSpec) []config.ToolInstallSpec {
	hostname := currentHostname()
	if install, ok := spec.Hosts[hostname]; ok {
		return []config.ToolInstallSpec{install}
	}
	if short := shortHostname(hostname); short != hostname {
		if install, ok := spec.Hosts[short]; ok {
			return []config.ToolInstallSpec{install}
		}
	}
	if len(spec.Providers) > 0 {
		return append([]config.ToolInstallSpec(nil), spec.Providers...)
	}
	specs := make([]config.ToolInstallSpec, 0, 1+len(spec.Variants))
	specs = append(specs, spec.DefaultInstallSpec())
	specs = append(specs, spec.Variants...)
	return specs
}

func (a *App) discoveryProviderAllowed(p provider.Provider, ecosystemMap map[string]string, scope discoveryScope) bool {
	name := p.Name()
	if scope.hasProvider(name) {
		return true
	}
	if scope.hasManager(name) {
		if ecosystem, ok := a.registry.EcosystemFor(name); ok && ecosystem != name && scope.hasProvider(ecosystem) {
			if _, ecosystemRegistered := a.registry.Get(ecosystem); ecosystemRegistered && !a.registry.ImportSkipsProvider(ecosystem) {
				return false
			}
		}
		return true
	}
	if ecosystem, ok := ecosystemMap[name]; ok {
		return scope.hasProvider(ecosystem) && scope.hasManager(name)
	}
	return false
}

func discoveryConfigProvider(providerName string, ecosystemMap map[string]string, scope discoveryScope) string {
	if ecosystem, ok := ecosystemMap[providerName]; ok && scope.hasProvider(ecosystem) {
		return ecosystem
	}
	return providerName
}

func filterDiscoveredByScope(discovered []*database.ToolCache, scope discoveryScope) []*database.ToolCache {
	if scope.empty() {
		return nil
	}
	out := discovered[:0]
	for _, tool := range discovered {
		if tool == nil {
			continue
		}
		if discoveredToolAllowed(tool, scope) {
			out = append(out, tool)
		}
	}
	return out
}

func filterToolCachesByConfigAndScope(tools []*database.ToolCache, configured map[string]struct{}, authoritative bool, scope discoveryScope) []*database.ToolCache {
	if len(tools) == 0 {
		return tools
	}
	out := tools[:0]
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		key := NewToolKey(tool.Name, tool.Provider, tool.Package).String()
		if tool.Tracked && authoritative {
			if _, ok := configured[key]; ok {
				out = append(out, tool)
			}
			continue
		}
		if tool.Tracked || discoveredToolAllowed(tool, scope) {
			out = append(out, tool)
		}
	}
	return out
}

func discoveredToolAllowed(tool *database.ToolCache, scope discoveryScope) bool {
	return tool != nil && tool.Installed && scope.allowsDiscovered(tool.Provider, tool.InstalledWith)
}

// Node (bun/pnpm/npm) and Python (uv/pip) share a store; system PMs do not.
func ecosystemSharesGlobalStore(ecosystem string) bool {
	return ecosystem == provider.EcosystemNode || ecosystem == provider.EcosystemPython
}

// Shared-store managers each report the same global package, which would otherwise yield one row per manager.
func collapseSharedStoreDuplicates(discovered []database.DiscoveredUpsert, effective map[string]string) []database.DiscoveredUpsert {
	out := discovered[:0]
	idx := make(map[string]int)
	for _, d := range discovered {
		ecosystem, ok := provider.BuiltinEcosystemFor(d.InstalledWith)
		if !ok || !ecosystemSharesGlobalStore(ecosystem) {
			out = append(out, d)
			continue
		}
		key := ecosystem + "\x00" + strings.ToLower(d.Name)
		if i, seen := idx[key]; seen {
			if d.InstalledWith == effective[ecosystem] && out[i].InstalledWith != effective[ecosystem] {
				out[i] = d
			}
			continue
		}
		idx[key] = len(out)
		out = append(out, d)
	}
	return out
}

func reverseEcosystemProviders(ecosystemProviders map[string]string) map[string]string {
	rev := make(map[string]string, len(ecosystemProviders))
	for ecosystem, concrete := range ecosystemProviders {
		if concrete != "" {
			rev[concrete] = ecosystem
		}
	}
	return rev
}

func (a *App) reconcileResolvedTools(ctx context.Context, tools []config.ToolEntry) error {
	desired := make([]*database.ToolCache, 0, len(tools))
	for _, t := range tools {
		desired = append(desired, &database.ToolCache{
			Name:     t.Name,
			Provider: t.Provider,
			Package:  t.EffectivePackage(),
		})
	}
	if err := a.readDB().ReconcileTracked(ctx, desired); err != nil {
		return fmt.Errorf("reconciling tracked tools: %w", err)
	}
	return nil
}

func (a *App) Providers(ctx context.Context) ([]ProviderInfo, error) {
	var infos []ProviderInfo
	for _, p := range a.registry.All() {
		avail, err := p.Available(ctx)
		if err != nil {
			avail = false // treat availability-check error as unavailable; continue
		}
		infos = append(infos, ProviderInfo{
			Name:        p.Name(),
			Description: p.Description(),
			Available:   avail,
		})
	}
	return infos, nil
}

type ProviderInfo struct {
	Name        string
	Description string
	Available   bool
}

// Search — Best-effort: per-provider errors are joined, and partial results are still returned.
func (a *App) Search(ctx context.Context, query, providerFilter string) ([]provider.SearchResult, error) {
	var results []provider.SearchResult
	var errs []error
	for _, p := range a.registry.All() {
		if !a.searchProviderMatches(p.Name(), providerFilter) {
			continue
		}
		s, ok := p.(provider.Searcher)
		if !ok {
			continue
		}
		res, err := s.Search(ctx, query)
		if err != nil {
			errs = append(errs, fmt.Errorf("searching %s: %w", p.Name(), err))
			continue
		}
		for _, r := range res {
			resultProvider := r.Provider
			if resultProvider == "" {
				resultProvider = p.Name()
			}
			if r.SourceProvider == "" {
				r.SourceProvider = resultProvider
			}
			r.Provider = a.searchResultConfigProvider(resultProvider)
			results = append(results, r)
		}
	}
	if err := a.cacheSearchMetadata(ctx, results); err != nil {
		errs = append(errs, err)
	}
	return results, errors.Join(errs...)
}

// SearchForDisplay — The raw Search results keep every concrete provider as an install candidate.
func (a *App) SearchForDisplay(ctx context.Context, query, providerFilter string) ([]provider.SearchResult, error) {
	results, err := a.Search(ctx, query, providerFilter)
	settings := config.Settings{}
	if cfg, cfgErr := a.loadConfig(); cfgErr == nil {
		settings = a.effectiveSettings(cfg)
	}
	results = dedupSearchResults(results, a.providerPriorityRank(defaultProviderPriority(settings)), query)
	return results, err
}

// rank maps a provider to its priority index; providers absent from rank sort last.
func dedupSearchResults(results []provider.SearchResult, rank map[string]int, query string) []provider.SearchResult {
	rankOf := func(name string) int {
		if r, ok := rank[name]; ok {
			return r
		}
		return len(rank) + 1000
	}
	type key struct{ name, scope string }
	index := make(map[key]int, len(results))
	out := make([]provider.SearchResult, 0, len(results))
	for _, r := range results {
		// Only shared-store providers collapse; system PMs are genuinely distinct install targets.
		eco := ToolProviderEcosystem(r.Provider)
		scope := r.Provider
		if ecosystemSharesGlobalStore(eco) {
			scope = eco
		}
		k := key{strings.ToLower(r.Name), scope}
		if idx, ok := index[k]; ok {
			if rankOf(r.Provider) < rankOf(out[idx].Provider) {
				out[idx] = r
			}
			continue
		}
		index[k] = len(out)
		out = append(out, r)
	}
	// Relevance first so typing a tool name surfaces it regardless of which manager would install it.
	sort.SliceStable(out, func(i, j int) bool {
		si, sj := queryMatchScore(out[i].Name, query), queryMatchScore(out[j].Name, query)
		if si != sj {
			return si > sj
		}
		ri, rj := rankOf(out[i].Provider), rankOf(out[j].Provider)
		if ri != rj {
			return ri < rj
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// 3 exact, 2 prefix, 1 substring, 0 none.
func queryMatchScore(name, query string) int {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return 0
	}
	n := strings.ToLower(name)
	switch {
	case n == q:
		return 3
	case strings.HasPrefix(n, q):
		return 2
	case strings.Contains(n, q):
		return 1
	default:
		return 0
	}
}

func SearchResultDisplayProvider(r provider.SearchResult) string {
	if r.SourceProvider == "" || r.SourceProvider == r.Provider {
		return ""
	}
	if ToolProviderEcosystem(r.SourceProvider) != ToolProviderEcosystem(r.Provider) {
		return ""
	}
	return r.SourceProvider
}

func ClassifyProviderMatch(logicalName string, spec config.ToolSpec, result provider.SearchResult) ProviderMatchConfidence {
	resultName := strings.TrimSpace(result.Name)
	if resultName == "" || strings.TrimSpace(result.Provider) == "" {
		return ProviderMatchNone
	}
	// A source/git match is decisive on any provider, including language ecosystems.
	if sameGitHubSource(spec.Git, result.Source) {
		return ProviderMatchHigh
	}
	// Language ecosystems carry same-named squatters, so a bare name match is weak there without a source match.
	if providerNameMatch(logicalName, spec, resultName) && isNativeProvider(result.Provider) {
		return ProviderMatchHigh
	}
	return ProviderMatchWeak
}

func providerNameMatch(logicalName string, spec config.ToolSpec, resultName string) bool {
	if samePackageName(resultName, logicalName) {
		return true
	}
	for _, install := range spec.Providers {
		if samePackageName(resultName, install.EffectivePackage(logicalName)) {
			return true
		}
	}
	if len(spec.Providers) == 0 && samePackageName(resultName, spec.DefaultInstallSpec().EffectivePackage(logicalName)) {
		return true
	}
	return false
}

func isNativeProvider(name string) bool {
	return ToolProviderEcosystem(name) == provider.EcosystemSystem
}

func normalizedSourceRepoKey(s provider.SourceMetadata) string {
	if s.Type != provider.SourceTypeGitHub {
		return ""
	}
	owner := strings.ToLower(strings.TrimSpace(s.Owner))
	repo := strings.ToLower(strings.TrimSpace(s.Repo))
	if (owner == "" || repo == "") && strings.TrimSpace(s.URL) != "" {
		if o, r, err := parseGitHubRepo(s.URL); err == nil {
			owner, repo = strings.ToLower(o), strings.ToLower(r)
		}
	}
	if owner == "" || repo == "" {
		return ""
	}
	return owner + "/" + repo
}

// Two providers agreeing on a repo is strong evidence; only name-matching results are eligible, so monorepo siblings are not promoted.
func promoteSourceConsensus(logicalName string, spec config.ToolSpec, matches []ProviderMatch) {
	counts := make(map[string]int, len(matches))
	for _, m := range matches {
		if !providerNameMatch(logicalName, spec, strings.TrimSpace(m.Name)) {
			continue
		}
		if key := normalizedSourceRepoKey(m.Source); key != "" {
			counts[key]++
		}
	}
	for i := range matches {
		if matches[i].Confidence == ProviderMatchHigh {
			continue
		}
		if !providerNameMatch(logicalName, spec, strings.TrimSpace(matches[i].Name)) {
			continue
		}
		if key := normalizedSourceRepoKey(matches[i].Source); key != "" && counts[key] >= 2 {
			matches[i].Confidence = ProviderMatchHigh
		}
	}
}

// Reflects only what is already installed locally: no search or install is performed.
func captureEmptyProviderInstalls(
	ctx context.Context,
	a *App,
	tools []config.ToolEntry,
	available []provider.Provider,
	multiMaps map[string]map[string]provider.InstalledEntry,
	installedMaps map[string]map[string]string,
	metadataMaps map[string]map[string]provider.InstalledMetadata,
	concreteForBulk map[string]string,
) (map[string][]config.ToolInstallSpec, map[string]string) {
	captured := make(map[string][]config.ToolInstallSpec)
	gitByTool := make(map[string]string)
	for _, t := range tools {
		if a.operationProviderName(t) != "" {
			continue
		}
		keys := toolEntryLookupKeys(t)
		seen := make(map[string]bool)
		add := func(concrete string, src provider.SourceMetadata) {
			concrete = config.NormalizeConcreteProvider(concrete)
			if concrete == "" || seen[concrete] {
				return
			}
			seen[concrete] = true
			captured[t.Name] = append(captured[t.Name], config.ToolInstallSpec{Provider: concrete, Package: t.Name})
			if gitByTool[t.Name] == "" {
				if g := gitURLFromSourceMetadata(src); g != "" {
					gitByTool[t.Name] = g
				}
			}
		}
		for _, mm := range multiMaps {
			if entry := provider.LookupInstalledEntry(mm, keys); entry.ConcreteManager != "" {
				add(entry.ConcreteManager, provider.SourceMetadata{})
			}
		}
		for provName, mmeta := range metadataMaps {
			if md, ok := provider.LookupInstalledMetadata(mmeta, keys); ok {
				add(concreteForBulk[provName], md.Source)
			}
		}
		for provName, m := range installedMaps {
			if _, isMeta := metadataMaps[provName]; isMeta {
				continue
			}
			if _, isMulti := multiMaps[provName]; isMulti {
				continue
			}
			if _, ok := provider.LookupString(m, keys); ok {
				add(concreteForBulk[provName], provider.SourceMetadata{})
			}
		}
		// A tool installed as a dependency, cask or tap is absent from the bulk maps, so probe per-tool.
		if len(seen) == 0 {
			for _, p := range available {
				installed, _, err := a.isInstalledWithEntry(ctx, p, p.Name(), t)
				if err != nil || !installed {
					continue
				}
				add(installedWithForOperation(ctx, p, p.Name(), ""), provider.SourceMetadata{})
			}
		}
	}
	return captured, gitByTool
}

// A nil result means no config change.
func (a *App) persistCapturedProviders(cfg *config.RootConfig, captured map[string][]config.ToolInstallSpec, gitByTool map[string]string) (*config.RootConfig, error) {
	if len(captured) == 0 {
		return nil, nil
	}
	settings, _ := a.LoadSettings()
	rank := a.providerPriorityRank(defaultProviderPriority(settings))
	changed := false
	for name, provs := range captured {
		spec, ok := cfg.Tools[name]
		if !ok || len(spec.Providers) > 0 {
			continue
		}
		sortByProviderRank(provs, rank)
		spec.Providers = provs
		if spec.Git == "" && gitByTool[name] != "" {
			spec.Git = gitByTool[name]
		}
		cfg.Tools[name] = spec
		changed = true
	}
	if !changed {
		return nil, nil
	}
	toolsRaw, err := json.Marshal(cfg.Tools)
	if err != nil {
		return nil, fmt.Errorf("encoding captured providers: %w", err)
	}
	if err := config.PatchRawRouted(a.ConfigPath, map[string]json.RawMessage{"tools": toolsRaw}); err != nil {
		return nil, fmt.Errorf("persisting captured providers: %w", err)
	}
	return cfg, nil
}

func sortByProviderRank(provs []config.ToolInstallSpec, rank map[string]int) {
	sort.SliceStable(provs, func(i, j int) bool {
		ri, iOK := rank[provs[i].Provider]
		rj, jOK := rank[provs[j].Provider]
		if iOK != jOK {
			return iOK
		}
		if ri != rj {
			return ri < rj
		}
		return provs[i].Provider < provs[j].Provider
	})
}

// Bounds how long a no-match verdict is cached so sync skips re-searching the same tool.
const providerSearchMissTTL = 6 * time.Hour

// Cached as a package_availability row with an empty provider (the search sentinel).
func (a *App) recentProviderSearchMiss(ctx context.Context, name string) bool {
	db := a.readDB()
	if db == nil {
		return false
	}
	cached, err := db.GetPackageAvailability(ctx, name, "", name)
	if err != nil || cached == nil || cached.Available {
		return false
	}
	return time.Since(cached.CheckedAt) < providerSearchMissTTL
}

func (a *App) recordProviderSearchMiss(ctx context.Context, name string) {
	db := a.readDB()
	if db == nil {
		return
	}
	if err := db.UpsertPackageAvailability(ctx, database.PackageAvailability{
		Name:      name,
		Provider:  "",
		Package:   name,
		Available: false,
		Reason:    "no high-confidence provider match",
		CheckedAt: time.Now().UTC(),
	}); err != nil {
		return // best-effort cache; a failed write just re-searches next sync
	}
}

func (a *App) clearProviderSearchMiss(ctx context.Context, name string) {
	db := a.readDB()
	if db == nil {
		return
	}
	if err := db.UpsertPackageAvailability(ctx, database.PackageAvailability{
		Name:      name,
		Provider:  "",
		Package:   name,
		Available: true,
		CheckedAt: time.Now().UTC(),
	}); err != nil {
		return // best-effort
	}
}

func (a *App) ProviderMatches(ctx context.Context, logicalName string, spec config.ToolSpec, providerFilter string) ([]ProviderMatch, error) {
	results, err := a.Search(ctx, logicalName, providerFilter)
	settings, settingsErr := a.LoadSettings()
	disabled := disabledProviderSet(settings.DisabledProviders)
	matches := make([]ProviderMatch, 0, len(results))
	for _, result := range results {
		resultProvider := strings.TrimSpace(result.Provider)
		if disabled[resultProvider] || !a.providerMatchInstallCandidate(resultProvider) {
			continue
		}
		confidence := ClassifyProviderMatch(logicalName, spec, result)
		if confidence == ProviderMatchNone {
			continue
		}
		matches = append(matches, ProviderMatch{SearchResult: result, Confidence: confidence})
	}
	promoteSourceConsensus(logicalName, spec, matches)
	rank := a.providerPriorityRank(defaultProviderPriority(settings))
	sort.SliceStable(matches, func(i, j int) bool {
		leftHigh := matches[i].Confidence == ProviderMatchHigh
		rightHigh := matches[j].Confidence == ProviderMatchHigh
		if leftHigh != rightHigh {
			return leftHigh
		}
		leftRank, leftOK := rank[matches[i].Provider]
		rightRank, rightOK := rank[matches[j].Provider]
		if leftOK != rightOK {
			return leftOK
		}
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		if matches[i].Provider != matches[j].Provider {
			return matches[i].Provider < matches[j].Provider
		}
		return matches[i].Name < matches[j].Name
	})
	return matches, errors.Join(err, settingsErr)
}

func (a *App) providerMatchInstallCandidate(providerName string) bool {
	if providerName == "" || a == nil || a.registry == nil {
		return providerName != ""
	}
	meta, ok := a.registry.Metadata(providerName)
	if !ok {
		return true
	}
	return meta.Kind != provider.ProviderKindEcosystem
}

func (a *App) AddHighConfidenceProviderMatches(ctx context.Context, name, providerFilter string) (*ProviderMatchInstallResult, error) {
	return a.AddProviderMatches(ctx, name, providerFilter, ProviderMatchOptions{})
}

func (a *App) AddProviderMatches(ctx context.Context, name, providerFilter string, opts ProviderMatchOptions) (*ProviderMatchInstallResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	spec, ok := cfg.Tools[name]
	if !ok {
		return nil, fmt.Errorf("%w: tool %q not found", ErrProviderDiscoveryNotConfigured, name)
	}
	if len(spec.Providers) > 0 || spec.Fallback != nil {
		return nil, fmt.Errorf("%w: tool %q already has provider candidates", ErrProviderDiscoveryAlreadyConfigured, name)
	}
	matches, matchErr := a.ProviderMatches(ctx, name, spec, providerFilter)
	result := &ProviderMatchInstallResult{Matches: matches, SearchErr: matchErr}
	for _, match := range matches {
		if match.Confidence != ProviderMatchHigh {
			continue
		}
		result.Added = append(result.Added, config.ToolInstallSpec{
			Provider: match.Provider,
			Package:  match.Name,
			Options:  cloneOptionMap(match.Options),
		})
	}
	if len(result.Added) == 0 && opts.AllowWeak {
		for _, match := range matches {
			if match.Confidence != ProviderMatchWeak {
				continue
			}
			result.Added = append(result.Added, config.ToolInstallSpec{
				Provider: match.Provider,
				Package:  match.Name,
				Options:  cloneOptionMap(match.Options),
			})
			break
		}
	}
	if len(result.Added) == 0 {
		return result, fmt.Errorf("%w for %q", ErrProviderDiscoveryNoHighConfidence, name)
	}
	result.Installed = result.Added[0]
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		spec, ok := cfg.Tools[name]
		if !ok {
			return fmt.Errorf("tool %q not found", name)
		}
		for i, entry := range result.Added {
			if i == 0 {
				setDefaultToolProviderCandidate(&spec, entry)
				continue
			}
			setToolProviderCandidate(&spec, entry)
		}
		cfg.Tools[name] = spec
		return nil
	}); err != nil {
		return result, err
	}
	return result, nil
}

func (a *App) InstallHighConfidenceProviderMatches(ctx context.Context, name, providerFilter string) (*ProviderMatchInstallResult, error) {
	return a.InstallProviderMatches(ctx, name, providerFilter, ProviderMatchOptions{})
}

func (a *App) InstallProviderMatches(ctx context.Context, name, providerFilter string, opts ProviderMatchOptions) (*ProviderMatchInstallResult, error) {
	result, err := a.AddProviderMatches(ctx, name, providerFilter, opts)
	if err != nil {
		return result, err
	}
	if err := a.Install(ctx, name, result.Installed.Provider); err != nil {
		return result, err
	}
	return result, nil
}

func (a *App) providerPriorityRank(priority []string) map[string]int {
	if len(priority) == 0 {
		if a != nil && a.registry != nil {
			priority = a.registry.DefaultInstallProviderNames()
		}
		if len(priority) == 0 {
			priority = provider.BuiltinDefaultInstallProviderNames()
		}
	}
	rank := make(map[string]int, len(priority))
	for i, name := range priority {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := rank[name]; !ok {
			rank[name] = i
		}
	}
	return rank
}

func samePackageName(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

func sameGitHubSource(toolGit string, source provider.SourceMetadata) bool {
	if strings.TrimSpace(toolGit) == "" || source.Type != provider.SourceTypeGitHub {
		return false
	}
	toolOwner, toolRepo, err := parseGitHubRepo(toolGit)
	if err != nil {
		return false
	}
	sourceOwner := strings.TrimSpace(source.Owner)
	sourceRepo := strings.TrimSpace(source.Repo)
	if sourceOwner == "" || sourceRepo == "" {
		var err error
		sourceOwner, sourceRepo, err = parseGitHubRepo(source.URL)
		if err != nil {
			return false
		}
	}
	return strings.EqualFold(toolOwner, sourceOwner) &&
		strings.EqualFold(toolRepo, sourceRepo)
}

func (a *App) cacheSearchMetadata(ctx context.Context, results []provider.SearchResult) error {
	updates := make([]database.MetadataUpdate, 0, len(results))
	for _, r := range results {
		name := strings.TrimSpace(r.Name)
		providerName := strings.TrimSpace(r.Provider)
		if name == "" || providerName == "" {
			continue
		}
		u := database.MetadataUpdate{
			Name:        name,
			Provider:    providerName,
			Package:     name,
			Version:     strings.TrimSpace(r.Version),
			Description: strings.TrimSpace(r.Description),
			SourceType:  strings.TrimSpace(r.Source.Type),
			SourceOwner: strings.TrimSpace(r.Source.Owner),
			SourceRepo:  strings.TrimSpace(r.Source.Repo),
			SourceURL:   strings.TrimSpace(r.Source.URL),
		}
		if r.Privilege.RequiresPrivilege() {
			u.Privilege = string(r.Privilege.Requirement)
			u.PrivilegeReason = strings.TrimSpace(r.Privilege.Reason)
		}
		if u.Version == "" && u.Description == "" && u.Privilege == "" && u.SourceType == "" {
			continue
		}
		updates = append(updates, u)
	}
	if err := a.readDB().UpsertMetadataBatch(ctx, updates); err != nil {
		return fmt.Errorf("caching search metadata: %w", err)
	}
	return nil
}

func (a *App) searchProviderMatches(providerName, filter string) bool {
	if filter == "" || providerName == filter {
		return true
	}
	if meta, ok := a.registry.Metadata(providerName); ok && meta.Ecosystem == filter {
		return true
	}
	ecosystem, ok := a.providerEcosystem(providerName)
	return ok && ecosystem == filter
}

func (a *App) searchResultConfigProvider(providerName string) string {
	return providerName
}

func (a *App) providerEcosystem(name string) (string, bool) {
	if a != nil && a.registry != nil {
		if ecosystem, ok := a.registry.EcosystemFor(name); ok {
			return ecosystem, true
		}
	}
	return provider.BuiltinEcosystemFor(name)
}

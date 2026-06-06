package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/profile"
	"github.com/lkshrk/omni/internal/provider"
)

// ─── List / Search / Providers ───────────────────────────────────────────────

type ToolListState string

const (
	ToolStateInstalled       ToolListState = "installed"
	ToolStateMissing         ToolListState = "missing"
	ToolStateOutdated        ToolListState = "outdated"
	ToolStateQuarantined     ToolListState = "quarantined"
	ToolStateBlockedMetadata ToolListState = "blocked-metadata"
	ToolStateIgnored         ToolListState = "ignored"
	ToolStateUnclaimed       ToolListState = "unclaimed"
	ToolStateOutOfSync       ToolListState = "out-of-sync"
	ToolStateFailed          ToolListState = "failed"
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
	cfg, cfgErr := a.loadConfig()
	ecosystemProviders := map[string]string(nil)
	if cfgErr == nil {
		ecosystemProviders = a.ResolvedEcosystemProviders(ctx)
	}
	tools, err := a.listToolsFromConfig(ctx, cfg, providerFilter, ecosystemProviders)
	if err != nil {
		return nil, err
	}
	if cfgErr != nil {
		return filterIgnoredToolCaches(tools, nil), nil
	}
	a.annotateUpdateQuarantine(ctx, cfg, tools)
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
	resolved, _ := a.resolveTools(ctx, cfg, cfg.Groups)
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
	tools, err := a.listToolsFromConfig(ctx, cfg, "", resolvedProviders)
	if err != nil {
		return nil, err
	}
	a.annotateUpdateQuarantine(ctx, cfg, tools)
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
	resolved, _ := a.resolveTools(ctx, cfg, groups)
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
		if _, ok := a.registry.Get(t.InstalledWith); !ok {
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

func (a *App) refreshWithCachedOwner(ctx context.Context, t config.ToolEntry, keys []string, owner string, installedMaps map[string]map[string]string, metadataMaps map[string]map[string]provider.InstalledMetadata) (*database.ToolCache, bool) {
	ownerProv, ok := a.registry.Get(owner)
	if !ok {
		return nil, false
	}
	available, err := ownerProv.Available(ctx)
	if err != nil || !available {
		return nil, true
	}
	var (
		installed bool
		ver       string
	)
	if metadataMaps != nil {
		if m, ok := metadataMaps[owner]; ok {
			entry, installed := provider.LookupInstalledMetadata(m, keys)
			return installedOwnerUpsertWithMetadata(t, owner, installed, entry), true
		}
	}
	if installedMaps != nil {
		if m, ok := installedMaps[owner]; ok {
			ver, installed = provider.LookupString(m, keys)
			return installedOwnerUpsert(t, owner, installed, ver), true
		}
	}
	if mbc, ok := ownerProv.(provider.MetadataBulkChecker); ok {
		if m, err := mbc.InstalledMetadataMap(ctx); err == nil {
			if metadataMaps != nil {
				metadataMaps[owner] = m
			}
			entry, installed := provider.LookupInstalledMetadata(m, keys)
			return installedOwnerUpsertWithMetadata(t, owner, installed, entry), true
		}
	}
	if bc, ok := ownerProv.(provider.BulkChecker); ok {
		if m, err := bc.InstalledMap(ctx); err == nil {
			if installedMaps != nil {
				installedMaps[owner] = m
			}
			ver, installed = provider.LookupString(m, keys)
			return installedOwnerUpsert(t, owner, installed, ver), true
		}
	}
	tool := provider.Tool{Name: t.Name, Provider: owner, Package: t.EffectivePackage(), Options: t.Options}
	installed, ver, err = ownerProv.IsInstalled(ctx, tool)
	if err != nil {
		return nil, true
	}
	return installedOwnerUpsert(t, owner, installed, ver), true
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
	if strings.TrimSpace(entry.Source.Type) == "" {
		return database.MetadataUpdate{}, false
	}
	return database.MetadataUpdate{
		Name:        t.Name,
		Provider:    t.Provider,
		Package:     t.EffectivePackage(),
		SourceType:  strings.TrimSpace(entry.Source.Type),
		SourceOwner: strings.TrimSpace(entry.Source.Owner),
		SourceRepo:  strings.TrimSpace(entry.Source.Repo),
		SourceURL:   strings.TrimSpace(entry.Source.URL),
	}, true
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
	classification := ClassifyToolView(t, ToolClassificationContext{
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
	case ToolSyncWrongProvider:
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

func toolStateMatches(state, filter ToolListState) bool {
	if state == filter {
		return true
	}
	return filter == ToolStateOutOfSync && (state == ToolStateMissing || state == ToolStateUnclaimed)
}

// RefreshInstalled updates the DB install status for every configured tool
// using the provider's current state. Does not install missing tools.
// Best-effort: errors on individual providers or tools are silently skipped.
// The optional progress callback is called with provider scan progress
// (e.g. "Scanning system/brew… (1/3)"); pass nil to omit progress.
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

	// Resolve availability once in parallel; reuse the result for both the
	// scanTotal count and the main scan loop below to avoid two Available
	// passes per provider.
	stop = profile.Start("app.refresh.installed.available_providers")
	available := a.availableProviders(ctx)
	stop()
	scanProgress := a.newRefreshInstalledScanProgress(ctx, progress, tools, available)

	// Build installed map per provider using bulk check where available.
	// multiMaps: provider → name → InstalledEntry (per-tool manager attribution).
	// installedMaps: provider → name → version (single-manager bulk path).
	// metadataMaps: provider → name → version plus provider metadata.
	// concreteForBulk: provider → concrete backend for BulkChecker providers.
	multiMaps := make(map[string]map[string]provider.InstalledEntry)
	installedMaps := make(map[string]map[string]string)
	metadataMaps := make(map[string]map[string]provider.InstalledMetadata)
	concreteForBulk := make(map[string]string)
	// Iterate the precomputed available set so we don't call Available a
	// second time. Per-provider scan failures are skipped silently so a
	// single failing backend doesn't block discovery for others.
	stop = profile.Start("app.refresh.installed.bulk_maps")
	for _, p := range available {
		scanProgress.emitProvider(p.Name())
		// MultiManagerBulkChecker takes priority: probes all backends for per-tool attribution.
		if mbc, ok := p.(provider.MultiManagerBulkChecker); ok {
			entries, err := mbc.InstalledByManager(ctx)
			if err != nil {
				continue
			}
			multiMaps[p.Name()] = entries
			continue
		}
		var (
			m   map[string]string
			err error
		)
		if mbc, ok := p.(provider.MetadataBulkChecker); ok {
			metadata, err := mbc.InstalledMetadataMap(ctx)
			if err != nil {
				continue
			}
			metadataMaps[p.Name()] = metadata
			m = installedMapFromMetadata(metadata)
		} else {
			bc, ok := p.(provider.BulkChecker)
			if !ok {
				continue
			}
			m, err = bc.InstalledMap(ctx)
			if err != nil {
				continue
			}
		}
		// Resolve concrete backend for InstalledWith. Ecosystem providers (e.g. node)
		// implement ConcreteResolver; concrete providers are their own backend.
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
		installedMaps[p.Name()] = m
		concreteForBulk[p.Name()] = installedWith
	}
	stop()

	stop = profile.Start("app.refresh.installed.resolve_installed")
	upserts := make([]*database.ToolCache, 0, len(tools))
	metadataUpdates := make([]database.MetadataUpdate, 0)
	for _, t := range tools {
		keys := toolEntryLookupKeys(t)

		opProvider := a.operationProviderName(t)
		if owner := cachedOwners[resolvedToolKey(t)]; owner != "" && owner != opProvider && t.InstallWith == "" {
			if upsert, handled := a.refreshWithCachedOwner(ctx, t, keys, owner, installedMaps, metadataMaps); handled {
				if upsert == nil {
					continue
				}
				upserts = append(upserts, upsert)
				continue
			}
		}
		if mm, hasMulti := multiMaps[opProvider]; hasMulti && t.InstallWith == "" {
			// Multi-manager path: per-tool InstalledWith from the manager that owns it.
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

		if m, hasBulk := installedMaps[opProvider]; hasBulk && !(t.InstallWith != "" && opProvider == t.Provider) {
			// Fast path: bulk map lookup (concrete providers with BulkChecker).
			ver, installed := provider.LookupString(m, keys)
			upsert := &database.ToolCache{
				Name:          t.Name,
				Provider:      t.Provider,
				Package:       t.EffectivePackage(),
				Installed:     installed,
				InstalledWith: concreteForBulk[opProvider],
				Version:       sql.NullString{String: ver, Valid: ver != ""},
				LastChecked:   time.Now(),
			}
			if metadata, ok := provider.LookupInstalledMetadata(metadataMaps[opProvider], keys); ok {
				applyPrivilegeMetadata(upsert, metadata.Privilege)
				if update, ok := installedSourceMetadataUpdate(t, metadata); ok {
					metadataUpdates = append(metadataUpdates, update)
				}
			}
			upserts = append(upserts, upsert)
			continue
		}
		// Slow path: per-tool IsInstalled call.
		// Used for providers that do not implement BulkChecker.
		p, ok := a.registry.Get(opProvider)
		if !ok {
			continue
		}
		avail, err := p.Available(ctx)
		if err != nil {
			continue
		}
		if !avail {
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
	stop()
	writeCtx := context.WithoutCancel(ctx)
	stop = profile.Start("app.refresh.installed.write_reconcile")
	if err := a.readDB().UpsertBatch(writeCtx, upserts); err != nil {
		stop()
		return fmt.Errorf("upserting installed status: %w", err)
	}
	if err := a.readDB().UpsertMetadataBatch(writeCtx, metadataUpdates); err != nil {
		stop()
		return fmt.Errorf("upserting installed metadata: %w", err)
	}
	if err := a.reconcileResolvedTools(writeCtx, tools); err != nil {
		stop()
		return err
	}
	stop()
	return nil
}

type refreshInstalledScanProgress struct {
	progress         func(string)
	index            int
	total            int
	labelsByProvider map[string][]string
	emitted          map[string]bool
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

// RefreshProviderInstalled updates the DB install status for all configured tools
// belonging to a single named provider. It mirrors the inner logic of
// RefreshInstalled but scoped to one provider so callers can run providers in
// parallel with independent timeouts. Best-effort provider failures are skipped,
// but caller cancellation/deadline errors are returned so refresh state does not
// silently go stale.
func (a *App) RefreshProviderInstalled(ctx context.Context, provName string) error {
	return a.RefreshProviderInstalledWithProgress(ctx, provName, nil)
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

	// Collect only the tools that belong to this provider.
	var provTools []config.ToolEntry
	for _, t := range tools {
		if a.operationProviderName(t) == provName {
			provTools = append(provTools, t)
		}
	}
	if len(provTools) == 0 {
		return nil
	}

	p, ok := a.registry.Get(provName)
	if !ok {
		return nil
	}
	avail, err := p.Available(ctx)
	if err != nil {
		return refreshContextErr(ctx, err)
	}
	if !avail {
		return nil
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
		if err := a.readDB().UpsertBatch(writeCtx, upserts); err != nil {
			return fmt.Errorf("upserting installed status for %s: %w", provName, err)
		}
		if err := a.readDB().UpsertMetadataBatch(writeCtx, metadataUpdates); err != nil {
			return fmt.Errorf("upserting metadata for %s: %w", provName, err)
		}
		return nil
	}
	ownerInstalledMaps := make(map[string]map[string]string)
	ownerMetadataMaps := make(map[string]map[string]provider.InstalledMetadata)

	if mbc, ok := p.(provider.MultiManagerBulkChecker); ok {
		entries, err := mbc.InstalledByManager(ctx)
		if err != nil {
			return refreshContextErr(ctx, err)
		}
		for i, t := range provTools {
			emitTool(i, t)
			keys := toolEntryLookupKeys(t)
			if owner := cachedOwners[resolvedToolKey(t)]; owner != "" && owner != provName && t.InstallWith == "" {
				if upsert, handled := a.refreshWithCachedOwner(ctx, t, keys, owner, ownerInstalledMaps, ownerMetadataMaps); handled {
					if upsert == nil {
						continue
					}
					upserts = append(upserts, upsert)
					continue
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

	// BulkChecker path: single backend, bulk map lookup.
	// On error fall through to per-tool slow path so a partial failure (e.g. a
	// ecosystem provider whose delegate doesn't support bulk) doesn't skip all tools.
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
			}
			for i, t := range provTools {
				emitTool(i, t)
				keys := toolEntryLookupKeys(t)
				if owner := cachedOwners[resolvedToolKey(t)]; owner != "" && owner != provName && t.InstallWith == "" {
					if upsert, handled := a.refreshWithCachedOwner(ctx, t, keys, owner, ownerInstalledMaps, ownerMetadataMaps); handled {
						if upsert == nil {
							continue
						}
						upserts = append(upserts, upsert)
						continue
					}
				}
				ver, installed := provider.LookupString(m, keys)
				upsert := &database.ToolCache{
					Name:          t.Name,
					Provider:      t.Provider,
					Package:       t.EffectivePackage(),
					Installed:     installed,
					InstalledWith: installedWith,
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
			}
			for i, t := range provTools {
				emitTool(i, t)
				keys := toolEntryLookupKeys(t)
				if owner := cachedOwners[resolvedToolKey(t)]; owner != "" && owner != provName && t.InstallWith == "" {
					if upsert, handled := a.refreshWithCachedOwner(ctx, t, keys, owner, ownerInstalledMaps, ownerMetadataMaps); handled {
						if upsert == nil {
							continue
						}
						upserts = append(upserts, upsert)
						continue
					}
				}
				ver, installed := provider.LookupString(m, keys)
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
		} else if err := refreshContextErr(ctx, err); err != nil {
			return err
		}
		// InstalledMap failed — fall through to per-tool slow path.
	}

	// Slow path: per-tool IsInstalled. Used when provider has no BulkChecker,
	// or when BulkChecker.InstalledMap returned an error.
	for i, t := range provTools {
		emitTool(i, t)
		if owner := cachedOwners[resolvedToolKey(t)]; owner != "" && owner != provName && t.InstallWith == "" {
			if upsert, handled := a.refreshWithCachedOwner(ctx, t, toolEntryLookupKeys(t), owner, ownerInstalledMaps, ownerMetadataMaps); handled {
				if upsert == nil {
					continue
				}
				upserts = append(upserts, upsert)
				continue
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

// RefreshDiscovered scans all available providers whose concrete delegates are
// not already iterated separately, then stores locally-installed tools in the DB
// as tracked=false entries. Config-tracked rows are never overwritten.
// Best-effort: providers that error are silently skipped.
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

func (a *App) discoverUntrackedInstalled(ctx context.Context, cfg *config.RootConfig, progress func(RefreshDiscoveredProgressEvent)) []database.DiscoveredUpsert {
	// Build name-only set of tools already declared in config.
	configuredNames := make(map[string]struct{})
	for name := range cfg.Tools {
		configuredNames[name] = struct{}{}
	}
	for name := range ignoredToolSet(cfg) {
		configuredNames[name] = struct{}{}
	}

	// Build ecosystem maps so discovered tools get the ecosystem provider name
	// as their config provider label while scope checks can use the resolved
	// concrete manager for default ecosystem tools.
	stop := profile.Start("app.refresh.discovered.resolve_ecosystems")
	ecosystemProviders := a.ResolvedEcosystemProviders(ctx)
	revEcosystem := reverseEcosystemProviders(ecosystemProviders)
	stop()
	scope := a.discoveryProviderScope(ctx, cfg, ecosystemProviders)
	if scope.empty() {
		return nil
	}

	// Collect discovered upserts across all available providers. Providers are
	// processed serially after the availability pass, so no lock is needed.
	var discovered []database.DiscoveredUpsert
	// Best-effort: per-provider errors are skipped so one bad provider
	// doesn't prevent discovering the rest.
	providers := make([]provider.Provider, 0)
	stop = profile.Start("app.refresh.discovered.available_providers")
	for _, p := range a.availableProviders(ctx) {
		if !a.registry.ImportSkipsProvider(p.Name()) && a.discoveryProviderAllowed(p, revEcosystem, scope) {
			providers = append(providers, p)
		}
	}
	stop()
	for i, p := range providers {
		if progress != nil {
			progress(RefreshDiscoveredProgressEvent{Provider: p.Name(), Index: i + 1, Total: len(providers)})
		}

		configProvider := discoveryConfigProvider(p.Name(), revEcosystem, scope)

		// MultiManagerBulkChecker path: probe all backends to get per-tool
		// concrete-manager attribution (e.g. pnpm vs npm vs bun).
		if mbc, ok := p.(provider.MultiManagerBulkChecker); ok {
			entries, err := mbc.InstalledByManager(ctx)
			if err != nil {
				return nil
			}
			for name, entry := range entries {
				if _, ok := configuredNames[name]; ok {
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

		// Standard path: ListInstalled returns tools without per-manager attribution.
		installed, err := p.ListInstalled(ctx)
		if err != nil {
			continue // best-effort: skip erroring providers
		}
		for _, t := range installed {
			if _, ok := configuredNames[t.Name]; ok {
				continue // already in config; skip
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
	return discovered
}

// ListDiscovered returns all tool entries that are installed locally but not
// declared in config (tracked=false).
func (a *App) ListDiscovered(ctx context.Context) ([]*database.ToolCache, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	return a.listDiscoveredFromConfig(ctx, cfg, a.ResolvedEcosystemProviders(ctx))
}

type ToolDisplaySnapshot struct {
	Tools                  []*database.ToolCache
	Discovered             []*database.ToolCache
	EffectiveSystemManager string
}

func (a *App) ToolDisplaySnapshot(ctx context.Context) (*ToolDisplaySnapshot, error) {
	tools, err := a.ListTools(ctx, "")
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

func (a *App) listToolsFromConfig(ctx context.Context, cfg *config.RootConfig, providerFilter string, ecosystemProviders map[string]string) ([]*database.ToolCache, error) {
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
	configured, authoritative := a.configuredToolCacheKeys(ctx, cfg)
	scope := a.discoveryProviderScope(ctx, cfg, ecosystemProviders)
	tools = filterToolCachesByConfigAndScope(tools, configured, authoritative, scope)
	return filterIgnoredToolCaches(tools, ignoredToolSet(cfg)), nil
}

func (a *App) configuredToolCacheKeys(ctx context.Context, cfg *config.RootConfig) (map[string]struct{}, bool) {
	groups, hostAuthoritative := a.currentToolGroupsWithAuthority(cfg)
	entries, _ := a.resolvedToolEntries(ctx, cfg, groups)
	keys := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		keys[resolvedToolKey(entry)] = struct{}{}
		if entry.InstallWith != "" && entry.InstallWith != entry.Provider {
			keys[NewToolKey(entry.Name, entry.InstallWith, entry.EffectivePackage()).String()] = struct{}{}
		}
	}
	return keys, (hostAuthoritative && len(cfg.Tools) > 0) || len(keys) > 0
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

// Search fans out to every provider that implements provider.Searcher.
// Best-effort: errors on individual providers are collected and joined; partial
// results from successful providers are still returned.
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

func SearchResultDisplayProvider(r provider.SearchResult) string {
	if r.SourceProvider == "" || r.SourceProvider == r.Provider {
		return ""
	}
	if ToolProviderEcosystem(r.SourceProvider) != ToolProviderEcosystem(r.Provider) {
		return ""
	}
	return r.SourceProvider
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
	if a.registry.IsEcosystemProvider(providerName) {
		return providerName
	}
	if meta, ok := a.registry.Metadata(providerName); ok && meta.Ecosystem != "" {
		return meta.Ecosystem
	}
	if ecosystem, ok := a.providerEcosystem(providerName); ok && ecosystem != providerName {
		return ecosystem
	}
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

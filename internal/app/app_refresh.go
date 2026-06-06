package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/profile"
	"github.com/lkshrk/omni/internal/provider"
)

// ─── Auto-refresh with staleness check ───────────────────────────────────────

const (
	stateKeyRefreshInstalled = "last_refresh_installed"
	stateKeyRefreshOutdated  = "last_refresh_outdated"
	refreshStaleness         = 2 * time.Minute
)

// refreshInstalledIfStale runs RefreshInstalled when the last successful run
// was more than refreshStaleness ago (or never recorded).
func (a *App) refreshInstalledIfStale(ctx context.Context, progress func(string)) error {
	if a.refreshRecent(ctx, stateKeyRefreshInstalled) {
		return nil
	}
	if err := a.RefreshInstalled(ctx, progress); err != nil {
		return err
	}
	return a.markRefreshed(ctx, stateKeyRefreshInstalled)
}

// refreshOutdatedIfStale runs RefreshOutdated when the last successful run
// was more than refreshStaleness ago (or never recorded).
func (a *App) refreshOutdatedIfStale(ctx context.Context, progress func(string)) error {
	if a.refreshRecent(ctx, stateKeyRefreshOutdated) {
		return nil
	}
	if err := a.RefreshOutdated(ctx, false, progress); err != nil {
		return err
	}
	return a.markRefreshed(ctx, stateKeyRefreshOutdated)
}

// refreshRecent returns true when the given state key was marked within the
// staleness window. DB or parse errors are treated as "stale" (fail-open) so
// a transient DB issue triggers a fresh refresh rather than silently skipping.
func (a *App) refreshRecent(ctx context.Context, key string) bool {
	val, err := a.readDB().GetState(ctx, key)
	if err != nil {
		return false
	}
	t, err := time.Parse(time.RFC3339, val)
	if err != nil {
		return false
	}
	return time.Since(t) < refreshStaleness
}

func (a *App) markRefreshed(ctx context.Context, key string) error {
	return a.readDB().SetState(ctx, key, time.Now().UTC().Format(time.RFC3339))
}

// ─── Outdated / Descriptions ──────────────────────────────────────────────────

const descriptionFallbackConcurrency = 4

type descriptionPendingTool struct {
	name          string
	cacheProvider string
	tool          provider.Tool
}

// RefreshOutdated queries each provider's OutdatedMap and writes results to the DB.
// When refreshMetadata is true (user-initiated refresh), providers with a stale
// local index are refreshed first; passive background scans pass false to avoid
// the network latency.
func (a *App) RefreshOutdated(ctx context.Context, refreshMetadata bool, progress func(string)) error {
	defer profile.Start("app.refresh.outdated.total")()

	stop := profile.Start("app.refresh.outdated.list_tools")
	tools, err := a.readDB().List(ctx)
	stop()
	if err != nil {
		return fmt.Errorf("listing tools: %w", err)
	}
	ignored := a.ignoredToolSetBestEffort()
	tools = filterIgnoredToolCaches(tools, ignored)
	neededProviders := make(map[string]struct{})
	for _, t := range tools {
		if lookupProvider := a.outdatedLookupProvider(t); lookupProvider != "" {
			neededProviders[lookupProvider] = struct{}{}
		}
	}
	if len(neededProviders) == 0 {
		return nil
	}

	if refreshMetadata {
		stop = profile.Start("app.refresh.outdated.metadata")
		a.refreshProviderMetadataBestEffort(ctx, neededProviders)
		stop()
	}

	stop = profile.Start("app.refresh.outdated.provider_maps")
	outdatedByProv, outdatedByManager, updateMetadata := a.outdatedMapsForProvidersBestEffort(ctx, neededProviders)
	stop()

	stop = profile.Start("app.refresh.outdated.build_updates")
	updates := make([]database.OutdatedUpdate, 0, len(tools))
	for i, t := range tools {
		outdatedProvider := a.outdatedLookupProvider(t)
		if progress != nil {
			labelProvider := outdatedProvider
			if labelProvider == "" {
				labelProvider = t.Provider
			}
			progress(fmt.Sprintf("Checking updates %d/%d: %s/%s…", i+1, len(tools), labelProvider, t.Name))
		}
		m, ok := outdatedByProv[outdatedProvider]
		if !ok {
			continue
		}
		latestVer, isOutdated := outdatedForTool(t, m, outdatedByManager[outdatedProvider])
		updates = append(updates, database.OutdatedUpdate{
			Name:          t.Name,
			Provider:      t.Provider,
			Package:       t.Package,
			Outdated:      isOutdated,
			LatestVersion: latestVer,
		})
	}
	stop()
	writeCtx := context.WithoutCancel(ctx)
	stop = profile.Start("app.refresh.outdated.write")
	if err := a.readDB().UpdateOutdatedBatch(writeCtx, updates); err != nil {
		stop()
		return fmt.Errorf("updating outdated status: %w", err)
	}
	if err := a.readDB().UpsertUpdateMetadataBatch(writeCtx, updateMetadata); err != nil {
		stop()
		return fmt.Errorf("updating update metadata: %w", err)
	}
	stop()
	return nil
}

func (a *App) outdatedMapsForProvidersBestEffort(ctx context.Context, providerNames map[string]struct{}) (map[string]map[string]string, map[string]map[string]map[string]string, []database.UpdateMetadata) {
	outdatedByProv := make(map[string]map[string]string)
	outdatedByManager := make(map[string]map[string]map[string]string)
	var updateMetadata []database.UpdateMetadata
	var mu sync.Mutex
	var wg sync.WaitGroup
	for provName := range providerNames {
		p, ok := a.registry.Get(provName)
		if !ok {
			continue
		}
		if !providerSupportsOutdatedRefresh(p) {
			continue
		}
		provName := provName
		wg.Add(1)
		go func() {
			defer wg.Done()
			m, byManager, metadata, ok, err := a.outdatedMapsForProvider(ctx, provName)
			if err != nil || !ok {
				return
			}
			mu.Lock()
			outdatedByProv[provName] = m
			if byManager != nil {
				outdatedByManager[provName] = byManager
			}
			updateMetadata = append(updateMetadata, metadata...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	return outdatedByProv, outdatedByManager, updateMetadata
}

// refreshProviderMetadataBestEffort refreshes the locally-cached package index
// for providers that support it (e.g. `brew update`), so OutdatedMap can see
// newly published versions. Failures are logged and ignored: a stale index is
// better than a failed scan, and the refresh is purely an accuracy improvement.
func (a *App) refreshProviderMetadataBestEffort(ctx context.Context, providerNames map[string]struct{}) {
	for name := range providerNames {
		p, ok := a.registry.Get(name)
		if !ok {
			continue
		}
		mr, ok := p.(provider.MetadataRefresher)
		if !ok {
			continue
		}
		if avail, err := p.Available(ctx); err != nil || !avail {
			continue
		}
		if err := mr.RefreshMetadata(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "warning: omni: refresh %s metadata: %v\n", name, err)
		}
	}
}

// RefreshProviderOutdated queries outdated status for a single named provider and
// writes results to the DB.
func (a *App) RefreshProviderOutdated(ctx context.Context, provName string, refreshMetadata bool) error {
	defer profile.Start("app.refresh.outdated.provider." + provName)()

	tools, err := a.readDB().List(ctx)
	if err != nil {
		return fmt.Errorf("listing tools for %s: %w", provName, err)
	}
	ignored := a.ignoredToolSetBestEffort()
	tools = filterIgnoredToolCaches(tools, ignored)
	targetTools := make([]*database.ToolCache, 0, len(tools))
	neededLookups := make(map[string]struct{})
	for _, t := range tools {
		if t.Provider != provName && t.InstalledWith != provName {
			continue
		}
		targetTools = append(targetTools, t)
		if lookupProvider := a.outdatedLookupProvider(t); lookupProvider != "" {
			neededLookups[lookupProvider] = struct{}{}
		}
	}
	if cfg, cfgErr := a.loadConfig(); cfgErr == nil {
		resolved, _ := a.currentResolvedToolEntries(ctx, cfg)
		for _, entry := range resolved {
			opProvider := a.operationProviderName(entry)
			if entry.Provider != provName && opProvider != provName && entry.InstallWith != provName {
				continue
			}
			if opProvider != "" {
				neededLookups[opProvider] = struct{}{}
			}
		}
	}
	if len(neededLookups) == 0 {
		return nil
	}
	if refreshMetadata {
		a.refreshProviderMetadataBestEffort(ctx, neededLookups)
	}
	outdatedByProv := make(map[string]map[string]string)
	outdatedByManager := make(map[string]map[string]map[string]string)
	updateMetadata := make([]database.UpdateMetadata, 0)
	if _, needed := neededLookups[provName]; needed {
		if m, byManager, metadata, ok, err := a.outdatedMapsForProvider(ctx, provName); err != nil {
			return err
		} else if ok {
			outdatedByProv[provName] = m
			if byManager != nil {
				outdatedByManager[provName] = byManager
			}
			updateMetadata = append(updateMetadata, metadata...)
		}
	}
	ensureOutdated := func(providerName string) bool {
		if providerName == "" {
			return false
		}
		if _, ok := outdatedByProv[providerName]; ok {
			return true
		}
		m, byManager, metadata, ok, err := a.outdatedMapsForProvider(ctx, providerName)
		if err != nil {
			return false
		}
		if !ok {
			return false
		}
		outdatedByProv[providerName] = m
		if byManager != nil {
			outdatedByManager[providerName] = byManager
		}
		updateMetadata = append(updateMetadata, metadata...)
		return true
	}
	updates := make([]database.OutdatedUpdate, 0, len(targetTools))
	for _, t := range targetTools {
		lookupProvider := a.outdatedLookupProvider(t)
		if !ensureOutdated(lookupProvider) {
			continue
		}
		latestVer, isOutdated := outdatedForTool(t, outdatedByProv[lookupProvider], outdatedByManager[lookupProvider])
		updates = append(updates, database.OutdatedUpdate{
			Name:          t.Name,
			Provider:      t.Provider,
			Package:       t.Package,
			Outdated:      isOutdated,
			LatestVersion: latestVer,
		})
	}
	writeCtx := context.WithoutCancel(ctx)
	if err := a.readDB().UpdateOutdatedBatch(writeCtx, updates); err != nil {
		return fmt.Errorf("updating outdated status for %s: %w", provName, err)
	}
	if err := a.readDB().UpsertUpdateMetadataBatch(writeCtx, updateMetadata); err != nil {
		return fmt.Errorf("updating update metadata for %s: %w", provName, err)
	}
	return nil
}

func (a *App) outdatedMapsForProvider(ctx context.Context, providerName string) (map[string]string, map[string]map[string]string, []database.UpdateMetadata, bool, error) {
	defer profile.Start("app.refresh.outdated.map." + providerName)()

	p, ok := a.registry.Get(providerName)
	if !ok {
		return nil, nil, nil, false, nil
	}
	if !providerSupportsOutdatedRefresh(p) {
		return nil, nil, nil, false, nil
	}
	avail, err := p.Available(ctx)
	if err != nil || !avail {
		return nil, nil, nil, false, nil
	}
	if moc, ok := p.(provider.ManagerOutdatedInfoChecker); ok {
		byManager, err := moc.OutdatedInfoByManager(ctx)
		if err != nil {
			return nil, nil, nil, false, fmt.Errorf("checking outdated tools for %s: %w", providerName, err)
		}
		return flattenOutdatedInfoManagers(byManager), outdatedInfoManagersToVersions(byManager), updateMetadataForManagerInfo(byManager), true, nil
	}
	if oc, ok := p.(provider.OutdatedInfoChecker); ok {
		m, err := oc.OutdatedInfoMap(ctx)
		if err != nil {
			return nil, nil, nil, false, fmt.Errorf("checking outdated tools for %s: %w", providerName, err)
		}
		return outdatedInfoMapToVersions(m), nil, updateMetadataForInfo(providerName, m), true, nil
	}
	if moc, ok := p.(provider.ManagerOutdatedChecker); ok {
		byManager, err := moc.OutdatedByManager(ctx)
		if err != nil {
			return nil, nil, nil, false, fmt.Errorf("checking outdated tools for %s: %w", providerName, err)
		}
		return flattenOutdatedManagers(byManager), byManager, nil, true, nil
	}
	oc, ok := p.(provider.OutdatedChecker)
	if !ok {
		return nil, nil, nil, false, nil
	}
	m, err := oc.OutdatedMap(ctx)
	if err != nil {
		return nil, nil, nil, false, fmt.Errorf("checking outdated tools for %s: %w", providerName, err)
	}
	return m, nil, nil, true, nil
}

func providerSupportsOutdatedRefresh(p provider.Provider) bool {
	switch p.(type) {
	case provider.ManagerOutdatedInfoChecker, provider.OutdatedInfoChecker, provider.ManagerOutdatedChecker, provider.OutdatedChecker:
		return true
	default:
		return false
	}
}

func (a *App) outdatedLookupProvider(t *database.ToolCache) string {
	if t.InstalledWith != "" {
		if _, ok := a.registry.Get(t.InstalledWith); ok {
			return t.InstalledWith
		}
		if ecosystem, ok := provider.BuiltinEcosystemFor(t.InstalledWith); ok {
			return ecosystem
		}
	}
	if t.Provider != "" {
		return t.Provider
	}
	return t.InstalledWith
}

func flattenOutdatedManagers(byManager map[string]map[string]string) map[string]string {
	if len(byManager) == 0 {
		return nil
	}
	out := make(map[string]string)
	for _, m := range byManager {
		for name, latest := range m {
			if _, exists := out[name]; !exists {
				out[name] = latest
			}
		}
	}
	return out
}

func flattenOutdatedInfoManagers(byManager map[string]map[string]provider.OutdatedInfo) map[string]string {
	if len(byManager) == 0 {
		return nil
	}
	out := make(map[string]string)
	for _, m := range byManager {
		for name, info := range m {
			if _, exists := out[name]; !exists && info.LatestVersion != "" {
				out[name] = info.LatestVersion
			}
		}
	}
	return out
}

func outdatedInfoManagersToVersions(byManager map[string]map[string]provider.OutdatedInfo) map[string]map[string]string {
	if len(byManager) == 0 {
		return nil
	}
	out := make(map[string]map[string]string, len(byManager))
	for manager, m := range byManager {
		versions := outdatedInfoMapToVersions(m)
		if len(versions) > 0 {
			out[manager] = versions
		}
	}
	return out
}

func outdatedInfoMapToVersions(in map[string]provider.OutdatedInfo) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for name, info := range in {
		if info.LatestVersion != "" {
			out[name] = info.LatestVersion
		}
	}
	return out
}

func updateMetadataForManagerInfo(byManager map[string]map[string]provider.OutdatedInfo) []database.UpdateMetadata {
	var updates []database.UpdateMetadata
	for manager, m := range byManager {
		updates = append(updates, updateMetadataForInfo(manager, m)...)
	}
	return updates
}

func updateMetadataForInfo(providerName string, infoByPackage map[string]provider.OutdatedInfo) []database.UpdateMetadata {
	if providerName == "" || len(infoByPackage) == 0 {
		return nil
	}
	now := time.Now()
	updates := make([]database.UpdateMetadata, 0, len(infoByPackage))
	for pkg, info := range infoByPackage {
		if pkg == "" || info.LatestVersion == "" || info.AvailableAt == nil || info.AvailableAt.IsZero() {
			continue
		}
		source := strings.TrimSpace(info.DateSource)
		if source == "" {
			source = "package-manager"
		}
		updates = append(updates, database.UpdateMetadata{
			Provider:    providerName,
			Package:     pkg,
			Version:     info.LatestVersion,
			AvailableAt: *info.AvailableAt,
			DateSource:  source,
			CheckedAt:   now,
		})
	}
	return updates
}

func outdatedForTool(t *database.ToolCache, flat map[string]string, byManager map[string]map[string]string) (string, bool) {
	keys := toolCacheLookupKeys(t)
	if t.InstalledWith != "" && byManager != nil {
		m := byManager[t.InstalledWith]
		return provider.LookupString(m, keys)
	}
	return provider.LookupString(flat, keys)
}

// RefreshDescriptions fetches and caches descriptions for configured and
// discovered tools that don't already have one. Bulk provider metadata is used
// first; individual provider lookups are a bounded-concurrent fallback.
func (a *App) RefreshDescriptions(ctx context.Context, _ time.Duration) error {
	return a.RefreshDescriptionsWithProgress(ctx, 0, nil)
}

type RefreshDescriptionsProgressEvent struct {
	Provider string
	Name     string
	Index    int
	Total    int
}

func RefreshDescriptionsProgressText(event RefreshDescriptionsProgressEvent) string {
	total := event.Total
	if total <= 0 {
		total = event.Index
	}
	index := event.Index
	if index < 0 {
		index = 0
	}
	providerName := event.Provider
	if providerName == "" {
		providerName = "tool"
	}
	return fmt.Sprintf("Refreshing descriptions %d/%d: %s/%s…", index, total, providerName, event.Name)
}

func (a *App) RefreshDescriptionsWithProgress(ctx context.Context, _ time.Duration, progress func(RefreshDescriptionsProgressEvent)) error {
	defer profile.Start("app.refresh.descriptions.total")()

	stop := profile.Start("app.refresh.descriptions.load_config")
	cfg, err := a.loadConfig()
	stop()
	if err != nil {
		return err
	}

	// List the cache once and build a lookup map so the configured-tools loop
	// below can avoid N point-lookups (one per resolved tool).
	stop = profile.Start("app.refresh.descriptions.list_tools")
	cachedTools, err := a.readDB().List(ctx)
	stop()
	if err != nil {
		return err
	}
	ignored := ignoredToolSet(cfg)
	cachedTools = filterIgnoredToolCaches(cachedTools, ignored)
	cacheByKey := make(map[string]*database.ToolCache, len(cachedTools))
	for _, t := range cachedTools {
		pkg := t.Package
		if pkg == "" {
			pkg = t.Name
		}
		cacheByKey[NewToolKey(t.Name, t.Provider, pkg).String()] = t
	}
	stop = profile.Start("app.refresh.descriptions.list_metadata")
	cachedMetadata, err := a.readDB().ListMetadata(ctx)
	stop()
	if err != nil {
		return err
	}
	metadataByKey := make(map[string]*database.ToolMetadata, len(cachedMetadata))
	for _, m := range cachedMetadata {
		if m == nil {
			continue
		}
		pkg := m.Package
		if pkg == "" {
			pkg = m.Name
		}
		metadataByKey[NewToolKey(m.Name, m.Provider, pkg).String()] = m
	}
	hasCachedDescription := func(key string, cached *database.ToolCache) bool {
		if cached != nil && cached.Description.Valid && cached.Description.String != "" {
			return true
		}
		meta := metadataByKey[key]
		return meta != nil && meta.Description.Valid && meta.Description.String != ""
	}

	stop = profile.Start("app.refresh.descriptions.queue")
	byProvider := make(map[string][]descriptionPendingTool)
	queued := make(map[string]bool)
	tools, _ := a.currentResolvedToolEntries(ctx, cfg)
	for _, e := range tools {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		pkg := e.EffectivePackage()
		key := NewToolKey(e.Name, e.Provider, pkg).String()
		cached, ok := cacheByKey[key]
		if hasCachedDescription(key, cached) {
			continue // already cached
		}
		if ok && cached.Package != "" {
			pkg = cached.Package
			key = NewToolKey(e.Name, e.Provider, pkg).String()
		}
		opProvider := a.operationProviderName(e)
		byProvider[opProvider] = append(byProvider[opProvider], descriptionPendingTool{
			name:          e.Name,
			cacheProvider: e.Provider,
			tool:          provider.Tool{Name: e.Name, Provider: opProvider, Package: pkg},
		})
		queued[key] = true
	}
	for _, t := range cachedTools {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		pkg := t.Package
		if pkg == "" {
			pkg = t.Name
		}
		if queued[NewToolKey(t.Name, t.Provider, pkg).String()] {
			continue
		}
		key := NewToolKey(t.Name, t.Provider, pkg).String()
		if hasCachedDescription(key, t) {
			continue
		}
		describeProvider := a.descriptionProviderName(t.Provider, t.InstalledWith)
		if describeProvider == "" {
			continue
		}
		byProvider[describeProvider] = append(byProvider[describeProvider], descriptionPendingTool{
			name:          t.Name,
			cacheProvider: t.Provider,
			tool:          provider.Tool{Name: t.Name, Provider: describeProvider, Package: pkg},
		})
		queued[key] = true
	}
	stop()

	pendingTotal := 0
	for _, pending := range byProvider {
		pendingTotal += len(pending)
	}
	pendingDone := 0
	emit := func(provName string, pending descriptionPendingTool) {
		if progress == nil {
			return
		}
		pendingDone++
		progress(RefreshDescriptionsProgressEvent{
			Provider: provName,
			Name:     pending.name,
			Index:    pendingDone,
			Total:    pendingTotal,
		})
	}

	stop = profile.Start("app.refresh.descriptions.providers")
	for provName, pending := range byProvider {
		if ctx.Err() != nil {
			stop()
			return ctx.Err()
		}
		prov, ok := a.registry.Get(provName)
		if !ok {
			for _, p := range pending {
				emit(provName, p)
			}
			continue
		}

		bulkProgressEmitted := false
		if bd, ok := prov.(provider.BulkDescriber); ok {
			toolSlice := make([]provider.Tool, len(pending))
			for i, p := range pending {
				toolSlice[i] = p.tool
			}
			if descs, err := bd.BulkDescribe(ctx, toolSlice); err == nil {
				bulkUpdates := make([]database.DescriptionUpdate, 0, len(pending))
				missing := pending[:0]
				for _, p := range pending {
					emit(provName, p)
					if desc := lookupDescription(descs, p.name, p.tool.EffectivePackage()); desc != "" {
						bulkUpdates = append(bulkUpdates, database.DescriptionUpdate{
							Name:        p.name,
							Provider:    p.cacheProvider,
							Package:     p.tool.EffectivePackage(),
							Description: desc,
						})
						continue
					}
					missing = append(missing, p)
				}
				bulkProgressEmitted = true
				if err := a.readDB().UpdateDescriptionBatch(ctx, bulkUpdates); err != nil {
					stop()
					return fmt.Errorf("updating descriptions for %s: %w", provName, err)
				}
				pending = missing
			}
		}
		if len(pending) == 0 {
			continue
		}

		d, ok := prov.(provider.Descriptor)
		if !ok {
			if !bulkProgressEmitted {
				for _, p := range pending {
					emit(provName, p)
				}
			}
			continue
		}
		if !bulkProgressEmitted {
			for _, p := range pending {
				emit(provName, p)
			}
		}
		if err := a.refreshDescriptionsIndividually(ctx, provName, pending, d); err != nil {
			stop()
			return err
		}
	}
	stop()
	return nil
}

func (a *App) descriptionProviderName(cacheProvider, installedWith string) string {
	if _, ok := a.registry.Get(cacheProvider); ok {
		return cacheProvider
	}
	for _, name := range []string{installedWith, cacheProvider} {
		if ecosystem, ok := provider.BuiltinEcosystemFor(name); ok {
			if _, registered := a.registry.Get(ecosystem); registered {
				return ecosystem
			}
		}
	}
	return ""
}

type descriptionResult struct {
	name          string
	cacheProvider string
	pkg           string
	desc          string
}

func (a *App) refreshDescriptionsIndividually(ctx context.Context, provName string, pending []descriptionPendingTool, d provider.Descriptor) error {
	if len(pending) == 0 {
		return nil
	}
	workers := min(len(pending), descriptionFallbackConcurrency)

	jobs := make(chan descriptionPendingTool)
	results := make(chan descriptionResult, len(pending))
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for p := range jobs {
				if ctx.Err() != nil {
					return
				}
				desc, err := d.Describe(ctx, p.tool)
				if err == nil && desc != "" {
					results <- descriptionResult{name: p.name, cacheProvider: p.cacheProvider, pkg: p.tool.EffectivePackage(), desc: desc}
				}
			}
		}()
	}
	var feederDone sync.WaitGroup
	feederDone.Add(1)
	go func() {
		defer feederDone.Done()
		defer close(jobs)
		for _, p := range pending {
			select {
			case <-ctx.Done():
				return
			case jobs <- p:
			}
		}
	}()
	wg.Wait()
	feederDone.Wait()
	close(results)

	if err := ctx.Err(); err != nil {
		return err
	}
	updates := make([]database.DescriptionUpdate, 0)
	for r := range results {
		updates = append(updates, database.DescriptionUpdate{
			Name:        r.name,
			Provider:    r.cacheProvider,
			Package:     r.pkg,
			Description: r.desc,
		})
	}
	if err := a.readDB().UpdateDescriptionBatch(ctx, updates); err != nil {
		return fmt.Errorf("updating descriptions for %s: %w", provName, err)
	}
	return nil
}

func lookupDescription(descs map[string]string, name, pkg string) string {
	for _, key := range descriptionLookupKeys(name, pkg) {
		if desc := strings.TrimSpace(descs[key]); desc != "" {
			return desc
		}
	}
	return ""
}

func descriptionLookupKeys(name, pkg string) []string {
	if pkg == "" {
		pkg = name
	}
	keys := make([]string, 0, 6)
	add := func(key string) {
		key = strings.TrimSpace(key)
		if key == "" {
			return
		}
		for _, existing := range keys {
			if existing == key {
				return
			}
		}
		keys = append(keys, key)
	}
	add(pkg)
	add(name)
	for _, key := range provider.PackageLookupKeys(name, pkg) {
		add(key)
	}
	return keys
}

// ─── Init helpers ─────────────────────────────────────────────────────────────

// InitTestMode wires the App with the given providers instead of the built-in
// brew/node/pip suite. Intended for use in tests only.
func (a *App) InitTestMode(ctx context.Context, providers ...provider.Provider) error {
	a.testMode = true
	if _, err := config.NormalizeFile(a.ConfigPath); err != nil {
		return fmt.Errorf("normalizing config file: %w", err)
	}
	if err := a.repairCurrentHostEntry(); err != nil {
		return fmt.Errorf("repairing current host entry: %w", err)
	}
	dbDir := a.CacheDir
	if dbDir == "" {
		dbDir = a.configDir()
	}
	a.DBPath = filepath.Join(dbDir, "omni.db")
	db, err := database.Open(a.DBPath)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	if err := db.Migrate(ctx); err != nil {
		return fmt.Errorf("migrating database: %w", err)
	}
	a.db = db
	a.registry = provider.NewRegistry()
	for _, p := range providers {
		a.registry.RegisterWithMetadata(p, provider.BuiltinMetadata(p.Name()))
	}
	return nil
}

// Registry and DB are test helpers; no production code calls these.
func (a *App) Registry() *provider.Registry { return a.registry }
func (a *App) DB() *database.DB             { return a.db }

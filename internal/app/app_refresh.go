package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

// ─── Outdated / Descriptions ──────────────────────────────────────────────────

const descriptionFallbackConcurrency = 4

type descriptionPendingTool struct {
	name          string
	cacheProvider string
	tool          provider.Tool
}

// RefreshOutdated queries each provider's OutdatedMap and writes results to the DB.
func (a *App) RefreshOutdated(ctx context.Context, progress func(string)) error {
	tools, err := a.readDB().List(ctx)
	if err != nil {
		return fmt.Errorf("listing tools: %w", err)
	}

	outdatedByProv := make(map[string]map[string]string)
	outdatedByManager := make(map[string]map[string]map[string]string)
	// Closure always returns nil; per-provider OutdatedMap failures are skipped so a
	// single bad provider doesn't prevent updating the rest.
	_ = a.forEachAvailable(ctx, func(p provider.Provider) error {
		oc, ok := p.(provider.OutdatedChecker)
		if !ok {
			return nil
		}
		if progress != nil {
			progress("checking " + p.Name() + " for updates…")
		}
		if moc, ok := p.(provider.ManagerOutdatedChecker); ok {
			m, err := moc.OutdatedByManager(ctx)
			if err == nil {
				outdatedByManager[p.Name()] = m
				outdatedByProv[p.Name()] = flattenOutdatedManagers(m)
			}
			return nil
		}
		m, err := oc.OutdatedMap(ctx)
		if err == nil {
			outdatedByProv[p.Name()] = m
		}
		return nil
	})

	updates := make([]database.OutdatedUpdate, 0, len(tools))
	for _, t := range tools {
		outdatedProvider := a.outdatedLookupProvider(t)
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
	writeCtx := context.WithoutCancel(ctx)
	if err := a.readDB().UpdateOutdatedBatch(writeCtx, updates); err != nil {
		return fmt.Errorf("updating outdated status: %w", err)
	}
	return nil
}

// RefreshProviderOutdated queries outdated status for a single named provider and
// writes results to the DB.
func (a *App) RefreshProviderOutdated(ctx context.Context, provName string) error {
	tools, err := a.readDB().List(ctx)
	if err != nil {
		return fmt.Errorf("listing tools for %s: %w", provName, err)
	}
	outdatedByProv := make(map[string]map[string]string)
	outdatedByManager := make(map[string]map[string]map[string]string)
	if m, byManager, ok, err := a.outdatedMapsForProvider(ctx, provName); err != nil {
		return err
	} else if ok {
		outdatedByProv[provName] = m
		if byManager != nil {
			outdatedByManager[provName] = byManager
		}
	}
	ensureOutdated := func(providerName string) bool {
		if providerName == "" {
			return false
		}
		if _, ok := outdatedByProv[providerName]; ok {
			return true
		}
		m, byManager, ok, err := a.outdatedMapsForProvider(ctx, providerName)
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
		return true
	}
	updates := make([]database.OutdatedUpdate, 0, len(tools))
	for _, t := range tools {
		if t.Provider != provName && t.InstalledWith != provName {
			continue
		}
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
	return nil
}

func (a *App) outdatedMapsForProvider(ctx context.Context, providerName string) (map[string]string, map[string]map[string]string, bool, error) {
	p, ok := a.registry.Get(providerName)
	if !ok {
		return nil, nil, false, nil
	}
	oc, ok := p.(provider.OutdatedChecker)
	if !ok {
		return nil, nil, false, nil
	}
	avail, err := p.Available(ctx)
	if err != nil || !avail {
		return nil, nil, false, nil
	}
	if moc, ok := p.(provider.ManagerOutdatedChecker); ok {
		byManager, err := moc.OutdatedByManager(ctx)
		if err != nil {
			return nil, nil, false, fmt.Errorf("checking outdated tools for %s: %w", providerName, err)
		}
		return flattenOutdatedManagers(byManager), byManager, true, nil
	}
	m, err := oc.OutdatedMap(ctx)
	if err != nil {
		return nil, nil, false, fmt.Errorf("checking outdated tools for %s: %w", providerName, err)
	}
	return m, nil, true, nil
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
	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}

	// List the cache once and build a lookup map so the configured-tools loop
	// below can avoid N point-lookups (one per resolved tool).
	cachedTools, err := a.readDB().List(ctx)
	if err != nil {
		return err
	}
	cacheByKey := make(map[string]*database.ToolCache, len(cachedTools))
	for _, t := range cachedTools {
		pkg := t.Package
		if pkg == "" {
			pkg = t.Name
		}
		cacheByKey[NewToolKey(t.Name, t.Provider, pkg).String()] = t
	}
	cachedMetadata, err := a.readDB().ListMetadata(ctx)
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

	byProvider := make(map[string][]descriptionPendingTool)
	queued := make(map[string]bool)
	tools, _ := a.resolvedToolEntries(ctx, cfg, cfg.Groups)
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

	for provName, pending := range byProvider {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		prov, ok := a.registry.Get(provName)
		if !ok {
			continue
		}

		if bd, ok := prov.(provider.BulkDescriber); ok {
			toolSlice := make([]provider.Tool, len(pending))
			for i, p := range pending {
				toolSlice[i] = p.tool
			}
			if descs, err := bd.BulkDescribe(ctx, toolSlice); err == nil {
				bulkUpdates := make([]database.DescriptionUpdate, 0, len(pending))
				missing := pending[:0]
				for _, p := range pending {
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
				if err := a.readDB().UpdateDescriptionBatch(ctx, bulkUpdates); err != nil {
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
			continue
		}
		if err := a.refreshDescriptionsIndividually(ctx, provName, pending, d); err != nil {
			return err
		}
	}
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
	go func() {
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

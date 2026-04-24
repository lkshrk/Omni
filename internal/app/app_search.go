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
	"github.com/lkshrk/omni/internal/provider"
)

// ─── List / Search / Providers ───────────────────────────────────────────────

type ToolListState string

const (
	ToolStateInstalled ToolListState = "installed"
	ToolStateMissing   ToolListState = "missing"
	ToolStateOutdated  ToolListState = "outdated"
	ToolStateIgnored   ToolListState = "ignored"
	ToolStateUnclaimed ToolListState = "unclaimed"
	ToolStateOutOfSync ToolListState = "out-of-sync"
	ToolStateFailed    ToolListState = "failed"
)

type ToolListOptions struct {
	Provider string
	Group    string
	Profile  string
	Name     string
	State    string
}

type ToolListItem struct {
	Tool  *database.ToolCache
	Group string
	State ToolListState
}

func (a *App) ListTools(ctx context.Context, providerFilter string) ([]*database.ToolCache, error) {
	if providerFilter != "" {
		return a.readDB().ListByProvider(ctx, providerFilter)
	}
	return a.readDB().List(ctx)
}

func (a *App) ConfiguredProviders(ctx context.Context) ([]string, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	tools, _ := a.resolvedToolEntries(ctx, cfg, cfg.Groups)
	provSet := make(map[string]struct{})
	for _, t := range tools {
		providerName := a.operationProviderName(t)
		if providerName != "" {
			provSet[providerName] = struct{}{}
		}
	}
	providers := make([]string, 0, len(provSet))
	for p := range provSet {
		providers = append(providers, p)
	}
	return providers, nil
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
	resolved, _ := a.resolveTools(ctx, cfg, cfg.Groups)
	groups := make(map[string][]string, len(resolved))
	for _, t := range resolved {
		if len(t.memberships) == 0 {
			continue
		}
		groups[t.entry.Name+"\x00"+t.entry.Provider] = append([]string(nil), t.memberships...)
	}
	return groups, nil
}

func (a *App) QueryTools(ctx context.Context, opts ToolListOptions) ([]ToolListItem, error) {
	tools, err := a.ListTools(ctx, "")
	if err != nil {
		return nil, err
	}
	if opts.Provider != "" {
		filtered := tools[:0]
		for _, tool := range tools {
			if a.cacheToolMatchesProvider(tool, opts.Provider) {
				filtered = append(filtered, tool)
			}
		}
		tools = filtered
	}

	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	groups := cfg.Groups
	if opts.Profile != "" {
		groups, _, err = effectiveProfileGroups(cfg, groups, opts.Profile)
		if err != nil {
			return nil, err
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

	ignoreSet := profileIgnoreSet(cfg, opts.Profile)
	resolvedProviders := a.ResolvedEcosystemProviders(ctx)
	stateFilter, err := normalizeToolState(opts.State)
	if err != nil {
		return nil, err
	}

	var items []ToolListItem
	for _, t := range tools {
		key := NewToolKey(t.Name, t.Provider, t.Package).String()
		groupName := groupByTool[key]
		if opts.Profile != "" {
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

func (a *App) refreshWithCachedOwner(ctx context.Context, t config.ToolEntry, keys []string, owner string, installedMaps map[string]map[string]string) (*database.ToolCache, bool) {
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
	if installedMaps != nil {
		if m, ok := installedMaps[owner]; ok {
			ver, installed = provider.LookupString(m, keys)
			return installedOwnerUpsert(t, owner, installed, ver), true
		}
	}
	if bc, ok := ownerProv.(provider.BulkChecker); ok {
		if m, err := bc.InstalledMap(ctx); err == nil {
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

func profileIgnoreSet(cfg *config.RootConfig, profileName string) map[string]struct{} {
	ignoreSet := make(map[string]struct{})
	if profileName == "" {
		var ok bool
		profileName, ok = cfg.ActiveProfile(currentHostname())
		if !ok {
			return ignoreSet
		}
	}
	profile, ok := cfg.Profiles[profileName]
	if !ok {
		return ignoreSet
	}
	for _, name := range profile.Ignore {
		ignoreSet[name] = struct{}{}
	}
	return ignoreSet
}

func classifyToolState(t *database.ToolCache, ignoreSet map[string]struct{}, resolved map[string]string) ToolListState {
	if t.FailedAt != nil {
		return ToolStateFailed
	}
	if _, ignored := ignoreSet[t.Name]; ignored {
		return ToolStateIgnored
	}
	if t.Installed && t.Outdated {
		return ToolStateOutdated
	}
	if !t.Tracked {
		return ToolStateUnclaimed
	}
	if t.Tracked && !t.Installed {
		return ToolStateMissing
	}
	if concrete := resolved[t.Provider]; concrete != "" && t.InstalledWith != "" && t.InstalledWith != concrete {
		return ToolStateOutOfSync
	}
	if t.Installed {
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
// The optional progress callback is called with a short description before each
// provider is scanned (e.g. "Scanning brew…"); pass nil to omit progress.
func (a *App) RefreshInstalled(ctx context.Context, progress func(string)) error {
	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	tools, _ := a.resolvedToolEntries(ctx, cfg, cfg.Groups)
	if len(tools) == 0 {
		return nil
	}
	cachedOwners, err := a.cachedInstalledOwners(ctx)
	if err != nil {
		return err
	}

	// Build a set of providers that have tools in config — used to filter
	// progress messages and the pre-scan count.
	configProviders := make(map[string]bool, len(tools))
	for _, t := range tools {
		configProviders[a.operationProviderName(t)] = true
	}

	// Resolve availability once in parallel; reuse the result for both the
	// scanTotal count and the main scan loop below to avoid two Available
	// passes per provider.
	available := a.availableProviders(ctx)
	var scanTotal int
	if progress != nil {
		for _, p := range available {
			if configProviders[p.Name()] {
				scanTotal++
			}
		}
	}
	scanIdx := 0
	emit := func(label string) {
		if progress == nil {
			return
		}
		scanIdx++
		if scanTotal > 1 {
			progress(fmt.Sprintf("%s (%d/%d)", label, scanIdx, scanTotal))
		} else {
			progress(label)
		}
	}

	// Build installed map per provider using bulk check where available.
	// multiMaps: provider → name → InstalledEntry (per-tool manager attribution).
	// installedMaps: provider → name → version (single-manager bulk path).
	// concreteForBulk: provider → concrete backend for BulkChecker providers.
	multiMaps := make(map[string]map[string]provider.InstalledEntry)
	installedMaps := make(map[string]map[string]string)
	concreteForBulk := make(map[string]string)
	// Iterate the precomputed available set so we don't call Available a
	// second time. Per-provider scan failures are skipped silently so a
	// single failing backend doesn't block discovery for others.
	for _, p := range available {
		// MultiManagerBulkChecker takes priority: probes all backends for per-tool attribution.
		if mbc, ok := p.(provider.MultiManagerBulkChecker); ok {
			entries, err := mbc.InstalledByManager(ctx)
			if err != nil {
				continue
			}
			multiMaps[p.Name()] = entries
			if configProviders[p.Name()] {
				emit("Scanning " + p.Name() + "…")
			}
			continue
		}
		bc, ok := p.(provider.BulkChecker)
		if !ok {
			continue
		}
		m, err := bc.InstalledMap(ctx)
		if err != nil {
			continue
		}
		// Resolve concrete backend for InstalledWith. Ecosystem providers (e.g. node)
		// implement ConcreteResolver; concrete providers are their own backend.
		installedWith := p.Name()
		if cr, ok := p.(provider.ConcreteResolver); ok {
			if name, resolveErr := cr.ResolvedName(ctx); resolveErr == nil && name != "" {
				installedWith = name
			} else {
				installedWith = "" // prefer unknown over stale ecosystem name
			}
		}
		installedMaps[p.Name()] = m
		concreteForBulk[p.Name()] = installedWith
		// Emit progress after InstalledMap succeeds (only for providers with config tools).
		if configProviders[p.Name()] {
			emit("Scanning " + p.Name() + "…")
		}
	}

	// Emit one progress message per unique non-bulk provider with config tools.
	{
		seen := make(map[string]bool)
		for _, t := range tools {
			opProvider := a.operationProviderName(t)
			_, hasBulk := installedMaps[opProvider]
			_, hasMulti := multiMaps[opProvider]
			if t.InstallWith != "" && opProvider == t.Provider {
				hasBulk = false
				hasMulti = false
			}
			if !hasBulk && !hasMulti && !seen[opProvider] {
				seen[opProvider] = true
				emit("Scanning " + opProvider + "…")
			}
		}
	}

	upserts := make([]*database.ToolCache, 0, len(tools))
	for _, t := range tools {
		keys := toolEntryLookupKeys(t)

		opProvider := a.operationProviderName(t)
		if owner := cachedOwners[resolvedToolKey(t)]; owner != "" && owner != opProvider && t.InstallWith == "" {
			if upsert, handled := a.refreshWithCachedOwner(ctx, t, keys, owner, installedMaps); handled {
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
			upserts = append(upserts, &database.ToolCache{
				Name:          t.Name,
				Provider:      t.Provider,
				Package:       t.EffectivePackage(),
				Installed:     installed,
				InstalledWith: concreteForBulk[opProvider],
				Version:       sql.NullString{String: ver, Valid: ver != ""},
				LastChecked:   time.Now(),
			})
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
	if err := a.readDB().UpsertBatch(ctx, upserts); err != nil {
		return fmt.Errorf("upserting installed status: %w", err)
	}
	if err := a.reconcileResolvedTools(ctx, tools); err != nil {
		return err
	}
	return nil
}

// RefreshProviderInstalled updates the DB install status for all configured tools
// belonging to a single named provider. It mirrors the inner logic of
// RefreshInstalled but scoped to one provider so callers can run providers in
// parallel with independent timeouts. Best-effort: errors are silently skipped.
func (a *App) RefreshProviderInstalled(ctx context.Context, provName string) error {
	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	tools, _ := a.resolvedToolEntries(ctx, cfg, cfg.Groups)
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
	if err != nil || !avail {
		return nil
	}

	if mbc, ok := p.(provider.MultiManagerBulkChecker); ok {
		entries, err := mbc.InstalledByManager(ctx)
		if err != nil {
			return nil
		}
		for _, t := range provTools {
			keys := toolEntryLookupKeys(t)
			if owner := cachedOwners[resolvedToolKey(t)]; owner != "" && owner != provName && t.InstallWith == "" {
				if upsert, handled := a.refreshWithCachedOwner(ctx, t, keys, owner, nil); handled {
					if upsert == nil {
						continue
					}
					if err := a.readDB().Upsert(ctx, upsert); err != nil {
						return fmt.Errorf("upserting installed status for %s/%s: %w", t.Provider, t.Name, err)
					}
					continue
				}
			}
			if t.InstallWith != "" && a.operationProviderName(t) == t.Provider {
				installed, ver, err := a.isInstalledWithEntry(ctx, p, provName, t)
				if err != nil {
					continue
				}
				if err := a.readDB().Upsert(ctx, &database.ToolCache{
					Name:          t.Name,
					Provider:      t.Provider,
					Package:       t.EffectivePackage(),
					Installed:     installed,
					InstalledWith: t.InstallWith,
					Version:       sql.NullString{String: ver, Valid: ver != ""},
					LastChecked:   time.Now(),
				}); err != nil {
					return fmt.Errorf("upserting installed status for %s/%s: %w", t.Provider, t.Name, err)
				}
				continue
			}
			entry := provider.LookupInstalledEntry(entries, keys)
			installed := entry.ConcreteManager != ""
			if err := a.readDB().Upsert(ctx, &database.ToolCache{
				Name:          t.Name,
				Provider:      t.Provider,
				Package:       t.EffectivePackage(),
				Installed:     installed,
				InstalledWith: entry.ConcreteManager,
				Version:       sql.NullString{String: entry.Version, Valid: entry.Version != ""},
				LastChecked:   time.Now(),
			}); err != nil {
				return fmt.Errorf("upserting installed status for %s/%s: %w", t.Provider, t.Name, err)
			}
		}
		return nil
	}

	// BulkChecker path: single backend, bulk map lookup.
	// On error fall through to per-tool slow path so a partial failure (e.g. a
	// ecosystem provider whose delegate doesn't support bulk) doesn't skip all tools.
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
			for _, t := range provTools {
				keys := toolEntryLookupKeys(t)
				if owner := cachedOwners[resolvedToolKey(t)]; owner != "" && owner != provName && t.InstallWith == "" {
					if upsert, handled := a.refreshWithCachedOwner(ctx, t, keys, owner, nil); handled {
						if upsert == nil {
							continue
						}
						if err := a.readDB().Upsert(ctx, upsert); err != nil {
							return fmt.Errorf("upserting installed status for %s/%s: %w", t.Provider, t.Name, err)
						}
						continue
					}
				}
				ver, installed := provider.LookupString(m, keys)
				if err := a.readDB().Upsert(ctx, &database.ToolCache{
					Name:          t.Name,
					Provider:      t.Provider,
					Package:       t.EffectivePackage(),
					Installed:     installed,
					InstalledWith: installedWith,
					Version:       sql.NullString{String: ver, Valid: ver != ""},
					LastChecked:   time.Now(),
				}); err != nil {
					return fmt.Errorf("upserting installed status for %s/%s: %w", t.Provider, t.Name, err)
				}
			}
			return nil
		}
		// InstalledMap failed — fall through to per-tool slow path.
	}

	// Slow path: per-tool IsInstalled. Used when provider has no BulkChecker,
	// or when BulkChecker.InstalledMap returned an error.
	for _, t := range provTools {
		if owner := cachedOwners[resolvedToolKey(t)]; owner != "" && owner != provName && t.InstallWith == "" {
			if upsert, handled := a.refreshWithCachedOwner(ctx, t, toolEntryLookupKeys(t), owner, nil); handled {
				if upsert == nil {
					continue
				}
				if err := a.readDB().Upsert(ctx, upsert); err != nil {
					return fmt.Errorf("upserting installed status for %s/%s: %w", t.Provider, t.Name, err)
				}
				continue
			}
		}
		installed, ver, err := a.isInstalledWithEntry(ctx, p, provName, t)
		if err != nil {
			continue
		}
		installedWith := installedWithForOperation(ctx, p, provName, t.InstallWith)
		if err := a.readDB().Upsert(ctx, &database.ToolCache{
			Name:          t.Name,
			Provider:      t.Provider,
			Package:       t.EffectivePackage(),
			Installed:     installed,
			InstalledWith: installedWith,
			Version:       sql.NullString{String: ver, Valid: ver != ""},
			LastChecked:   time.Now(),
		}); err != nil {
			return fmt.Errorf("upserting installed status for %s/%s: %w", t.Provider, t.Name, err)
		}
	}
	return nil
}

// RefreshDiscovered scans all available providers whose concrete delegates are
// not already iterated separately, then stores locally-installed tools in the DB
// as tracked=false entries. Config-tracked rows are never overwritten.
// Best-effort: providers that error are silently skipped.
func (a *App) RefreshDiscovered(ctx context.Context) error {
	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}

	cutoff := time.Now()
	discovered := a.discoverUntrackedInstalled(ctx, cfg)

	if err := a.readDB().UpsertDiscoveredBatch(ctx, discovered); err != nil {
		return fmt.Errorf("upserting discovered tools: %w", err)
	}

	if err := a.readDB().PruneDiscovered(ctx, cutoff); err != nil {
		return fmt.Errorf("pruning discovered tools: %w", err)
	}
	return nil
}

func (a *App) previewDiscovered(ctx context.Context) ([]*database.ToolCache, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	discovered := a.discoverUntrackedInstalled(ctx, cfg)
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

func (a *App) discoverUntrackedInstalled(ctx context.Context, cfg *config.RootConfig) []database.DiscoveredUpsert {
	// Build name-only set of tools already declared in config.
	configuredNames := make(map[string]struct{})
	for name := range cfg.Tools {
		configuredNames[name] = struct{}{}
	}

	// Build reverse ecosystem map (concrete → ecosystem) so discovered tools get
	// the ecosystem provider name as their config provider label.
	revEcosystem := make(map[string]string)
	for eco, concrete := range a.ResolvedEcosystemProviders(ctx) {
		revEcosystem[concrete] = eco
	}

	// Collect discovered upserts across all providers. forEachAvailable is serial
	// so the slice is captured without a lock.
	var discovered []database.DiscoveredUpsert
	_ = a.forEachAvailable(ctx, func(p provider.Provider) error {
		if a.registry.ImportSkipsProvider(p.Name()) {
			return nil // skip ecosystem providers whose concrete delegates are iterated
		}

		configProvider := p.Name()
		if eco, ok := revEcosystem[p.Name()]; ok {
			configProvider = eco
		}

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
				discovered = append(discovered, database.DiscoveredUpsert{
					Name:          name,
					Provider:      configProvider,
					InstalledWith: entry.ConcreteManager,
					Version:       entry.Version,
				})
			}
			return nil
		}

		// Standard path: ListInstalled returns tools without per-manager attribution.
		installed, err := p.ListInstalled(ctx)
		if err != nil {
			return nil // best-effort: skip erroring providers
		}
		for _, t := range installed {
			if _, ok := configuredNames[t.Name]; ok {
				continue // already in config; skip
			}
			discovered = append(discovered, database.DiscoveredUpsert{
				Name:          t.Name,
				Provider:      configProvider,
				InstalledWith: p.Name(),
				Version:       t.Version,
			})
		}
		return nil
	})
	return discovered
}

// ListDiscovered returns all tool entries that are installed locally but not
// declared in config (tracked=false).
func (a *App) ListDiscovered(ctx context.Context) ([]*database.ToolCache, error) {
	return a.readDB().ListDiscovered(ctx)
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
			r.Provider = a.searchResultConfigProvider(resultProvider)
			results = append(results, r)
		}
	}
	return results, errors.Join(errs...)
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

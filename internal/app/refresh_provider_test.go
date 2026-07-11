package app_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

// ─── RefreshProviderOutdated ──────────────────────────────────────────────────

// provOutdatedStub extends stubProvider with the OutdatedChecker interface.
// (Distinct from outdatedStub in config_test.go which uses field "outdated".)
type provOutdatedStub struct {
	stubProvider
	outdatedMap map[string]string // lowercase name → latest version
	outdatedErr error
}

func (s *provOutdatedStub) OutdatedMap(_ context.Context) (map[string]string, error) {
	if s.outdatedErr != nil {
		return nil, s.outdatedErr
	}
	return s.outdatedMap, nil
}

// metadataRefreshStub extends provOutdatedStub with MetadataRefresher, counting
// how often RefreshMetadata is invoked so tests can assert the user-initiated
// gate (refreshMetadata bool) actually controls the index refresh.
type metadataRefreshStub struct {
	provOutdatedStub
	refreshes int32
}

func (s *metadataRefreshStub) RefreshMetadata(_ context.Context) error {
	atomic.AddInt32(&s.refreshes, 1)
	return nil
}

type managerOutdatedStub struct {
	stubProvider
	byManager map[string]map[string]string
	err       error
}

func (s *managerOutdatedStub) OutdatedMap(_ context.Context) (map[string]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make(map[string]string)
	for _, m := range s.byManager {
		for name, latest := range m {
			if _, exists := out[name]; !exists {
				out[name] = latest
			}
		}
	}
	return out, nil
}

func (s *managerOutdatedStub) OutdatedByManager(_ context.Context) (map[string]map[string]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.byManager, nil
}

type managerOutdatedInfoStub struct {
	stubProvider
	byManager map[string]map[string]provider.OutdatedInfo
	err       error
}

func (s *managerOutdatedInfoStub) OutdatedMap(_ context.Context) (map[string]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make(map[string]string)
	for _, m := range s.byManager {
		for name, info := range m {
			if _, exists := out[name]; !exists {
				out[name] = info.LatestVersion
			}
		}
	}
	return out, nil
}

func (s *managerOutdatedInfoStub) OutdatedInfoByManager(_ context.Context) (map[string]map[string]provider.OutdatedInfo, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.byManager, nil
}

// selfUnupgradeableStub is an outdated-reporting provider whose own package
// cannot be upgraded (PEP 668 externally managed), exercising suppression.
type selfUnupgradeableStub struct {
	provOutdatedStub
	upgradeable bool
}

func (s *selfUnupgradeableStub) SelfPackageName() string                       { return "pip" }
func (s *selfUnupgradeableStub) SelfPackageUpgradeable(_ context.Context) bool { return s.upgradeable }

func TestRefreshOutdated_SuppressesSelfUnupgradeableManager(t *testing.T) {
	prov := &selfUnupgradeableStub{
		provOutdatedStub: provOutdatedStub{
			stubProvider: stubProvider{name: "pip", available: true},
			outdatedMap:  map[string]string{"pip": "25.0"},
		},
		upgradeable: false,
	}
	a, _ := newImportApp(t, prov)
	if err := a.DB().Upsert(context.Background(), &database.ToolCache{
		Name: "pip", Provider: "pip", Package: "pip",
		Installed: true, InstalledWith: "pip", LastChecked: time.Now(),
	}); err != nil {
		t.Fatalf("db.Upsert: %v", err)
	}

	if err := a.RefreshOutdated(context.Background(), false, nil); err != nil {
		t.Fatalf("RefreshOutdated: %v", err)
	}

	got, err := a.DB().Get(context.Background(), "pip", "pip", "pip")
	if err != nil {
		t.Fatalf("get pip: %v", err)
	}
	if got.Outdated {
		t.Errorf("pip.Outdated = true, want false (manager cannot self-upgrade under PEP 668)")
	}
}

func TestRefreshOutdated_KeepsSelfUpgradeableManagerOutdated(t *testing.T) {
	prov := &selfUnupgradeableStub{
		provOutdatedStub: provOutdatedStub{
			stubProvider: stubProvider{name: "pip", available: true},
			outdatedMap:  map[string]string{"pip": "25.0"},
		},
		upgradeable: true,
	}
	a, _ := newImportApp(t, prov)
	if err := a.DB().Upsert(context.Background(), &database.ToolCache{
		Name: "pip", Provider: "pip", Package: "pip",
		Installed: true, InstalledWith: "pip", LastChecked: time.Now(),
	}); err != nil {
		t.Fatalf("db.Upsert: %v", err)
	}

	if err := a.RefreshOutdated(context.Background(), false, nil); err != nil {
		t.Fatalf("RefreshOutdated: %v", err)
	}

	got, err := a.DB().Get(context.Background(), "pip", "pip", "pip")
	if err != nil {
		t.Fatalf("get pip: %v", err)
	}
	if !got.Outdated {
		t.Errorf("pip.Outdated = false, want true (manager can self-upgrade on this host)")
	}
}

// TestRefreshProviderOutdated_UnknownProvider verifies that an unregistered
// provider name is silently skipped (returns nil, no panic).
func TestRefreshProviderOutdated_UnknownProvider(t *testing.T) {
	a, _ := newImportApp(t)
	if err := a.RefreshProviderOutdated(context.Background(), "nonexistent", false); err != nil {
		t.Errorf("RefreshProviderOutdated with unknown provider: %v", err)
	}
}

// TestRefreshProviderOutdated_ProviderNotOutdatedChecker verifies that a
// provider registered without OutdatedChecker is silently skipped.
func TestRefreshProviderOutdated_ProviderNotOutdatedChecker(t *testing.T) {
	prov := &stubProvider{name: "brew", available: true}
	a, _ := newImportApp(t, prov)
	if err := a.RefreshProviderOutdated(context.Background(), "brew", false); err != nil {
		t.Errorf("RefreshProviderOutdated non-OutdatedChecker: %v", err)
	}
}

// TestRefreshProviderOutdated_UnavailableProvider verifies that when the
// provider is not available, the function returns nil without querying.
func TestRefreshProviderOutdated_UnavailableProvider(t *testing.T) {
	prov := &provOutdatedStub{
		stubProvider: stubProvider{name: "brew", available: false},
		outdatedMap:  map[string]string{"ripgrep": "15.0.0"},
	}
	a, _ := newImportApp(t, prov)
	if err := a.RefreshProviderOutdated(context.Background(), "brew", false); err != nil {
		t.Errorf("RefreshProviderOutdated unavailable: %v", err)
	}
}

func TestRefreshProviderOutdated_ReturnsOutdatedMapError(t *testing.T) {
	prov := &provOutdatedStub{
		stubProvider: stubProvider{name: "brew", available: true},
		outdatedErr:  errors.New("outdated command failed"),
	}
	a, cfgPath := newImportApp(t, prov)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	err := a.RefreshProviderOutdated(context.Background(), "brew", false)
	if err == nil {
		t.Fatal("RefreshProviderOutdated returned nil, want provider error")
	}
	if !strings.Contains(err.Error(), "outdated command failed") {
		t.Fatalf("RefreshProviderOutdated error = %q, want provider failure", err)
	}
}

// TestRefreshProviderOutdated_NoOutdatedTools verifies the happy path where the
// provider is registered, available, implements OutdatedChecker, and the
// outdated map is empty (nothing to update).
func TestRefreshProviderOutdated_NoOutdatedTools(t *testing.T) {
	prov := &provOutdatedStub{
		stubProvider: stubProvider{name: "brew", available: true},
		outdatedMap:  map[string]string{}, // no outdated tools
	}
	a, cfgPath := newImportApp(t, prov)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.RefreshProviderOutdated(context.Background(), "brew", false); err != nil {
		t.Errorf("RefreshProviderOutdated (empty map): %v", err)
	}
}

func newMetadataRefreshApp(t *testing.T) (*app.App, *metadataRefreshStub) {
	t.Helper()
	prov := &metadataRefreshStub{provOutdatedStub: provOutdatedStub{
		stubProvider: stubProvider{name: "brew", available: true},
		outdatedMap:  map[string]string{},
	}}
	a, cfgPath := newImportApp(t, prov)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{Tools: groupTools("ripgrep")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	return a, prov
}

// TestRefreshProviderOutdated_RefreshMetadataTrue verifies a user-initiated
// refresh refreshes the provider's local index before checking outdated status.
func TestRefreshProviderOutdated_RefreshMetadataTrue(t *testing.T) {
	a, prov := newMetadataRefreshApp(t)
	if err := a.RefreshProviderOutdated(context.Background(), "brew", true); err != nil {
		t.Fatalf("RefreshProviderOutdated: %v", err)
	}
	if got := atomic.LoadInt32(&prov.refreshes); got != 1 {
		t.Fatalf("RefreshMetadata calls = %d, want 1", got)
	}
}

// TestRefreshProviderOutdated_RefreshMetadataFalse verifies a passive scan does
// not pay the index-refresh cost.
func TestRefreshProviderOutdated_RefreshMetadataFalse(t *testing.T) {
	a, prov := newMetadataRefreshApp(t)
	if err := a.RefreshProviderOutdated(context.Background(), "brew", false); err != nil {
		t.Fatalf("RefreshProviderOutdated: %v", err)
	}
	if got := atomic.LoadInt32(&prov.refreshes); got != 0 {
		t.Fatalf("RefreshMetadata calls = %d, want 0", got)
	}
}

// TestRefreshProviderOutdated_MarksOutdated verifies that when a tool is in
// the outdated map, its DB entry is updated.
func TestRefreshProviderOutdated_MarksOutdated(t *testing.T) {
	prov := &provOutdatedStub{
		stubProvider: stubProvider{name: "brew", available: true},
		outdatedMap:  map[string]string{"ripgrep": "15.0.0"},
	}
	a, cfgPath := newImportApp(t, prov)

	// Seed DB with the tool first.
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.RefreshInstalled(context.Background(), nil); err != nil {
		t.Fatalf("RefreshInstalled seed: %v", err)
	}

	if err := a.RefreshProviderOutdated(context.Background(), "brew", false); err != nil {
		t.Errorf("RefreshProviderOutdated: %v", err)
	}

	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("expected tool in DB after seed")
	}
	if !tools[0].Outdated {
		t.Errorf("ripgrep.Outdated = false, want true after RefreshProviderOutdated")
	}
	if tools[0].LatestVersion.String != "15.0.0" {
		t.Errorf("LatestVersion = %q, want 15.0.0", tools[0].LatestVersion.String)
	}
}

func TestRefreshProviderOutdated_UsesInstalledWithManager(t *testing.T) {
	prov := &managerOutdatedStub{
		stubProvider: stubProvider{name: "node", available: true},
		byManager: map[string]map[string]string{
			"npm":  {"typescript": "5.4.0"},
			"pnpm": {},
		},
	}
	a, _ := newImportApp(t, prov)
	now := time.Now()
	for _, tc := range []*database.ToolCache{
		{Name: "typescript", Provider: "node", Package: "typescript", Installed: true, InstalledWith: "npm", LastChecked: now},
		{Name: "prettier", Provider: "node", Package: "prettier", Installed: true, InstalledWith: "pnpm", Outdated: true, LastChecked: now},
	} {
		if err := a.DB().Upsert(context.Background(), tc); err != nil {
			t.Fatalf("db.Upsert: %v", err)
		}
	}

	if err := a.RefreshProviderOutdated(context.Background(), "node", false); err != nil {
		t.Fatalf("RefreshProviderOutdated: %v", err)
	}

	typescript, err := a.DB().Get(context.Background(), "typescript", "node", "typescript")
	if err != nil {
		t.Fatalf("get typescript: %v", err)
	}
	prettier, err := a.DB().Get(context.Background(), "prettier", "node", "prettier")
	if err != nil {
		t.Fatalf("get prettier: %v", err)
	}
	if !typescript.Outdated || typescript.LatestVersion.String != "5.4.0" {
		t.Fatalf("typescript outdated/latest = %v/%q, want true/5.4.0", typescript.Outdated, typescript.LatestVersion.String)
	}
	if prettier.Outdated {
		t.Fatalf("prettier.Outdated = true, want false because pnpm map had no update")
	}
}

func TestRefreshProviderOutdated_PersistsManagerUpdateMetadata(t *testing.T) {
	availableAt := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	prov := &managerOutdatedInfoStub{
		stubProvider: stubProvider{name: "node", available: true},
		byManager: map[string]map[string]provider.OutdatedInfo{
			"npm": {
				"typescript": {
					LatestVersion: "5.4.0",
					AvailableAt:   &availableAt,
					DateSource:    "npm_registry_time",
				},
			},
		},
	}
	a, _ := newImportApp(t, prov)
	ctx := context.Background()
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "typescript",
		Provider:      "node",
		Package:       "typescript",
		Installed:     true,
		InstalledWith: "npm",
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("db.Upsert: %v", err)
	}

	if err := a.RefreshProviderOutdated(ctx, "node", false); err != nil {
		t.Fatalf("RefreshProviderOutdated: %v", err)
	}

	metadata, err := a.DB().GetUpdateMetadata(ctx, "npm", "typescript", "5.4.0")
	if err != nil {
		t.Fatalf("GetUpdateMetadata: %v", err)
	}
	if !metadata.AvailableAt.Equal(availableAt) || metadata.DateSource != "npm_registry_time" {
		t.Fatalf("metadata = %+v, want npm_registry_time at %s", metadata, availableAt)
	}
}

// onlyManagerOutdatedInfoStub mirrors the real node/python provider shape:
// OutdatedInfoMap (satisfying plain OutdatedInfoChecker) is a convenience
// wrapper that merges OutdatedInfoByManager's per-manager results into one
// flat, first-writer-wins map by package name, exactly like
// node.Provider.OutdatedInfoMap does. This is what lets the bug hide: even
// without ManagerOutdatedInfoChecker forwarding, namedProvider's own
// OutdatedInfoMap method still finds a real (but manager-unattributed and
// therefore cross-contaminated) result from the base.
type onlyManagerOutdatedInfoStub struct {
	stubProvider
	byManager map[string]map[string]provider.OutdatedInfo
}

func (s *onlyManagerOutdatedInfoStub) OutdatedInfoByManager(_ context.Context) (map[string]map[string]provider.OutdatedInfo, error) {
	return s.byManager, nil
}

func (s *onlyManagerOutdatedInfoStub) OutdatedInfoMap(_ context.Context) (map[string]provider.OutdatedInfo, error) {
	result := make(map[string]provider.OutdatedInfo)
	for _, m := range s.byManager {
		for name, info := range m {
			if _, exists := result[name]; !exists {
				result[name] = info
			}
		}
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

// TestRefreshProviderOutdated_NamedAliasPreservesManagerAttribution guards
// against a regression where provider.Named-wrapped aliases (bun, pnpm, uv,
// ...) lost ManagerOutdatedInfoChecker because Named embedded the base as the
// Provider interface, so the optional method wasn't promoted.
// outdatedMapsForProvider then fell through to the wrapper's own concrete
// OutdatedMap/OutdatedInfoMap methods (which always structurally satisfy
// OutdatedChecker/OutdatedInfoChecker but return nil when the base lacks
// those), losing per-manager attribution entirely. outdatedForTool then
// matched by package name only across every concrete manager flattened
// together: a same-named package already current under pnpm got contaminated
// by npm's outdated entry for typescript, wrongly flipping the pnpm-owned row
// to Outdated=true even though its own manager (pnpm) has no update.
func TestRefreshProviderOutdated_NamedAliasPreservesManagerAttribution(t *testing.T) {
	base := &onlyManagerOutdatedInfoStub{
		stubProvider: stubProvider{name: "node", available: true},
		byManager: map[string]map[string]provider.OutdatedInfo{
			"npm":  {"typescript": {LatestVersion: "5.5.0"}},
			"pnpm": {},
		},
	}
	// Mirror app.go's real registration: bun/pnpm/npm are separate Named
	// aliases wrapping the same underlying node provider.
	bun := provider.Named("bun", base)
	pnpm := provider.Named("pnpm", base)
	a, _ := newImportApp(t, bun, pnpm)
	ctx := context.Background()
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "typescript",
		Provider:      "pnpm",
		Package:       "typescript",
		Installed:     true,
		InstalledWith: "pnpm",
		Outdated:      false,
		LatestVersion: sql.NullString{},
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("db.Upsert: %v", err)
	}

	if err := a.RefreshOutdated(ctx, false, nil); err != nil {
		t.Fatalf("RefreshOutdated: %v", err)
	}

	typescript, err := a.DB().Get(ctx, "typescript", "pnpm", "typescript")
	if err != nil {
		t.Fatalf("get typescript: %v", err)
	}
	if typescript.Outdated {
		t.Fatalf("typescript.Outdated = true, want false: pnpm has no update for it, only npm does, and manager attribution must keep them separate")
	}
}

func TestRefreshProviderOutdated_UsesFullSlashPackage(t *testing.T) {
	prov := &managerOutdatedStub{
		stubProvider: stubProvider{name: "node", available: true},
		byManager: map[string]map[string]string{
			"npm": {
				"@playwright/test": "1.53.0",
				"test":             "0.0.1",
			},
		},
	}
	a, _ := newImportApp(t, prov)
	now := time.Now()
	if err := a.DB().Upsert(context.Background(), &database.ToolCache{
		Name:          "playwright-test",
		Provider:      "node",
		Package:       "@playwright/test",
		Installed:     true,
		InstalledWith: "npm",
		LastChecked:   now,
	}); err != nil {
		t.Fatalf("db.Upsert: %v", err)
	}

	if err := a.RefreshProviderOutdated(context.Background(), "node", false); err != nil {
		t.Fatalf("RefreshProviderOutdated: %v", err)
	}

	got, err := a.DB().Get(context.Background(), "playwright-test", "node", "@playwright/test")
	if err != nil {
		t.Fatalf("get playwright-test: %v", err)
	}
	if !got.Outdated || got.LatestVersion.String != "1.53.0" {
		t.Fatalf("outdated/latest = %v/%q, want true/1.53.0", got.Outdated, got.LatestVersion.String)
	}
}

func TestRefreshProviderOutdated_UsesRegisteredInstalledWithOwner(t *testing.T) {
	brew := &provOutdatedStub{
		stubProvider: stubProvider{name: "brew", available: true},
		outdatedMap:  map[string]string{"ripgrep": "15.0.0"},
	}
	system := &stubProvider{name: "system", available: true}
	a, _ := newImportApp(t, brew, system)
	ctx := context.Background()
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "ripgrep",
		Provider:      "system",
		Package:       "ripgrep",
		Installed:     true,
		InstalledWith: "brew",
		Tracked:       true,
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	if err := a.RefreshProviderOutdated(ctx, "system", false); err != nil {
		t.Fatalf("RefreshProviderOutdated: %v", err)
	}

	got, err := a.DB().Get(ctx, "ripgrep", "system", "ripgrep")
	if err != nil {
		t.Fatalf("cache get: %v", err)
	}
	if !got.Outdated || got.LatestVersion.String != "15.0.0" {
		t.Fatalf("outdated/latest = %v/%q, want true/15.0.0", got.Outdated, got.LatestVersion.String)
	}
}

// ─── RefreshProviderInstalled ─────────────────────────────────────────────────

// TestRefreshProviderInstalled_UnknownProvider verifies that an unregistered
// provider is silently skipped.
func TestRefreshProviderInstalled_UnknownProvider(t *testing.T) {
	a, _ := newImportApp(t)
	if err := a.RefreshProviderInstalled(context.Background(), "nonexistent"); err != nil {
		t.Errorf("RefreshProviderInstalled unknown provider: %v", err)
	}
}

// TestRefreshProviderInstalled_NoTools verifies that when there are no config
// tools for the named provider, the function returns nil immediately.
func TestRefreshProviderInstalled_NoTools(t *testing.T) {
	prov := &bulkCheckingStub{
		stubProvider: stubProvider{name: "brew", available: true},
		bulk:         map[string]string{"ripgrep": "14.1.0"},
	}
	a, cfgPath := newImportApp(t, prov)

	// Config has no tools at all.
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Groups: []*config.GroupConfig{},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.RefreshProviderInstalled(context.Background(), "brew"); err != nil {
		t.Errorf("RefreshProviderInstalled (no tools): %v", err)
	}
}

// TestRefreshProviderInstalled_BulkPath_MarksInstalled tests the BulkChecker
// path of RefreshProviderInstalled with an installed tool.
func TestRefreshProviderInstalled_BulkPath_MarksInstalled(t *testing.T) {
	prov := &bulkCheckingStub{
		stubProvider: stubProvider{name: "brew", available: true},
		bulk:         map[string]string{"ripgrep": "14.1.0"},
	}
	a, cfgPath := newImportApp(t, prov)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.RefreshProviderInstalled(context.Background(), "brew"); err != nil {
		t.Fatalf("RefreshProviderInstalled: %v", err)
	}

	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(tools))
	}
	if !tools[0].Installed {
		t.Errorf("ripgrep.Installed = false, want true")
	}
	if tools[0].Version.String != "14.1.0" {
		t.Errorf("ripgrep.Version = %q, want 14.1.0", tools[0].Version.String)
	}
}

func TestRefreshProviderInstalledWithProgress_ReportsEachTool(t *testing.T) {
	prov := &bulkCheckingStub{
		stubProvider: stubProvider{name: "brew", available: true},
		bulk:         map[string]string{"git": "2.45.0", "ripgrep": "14.1.0"},
	}
	a, cfgPath := newImportApp(t, prov)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("git", "brew"),
		),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep", "git"),
		}},
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	var events []app.RefreshInstalledProgressEvent
	if err := a.RefreshProviderInstalledWithProgress(context.Background(), "brew", func(event app.RefreshInstalledProgressEvent) {
		events = append(events, event)
	}); err != nil {
		t.Fatalf("RefreshProviderInstalledWithProgress: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("events len = %d, want 2: %#v", len(events), events)
	}
	for i, event := range events {
		if event.Provider != "brew" || event.Index != i+1 || event.Total != 2 {
			t.Fatalf("event[%d] = %#v, want brew %d/2", i, event, i+1)
		}
	}
}

func TestRefreshProviderInstalled_UsesCachedConcreteOwnerMetadata(t *testing.T) {
	brew, apt := cachedOwnerMetadataProviders()
	a, cfgPath := newImportApp(t, brew, apt)
	ctx := context.Background()

	seedRipgrepBrewWithCachedAptOwner(t, a, cfgPath, ctx)

	if err := a.RefreshProviderInstalled(ctx, "brew"); err != nil {
		t.Fatalf("RefreshProviderInstalled: %v", err)
	}

	assertRipgrepBrewOwnedByAptMetadata(t, a, cfgPath, ctx)
}

func TestRefreshProviderInstalled_UsesCachedConcreteOwnerSlowPath(t *testing.T) {
	brew, apt := cachedOwnerSlowPathProviders()
	a, cfgPath := newImportApp(t, brew, apt)
	ctx := context.Background()

	seedRipgrepBrewWithCachedAptOwner(t, a, cfgPath, ctx)

	if err := a.RefreshProviderInstalled(ctx, "brew"); err != nil {
		t.Fatalf("RefreshProviderInstalled: %v", err)
	}

	assertRipgrepBrewCache(t, a, ctx, "apt", "14.1.2")
}

func cachedOwnerSlowPathProviders() (*stubProvider, *stubProvider) {
	brew := &stubProvider{name: "brew", available: true}
	apt := &stubProvider{
		name:      "apt",
		available: true,
		installed: []provider.InstalledTool{{
			Tool:    provider.Tool{Name: "ripgrep", Provider: "apt"},
			Version: "14.1.2",
		}},
	}
	return brew, apt
}

func TestRefreshProviderInstalled_FallsBackWhenCachedOwnerUnavailable(t *testing.T) {
	brew := &bulkCheckingStub{
		stubProvider: stubProvider{name: "brew", available: true},
		bulk:         map[string]string{"ripgrep": "14.2.0"},
	}
	apt := &stubProvider{name: "apt", available: false}
	a, cfgPath := newImportApp(t, brew, apt)
	ctx := context.Background()

	seedRipgrepBrewWithCachedAptOwner(t, a, cfgPath, ctx)

	if err := a.RefreshProviderInstalled(ctx, "brew"); err != nil {
		t.Fatalf("RefreshProviderInstalled: %v", err)
	}

	assertRipgrepBrewCache(t, a, ctx, "brew", "14.2.0")
}

func cachedOwnerMetadataProviders() (*stubProvider, *metadataCheckingStub) {
	brew := &stubProvider{name: "brew", available: true}
	apt := &metadataCheckingStub{
		stubProvider: stubProvider{name: "apt", available: true},
		metadata: map[string]provider.InstalledMetadata{
			"ripgrep": {
				Version: "14.1.1",
				Privilege: provider.PrivilegePlan{
					Requirement: provider.PrivilegeRequired,
					Reason:      "apt requires administrator privileges",
				},
				Source: provider.SourceMetadata{
					Type:  provider.SourceTypeGitHub,
					Owner: "BurntSushi",
					Repo:  "ripgrep",
					URL:   "https://github.com/BurntSushi/ripgrep",
				},
			},
		},
	}
	return brew, apt
}

func seedRipgrepBrewWithCachedAptOwner(t *testing.T, a *app.App, cfgPath string, ctx context.Context) {
	t.Helper()
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "ripgrep",
		Provider:      "brew",
		Package:       "ripgrep",
		Installed:     true,
		InstalledWith: "apt",
	}); err != nil {
		t.Fatalf("seed cached owner: %v", err)
	}
}

func assertRipgrepBrewCache(t *testing.T, a *app.App, ctx context.Context, owner, version string) *database.ToolCache {
	t.Helper()
	got, err := a.DB().Get(ctx, "ripgrep", "brew", "ripgrep")
	if err != nil {
		t.Fatalf("Get ripgrep: %v", err)
	}
	if !got.Installed || got.InstalledWith != owner || got.Version.String != version {
		t.Fatalf("cache = installed:%v owner:%q version:%q, want true/%s/%s", got.Installed, got.InstalledWith, got.Version.String, owner, version)
	}
	return got
}

func assertRipgrepBrewOwnedByAptMetadata(t *testing.T, a *app.App, cfgPath string, ctx context.Context) {
	t.Helper()
	got := assertRipgrepBrewCache(t, a, ctx, "apt", "14.1.1")
	if got.Privilege != string(provider.PrivilegeRequired) || !got.PrivilegeReason.Valid {
		t.Fatalf("privilege = %q reason:%+v, want required with reason", got.Privilege, got.PrivilegeReason)
	}
	meta, err := a.DB().GetMetadata(ctx, "ripgrep", "brew", "ripgrep")
	if err != nil {
		t.Fatalf("GetMetadata ripgrep: %v", err)
	}
	if meta.SourceType != provider.SourceTypeGitHub || meta.SourceOwner != "BurntSushi" || meta.SourceRepo != "ripgrep" {
		t.Fatalf("metadata source = %s/%s/%s, want github/BurntSushi/ripgrep", meta.SourceType, meta.SourceOwner, meta.SourceRepo)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got := cfg.Tools["ripgrep"].Git; got != "https://github.com/BurntSushi/ripgrep" {
		t.Fatalf("tool git = %q, want cached source URL persisted", got)
	}
}

type cancelingBulkStub struct {
	bulkCheckingStub
	cancel context.CancelFunc
}

func (b *cancelingBulkStub) InstalledMap(ctx context.Context) (map[string]string, error) {
	if b.cancel != nil {
		b.cancel()
	}
	return b.bulkCheckingStub.InstalledMap(ctx)
}

type errorBulkStub struct {
	stubProvider
	err error
}

func (b *errorBulkStub) InstalledMap(_ context.Context) (map[string]string, error) {
	return nil, b.err
}

type countingBulkConcreteStub struct {
	stubProvider
	bulk         map[string]string
	concreteName string
	calls        int
}

func (b *countingBulkConcreteStub) InstalledMap(_ context.Context) (map[string]string, error) {
	b.calls++
	return b.bulk, nil
}

func (b *countingBulkConcreteStub) ResolvedName(_ context.Context) (string, error) {
	return b.concreteName, nil
}

func TestRefreshProviderInstalled_WritesAfterScanContextExpires(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	prov := &cancelingBulkStub{
		bulkCheckingStub: bulkCheckingStub{
			stubProvider: stubProvider{name: "brew", available: true},
			bulk:         map[string]string{"fd": "9.0.0"},
		},
		cancel: cancel,
	}
	a, cfgPath := newImportApp(t, prov)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("fd", "brew")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("fd"),
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.RefreshProviderInstalled(ctx, "brew"); err != nil {
		t.Fatalf("RefreshProviderInstalled: %v", err)
	}
	got, err := a.DB().Get(context.Background(), "fd", "brew", "fd")
	if err != nil {
		t.Fatalf("DB.Get: %v", err)
	}
	if !got.Installed || got.Version.String != "9.0.0" {
		t.Fatalf("installed/version = %v/%q, want true/9.0.0", got.Installed, got.Version.String)
	}
}

func TestRefreshProviderInstalled_ReturnsBulkScanDeadline(t *testing.T) {
	prov := &errorBulkStub{
		stubProvider: stubProvider{name: "brew", available: true},
		err:          context.DeadlineExceeded,
	}
	a, cfgPath := newImportApp(t, prov)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("fd", "brew")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("fd"),
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	err := a.RefreshProviderInstalled(context.Background(), "brew")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RefreshProviderInstalled error = %v, want context deadline", err)
	}
}

// TestRefreshProviderInstalled_BulkPath_MarksNotInstalled tests the BulkChecker
// path when the tool is absent from the bulk map.
func TestRefreshProviderInstalled_BulkPath_MarksNotInstalled(t *testing.T) {
	prov := &bulkCheckingStub{
		stubProvider: stubProvider{name: "brew", available: true},
		bulk:         map[string]string{}, // ripgrep not installed
	}
	a, cfgPath := newImportApp(t, prov)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.RefreshProviderInstalled(context.Background(), "brew"); err != nil {
		t.Fatalf("RefreshProviderInstalled: %v", err)
	}

	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(tools))
	}
	if tools[0].Installed {
		t.Errorf("ripgrep.Installed = true, want false (not in bulk map)")
	}
}

// TestRefreshProviderInstalled_SlowPath verifies the per-tool IsInstalled path
// when the provider has no BulkChecker.
func TestRefreshProviderInstalled_SlowPath(t *testing.T) {
	prov := &isInstalledStub{
		stubProvider:  stubProvider{name: "pip", available: true},
		installedName: "black",
		installedVer:  "24.3.0",
	}
	a, cfgPath := newImportApp(t, prov)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("black", "pip")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("black"),
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.RefreshProviderInstalled(context.Background(), "pip"); err != nil {
		t.Fatalf("RefreshProviderInstalled: %v", err)
	}

	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(tools))
	}
	if !tools[0].Installed {
		t.Errorf("black.Installed = false, want true (slow path)")
	}
}

// TestRefreshProviderInstalled_UnavailableProvider verifies that when a provider
// is not available, all tools for it are skipped.
func TestRefreshProviderInstalled_UnavailableProvider(t *testing.T) {
	prov := &bulkCheckingStub{
		stubProvider: stubProvider{name: "brew", available: false},
		bulk:         map[string]string{"ripgrep": "14.1.0"},
	}
	a, cfgPath := newImportApp(t, prov)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.RefreshProviderInstalled(context.Background(), "brew"); err != nil {
		t.Fatalf("RefreshProviderInstalled unavailable: %v", err)
	}

	// Provider unavailable — config-led row may show but must be not-installed
	// since nothing was upserted.
	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if anyToolInstalled(tools) {
		t.Errorf("tools = %+v, want none installed (provider unavailable)", tools)
	}
}

func TestRefreshProviderInstalled_MultiManagerPath_UsesFullSlashPackage(t *testing.T) {
	prov := &multiManagerStub{
		stubProvider: stubProvider{name: "npm", available: true},
		entries: map[string]provider.InstalledEntry{
			"@playwright/test": {Version: "1.52.0", ConcreteManager: "npm"},
			"test":             {Version: "0.0.1", ConcreteManager: "npm"},
		},
	}
	a, cfgPath := newImportApp(t, prov)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalToolPackage("playwright-test", "npm", "@playwright/test"),
		),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("playwright-test"),
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.RefreshProviderInstalled(context.Background(), "npm"); err != nil {
		t.Fatalf("RefreshProviderInstalled multi-manager: %v", err)
	}

	got, err := a.DB().Get(context.Background(), "playwright-test", "npm", "@playwright/test")
	if err != nil {
		t.Fatalf("Get playwright-test: %v", err)
	}
	if !got.Installed || got.InstalledWith != "npm" || got.Version.String != "1.52.0" {
		t.Fatalf("cache = installed:%v owner:%q version:%q, want true/npm/1.52.0", got.Installed, got.InstalledWith, got.Version.String)
	}
}

// ─── RefreshDiscovered ────────────────────────────────────────────────────────

// listInstalledStub extends stubProvider with a controlled ListInstalled response.
type listInstalledStub struct {
	stubProvider
	installed []provider.InstalledTool
}

func (s *listInstalledStub) ListInstalled(_ context.Context) ([]provider.InstalledTool, error) {
	return s.installed, nil
}

type cliFilteredListInstalledStub struct {
	listInstalledStub
	cliSet map[string]bool
}

func (s *cliFilteredListInstalledStub) CLIToolSet(context.Context) (map[string]bool, error) {
	return s.cliSet, nil
}

// TestRefreshDiscovered_PopulatesDB verifies that tools reported by
// ListInstalled are stored as discovered (tracked=false) entries.
func TestRefreshDiscovered_PopulatesDB(t *testing.T) {
	prov := &listInstalledStub{
		stubProvider: stubProvider{name: "brew", available: true},
		installed: []provider.InstalledTool{
			{Tool: provider.Tool{Name: "ripgrep", Provider: "brew"}, Version: "14.1.0"},
			{Tool: provider.Tool{Name: "git", Provider: "brew"}, Version: "2.45.0"},
		},
	}
	a, cfgPath := newImportApp(t, prov)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("git", "brew")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("git"),
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.RefreshDiscovered(context.Background()); err != nil {
		t.Fatalf("RefreshDiscovered: %v", err)
	}

	discovered, err := a.ListDiscovered(context.Background())
	if err != nil {
		t.Fatalf("ListDiscovered: %v", err)
	}
	if len(discovered) != 1 {
		t.Fatalf("got %d discovered tools, want 1", len(discovered))
	}
	if discovered[0].Name != "ripgrep" {
		t.Errorf("discovered name = %q, want ripgrep", discovered[0].Name)
	}
}

func TestRefreshDiscovered_ScansOnlyTrackedProviders(t *testing.T) {
	brew := &listInstalledStub{
		stubProvider: stubProvider{name: "brew", available: true},
		installed: []provider.InstalledTool{
			{Tool: provider.Tool{Name: "ripgrep", Provider: "brew"}, Version: "14.1.0"},
			{Tool: provider.Tool{Name: "jq", Provider: "brew"}, Version: "1.7.0"},
		},
	}
	pip := &listInstalledStub{
		stubProvider: stubProvider{name: "pip", available: true},
		installed: []provider.InstalledTool{
			{Tool: provider.Tool{Name: "black", Provider: "pip"}, Version: "24.4.0"},
		},
	}
	a, cfgPath := newImportApp(t, brew, pip)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.RefreshDiscovered(context.Background()); err != nil {
		t.Fatalf("RefreshDiscovered: %v", err)
	}

	discovered, err := a.ListDiscovered(context.Background())
	if err != nil {
		t.Fatalf("ListDiscovered: %v", err)
	}
	if len(discovered) != 1 || discovered[0].Name != "jq" {
		t.Fatalf("discovered = %+v, want only brew jq", discovered)
	}
}

func TestRefreshDiscovered_SkipsUnconfiguredPipPackages(t *testing.T) {
	pip := &cliFilteredListInstalledStub{
		listInstalledStub: listInstalledStub{
			stubProvider: stubProvider{name: "pip", available: true},
			installed: []provider.InstalledTool{
				{Tool: provider.Tool{Name: "asyncpg", Provider: "pip"}, Version: "0.31.0"},
				{Tool: provider.Tool{Name: "black", Provider: "pip"}, Version: "24.4.0"},
			},
		},
		cliSet: map[string]bool{"black": true},
	}
	a, cfgPath := newImportApp(t, pip)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("ruff", "pip")),
		Groups: []*config.GroupConfig{testHostToolGroup("ruff")},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.RefreshDiscovered(context.Background()); err != nil {
		t.Fatalf("RefreshDiscovered: %v", err)
	}

	discovered, err := a.ListDiscovered(context.Background())
	if err != nil {
		t.Fatalf("ListDiscovered: %v", err)
	}
	if len(discovered) != 0 {
		t.Fatalf("discovered = %+v, want no unconfigured pip packages", discovered)
	}
}

// TestRefreshDiscovered_SkipsConfiguredTools verifies that tools already
// declared in config are not added to the discovered set.
func TestRefreshDiscovered_SkipsConfiguredTools(t *testing.T) {
	prov := &listInstalledStub{
		stubProvider: stubProvider{name: "brew", available: true},
		installed: []provider.InstalledTool{
			{Tool: provider.Tool{Name: "ripgrep", Provider: "brew"}, Version: "14.1.0"},
			{Tool: provider.Tool{Name: "jq", Provider: "brew"}, Version: "1.7.0"},
		},
	}
	a, cfgPath := newImportApp(t, prov)

	// ripgrep is already in config — only jq should be discovered.
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.RefreshDiscovered(context.Background()); err != nil {
		t.Fatalf("RefreshDiscovered: %v", err)
	}

	discovered, err := a.ListDiscovered(context.Background())
	if err != nil {
		t.Fatalf("ListDiscovered: %v", err)
	}
	if len(discovered) != 1 {
		t.Fatalf("got %d discovered tools, want 1 (jq only)", len(discovered))
	}
	if discovered[0].Name != "jq" {
		t.Errorf("discovered name = %q, want jq", discovered[0].Name)
	}
}

// TestRefreshDiscovered_SkipsUnavailableProvider verifies that an unavailable
// provider is silently skipped.
func TestRefreshDiscovered_SkipsUnavailableProvider(t *testing.T) {
	prov := &listInstalledStub{
		stubProvider: stubProvider{name: "brew", available: false},
		installed: []provider.InstalledTool{
			{Tool: provider.Tool{Name: "ripgrep", Provider: "brew"}, Version: "14.1.0"},
		},
	}
	a, _ := newImportApp(t, prov)

	if err := a.RefreshDiscovered(context.Background()); err != nil {
		t.Fatalf("RefreshDiscovered: %v", err)
	}

	discovered, err := a.ListDiscovered(context.Background())
	if err != nil {
		t.Fatalf("ListDiscovered: %v", err)
	}
	if len(discovered) != 0 {
		t.Errorf("got %d discovered tools, want 0 (provider unavailable)", len(discovered))
	}
}

// bulkScanTracker holds shared atomic counters used by the concurrent bulk
// stubs below. Sharing a single tracker lets both stubs increment the same
// active counter, so the peak reflects true cross-stub overlap.
type bulkScanTracker struct {
	active  atomic.Int32
	maxSeen atomic.Int32
}

func (tr *bulkScanTracker) enter() {
	cur := tr.active.Add(1)
	for {
		prev := tr.maxSeen.Load()
		if cur <= prev || tr.maxSeen.CompareAndSwap(prev, cur) {
			break
		}
	}
}

func (tr *bulkScanTracker) exit() { tr.active.Add(-1) }

// concurrentBulkStub is a BulkChecker that records entry/exit in a shared
// tracker so TestRefreshInstalled_BulkScansRunConcurrently can assert overlap.
type concurrentBulkStub struct {
	stubProvider
	bulk    map[string]string
	delay   time.Duration
	tracker *bulkScanTracker
}

func (s *concurrentBulkStub) InstalledMap(_ context.Context) (map[string]string, error) {
	s.tracker.enter()
	time.Sleep(s.delay) // widen overlap window so both goroutines are active together
	s.tracker.exit()
	return s.bulk, nil
}

// concurrentConcreteStub adds ConcreteResolver to concurrentBulkStub.
// This exercises the rcMu-guarded resolvedConcrete read path under -race.
type concurrentConcreteStub struct {
	concurrentBulkStub
	concreteName string
}

func (s *concurrentConcreteStub) ResolvedName(_ context.Context) (string, error) {
	return s.concreteName, nil
}

// TestRefreshInstalled_BulkScansRunConcurrently verifies that RefreshInstalled
// fans out independent provider bulk scans in parallel. A shared atomic
// active-counter proves overlap: peak ≥ 2 means both InstalledMap calls were
// executing simultaneously. No wall-clock threshold is used.
//
// provB implements ConcreteResolver so the rcMu-guarded resolvedConcrete read
// inside the goroutine is exercised under -race.
func TestRefreshInstalled_BulkScansRunConcurrently(t *testing.T) {
	tracker := &bulkScanTracker{}

	provA := &concurrentBulkStub{
		stubProvider: stubProvider{name: "prov-a", available: true},
		bulk:         map[string]string{"tool-a": "1.0"},
		delay:        20 * time.Millisecond,
		tracker:      tracker,
	}
	provB := &concurrentConcreteStub{
		concurrentBulkStub: concurrentBulkStub{
			stubProvider: stubProvider{name: "prov-b", available: true},
			bulk:         map[string]string{"tool-b": "2.0"},
			delay:        20 * time.Millisecond,
			tracker:      tracker,
		},
		concreteName: "prov-b-concrete",
	}

	a, cfgPath := newImportApp(t, provA, provB)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"tool-a": {Providers: []config.ToolInstallSpec{{Provider: "prov-a"}}},
			"tool-b": {Providers: []config.ToolInstallSpec{{Provider: "prov-b"}}},
		},
		Groups: []*config.GroupConfig{{Tools: groupTools("tool-a", "tool-b")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.RefreshInstalled(context.Background(), nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	if got := tracker.maxSeen.Load(); got < 2 {
		t.Errorf("peak concurrent InstalledMap calls = %d, want ≥ 2: provider scans appear serial", got)
	}

	// Verify detection results are correct.
	gotA, err := a.DB().Get(context.Background(), "tool-a", "prov-a", "tool-a")
	if err != nil {
		t.Fatalf("DB.Get tool-a: %v", err)
	}
	if !gotA.Installed {
		t.Errorf("tool-a installed = false, want true")
	}
	gotB, err := a.DB().Get(context.Background(), "tool-b", "prov-b", "tool-b")
	if err != nil {
		t.Fatalf("DB.Get tool-b: %v", err)
	}
	if !gotB.Installed {
		t.Errorf("tool-b installed = false, want true")
	}
}

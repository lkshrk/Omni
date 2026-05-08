package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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

// TestRefreshProviderOutdated_UnknownProvider verifies that an unregistered
// provider name is silently skipped (returns nil, no panic).
func TestRefreshProviderOutdated_UnknownProvider(t *testing.T) {
	a, _ := newImportApp(t)
	if err := a.RefreshProviderOutdated(context.Background(), "nonexistent"); err != nil {
		t.Errorf("RefreshProviderOutdated with unknown provider: %v", err)
	}
}

// TestRefreshProviderOutdated_ProviderNotOutdatedChecker verifies that a
// provider registered without OutdatedChecker is silently skipped.
func TestRefreshProviderOutdated_ProviderNotOutdatedChecker(t *testing.T) {
	prov := &stubProvider{name: "brew", available: true}
	a, _ := newImportApp(t, prov)
	if err := a.RefreshProviderOutdated(context.Background(), "brew"); err != nil {
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
	if err := a.RefreshProviderOutdated(context.Background(), "brew"); err != nil {
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

	err := a.RefreshProviderOutdated(context.Background(), "brew")
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

	if err := a.RefreshProviderOutdated(context.Background(), "brew"); err != nil {
		t.Errorf("RefreshProviderOutdated (empty map): %v", err)
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

	if err := a.RefreshProviderOutdated(context.Background(), "brew"); err != nil {
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

	if err := a.RefreshProviderOutdated(context.Background(), "node"); err != nil {
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

	if err := a.RefreshProviderOutdated(context.Background(), "node"); err != nil {
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

	if err := a.RefreshProviderOutdated(ctx, "system"); err != nil {
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
			stubProvider: stubProvider{name: "system", available: true},
			bulk:         map[string]string{"fd": "9.0.0"},
		},
		cancel: cancel,
	}
	a, cfgPath := newImportApp(t, prov)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("fd", "system")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("fd"),
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.RefreshProviderInstalled(ctx, "system"); err != nil {
		t.Fatalf("RefreshProviderInstalled: %v", err)
	}
	got, err := a.DB().Get(context.Background(), "fd", "system", "fd")
	if err != nil {
		t.Fatalf("DB.Get: %v", err)
	}
	if !got.Installed || got.Version.String != "9.0.0" {
		t.Fatalf("installed/version = %v/%q, want true/9.0.0", got.Installed, got.Version.String)
	}
}

func TestRefreshProviderInstalled_ReturnsBulkScanDeadline(t *testing.T) {
	prov := &errorBulkStub{
		stubProvider: stubProvider{name: "system", available: true},
		err:          context.DeadlineExceeded,
	}
	a, cfgPath := newImportApp(t, prov)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("fd", "system")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("fd"),
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	err := a.RefreshProviderInstalled(context.Background(), "system")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RefreshProviderInstalled error = %v, want context deadline", err)
	}
}

func TestRefreshProviderInstalled_ReusesConcreteOwnerBulkMap(t *testing.T) {
	system := &countingBulkConcreteStub{
		stubProvider: stubProvider{name: "system", available: true},
		bulk:         map[string]string{"fd": "9.0.0"},
		concreteName: "brew",
	}
	brew := &countingBulkConcreteStub{
		stubProvider: stubProvider{name: "brew", available: true},
		bulk:         map[string]string{"fd": "9.0.0"},
	}
	a, cfgPath := newImportApp(t, system, brew)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("fd", "system")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("fd"),
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().Upsert(context.Background(), &database.ToolCache{
		Name:          "fd",
		Provider:      "system",
		Package:       "fd",
		Installed:     true,
		InstalledWith: "brew",
		Tracked:       true,
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	if err := a.RefreshProviderInstalled(context.Background(), "system"); err != nil {
		t.Fatalf("RefreshProviderInstalled: %v", err)
	}
	if system.calls != 1 {
		t.Fatalf("system bulk calls = %d, want 1", system.calls)
	}
	if brew.calls != 0 {
		t.Fatalf("brew owner bulk calls = %d, want 0 because system scan already resolved to brew", brew.calls)
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

	// Provider unavailable — tool should not be in the DB (never upserted).
	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("got %d tools in DB, want 0 (provider unavailable)", len(tools))
	}
}

// TestRefreshProviderInstalled_MultiManagerPath verifies the
// MultiManagerBulkChecker path of RefreshProviderInstalled.
func TestRefreshProviderInstalled_MultiManagerPath(t *testing.T) {
	prov := &multiManagerStub{
		stubProvider: stubProvider{name: "python", available: true},
		entries: map[string]provider.InstalledEntry{
			"black": {Version: "24.3.0", ConcreteManager: "uv"},
		},
	}
	a, cfgPath := newImportApp(t, prov)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("black", "python")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("black"),
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.RefreshProviderInstalled(context.Background(), "python"); err != nil {
		t.Fatalf("RefreshProviderInstalled multi-manager: %v", err)
	}

	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(tools))
	}
	if !tools[0].Installed {
		t.Errorf("black.Installed = false, want true")
	}
	if tools[0].InstalledWith != "uv" {
		t.Errorf("black.InstalledWith = %q, want uv", tools[0].InstalledWith)
	}
}

func TestRefreshProviderInstalled_MultiManagerPath_UsesFullSlashPackage(t *testing.T) {
	prov := &multiManagerStub{
		stubProvider: stubProvider{name: "node", available: true},
		entries: map[string]provider.InstalledEntry{
			"@playwright/test": {Version: "1.52.0", ConcreteManager: "npm"},
			"test":             {Version: "0.0.1", ConcreteManager: "pnpm"},
		},
	}
	a, cfgPath := newImportApp(t, prov)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalToolPackage("playwright-test", "node", "@playwright/test"),
		),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("playwright-test"),
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.RefreshProviderInstalled(context.Background(), "node"); err != nil {
		t.Fatalf("RefreshProviderInstalled multi-manager: %v", err)
	}

	got, err := a.DB().Get(context.Background(), "playwright-test", "node", "@playwright/test")
	if err != nil {
		t.Fatalf("Get playwright-test: %v", err)
	}
	if !got.Installed || got.InstalledWith != "npm" || got.Version.String != "1.52.0" {
		t.Fatalf("cache = installed:%v owner:%q version:%q, want true/npm/1.52.0", got.Installed, got.InstalledWith, got.Version.String)
	}
}

func TestRefreshProviderInstalled_PreservesCachedConcreteOwner(t *testing.T) {
	brew := &bulkCheckingStub{
		stubProvider: stubProvider{name: "brew", available: true},
		bulk:         map[string]string{"ripgrep": "14.1.0"},
	}
	system := &bulkConcreteStub{
		bulkCheckingStub: bulkCheckingStub{
			stubProvider: stubProvider{name: "system", available: true},
			bulk:         map[string]string{},
		},
		concreteName: "apt",
	}
	a, cfgPath := newImportApp(t, brew, system)
	ctx := context.Background()
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "system")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
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

	if err := a.RefreshProviderInstalled(ctx, "system"); err != nil {
		t.Fatalf("RefreshProviderInstalled: %v", err)
	}

	got, err := a.DB().Get(ctx, "ripgrep", "system", "ripgrep")
	if err != nil {
		t.Fatalf("cache get: %v", err)
	}
	if !got.Installed || got.InstalledWith != "brew" || got.Version.String != "14.1.0" {
		t.Fatalf("cache installed/with/version = %v/%q/%q, want true/brew/14.1.0", got.Installed, got.InstalledWith, got.Version.String)
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

// TestRefreshDiscovered_PopulatesDB verifies that tools reported by
// ListInstalled are stored as discovered (tracked=false) entries.
func TestRefreshDiscovered_PopulatesDB(t *testing.T) {
	prov := &listInstalledStub{
		stubProvider: stubProvider{name: "brew", available: true},
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
	if len(discovered) != 1 {
		t.Fatalf("got %d discovered tools, want 1", len(discovered))
	}
	if discovered[0].Name != "ripgrep" {
		t.Errorf("discovered name = %q, want ripgrep", discovered[0].Name)
	}
}

func TestRefreshDiscovered_MetaMappedToolKeepsConcreteInstalledWith(t *testing.T) {
	brew := &listInstalledStub{
		stubProvider: stubProvider{name: "brew", available: true},
		installed: []provider.InstalledTool{
			{Tool: provider.Tool{Name: "ripgrep", Provider: "brew"}, Version: "14.1.0"},
		},
	}
	system := &bulkConcreteStub{
		bulkCheckingStub: bulkCheckingStub{
			stubProvider: stubProvider{name: "system", available: true},
		},
		concreteName: "brew",
	}
	a, _ := newImportApp(t, brew, system)

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
	if discovered[0].Provider != "system" {
		t.Fatalf("Provider = %q, want system", discovered[0].Provider)
	}
	if discovered[0].InstalledWith != "brew" {
		t.Fatalf("InstalledWith = %q, want brew", discovered[0].InstalledWith)
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

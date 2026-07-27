package app_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

type describingProvider struct {
	stubProvider
	calls atomic.Int32
}

func (d *describingProvider) Describe(_ context.Context, t provider.Tool) (string, error) {
	d.calls.Add(1)
	return "description of " + t.Name, nil
}

func TestRefreshDescriptions_FetchesMissing(t *testing.T) {
	t.Parallel()
	prov := &describingProvider{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, prov)

	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("jq", "brew"),
		),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep", "jq"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if err := a.RefreshDescriptions(context.Background(), 0); err != nil {
		t.Fatalf("RefreshDescriptions: %v", err)
	}

	if got := prov.calls.Load(); got != 2 {
		t.Errorf("Describe called %d times, want 2", got)
	}
}

func TestRefreshDescriptions_SkipsCached(t *testing.T) {
	t.Parallel()
	prov := &describingProvider{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, prov)

	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.RefreshDescriptions(context.Background(), 0); err != nil {
		t.Fatalf("first refresh: %v", err)
	}

	prov.calls.Store(0)
	if err := a.RefreshDescriptions(context.Background(), 0); err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if got := prov.calls.Load(); got != 0 {
		t.Errorf("Describe called %d times on second run, want 0 (already cached)", got)
	}
}

func TestRefreshDescriptions_RespectsContextCancel(t *testing.T) {
	t.Parallel()
	prov := &describingProvider{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, prov)

	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("jq", "brew"),
			logicalTool("fd", "brew"),
		),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep", "jq", "fd"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := a.RefreshDescriptions(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("RefreshDescriptions error = %v, want context.Canceled", err)
	}
	if got := prov.calls.Load(); got != 0 {
		t.Errorf("Describe called %d times after cancel, want 0", got)
	}
}

type bulkDescribingProvider struct {
	stubProvider
	calls atomic.Int32
}

func (b *bulkDescribingProvider) BulkDescribe(_ context.Context, tools []provider.Tool) (map[string]string, error) {
	b.calls.Add(1)
	m := make(map[string]string, len(tools))
	for _, t := range tools {
		m[t.EffectivePackage()] = "bulk description of " + t.EffectivePackage()
	}
	return m, nil
}

func TestRefreshDescriptions_UsesBulkDescriber(t *testing.T) {
	t.Parallel()
	prov := &bulkDescribingProvider{stubProvider: stubProvider{name: "apt", available: true}}
	a, cfgPath := newImportApp(t, prov)

	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "apt"),
			logicalTool("curl", "apt"),
		),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep", "curl"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if err := a.RefreshDescriptions(context.Background(), 0); err != nil {
		t.Fatalf("RefreshDescriptions: %v", err)
	}

	if got := prov.calls.Load(); got != 1 {
		t.Errorf("BulkDescribe called %d times, want 1", got)
	}
}

func TestRefreshDescriptions_BulkDescriberMatchesPackageAlias(t *testing.T) {
	t.Parallel()
	prov := &bulkDescribingProvider{stubProvider: stubProvider{name: "apt", available: true}}
	a, cfgPath := newImportApp(t, prov)

	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalToolPackage("editor", "apt", "neovim")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("editor"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if err := a.RefreshDescriptions(context.Background(), 0); err != nil {
		t.Fatalf("RefreshDescriptions: %v", err)
	}

	got, err := a.DB().GetMetadata(context.Background(), "editor", "apt", "neovim")
	if err != nil {
		t.Fatalf("metadata get: %v", err)
	}
	if !got.Description.Valid || got.Description.String != "bulk description of neovim" {
		t.Fatalf("description = %#v, want package alias description", got.Description)
	}
}

type basenameBulkDescribingProvider struct {
	stubProvider
	calls atomic.Int32
}

func (b *basenameBulkDescribingProvider) BulkDescribe(_ context.Context, _ []provider.Tool) (map[string]string, error) {
	b.calls.Add(1)
	return map[string]string{
		"cloudflared":      "Cloudflare tunnel daemon",
		"@playwright/test": "Playwright test framework",
		"test":             "wrong basename description",
	}, nil
}

func TestRefreshDescriptions_BulkDescriberMatchesPackageBasename(t *testing.T) {
	t.Parallel()
	prov := &basenameBulkDescribingProvider{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, prov)

	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalToolPackage("cloudflare-ddns", "brew", "cloudflare/cloudflare/cloudflared")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("cloudflare-ddns"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if err := a.RefreshDescriptions(context.Background(), 0); err != nil {
		t.Fatalf("RefreshDescriptions: %v", err)
	}

	got, err := a.DB().GetMetadata(context.Background(), "cloudflare-ddns", "brew", "cloudflare/cloudflare/cloudflared")
	if err != nil {
		t.Fatalf("metadata get: %v", err)
	}
	if !got.Description.Valid || got.Description.String != "Cloudflare tunnel daemon" {
		t.Fatalf("description = %#v, want basename package description", got.Description)
	}
}

func TestRefreshDescriptions_BulkDescriberPrefersFullScopedPackage(t *testing.T) {
	t.Parallel()
	prov := &basenameBulkDescribingProvider{stubProvider: stubProvider{name: "npm", available: true}}
	a, cfgPath := newImportApp(t, prov)

	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalToolPackage("playwright", "npm", "@playwright/test")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("playwright"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if err := a.RefreshDescriptions(context.Background(), 0); err != nil {
		t.Fatalf("RefreshDescriptions: %v", err)
	}

	got, err := a.DB().GetMetadata(context.Background(), "playwright", "npm", "@playwright/test")
	if err != nil {
		t.Fatalf("metadata get: %v", err)
	}
	if !got.Description.Valid || got.Description.String != "Playwright test framework" {
		t.Fatalf("description = %#v, want full scoped package description", got.Description)
	}
}

type partialBulkDescribingProvider struct {
	describingProvider
	bulkCalls atomic.Int32
}

func (b *partialBulkDescribingProvider) BulkDescribe(_ context.Context, tools []provider.Tool) (map[string]string, error) {
	b.bulkCalls.Add(1)
	if len(tools) == 0 {
		return nil, nil
	}
	return map[string]string{tools[0].EffectivePackage(): "bulk description of " + tools[0].EffectivePackage()}, nil
}

func TestRefreshDescriptions_FallsBackForBulkMisses(t *testing.T) {
	t.Parallel()
	prov := &partialBulkDescribingProvider{
		describingProvider: describingProvider{stubProvider: stubProvider{name: "brew", available: true}},
	}
	a, cfgPath := newImportApp(t, prov)

	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("fd", "brew"),
		),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep", "fd"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if err := a.RefreshDescriptions(context.Background(), 0); err != nil {
		t.Fatalf("RefreshDescriptions: %v", err)
	}
	if got := prov.bulkCalls.Load(); got != 1 {
		t.Fatalf("BulkDescribe called %d times, want 1", got)
	}
	if got := prov.calls.Load(); got != 1 {
		t.Fatalf("Describe fallback called %d times, want 1 missing tool", got)
	}
	rg, err := a.DB().GetMetadata(context.Background(), "ripgrep", "brew", "ripgrep")
	if err != nil {
		t.Fatalf("metadata get ripgrep: %v", err)
	}
	fd, err := a.DB().GetMetadata(context.Background(), "fd", "brew", "fd")
	if err != nil {
		t.Fatalf("metadata get fd: %v", err)
	}
	if !rg.Description.Valid || rg.Description.String != "bulk description of ripgrep" {
		t.Fatalf("ripgrep description = %#v, want bulk description", rg.Description)
	}
	if !fd.Description.Valid || fd.Description.String != "description of fd" {
		t.Fatalf("fd description = %#v, want fallback description", fd.Description)
	}
}

func TestRefreshDescriptions_FetchesDiscoveredTools(t *testing.T) {
	t.Parallel()
	prov := &describingProvider{stubProvider: stubProvider{name: "npm", available: true}}
	a, _ := newImportApp(t, prov)
	ctx := context.Background()

	if err := a.DB().UpsertDiscovered(ctx, "playwright", "npm", "pnpm", "1.52.0"); err != nil {
		t.Fatalf("seed discovered tool: %v", err)
	}

	if err := a.RefreshDescriptions(ctx, 0); err != nil {
		t.Fatalf("RefreshDescriptions: %v", err)
	}

	got, err := a.DB().Get(ctx, "playwright", "npm", "playwright")
	if err != nil {
		t.Fatalf("db get: %v", err)
	}
	if !got.Description.Valid || got.Description.String != "description of playwright" {
		t.Fatalf("description = %#v, want discovered tool description", got.Description)
	}
	if got.Tracked {
		t.Fatal("description refresh must not mark discovered tool as tracked")
	}
}

func TestRefreshDescriptions_FetchesConfiguredNodeRowsInstalledWithManager(t *testing.T) {
	t.Parallel()
	prov := &describingProvider{stubProvider: stubProvider{name: "npm", available: true}}
	a, cfgPath := newImportApp(t, prov)
	ctx := context.Background()

	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("pm2", "npm")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("pm2"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "pm2",
		Provider:      "npm",
		Package:       "pm2",
		Installed:     true,
		InstalledWith: "bun",
	}); err != nil {
		t.Fatalf("seed node cache row: %v", err)
	}

	if err := a.RefreshDescriptions(ctx, 0); err != nil {
		t.Fatalf("RefreshDescriptions: %v", err)
	}

	got, err := a.DB().Get(ctx, "pm2", "npm", "pm2")
	if err != nil {
		t.Fatalf("db get: %v", err)
	}
	if !got.Description.Valid || got.Description.String != "description of pm2" {
		t.Fatalf("description = %#v, want node registry description", got.Description)
	}
	if got.InstalledWith != "bun" {
		t.Fatalf("InstalledWith = %q, want bun", got.InstalledWith)
	}
}

func TestRefreshDescriptions_UsesInstalledWithEcosystemWhenCacheProviderMissing(t *testing.T) {
	t.Parallel()
	prov := &describingProvider{stubProvider: stubProvider{name: "node", available: true}}
	a, _ := newImportApp(t, prov)
	ctx := context.Background()

	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "pm2",
		Provider:      "npm",
		Package:       "pm2",
		Installed:     true,
		InstalledWith: "bun",
	}); err != nil {
		t.Fatalf("seed node cache row: %v", err)
	}

	if err := a.RefreshDescriptions(ctx, 0); err != nil {
		t.Fatalf("RefreshDescriptions: %v", err)
	}

	got, err := a.DB().Get(ctx, "pm2", "npm", "pm2")
	if err != nil {
		t.Fatalf("db get: %v", err)
	}
	if !got.Description.Valid || got.Description.String != "description of pm2" {
		t.Fatalf("description = %#v, want node ecosystem description", got.Description)
	}
	if got.Provider != "npm" || got.InstalledWith != "bun" {
		t.Fatalf("cache identity = provider %q installed_with %q, want npm/bun", got.Provider, got.InstalledWith)
	}
}

func TestRefreshDescriptions_FetchesConcreteManagerCacheRows(t *testing.T) {
	t.Parallel()
	prov := &describingProvider{stubProvider: stubProvider{name: "npm", available: true}}
	a, _ := newImportApp(t, prov)
	ctx := context.Background()

	if err := a.DB().UpsertDiscovered(ctx, "playwright", "npm", "npm", "1.59.1"); err != nil {
		t.Fatalf("seed concrete manager row: %v", err)
	}

	if err := a.RefreshDescriptions(ctx, 0); err != nil {
		t.Fatalf("RefreshDescriptions: %v", err)
	}

	got, err := a.DB().Get(ctx, "playwright", "npm", "playwright")
	if err != nil {
		t.Fatalf("db get: %v", err)
	}
	if !got.Description.Valid || got.Description.String != "description of playwright" {
		t.Fatalf("description = %#v, want node registry description for concrete bun row", got.Description)
	}
	if got.Provider != "npm" || got.InstalledWith != "npm" {
		t.Fatalf("cache identity = provider %q installed_with %q, want npm/npm", got.Provider, got.InstalledWith)
	}
}

type concurrentDescribingProvider struct {
	stubProvider
	calls   atomic.Int32
	current atomic.Int32
	maxSeen atomic.Int32
}

func (d *concurrentDescribingProvider) Describe(_ context.Context, t provider.Tool) (string, error) {
	d.calls.Add(1)
	cur := d.current.Add(1)
	for {
		maxSeen := d.maxSeen.Load()
		if cur <= maxSeen || d.maxSeen.CompareAndSwap(maxSeen, cur) {
			break
		}
	}
	time.Sleep(20 * time.Millisecond)
	d.current.Add(-1)
	return "description of " + t.Name, nil
}

func TestRefreshDescriptions_IndividualFallbackIsBoundedConcurrent(t *testing.T) {
	t.Parallel()
	prov := &concurrentDescribingProvider{stubProvider: stubProvider{name: "npm", available: true}}
	a, cfgPath := newImportApp(t, prov)

	var fixtureTools []logicalFixtureTool
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		fixtureTools = append(fixtureTools, logicalTool(name, "npm"))
	}
	cfg := &config.RootConfig{
		Tools:  logicalToolSpecs(fixtureTools...),
		Groups: []*config.GroupConfig{testHostToolGroup("a", "b", "c", "d", "e", "f", "g", "h")},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if err := a.RefreshDescriptions(context.Background(), 0); err != nil {
		t.Fatalf("RefreshDescriptions: %v", err)
	}
	if got := prov.calls.Load(); got != int32(len(fixtureTools)) {
		t.Fatalf("Describe calls = %d, want %d", got, len(fixtureTools))
	}
	if got := prov.maxSeen.Load(); got <= 1 || got > 4 {
		t.Fatalf("max concurrent Describe calls = %d, want between 2 and 4", got)
	}
}

type failingBulkDescribingProvider struct {
	describingProvider
	bulkCalls atomic.Int32
}

func (b *failingBulkDescribingProvider) BulkDescribe(_ context.Context, _ []provider.Tool) (map[string]string, error) {
	b.bulkCalls.Add(1)
	return nil, errors.New("bulk failed")
}

func TestRefreshDescriptions_FallsBackWhenBulkDescribeFails(t *testing.T) {
	t.Parallel()
	prov := &failingBulkDescribingProvider{
		describingProvider: describingProvider{stubProvider: stubProvider{name: "apt", available: true}},
	}
	a, cfgPath := newImportApp(t, prov)

	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "apt")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if err := a.RefreshDescriptions(context.Background(), 0); err != nil {
		t.Fatalf("RefreshDescriptions: %v", err)
	}
	if got := prov.bulkCalls.Load(); got != 1 {
		t.Fatalf("BulkDescribe called %d times, want 1", got)
	}
	if got := prov.calls.Load(); got != 1 {
		t.Fatalf("Describe fallback called %d times, want 1", got)
	}
}

func TestRefreshDescriptions_EmptyConfig(t *testing.T) {
	t.Parallel()
	prov := &describingProvider{stubProvider: stubProvider{name: "brew", available: true}}
	a, _ := newImportApp(t, prov)
	if err := a.RefreshDescriptions(context.Background(), 0); err != nil {
		t.Fatalf("RefreshDescriptions on missing config: %v", err)
	}
	if got := prov.calls.Load(); got != 0 {
		t.Errorf("Describe called %d times on missing config, want 0", got)
	}
}

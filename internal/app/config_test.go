package app_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
)

// ─── HasConfig / CreateEmptyConfig ───────────────────────────────────────────

func TestHasConfig_False(t *testing.T) {
	a, _ := newImportApp(t)
	// No settings.json written yet.
	if a.HasConfig() {
		t.Error("HasConfig should be false before any config is created")
	}
}

func TestHasConfig_True(t *testing.T) {
	a, cfgPath := newImportApp(t)
	if err := os.WriteFile(cfgPath, []byte(""), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if !a.HasConfig() {
		t.Error("HasConfig should be true when settings.json exists")
	}
}

func TestCreateEmptyConfig_CreatesFile(t *testing.T) {
	a, cfgPath := newImportApp(t)
	if err := a.CreateEmptyConfig(); err != nil {
		t.Fatalf("CreateEmptyConfig: %v", err)
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Errorf("settings.json not created: %v", err)
	}
	if err := a.SaveSettings(context.Background(), config.Settings{}); err != nil {
		t.Fatalf("SaveSettings after CreateEmptyConfig: %v", err)
	}
}

func TestCreateEmptyConfig_Noop(t *testing.T) {
	a, cfgPath := newImportApp(t)
	// Write an existing non-empty config.
	existing := &config.RootConfig{
		Groups: []*config.GroupConfig{{Name: "base"}},
	}
	if err := saveAppConfig(t, cfgPath, existing); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	// Second call should not overwrite the file.
	if err := a.CreateEmptyConfig(); err != nil {
		t.Fatalf("CreateEmptyConfig (noop): %v", err)
	}
	data, _ := os.ReadFile(cfgPath)
	if string(data) == "" {
		t.Error("CreateEmptyConfig overwrote existing file")
	}
}

// ─── LoadTaps ─────────────────────────────────────────────────────────────────

func TestLoadTaps_Empty(t *testing.T) {
	a, _ := newImportApp(t)
	taps, err := a.LoadTaps()
	if err != nil {
		t.Fatalf("LoadTaps: %v", err)
	}
	if len(taps) != 0 {
		t.Errorf("expected 0 taps, got %d", len(taps))
	}
}

func TestLoadTaps_ReturnsUnion(t *testing.T) {
	a, cfgPath := newImportApp(t)

	rootCfg := &config.RootConfig{
		Groups: []*config.GroupConfig{
			{Name: testShortHostname(), Special: "host", Taps: []string{"hashicorp/tap", "homebrew/cask"}},
			{Name: "work", Taps: []string{"hashicorp/tap", "owner/repo"}},
		},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	taps, err := a.LoadTaps()
	if err != nil {
		t.Fatalf("LoadTaps: %v", err)
	}
	// Union across 2 groups; hashicorp/tap is deduplicated.
	if len(taps) != 3 {
		t.Errorf("LoadTaps = %v (len %d), want 3 unique taps", taps, len(taps))
	}
}

// ─── ActiveHostInfo ───────────────────────────────────────────────────────────

func TestActiveHostInfo_NoConfig(t *testing.T) {
	a, _ := newImportApp(t)
	name, groups, ok := a.ActiveHostInfo()
	if ok {
		t.Errorf("ActiveHostInfo should return ok=false, got name=%q groups=%v", name, groups)
	}
}

func TestActiveHostInfo_MatchesHostname(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "workstation.example")
	a, _ := newImportApp(t)

	if err := a.CreateGroup("work"); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if err := a.EnsureHost("workstation"); err != nil {
		t.Fatalf("EnsureHost: %v", err)
	}
	if err := a.AddGroupToHost("workstation", "work"); err != nil {
		t.Fatalf("AddGroupToHost: %v", err)
	}

	name, groups, ok := a.ActiveHostInfo()
	if !ok {
		t.Fatal("ActiveHostInfo should return ok=true")
	}
	if name != "workstation" {
		t.Errorf("name = %q, want workstation", name)
	}
	if strings.Join(groups, ",") != "workstation,work" {
		t.Errorf("groups = %v, want [workstation work]", groups)
	}
}

func TestToolMembershipMap_ReturnsSingleOwnerMembership(t *testing.T) {
	a, cfgPath := newImportApp(t)
	rootCfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{
			{Name: "work", Tools: groupTools("ripgrep")},
		},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	got, err := a.ToolMembershipMap(context.Background())
	if err != nil {
		t.Fatalf("ToolMembershipMap: %v", err)
	}
	memberships := got["ripgrep\x00system"]
	want := "work"
	if strings.Join(memberships, ",") != want {
		t.Fatalf("memberships = %v, want [%s]", memberships, want)
	}
}

func TestSetToolAndGroupIgnoreScopes(t *testing.T) {
	a, cfgPath := newImportApp(t)
	rootCfg := &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{Name: "work", Tools: groupTools("ripgrep")}},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.SetToolIgnore("ripgrep", true); err != nil {
		t.Fatalf("SetToolIgnore: %v", err)
	}
	if err := a.SetGroupIgnore("work", "ripgrep", true); err != nil {
		t.Fatalf("SetGroupIgnore: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !cfg.Tools["ripgrep"].Ignore {
		t.Fatal("tool-level ignore was not persisted")
	}
	if len(cfg.Ignore.Tools) != 1 || cfg.Ignore.Tools[0] != "ripgrep" {
		t.Fatalf("global ignore = %v, want [ripgrep]", cfg.Ignore.Tools)
	}
}

func TestSetToolHostInstallSpec_WritesHostOverride(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "linuxbox.example.com")
	a, cfgPath := newImportApp(t)
	rootCfg := &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("typescript", "node")),
		Groups: []*config.GroupConfig{testHostToolGroup("typescript")},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.SetToolHostInstallSpec("typescript", "node", "typescript", "pnpm"); err != nil {
		t.Fatalf("SetToolHostInstallSpec: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	override, ok := cfg.Tools["typescript"].Hosts["linuxbox"]
	if !ok {
		t.Fatalf("expected linuxbox host override, got %#v", cfg.Tools["typescript"].Hosts)
	}
	if override.Provider != "node" || override.InstallWith != "pnpm" {
		t.Fatalf("override = %+v, want node via pnpm", override)
	}
}

func TestClearToolInstallOverride_RemovesEffectiveHostOverride(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "linuxbox.example.com")
	a, cfgPath := newImportApp(t)
	rootCfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"typescript": {
				Provider:    "node",
				InstallWith: "npm",
				Hosts: map[string]config.ToolInstallSpec{
					"linuxbox": {Provider: "node", InstallWith: "pnpm"},
				},
			},
		},
		Groups: []*config.GroupConfig{testHostToolGroup("typescript")},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	cleared, err := a.ClearToolInstallOverride(context.Background(), "typescript", "node")
	if err != nil {
		t.Fatalf("ClearToolInstallOverride: %v", err)
	}
	if cleared.Scope != "host" || cleared.InstallWith != "pnpm" {
		t.Fatalf("cleared = %+v, want host/pnpm", cleared)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, ok := cfg.Tools["typescript"].Hosts["linuxbox"]; ok {
		t.Fatalf("host override still present: %+v", cfg.Tools["typescript"].Hosts)
	}
	if cfg.Tools["typescript"].InstallWith != "" {
		t.Fatalf("default install_with = %q, want cleared", cfg.Tools["typescript"].InstallWith)
	}
}

func TestClearToolInstallOverride_RemovesDefaultOverride(t *testing.T) {
	a, cfgPath := newImportApp(t)
	rootCfg := &config.RootConfig{
		Tools:  logicalToolSpecs(logicalFixtureTool{Name: "black", Provider: "python", InstallWith: "pip3"}),
		Groups: []*config.GroupConfig{testHostToolGroup("black")},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	cleared, err := a.ClearToolInstallOverride(context.Background(), "black", "python")
	if err != nil {
		t.Fatalf("ClearToolInstallOverride: %v", err)
	}
	if cleared.Scope != "tool" || cleared.InstallWith != "pip3" {
		t.Fatalf("cleared = %+v, want tool/pip3", cleared)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.Tools["black"].InstallWith != "" {
		t.Fatalf("install_with = %q, want cleared", cfg.Tools["black"].InstallWith)
	}
}

func TestNormalizeDefaultInstallOverrides_DryRunAndApply(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "linuxbox.example.com")
	brew := &lifecycleProvider{stubProvider: stubProvider{name: "brew", available: true}}
	system := &lifecycleProvider{
		stubProvider: stubProvider{name: "system", available: true},
		resolvedName: "brew",
	}
	a, cfgPath := newImportApp(t, brew, system)
	rootCfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {Provider: "system", InstallWith: "brew"},
			"jq":      {Provider: "system", InstallWith: "apt"},
			"bat": {
				Provider: "system",
				Hosts: map[string]config.ToolInstallSpec{
					"linuxbox": {Provider: "system", InstallWith: "brew"},
				},
			},
			"fd": {
				Provider: "system",
				Hosts: map[string]config.ToolInstallSpec{
					"other": {Provider: "system", InstallWith: "brew"},
				},
			},
		},
		Groups: []*config.GroupConfig{testHostToolGroup("ripgrep", "jq", "bat", "fd")},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	dryRun, err := a.NormalizeDefaultInstallOverrides(context.Background(), app.NormalizeInstallOverridesOptions{
		IncludeDefaults:    true,
		IncludeCurrentHost: true,
		DryRun:             true,
	})
	if err != nil {
		t.Fatalf("NormalizeDefaultInstallOverrides dry-run: %v", err)
	}
	if len(dryRun) != 2 {
		t.Fatalf("dry-run normalized = %+v, want 2 entries", dryRun)
	}
	unchanged, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if unchanged.Tools["ripgrep"].InstallWith != "brew" {
		t.Fatalf("dry-run changed ripgrep install_with to %q", unchanged.Tools["ripgrep"].InstallWith)
	}
	if unchanged.Tools["bat"].Hosts["linuxbox"].InstallWith != "brew" {
		t.Fatalf("dry-run changed bat host override to %+v", unchanged.Tools["bat"].Hosts["linuxbox"])
	}

	applied, err := a.NormalizeDefaultInstallOverrides(context.Background(), app.NormalizeInstallOverridesOptions{
		IncludeDefaults:    true,
		IncludeCurrentHost: true,
	})
	if err != nil {
		t.Fatalf("NormalizeDefaultInstallOverrides apply: %v", err)
	}
	if len(applied) != 2 {
		t.Fatalf("applied normalized = %+v, want 2 entries", applied)
	}
	if applied[0].Name != "bat" || applied[0].Scope != "host" || applied[0].Host != "linuxbox" {
		t.Fatalf("first normalized entry = %+v, want bat host/linuxbox", applied[0])
	}
	if applied[1].Name != "ripgrep" || applied[1].Scope != "tool" {
		t.Fatalf("second normalized entry = %+v, want ripgrep tool", applied[1])
	}
	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if updated.Tools["ripgrep"].InstallWith != "" {
		t.Fatalf("ripgrep install_with = %q, want cleared", updated.Tools["ripgrep"].InstallWith)
	}
	if updated.Tools["jq"].InstallWith != "apt" {
		t.Fatalf("jq install_with = %q, want preserved apt pin", updated.Tools["jq"].InstallWith)
	}
	if _, ok := updated.Tools["bat"].Hosts["linuxbox"]; ok {
		t.Fatalf("bat host override still present: %+v", updated.Tools["bat"].Hosts)
	}
	if updated.Tools["fd"].Hosts["other"].InstallWith != "brew" {
		t.Fatalf("fd other-host override = %+v, want preserved", updated.Tools["fd"].Hosts["other"])
	}
}

// ─── RefreshOutdated ──────────────────────────────────────────────────────────

// outdatedStub is a provider that also implements OutdatedChecker.
type outdatedStub struct {
	stubProvider
	outdated map[string]string // lowercase name → latest version
}

func (o *outdatedStub) OutdatedMap(_ context.Context) (map[string]string, error) {
	return o.outdated, nil
}

func TestRefreshOutdated_SetsOutdatedFlag(t *testing.T) {
	stub := &outdatedStub{
		stubProvider: stubProvider{name: "brew", available: true},
		outdated:     map[string]string{"ripgrep": "15.0.0"},
	}
	a, _ := newImportApp(t, stub)
	ctx := context.Background()

	if err := a.Install(ctx, "ripgrep", "brew"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	var progress []string
	if err := a.RefreshOutdated(ctx, func(s string) { progress = append(progress, s) }); err != nil {
		t.Fatalf("RefreshOutdated: %v", err)
	}

	tools, err := a.ListTools(ctx, "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("no tools in DB")
	}
	if !tools[0].Outdated {
		t.Error("expected ripgrep to be marked outdated")
	}
	if tools[0].LatestVersion.String != "15.0.0" {
		t.Errorf("LatestVersion = %q, want 15.0.0", tools[0].LatestVersion.String)
	}
	if len(progress) == 0 {
		t.Error("expected progress callback to be called")
	}
}

func TestRefreshOutdated_NoOutdatedChecker(t *testing.T) {
	// Provider without OutdatedChecker should be silently skipped.
	stub := &stubProvider{name: "brew", available: true}
	a, _ := newImportApp(t, stub)
	ctx := context.Background()

	if err := a.Install(ctx, "git", "brew"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := a.RefreshOutdated(ctx, nil); err != nil {
		t.Fatalf("RefreshOutdated (no checker): %v", err)
	}
	// Tool should remain not-outdated.
	tools, _ := a.ListTools(ctx, "")
	if len(tools) > 0 && tools[0].Outdated {
		t.Error("tool should not be outdated when provider has no OutdatedChecker")
	}
}

// ─── Registry ─────────────────────────────────────────────────────────────────

func TestRegistry_ReturnsNonNil(t *testing.T) {
	stub := &stubProvider{name: "brew", available: true}
	a, _ := newImportApp(t, stub)
	if r := a.Registry(); r == nil {
		t.Error("Registry() should return non-nil")
	}
}

func TestRegistry_ContainsRegisteredProviders(t *testing.T) {
	brew := &stubProvider{name: "brew", available: true}
	npm := &stubProvider{name: "npm", available: true}
	a, _ := newImportApp(t, brew, npm)

	reg := a.Registry()
	if _, ok := reg.Get("brew"); !ok {
		t.Error("brew not found in registry")
	}
	if _, ok := reg.Get("npm"); !ok {
		t.Error("npm not found in registry")
	}
}

// ─── Init ─────────────────────────────────────────────────────────────────────

func TestInit_CreatesCacheDir(t *testing.T) {
	configDir := t.TempDir()
	cfgPath := filepath.Join(configDir, "settings.json")
	cacheDir := t.TempDir()
	// Set env var so Init uses our temp dir for the cache.
	t.Setenv("OMNI_CACHE_DIR", cacheDir)

	a := app.New(cfgPath)
	ctx := context.Background()
	if err := a.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer a.Close()

	if !strings.HasPrefix(a.DBPath, cacheDir) {
		t.Errorf("DBPath = %q, want prefix in %q", a.DBPath, cacheDir)
	}
	if _, err := os.Stat(a.DBPath); err != nil {
		t.Errorf("DB file not created: %v", err)
	}
}

func TestInit_CacheDirOverride(t *testing.T) {
	configDir := t.TempDir()
	cfgPath := filepath.Join(configDir, "settings.json")
	cacheDir := t.TempDir()

	a := app.New(cfgPath)
	a.CacheDir = cacheDir // explicit override — env var not needed
	ctx := context.Background()
	if err := a.Init(ctx); err != nil {
		t.Fatalf("Init with CacheDir set: %v", err)
	}
	defer a.Close()

	if !strings.HasPrefix(a.DBPath, cacheDir) {
		t.Errorf("DBPath = %q, want prefix in %q", a.DBPath, cacheDir)
	}
}

// Compile-time check that outdatedStub satisfies both interfaces.
var _ provider.Provider = (*outdatedStub)(nil)
var _ provider.OutdatedChecker = (*outdatedStub)(nil)

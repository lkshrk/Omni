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

func TestImportConfigFile_CopiesExistingSettings(t *testing.T) {
	a, cfgPath := newImportApp(t)
	sourcePath := filepath.Join(t.TempDir(), "settings.json")
	source := &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "system")),
		Groups: []*config.GroupConfig{
			{Name: "work", Tools: groupTools("ripgrep")},
		},
		Hosts: map[string][]string{"laptop": {"work"}},
	}
	source.Settings.SetEcosystemManager("node", "pnpm")
	if err := config.Save(sourcePath, source); err != nil {
		t.Fatalf("save source config: %v", err)
	}

	if err := a.ImportConfigFile(sourcePath); err != nil {
		t.Fatalf("ImportConfigFile: %v", err)
	}

	got, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, ok := got.Tools["ripgrep"]; !ok {
		t.Fatal("imported config missing ripgrep tool")
	}
	if group := findTestGroup(got, "work"); group == nil || !testGroupHasTool(group, "ripgrep") {
		t.Fatalf("imported work group = %#v, want ripgrep membership", group)
	}
	if got.Settings.EcosystemManager("node") != "pnpm" {
		t.Fatalf("node manager = %q, want pnpm", got.Settings.EcosystemManager("node"))
	}
}

func TestImportConfigFile_RejectsMissingSource(t *testing.T) {
	a, _ := newImportApp(t)
	err := a.ImportConfigFile(filepath.Join(t.TempDir(), "missing-settings.json"))
	if err == nil || !strings.Contains(err.Error(), "settings import") {
		t.Fatalf("ImportConfigFile err = %v, want missing source error", err)
	}
}

func TestImportConfigFile_RejectsDirectorySource(t *testing.T) {
	a, _ := newImportApp(t)
	err := a.ImportConfigFile(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("ImportConfigFile err = %v, want directory source error", err)
	}
}

func TestImportConfigFile_RejectsMalformedSource(t *testing.T) {
	a, _ := newImportApp(t)
	sourcePath := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(sourcePath, []byte(`{"version":`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err := a.ImportConfigFile(sourcePath)
	if err == nil || !strings.Contains(err.Error(), "load settings import") {
		t.Fatalf("ImportConfigFile err = %v, want malformed source error", err)
	}
}

func TestSaveToolFallback_PersistsRecipeWithoutInstalling(t *testing.T) {
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true})
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("rg", "system")),
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	err := a.SaveToolFallback(context.Background(), "rg", config.FallbackSpec{
		Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
		Status: config.FallbackStatusUnverified,
		Binary: "rg",
		Recipe: config.FallbackRecipe{Type: config.FallbackRecipeGitHubReleaseAsset, AssetPattern: "ripgrep-{version}-{os}-{arch}.tar.gz"},
		Commands: config.FallbackCommands{
			Install: "install rg",
			Check:   "command -v rg",
		},
	})
	if err != nil {
		t.Fatalf("SaveToolFallback: %v", err)
	}

	got, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	fallback := got.Tools["rg"].Fallback
	if fallback == nil {
		t.Fatal("fallback was not persisted")
	}
	if fallback.Source.Owner != "BurntSushi" || fallback.Source.Repo != "ripgrep" || fallback.Commands.Check != "command -v rg" {
		t.Fatalf("fallback = %+v, want ripgrep GitHub recipe with check", fallback)
	}
	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 0 {
		t.Fatalf("SaveToolFallback should not install or refresh DB rows, got %d tools", len(tools))
	}
}

func TestSaveToolFallbackFromGitHubSpec_NormalizesSourceAndPersistsRecipe(t *testing.T) {
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true})
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("rg", "system")),
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	err := a.SaveToolFallbackFromGitHubSpec(context.Background(), "rg", "git@github.com:BurntSushi/ripgrep.git", config.FallbackSpec{
		Status:         config.FallbackStatusUnverified,
		Binary:         "rg",
		BinDir:         "~/.local/share/omni/fallback/bin",
		ReleaseChannel: "stable",
		Recipe:         config.FallbackRecipe{Type: config.FallbackRecipeGitHubReleaseAsset, AssetPattern: "ripgrep-{version}-{os}-{arch}.tar.gz"},
		Commands: config.FallbackCommands{
			Install: "install rg",
			Check:   "command -v rg",
		},
	})
	if err != nil {
		t.Fatalf("SaveToolFallbackFromGitHubSpec: %v", err)
	}

	got, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	fallback := got.Tools["rg"].Fallback
	if fallback == nil {
		t.Fatal("fallback was not persisted")
	}
	if fallback.Source.Type != config.FallbackSourceGitHub ||
		fallback.Source.Owner != "BurntSushi" ||
		fallback.Source.Repo != "ripgrep" ||
		fallback.Source.URL != "https://github.com/BurntSushi/ripgrep" {
		t.Fatalf("source = %+v, want normalized GitHub source", fallback.Source)
	}
	if fallback.Binary != "rg" || fallback.Commands.Install != "install rg" || fallback.Commands.Check != "command -v rg" {
		t.Fatalf("fallback = %+v, want recipe preserved", fallback)
	}
}

func TestSaveToolFallback_RejectsMissingTool(t *testing.T) {
	a, _ := newImportApp(t, &stubProvider{name: "system", available: true})
	err := a.SaveToolFallback(context.Background(), "ghost", config.FallbackSpec{
		Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
		Status: config.FallbackStatusUnresolved,
	})
	if err == nil || !strings.Contains(err.Error(), `tool "ghost" not found`) {
		t.Fatalf("SaveToolFallback err = %v, want missing tool", err)
	}
}

func TestSaveToolFallback_PersistsForConcreteProviderTool(t *testing.T) {
	a, cfgPath := newImportApp(t, &stubProvider{name: "npm", available: true})
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("eslint", "npm")),
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	err := a.SaveToolFallback(context.Background(), "eslint", config.FallbackSpec{
		Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "eslint", Repo: "eslint"},
		Status: config.FallbackStatusUnresolved,
	})
	if err != nil {
		t.Fatalf("SaveToolFallback: %v", err)
	}
	got, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got.Tools["eslint"].Fallback == nil {
		t.Fatal("fallback was not persisted for concrete provider tool")
	}
}

func TestImportConfigFile_RejectsActiveConfigSource(t *testing.T) {
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	err := a.ImportConfigFile(cfgPath)
	if err == nil || !strings.Contains(err.Error(), "already the active config") {
		t.Fatalf("ImportConfigFile err = %v, want active config source error", err)
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
	memberships := got["ripgrep\x00brew"]
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

func TestSetToolQuarantine_PersistsToolOverride(t *testing.T) {
	a, cfgPath := newImportApp(t)
	rootCfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.SetToolQuarantine("ripgrep", "exempt"); err != nil {
		t.Fatalf("SetToolQuarantine: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got := cfg.Tools["ripgrep"].Quarantine; got != "exempt" {
		t.Fatalf("quarantine = %q, want exempt", got)
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
	if err := a.RefreshOutdated(ctx, false, func(s string) { progress = append(progress, s) }); err != nil {
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
	if err := a.RefreshOutdated(ctx, false, nil); err != nil {
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

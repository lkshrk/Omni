package app_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	brewprovider "github.com/lkshrk/omni/internal/provider/brew"
	scriptprovider "github.com/lkshrk/omni/internal/provider/script"
)

type lifecycleProvider struct {
	stubProvider
	uninstalled       []provider.Tool
	uninstallManagers []string
	upgraded          []provider.Tool
	installedChecks   []provider.Tool
	managerChecks     []string
	resolvedName      string
	installed         bool
	version           string
	outdated          map[string]string
}

type multiManagerLifecycleProvider struct {
	lifecycleProvider
	entries map[string]provider.InstalledEntry
}

func (p *multiManagerLifecycleProvider) InstalledByManager(_ context.Context) (map[string]provider.InstalledEntry, error) {
	return p.entries, nil
}

func (p *lifecycleProvider) Uninstall(_ context.Context, tool provider.Tool) error {
	p.uninstalled = append(p.uninstalled, tool)
	return nil
}

func (p *lifecycleProvider) UninstallFrom(_ context.Context, tool provider.Tool, manager string) error {
	p.uninstallManagers = append(p.uninstallManagers, manager)
	p.uninstalled = append(p.uninstalled, tool)
	return nil
}

func (p *lifecycleProvider) Upgrade(_ context.Context, tool provider.Tool) error {
	p.upgraded = append(p.upgraded, tool)
	return nil
}

func (p *lifecycleProvider) IsInstalled(_ context.Context, tool provider.Tool) (bool, string, error) {
	p.installedChecks = append(p.installedChecks, tool)
	return p.installed, p.version, nil
}

func (p *lifecycleProvider) IsInstalledWithManager(_ context.Context, tool provider.Tool, manager string) (bool, string, error) {
	p.managerChecks = append(p.managerChecks, manager)
	p.installedChecks = append(p.installedChecks, tool)
	return p.installed, p.version, nil
}

func (p *lifecycleProvider) OutdatedMap(_ context.Context) (map[string]string, error) {
	return p.outdated, nil
}

func (p *lifecycleProvider) ResolvedName(_ context.Context) (string, error) {
	return p.resolvedName, nil
}

func TestCompleteExternalToolAction_UpgradeVerifiesAndClearsOutdated(t *testing.T) {
	ctx := context.Background()
	brew := &lifecycleProvider{
		stubProvider: stubProvider{name: "brew", available: true},
		installed:    true,
		version:      "2.0.0",
	}
	a, _ := newImportApp(t, brew)
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "parsec",
		Provider:      "system",
		Package:       "parsec",
		Installed:     true,
		InstalledWith: "brew",
		Version:       sql.NullString{String: "1.0.0", Valid: true},
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	if err := a.DB().UpdateOutdated(ctx, "parsec", "system", "parsec", true, "2.0.0"); err != nil {
		t.Fatalf("seed outdated: %v", err)
	}

	if err := a.CompleteExternalToolAction(ctx, provider.PrivilegeActionUpgrade, "parsec", "system", "parsec", "brew"); err != nil {
		t.Fatalf("CompleteExternalToolAction: %v", err)
	}

	cached, err := a.DB().Get(ctx, "parsec", "system", "parsec")
	if err != nil {
		t.Fatalf("db.Get: %v", err)
	}
	if !cached.Installed || cached.InstalledWith != "brew" || !cached.Version.Valid || cached.Version.String != "2.0.0" {
		t.Fatalf("cached row = installed=%v installedWith=%q version=%+v, want installed via brew version 2.0.0", cached.Installed, cached.InstalledWith, cached.Version)
	}
	if cached.Outdated || cached.LatestVersion.Valid {
		t.Fatalf("outdated state = %v latest=%+v, want cleared", cached.Outdated, cached.LatestVersion)
	}
	if len(brew.installedChecks) != 1 || brew.installedChecks[0].Provider != "brew" {
		t.Fatalf("installed checks = %#v, want one brew-owned verification", brew.installedChecks)
	}
}

func TestUpgrade_SystemBrewFormulaUsesFormulaMode(t *testing.T) {
	ctx := context.Background()
	exec := executor.NewMatchMock(
		executor.MatchRule{Pattern: "brew list --versions flux", Response: executor.MockCall{Stdout: "flux 2.8.8\n"}},
		executor.MatchRule{Pattern: "brew upgrade --formula fluxcd/tap/flux", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "brew --version", Response: executor.MockCall{Stdout: "Homebrew 4.4.0\n"}},
		executor.MatchRule{Pattern: "brew outdated --json=v2", Response: executor.MockCall{Stdout: `{"formulae":[],"casks":[]}`}},
	).WithFallback(executor.MockCall{Err: errors.New("unexpected brew command")})
	brew := brewprovider.New(exec)
	system := &lifecycleProvider{stubProvider: stubProvider{name: "system", available: true}, resolvedName: "brew"}
	a, cfgPath := newImportApp(t, brew, system)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalToolPackage("flux", "system", "fluxcd/tap/flux")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("flux"),
		}},
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "flux",
		Provider:      "system",
		Package:       "fluxcd/tap/flux",
		Installed:     true,
		InstalledWith: "brew",
		Outdated:      true,
		Version:       sql.NullString{String: "2.8.8", Valid: true},
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	if err := a.Upgrade(ctx, "flux", "system"); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	if calls := exec.CallsMatching("brew upgrade --formula fluxcd/tap/flux"); len(calls) != 1 {
		t.Fatalf("formula upgrade calls = %+v, want one brew upgrade --formula fluxcd/tap/flux", calls)
	}
	if calls := exec.CallsMatching("brew upgrade flux"); len(calls) != 0 {
		t.Fatalf("bare upgrade calls = %+v, want none", calls)
	}
}

func TestCompleteExternalToolAction_UninstallRemovesConfigAndCache(t *testing.T) {
	ctx := context.Background()
	brew := &lifecycleProvider{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, brew)
	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("parsec", "system")),
		Groups: []*config.GroupConfig{{
			Name:  "testhost",
			Tools: groupTools("parsec"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "parsec",
		Provider:      "system",
		Package:       "parsec",
		Installed:     true,
		InstalledWith: "brew",
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	if err := a.CompleteExternalToolAction(ctx, provider.PrivilegeActionUninstall, "parsec", "system", "parsec", "brew"); err != nil {
		t.Fatalf("CompleteExternalToolAction: %v", err)
	}

	if _, err := a.DB().Get(ctx, "parsec", "system", "parsec"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cache row after uninstall err = %v, want sql.ErrNoRows", err)
	}
	gotCfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	if _, ok := gotCfg.Tools["parsec"]; ok {
		t.Fatal("logical tool spec survived external uninstall completion")
	}
	if len(gotCfg.Groups) != 1 || len(gotCfg.Groups[0].Tools) != 0 {
		t.Fatalf("group tools after uninstall = %+v, want parsec removed", gotCfg.Groups)
	}
}

func TestCompleteExternalToolActionWithState_InstallAndAddReturnsState(t *testing.T) {
	ctx := context.Background()
	brew := &lifecycleProvider{
		stubProvider: stubProvider{name: "brew", available: true},
		installed:    true,
		version:      "9.1.0",
	}
	a, cfgPath := newImportApp(t, brew)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{}); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	result, err := a.CompleteExternalToolActionWithState(ctx, app.CompleteExternalToolActionOptions{
		Action:       provider.PrivilegeActionInstall,
		Name:         "vim",
		ProviderName: "brew",
		Package:      "vim",
		AddToConfig:  true,
		GroupName:    "work",
		AssignHosts:  []string{"desktop"},
	})
	if err != nil {
		t.Fatalf("CompleteExternalToolActionWithState: %v", err)
	}
	if len(result.Tools) != 1 || result.Tools[0].Name != "vim" || !result.Tools[0].Tracked {
		t.Fatalf("Tools = %+v, want tracked vim", result.Tools)
	}
	key := "vim\x00brew"
	if result.GroupState == nil {
		t.Fatal("GroupState is nil")
	}
	if got := result.GroupState.ToolGroups[key]; got != "work" {
		t.Fatalf("ToolGroups[%q] = %q, want work", key, got)
	}
	if !slices.Contains(result.GroupState.ToolMemberships[key], "work") {
		t.Fatalf("ToolMemberships[%q] = %v, want work", key, result.GroupState.ToolMemberships[key])
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	if !slices.Contains(cfg.Hosts[testShortHostname()], "work") {
		t.Fatalf("current host groups = %v, want work", cfg.Hosts[testShortHostname()])
	}
	if !slices.Contains(cfg.Hosts["desktop"], "work") {
		t.Fatalf("desktop host groups = %v, want work", cfg.Hosts["desktop"])
	}
}

func TestCompleteExternalToolActionWithState_PersistsInstallOptions(t *testing.T) {
	ctx := context.Background()
	brew := &lifecycleProvider{
		stubProvider: stubProvider{name: "brew", available: true},
		installed:    true,
		version:      "1.2.3",
	}
	a, cfgPath := newImportApp(t, brew)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{}); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	_, err := a.CompleteExternalToolActionWithState(ctx, app.CompleteExternalToolActionOptions{
		Action:       provider.PrivilegeActionInstall,
		Name:         "visual-studio-code",
		ProviderName: "brew",
		Package:      "visual-studio-code",
		Options:      map[string]string{"brew_kind": "cask"},
		AddToConfig:  true,
		GroupName:    "work",
	})
	if err != nil {
		t.Fatalf("CompleteExternalToolActionWithState: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	spec := cfg.Tools["visual-studio-code"]
	if len(spec.Providers) != 1 || spec.Providers[0].Provider != "brew" {
		t.Fatalf("spec providers = %+v, want brew", spec.Providers)
	}
	if spec.Providers[0].Options["brew_kind"] != "cask" {
		t.Fatalf("spec provider options[brew_kind] = %q, want cask", spec.Providers[0].Options["brew_kind"])
	}
}

func TestCompleteExternalToolActionWithState_UninstallReturnsState(t *testing.T) {
	ctx := context.Background()
	brew := &lifecycleProvider{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, brew)
	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("parsec", "system")),
		Groups: []*config.GroupConfig{{
			Name:  "testhost",
			Tools: groupTools("parsec"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "parsec",
		Provider:      "system",
		Package:       "parsec",
		Installed:     true,
		InstalledWith: "brew",
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	result, err := a.CompleteExternalToolActionWithState(ctx, app.CompleteExternalToolActionOptions{
		Action:        provider.PrivilegeActionUninstall,
		Name:          "parsec",
		ProviderName:  "system",
		Package:       "parsec",
		InstalledWith: "brew",
	})
	if err != nil {
		t.Fatalf("CompleteExternalToolActionWithState: %v", err)
	}
	if len(result.Tools) != 0 {
		t.Fatalf("Tools = %+v, want empty after uninstall", result.Tools)
	}
	if result.GroupState == nil {
		t.Fatal("GroupState is nil")
	}
	key := "parsec\x00system"
	if got := result.GroupState.ToolMemberships[key]; len(got) != 0 {
		t.Fatalf("ToolMemberships[%q] = %v, want removed", key, got)
	}
}

func TestCompleteExternalToolAction_UninstallRejectsProviderTool(t *testing.T) {
	ctx := context.Background()
	node := &lifecycleProvider{stubProvider: stubProvider{name: "node", available: true}}
	a, cfgPath := newImportApp(t, node)
	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalFixtureTool{Name: "npm", Provider: "node", InstallWith: "npm"}),
		Groups: []*config.GroupConfig{{
			Name:  "testhost",
			Tools: groupTools("npm"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "npm",
		Provider:      "node",
		Package:       "npm",
		Installed:     true,
		InstalledWith: "npm",
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	err := a.CompleteExternalToolAction(ctx, provider.PrivilegeActionUninstall, "npm", "node", "npm", "npm")
	if err == nil || !strings.Contains(err.Error(), "package manager/provider") {
		t.Fatalf("CompleteExternalToolAction err = %v, want protected provider tool error", err)
	}

	if _, err := a.DB().Get(ctx, "npm", "node", "npm"); err != nil {
		t.Fatalf("cache row after rejected uninstall err = %v, want still present", err)
	}
	gotCfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	if _, ok := gotCfg.Tools["npm"]; !ok {
		t.Fatal("npm logical tool spec was removed despite protected provider guard")
	}
	if len(gotCfg.Groups) != 1 || !logicalTestGroupHasTool(gotCfg.Groups[0], "npm") {
		t.Fatalf("group tools after rejected uninstall = %+v, want npm preserved", gotCfg.Groups)
	}
}

func TestUninstall_UsesRegisteredInstalledWithProvider(t *testing.T) {
	system := &lifecycleProvider{stubProvider: stubProvider{name: "system", available: true}}
	brew := &lifecycleProvider{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, system, brew)
	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalToolPackage("ripgrep", "system", "rg")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.DB().Upsert(context.Background(), &database.ToolCache{
		Name:          "ripgrep",
		Provider:      "system",
		Package:       "rg",
		Installed:     true,
		InstalledWith: "brew",
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("db.Upsert: %v", err)
	}

	if err := a.Uninstall(context.Background(), "ripgrep", "system"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	if len(brew.uninstalled) != 1 || brew.uninstalled[0].Package != "rg" {
		t.Fatalf("brew uninstalled = %+v, want package rg", brew.uninstalled)
	}
	if len(system.uninstalled) != 0 {
		t.Fatalf("system uninstalled = %+v, want no calls", system.uninstalled)
	}
}

func TestUninstall_ReturnsCacheReadErrorBeforeProviderCall(t *testing.T) {
	brew := &lifecycleProvider{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, brew)
	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.DB().Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}

	if err := a.Uninstall(context.Background(), "ripgrep", "brew"); err == nil {
		t.Fatal("Uninstall error = nil, want cache read error")
	}
	if len(brew.uninstalled) != 0 {
		t.Fatalf("provider was called despite cache read error: %+v", brew.uninstalled)
	}
}

func TestUninstall_UsesCachedManagerForEcosystemProvider(t *testing.T) {
	python := &lifecycleProvider{stubProvider: stubProvider{name: "python", available: true}}
	a, cfgPath := newImportApp(t, python)
	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("black", "python")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("black"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.DB().Upsert(context.Background(), &database.ToolCache{
		Name:          "black",
		Provider:      "python",
		Package:       "black",
		Installed:     true,
		InstalledWith: "pip3",
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("db.Upsert: %v", err)
	}

	if err := a.Uninstall(context.Background(), "black", "python"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	if len(python.uninstallManagers) != 1 || python.uninstallManagers[0] != "pip3" {
		t.Fatalf("uninstall managers = %v, want [pip3]", python.uninstallManagers)
	}
}

func TestUpgrade_UsesRegisteredInstalledWithProvider(t *testing.T) {
	system := &lifecycleProvider{stubProvider: stubProvider{name: "system", available: true}}
	brew := &lifecycleProvider{
		stubProvider: stubProvider{name: "brew", available: true},
		installed:    true,
		version:      "15.0.0",
	}
	a, cfgPath := newImportApp(t, system, brew)
	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalToolPackage("ripgrep", "system", "rg")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.DB().Upsert(context.Background(), &database.ToolCache{
		Name:          "ripgrep",
		Provider:      "system",
		Package:       "rg",
		Installed:     true,
		InstalledWith: "brew",
		Version:       sql.NullString{String: "14.0.0", Valid: true},
		Outdated:      true,
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("db.Upsert: %v", err)
	}

	if err := a.Upgrade(context.Background(), "ripgrep", "system"); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	if len(brew.upgraded) != 1 || brew.upgraded[0].Package != "rg" {
		t.Fatalf("brew upgraded = %+v, want package rg", brew.upgraded)
	}
	if len(system.upgraded) != 0 {
		t.Fatalf("system upgraded = %+v, want no calls", system.upgraded)
	}
}

func TestUpgrade_ConfiguredScriptPreservesLifecycleCommands(t *testing.T) {
	ctx := context.Background()
	exec := executor.NewMatchMock(
		executor.MatchRule{Pattern: "sh -c exit 0", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "sh -c bun upgrade", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "sh -c bun-check", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "sh -c bun --version", Response: executor.MockCall{Stdout: "1.3.0\n"}},
	).WithFallback(executor.MockCall{Err: errors.New("unexpected script command")})
	a, cfgPath := newImportApp(t, scriptprovider.New(exec))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"bun": {Providers: []config.ToolInstallSpec{{Provider: "script", Options: map[string]string{
				"install": "bun install", "check": "bun-check", "version": "bun --version", "upgrade": "bun upgrade",
			}}}},
		},
		Groups: []*config.GroupConfig{{Tools: groupTools("bun")}},
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name: "bun", Provider: "script", Package: "bun", Installed: true, InstalledWith: "script",
		Version: sql.NullString{String: "1.2.3", Valid: true}, Outdated: true, LastChecked: time.Now(),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	if err := a.Upgrade(ctx, "bun", "script"); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	if calls := exec.CallsMatching("sh -c bun upgrade"); len(calls) != 1 {
		t.Fatalf("upgrade calls = %d, want 1", len(calls))
	}
}

func TestUpgrade_GitHubRecipeUsesCachedLatestRelease(t *testing.T) {
	ctx := context.Background()
	const assetURL = "https://downloads.example.test/gh_2.93.0_linux_amd64.tar.gz"
	exec := executor.NewMatchMock(executor.MatchRule{
		Pattern:  "sh -c gh --version",
		Response: executor.MockCall{Stdout: "2.93.0\n"},
	}).WithFallback(executor.MockCall{})
	a, cfgPath := newImportApp(t, scriptprovider.New(exec))
	a.SetGitHubFallbackAPIForTest("https://api.github.test", githubFallbackLatestReleaseClient(t, nil, func() io.ReadCloser {
		return githubFallbackReleaseBody("v2.93.0", "2026-05-27T17:47:41Z", "gh_2.93.0_linux_amd64.tar.gz", assetURL)
	}))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"gh": {Providers: []config.ToolInstallSpec{{
				Provider: "script",
				Bin:      "gh",
				Options:  map[string]string{"version": "gh --version"},
				Source:   &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "cli", Repo: "cli"},
				Recipe: &config.FallbackRecipe{
					Type: config.FallbackRecipeGitHubReleaseAsset, AssetPattern: "gh_{version}_linux_amd64.tar.gz",
				},
			}}},
		},
		Groups: []*config.GroupConfig{{Tools: groupTools("gh")}},
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name: "gh", Provider: "script", Package: "gh", Installed: true, InstalledWith: "script",
		Version: sql.NullString{String: "2.92.0", Valid: true}, LastChecked: time.Now(),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	if err := a.DB().UpdateOutdated(ctx, "gh", "script", "gh", true, "v2.93.0"); err != nil {
		t.Fatalf("seed outdated: %v", err)
	}

	if err := a.UpgradeWithOptions(ctx, "gh", "script", app.UpgradeOptions{Force: true}); err != nil {
		t.Fatalf("UpgradeWithOptions: %v", err)
	}

	var hydratedCalls int
	for _, call := range exec.CallsMatching("sh -c") {
		if strings.Contains(strings.Join(call.Args, " "), assetURL) {
			hydratedCalls++
		}
	}
	if hydratedCalls != 1 {
		t.Fatalf("hydrated GitHub upgrade calls = %d, want 1", hydratedCalls)
	}
}

func TestUpgrade_ConcreteProviderRequestUsesConfiguredEcosystemTool(t *testing.T) {
	system := &lifecycleProvider{stubProvider: stubProvider{name: "system", available: true}, resolvedName: "brew"}
	brew := &lifecycleProvider{
		stubProvider: stubProvider{name: "brew", available: true},
		installed:    true,
		version:      "15.0.0",
	}
	a, cfgPath := newImportApp(t, system, brew)
	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalToolPackage("ripgrep", "system", "rg")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.DB().Upsert(context.Background(), &database.ToolCache{
		Name:          "ripgrep",
		Provider:      "system",
		Package:       "rg",
		Installed:     true,
		InstalledWith: "brew",
		Version:       sql.NullString{String: "14.0.0", Valid: true},
		Outdated:      true,
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("db.Upsert: %v", err)
	}

	if err := a.Upgrade(context.Background(), "ripgrep", "brew"); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	if len(brew.upgraded) != 1 || brew.upgraded[0].Package != "rg" {
		t.Fatalf("brew upgraded = %+v, want package rg", brew.upgraded)
	}
	got, err := a.DB().Get(context.Background(), "ripgrep", "system", "rg")
	if err != nil {
		t.Fatalf("Get configured row: %v", err)
	}
	if got.Outdated {
		t.Fatal("configured row should no longer be outdated")
	}
	if _, err := a.DB().Get(context.Background(), "ripgrep", "brew", "ripgrep"); err == nil {
		t.Fatal("unexpected fallback concrete cache row for brew/ripgrep")
	}
}

func TestUpgrade_ReturnsCacheReadErrorBeforeProviderCall(t *testing.T) {
	brew := &lifecycleProvider{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, brew)
	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.DB().Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}

	if err := a.Upgrade(context.Background(), "ripgrep", "brew"); err == nil {
		t.Fatal("Upgrade error = nil, want cache read error")
	}
	if len(brew.upgraded) != 0 {
		t.Fatalf("provider was called despite cache read error: %+v", brew.upgraded)
	}
}

package app_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
)

func TestBootstrapPlanDetectsManagersAndHost(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost.example")
	binDir := t.TempDir()
	writeTestExecutable(t, binDir, "pnpm")
	writeTestExecutable(t, binDir, "npm")
	writeTestExecutable(t, binDir, "pip3")
	t.Setenv("PATH", binDir)
	a, _ := newImportApp(t)

	plan, err := a.BootstrapPlan(context.Background())
	if err != nil {
		t.Fatalf("BootstrapPlan: %v", err)
	}
	if plan.HasConfig {
		t.Fatal("BootstrapPlan.HasConfig = true, want false")
	}
	if plan.NodeManager != "pnpm" {
		t.Fatalf("BootstrapPlan.NodeManager = %q, want pnpm", plan.NodeManager)
	}
	if plan.PythonManager != "pip3" {
		t.Fatalf("BootstrapPlan.PythonManager = %q, want pip3", plan.PythonManager)
	}
	if plan.HostName != "testhost" {
		t.Fatalf("BootstrapPlan.HostName = %q, want testhost", plan.HostName)
	}
}

func TestBootstrapPlanDetectsAnyAvailableProvider(t *testing.T) {
	a, _ := newImportApp(t,
		&stubProvider{name: "apt", available: false},
		&stubProvider{name: "brew", available: true},
	)

	plan, err := a.BootstrapPlan(context.Background())
	if err != nil {
		t.Fatalf("BootstrapPlan: %v", err)
	}
	if !plan.AnyProviderAvailable {
		t.Fatal("BootstrapPlan.AnyProviderAvailable = false, want true")
	}
	if len(plan.Providers) != 2 {
		t.Fatalf("BootstrapPlan.Providers len = %d, want 2", len(plan.Providers))
	}
	availability := make(map[string]bool, len(plan.Providers))
	for _, p := range plan.Providers {
		availability[p.Name] = p.Available
	}
	if availability["apt"] {
		t.Fatalf("apt availability = true, want false: %+v", plan.Providers)
	}
	if !availability["brew"] {
		t.Fatalf("brew availability = false, want true: %+v", plan.Providers)
	}
}

func TestSetupHostSummariesUseActiveOrCurrentHost(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "desk.example.com")
	if got := app.DefaultSetupHostName(); got != "desk" {
		t.Fatalf("DefaultSetupHostName = %q, want desk", got)
	}

	fallback := app.SetupActivationHostSummary(nil)
	if fallback.Host != "desk" || len(fallback.Groups) != 0 {
		t.Fatalf("fallback setup activation summary = %#v, want desk with no groups", fallback)
	}

	active := app.SetupActivationHostSummary(&app.HostInfo{
		Active: "workstation",
		Hosts: map[string]config.HostAssignment{
			"workstation": {Groups: []string{"dev", "ops"}},
		},
	})
	if active.Host != "workstation" || !slices.Equal(active.Groups, []string{"dev", "ops"}) {
		t.Fatalf("active setup activation summary = %#v, want workstation dev/ops", active)
	}
	active.Groups[0] = "changed"
	again := app.SetupActivationHostSummary(&app.HostInfo{
		Active: "workstation",
		Hosts: map[string]config.HostAssignment{
			"workstation": {Groups: []string{"dev", "ops"}},
		},
	})
	if again.Groups[0] != "dev" {
		t.Fatalf("SetupActivationHostSummary leaked groups slice: %#v", again.Groups)
	}
}

func TestApplyBootstrapCreatesConfigManagersAndHost(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "virtualhost.example")
	a, cfgPath := newImportApp(t)

	result, err := a.ApplyBootstrap(context.Background(), app.BootstrapApplyOptions{
		NodeManager:   "pnpm",
		PythonManager: "pip3",
	})
	if err != nil {
		t.Fatalf("ApplyBootstrap: %v", err)
	}
	if !result.CreatedConfig {
		t.Fatal("ApplyBootstrap.CreatedConfig = false, want true")
	}
	if result.ConfigPath != cfgPath {
		t.Fatalf("ApplyBootstrap.ConfigPath = %q, want %q", result.ConfigPath, cfgPath)
	}
	if !result.Host.Created {
		t.Fatal("ApplyBootstrap.Host.Created = false, want true")
	}
	if result.Host.Host != "virtualhost" {
		t.Fatalf("ApplyBootstrap.Host.Host = %q, want virtualhost", result.Host.Host)
	}

	settings, err := a.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if got := settings.EcosystemManager("node"); got != "pnpm" {
		t.Fatalf("node manager = %q, want pnpm", got)
	}
	if got := settings.EcosystemManager("python"); got != "pip3" {
		t.Fatalf("python manager = %q, want pip3", got)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, ok := cfg.Hosts["virtualhost"]; !ok {
		t.Fatalf("hosts = %v, want virtualhost", cfg.Hosts)
	}
	if group := findAppTestGroup(cfg, "virtualhost"); group == nil || !group.IsHost() {
		t.Fatalf("virtualhost group = %#v, want special host group", group)
	}
}

func TestSetupImportSavesDisabledProvidersAndReturnsHostInfo(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "setupbox.example")
	brew := &stubProvider{
		name:      "brew",
		available: true,
		installed: []provider.InstalledTool{
			installedTool("ripgrep", "14.1.1", "brew"),
		},
	}
	a, cfgPath := newImportApp(t, brew)

	result, err := a.SetupImport(context.Background(), []string{"node"})
	if err != nil {
		t.Fatalf("SetupImport: %v", err)
	}
	if result.Added != 1 {
		t.Fatalf("SetupImport.Added = %d, want 1", result.Added)
	}
	if result.HostInfo == nil {
		t.Fatal("SetupImport.HostInfo is nil")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	// The node family disable is stored as its concrete members.
	if got := cfg.HostSettings["setupbox"].DisabledProviders; !slices.Equal(got, []string{"bun", "pnpm", "npm"}) {
		t.Fatalf("disabled providers = %v, want [bun pnpm npm]", got)
	}
	hostGroup := findAppTestGroup(cfg, "setupbox")
	if hostGroup == nil || !hostGroup.IsHost() || !testGroupHasTool(hostGroup, "ripgrep") {
		t.Fatalf("host group = %#v, want host group containing ripgrep", hostGroup)
	}
}

func TestEnsureBootstrapHostReusesExistingHost(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Hosts: map[string][]string{"testhost": {"work"}},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "work"},
		},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	result, err := a.EnsureBootstrapHost()
	if err != nil {
		t.Fatalf("EnsureBootstrapHost: %v", err)
	}
	if result.Created {
		t.Fatal("EnsureBootstrapHost.Created = true, want false")
	}
	if result.Host != "testhost" {
		t.Fatalf("EnsureBootstrapHost.Host = %q, want testhost", result.Host)
	}
	if len(result.Groups) != 2 || result.Groups[0] != "testhost" || result.Groups[1] != "work" {
		t.Fatalf("EnsureBootstrapHost.Groups = %v, want [testhost work]", result.Groups)
	}
}

func TestEnsureSetupHostUsesCurrentMachineHost(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "desk.example.com")
	a, _ := newImportApp(t)

	result, err := a.EnsureSetupHost("typed-legacy-name")
	if err != nil {
		t.Fatalf("EnsureSetupHost: %v", err)
	}
	if result.Host.Host != "desk" {
		t.Fatalf("host = %q, want desk", result.Host.Host)
	}
	if result.Info == nil || result.Info.Active != "desk" {
		t.Fatalf("active host info = %#v, want desk", result.Info)
	}
	if _, ok := result.Info.Hosts["typed-legacy-name"]; ok {
		t.Fatalf("typed legacy host should not be created: %#v", result.Info.Hosts)
	}
}

func TestCopyHostConfigToCurrentHost(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "desk.example.com")
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalToolPackage("fd", "brew", "fd-find")),
		HostSettings: map[string]config.Settings{
			"alpha": {DotsRepo: "/alpha/dots", DisabledProviders: []string{"node"}},
		},
		Groups: []*config.GroupConfig{
			{Name: "alpha", Special: "host"},
			{Name: "shared"},
		},
		Hosts: map[string][]string{"alpha": {"shared"}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	result, err := a.CopyHostConfigToCurrentHost("alpha")
	if err != nil {
		t.Fatalf("CopyHostConfigToCurrentHost: %v", err)
	}
	if result.Source != "alpha" || result.Target != "desk" {
		t.Fatalf("source/target = %q/%q, want alpha/desk", result.Source, result.Target)
	}
	if result.Info == nil || result.Info.Active != "desk" {
		t.Fatalf("active host info = %#v, want desk", result.Info)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if groups := cfg.Hosts["desk"]; !slices.Equal(groups, []string{"shared"}) {
		t.Fatalf("desk groups = %v, want [shared]", groups)
	}
	if cfg.HostSettings["desk"].DotsRepo != "/alpha/dots" {
		t.Fatalf("desk dots repo = %q, want copied source", cfg.HostSettings["desk"].DotsRepo)
	}
}

func TestSetCurrentHostGroups(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "desk.example.com")
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Groups: []*config.GroupConfig{
			{Name: "desk", Special: "host"},
			{Name: "shared"},
		},
		Hosts: map[string][]string{"desk": {}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	result, err := a.SetCurrentHostGroups([]string{"shared"})
	if err != nil {
		t.Fatalf("SetCurrentHostGroups: %v", err)
	}
	if !slices.Equal(result.Groups, []string{"shared"}) {
		t.Fatalf("result groups = %v, want [shared]", result.Groups)
	}
	if result.Info == nil || result.Info.Active != "desk" {
		t.Fatalf("active host info = %#v, want desk", result.Info)
	}
	if groups := result.Info.Hosts["desk"].Groups; !slices.Equal(groups, []string{"shared"}) {
		t.Fatalf("desk groups = %v, want [shared]", groups)
	}
}

func TestCopyHostGroupsToCurrentHost(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "desk.example.com")
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Groups: []*config.GroupConfig{
			{Name: "desk", Special: "host"},
			{Name: "beta", Special: "host"},
			{Name: "shared"},
		},
		Hosts: map[string][]string{"desk": {}, "beta": {"shared"}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	result, err := a.CopyHostGroupsToCurrentHost("beta")
	if err != nil {
		t.Fatalf("CopyHostGroupsToCurrentHost: %v", err)
	}
	if result.Target != "desk" {
		t.Fatalf("target = %q, want desk", result.Target)
	}
	if !slices.Equal(result.Groups, []string{"shared"}) {
		t.Fatalf("result groups = %v, want [shared]", result.Groups)
	}
	if result.Info == nil || result.Info.Active != "desk" {
		t.Fatalf("active host info = %#v, want desk", result.Info)
	}
	if got := result.Info.Hosts["desk"].Groups; !slices.Equal(got, []string{"shared"}) {
		t.Fatalf("desk groups = %v, want [shared]", got)
	}
}

func TestConfigureDotsRepoPersistsAndBootstraps(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	t.Setenv("HOME", t.TempDir())
	repoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoDir, "dotfiles", "nvim"), 0o755); err != nil {
		t.Fatal(err)
	}
	a, cfgPath := newImportApp(t)
	if err := a.SaveDotsDisabled(context.Background(), true); err != nil {
		t.Fatalf("SaveDotsDisabled: %v", err)
	}

	result, err := a.ConfigureDotsRepo(context.Background(), repoDir)
	if err != nil {
		t.Fatalf("ConfigureDotsRepo: %v", err)
	}
	if result.RepoPath != repoDir {
		t.Fatalf("RepoPath = %q, want %q", result.RepoPath, repoDir)
	}
	if len(result.Entries) != 1 || result.Entries[0].Name != "nvim" {
		t.Fatalf("entries = %#v, want nvim", result.Entries)
	}

	settings, err := a.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if settings.DotsRepo != repoDir {
		t.Fatalf("DotsRepo = %q, want %q", settings.DotsRepo, repoDir)
	}
	if config.BoolVal(settings.DotsDisabled) {
		t.Fatal("DotsDisabled should be false")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	group := findAppTestGroup(cfg, "testhost")
	if group == nil || len(group.Dots) != 1 || group.Dots[0].Name != "nvim" {
		t.Fatalf("bootstrap dots group = %#v, want testhost/nvim", group)
	}
}

func TestConfigureDotsRepoNormalizesRelativePathAndRejectsMissingWithoutSaving(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	ctx := context.Background()
	a, _ := newImportApp(t)
	originalRepo := t.TempDir()
	if err := a.SaveSettings(ctx, config.Settings{DotsRepo: originalRepo}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	cwd := t.TempDir()
	repoName := "relative-repo"
	repoDir := filepath.Join(cwd, repoName)
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	startWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(startWD) })

	result, err := a.ConfigureDotsRepo(ctx, repoName)
	if err != nil {
		t.Fatalf("ConfigureDotsRepo(%q): %v", repoName, err)
	}
	resolvedResultRepo, err := filepath.EvalSymlinks(result.RepoPath)
	if err != nil {
		t.Fatalf("EvalSymlinks(result.RepoPath): %v", err)
	}
	resolvedRepoDir, err := filepath.EvalSymlinks(repoDir)
	if err != nil {
		t.Fatalf("EvalSymlinks(repoDir): %v", err)
	}
	if resolvedResultRepo != resolvedRepoDir {
		t.Fatalf("RepoPath = %q, want normalized %q", result.RepoPath, repoDir)
	}
	settings, err := a.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	savedRepo := settings.DotsRepo
	resolvedSavedRepo, err := filepath.EvalSymlinks(savedRepo)
	if err != nil {
		t.Fatalf("EvalSymlinks(settings.DotsRepo): %v", err)
	}
	if resolvedSavedRepo != resolvedRepoDir {
		t.Fatalf("DotsRepo = %q, want normalized %q", settings.DotsRepo, repoDir)
	}

	missing := filepath.Join(cwd, "missing")
	if _, err := a.ConfigureDotsRepo(ctx, missing); err == nil {
		t.Fatalf("ConfigureDotsRepo(%q) error = nil, want invalid path error", missing)
	} else if !strings.Contains(err.Error(), "repo path") {
		t.Fatalf("ConfigureDotsRepo(%q) error = %v, want repo path validation error", missing, err)
	}
	settings, err = a.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if settings.DotsRepo != savedRepo {
		t.Fatalf("after missing path, DotsRepo = %q, want unchanged %q", settings.DotsRepo, savedRepo)
	}
}

func TestDotsSyncConfiguredRequiresRepoAndEnabled(t *testing.T) {
	a, _ := newImportApp(t)

	availability, err := a.DotsSyncAvailability()
	if err != nil {
		t.Fatalf("DotsSyncAvailability: %v", err)
	}
	if availability.Configured || availability.Reason != app.DotsSyncAvailabilityNoRepo {
		t.Fatalf("DotsSyncAvailability = %+v, want no repo unavailable", availability)
	}

	configured, err := a.DotsSyncConfigured()
	if err != nil {
		t.Fatalf("DotsSyncConfigured: %v", err)
	}
	if configured {
		t.Fatal("DotsSyncConfigured = true, want false without dots repo")
	}

	if err := a.SaveSettings(context.Background(), config.Settings{DotsRepo: t.TempDir()}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	availability, err = a.DotsSyncAvailability()
	if err != nil {
		t.Fatalf("DotsSyncAvailability with repo: %v", err)
	}
	if !availability.Configured || availability.Reason != app.DotsSyncAvailabilityReady {
		t.Fatalf("DotsSyncAvailability = %+v, want ready", availability)
	}
	if availability.RepoPath == "" {
		t.Fatalf("DotsSyncAvailability RepoPath = %q, want configured repo path", availability.RepoPath)
	}

	configured, err = a.DotsSyncConfigured()
	if err != nil {
		t.Fatalf("DotsSyncConfigured with repo: %v", err)
	}
	if !configured {
		t.Fatal("DotsSyncConfigured = false, want true with dots repo")
	}

	if err := a.SaveDotsDisabled(context.Background(), true); err != nil {
		t.Fatalf("SaveDotsDisabled: %v", err)
	}
	availability, err = a.DotsSyncAvailability()
	if err != nil {
		t.Fatalf("DotsSyncAvailability disabled: %v", err)
	}
	if availability.Configured || availability.Reason != app.DotsSyncAvailabilityDisabled {
		t.Fatalf("DotsSyncAvailability = %+v, want disabled unavailable", availability)
	}
	if availability.RepoPath == "" {
		t.Fatalf("disabled DotsSyncAvailability RepoPath = %q, want configured repo path", availability.RepoPath)
	}

	configured, err = a.DotsSyncConfigured()
	if err != nil {
		t.Fatalf("DotsSyncConfigured disabled: %v", err)
	}
	if configured {
		t.Fatal("DotsSyncConfigured = true, want false when dots are disabled")
	}
}

func TestBootstrapStateKeyUsesWideHash(t *testing.T) {
	// Verify the state key uses at least 128-bit hash (32 hex chars)
	// to avoid birthday collisions across config paths.
	t.Setenv("OMNI_HOSTNAME", "testhost")
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Hosts:  map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{{Name: "testhost", Special: "host"}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.MarkHostBootstrapCompleted(context.Background(), "testhost"); err != nil {
		t.Fatalf("MarkHostBootstrapCompleted: %v", err)
	}
	// After marking, verify the marker persists (round-trip).
	completed, err := a.HostBootstrapCompleted(context.Background(), "testhost")
	if err != nil {
		t.Fatalf("HostBootstrapCompleted: %v", err)
	}
	if !completed {
		t.Fatal("HostBootstrapCompleted = false after marking, want true")
	}
	// A different config path must NOT match.
	a2 := app.New(filepath.Join(t.TempDir(), "other-settings.json"))
	if err := saveAppConfig(t, a2.ConfigPath, &config.RootConfig{
		Hosts:  map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{{Name: "testhost", Special: "host"}},
	}); err != nil {
		t.Fatalf("config.Save a2: %v", err)
	}
	a2.CacheDir = a.CacheDir // share same cache DB
	if err := a2.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode a2: %v", err)
	}
	t.Cleanup(func() { _ = a2.Close() })
	completed2, err := a2.HostBootstrapCompleted(context.Background(), "testhost")
	if err != nil {
		t.Fatalf("HostBootstrapCompleted a2: %v", err)
	}
	if completed2 {
		t.Fatal("different config path matched bootstrap marker — hash collision or key too short")
	}
}

func TestMarkCurrentHostBootstrapCompletedUsesActiveHost(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost.example")
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Hosts: map[string][]string{"testhost": {"work"}, "other": {}},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "other", Special: "host"},
			{Name: "work"},
		},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.MarkCurrentHostBootstrapCompleted(context.Background()); err != nil {
		t.Fatalf("MarkCurrentHostBootstrapCompleted: %v", err)
	}
	completed, err := a.HostBootstrapCompleted(context.Background(), "testhost")
	if err != nil {
		t.Fatalf("HostBootstrapCompleted testhost: %v", err)
	}
	if !completed {
		t.Fatal("HostBootstrapCompleted(testhost) = false, want true")
	}
	otherCompleted, err := a.HostBootstrapCompleted(context.Background(), "other")
	if err != nil {
		t.Fatalf("HostBootstrapCompleted other: %v", err)
	}
	if otherCompleted {
		t.Fatal("HostBootstrapCompleted(other) = true, want false")
	}
}

func writeTestExecutable(t testing.TB, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("os.WriteFile(%s): %v", name, err)
	}
}

func findAppTestGroup(cfg *config.RootConfig, name string) *config.GroupConfig {
	for _, group := range cfg.Groups {
		if group != nil && group.Name == name {
			return group
		}
	}
	return nil
}

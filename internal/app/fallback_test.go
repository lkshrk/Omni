package app_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/executor"
	isync "github.com/lkshrk/omni/internal/sync"
)

func TestInstallToolFallback_RunsInstallCheckAndUpdatesState(t *testing.T) {
	ctx := context.Background()
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "sh -c install rg", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "sh -c command -v rg", Response: executor.MockCall{}},
	).WithFallback(executor.MockCall{Err: context.DeadlineExceeded})
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true}, &stubProvider{name: "apt", available: true})
	a.SetFallbackExecutor(mock)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
					Status: config.FallbackStatusUnverified,
					Commands: config.FallbackCommands{
						Install: "install rg",
						Check:   "command -v rg",
					},
				},
			},
		},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.InstallToolFallback(ctx, "rg"); err != nil {
		t.Fatalf("InstallToolFallback: %v", err)
	}
	mock.AssertCalled(t, "sh -c install rg")
	mock.AssertCalled(t, "sh -c command -v rg")

	cached, err := a.DB().Get(ctx, "rg", "apt", "rg")
	if err != nil {
		t.Fatalf("Get rg: %v", err)
	}
	if !cached.Installed || cached.InstalledWith != "gh" {
		t.Fatalf("cached = installed %v with %q, want installed with gh", cached.Installed, cached.InstalledWith)
	}
	got, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got.Tools["rg"].Fallback.Status != config.FallbackStatusVerified {
		t.Fatalf("fallback status = %q, want verified", got.Tools["rg"].Fallback.Status)
	}
}

func TestInstallToolFallback_MaterializesTemplateRecipe(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true}, &stubProvider{name: "apt", available: true})
	a.CacheDir = filepath.Join(t.TempDir(), "cache")
	binDir := filepath.Join(home, ".local", "share", "omni", "fallback", "bin")
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{FallbackBinDir: "~/.local/share/omni/fallback/bin"},
		Tools: map[string]config.ToolSpec{
			"rg": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
					Status: config.FallbackStatusUnverified,
					Binary: "rg",
					Commands: config.FallbackCommands{
						Install: `mkdir -p "{{bin_dir}}" "{{cache_dir}}" && printf '#!/bin/sh\nexit 0\n' > "{{bin_dir}}/{{binary}}" && chmod +x "{{bin_dir}}/{{binary}}"`,
						Check:   `test -x "{{bin_dir}}/{{binary}}" && test -d "{{cache_dir}}" && test "{{repo}}" = "BurntSushi/ripgrep"`,
					},
				},
			},
		},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.InstallToolFallback(ctx, "rg"); err != nil {
		t.Fatalf("InstallToolFallback: %v", err)
	}

	info, err := os.Stat(filepath.Join(binDir, "rg"))
	if err != nil {
		t.Fatalf("fallback binary stat: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("fallback binary mode = %v, want executable", info.Mode().Perm())
	}
	cached, err := a.DB().Get(ctx, "rg", "apt", "rg")
	if err != nil {
		t.Fatalf("Get rg: %v", err)
	}
	if !cached.Installed || cached.InstalledWith != "gh" {
		t.Fatalf("cached = installed %v with %q, want installed with gh", cached.Installed, cached.InstalledWith)
	}
	got, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got.Tools["rg"].Fallback.Status != config.FallbackStatusVerified {
		t.Fatalf("fallback status = %q, want verified", got.Tools["rg"].Fallback.Status)
	}
}

func TestInstallToolFallback_RejectsUnknownTemplateVariable(t *testing.T) {
	ctx := context.Background()
	mock := executor.NewMatchMock().WithFallback(executor.MockCall{})
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true}, &stubProvider{name: "apt", available: true})
	a.SetFallbackExecutor(mock)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
					Status: config.FallbackStatusUnverified,
					Commands: config.FallbackCommands{
						Install: "echo {{missing}}",
						Check:   "command -v rg",
					},
				},
			},
		},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	err := a.InstallToolFallback(ctx, "rg")
	if err == nil || !strings.Contains(err.Error(), `unknown fallback template variable "missing"`) {
		t.Fatalf("InstallToolFallback err = %v, want unknown template variable", err)
	}
	if mock.CallCount() != 0 {
		t.Fatalf("fallback command count = %d, want no shell execution", mock.CallCount())
	}
	got, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got.Tools["rg"].Fallback.Status != config.FallbackStatusFailed {
		t.Fatalf("fallback status = %q, want failed", got.Tools["rg"].Fallback.Status)
	}
}

func TestInstallToolFallback_CheckFailureMarksFailed(t *testing.T) {
	ctx := context.Background()
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "sh -c install rg", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "sh -c command -v rg", Response: executor.MockCall{Err: context.DeadlineExceeded}},
	).WithFallback(executor.MockCall{})
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true}, &stubProvider{name: "apt", available: true})
	a.SetFallbackExecutor(mock)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
					Status: config.FallbackStatusUnverified,
					Commands: config.FallbackCommands{
						Install: "install rg",
						Check:   "command -v rg",
					},
				},
			},
		},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	err := a.InstallToolFallback(ctx, "rg")
	if err == nil || !strings.Contains(err.Error(), "install verification failed") {
		t.Fatalf("InstallToolFallback err = %v, want verification failure", err)
	}
	got, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got.Tools["rg"].Fallback.Status != config.FallbackStatusFailed {
		t.Fatalf("fallback status = %q, want failed", got.Tools["rg"].Fallback.Status)
	}
}

func TestInstall_UsesFallbackWhenNativePackageUnavailable(t *testing.T) {
	ctx := context.Background()
	system := &lifecycleProvider{
		stubProvider: stubProvider{name: "system", available: true},
		resolvedName: "apt",
		installed:    true,
		version:      "fallback-version",
	}
	apt := &lifecycleProvider{
		stubProvider: stubProvider{name: "apt", available: true},
		installed:    true,
		version:      "native-version",
	}
	fallbackExec := executor.NewMatchMock(
		executor.MatchRule{Pattern: "sh -c install rg", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "sh -c command -v rg", Response: executor.MockCall{}},
	).WithFallback(executor.MockCall{Err: errors.New("unexpected fallback command")})
	a, cfgPath := newImportApp(t, system, apt)
	a.SetFallbackExecutor(fallbackExec)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Package:   "ripgrep",
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
					Status: config.FallbackStatusUnverified,
					Commands: config.FallbackCommands{
						Install: "install rg",
						Check:   "command -v rg",
					},
				},
			},
		},
		Groups: []*config.GroupConfig{{Tools: []config.ToolEntry{{Name: "rg"}}}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().UpsertPackageAvailability(ctx, database.PackageAvailability{
		Name:      "rg",
		Provider:  "apt",
		Package:   "ripgrep",
		Available: false,
		Reason:    "no candidate",
	}); err != nil {
		t.Fatalf("seed package availability: %v", err)
	}

	if err := a.Install(ctx, "rg", "apt"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if len(apt.upgraded) != 0 || len(apt.installedChecks) != 0 {
		t.Fatalf("apt lifecycle calls = upgraded:%v checks:%v, want no native lifecycle", apt.upgraded, apt.installedChecks)
	}
	fallbackExec.AssertCalled(t, "sh -c install rg")
	fallbackExec.AssertCalled(t, "sh -c command -v rg")
	cached, err := a.DB().Get(ctx, "rg", "apt", "ripgrep")
	if err != nil {
		t.Fatalf("Get rg: %v", err)
	}
	if !cached.Installed || cached.InstalledWith != "gh" {
		t.Fatalf("cached = installed %v with %q, want installed with gh", cached.Installed, cached.InstalledWith)
	}
	got, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got.Tools["rg"].Fallback.Status != config.FallbackStatusVerified {
		t.Fatalf("fallback status = %q, want verified", got.Tools["rg"].Fallback.Status)
	}
}

func TestSync_UsesFallbackWhenNativePackageUnavailable(t *testing.T) {
	ctx := context.Background()
	system := &lifecycleProvider{
		stubProvider: stubProvider{name: "system", available: true},
		resolvedName: "apt",
	}
	apt := &lifecycleProvider{stubProvider: stubProvider{name: "apt", available: true}}
	fallbackExec := executor.NewMatchMock(
		executor.MatchRule{Pattern: "sh -c install rg", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "sh -c command -v rg", Response: executor.MockCall{}},
	).WithFallback(executor.MockCall{Err: errors.New("unexpected fallback command")})
	a, cfgPath := newImportApp(t, system, apt)
	a.SetFallbackExecutor(fallbackExec)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Package:   "ripgrep",
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
					Status: config.FallbackStatusUnverified,
					Commands: config.FallbackCommands{
						Install: "install rg",
						Check:   "command -v rg",
					},
				},
			},
		},
		Groups: []*config.GroupConfig{{Tools: []config.ToolEntry{{Name: "rg"}}}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().UpsertPackageAvailability(ctx, database.PackageAvailability{
		Name:      "rg",
		Provider:  "apt",
		Package:   "ripgrep",
		Available: false,
		Reason:    "no candidate",
	}); err != nil {
		t.Fatalf("seed package availability: %v", err)
	}

	result, err := a.Sync(ctx, isync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	fallbackExec.AssertCalled(t, "sh -c install rg")
	if installed := result.Installed(); len(installed) != 1 || installed[0].Tool.Name != "rg" {
		t.Fatalf("installed ops = %+v, want fallback install op for rg", installed)
	}
	cached, err := a.DB().Get(ctx, "rg", "apt", "ripgrep")
	if err != nil {
		t.Fatalf("Get rg: %v", err)
	}
	if !cached.Installed || cached.InstalledWith != "gh" {
		t.Fatalf("cached = installed %v with %q, want installed with gh", cached.Installed, cached.InstalledWith)
	}
}

func TestSync_UsesNativeCandidateBeforeFallbackWhenAnyProviderAvailable(t *testing.T) {
	ctx := context.Background()
	apt := &stubProvider{name: "apt", available: true}
	brew := &stubProvider{name: "brew", available: true}
	fallbackExec := executor.NewMatchMock().WithFallback(executor.MockCall{Err: errors.New("unexpected fallback command")})
	a, cfgPath := newImportApp(t, apt, brew)
	a.SetFallbackExecutor(fallbackExec)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Providers: []config.ToolInstallSpec{
					{Provider: "apt", Package: "ripgrep"},
					{Provider: "brew", Package: "ripgrep"},
				},
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
					Status: config.FallbackStatusUnverified,
					Commands: config.FallbackCommands{
						Install: "install rg",
						Check:   "command -v rg",
					},
				},
			},
		},
		Groups: []*config.GroupConfig{{Tools: []config.ToolEntry{{Name: "rg"}}}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().UpsertPackageAvailability(ctx, database.PackageAvailability{
		Name:      "rg",
		Provider:  "apt",
		Package:   "ripgrep",
		Available: false,
		Reason:    "no apt candidate",
	}); err != nil {
		t.Fatalf("seed apt package availability: %v", err)
	}
	if err := a.DB().UpsertPackageAvailability(ctx, database.PackageAvailability{
		Name:      "rg",
		Provider:  "brew",
		Package:   "ripgrep",
		Available: true,
	}); err != nil {
		t.Fatalf("seed brew package availability: %v", err)
	}

	_, err := a.Sync(ctx, isync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if fallbackExec.CallCount() != 0 {
		t.Fatalf("fallback command count = %d, want native install without fallback", fallbackExec.CallCount())
	}
	if len(apt.installed) != 0 {
		t.Fatalf("apt installed = %+v, want skipped unavailable candidate", apt.installed)
	}
	if len(brew.installed) != 1 || brew.installed[0].EffectivePackage() != "ripgrep" {
		t.Fatalf("brew installed = %+v, want ripgrep native install", brew.installed)
	}
	cached, err := a.DB().Get(ctx, "rg", "brew", "ripgrep")
	if err != nil {
		t.Fatalf("Get brew rg: %v", err)
	}
	if !cached.Installed || cached.InstalledWith == "gh" {
		t.Fatalf("cached = installed %v with %q, want native brew install", cached.Installed, cached.InstalledWith)
	}
}

func TestSync_UsesFallbackOnlyWhenAllNativeCandidatesUnavailable(t *testing.T) {
	ctx := context.Background()
	apt := &stubProvider{name: "apt", available: true}
	brew := &stubProvider{name: "brew", available: true}
	fallbackExec := executor.NewMatchMock(
		executor.MatchRule{Pattern: "sh -c install rg", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "sh -c command -v rg", Response: executor.MockCall{}},
	).WithFallback(executor.MockCall{Err: errors.New("unexpected fallback command")})
	a, cfgPath := newImportApp(t, apt, brew)
	a.SetFallbackExecutor(fallbackExec)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Providers: []config.ToolInstallSpec{
					{Provider: "apt", Package: "ripgrep"},
					{Provider: "brew", Package: "ripgrep"},
				},
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
					Status: config.FallbackStatusUnverified,
					Commands: config.FallbackCommands{
						Install: "install rg",
						Check:   "command -v rg",
					},
				},
			},
		},
		Groups: []*config.GroupConfig{{Tools: []config.ToolEntry{{Name: "rg"}}}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	for _, providerName := range []string{"apt", "brew"} {
		if err := a.DB().UpsertPackageAvailability(ctx, database.PackageAvailability{
			Name:      "rg",
			Provider:  providerName,
			Package:   "ripgrep",
			Available: false,
			Reason:    "no " + providerName + " candidate",
		}); err != nil {
			t.Fatalf("seed %s package availability: %v", providerName, err)
		}
	}

	result, err := a.Sync(ctx, isync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	fallbackExec.AssertCalled(t, "sh -c install rg")
	if len(apt.installed) != 0 || len(brew.installed) != 0 {
		t.Fatalf("native installs = apt:%+v brew:%+v, want fallback only", apt.installed, brew.installed)
	}
	if installed := result.Installed(); len(installed) != 1 || installed[0].Tool.Name != "rg" {
		t.Fatalf("installed ops = %+v, want fallback install op for rg", installed)
	}
	cached, err := a.DB().Get(ctx, "rg", "apt", "ripgrep")
	if err != nil {
		t.Fatalf("Get apt rg: %v", err)
	}
	if !cached.Installed || cached.InstalledWith != "gh" {
		t.Fatalf("cached = installed %v with %q, want fallback gh install", cached.Installed, cached.InstalledWith)
	}
}

func TestSync_DoesNotUseFallbackWhenProviderUnavailable(t *testing.T) {
	ctx := context.Background()
	apt := &stubProvider{name: "apt", available: false}
	fallbackExec := executor.NewMatchMock().WithFallback(executor.MockCall{})
	a, cfgPath := newImportApp(t, apt)
	a.SetFallbackExecutor(fallbackExec)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Providers: []config.ToolInstallSpec{
					{Provider: "apt", Package: "ripgrep"},
				},
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
					Status: config.FallbackStatusUnverified,
					Commands: config.FallbackCommands{
						Install: "install rg",
						Check:   "command -v rg",
					},
				},
			},
		},
		Groups: []*config.GroupConfig{{Tools: []config.ToolEntry{{Name: "rg"}}}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().UpsertPackageAvailability(ctx, database.PackageAvailability{
		Name:      "rg",
		Provider:  "apt",
		Package:   "ripgrep",
		Available: false,
		Reason:    "no apt candidate",
	}); err != nil {
		t.Fatalf("seed apt package availability: %v", err)
	}

	result, err := a.Sync(ctx, isync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if fallbackExec.CallCount() != 0 {
		t.Fatalf("fallback command count = %d, want no fallback when provider is unavailable", fallbackExec.CallCount())
	}
	if installed := result.Installed(); len(installed) != 0 {
		t.Fatalf("installed ops = %+v, want no fallback install", installed)
	}
	if failed := result.Failed(); len(failed) != 1 || failed[0].Tool.Name != "rg" {
		t.Fatalf("failed ops = %+v, want provider unavailable failure for rg", failed)
	}
}

func TestSync_UsesFallbackRecipeSavedFromGitHubSpec(t *testing.T) {
	ctx := context.Background()
	system := &lifecycleProvider{
		stubProvider: stubProvider{name: "system", available: true},
		resolvedName: "apt",
	}
	apt := &lifecycleProvider{stubProvider: stubProvider{name: "apt", available: true}}
	fallbackExec := executor.NewMatchMock(
		executor.MatchRule{Pattern: "sh -c editor install rg", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "sh -c editor check rg", Response: executor.MockCall{}},
	).WithFallback(executor.MockCall{Err: errors.New("unexpected fallback command")})
	a, cfgPath := newImportApp(t, system, apt)
	a.SetFallbackExecutor(fallbackExec)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Package:   "ripgrep",
			},
		},
		Groups: []*config.GroupConfig{{Tools: []config.ToolEntry{{Name: "rg"}}}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.SaveToolFallbackFromGitHubSpec(ctx, "rg", "BurntSushi/ripgrep", config.FallbackSpec{
		Status: config.FallbackStatusUnverified,
		Binary: "rg",
		Commands: config.FallbackCommands{
			Install: "editor install rg",
			Check:   "editor check rg",
		},
	}); err != nil {
		t.Fatalf("SaveToolFallbackFromGitHubSpec: %v", err)
	}
	if err := a.DB().UpsertPackageAvailability(ctx, database.PackageAvailability{
		Name:      "rg",
		Provider:  "apt",
		Package:   "ripgrep",
		Available: false,
		Reason:    "no candidate",
	}); err != nil {
		t.Fatalf("seed package availability: %v", err)
	}

	result, err := a.Sync(ctx, isync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	fallbackExec.AssertCalled(t, "sh -c editor install rg")
	fallbackExec.AssertCalled(t, "sh -c editor check rg")
	if installed := result.Installed(); len(installed) != 1 || installed[0].Tool.Name != "rg" {
		t.Fatalf("installed ops = %+v, want fallback install op for rg", installed)
	}
	got, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	fallback := got.Tools["rg"].Fallback
	if fallback == nil || fallback.Status != config.FallbackStatusVerified {
		t.Fatalf("fallback = %+v, want verified saved editor recipe", fallback)
	}
}

func TestSync_DryRunPlansFallbackWhenNativePackageUnavailable(t *testing.T) {
	ctx := context.Background()
	system := &lifecycleProvider{
		stubProvider: stubProvider{name: "system", available: true},
		resolvedName: "apt",
	}
	apt := &lifecycleProvider{stubProvider: stubProvider{name: "apt", available: true}}
	fallbackExec := executor.NewMatchMock().WithFallback(executor.MockCall{})
	a, cfgPath := newImportApp(t, system, apt)
	a.SetFallbackExecutor(fallbackExec)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
					Status: config.FallbackStatusUnverified,
					Commands: config.FallbackCommands{
						Install: "install rg",
						Check:   "command -v rg",
					},
				},
			},
		},
		Groups: []*config.GroupConfig{{Tools: []config.ToolEntry{{Name: "rg"}}}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().UpsertPackageAvailability(ctx, database.PackageAvailability{
		Name:      "rg",
		Provider:  "apt",
		Package:   "rg",
		Available: false,
		Reason:    "no candidate",
	}); err != nil {
		t.Fatalf("seed package availability: %v", err)
	}

	result, err := a.Sync(ctx, isync.SyncOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Sync dry-run: %v", err)
	}

	if fallbackExec.CallCount() != 0 {
		t.Fatalf("fallback command count = %d, want none for dry-run", fallbackExec.CallCount())
	}
	if installed := result.Installed(); len(installed) != 1 || installed[0].Tool.Name != "rg" {
		t.Fatalf("dry-run installed ops = %+v, want planned fallback install for rg", installed)
	}
	if _, err := a.DB().Get(ctx, "rg", "apt", "rg"); err == nil {
		t.Fatal("dry-run wrote cache row, want no fallback install side effect")
	}
}

func TestSync_RetryFailedRerunsFailedFallback(t *testing.T) {
	ctx := context.Background()
	system := &lifecycleProvider{
		stubProvider: stubProvider{name: "system", available: true},
		resolvedName: "apt",
	}
	apt := &lifecycleProvider{stubProvider: stubProvider{name: "apt", available: true}}
	fallbackExec := executor.NewMatchMock(
		executor.MatchRule{Pattern: "sh -c install rg", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "sh -c command -v rg", Response: executor.MockCall{}},
	).WithFallback(executor.MockCall{Err: errors.New("unexpected fallback command")})
	a, cfgPath := newImportApp(t, system, apt)
	a.SetFallbackExecutor(fallbackExec)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
					Status: config.FallbackStatusFailed,
					Commands: config.FallbackCommands{
						Install: "install rg",
						Check:   "command -v rg",
					},
				},
			},
		},
		Groups: []*config.GroupConfig{{Tools: []config.ToolEntry{{Name: "rg"}}}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().UpsertPackageAvailability(ctx, database.PackageAvailability{
		Name:      "rg",
		Provider:  "apt",
		Package:   "rg",
		Available: false,
		Reason:    "no candidate",
	}); err != nil {
		t.Fatalf("seed package availability: %v", err)
	}
	if err := a.DB().MarkFailed(ctx, "rg", "apt", "rg", "previous fallback failure"); err != nil {
		t.Fatalf("seed failed row: %v", err)
	}

	result, err := a.Sync(ctx, isync.SyncOptions{RetryFailed: true})
	if err != nil {
		t.Fatalf("Sync retry-failed: %v", err)
	}

	fallbackExec.AssertCalled(t, "sh -c install rg")
	if installed := result.Installed(); len(installed) != 1 || installed[0].Tool.Name != "rg" {
		t.Fatalf("retry installed ops = %+v, want fallback retry install for rg", installed)
	}
	cached, err := a.DB().Get(ctx, "rg", "apt", "rg")
	if err != nil {
		t.Fatalf("Get rg: %v", err)
	}
	if cached.FailedAt != nil || cached.FailureCount != 0 || !cached.Installed || cached.InstalledWith != "gh" {
		t.Fatalf("cache failure/install state = failed_at:%v count:%d installed:%v with:%q, want cleared installed gh", cached.FailedAt, cached.FailureCount, cached.Installed, cached.InstalledWith)
	}
}

func TestInstall_DoesNotRetryFailedFallbackWhenNativePackageUnavailable(t *testing.T) {
	ctx := context.Background()
	system := &lifecycleProvider{
		stubProvider: stubProvider{name: "system", available: true},
		resolvedName: "apt",
	}
	apt := &lifecycleProvider{stubProvider: stubProvider{name: "apt", available: true}}
	fallbackExec := executor.NewMatchMock().WithFallback(executor.MockCall{})
	a, cfgPath := newImportApp(t, system, apt)
	a.SetFallbackExecutor(fallbackExec)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
					Status: config.FallbackStatusFailed,
					Commands: config.FallbackCommands{
						Install: "install rg",
						Check:   "command -v rg",
					},
				},
			},
		},
		Groups: []*config.GroupConfig{{Tools: []config.ToolEntry{{Name: "rg"}}}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().UpsertPackageAvailability(ctx, database.PackageAvailability{
		Name:      "rg",
		Provider:  "apt",
		Package:   "rg",
		Available: false,
		Reason:    "no candidate",
	}); err != nil {
		t.Fatalf("seed package availability: %v", err)
	}

	err := a.Install(ctx, "rg", "apt")
	if err == nil || !strings.Contains(err.Error(), "native package rg is unavailable from apt") {
		t.Fatalf("Install err = %v, want native unavailable fallback skip", err)
	}
	if fallbackExec.CallCount() != 0 {
		t.Fatalf("fallback command count = %d, want no retry", fallbackExec.CallCount())
	}
}

func TestUninstall_UsesFallbackUninstallForGitHubInstall(t *testing.T) {
	ctx := context.Background()
	fallbackExec := executor.NewMatchMock(
		executor.MatchRule{Pattern: "sh -c uninstall rg", Response: executor.MockCall{}},
	).WithFallback(executor.MockCall{Err: errors.New("unexpected fallback command")})
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true}, &stubProvider{name: "apt", available: true})
	a.SetFallbackExecutor(fallbackExec)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
					Status: config.FallbackStatusVerified,
					Commands: config.FallbackCommands{
						Check:     "command -v rg",
						Uninstall: "uninstall rg",
					},
				},
			},
		},
		Groups: []*config.GroupConfig{{Tools: []config.ToolEntry{{Name: "rg"}}}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "rg",
		Provider:      "apt",
		Package:       "rg",
		Installed:     true,
		InstalledWith: "gh",
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	if err := a.Uninstall(ctx, "rg", "apt"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	fallbackExec.AssertCalled(t, "sh -c uninstall rg")
	if _, err := a.DB().Get(ctx, "rg", "apt", "rg"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cache row err = %v, want sql.ErrNoRows", err)
	}
	got, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, ok := got.Tools["rg"]; ok {
		t.Fatal("logical tool spec survived fallback uninstall")
	}
}

func TestUninstall_FallbackWithoutScriptLeavesConfig(t *testing.T) {
	ctx := context.Background()
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true}, &stubProvider{name: "apt", available: true})
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback: &config.FallbackSpec{
					Source:   config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
					Status:   config.FallbackStatusVerified,
					Commands: config.FallbackCommands{Check: "command -v rg"},
				},
			},
		},
		Groups: []*config.GroupConfig{{Tools: []config.ToolEntry{{Name: "rg"}}}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "rg",
		Provider:      "apt",
		Package:       "rg",
		Installed:     true,
		InstalledWith: "gh",
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	err := a.Uninstall(ctx, "rg", "apt")
	if err == nil || !strings.Contains(err.Error(), "fallback uninstall is not available") {
		t.Fatalf("Uninstall err = %v, want unavailable fallback uninstall", err)
	}
	if _, err := a.DB().Get(ctx, "rg", "apt", "rg"); err != nil {
		t.Fatalf("cache row after failed uninstall: %v", err)
	}
	got, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, ok := got.Tools["rg"]; !ok {
		t.Fatal("logical tool spec was removed despite unavailable fallback uninstall")
	}
}

func TestUpgrade_UsesFallbackForGitHubInstall(t *testing.T) {
	ctx := context.Background()
	fallbackExec := executor.NewMatchMock(
		executor.MatchRule{Pattern: "sh -c upgrade rg", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "sh -c command -v rg", Response: executor.MockCall{}},
	).WithFallback(executor.MockCall{Err: errors.New("unexpected fallback command")})
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true}, &stubProvider{name: "apt", available: true})
	a.SetFallbackExecutor(fallbackExec)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
					Status: config.FallbackStatusVerified,
					Commands: config.FallbackCommands{
						Upgrade: "upgrade rg",
						Check:   "command -v rg",
					},
				},
			},
		},
		Groups: []*config.GroupConfig{{Tools: []config.ToolEntry{{Name: "rg"}}}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "rg",
		Provider:      "apt",
		Package:       "rg",
		Installed:     true,
		InstalledWith: "gh",
		Outdated:      true,
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	if err := a.Upgrade(ctx, "rg", "apt"); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	fallbackExec.AssertCalled(t, "sh -c upgrade rg")
	fallbackExec.AssertCalled(t, "sh -c command -v rg")
	cached, err := a.DB().Get(ctx, "rg", "apt", "rg")
	if err != nil {
		t.Fatalf("Get rg: %v", err)
	}
	if !cached.Installed || cached.InstalledWith != "gh" || cached.Outdated {
		t.Fatalf("cached = installed:%v with:%q outdated:%v, want installed gh not outdated", cached.Installed, cached.InstalledWith, cached.Outdated)
	}
}

func TestUpgradeToolFallback_GitHubOutdatedRefreshesRecipeBeforeUpgrade(t *testing.T) {
	ctx := context.Background()
	latestAsset := currentPlatformGitHubCLIAsset(t)
	calls := int32(0)
	fallbackExec := executor.NewMatchMock().WithFallback(executor.MockCall{})
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true}, &stubProvider{name: "apt", available: true})
	a.SetFallbackExecutor(fallbackExec)
	a.SetGitHubFallbackAPIForTest("https://api.github.test", githubFallbackLatestReleaseClient(t, &calls, func() io.ReadCloser {
		body, err := os.Open("testdata/github_cli_latest_release.json")
		if err != nil {
			t.Fatalf("open GitHub fixture: %v", err)
		}
		return body
	}))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"gh": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback:  oldGitHubCLIFallbackSpec(),
			},
		},
		Groups: []*config.GroupConfig{{Tools: groupTools("gh")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	seedGitHubFallbackUpgradeCacheRow(t, a.DB(), true, "v2.93.0")

	if err := a.UpgradeToolFallback(ctx, "gh"); err != nil {
		t.Fatalf("UpgradeToolFallback: %v", err)
	}

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("GitHub latest release calls = %d, want 1", got)
	}
	assertFallbackCommandContains(t, fallbackExec, latestAsset.downloadURL)
	assertFallbackCommandNotContains(t, fallbackExec, "https://github.com/cli/cli/releases/download/v2.92.0/gh_2.92.0_old.zip")
	got, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	fallback := got.Tools["gh"].Fallback
	if fallback == nil {
		t.Fatal("fallback missing")
	}
	if fallback.Status != config.FallbackStatusVerified {
		t.Fatalf("fallback status = %q, want verified", fallback.Status)
	}
	if fallback.Recipe.TagName != "v2.93.0" {
		t.Fatalf("fallback tag = %q, want v2.93.0", fallback.Recipe.TagName)
	}
	if fallback.Recipe.PublishedAt != "2026-05-27T17:47:41Z" {
		t.Fatalf("fallback published_at = %q, want latest fixture timestamp", fallback.Recipe.PublishedAt)
	}
	if fallback.Recipe.AssetDownloadURL != latestAsset.downloadURL {
		t.Fatalf("fallback asset URL = %q, want %q", fallback.Recipe.AssetDownloadURL, latestAsset.downloadURL)
	}
	assertGitHubFallbackOutdated(t, a.DB(), false, "")
}

func TestUpgradeToolFallback_GitHubRefreshFailureKeepsOldRecipeAndOutdatedRow(t *testing.T) {
	ctx := context.Background()
	latestAsset := currentPlatformGitHubCLIAsset(t)
	calls := int32(0)
	fallbackExec := executor.NewMatchMock(
		executor.MatchRule{Pattern: "sh -c mkdir -p", Response: executor.MockCall{Err: errors.New("download failed"), Stderr: "curl failed"}},
	).WithFallback(executor.MockCall{})
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true}, &stubProvider{name: "apt", available: true})
	a.SetFallbackExecutor(fallbackExec)
	a.SetGitHubFallbackAPIForTest("https://api.github.test", githubFallbackLatestReleaseClient(t, &calls, func() io.ReadCloser {
		body, err := os.Open("testdata/github_cli_latest_release.json")
		if err != nil {
			t.Fatalf("open GitHub fixture: %v", err)
		}
		return body
	}))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"gh": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback:  oldGitHubCLIFallbackSpec(),
			},
		},
		Groups: []*config.GroupConfig{{Tools: groupTools("gh")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	seedGitHubFallbackUpgradeCacheRow(t, a.DB(), true, "v2.93.0")

	err := a.UpgradeToolFallback(ctx, "gh")
	if err == nil || !strings.Contains(err.Error(), "download failed") {
		t.Fatalf("UpgradeToolFallback err = %v, want upgrade command failure", err)
	}

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("GitHub latest release calls = %d, want 1", got)
	}
	assertFallbackCommandContains(t, fallbackExec, latestAsset.downloadURL)
	got, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	fallback := got.Tools["gh"].Fallback
	if fallback == nil {
		t.Fatal("fallback missing")
	}
	if fallback.Status != config.FallbackStatusFailed {
		t.Fatalf("fallback status = %q, want failed", fallback.Status)
	}
	if fallback.Recipe.TagName != "v2.92.0" {
		t.Fatalf("fallback tag = %q, want preserved old tag", fallback.Recipe.TagName)
	}
	if fallback.Recipe.PublishedAt != "2026-05-01T00:00:00Z" {
		t.Fatalf("fallback published_at = %q, want preserved old timestamp", fallback.Recipe.PublishedAt)
	}
	if strings.Contains(fallback.Commands.Upgrade, latestAsset.downloadURL) {
		t.Fatalf("fallback upgrade command = %q, want old command preserved after failure", fallback.Commands.Upgrade)
	}
	assertGitHubFallbackOutdated(t, a.DB(), true, "v2.93.0")
}

func TestUpgradeToolFallback_GitHubRefreshCheckFailureKeepsOldRecipeAndOutdatedRow(t *testing.T) {
	ctx := context.Background()
	latestAsset := currentPlatformGitHubCLIAsset(t)
	calls := int32(0)
	fallbackExec := executor.NewMatchMock(
		executor.MatchRule{Pattern: "sh -c mkdir -p", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "sh -c test -x", Response: executor.MockCall{Err: errors.New("missing refreshed binary")}},
	).WithFallback(executor.MockCall{})
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true}, &stubProvider{name: "apt", available: true})
	a.SetFallbackExecutor(fallbackExec)
	a.SetGitHubFallbackAPIForTest("https://api.github.test", githubFallbackLatestReleaseClient(t, &calls, func() io.ReadCloser {
		body, err := os.Open("testdata/github_cli_latest_release.json")
		if err != nil {
			t.Fatalf("open GitHub fixture: %v", err)
		}
		return body
	}))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"gh": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback:  oldGitHubCLIFallbackSpec(),
			},
		},
		Groups: []*config.GroupConfig{{Tools: groupTools("gh")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	seedGitHubFallbackUpgradeCacheRow(t, a.DB(), true, "v2.93.0")

	err := a.UpgradeToolFallback(ctx, "gh")
	if err == nil || !strings.Contains(err.Error(), "upgrade verification failed") {
		t.Fatalf("UpgradeToolFallback err = %v, want check failure", err)
	}

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("GitHub latest release calls = %d, want 1", got)
	}
	assertFallbackCommandContains(t, fallbackExec, latestAsset.downloadURL)
	got, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	fallback := got.Tools["gh"].Fallback
	if fallback == nil {
		t.Fatal("fallback missing")
	}
	if fallback.Status != config.FallbackStatusFailed {
		t.Fatalf("fallback status = %q, want failed", fallback.Status)
	}
	if fallback.Recipe.TagName != "v2.92.0" {
		t.Fatalf("fallback tag = %q, want preserved old tag", fallback.Recipe.TagName)
	}
	if strings.Contains(fallback.Commands.Check, "test -x") {
		t.Fatalf("fallback check command = %q, want old command preserved after check failure", fallback.Commands.Check)
	}
	assertGitHubFallbackOutdated(t, a.DB(), true, "v2.93.0")
}

func TestUpgradeToolFallback_GitHubResolverFailureKeepsOldRecipeAndOutdatedRow(t *testing.T) {
	ctx := context.Background()
	calls := int32(0)
	fallbackExec := executor.NewMatchMock().WithFallback(executor.MockCall{Err: errors.New("unexpected fallback command")})
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true}, &stubProvider{name: "apt", available: true})
	a.SetFallbackExecutor(fallbackExec)
	a.SetGitHubFallbackAPIForTest("https://api.github.test", githubFallbackLatestReleaseClient(t, &calls, func() io.ReadCloser {
		return githubFallbackReleaseBody(
			"v2.93.0",
			"2026-05-27T17:47:41Z",
			"gh_2.93.0_plan9_mips.tar.gz",
			"https://github.com/cli/cli/releases/download/v2.93.0/gh_2.93.0_plan9_mips.tar.gz",
		)
	}))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"gh": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback:  oldGitHubCLIFallbackSpec(),
			},
		},
		Groups: []*config.GroupConfig{{Tools: groupTools("gh")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	seedGitHubFallbackUpgradeCacheRow(t, a.DB(), true, "v2.93.0")

	err := a.UpgradeToolFallback(ctx, "gh")
	if err == nil {
		t.Fatal("UpgradeToolFallback err = nil, want resolver failure")
	}

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("GitHub latest release calls = %d, want 1", got)
	}
	if got := fallbackExec.CallCount(); got != 0 {
		t.Fatalf("fallback command count = %d, want zero when resolver fails", got)
	}
	got, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	fallback := got.Tools["gh"].Fallback
	if fallback == nil {
		t.Fatal("fallback missing")
	}
	if fallback.Status != config.FallbackStatusFailed {
		t.Fatalf("fallback status = %q, want failed", fallback.Status)
	}
	if fallback.Recipe.TagName != "v2.92.0" {
		t.Fatalf("fallback tag = %q, want preserved old tag", fallback.Recipe.TagName)
	}
	if fallback.Recipe.PublishedAt != "2026-05-01T00:00:00Z" {
		t.Fatalf("fallback published_at = %q, want preserved old timestamp", fallback.Recipe.PublishedAt)
	}
	if fallback.Recipe.AssetDownloadURL != "https://github.com/cli/cli/releases/download/v2.92.0/gh_2.92.0_old.zip" {
		t.Fatalf("fallback asset URL = %q, want old recipe URL preserved", fallback.Recipe.AssetDownloadURL)
	}
	if fallback.Commands.Upgrade != "curl -fsSL https://github.com/cli/cli/releases/download/v2.92.0/gh_2.92.0_old.zip -o /tmp/gh-old.zip" {
		t.Fatalf("fallback upgrade command = %q, want old command preserved", fallback.Commands.Upgrade)
	}
	if fallback.Commands.Check != "command -v gh" {
		t.Fatalf("fallback check command = %q, want old command preserved", fallback.Commands.Check)
	}
	assertGitHubFallbackOutdated(t, a.DB(), true, "v2.93.0")
}

func TestUpgradeToolFallback_GitHubNotOutdatedUsesSavedRecipeWithoutReleaseLookup(t *testing.T) {
	ctx := context.Background()
	fallbackExec := executor.NewMatchMock(
		executor.MatchRule{Pattern: "sh -c curl -fsSL https://github.com/cli/cli/releases/download/v2.92.0/gh_2.92.0_old.zip", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "sh -c command -v gh", Response: executor.MockCall{}},
	).WithFallback(executor.MockCall{Err: errors.New("unexpected fallback command")})
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true}, &stubProvider{name: "apt", available: true})
	a.SetFallbackExecutor(fallbackExec)
	a.SetGitHubFallbackAPIForTest("https://api.github.test", githubFallbackLatestReleaseClient(t, nil, func() io.ReadCloser {
		t.Fatal("GitHub latest release endpoint should not be called when the row is not marked outdated")
		return http.NoBody
	}))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"gh": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback:  oldGitHubCLIFallbackSpec(),
			},
		},
		Groups: []*config.GroupConfig{{Tools: groupTools("gh")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	seedGitHubFallbackUpgradeCacheRow(t, a.DB(), false, "")

	if err := a.UpgradeToolFallback(ctx, "gh"); err != nil {
		t.Fatalf("UpgradeToolFallback: %v", err)
	}

	fallbackExec.AssertCalled(t, "sh -c curl -fsSL https://github.com/cli/cli/releases/download/v2.92.0/gh_2.92.0_old.zip")
	got, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got.Tools["gh"].Fallback.Recipe.TagName != "v2.92.0" {
		t.Fatalf("fallback tag = %q, want saved old recipe", got.Tools["gh"].Fallback.Recipe.TagName)
	}
	assertGitHubFallbackOutdated(t, a.DB(), false, "")
}

func TestUninstallToolFallback_ReportsUnavailableWithoutCommand(t *testing.T) {
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true}, &stubProvider{name: "apt", available: true})
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback: &config.FallbackSpec{
					Source:   config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
					Status:   config.FallbackStatusUnverified,
					Commands: config.FallbackCommands{Check: "command -v rg"},
				},
			},
		},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	err := a.UninstallToolFallback(context.Background(), "rg")
	if err == nil || !strings.Contains(err.Error(), "fallback uninstall is not available") {
		t.Fatalf("UninstallToolFallback err = %v, want unavailable", err)
	}
}

func TestSaveToolFallbackFromGitHub_ResolverFailureDoesNotSaveFallback(t *testing.T) {
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true}, &stubProvider{name: "apt", available: true})
	a.SetGitHubFallbackAPIForTest("https://api.github.test", githubNotFoundClient())
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("rg", "system")),
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	err := a.SaveToolFallbackFromGitHub(context.Background(), "rg", "BurntSushi/ripgrep")
	if err == nil {
		t.Fatal("SaveToolFallbackFromGitHub err = nil, want resolver failure")
	}
	if !strings.Contains(err.Error(), "github latest release not found for BurntSushi/ripgrep") {
		t.Fatalf("SaveToolFallbackFromGitHub err = %v, want not found error", err)
	}
	if strings.Contains(err.Error(), "published_at") {
		t.Fatalf("SaveToolFallbackFromGitHub err = %v, want no misleading published_at error", err)
	}
	got, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if fallback := got.Tools["rg"].Fallback; fallback != nil {
		t.Fatalf("fallback = %+v, want no fallback saved after resolver failure", fallback)
	}
}

func TestSaveToolFallbackFromGitHub_NormalizesSSHRepoURL(t *testing.T) {
	currentPlatformGitHubCLIAsset(t)
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/repos/cli/cli/releases/latest" {
			t.Fatalf("unexpected GitHub API path %q", req.URL.Path)
		}
		body, err := os.Open("testdata/github_cli_latest_release.json")
		if err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       body,
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true}, &stubProvider{name: "apt", available: true})
	a.SetGitHubFallbackAPIForTest("https://api.github.test", client)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("gh", "system")),
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.SaveToolFallbackFromGitHub(context.Background(), "gh", "git@github.com:cli/cli.git"); err != nil {
		t.Fatalf("SaveToolFallbackFromGitHub: %v", err)
	}
	got, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	fallback := got.Tools["gh"].Fallback
	if fallback == nil {
		t.Fatal("fallback missing")
	}
	if fallback.Source.Owner != "cli" || fallback.Source.Repo != "cli" || fallback.Source.URL != "https://github.com/cli/cli" {
		t.Fatalf("fallback source = %+v, want normalized cli/cli", fallback.Source)
	}
}

func TestSaveToolFallbackFromGitHub_RejectsInvalidRepo(t *testing.T) {
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true}, &stubProvider{name: "apt", available: true})
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("rg", "system")),
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	err := a.SaveToolFallbackFromGitHub(context.Background(), "rg", "not-a-repo")
	if err == nil || !strings.Contains(err.Error(), "github repo must be owner/repo") {
		t.Fatalf("SaveToolFallbackFromGitHub err = %v, want repo validation", err)
	}
}

func oldGitHubCLIFallbackSpec() *config.FallbackSpec {
	oldURL := "https://github.com/cli/cli/releases/download/v2.92.0/gh_2.92.0_old.zip"
	return &config.FallbackSpec{
		Source: config.FallbackSource{
			Type:  config.FallbackSourceGitHub,
			Owner: "cli",
			Repo:  "cli",
			URL:   "https://github.com/cli/cli",
		},
		Status: config.FallbackStatusVerified,
		Binary: "gh",
		Recipe: config.FallbackRecipe{
			Type:             config.FallbackRecipeGitHubReleaseAsset,
			ReleaseID:        "320000000",
			TagName:          "v2.92.0",
			PublishedAt:      "2026-05-01T00:00:00Z",
			AssetID:          "420000000",
			AssetName:        "gh_2.92.0_old.zip",
			AssetPattern:     "gh_2.92.0_old.zip",
			AssetDownloadURL: oldURL,
		},
		Commands: config.FallbackCommands{
			Upgrade: "curl -fsSL " + oldURL + " -o /tmp/gh-old.zip",
			Check:   "command -v gh",
		},
	}
}

func seedGitHubFallbackUpgradeCacheRow(t *testing.T, db *database.DB, outdated bool, latestVersion string) {
	t.Helper()
	ctx := context.Background()
	if err := db.Upsert(ctx, &database.ToolCache{
		Name:          "gh",
		Provider:      "apt",
		Package:       "gh",
		Installed:     true,
		InstalledWith: "gh",
		Outdated:      outdated,
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	if outdated || latestVersion != "" {
		if err := db.UpdateOutdated(ctx, "gh", "apt", "gh", outdated, latestVersion); err != nil {
			t.Fatalf("seed outdated state: %v", err)
		}
	}
}

func assertFallbackCommandContains(t *testing.T, exec *executor.MatchMockExecutor, value string) {
	t.Helper()
	for _, call := range exec.CallsMatching("sh -c ") {
		if strings.Contains(call.Name+" "+strings.Join(call.Args, " "), value) {
			return
		}
	}
	t.Fatalf("fallback commands did not contain %q; calls = %+v", value, exec.Calls)
}

func assertFallbackCommandNotContains(t *testing.T, exec *executor.MatchMockExecutor, value string) {
	t.Helper()
	for _, call := range exec.CallsMatching("sh -c ") {
		if strings.Contains(call.Name+" "+strings.Join(call.Args, " "), value) {
			t.Fatalf("fallback command contained %q; calls = %+v", value, exec.Calls)
		}
	}
}

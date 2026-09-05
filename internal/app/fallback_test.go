package app_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
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

type fallbackTestExecutorFunc func(context.Context, string, ...string) (string, string, error)

func (f fallbackTestExecutorFunc) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	return f(ctx, name, args...)
}

func TestInstallToolFallback_RunsInstallCheckAndUpdatesState(t *testing.T) {
	t.Parallel()
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
						Install: `mkdir -p {{bin_dir}} {{cache_dir}} && printf '#!/bin/sh\nexit 0\n' > {{bin_dir}}/{{binary}} && chmod +x {{bin_dir}}/{{binary}}`,
						Check:   `test -x {{bin_dir}}/{{binary}} && test -d {{cache_dir}} && test {{repo}} = 'BurntSushi/ripgrep'`,
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
	t.Parallel()
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
	t.Parallel()
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

func TestUninstallToolFallback_RunsUninstallAndDeletesCache(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "sh -c uninstall rg", Response: executor.MockCall{}},
	).WithFallback(executor.MockCall{Err: errors.New("unexpected fallback command")})
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true}, &stubProvider{name: "apt", available: true})
	a.SetFallbackExecutor(mock)
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

	if err := a.UninstallToolFallback(ctx, "rg"); err != nil {
		t.Fatalf("UninstallToolFallback: %v", err)
	}

	mock.AssertCalled(t, "sh -c uninstall rg")
	if _, err := a.DB().Get(ctx, "rg", "apt", "rg"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cache row err = %v, want sql.ErrNoRows", err)
	}
	got, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got.Tools["rg"].Fallback == nil {
		t.Fatal("fallback config was removed by direct fallback uninstall")
	}
}

func TestInstall_UsesFallbackWhenNativePackageUnavailable(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestSync_FailedFallbackInstallRecordsOneAttempt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	system := &lifecycleProvider{
		stubProvider: stubProvider{name: "system", available: true},
		resolvedName: "apt",
	}
	apt := &lifecycleProvider{stubProvider: stubProvider{name: "apt", available: true}}
	fallbackExec := executor.NewMatchMock(
		executor.MatchRule{Pattern: "sh -c install rg", Response: executor.MockCall{Err: errors.New("install failed")}},
	).WithFallback(executor.MockCall{Err: errors.New("unexpected fallback command")})
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
		Groups: []*config.GroupConfig{{Tools: groupTools("rg")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().UpsertPackageAvailability(ctx, database.PackageAvailability{
		Name: "rg", Provider: "apt", Package: "rg", Available: false, Reason: "no candidate",
	}); err != nil {
		t.Fatalf("seed package availability: %v", err)
	}

	result, err := a.Sync(ctx, isync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if failed := result.Failed(); len(failed) != 1 || !strings.Contains(failed[0].Err.Error(), "install failed") {
		t.Fatalf("failed ops = %+v, want one install failure", failed)
	}
	cached, err := a.DB().Get(ctx, "rg", "apt", "rg")
	if err != nil {
		t.Fatalf("Get failed fallback row: %v", err)
	}
	if cached.FailureCount != 1 {
		t.Fatalf("FailureCount = %d, want one record for one failed attempt", cached.FailureCount)
	}
	got, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got.Tools["rg"].Fallback.Status != config.FallbackStatusFailed {
		t.Fatalf("fallback status = %q, want failed", got.Tools["rg"].Fallback.Status)
	}
}

func TestSync_UsesNativeCandidateBeforeFallbackWhenAnyProviderAvailable(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestSync_SkipsUnsupportedFallbackWhenNativePackageUnavailable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	apt := &stubProvider{name: "apt", available: true}
	fallbackExec := executor.NewMatchMock().WithFallback(executor.MockCall{Err: errors.New("unexpected fallback command")})
	a, cfgPath := newImportApp(t, apt)
	a.SetFallbackExecutor(fallbackExec)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Providers: []config.ToolInstallSpec{{Provider: "apt", Package: "ripgrep"}},
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
					Status: config.FallbackStatusUnsupported,
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
		t.Fatalf("fallback command count = %d, want no unsupported-platform execution", fallbackExec.CallCount())
	}
	failed := result.Failed()
	if len(failed) != 1 || !strings.Contains(failed[0].Err.Error(), "unsupported") {
		t.Fatalf("failed ops = %+v, want unsupported fallback failure", failed)
	}
}

func TestSync_UsesFallbackWhenProviderUnavailableAndNoNativeAlternative(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	apt := &stubProvider{name: "apt", available: false}
	fallbackExec := executor.NewMatchMock(
		executor.MatchRule{Pattern: "sh -c install rg", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "sh -c command -v rg", Response: executor.MockCall{}},
	).WithFallback(executor.MockCall{Err: errors.New("unexpected fallback command")})
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

	result, err := a.Sync(ctx, isync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if fallbackExec.CallCount() == 0 {
		t.Errorf("fallback was not attempted; ops = %+v", result.Ops)
	}
}

func TestSync_UsesFallbackRecipeSavedFromGitHubSpec(t *testing.T) {
	t.Parallel()
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
	if fallback.Source.Owner != "BurntSushi" || fallback.Source.Repo != "ripgrep" || fallback.Source.URL != "https://github.com/BurntSushi/ripgrep" {
		t.Fatalf("fallback source = %+v, want materialized GitHub source", fallback.Source)
	}
}

func TestSync_DryRunPlansFallbackWhenNativePackageUnavailable(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestSync_RecoversFailedFallbackThroughNativeWhenPackageAvailable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	apt := &stubProvider{name: "apt", available: true}
	fallbackExec := executor.NewMatchMock().WithFallback(executor.MockCall{Err: errors.New("unexpected fallback command")})
	a, cfgPath := newImportApp(t, apt)
	a.SetFallbackExecutor(fallbackExec)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Providers: []config.ToolInstallSpec{{Provider: "apt", Package: "ripgrep"}},
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
		Package:   "ripgrep",
		Available: true,
	}); err != nil {
		t.Fatalf("seed package availability: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "rg",
		Provider:      "apt",
		Package:       "ripgrep",
		Installed:     true,
		InstalledWith: "gh",
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("seed fallback row: %v", err)
	}
	if err := a.DB().MarkFailed(ctx, "rg", "apt", "ripgrep", "previous fallback failure"); err != nil {
		t.Fatalf("seed failed row: %v", err)
	}

	result, err := a.Sync(ctx, isync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if fallbackExec.CallCount() != 0 {
		t.Fatalf("fallback command count = %d, want native recovery without fallback", fallbackExec.CallCount())
	}
	if len(apt.installed) != 1 || apt.installed[0].EffectivePackage() != "ripgrep" {
		t.Fatalf("apt installed = %+v, want native ripgrep install", apt.installed)
	}
	if installed := result.Installed(); len(installed) != 1 || installed[0].Tool.Name != "rg" {
		t.Fatalf("installed ops = %+v, want native install for rg", installed)
	}
	cached, err := a.DB().Get(ctx, "rg", "apt", "ripgrep")
	if err != nil {
		t.Fatalf("Get rg: %v", err)
	}
	if cached.FailedAt != nil || cached.FailureCount != 0 || !cached.Installed || cached.InstalledWith != "apt" {
		t.Fatalf("cache failure/install state = failed_at:%v count:%d installed:%v with:%q, want cleared installed apt", cached.FailedAt, cached.FailureCount, cached.Installed, cached.InstalledWith)
	}
	got, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got.Tools["rg"].Fallback == nil || got.Tools["rg"].Fallback.Status != config.FallbackStatusFailed {
		t.Fatalf("fallback config = %+v, want failed fallback kept for editing/history", got.Tools["rg"].Fallback)
	}
}

func TestInstall_DoesNotRetryFailedFallbackWhenNativePackageUnavailable(t *testing.T) {
	t.Parallel()
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

func TestInstall_DoesNotUseFallbackForMixedRouteSkips(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	apt := &lifecycleProvider{stubProvider: stubProvider{name: "apt", available: true}}
	brew := &lifecycleProvider{stubProvider: stubProvider{name: "brew", available: false}}
	fallbackExec := executor.NewMatchMock().WithFallback(executor.MockCall{})
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
		t.Fatalf("seed package availability: %v", err)
	}

	err := a.Install(ctx, "rg", "apt")
	if err == nil {
		t.Fatal("Install err = nil, want unavailable route diagnostic")
	}
	for _, want := range []string{
		"native install candidates unavailable for rg",
		"apt/ripgrep unavailable: no apt candidate",
		"brew/ripgrep provider unavailable",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Install err = %v, want %q", err, want)
		}
	}
	if fallbackExec.CallCount() != 0 {
		t.Fatalf("fallback command count = %d, want no fallback install", fallbackExec.CallCount())
	}
	if len(apt.stubProvider.installed) != 0 || len(brew.stubProvider.installed) != 0 {
		t.Fatalf("installed = apt:%+v brew:%+v, want no native install", apt.stubProvider.installed, brew.stubProvider.installed)
	}
}

func TestUninstall_UsesFallbackUninstallForGitHubInstall(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	ctx := context.Background()
	latestAsset := currentPlatformGitHubCLIAsset(t)
	binaryContent := []byte("new gh binary")
	assetContent := currentPlatformGitHubCLIAssetContent(t, latestAsset, "gh", binaryContent)
	calls := int32(0)
	fallbackExec := executor.NewMatchMock().WithFallback(executor.MockCall{})
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true}, &stubProvider{name: "apt", available: true})
	a.CacheDir = t.TempDir()
	a.SetFallbackExecutor(fallbackExec)
	a.SetGitHubFallbackAPIForTest("https://api.github.test", githubFallbackUpgradeClient(t, &calls, func() io.ReadCloser {
		body, err := os.Open("testdata/github_cli_latest_release.json")
		if err != nil {
			t.Fatalf("open GitHub fixture: %v", err)
		}
		return body
	}, assetContent, http.StatusOK))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"gh": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback:  nativeOldGitHubCLIFallbackSpec(),
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
	assertFallbackCommandNotContains(t, fallbackExec, "https://github.com/cli/cli/releases/download/v2.92.0/gh_2.92.0_old.zip")
	assertFallbackCommandNotContains(t, fallbackExec, latestAsset.downloadURL)
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
	assertBinaryInstalled(t, a, *fallback, "gh", binaryContent)
	assertGitHubFallbackOutdated(t, a.DB(), false, "")
}

func TestUpgradeToolFallback_GitHubRefreshPreservesCustomCommands(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	calls := int32(0)
	fallbackExec := executor.NewMatchMock(
		executor.MatchRule{Pattern: "sh -c custom upgrade", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "sh -c custom check", Response: executor.MockCall{}},
	).WithFallback(executor.MockCall{Err: errors.New("unexpected fallback command")})
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true}, &stubProvider{name: "apt", available: true})
	a.SetFallbackExecutor(fallbackExec)
	a.SetGitHubFallbackAPIForTest("https://api.github.test", githubFallbackLatestReleaseClient(t, &calls, func() io.ReadCloser {
		body, err := os.Open("testdata/github_cli_latest_release.json")
		if err != nil {
			t.Fatalf("open GitHub fixture: %v", err)
		}
		return body
	}))
	fallback := oldGitHubCLIFallbackSpec()
	fallback.Recipe.ChecksumAssetPattern = "checksums.txt"
	want := config.FallbackCommands{
		Install:   "custom install",
		Check:     "custom check",
		Uninstall: "custom uninstall",
		Upgrade:   "custom upgrade",
		Version:   "custom version",
	}
	fallback.Commands = want
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"gh": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback:  fallback,
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
	fallbackExec.AssertCalled(t, "sh -c custom upgrade")
	fallbackExec.AssertCalled(t, "sh -c custom check")
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("GitHub latest release calls = %d, want 1", got)
	}

	got, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if commands := got.Tools["gh"].Fallback.Commands; commands != want {
		t.Fatalf("commands after refresh = %+v, want custom commands %+v", commands, want)
	}
	if pattern := got.Tools["gh"].Fallback.Recipe.ChecksumAssetPattern; pattern != "checksums.txt" {
		t.Fatalf("checksum asset pattern after refresh = %q, want checksums.txt", pattern)
	}
}

func TestUpgradeToolFallback_CustomCommandFailureMarksInstalledStateUnknown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fallbackExec := executor.NewMatchMock(
		executor.MatchRule{Pattern: "sh -c custom upgrade", Response: executor.MockCall{Err: errors.New("custom upgrade failed after mutation")}},
	).WithFallback(executor.MockCall{Err: errors.New("unexpected fallback command")})
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true}, &stubProvider{name: "apt", available: true})
	a.SetFallbackExecutor(fallbackExec)
	fallback := oldGitHubCLIFallbackSpec()
	fallback.Commands.Upgrade = "custom upgrade"
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"gh": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback:  fallback,
			},
		},
		Groups: []*config.GroupConfig{{Tools: groupTools("gh")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	seedGitHubFallbackUpgradeCacheRow(t, a.DB(), false, "")

	err := a.UpgradeToolFallback(ctx, "gh")
	if err == nil || !strings.Contains(err.Error(), "custom upgrade failed after mutation") {
		t.Fatalf("UpgradeToolFallback err = %v, want custom upgrade failure", err)
	}
	fallbackExec.AssertCalled(t, "sh -c custom upgrade")

	cached, err := a.DB().Get(ctx, "gh", "apt", "gh")
	if err != nil {
		t.Fatalf("Get gh after failed custom upgrade: %v", err)
	}
	if cached.Installed || cached.FailedAt == nil || cached.FailureCount != 1 {
		t.Fatalf("cache after failed custom upgrade = installed:%v failed_at:%v count:%d, want unknown installation with one failure", cached.Installed, cached.FailedAt, cached.FailureCount)
	}
}

func TestUpgradeToolFallback_GitHubRefreshFailureKeepsOldRecipeAndOutdatedRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	latestAsset := currentPlatformGitHubCLIAsset(t)
	calls := int32(0)
	fallbackExec := executor.NewMatchMock().WithFallback(executor.MockCall{})
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true}, &stubProvider{name: "apt", available: true})
	a.CacheDir = t.TempDir()
	a.SetFallbackExecutor(fallbackExec)
	a.SetGitHubFallbackAPIForTest("https://api.github.test", githubFallbackUpgradeClient(t, &calls, func() io.ReadCloser {
		body, err := os.Open("testdata/github_cli_latest_release.json")
		if err != nil {
			t.Fatalf("open GitHub fixture: %v", err)
		}
		return body
	}, nil, http.StatusNotFound))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"gh": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback:  nativeOldGitHubCLIFallbackSpec(),
			},
		},
		Groups: []*config.GroupConfig{{Tools: groupTools("gh")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	seedGitHubFallbackUpgradeCacheRow(t, a.DB(), true, "v2.93.0")
	if err := a.DB().UpsertPackageAvailability(ctx, database.PackageAvailability{
		Name: "gh", Provider: "apt", Package: "gh", Available: false, Reason: "no candidate",
	}); err != nil {
		t.Fatalf("seed package availability: %v", err)
	}

	err := a.UpgradeToolFallback(ctx, "gh")
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("UpgradeToolFallback err = %v, want native download failure", err)
	}

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("GitHub latest release calls = %d, want 1", got)
	}
	if got := fallbackExec.CallCount(); got != 0 {
		t.Fatalf("fallback shell command count = %d, want zero before native download failure", got)
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
	if strings.Contains(fallback.Commands.Upgrade, latestAsset.downloadURL) {
		t.Fatalf("fallback upgrade command = %q, want old command preserved after failure", fallback.Commands.Upgrade)
	}
	assertGitHubFallbackOutdated(t, a.DB(), true, "v2.93.0")

	result, err := a.Sync(ctx, isync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync after failed fallback download: %v", err)
	}
	if failed := result.Failed(); len(failed) != 0 {
		t.Fatalf("failed ops after retained fallback download = %+v, want none", failed)
	}
	if skipped := result.Skipped(); len(skipped) != 1 || skipped[0].Tool.Name != "gh" || skipped[0].Version != "2.92.0" {
		t.Fatalf("skipped ops after retained fallback download = %+v, want already-installed gh@2.92.0", skipped)
	}
}

func TestUpgradeToolFallback_GitHubRefreshCheckFailureKeepsOldRecipeAndOutdatedRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	latestAsset := currentPlatformGitHubCLIAsset(t)
	assetContent := currentPlatformGitHubCLIAssetContent(t, latestAsset, "gh", []byte("new gh binary"))
	calls := int32(0)
	fallbackExec := executor.NewMatchMock(
		executor.MatchRule{Pattern: "sh -c test -x", Response: executor.MockCall{Err: errors.New("missing refreshed binary")}},
	).WithFallback(executor.MockCall{Err: errors.New("old binary is not runnable")})
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true}, &stubProvider{name: "apt", available: true})
	a.CacheDir = t.TempDir()
	a.SetFallbackExecutor(fallbackExec)
	a.SetGitHubFallbackAPIForTest("https://api.github.test", githubFallbackUpgradeClient(t, &calls, func() io.ReadCloser {
		body, err := os.Open("testdata/github_cli_latest_release.json")
		if err != nil {
			t.Fatalf("open GitHub fixture: %v", err)
		}
		return body
	}, assetContent, http.StatusOK))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"gh": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback:  nativeOldGitHubCLIFallbackSpec(),
			},
		},
		Groups: []*config.GroupConfig{{Tools: groupTools("gh")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	seedGitHubFallbackUpgradeCacheRow(t, a.DB(), true, "v2.93.0")
	if err := a.DB().UpsertPackageAvailability(ctx, database.PackageAvailability{
		Name: "gh", Provider: "apt", Package: "gh", Available: false, Reason: "no candidate",
	}); err != nil {
		t.Fatalf("seed package availability: %v", err)
	}
	cacheDir, err := a.FallbackCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(cacheDir, "bin", "gh")
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		t.Fatal(err)
	}
	oldBinary := []byte("old gh binary")
	if err := os.WriteFile(binPath, oldBinary, 0o755); err != nil {
		t.Fatal(err)
	}

	err = a.UpgradeToolFallback(ctx, "gh")
	if err == nil || !strings.Contains(err.Error(), "upgrade verification failed") {
		t.Fatalf("UpgradeToolFallback err = %v, want check failure", err)
	}
	if got, readErr := os.ReadFile(binPath); readErr != nil || !bytes.Equal(got, oldBinary) {
		t.Fatalf("binary after failed upgrade = %q err=%v, want restored %q", got, readErr, oldBinary)
	}

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("GitHub latest release calls = %d, want 1", got)
	}
	assertFallbackCommandNotContains(t, fallbackExec, latestAsset.downloadURL)
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
	cached, err := a.DB().Get(ctx, "gh", "apt", "gh")
	if err != nil {
		t.Fatalf("Get gh after failed upgrade: %v", err)
	}
	if !cached.Installed || !cached.Version.Valid || cached.Version.String != "2.92.0" {
		t.Fatalf("cache after failed upgrade = installed:%v version:%+v, want installed gh@2.92.0", cached.Installed, cached.Version)
	}

	result, err := a.Sync(ctx, isync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync after fallback verification failure: %v", err)
	}
	if skipped := result.Skipped(); len(skipped) != 0 {
		t.Fatalf("skipped ops after unusable fallback upgrade = %+v, want none", skipped)
	}
	if failed := result.Failed(); len(failed) != 1 || failed[0].Tool.Name != "gh" {
		t.Fatalf("failed ops after unusable fallback upgrade = %+v, want gh failure", failed)
	}
	cached, err = a.DB().Get(ctx, "gh", "apt", "gh")
	if err != nil {
		t.Fatalf("Get gh after unusable fallback upgrade: %v", err)
	}
	if cached.Installed || cached.FailedAt == nil || cached.FailureCount != 1 {
		t.Fatalf("cache after unusable fallback upgrade = installed:%v failed_at:%v count:%d, want missing with one retained failure", cached.Installed, cached.FailedAt, cached.FailureCount)
	}
}

func TestUpgradeToolFallback_NativeRollbackFailureMarksInstalledStateUnknown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	assetContent := currentPlatformGitHubCLIAssetContent(t, currentPlatformGitHubCLIAsset(t), "gh", []byte("new gh binary"))
	calls := int32(0)
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true}, &stubProvider{name: "apt", available: true})
	a.CacheDir = t.TempDir()
	a.SetGitHubFallbackAPIForTest("https://api.github.test", githubFallbackUpgradeClient(t, &calls, func() io.ReadCloser {
		body, err := os.Open("testdata/github_cli_latest_release.json")
		if err != nil {
			t.Fatalf("open GitHub fixture: %v", err)
		}
		return body
	}, assetContent, http.StatusOK))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"gh": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback:  nativeOldGitHubCLIFallbackSpec(),
			},
		},
		Groups: []*config.GroupConfig{{Tools: groupTools("gh")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	seedGitHubFallbackUpgradeCacheRow(t, a.DB(), true, "v2.93.0")

	cacheDir, err := a.FallbackCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(cacheDir, "bin")
	binPath := filepath.Join(binDir, "gh")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binPath, []byte("old gh binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	movedBinDir := binDir + ".moved"
	a.SetFallbackExecutor(fallbackTestExecutorFunc(func(_ context.Context, name string, args ...string) (string, string, error) {
		if name != "sh" || len(args) != 2 || args[0] != "-c" || !strings.Contains(args[1], "test -x") {
			return "", "", fmt.Errorf("unexpected fallback command: %s %s", name, strings.Join(args, " "))
		}
		if err := os.Rename(binDir, movedBinDir); err != nil {
			return "", "", fmt.Errorf("move fallback bin dir: %w", err)
		}
		if err := os.WriteFile(binDir, []byte("block rollback destination"), 0o644); err != nil {
			return "", "", fmt.Errorf("block fallback bin dir: %w", err)
		}
		return "", "", errors.New("refreshed binary check failed")
	}))

	err = a.UpgradeToolFallback(ctx, "gh")
	if removeErr := os.Remove(binDir); removeErr != nil {
		t.Fatalf("remove rollback blocker: %v", removeErr)
	}
	if renameErr := os.Rename(movedBinDir, binDir); renameErr != nil {
		t.Fatalf("restore fallback bin dir fixture: %v", renameErr)
	}
	if err == nil || !strings.Contains(err.Error(), "restore previous fallback binary") {
		t.Fatalf("UpgradeToolFallback err = %v, want joined rollback failure", err)
	}

	cached, err := a.DB().Get(ctx, "gh", "apt", "gh")
	if err != nil {
		t.Fatalf("Get gh after failed rollback: %v", err)
	}
	if cached.Installed || cached.FailedAt == nil || cached.FailureCount != 1 {
		t.Fatalf("cache after failed rollback = installed:%v failed_at:%v count:%d, want unknown installation with one failure", cached.Installed, cached.FailedAt, cached.FailureCount)
	}
}

func TestUpgradeToolFallback_GitHubResolverFailureKeepsOldRecipeAndOutdatedRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	calls := int32(0)
	fallbackExec := executor.NewMatchMock(
		executor.MatchRule{Pattern: "sh -c command -v gh", Response: executor.MockCall{}},
	).WithFallback(executor.MockCall{Err: errors.New("unexpected fallback command")})
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
	if err := a.DB().UpsertPackageAvailability(ctx, database.PackageAvailability{
		Name: "gh", Provider: "apt", Package: "gh", Available: false, Reason: "no candidate",
	}); err != nil {
		t.Fatalf("seed package availability: %v", err)
	}

	err := a.UpgradeToolFallback(ctx, "gh")
	if err == nil {
		t.Fatal("UpgradeToolFallback err = nil, want resolver failure")
	}

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("GitHub latest release calls = %d, want 1", got)
	}
	if got := fallbackExec.CallCount(); got != 0 {
		t.Fatalf("fallback command count = %d, want zero when resolver fails; calls = %+v", got, fallbackExec.Calls)
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
	cached, err := a.DB().Get(ctx, "gh", "apt", "gh")
	if err != nil {
		t.Fatalf("Get gh after failed upgrade: %v", err)
	}
	if !cached.Installed || cached.InstalledWith != "gh" || !cached.Version.Valid || cached.Version.String != "2.92.0" {
		t.Fatalf("installed state after failed upgrade = installed:%v with:%q version:%+v, want installed gh@2.92.0", cached.Installed, cached.InstalledWith, cached.Version)
	}
	if cached.FailedAt == nil || cached.FailureCount != 1 || !cached.LastError.Valid {
		t.Fatalf("failure state after failed upgrade = failed_at:%v count:%d error:%+v, want one recorded failure", cached.FailedAt, cached.FailureCount, cached.LastError)
	}

	result, err := a.Sync(ctx, isync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync after failed fallback upgrade: %v", err)
	}
	if failed := result.Failed(); len(failed) != 0 {
		t.Fatalf("failed ops after retained fallback upgrade = %+v, want none; calls = %+v", failed, fallbackExec.Calls)
	}
	if skipped := result.Skipped(); len(skipped) != 1 || skipped[0].Tool.Name != "gh" || skipped[0].Version != "2.92.0" {
		t.Fatalf("skipped ops after retained fallback upgrade = %+v, want already-installed gh@2.92.0", skipped)
	}
	fallbackExec.AssertCalled(t, "sh -c command -v gh")
	checks := fallbackExec.CallsMatching("sh -c command -v gh")
	if got := fallbackExec.CallCount(); got != len(checks) {
		t.Fatalf("fallback commands after failed upgrade and sync = %+v, want checks only", fallbackExec.Calls)
	}
}

func TestUpgradeToolFallback_GitHubNotOutdatedUsesSavedRecipeWithoutReleaseLookup(t *testing.T) {
	t.Parallel()
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

func TestUpgradeToolFallback_GitHubWithoutCacheRowUsesSavedRecipeWithoutReleaseLookup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fallbackExec := executor.NewMatchMock(
		executor.MatchRule{Pattern: "sh -c curl -fsSL https://github.com/cli/cli/releases/download/v2.92.0/gh_2.92.0_old.zip", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "sh -c command -v gh", Response: executor.MockCall{}},
	).WithFallback(executor.MockCall{Err: errors.New("unexpected fallback command")})
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true}, &stubProvider{name: "apt", available: true})
	a.SetFallbackExecutor(fallbackExec)
	a.SetGitHubFallbackAPIForTest("https://api.github.test", githubFallbackLatestReleaseClient(t, nil, func() io.ReadCloser {
		t.Fatal("GitHub latest release endpoint should not be called without a cache row")
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

func nativeOldGitHubCLIFallbackSpec() *config.FallbackSpec {
	fallback := oldGitHubCLIFallbackSpec()
	fallback.Commands = config.FallbackCommands{}
	return fallback
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
		Version:       sql.NullString{String: "2.92.0", Valid: true},
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

func githubFallbackUpgradeClient(t *testing.T, calls *int32, latest func() io.ReadCloser, asset []byte, assetStatus int) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		status := http.StatusOK
		body := io.NopCloser(strings.NewReader(string(asset)))
		switch {
		case req.URL.Path == "/repos/cli/cli/releases/latest":
			atomic.AddInt32(calls, 1)
			body = latest()
		case strings.Contains(req.URL.Path, "/releases/download/"):
			status = assetStatus
		case req.URL.Path == "/repos/cli/cli/releases/tags/v2.93.0":
			status = http.StatusNotFound
		default:
			t.Fatalf("unexpected GitHub path %q", req.URL.Path)
		}
		return &http.Response{
			StatusCode: status,
			Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
			Body:       body,
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}
}

func assertFallbackCommandNotContains(t *testing.T, exec *executor.MatchMockExecutor, value string) {
	t.Helper()
	for _, call := range exec.CallsMatching("sh -c ") {
		if strings.Contains(call.Name+" "+strings.Join(call.Args, " "), value) {
			t.Fatalf("fallback command contained %q; calls = %+v", value, exec.Calls)
		}
	}
}

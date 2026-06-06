package app_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true})
	a.SetFallbackExecutor(mock)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Provider: "system",
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

	cached, err := a.DB().Get(ctx, "rg", "system", "rg")
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
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true})
	a.CacheDir = filepath.Join(t.TempDir(), "cache")
	binDir := filepath.Join(home, ".local", "share", "omni", "fallback", "bin")
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{FallbackBinDir: "~/.local/share/omni/fallback/bin"},
		Tools: map[string]config.ToolSpec{
			"rg": {
				Provider: "system",
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
	cached, err := a.DB().Get(ctx, "rg", "system", "rg")
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
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true})
	a.SetFallbackExecutor(mock)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Provider: "system",
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
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true})
	a.SetFallbackExecutor(mock)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Provider: "system",
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
				Provider: "system",
				Package:  "ripgrep",
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

	if err := a.Install(ctx, "rg", "system"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if len(apt.upgraded) != 0 || len(apt.installedChecks) != 0 {
		t.Fatalf("apt lifecycle calls = upgraded:%v checks:%v, want no native lifecycle", apt.upgraded, apt.installedChecks)
	}
	fallbackExec.AssertCalled(t, "sh -c install rg")
	fallbackExec.AssertCalled(t, "sh -c command -v rg")
	cached, err := a.DB().Get(ctx, "rg", "system", "ripgrep")
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
				Provider: "system",
				Package:  "ripgrep",
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
	cached, err := a.DB().Get(ctx, "rg", "system", "ripgrep")
	if err != nil {
		t.Fatalf("Get rg: %v", err)
	}
	if !cached.Installed || cached.InstalledWith != "gh" {
		t.Fatalf("cached = installed %v with %q, want installed with gh", cached.Installed, cached.InstalledWith)
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
				Provider: "system",
				Package:  "ripgrep",
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
				Provider: "system",
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
	if _, err := a.DB().Get(ctx, "rg", "system", "rg"); err == nil {
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
				Provider: "system",
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
	if err := a.DB().MarkFailed(ctx, "rg", "system", "rg", "previous fallback failure"); err != nil {
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
	cached, err := a.DB().Get(ctx, "rg", "system", "rg")
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
				Provider: "system",
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

	err := a.Install(ctx, "rg", "system")
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
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true})
	a.SetFallbackExecutor(fallbackExec)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Provider: "system",
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
		Provider:      "system",
		Package:       "rg",
		Installed:     true,
		InstalledWith: "gh",
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	if err := a.Uninstall(ctx, "rg", "system"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	fallbackExec.AssertCalled(t, "sh -c uninstall rg")
	if _, err := a.DB().Get(ctx, "rg", "system", "rg"); !errors.Is(err, sql.ErrNoRows) {
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
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true})
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Provider: "system",
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
		Provider:      "system",
		Package:       "rg",
		Installed:     true,
		InstalledWith: "gh",
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	err := a.Uninstall(ctx, "rg", "system")
	if err == nil || !strings.Contains(err.Error(), "fallback uninstall is not available") {
		t.Fatalf("Uninstall err = %v, want unavailable fallback uninstall", err)
	}
	if _, err := a.DB().Get(ctx, "rg", "system", "rg"); err != nil {
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
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true})
	a.SetFallbackExecutor(fallbackExec)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Provider: "system",
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
		Provider:      "system",
		Package:       "rg",
		Installed:     true,
		InstalledWith: "gh",
		Outdated:      true,
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	if err := a.Upgrade(ctx, "rg", "system"); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	fallbackExec.AssertCalled(t, "sh -c upgrade rg")
	fallbackExec.AssertCalled(t, "sh -c command -v rg")
	cached, err := a.DB().Get(ctx, "rg", "system", "rg")
	if err != nil {
		t.Fatalf("Get rg: %v", err)
	}
	if !cached.Installed || cached.InstalledWith != "gh" || cached.Outdated {
		t.Fatalf("cached = installed:%v with:%q outdated:%v, want installed gh not outdated", cached.Installed, cached.InstalledWith, cached.Outdated)
	}
}

func TestUninstallToolFallback_ReportsUnavailableWithoutCommand(t *testing.T) {
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true})
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Provider: "system",
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

func TestSaveToolFallbackFromGitHub_PersistsUnresolvedSource(t *testing.T) {
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true})
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("rg", "system")),
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.SaveToolFallbackFromGitHub(context.Background(), "rg", "BurntSushi/ripgrep"); err != nil {
		t.Fatalf("SaveToolFallbackFromGitHub: %v", err)
	}
	got, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	fallback := got.Tools["rg"].Fallback
	if fallback == nil {
		t.Fatal("fallback missing")
	}
	if fallback.Status != config.FallbackStatusUnresolved || fallback.Source.Owner != "BurntSushi" || fallback.Source.Repo != "ripgrep" {
		t.Fatalf("fallback = %+v, want unresolved BurntSushi/ripgrep source", fallback)
	}
	if fallback.Binary != "rg" || fallback.Commands.Check != "command -v rg" {
		t.Fatalf("fallback binary/check = %q/%q, want rg command check", fallback.Binary, fallback.Commands.Check)
	}
}

func TestSaveToolFallbackFromGitHub_NormalizesSSHRepoURL(t *testing.T) {
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true})
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("rg", "system")),
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.SaveToolFallbackFromGitHub(context.Background(), "rg", "git@github.com:BurntSushi/ripgrep.git"); err != nil {
		t.Fatalf("SaveToolFallbackFromGitHub: %v", err)
	}
	got, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	fallback := got.Tools["rg"].Fallback
	if fallback == nil {
		t.Fatal("fallback missing")
	}
	if fallback.Source.Owner != "BurntSushi" || fallback.Source.Repo != "ripgrep" || fallback.Source.URL != "https://github.com/BurntSushi/ripgrep" {
		t.Fatalf("fallback source = %+v, want normalized BurntSushi/ripgrep", fallback.Source)
	}
}

func TestSaveToolFallbackFromGitHub_RejectsInvalidRepo(t *testing.T) {
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true})
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

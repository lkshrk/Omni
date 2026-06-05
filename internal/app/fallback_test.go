package app_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/executor"
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

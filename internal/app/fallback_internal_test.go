package app

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/executor"
	isync "github.com/lkshrk/omni/internal/sync"
)

func TestAutomaticFallbackUsableStatusRules(t *testing.T) {
	base := config.FallbackSpec{
		Status: config.FallbackStatusUnverified,
		Commands: config.FallbackCommands{
			Install: "install rg",
			Check:   "command -v rg",
		},
	}
	tests := []struct {
		name        string
		fallback    *config.FallbackSpec
		allowFailed bool
		want        bool
	}{
		{name: "nil", want: false},
		{name: "unverified usable", fallback: &base, want: true},
		{name: "failed blocked", fallback: fallbackWithStatus(base, config.FallbackStatusFailed), want: false},
		{name: "failed allowed for retry", fallback: fallbackWithStatus(base, config.FallbackStatusFailed), allowFailed: true, want: true},
		{name: "unresolved blocked", fallback: fallbackWithStatus(base, config.FallbackStatusUnresolved), want: false},
		{name: "unsupported blocked", fallback: fallbackWithStatus(base, config.FallbackStatusUnsupported), want: false},
		{name: "missing install blocked", fallback: fallbackWithCommands(base, "", "command -v rg"), want: false},
		{name: "missing check blocked", fallback: fallbackWithCommands(base, "install rg", ""), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := automaticFallbackUsable(tt.fallback, tt.allowFailed); got != tt.want {
				t.Fatalf("automaticFallbackUsable = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAutomaticFallbackUsableForToolHandlesConfiguredMissingFallback(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	a := New(cfgPath)
	if err := config.Save(cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {Providers: []config.ToolInstallSpec{{Provider: "brew"}}},
		},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	usable, err := a.automaticFallbackUsableForTool("rg", false)
	if err != nil {
		t.Fatalf("automaticFallbackUsableForTool configured no fallback: %v", err)
	}
	if usable {
		t.Fatal("automaticFallbackUsableForTool = true, want false without fallback")
	}

	if _, err := a.automaticFallbackUsableForTool("missing", false); err == nil {
		t.Fatal("automaticFallbackUsableForTool missing err = nil, want missing tool error")
	}
}

func TestSyncNativeUnavailableFallbacksUsesCachedFallbackInstall(t *testing.T) {
	ctx := context.Background()
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	a := New(cfgPath)
	if err := config.Save(cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Package:   "ripgrep",
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
					Status: config.FallbackStatusVerified,
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
	if err := a.InitTestMode(ctx); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer a.Close() //nolint:errcheck
	fallbackExec := executor.NewMatchMock(
		executor.MatchRule{Pattern: "sh -c command -v rg", Response: executor.MockCall{}},
	).WithFallback(executor.MockCall{Err: context.DeadlineExceeded})
	a.SetFallbackExecutor(fallbackExec)
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "rg",
		Provider:      "apt",
		Package:       "ripgrep",
		Installed:     true,
		InstalledWith: "gh",
		Version:       sql.NullString{String: "14.1.0", Valid: true},
	}); err != nil {
		t.Fatalf("seed fallback cache row: %v", err)
	}
	entry := config.ToolEntry{Name: "rg", Provider: "apt", Package: "ripgrep"}

	filtered, ops := a.syncNativeUnavailableFallbacks(ctx, []resolvedTool{{
		entry: entry,
		route: installRoute{
			Kind:               installRouteFallbackEligible,
			Install:            config.ToolInstallSpec{Provider: "apt", Package: "ripgrep"},
			FallbackConfigured: true,
		},
	}}, isync.SyncOptions{})

	fallbackExec.AssertCalled(t, "sh -c command -v rg")
	if len(filtered) != 0 {
		t.Fatalf("filtered tools = %+v, want fallback handled outside native sync", filtered)
	}
	if len(ops) != 1 || ops[0].Kind != isync.OpAlreadyInstalled || ops[0].Version != "14.1.0" {
		t.Fatalf("fallback ops = %+v, want already-installed cached fallback version", ops)
	}
	if fallbackExec.CallCount() != 1 {
		t.Fatalf("fallback command count = %d, want check only", fallbackExec.CallCount())
	}
}

func fallbackWithStatus(base config.FallbackSpec, status string) *config.FallbackSpec {
	base.Status = status
	return &base
}

func fallbackWithCommands(base config.FallbackSpec, install, check string) *config.FallbackSpec {
	base.Commands.Install = install
	base.Commands.Check = check
	return &base
}

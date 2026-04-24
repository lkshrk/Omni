package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
)

// failInstallProvider wraps stubProvider and always returns an error from Install.
type failInstallProvider struct {
	stubProvider
}

func (f *failInstallProvider) Install(_ context.Context, _ provider.Tool) error {
	return errors.New("install failed")
}

// selectiveFailInstall wraps stubProvider and returns an error for specific tool names.
type selectiveFailInstall struct {
	stubProvider
	failFor []string
}

func (s *selectiveFailInstall) Install(_ context.Context, t provider.Tool) error {
	for _, name := range s.failFor {
		if t.Name == name {
			return errors.New("install failed: " + name)
		}
	}
	s.installed = append(s.installed, provider.InstalledTool{Tool: t})
	return nil
}

func TestConsolidateToProvider_MovesAllTools(t *testing.T) {
	brew := &installTracker{stubProvider: stubProvider{name: "brew", available: true}}
	pip := &stubProvider{name: "pip", available: true}
	node := &stubProvider{name: "node", available: true}

	a, cfgPath := newImportApp(t, brew, pip, node)

	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"black":      {Provider: "python", InstallWith: "pip"},
			"typescript": {Provider: "node"},
			"ripgrep":    {Provider: "system", InstallWith: "brew"},
		},
		Groups: []*config.GroupConfig{{
			Tools: []config.ToolEntry{
				{Name: "black"},
				{Name: "typescript"},
				{Name: "ripgrep"}, // already on target — skipped
			},
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	res, err := a.ConsolidateToProvider(context.Background(), "brew", false, nil)
	if err != nil {
		t.Fatalf("ConsolidateToProvider: %v", err)
	}

	if len(res.Migrated) != 2 {
		t.Errorf("Migrated = %d, want 2 (black + typescript)", len(res.Migrated))
	}
	if len(res.Failed) != 0 {
		t.Errorf("unexpected failures: %v", res.Failed)
	}
	if len(brew.installCalled) != 2 {
		t.Errorf("brew.Install called %d times, want 2", len(brew.installCalled))
	}

	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	for _, e := range toolsFromConfig(updated) {
		if e.Provider != "system" || e.InstallWith != "brew" {
			t.Errorf("tool %q = provider %q install_with %q after consolidate, want system via brew", e.Name, e.Provider, e.InstallWith)
		}
	}
}

func TestConsolidateToProvider_DryRun(t *testing.T) {
	brew := &installTracker{stubProvider: stubProvider{name: "brew", available: true}}
	pip := &stubProvider{name: "pip", available: true}

	a, cfgPath := newImportApp(t, brew, pip)

	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"black": {Provider: "python", InstallWith: "pip"},
		},
		Groups: []*config.GroupConfig{{
			Tools: []config.ToolEntry{{Name: "black"}},
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	res, err := a.ConsolidateToProvider(context.Background(), "brew", true, nil)
	if err != nil {
		t.Fatalf("ConsolidateToProvider dry-run: %v", err)
	}

	if len(res.Migrated) != 1 || res.Migrated[0].Name != "black" {
		t.Errorf("Migrated = %v, want [{black}]", res.Migrated)
	}
	if len(brew.installCalled) != 0 {
		t.Errorf("Install called during dry-run: %v", brew.installCalled)
	}

	// Config must be unchanged.
	unchanged, _ := config.Load(cfgPath)
	tools := toolsFromConfig(unchanged)
	if len(tools) == 0 || tools[0].Provider != "python" || tools[0].InstallWith != "pip" {
		t.Error("config was modified during dry-run")
	}
}

func TestConsolidateToProvider_AlreadyOnTarget(t *testing.T) {
	brew := &installTracker{stubProvider: stubProvider{name: "brew", available: true}}

	a, cfgPath := newImportApp(t, brew)

	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {Provider: "system", InstallWith: "brew"},
			"git":     {Provider: "system", InstallWith: "brew"},
		},
		Groups: []*config.GroupConfig{{
			Tools: []config.ToolEntry{
				{Name: "ripgrep"},
				{Name: "git"},
			},
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	res, err := a.ConsolidateToProvider(context.Background(), "brew", false, nil)
	if err != nil {
		t.Fatalf("ConsolidateToProvider: %v", err)
	}

	if len(res.Migrated) != 0 {
		t.Errorf("Migrated = %d, want 0 (already on brew)", len(res.Migrated))
	}
	if len(brew.installCalled) != 0 {
		t.Errorf("Install called %d times, want 0", len(brew.installCalled))
	}
}

func TestConsolidateToProvider_UnknownProvider(t *testing.T) {
	a, _ := newImportApp(t)
	_, err := a.ConsolidateToProvider(context.Background(), "winget", false, nil)
	if err == nil {
		t.Error("expected error for unknown provider, got nil")
	}
}

func TestConsolidateToProvider_InstallFailureIsNonFatal(t *testing.T) {
	failing := &failInstallProvider{stubProvider: stubProvider{name: "brew", available: true}}
	pip := &stubProvider{name: "pip", available: true}

	a, cfgPath := newImportApp(t, failing, pip)

	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"black": {Provider: "python", InstallWith: "pip"},
			"ruff":  {Provider: "python", InstallWith: "pip"},
		},
		Groups: []*config.GroupConfig{{
			Tools: []config.ToolEntry{
				{Name: "black"},
				{Name: "ruff"},
			},
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	res, err := a.ConsolidateToProvider(context.Background(), "brew", false, nil)
	if err != nil {
		t.Fatalf("ConsolidateToProvider should not return error on install failures: %v", err)
	}
	if len(res.Failed) != 2 {
		t.Errorf("Failed = %d, want 2", len(res.Failed))
	}
	if len(res.Migrated) != 0 {
		t.Errorf("Migrated = %d, want 0", len(res.Migrated))
	}

	// Config unchanged — nothing migrated.
	unchanged, _ := config.Load(cfgPath)
	for _, e := range toolsFromConfig(unchanged) {
		if e.Provider != "python" || e.InstallWith != "pip" {
			t.Errorf("tool %q = provider %q install_with %q despite install failure, want python via pip", e.Name, e.Provider, e.InstallWith)
		}
	}
}

func TestConsolidateToProvider_PartialFailure(t *testing.T) {
	// "brew" succeeds for "black" but fails for "ruff".
	brew := &selectiveFailInstall{
		stubProvider: stubProvider{name: "brew", available: true},
		failFor:      []string{"ruff"},
	}
	pip := &stubProvider{name: "pip", available: true}

	a, cfgPath := newImportApp(t, brew, pip)

	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"black": {Provider: "python", InstallWith: "pip"},
			"ruff":  {Provider: "python", InstallWith: "pip"},
		},
		Groups: []*config.GroupConfig{{
			Tools: []config.ToolEntry{
				{Name: "black"},
				{Name: "ruff"},
			},
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	res, err := a.ConsolidateToProvider(context.Background(), "brew", false, nil)
	if err != nil {
		t.Fatalf("ConsolidateToProvider: %v", err)
	}

	// black succeeded, ruff failed.
	if len(res.Migrated) != 1 || res.Migrated[0].Name != "black" {
		t.Errorf("Migrated = %v, want [{black}]", res.Migrated)
	}
	if len(res.Failed) != 1 || res.Failed[0].Name != "ruff" {
		t.Errorf("Failed = %v, want [{ruff}]", res.Failed)
	}

	// Config: black moved to brew, ruff stays on pip.
	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	providerFor := make(map[string]config.ToolEntry)
	for _, e := range toolsFromConfig(updated) {
		providerFor[e.Name] = e
	}
	if got := providerFor["black"]; got.Provider != "system" || got.InstallWith != "brew" {
		t.Errorf("black = %+v after partial migrate, want system via brew", got)
	}
	if got := providerFor["ruff"]; got.Provider != "python" || got.InstallWith != "pip" {
		t.Errorf("ruff = %+v after partial migrate failure, want python via pip", got)
	}
}

func TestConsolidateToProvider_UninstallsConfiguredSourceManager(t *testing.T) {
	brew := &installTracker{stubProvider: stubProvider{name: "brew", available: true}}
	python := &envCleanerProvider{stubProvider: stubProvider{name: "python", available: true}}
	a, cfgPath := newImportApp(t, brew, python)

	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"black": {Provider: "python", InstallWith: "pip3"},
		},
		Groups: []*config.GroupConfig{{
			Tools: []config.ToolEntry{{Name: "black"}},
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if _, err := a.ConsolidateToProvider(context.Background(), "brew", false, nil); err != nil {
		t.Fatalf("ConsolidateToProvider: %v", err)
	}
	if len(python.uninstallFromBinaries) != 1 || python.uninstallFromBinaries[0] != "pip3" {
		t.Fatalf("UninstallFrom managers = %v, want [pip3]", python.uninstallFromBinaries)
	}
}

func TestConsolidateToProvider_ReturnsDBUpdateError(t *testing.T) {
	brew := &installTracker{stubProvider: stubProvider{name: "brew", available: true}}
	pip := &stubProvider{name: "pip", available: true}

	a, cfgPath := newImportApp(t, brew, pip)

	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"black": {Provider: "python", InstallWith: "pip"},
		},
		Groups: []*config.GroupConfig{{
			Tools: []config.ToolEntry{{Name: "black"}},
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.DB().Close(); err != nil {
		t.Fatalf("closing DB: %v", err)
	}

	_, err := a.ConsolidateToProvider(context.Background(), "brew", false, nil)
	if err == nil {
		t.Fatal("ConsolidateToProvider returned nil, want DB update error")
	}
	if !strings.Contains(err.Error(), "upserting consolidate cache") {
		t.Fatalf("ConsolidateToProvider error = %q, want DB cache update context", err)
	}
}

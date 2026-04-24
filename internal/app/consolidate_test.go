package app_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
)

// installTracker wraps stubProvider and records Install calls.
type installTracker struct {
	stubProvider
	installed     []provider.InstalledTool
	installCalled []string
}

type installWithoutVerificationProvider struct {
	stubProvider
	installCalled []string
}

func (s *installWithoutVerificationProvider) Install(_ context.Context, t provider.Tool) error {
	s.installCalled = append(s.installCalled, t.Name)
	return nil
}

func (s *installTracker) Install(_ context.Context, t provider.Tool) error {
	s.installCalled = append(s.installCalled, t.Name)
	s.installed = append(s.installed, provider.InstalledTool{Tool: t})
	return nil
}

func (s *installTracker) IsInstalled(ctx context.Context, t provider.Tool) (bool, string, error) {
	for _, installed := range s.installed {
		if installed.Tool.Name == t.Name && installed.Tool.Provider == t.Provider {
			return true, installed.Version, nil
		}
	}
	return s.stubProvider.IsInstalled(ctx, t)
}

func TestConsolidateOptions_AllEcosystems(t *testing.T) {
	python := &stubProvider{name: "python", available: true}
	pip := &stubProvider{name: "pip", available: true}
	node := &stubProvider{name: "node", available: true}

	a, _ := newImportApp(t, python, pip, node)

	opts := a.ConsolidateOptions()
	if len(opts) == 0 {
		t.Fatal("ConsolidateOptions returned empty slice")
	}
	for _, o := range opts {
		if o.Ecosystem == "" || o.Manager == "" {
			t.Errorf("got empty field in option %+v", o)
		}
	}
	// Verify ordering: node before python (alphabetical by ecosystem).
	for i := 1; i < len(opts); i++ {
		if opts[i-1].Ecosystem > opts[i].Ecosystem {
			t.Errorf("ConsolidateOptions not sorted by ecosystem at index %d", i)
		}
	}
}

func TestConsolidateOptions_OnlyRegisteredEcosystems(t *testing.T) {
	// Only python providers — node options must not appear.
	python := &stubProvider{name: "python", available: true}
	a, _ := newImportApp(t, python)

	for _, o := range a.ConsolidateOptions() {
		if o.Ecosystem == "node" {
			t.Errorf("got node option without node provider registered: %+v", o)
		}
	}
}

func TestConsolidatePlan_DryRun(t *testing.T) {
	python := &installTracker{stubProvider: stubProvider{name: "python", available: true}}
	pip := &stubProvider{name: "pip", available: true}

	a, cfgPath := newImportApp(t, python, pip)

	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"black": {Provider: "python", InstallWith: "pip"},
			"git":   {Provider: "system", InstallWith: "brew"},
		},
		Groups: []*config.GroupConfig{{
			Tools: []config.ToolEntry{
				{Name: "black"},
				{Name: "git"}, // not in python ecosystem — ignored
			},
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	result, err := a.ConsolidatePlan(context.Background(), "python", "uv")
	if err != nil {
		t.Fatalf("ConsolidatePlan: %v", err)
	}

	if len(result.Migrated) != 1 || result.Migrated[0].Name != "black" {
		t.Errorf("Migrated = %v, want [{Name:black ...}]", result.Migrated)
	}
	if len(python.installCalled) != 0 {
		t.Errorf("Install called during dry-run: %v", python.installCalled)
	}
}

func TestConsolidate_PipToPythonUV(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost.example")
	python := &installTracker{stubProvider: stubProvider{name: "python", available: true}}
	pip := &stubProvider{name: "pip", available: true}

	a, cfgPath := newImportApp(t, python, pip)

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

	result, err := a.Consolidate(context.Background(), "python", "uv", nil)
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}

	if len(result.Migrated) != 2 {
		t.Errorf("Migrated = %d, want 2", len(result.Migrated))
	}
	if len(result.Failed) != 0 {
		t.Errorf("unexpected failures: %v", result.Failed)
	}
	if len(python.installCalled) != 2 {
		t.Errorf("Install called %d times, want 2", len(python.installCalled))
	}
	if !result.SettingsUpdated {
		t.Error("SettingsUpdated should be true")
	}

	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	for _, entry := range toolsFromConfig(updated) {
		if entry.Provider != "python" || entry.InstallWith != "" {
			t.Errorf("tool %q = provider %q install_with %q, want python with default manager", entry.Name, entry.Provider, entry.InstallWith)
		}
	}
	if updated.Settings.EcosystemManager("python") != "" {
		t.Errorf("global python manager = %q, want empty", updated.Settings.EcosystemManager("python"))
	}
	if got := updated.HostSettings["testhost"].EcosystemManager("python"); got != "uv" {
		t.Errorf("host python manager = %q, want uv", got)
	}
}

func TestConsolidate_VerificationFailureDoesNotRewriteConfigOrCache(t *testing.T) {
	brew := &installWithoutVerificationProvider{stubProvider: stubProvider{name: "brew", available: true}}
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

	result, err := a.ConsolidateToProvider(context.Background(), "brew", false, nil)
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if len(result.Failed) != 1 {
		t.Fatalf("Failed = %+v, want one verification failure", result.Failed)
	}
	if !strings.Contains(result.Failed[0].Err.Error(), "not installed after install") {
		t.Fatalf("failure = %q, want verification failure", result.Failed[0].Err.Error())
	}
	if len(result.Migrated) != 0 {
		t.Fatalf("Migrated = %+v, want none", result.Migrated)
	}
	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	tools := toolsFromConfig(updated)
	if len(tools) != 1 || tools[0].Provider != "python" || tools[0].InstallWith != "pip" {
		t.Fatalf("config tool = %+v, want original python via pip", tools)
	}
}

func TestConsolidateManager_VerificationFailureDoesNotRewriteConfigOrCache(t *testing.T) {
	python := &installWithoutVerificationProvider{stubProvider: stubProvider{name: "python", available: true}}
	pip := &stubProvider{name: "pip", available: true}
	a, cfgPath := newImportApp(t, python, pip)

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

	result, err := a.Consolidate(context.Background(), "python", "uv", nil)
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if len(result.Failed) != 1 {
		t.Fatalf("Failed = %+v, want one verification failure", result.Failed)
	}
	if !strings.Contains(result.Failed[0].Err.Error(), "not installed after install") {
		t.Fatalf("failure = %q, want verification failure", result.Failed[0].Err.Error())
	}
	if len(result.Migrated) != 0 {
		t.Fatalf("Migrated = %+v, want none", result.Migrated)
	}
	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	tools := toolsFromConfig(updated)
	if len(tools) != 1 || tools[0].Provider != "python" || tools[0].InstallWith != "pip" {
		t.Fatalf("config tool = %+v, want original python via pip", tools)
	}
	dbTools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range dbTools {
		if tool.Installed {
			t.Fatalf("DB tool = %+v, want no installed cache row", tool)
		}
	}
}

func TestConsolidate_PythonToPip(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost.example")
	python := &stubProvider{name: "python", available: true}
	pip := &installTracker{stubProvider: stubProvider{name: "pip", available: true}}

	a, cfgPath := newImportApp(t, python, pip)

	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"black": {Provider: "python", InstallWith: "uv"},
		},
		Groups: []*config.GroupConfig{{
			Tools: []config.ToolEntry{
				{Name: "black"},
			},
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	result, err := a.Consolidate(context.Background(), "python", "pip", nil)
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}

	if len(result.Migrated) != 1 {
		t.Errorf("Migrated = %d, want 1", len(result.Migrated))
	}
	if len(pip.installCalled) != 1 || pip.installCalled[0] != "black" {
		t.Errorf("pip.installCalled = %v, want [black]", pip.installCalled)
	}

	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	tools := toolsFromConfig(updated)
	if len(tools) == 0 || tools[0].Provider != "python" || tools[0].InstallWith != "" {
		t.Errorf("tool = %+v, want python with default manager", tools[0])
	}
	if updated.Settings.EcosystemManager("python") != "" {
		t.Errorf("global python manager = %q, want empty", updated.Settings.EcosystemManager("python"))
	}
	if got := updated.HostSettings["testhost"].EcosystemManager("python"); got != "pip3" {
		t.Errorf("host python manager = %q, want pip3", got)
	}
}

func TestConsolidate_NodeUpdatesSettingsOnly(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost.example")
	node := &installTracker{stubProvider: stubProvider{name: "node", available: true}}

	a, cfgPath := newImportApp(t, node)

	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"typescript": {Provider: "node"},
		},
		Groups: []*config.GroupConfig{{
			Tools: []config.ToolEntry{
				{Name: "typescript"},
			},
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	result, err := a.Consolidate(context.Background(), "node", "pnpm", nil)
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}

	// All tools already on "node" provider — no migration, only settings update.
	if len(result.Migrated) != 0 {
		t.Errorf("Migrated = %d, want 0", len(result.Migrated))
	}
	if len(node.installCalled) != 0 {
		t.Errorf("Install should not be called: %v", node.installCalled)
	}
	if !result.SettingsUpdated {
		t.Error("SettingsUpdated should be true")
	}

	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("loading omni: %v", err)
	}
	if updated.Settings.EcosystemManager("node") != "" {
		t.Errorf("global node manager = %q, want empty", updated.Settings.EcosystemManager("node"))
	}
	if got := updated.HostSettings["testhost"].EcosystemManager("node"); got != "pnpm" {
		t.Errorf("host node manager = %q, want pnpm", got)
	}
}

func TestConsolidate_AlreadyOnTargetProvider_Skipped(t *testing.T) {
	python := &installTracker{stubProvider: stubProvider{name: "python", available: true}}
	pip := &stubProvider{name: "pip", available: true}

	a, cfgPath := newImportApp(t, python, pip)

	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"black": {Provider: "python"},
			"ruff":  {Provider: "python"},
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

	result, err := a.Consolidate(context.Background(), "python", "uv", nil)
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if len(result.Migrated) != 0 {
		t.Errorf("Migrated = %d, want 0 (already on target provider)", len(result.Migrated))
	}
	if len(python.installCalled) != 0 {
		t.Errorf("Install should not be called: %v", python.installCalled)
	}
}

func TestConsolidate_UnknownEcosystem(t *testing.T) {
	a, _ := newImportApp(t)

	_, err := a.Consolidate(context.Background(), "cargo", "cargo-install", nil)
	if err == nil {
		t.Error("expected error for unknown ecosystem, got nil")
	}
}

func TestConsolidate_UnknownManager(t *testing.T) {
	python := &stubProvider{name: "python", available: true}
	a, _ := newImportApp(t, python)

	_, err := a.Consolidate(context.Background(), "python", "poetry", nil)
	if err == nil {
		t.Error("expected error for unknown manager, got nil")
	}
}

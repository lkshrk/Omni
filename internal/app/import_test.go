package app_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
)

// stubProvider is a fake provider for import tests.
type stubProvider struct {
	name      string
	available bool
	installed []provider.InstalledTool
}

func (s *stubProvider) Name() string        { return s.name }
func (s *stubProvider) Description() string { return s.name + " stub" }
func (s *stubProvider) Available(_ context.Context) (bool, error) {
	return s.available, nil
}
func (s *stubProvider) Install(_ context.Context, tool provider.Tool) error {
	for _, installed := range s.installed {
		if installed.EffectivePackage() == tool.EffectivePackage() {
			return nil
		}
	}
	s.installed = append(s.installed, provider.InstalledTool{Tool: tool})
	return nil
}
func (s *stubProvider) Uninstall(_ context.Context, tool provider.Tool) error {
	remaining := s.installed[:0]
	for _, installed := range s.installed {
		if installed.EffectivePackage() == tool.EffectivePackage() {
			continue
		}
		remaining = append(remaining, installed)
	}
	s.installed = remaining
	return nil
}
func (s *stubProvider) Upgrade(_ context.Context, _ provider.Tool) error { return nil }
func (s *stubProvider) IsInstalled(_ context.Context, tool provider.Tool) (bool, string, error) {
	for _, installed := range s.installed {
		if installed.EffectivePackage() == tool.EffectivePackage() {
			return true, installed.Version, nil
		}
	}
	return false, "", nil
}
func (s *stubProvider) ListInstalled(_ context.Context) ([]provider.InstalledTool, error) {
	return s.installed, nil
}

func newImportApp(t *testing.T, providers ...provider.Provider) (*app.App, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	a := app.New(cfgPath)
	if err := a.InitTestMode(context.Background(), providers...); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a, cfgPath
}

func installedTool(name, ver string, prov string) provider.InstalledTool {
	return provider.InstalledTool{
		Tool:    provider.Tool{Name: name, Provider: prov},
		Version: ver,
	}
}

func TestImport_AddsNewTools(t *testing.T) {
	stub := &stubProvider{
		name:      "brew",
		available: true,
		installed: []provider.InstalledTool{
			installedTool("git", "2.43.0", "brew"),
			installedTool("ripgrep", "14.1.0", "brew"),
		},
	}
	a, cfgPath := newImportApp(t, stub)

	result, err := a.Import(context.Background(), app.ImportOptions{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if len(result.Added) != 2 {
		t.Errorf("Added = %d, want 2", len(result.Added))
	}
	if len(result.Skipped) != 0 {
		t.Errorf("Skipped = %d, want 0", len(result.Skipped))
	}

	// Config file should contain both tools.
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	tools := toolsFromConfig(cfg)
	if len(tools) != 2 {
		t.Errorf("config tools = %d, want 2", len(tools))
	}
	hostGroup := findHostTestGroup(cfg.Groups, testShortHostname())
	if hostGroup == nil || !hostGroup.IsHost() {
		t.Fatalf("default import group = %#v, want protected host group", hostGroup)
	}
	if err := a.EnsureHost(testShortHostname()); err != nil {
		t.Fatalf("EnsureHost after import: %v", err)
	}
}

func TestImport_SkipsAlreadyConfigured(t *testing.T) {
	stub := &stubProvider{
		name:      "brew",
		available: true,
		installed: []provider.InstalledTool{
			installedTool("git", "2.43.0", "brew"),
			installedTool("ripgrep", "14.1.0", "brew"),
		},
	}
	a, cfgPath := newImportApp(t, stub)

	// Pre-populate config with git already declared.
	existing := &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("git", "brew")),
		Groups: []*config.GroupConfig{testHostToolGroup("git")},
	}
	if err := saveAppConfig(t, cfgPath, existing); err != nil {
		t.Fatalf("saving existing config: %v", err)
	}

	result, err := a.Import(context.Background(), app.ImportOptions{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if len(result.Added) != 1 {
		t.Errorf("Added = %d, want 1 (only ripgrep)", len(result.Added))
	}
	if result.Added[0].Name != "ripgrep" {
		t.Errorf("Added[0].Name = %q, want ripgrep", result.Added[0].Name)
	}
	if len(result.Skipped) != 1 {
		t.Errorf("Skipped = %d, want 1 (git)", len(result.Skipped))
	}
}

func TestImport_DryRunDoesNotWriteConfig(t *testing.T) {
	stub := &stubProvider{
		name:      "brew",
		available: true,
		installed: []provider.InstalledTool{
			installedTool("git", "2.43.0", "brew"),
		},
	}
	a, cfgPath := newImportApp(t, stub)

	result, err := a.Import(context.Background(), app.ImportOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(result.Added) != 1 {
		t.Errorf("Added = %d, want 1", len(result.Added))
	}

	// Config file must not exist — dry run should not write it.
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Error("dry run should not create config file")
	}
}

func TestImport_ProviderFilter(t *testing.T) {
	brew := &stubProvider{
		name:      "brew",
		available: true,
		installed: []provider.InstalledTool{installedTool("git", "2.43.0", "brew")},
	}
	npm := &stubProvider{
		name:      "npm",
		available: true,
		installed: []provider.InstalledTool{installedTool("typescript", "5.4.0", "npm")},
	}
	a, cfgPath := newImportApp(t, brew, npm)

	result, err := a.Import(context.Background(), app.ImportOptions{Provider: "brew"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(result.Added) != 1 {
		t.Errorf("Added = %d, want 1 (brew only)", len(result.Added))
	}
	if result.Added[0].Provider != "brew" {
		t.Errorf("Added[0].Provider = %q, want brew", result.Added[0].Provider)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	tools := toolsFromConfig(cfg)
	if len(tools) != 1 {
		t.Errorf("config tools = %d, want 1", len(tools))
	}
}

func TestImport_UnavailableProviderSkipped(t *testing.T) {
	stub := &stubProvider{
		name:      "brew",
		available: false, // not on this system
		installed: []provider.InstalledTool{installedTool("git", "2.43.0", "brew")},
	}
	a, _ := newImportApp(t, stub)

	result, err := a.Import(context.Background(), app.ImportOptions{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(result.Added) != 0 {
		t.Errorf("Added = %d, want 0 (provider unavailable)", len(result.Added))
	}
}

func TestImport_EcosystemProvidersWithRegisteredDelegatesSkipped(t *testing.T) {
	// system is the provider family whose ListInstalled output is a subset of
	// its concrete delegates. It must be skipped during a full import to
	// prevent duplicate config entries.
	brewStub := &stubProvider{
		name:      "brew",
		available: true,
		installed: []provider.InstalledTool{installedTool("git", "2.43.0", "brew")},
	}
	systemStub := &stubProvider{
		name:      "system",
		available: true,
		installed: []provider.InstalledTool{installedTool("git", "2.43.0", "system")},
	}
	a, cfgPath := newImportApp(t, brewStub, systemStub)

	result, err := a.Import(context.Background(), app.ImportOptions{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	// Only the concrete provider (brew) should contribute; system is skipped.
	if len(result.Added) != 1 {
		t.Errorf("Added = %d, want 1 (brew only, system provider family skipped)", len(result.Added))
	}
	if len(result.Added) > 0 && result.Added[0].Provider != "brew" {
		t.Errorf("Added[0].Provider = %q, want brew", result.Added[0].Provider)
	}

	cfg, _ := config.Load(cfgPath)
	if tools := toolsFromConfig(cfg); len(tools) != 1 {
		t.Errorf("config tools = %d, want 1 (no duplicate)", len(tools))
	}
}

func TestImport_ResolvedDefaultConcreteDoesNotWriteInstallWith(t *testing.T) {
	brewStub := &stubProvider{
		name:      "brew",
		available: true,
		installed: []provider.InstalledTool{installedTool("git", "2.43.0", "brew")},
	}
	systemStub := &lifecycleProvider{
		stubProvider: stubProvider{name: "system", available: true},
		resolvedName: "brew",
	}
	a, cfgPath := newImportApp(t, brewStub, systemStub)

	result, err := a.Import(context.Background(), app.ImportOptions{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(result.Added) != 1 {
		t.Fatalf("Added = %d, want 1", len(result.Added))
	}
	if result.Added[0].Provider != "brew" {
		t.Fatalf("Added[0].Provider = %q, want brew", result.Added[0].Provider)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	spec := cfg.Tools["git"]
	if len(spec.Providers) != 1 || spec.Providers[0].Provider != "brew" || spec.Provider != "" || spec.InstallWith != "" {
		t.Fatalf("git spec = %+v, want brew provider entry without legacy fields", spec)
	}
}

func TestImport_EcosystemProviderExplicitFilter(t *testing.T) {
	// When the user explicitly requests a provider family via --provider,
	// the skip set is bypassed and the provider family IS iterated.
	system := &stubProvider{
		name:      "system",
		available: true,
		installed: []provider.InstalledTool{installedTool("git", "2.43.0", "system")},
	}
	a, _ := newImportApp(t, system)

	result, err := a.Import(context.Background(), app.ImportOptions{Provider: "system"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(result.Added) != 0 {
		t.Errorf("Added = %d, want 0 (provider families are skipped)", len(result.Added))
	}
}

func TestImport_TapQualifiedPackageSkipped(t *testing.T) {
	// A tool with package = "homebrew/tap/tool" must be recognised as already
	// configured when ListInstalled returns just the plain name "tool".
	stub := &stubProvider{
		name:      "brew",
		available: true,
		installed: []provider.InstalledTool{installedTool("mytool", "1.0.0", "brew")},
	}
	a, cfgPath := newImportApp(t, stub)

	// Pre-populate config with tap-qualified entry.
	existing := &config.RootConfig{
		Tools:  logicalToolSpecs(logicalToolPackage("mytool", "brew", "homebrew/tap/mytool")),
		Groups: []*config.GroupConfig{testHostToolGroup("mytool")},
	}
	if err := saveAppConfig(t, cfgPath, existing); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	result, err := a.Import(context.Background(), app.ImportOptions{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(result.Added) != 0 {
		t.Errorf("Added = %d, want 0 (tap-qualified entry already configured by name)", len(result.Added))
	}
	if len(result.Skipped) != 1 {
		t.Errorf("Skipped = %d, want 1", len(result.Skipped))
	}
}

func TestImport_NoDuplicatesAcrossRuns(t *testing.T) {
	stub := &stubProvider{
		name:      "brew",
		available: true,
		installed: []provider.InstalledTool{
			installedTool("git", "2.43.0", "brew"),
		},
	}
	a, cfgPath := newImportApp(t, stub)

	// First import.
	if _, err := a.Import(context.Background(), app.ImportOptions{}); err != nil {
		t.Fatalf("first Import: %v", err)
	}
	// Second import — git is now in config, should be skipped.
	result, err := a.Import(context.Background(), app.ImportOptions{})
	if err != nil {
		t.Fatalf("second Import: %v", err)
	}
	if len(result.Added) != 0 {
		t.Errorf("second run Added = %d, want 0", len(result.Added))
	}
	if len(result.Skipped) != 1 {
		t.Errorf("second run Skipped = %d, want 1", len(result.Skipped))
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	tools := toolsFromConfig(cfg)
	if len(tools) != 1 {
		t.Errorf("config tools = %d after two runs, want 1 (no duplicates)", len(tools))
	}
}

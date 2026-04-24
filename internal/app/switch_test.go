package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
)

// toolsFromConfig extracts the resolved flat tools list from the base group of a RootConfig.
func toolsFromConfig(cfg *config.RootConfig) []config.ToolEntry {
	for _, g := range cfg.Groups {
		if g.IsBase() {
			return materializeTestTools(cfg, g.Tools)
		}
	}
	if len(cfg.Groups) > 0 {
		return materializeTestTools(cfg, cfg.Groups[0].Tools)
	}
	return nil
}

func materializeTestTools(cfg *config.RootConfig, memberships []config.ToolEntry) []config.ToolEntry {
	tools := make([]config.ToolEntry, 0, len(memberships))
	for _, membership := range memberships {
		spec, ok := cfg.Tools[membership.Name]
		if !ok {
			tools = append(tools, membership)
			continue
		}
		install := spec.DefaultInstallSpec()
		tools = append(tools, config.ToolEntry{
			Name:        membership.Name,
			Provider:    install.Provider,
			Package:     install.Package,
			InstallWith: install.InstallWith,
			Options:     install.Options,
			Ignore:      spec.Ignore,
		})
	}
	return tools
}

func TestSwitch_Success(t *testing.T) {
	brew := &stubProvider{name: "brew", available: true}
	pip := &stubProvider{name: "pip", available: true}
	a, cfgPath := newImportApp(t, brew, pip)

	cfg := &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("black", "brew")),
		Groups: []*config.GroupConfig{{Tools: groupTools("black")}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	result, err := a.Switch(context.Background(), "black", "brew", "pip")
	if err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if result.Name != "black" || result.FromProvider != "brew" || result.ToProvider != "pip" {
		t.Errorf("result = %+v", result)
	}
	if result.UninstallWarning != nil {
		t.Errorf("unexpected uninstall warning: %v", result.UninstallWarning)
	}

	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	tools := toolsFromConfig(updated)
	if len(tools) != 1 || tools[0].Provider != "python" || tools[0].InstallWith != "pip" {
		t.Errorf("config tool = %+v after switch, want provider python install_with pip", tools[0])
	}
}

func TestSwitch_UpdatesDB(t *testing.T) {
	brew := &stubProvider{name: "brew", available: true}
	pip := &stubProvider{name: "pip", available: true}
	a, cfgPath := newImportApp(t, brew, pip)

	cfg := &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("black", "brew")),
		Groups: []*config.GroupConfig{{Tools: groupTools("black")}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	// Pre-install under brew so there's a DB entry to remove.
	if err := a.Install(context.Background(), "black", "brew"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if _, err := a.Switch(context.Background(), "black", "brew", "pip"); err != nil {
		t.Fatalf("Switch: %v", err)
	}

	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	// Old brew entry gone, new logical python entry present.
	for _, t2 := range tools {
		if t2.Provider == "brew" {
			t.Errorf("old brew entry still in DB: %+v", t2)
		}
	}
	found := false
	for _, t2 := range tools {
		if t2.Provider == "python" && t2.Name == "black" && t2.InstalledWith == "pip" {
			found = true
		}
	}
	if !found {
		t.Error("python/pip entry for black not found in DB after switch")
	}
}

func TestSwitch_PersistsResolvedConcreteOwner(t *testing.T) {
	brew := &stubProvider{name: "brew", available: true}
	system := &lifecycleProvider{
		stubProvider: stubProvider{name: "system", available: true},
		resolvedName: "apt",
		installed:    true,
		version:      "14.1.0",
	}
	a, cfgPath := newImportApp(t, brew, system)

	cfg := &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{Tools: groupTools("ripgrep")}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if _, err := a.Switch(context.Background(), "ripgrep", "brew", "system"); err != nil {
		t.Fatalf("Switch: %v", err)
	}

	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1: %+v", len(tools), tools)
	}
	if tools[0].Provider != "system" || tools[0].InstalledWith != "apt" {
		t.Fatalf("DB tool = provider %q installed_with %q, want system/apt", tools[0].Provider, tools[0].InstalledWith)
	}
}

func TestSwitch_VerificationFailureDoesNotRewriteConfigOrCache(t *testing.T) {
	brew := &stubProvider{name: "brew", available: true}
	system := &lifecycleProvider{
		stubProvider: stubProvider{name: "system", available: true},
		resolvedName: "apt",
		installed:    false,
	}
	a, cfgPath := newImportApp(t, brew, system)

	cfg := &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{Tools: groupTools("ripgrep")}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	_, err := a.Switch(context.Background(), "ripgrep", "brew", "system")
	if err == nil {
		t.Fatal("Switch returned nil, want verification failure")
	}
	if !strings.Contains(err.Error(), "not installed after install") {
		t.Fatalf("Switch error = %q, want verification failure", err.Error())
	}
	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	tools := toolsFromConfig(updated)
	if len(tools) != 1 || tools[0].Provider != "system" || tools[0].InstallWith != "brew" {
		t.Fatalf("config tool = %+v, want original system via brew", tools)
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

func TestSwitch_UninstallsConfiguredSourceManager(t *testing.T) {
	python := &envCleanerProvider{stubProvider: stubProvider{name: "python", available: true}}
	brew := &stubProvider{name: "brew", available: true}
	a, cfgPath := newImportApp(t, python, brew)

	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"black": {Provider: "python", InstallWith: "pip3"},
		},
		Groups: []*config.GroupConfig{{Tools: groupTools("black")}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if _, err := a.Switch(context.Background(), "black", "python", "brew"); err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if len(python.uninstallFromBinaries) != 1 || python.uninstallFromBinaries[0] != "pip3" {
		t.Fatalf("UninstallFrom managers = %v, want [pip3]", python.uninstallFromBinaries)
	}
}

func TestSwitch_ReturnsDBUpdateError(t *testing.T) {
	brew := &stubProvider{name: "brew", available: true}
	pip := &stubProvider{name: "pip", available: true}
	a, cfgPath := newImportApp(t, brew, pip)

	cfg := &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("black", "brew")),
		Groups: []*config.GroupConfig{{Tools: groupTools("black")}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.DB().Close(); err != nil {
		t.Fatalf("closing DB: %v", err)
	}

	_, err := a.Switch(context.Background(), "black", "brew", "pip")
	if err == nil {
		t.Fatal("Switch returned nil, want DB update error")
	}
}

func TestSwitch_UnknownFromProvider(t *testing.T) {
	a, _ := newImportApp(t)
	_, err := a.Switch(context.Background(), "black", "unknown", "pip")
	if err == nil {
		t.Error("expected error for unknown --from provider, got nil")
	}
}

func TestSwitch_UnknownToProvider(t *testing.T) {
	brew := &stubProvider{name: "brew", available: true}
	a, _ := newImportApp(t, brew)
	_, err := a.Switch(context.Background(), "black", "brew", "unknown")
	if err == nil {
		t.Error("expected error for unknown --to provider, got nil")
	}
}

func TestSwitch_ToolNotInConfig(t *testing.T) {
	brew := &stubProvider{name: "brew", available: true}
	pip := &stubProvider{name: "pip", available: true}
	a, cfgPath := newImportApp(t, brew, pip)

	cfg := &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{Tools: groupTools("ripgrep")}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	_, err := a.Switch(context.Background(), "black", "brew", "pip")
	if err == nil {
		t.Error("expected error when tool not in config, got nil")
	}
}

func TestSwitch_PreservesOtherTools(t *testing.T) {
	brew := &stubProvider{name: "brew", available: true}
	pip := &stubProvider{name: "pip", available: true}
	a, cfgPath := newImportApp(t, brew, pip)

	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("black", "brew"),
			logicalTool("ripgrep", "brew"),
		),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("black", "ripgrep"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if _, err := a.Switch(context.Background(), "black", "brew", "pip"); err != nil {
		t.Fatalf("Switch: %v", err)
	}

	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	tools := toolsFromConfig(updated)
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools after switch, got %d", len(tools))
	}
	for _, e := range tools {
		if e.Name == "black" && (e.Provider != "python" || e.InstallWith != "pip") {
			t.Errorf("black tool = %+v, want python via pip", e)
		}
		if e.Name == "ripgrep" && (e.Provider != "system" || e.InstallWith != "brew") {
			t.Errorf("ripgrep tool = %+v, want system via brew (unchanged)", e)
		}
	}
}

// ─── MigrateInstallation ──────────────────────────────────────────────────────

// envCleanerProvider extends stubProvider with the oldEnvCleaner interface
// (UninstallFrom). The app package detects this via type assertion.
type envCleanerProvider struct {
	stubProvider
	installWithManagers   []string
	uninstallFromBinaries []string
	uninstallFromTools    []provider.Tool
	uninstallFromErr      error
}

func (e *envCleanerProvider) InstallWithManager(_ context.Context, _ provider.Tool, manager string) error {
	e.installWithManagers = append(e.installWithManagers, manager)
	return nil
}

func (e *envCleanerProvider) IsInstalledWithManager(_ context.Context, _ provider.Tool, manager string) (bool, string, error) {
	return true, manager + "-version", nil
}

func (e *envCleanerProvider) UninstallFrom(_ context.Context, tool provider.Tool, binary string) error {
	e.uninstallFromBinaries = append(e.uninstallFromBinaries, binary)
	e.uninstallFromTools = append(e.uninstallFromTools, tool)
	return e.uninstallFromErr
}

// TestMigrateInstallation_InstalledWithRegistered verifies that when installedWith
// is a known registered provider, from=installedWith and Switch(installedWith→configProv)
// runs — cross-provider migration, no oldEnvCleaner cleanup needed.
func TestMigrateInstallation_InstalledWithRegistered(t *testing.T) {
	pip := &stubProvider{name: "pip", available: true}
	python := &stubProvider{name: "python", available: true}
	brew := &stubProvider{name: "brew", available: true}
	a, cfgPath := newImportApp(t, pip, python, brew)

	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("black", "pip")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("black"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	// Tool is in config under "pip" but found installed via "brew" (wrong provider).
	result, err := a.MigrateInstallation(context.Background(), "black", "brew", "python")
	if err != nil {
		t.Fatalf("MigrateInstallation: %v", err)
	}
	if result.UninstallWarning != nil {
		t.Errorf("unexpected UninstallWarning: %v", result.UninstallWarning)
	}

	// Config provider should be unchanged (python via pip) since we migrated back to configured ownership.
	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	tools := toolsFromConfig(updated)
	if len(tools) != 1 || tools[0].Provider != "python" || tools[0].InstallWith != "pip" {
		t.Errorf("tool = %+v after migration, want python via pip", tools[0])
	}
}

func TestMigrateInstallation_RegisteredWrongProviderPersistsResolvedConcreteOwner(t *testing.T) {
	brew := &stubProvider{name: "brew", available: true}
	system := &lifecycleProvider{
		stubProvider: stubProvider{name: "system", available: true},
		resolvedName: "apt",
		installed:    true,
		version:      "14.1.0",
	}
	a, cfgPath := newImportApp(t, brew, system)

	cfg := &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("ripgrep", "system")),
		Groups: []*config.GroupConfig{{Tools: groupTools("ripgrep")}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if _, err := a.MigrateInstallation(context.Background(), "ripgrep", "brew", "system"); err != nil {
		t.Fatalf("MigrateInstallation: %v", err)
	}

	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1: %+v", len(tools), tools)
	}
	if tools[0].Provider != "system" || tools[0].InstalledWith != "apt" {
		t.Fatalf("DB tool = provider %q installed_with %q, want system/apt", tools[0].Provider, tools[0].InstalledWith)
	}
}

func TestMigrateInstallation_VerificationFailureDoesNotUninstallOldProvider(t *testing.T) {
	brew := &lifecycleProvider{
		stubProvider: stubProvider{name: "brew", available: true},
		installed:    true,
	}
	system := &lifecycleProvider{
		stubProvider: stubProvider{name: "system", available: true},
		resolvedName: "apt",
		installed:    false,
	}
	a, cfgPath := newImportApp(t, brew, system)

	cfg := &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("ripgrep", "system")),
		Groups: []*config.GroupConfig{{Tools: groupTools("ripgrep")}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	_, err := a.MigrateInstallation(context.Background(), "ripgrep", "brew", "system")
	if err == nil {
		t.Fatal("MigrateInstallation returned nil, want verification failure")
	}
	if !strings.Contains(err.Error(), "not installed after install") {
		t.Fatalf("MigrateInstallation error = %q, want verification failure", err.Error())
	}
	if len(brew.uninstalled) != 0 {
		t.Fatalf("brew uninstalled = %+v, want old provider left untouched after verification failure", brew.uninstalled)
	}
}

// TestMigrateInstallation_InstalledWithUnregistered verifies the cross-backend
// path: installedWith ("uv") is not a registered provider, so from=configProv
// ("python") and Switch runs python→python (install only). Afterwards the provider's
// UninstallFrom is called with the concrete binary ("uv") to clean the old env.
func TestMigrateInstallation_InstalledWithUnregistered_CallsCleanup(t *testing.T) {
	python := &envCleanerProvider{
		stubProvider: stubProvider{name: "python", available: true},
	}
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

	// installedWith="uv" is not a registered provider → from=configProv="python".
	result, err := a.MigrateInstallation(context.Background(), "black", "uv", "python")
	if err != nil {
		t.Fatalf("MigrateInstallation: %v", err)
	}
	if result.UninstallWarning != nil {
		t.Errorf("unexpected UninstallWarning: %v", result.UninstallWarning)
	}
	if len(python.uninstallFromBinaries) != 1 || python.uninstallFromBinaries[0] != "uv" {
		t.Errorf("UninstallFrom binaries = %v, want [uv]", python.uninstallFromBinaries)
	}

	dbTools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(dbTools) != 1 {
		t.Fatalf("got %d DB tools, want 1", len(dbTools))
	}
	if dbTools[0].Provider != "python" || dbTools[0].Name != "black" {
		t.Fatalf("DB tool = %s/%s, want python/black", dbTools[0].Provider, dbTools[0].Name)
	}
}

// TestMigrateInstallation_InstalledWithUnregistered_NoEnvCleaner verifies that
// when installedWith is unregistered but the provider does not implement
// oldEnvCleaner, MigrateInstallation succeeds without panicking or erroring.
func TestMigrateInstallation_InstalledWithUnregistered_NoEnvCleaner(t *testing.T) {
	python := &stubProvider{name: "python", available: true}
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

	// installedWith="pip3" is not registered; python doesn't implement oldEnvCleaner.
	_, err := a.MigrateInstallation(context.Background(), "black", "pip3", "python")
	if err != nil {
		t.Fatalf("MigrateInstallation: %v", err)
	}
}

// TestMigrateInstallation_CleanupError verifies that an UninstallFrom failure
// is surfaced as UninstallWarning (not a hard error).
func TestMigrateInstallation_CleanupError(t *testing.T) {
	cleanErr := errors.New("env removal failed")
	python := &envCleanerProvider{
		stubProvider:     stubProvider{name: "python", available: true},
		uninstallFromErr: cleanErr,
	}
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

	result, err := a.MigrateInstallation(context.Background(), "black", "uv", "python")
	if err != nil {
		t.Fatalf("MigrateInstallation returned hard error: %v (expected soft warning)", err)
	}
	if result.UninstallWarning == nil {
		t.Error("expected UninstallWarning, got nil")
	}
	if result.UninstallWarning != cleanErr {
		t.Errorf("UninstallWarning = %v, want %v", result.UninstallWarning, cleanErr)
	}
}

func TestMigrateInstallation_CleanupUsesPackageAlias(t *testing.T) {
	python := &envCleanerProvider{
		stubProvider: stubProvider{name: "python", available: true},
	}
	a, cfgPath := newImportApp(t, python)

	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalToolPackage("black", "python", "black-cli")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("black"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if _, err := a.MigrateInstallation(context.Background(), "black", "uv", "python"); err != nil {
		t.Fatalf("MigrateInstallation: %v", err)
	}
	if len(python.uninstallFromTools) != 1 {
		t.Fatalf("UninstallFrom tools = %v, want one cleanup call", python.uninstallFromTools)
	}
	if got := python.uninstallFromTools[0].Package; got != "black-cli" {
		t.Fatalf("cleanup package = %q, want black-cli", got)
	}
}

func TestMigrateInstallation_UnregisteredPreservesConfiguredInstallWith(t *testing.T) {
	python := &envCleanerProvider{
		stubProvider: stubProvider{name: "python", available: true},
	}
	a, cfgPath := newImportApp(t, python)

	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalFixtureTool{Name: "black", Provider: "python", InstallWith: "pip3"}),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("black"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	result, err := a.MigrateInstallation(context.Background(), "black", "uv", "python")
	if err != nil {
		t.Fatalf("MigrateInstallation: %v", err)
	}
	if result.UninstallWarning != nil {
		t.Fatalf("unexpected uninstall warning: %v", result.UninstallWarning)
	}
	if len(python.installWithManagers) != 1 || python.installWithManagers[0] != "pip3" {
		t.Fatalf("InstallWithManager calls = %v, want [pip3]", python.installWithManagers)
	}
	if len(python.uninstallFromBinaries) != 1 || python.uninstallFromBinaries[0] != "uv" {
		t.Fatalf("UninstallFrom binaries = %v, want [uv]", python.uninstallFromBinaries)
	}

	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	tools := toolsFromConfig(updated)
	if len(tools) != 1 || tools[0].Provider != "python" || tools[0].InstallWith != "pip3" {
		t.Fatalf("config tool = %+v, want python via pip3", tools)
	}
	dbTools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(dbTools) != 1 || dbTools[0].InstalledWith != "pip3" {
		t.Fatalf("DB tools = %+v, want installed_with pip3", dbTools)
	}
}

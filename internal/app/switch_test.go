package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

// toolsFromConfig extracts the resolved flat tools list from the first regular
// group of a RootConfig, falling back to the first group.
func toolsFromConfig(cfg *config.RootConfig) []config.ToolEntry {
	for _, g := range cfg.Groups {
		if !g.IsHost() {
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
		Groups: []*config.GroupConfig{testHostToolGroup("black")},
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
	if len(tools) != 1 || tools[0].Provider != "pip" || tools[0].InstallWith != "" {
		t.Errorf("config tool = %+v after switch, want provider pip", tools[0])
	}
}

func TestSwitchWithStateReturnsUpdatedTools(t *testing.T) {
	ctx := context.Background()
	brew := &stubProvider{name: "brew", available: true}
	pip := &describingProvider{stubProvider: stubProvider{name: "pip", available: true}}
	a, cfgPath := newImportApp(t, brew, pip)

	cfg := &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("black", "brew")),
		Groups: []*config.GroupConfig{testHostToolGroup("black")},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	result, err := a.SwitchWithState(ctx, "black", "brew", "pip")
	if err != nil {
		t.Fatalf("SwitchWithState: %v", err)
	}
	if result.Result == nil || result.Result.FromProvider != "brew" || result.Result.ToProvider != "pip" {
		t.Fatalf("Result = %+v, want brew -> pip", result.Result)
	}
	if len(result.Tools) != 1 || result.Tools[0].Name != "black" {
		t.Fatalf("Tools = %v, want black only", toolNames(result.Tools))
	}
	tool := result.Tools[0]
	if tool.Provider != "pip" || tool.InstalledWith != "pip" {
		t.Fatalf("tool provider = %s/%s, want pip/pip", tool.Provider, tool.InstalledWith)
	}
	if !tool.Description.Valid || tool.Description.String != "description of black" {
		t.Fatalf("description = %+v, want refreshed description", tool.Description)
	}
}

func TestFirstApplicableProviderSolutionSelectsSwitchProviderTarget(t *testing.T) {
	actionErr := &provider.ActionError{Solutions: []provider.ErrorSolution{
		{Label: "Run manually", Command: "omni switch black --from python --to uv"},
		{Action: provider.ErrorSolutionActionSwitchProvider},
		{Action: provider.ErrorSolutionActionSwitchProvider, TargetProvider: "uv"},
	}}

	solution, ok := app.FirstApplicableProviderSolution(actionErr)
	if !ok {
		t.Fatal("FirstApplicableProviderSolution ok = false, want true")
	}
	if solution.TargetProvider != "uv" {
		t.Fatalf("TargetProvider = %q, want uv", solution.TargetProvider)
	}
	if idx := app.FirstApplicableProviderSolutionIndex(actionErr); idx != 2 {
		t.Fatalf("FirstApplicableProviderSolutionIndex = %d, want 2", idx)
	}
}

func TestFirstApplicableProviderSolutionRejectsMissingSwitchTarget(t *testing.T) {
	for _, actionErr := range []*provider.ActionError{
		nil,
		{},
		{Solutions: []provider.ErrorSolution{
			{Action: provider.ErrorSolutionActionSwitchProvider},
			{Action: "run-command", TargetProvider: "brew"},
		}},
	} {
		if solution, ok := app.FirstApplicableProviderSolution(actionErr); ok {
			t.Fatalf("FirstApplicableProviderSolution(%+v) = %+v, true; want no solution", actionErr, solution)
		}
		if idx := app.FirstApplicableProviderSolutionIndex(actionErr); idx != -1 {
			t.Fatalf("FirstApplicableProviderSolutionIndex(%+v) = %d, want -1", actionErr, idx)
		}
	}
}

func TestApplyProviderSolutionWithStateSwitchesTargetProvider(t *testing.T) {
	ctx := context.Background()
	brew := &stubProvider{name: "brew", available: true}
	pip := &describingProvider{stubProvider: stubProvider{name: "pip", available: true}}
	a, cfgPath := newImportApp(t, brew, pip)

	cfg := &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("black", "brew")),
		Groups: []*config.GroupConfig{testHostToolGroup("black")},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	result, err := a.ApplyProviderSolutionWithState(ctx, "black", "brew", provider.ErrorSolution{
		Action:         provider.ErrorSolutionActionSwitchProvider,
		TargetProvider: "pip",
	})
	if err != nil {
		t.Fatalf("ApplyProviderSolutionWithState: %v", err)
	}
	if result.Result == nil || result.Result.FromProvider != "brew" || result.Result.ToProvider != "pip" {
		t.Fatalf("Result = %+v, want brew -> pip", result.Result)
	}
	if len(result.Tools) != 1 || result.Tools[0].Provider != "pip" || result.Tools[0].InstalledWith != "pip" {
		t.Fatalf("Tools = %+v, want pip/pip", result.Tools)
	}
}

func TestApplyProviderSolutionWithStateRejectsMissingTarget(t *testing.T) {
	a, _ := newImportApp(t, &stubProvider{name: "brew", available: true})

	_, err := a.ApplyProviderSolutionWithState(context.Background(), "black", "brew", provider.ErrorSolution{})
	if err == nil {
		t.Fatal("ApplyProviderSolutionWithState error = nil, want missing target error")
	}
	if !strings.Contains(err.Error(), "missing target provider") {
		t.Fatalf("error = %q, want missing target provider", err)
	}
}

type uninstallFailProvider struct {
	stubProvider
	err error
}

func (p *uninstallFailProvider) Uninstall(_ context.Context, _ provider.Tool) error {
	return p.err
}

func TestSwitch_PreservesUninstallWarning(t *testing.T) {
	uninstallErr := errors.New("old provider cleanup failed")
	brew := &uninstallFailProvider{stubProvider: stubProvider{name: "brew", available: true}, err: uninstallErr}
	pip := &stubProvider{name: "pip", available: true}
	a, cfgPath := newImportApp(t, brew, pip)

	cfg := &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("black", "brew")),
		Groups: []*config.GroupConfig{testHostToolGroup("black")},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	result, err := a.Switch(context.Background(), "black", "brew", "pip")
	if err != nil {
		t.Fatalf("Switch returned hard error: %v", err)
	}
	if result.UninstallWarning != uninstallErr {
		t.Fatalf("UninstallWarning = %v, want %v", result.UninstallWarning, uninstallErr)
	}
	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	tools := toolsFromConfig(updated)
	if len(tools) != 1 || tools[0].Provider != "pip" || tools[0].InstallWith != "" {
		t.Fatalf("config tool = %+v, want switched despite cleanup warning", tools)
	}
}

func TestSwitch_UpdatesDB(t *testing.T) {
	brew := &stubProvider{name: "brew", available: true}
	pip := &stubProvider{name: "pip", available: true}
	a, cfgPath := newImportApp(t, brew, pip)

	cfg := &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("black", "brew")),
		Groups: []*config.GroupConfig{testHostToolGroup("black")},
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
	// Old brew entry gone, new concrete pip entry present.
	for _, t2 := range tools {
		if t2.Provider == "brew" {
			t.Errorf("old brew entry still in DB: %+v", t2)
		}
	}
	found := false
	for _, t2 := range tools {
		if t2.Provider == "pip" && t2.Name == "black" && t2.InstalledWith == "pip" {
			found = true
		}
	}
	if !found {
		t.Error("pip entry for black not found in DB after switch")
	}
}

func TestSwitch_PersistsResolvedConcreteOwner(t *testing.T) {
	brew := &stubProvider{name: "brew", available: true}
	apt := &lifecycleProvider{
		stubProvider: stubProvider{name: "apt", available: true},
		installed:    true,
		version:      "14.1.0",
	}
	a, cfgPath := newImportApp(t, brew, apt)

	cfg := &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{testHostToolGroup("ripgrep")},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if _, err := a.Switch(context.Background(), "ripgrep", "brew", "apt"); err != nil {
		t.Fatalf("Switch: %v", err)
	}

	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1: %+v", len(tools), tools)
	}
	if tools[0].Provider != "apt" || tools[0].InstalledWith != "" {
		t.Fatalf("DB tool = provider %q installed_with %q, want apt", tools[0].Provider, tools[0].InstalledWith)
	}
}

func TestSwitch_VerificationFailureDoesNotRewriteConfigOrCache(t *testing.T) {
	brew := &stubProvider{name: "brew", available: true}
	apt := &lifecycleProvider{
		stubProvider: stubProvider{name: "apt", available: true},
		installed:    false,
	}
	a, cfgPath := newImportApp(t, brew, apt)

	cfg := &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{testHostToolGroup("ripgrep")},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	_, err := a.Switch(context.Background(), "ripgrep", "brew", "apt")
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
	if len(tools) != 1 || tools[0].Provider != "brew" || tools[0].InstallWith != "" {
		t.Fatalf("config tool = %+v, want original brew", tools)
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

func TestSwitch_ReturnsDBUpdateError(t *testing.T) {
	brew := &stubProvider{name: "brew", available: true}
	pip := &stubProvider{name: "pip", available: true}
	a, cfgPath := newImportApp(t, brew, pip)

	cfg := &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("black", "brew")),
		Groups: []*config.GroupConfig{testHostToolGroup("black")},
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
		Groups: []*config.GroupConfig{testHostToolGroup("ripgrep")},
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
		if e.Name == "black" && (e.Provider != "pip" || e.InstallWith != "") {
			t.Errorf("black tool = %+v, want pip", e)
		}
		if e.Name == "ripgrep" && (e.Provider != "brew" || e.InstallWith != "") {
			t.Errorf("ripgrep tool = %+v, want brew (unchanged)", e)
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
	brew := &stubProvider{name: "brew", available: true}
	a, cfgPath := newImportApp(t, pip, brew)

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
	result, err := a.MigrateInstallation(context.Background(), "black", "brew", "pip")
	if err != nil {
		t.Fatalf("MigrateInstallation: %v", err)
	}
	if result.UninstallWarning != nil {
		t.Errorf("unexpected UninstallWarning: %v", result.UninstallWarning)
	}

	// Config provider should be unchanged since we migrated back to configured ownership.
	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	tools := toolsFromConfig(updated)
	if len(tools) != 1 || tools[0].Provider != "pip" || tools[0].InstallWith != "" {
		t.Errorf("tool = %+v after migration, want pip", tools[0])
	}
}

func TestMigrateInstallationWithStateReturnsUpdatedTools(t *testing.T) {
	ctx := context.Background()
	pip := &describingProvider{stubProvider: stubProvider{name: "pip", available: true}}
	brew := &stubProvider{name: "brew", available: true}
	a, cfgPath := newImportApp(t, pip, brew)

	cfg := &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("black", "pip")),
		Groups: []*config.GroupConfig{testHostToolGroup("black")},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	result, err := a.MigrateInstallationWithState(ctx, "black", "brew", "pip")
	if err != nil {
		t.Fatalf("MigrateInstallationWithState: %v", err)
	}
	if result.Result == nil || result.Result.FromProvider != "brew" || result.Result.ToProvider != "pip" {
		t.Fatalf("Result = %+v, want brew -> pip", result.Result)
	}
	if len(result.Tools) != 1 || result.Tools[0].Name != "black" {
		t.Fatalf("Tools = %v, want black only", toolNames(result.Tools))
	}
	tool := result.Tools[0]
	if tool.Provider != "pip" || tool.InstalledWith != "pip" {
		t.Fatalf("tool provider = %s/%s, want pip/pip", tool.Provider, tool.InstalledWith)
	}
	if !tool.Description.Valid || tool.Description.String != "description of black" {
		t.Fatalf("description = %+v, want refreshed description", tool.Description)
	}
}

func TestMigrateInstallation_RegisteredWrongProviderPersistsResolvedConcreteOwner(t *testing.T) {
	brew := &stubProvider{name: "brew", available: true}
	apt := &lifecycleProvider{
		stubProvider: stubProvider{name: "apt", available: true},
		installed:    true,
		version:      "14.1.0",
	}
	a, cfgPath := newImportApp(t, brew, apt)

	cfg := &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("ripgrep", "apt")),
		Groups: []*config.GroupConfig{testHostToolGroup("ripgrep")},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if _, err := a.MigrateInstallation(context.Background(), "ripgrep", "brew", "apt"); err != nil {
		t.Fatalf("MigrateInstallation: %v", err)
	}

	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1: %+v", len(tools), tools)
	}
	if tools[0].Provider != "apt" || tools[0].InstalledWith != "" {
		t.Fatalf("DB tool = provider %q installed_with %q, want apt", tools[0].Provider, tools[0].InstalledWith)
	}
}

func TestMigrateInstallation_UnregisteredBackendCleansOldEnvironment(t *testing.T) {
	pip := &envCleanerProvider{stubProvider: stubProvider{name: "pip", available: true}}
	a, cfgPath := newImportApp(t, pip)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("black", "pip")),
		Groups: []*config.GroupConfig{testHostToolGroup("black")},
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	result, err := a.MigrateInstallation(context.Background(), "black", "uv", "pip")
	if err != nil {
		t.Fatalf("MigrateInstallation: %v", err)
	}
	if result.FromProvider != "pip" || result.ToProvider != "pip" || result.Package != "black" {
		t.Fatalf("result = %+v, want pip -> pip for black", result)
	}
	if len(pip.installWithManagers) != 0 {
		t.Fatalf("install managers = %v, want direct pip install", pip.installWithManagers)
	}
	if len(pip.uninstallFromBinaries) != 1 || pip.uninstallFromBinaries[0] != "uv" {
		t.Fatalf("cleanup binaries = %v, want [uv]", pip.uninstallFromBinaries)
	}
	if len(pip.uninstallFromTools) != 1 || pip.uninstallFromTools[0].Package != "black" {
		t.Fatalf("cleanup tools = %+v, want black package cleanup", pip.uninstallFromTools)
	}

	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1: %+v", len(tools), tools)
	}
	if tools[0].Provider != "pip" || tools[0].InstalledWith != "pip" {
		t.Fatalf("DB tool = provider %q installed_with %q, want pip/pip", tools[0].Provider, tools[0].InstalledWith)
	}
}

func TestReinstallWithDefault_UsesConfiguredProviderAfterDefaultChange(t *testing.T) {
	pip := &lifecycleProvider{
		stubProvider: stubProvider{name: "pip", available: true},
		installed:    false,
		version:      "1.0.0",
	}
	uv := &installCaptureStub{
		stubProvider: stubProvider{name: "uv", available: true},
		version:      "1.0.0",
	}
	a, cfgPath := newImportApp(t, pip, uv)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"black": {Providers: []config.ToolInstallSpec{{Provider: "uv"}, {Provider: "pip"}}},
		},
		Groups: []*config.GroupConfig{testHostToolGroup("black")},
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.DB().Upsert(context.Background(), &database.ToolCache{
		Name:          "black",
		Provider:      "pip",
		Package:       "black",
		Installed:     true,
		InstalledWith: "pip",
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	result, err := a.ReinstallWithDefault(context.Background(), "black", "")
	if err != nil {
		t.Fatalf("ReinstallWithDefault: %v", err)
	}
	if result.ToProvider != "uv" {
		t.Fatalf("ToProvider = %q, want uv", result.ToProvider)
	}
	if len(uv.installed) != 1 || uv.installed[0].Name != "black" {
		t.Fatalf("uv installed = %+v, want [black]", uv.installed)
	}
}

func TestMigrateInstallation_VerificationFailureDoesNotUninstallOldProvider(t *testing.T) {
	brew := &lifecycleProvider{
		stubProvider: stubProvider{name: "brew", available: true},
		installed:    true,
	}
	apt := &lifecycleProvider{
		stubProvider: stubProvider{name: "apt", available: true},
		installed:    false,
	}
	a, cfgPath := newImportApp(t, brew, apt)

	cfg := &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("ripgrep", "apt")),
		Groups: []*config.GroupConfig{testHostToolGroup("ripgrep")},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	_, err := a.MigrateInstallation(context.Background(), "ripgrep", "brew", "apt")
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

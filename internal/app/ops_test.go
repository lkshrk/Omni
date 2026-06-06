package app_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
	syncprogress "github.com/lkshrk/omni/internal/sync"
)

// searchStub extends stubProvider with the optional Searcher interface.
type searchStub struct {
	stubProvider
	results []provider.SearchResult
}

func (s *searchStub) Search(_ context.Context, _ string) ([]provider.SearchResult, error) {
	return s.results, nil
}

type uninstallErrStub struct {
	stubProvider
}

func (s *uninstallErrStub) Uninstall(_ context.Context, _ provider.Tool) error {
	return errors.New("uninstall failed")
}

type uninstallCaptureStub struct {
	stubProvider
	uninstalled []provider.Tool
}

func (s *uninstallCaptureStub) Uninstall(_ context.Context, tool provider.Tool) error {
	s.uninstalled = append(s.uninstalled, tool)
	return nil
}

type managerUninstallCaptureStub struct {
	uninstallCaptureStub
	managerUninstalls []string
}

func (s *managerUninstallCaptureStub) UninstallFrom(_ context.Context, tool provider.Tool, manager string) error {
	s.managerUninstalls = append(s.managerUninstalls, manager)
	s.uninstalled = append(s.uninstalled, tool)
	return nil
}

type installCaptureStub struct {
	stubProvider
	installed []provider.Tool
	version   string
}

func (s *installCaptureStub) Install(_ context.Context, tool provider.Tool) error {
	s.installed = append(s.installed, tool)
	return nil
}

func (s *installCaptureStub) IsInstalled(_ context.Context, _ provider.Tool) (bool, string, error) {
	if s.version == "" {
		return true, "1.0.0", nil
	}
	return true, s.version, nil
}

type concreteInstallStub struct {
	installCaptureStub
	concreteName string
}

func (s *concreteInstallStub) ResolvedName(_ context.Context) (string, error) {
	return s.concreteName, nil
}

type installVerifyStub struct {
	stubProvider
	verifyInstalled bool
	verifyErr       error
}

func (s *installVerifyStub) Install(_ context.Context, _ provider.Tool) error {
	return nil
}

func (s *installVerifyStub) IsInstalled(_ context.Context, _ provider.Tool) (bool, string, error) {
	if s.verifyErr != nil {
		return false, "", s.verifyErr
	}
	return s.verifyInstalled, "1.0.0", nil
}

func hasTool(cfg *config.RootConfig, name, providerName string) bool {
	for _, g := range cfg.Groups {
		for _, tool := range g.Tools {
			if tool.Name != name {
				continue
			}
			spec, ok := cfg.Tools[name]
			if !ok {
				return tool.Provider == providerName
			}
			if spec.Provider == providerName || spec.InstallWith == providerName {
				return true
			}
		}
	}
	return false
}

// ─── Install ─────────────────────────────────────────────────────────────────

func TestInstall_Success(t *testing.T) {
	stub := &stubProvider{name: "brew", available: true}
	a, _ := newImportApp(t, stub)

	if err := a.Install(context.Background(), "ripgrep", "brew"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "ripgrep" {
		t.Errorf("DB = %v, want [ripgrep]", tools)
	}
}

func TestInstall_PostInstallVerificationFailureDoesNotMarkInstalled(t *testing.T) {
	tests := []struct {
		name      string
		provider  *installVerifyStub
		wantError string
	}{
		{
			name:      "status error",
			provider:  &installVerifyStub{stubProvider: stubProvider{name: "brew", available: true}, verifyErr: errors.New("status failed")},
			wantError: "status failed",
		},
		{
			name:      "not installed",
			provider:  &installVerifyStub{stubProvider: stubProvider{name: "brew", available: true}, verifyInstalled: false},
			wantError: "not installed after install",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _ := newImportApp(t, tt.provider)

			err := a.Install(context.Background(), "ripgrep", "brew")
			if err == nil {
				t.Fatal("Install returned nil, want verification error")
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("Install error = %q, want %q", err.Error(), tt.wantError)
			}
			tools, listErr := a.ListTools(context.Background(), "")
			if listErr != nil {
				t.Fatalf("ListTools: %v", listErr)
			}
			if len(tools) != 0 {
				t.Fatalf("tools = %+v, want no installed cache row", tools)
			}
		})
	}
}

func TestInstall_UsesLogicalPackageAndInstallWith(t *testing.T) {
	system := &stubProvider{name: "system", available: true}
	brew := &installCaptureStub{stubProvider: stubProvider{name: "brew", available: true}, version: "14.1.0"}
	a, cfgPath := newImportApp(t, system, brew)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalFixtureTool{Name: "ripgrep", Provider: "system", Package: "rg", InstallWith: "brew"},
		),
		Groups: []*config.GroupConfig{
			{Tools: groupTools("ripgrep")},
		},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	if err := a.Install(context.Background(), "ripgrep", "system"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if len(brew.installed) != 1 {
		t.Fatalf("brew installs = %d, want 1", len(brew.installed))
	}
	if got := brew.installed[0]; got.Name != "ripgrep" || got.Provider != "brew" || got.Package != "rg" {
		t.Fatalf("installed tool = %+v, want ripgrep via brew package rg", got)
	}
	cached, err := a.DB().Get(context.Background(), "ripgrep", "system", "rg")
	if err != nil {
		t.Fatalf("cache get: %v", err)
	}
	if !cached.Installed || cached.InstalledWith != "brew" {
		t.Fatalf("cache installed=%v installed_with=%q, want true/brew", cached.Installed, cached.InstalledWith)
	}
	if cached.Version.String != "14.1.0" {
		t.Fatalf("cache version = %q, want 14.1.0", cached.Version.String)
	}
}

func TestInstall_SystemResolvedToBrewPopulatesGit(t *testing.T) {
	system := &concreteInstallStub{
		installCaptureStub: installCaptureStub{
			stubProvider: stubProvider{name: "system", available: true},
			version:      "14.1.1",
		},
		concreteName: "brew",
	}
	brew := &metadataCheckingStub{
		stubProvider: stubProvider{name: "brew", available: true},
		metadata: map[string]provider.InstalledMetadata{
			"ripgrep": {
				Version: "14.1.1",
				Source: provider.SourceMetadata{
					Type:  provider.SourceTypeGitHub,
					Owner: "BurntSushi",
					Repo:  "ripgrep",
					URL:   "https://github.com/BurntSushi/ripgrep",
				},
			},
		},
	}
	a, cfgPath := newImportApp(t, system, brew)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("ripgrep", "system")),
		Groups: []*config.GroupConfig{{Tools: groupTools("ripgrep")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.Install(context.Background(), "ripgrep", "system"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got := cfg.Tools["ripgrep"].Git; got != "https://github.com/BurntSushi/ripgrep" {
		t.Fatalf("tool git = %q, want resolved Brew GitHub URL", got)
	}
}

func TestInstallWithStateReturnsUpdatedToolsAndGroups(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost.local")
	brew := &stubProvider{name: "brew", available: true}
	a, cfgPath := newImportApp(t, brew)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Tools: groupTools("ripgrep")},
		},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	result, err := a.InstallWithState(context.Background(), "ripgrep", "system")
	if err != nil {
		t.Fatalf("InstallWithState: %v", err)
	}

	toolKey := "ripgrep\x00system"
	if _, ok := result.State.ToolMemberships[toolKey]; !ok {
		t.Fatalf("ToolMemberships[%q] missing after install: %v", toolKey, result.State.ToolMemberships)
	}
	found := false
	for _, tool := range result.Tools {
		if tool.Name == "ripgrep" && tool.Provider == "system" && tool.Installed {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Tools = %+v, want installed ripgrep/system", result.Tools)
	}
}

func TestInstall_UnknownProvider(t *testing.T) {
	a, _ := newImportApp(t)
	if err := a.Install(context.Background(), "ripgrep", "unknown"); err == nil {
		t.Error("expected error for unknown provider, got nil")
	}
}

func TestInstall_UnavailableProvider(t *testing.T) {
	stub := &stubProvider{name: "brew", available: false}
	a, _ := newImportApp(t, stub)
	if err := a.Install(context.Background(), "ripgrep", "brew"); err == nil {
		t.Error("expected error when provider unavailable, got nil")
	}
}

// ─── Uninstall ───────────────────────────────────────────────────────────────

func TestUninstall_Success(t *testing.T) {
	stub := &stubProvider{name: "brew", available: true}
	a, _ := newImportApp(t, stub)

	if err := a.Install(context.Background(), "ripgrep", "brew"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := a.Uninstall(context.Background(), "ripgrep", "brew"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("tool should be removed from cache after Uninstall, got %+v", tools)
	}
}

func TestUninstall_RemovesConfiguredToolFromFile(t *testing.T) {
	stub := &stubProvider{name: "brew", available: true}
	a, cfgPath := newImportApp(t, stub)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("fd", "brew"),
		),
		Groups: []*config.GroupConfig{
			{Tools: groupTools("ripgrep")},
			{Name: "work", Tools: groupTools("fd")},
		},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.Uninstall(context.Background(), "ripgrep", "brew"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if hasTool(cfg, "ripgrep", "brew") {
		t.Fatalf("ripgrep still present in config after uninstall: %+v", cfg.Groups)
	}
	if !hasTool(cfg, "fd", "brew") {
		t.Fatalf("unrelated tool fd was removed from config: %+v", cfg.Groups)
	}
}

func TestUninstall_UsesConfiguredPackage(t *testing.T) {
	stub := &uninstallCaptureStub{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, stub)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalToolPackage("terraform", "brew", "hashicorp/tap/terraform")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("terraform"),
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.Uninstall(context.Background(), "terraform", "brew"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	if len(stub.uninstalled) != 1 {
		t.Fatalf("uninstalled calls = %d, want 1", len(stub.uninstalled))
	}
	if got := stub.uninstalled[0].Package; got != "hashicorp/tap/terraform" {
		t.Fatalf("uninstall package = %q, want configured package", got)
	}
}

func TestUninstall_UsesConfiguredInstallWithWhenCacheMissing(t *testing.T) {
	node := &managerUninstallCaptureStub{
		uninstallCaptureStub: uninstallCaptureStub{stubProvider: stubProvider{name: "node", available: true}},
	}
	a, cfgPath := newImportApp(t, node)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalFixtureTool{Name: "typescript", Provider: "node", InstallWith: "npm"}),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("typescript"),
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.Uninstall(context.Background(), "typescript", "node"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	if len(node.managerUninstalls) != 1 || node.managerUninstalls[0] != "npm" {
		t.Fatalf("manager uninstalls = %v, want [npm]", node.managerUninstalls)
	}
	if len(node.uninstalled) != 1 || node.uninstalled[0].Provider != "node" {
		t.Fatalf("uninstalled tools = %+v, want one node tool", node.uninstalled)
	}
}

func TestUninstall_RejectsProviderTool(t *testing.T) {
	node := &managerUninstallCaptureStub{
		uninstallCaptureStub: uninstallCaptureStub{stubProvider: stubProvider{name: "node", available: true}},
	}
	a, cfgPath := newImportApp(t, node)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalFixtureTool{Name: "npm", Provider: "node", InstallWith: "npm"}),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("npm"),
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	err := a.Uninstall(context.Background(), "npm", "node")
	if err == nil || !strings.Contains(err.Error(), "package manager/provider") {
		t.Fatalf("Uninstall err = %v, want protected provider tool error", err)
	}
	if len(node.uninstalled) != 0 || len(node.managerUninstalls) != 0 {
		t.Fatalf("uninstall calls = %+v manager calls = %+v, want none", node.uninstalled, node.managerUninstalls)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !hasTool(cfg, "npm", "node") {
		t.Fatalf("npm config entry was removed despite protected provider guard: %+v", cfg.Groups)
	}
}

func TestUninstall_RemovesConfiguredEntry(t *testing.T) {
	stub := &stubProvider{name: "brew", available: true}
	a, cfgPath := newImportApp(t, stub)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{
			{Name: "work", Tools: groupTools("ripgrep")},
		},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.Uninstall(context.Background(), "ripgrep", "brew"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if hasTool(cfg, "ripgrep", "brew") {
		t.Fatalf("ripgrep entry still present after uninstall: %+v", cfg.Groups)
	}
}

func TestUninstall_ProviderFailureLeavesConfigUntouched(t *testing.T) {
	stub := &uninstallErrStub{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, stub)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{testHostToolGroup("ripgrep")},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.Uninstall(context.Background(), "ripgrep", "brew"); err == nil {
		t.Fatal("expected uninstall error")
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !hasTool(cfg, "ripgrep", "brew") {
		t.Fatalf("ripgrep config entry was removed despite provider failure: %+v", cfg.Groups)
	}
}

func TestUninstall_UnknownProvider(t *testing.T) {
	a, _ := newImportApp(t)
	if err := a.Uninstall(context.Background(), "ripgrep", "unknown"); err == nil {
		t.Error("expected error for unknown provider, got nil")
	}
}

func TestUninstallWithStateReturnsUpdatedToolsAndGroups(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost.local")
	stub := &stubProvider{
		name:      "brew",
		available: true,
		installed: []provider.InstalledTool{
			installedTool("fd", "1.0.0", "brew"),
			installedTool("ripgrep", "1.0.0", "brew"),
		},
	}
	a, cfgPath := newImportApp(t, stub)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("fd", "brew"),
			logicalTool("ripgrep", "brew"),
		),
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Tools: groupTools("fd", "ripgrep")},
		},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.RefreshInstalled(context.Background(), nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	result, err := a.UninstallWithState(context.Background(), "ripgrep", "brew")
	if err != nil {
		t.Fatalf("UninstallWithState: %v", err)
	}

	deletedKey := "ripgrep\x00system"
	if _, ok := result.State.ToolGroups[deletedKey]; ok {
		t.Fatalf("ToolGroups[%q] still present after uninstall: %v", deletedKey, result.State.ToolGroups)
	}
	if _, ok := result.State.ToolMemberships[deletedKey]; ok {
		t.Fatalf("ToolMemberships[%q] still present after uninstall: %v", deletedKey, result.State.ToolMemberships)
	}
	foundFD := false
	for _, tool := range result.Tools {
		if tool.Name == "fd" && tool.Provider == "system" {
			foundFD = true
		}
		if tool.Name == "ripgrep" {
			t.Fatalf("Tools include deleted ripgrep: %+v", result.Tools)
		}
	}
	if !foundFD {
		t.Fatalf("Tools = %+v, want remaining fd/system", result.Tools)
	}
}

func TestRemoveToolFromConfig_RemovesMissingConfiguredTool(t *testing.T) {
	stub := &stubProvider{name: "brew", available: true}
	a, cfgPath := newImportApp(t, stub)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{testHostToolGroup("ripgrep")},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().Upsert(context.Background(), &database.ToolCache{
		Name:      "ripgrep",
		Provider:  "brew",
		Installed: false,
		Tracked:   true,
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	if err := a.RemoveToolFromConfig(context.Background(), "ripgrep", "brew"); err != nil {
		t.Fatalf("RemoveToolFromConfig: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if hasTool(cfg, "ripgrep", "brew") {
		t.Fatalf("ripgrep still present in config: %+v", cfg.Groups)
	}
	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 0 {
		t.Fatalf("cache tools = %+v, want removed", tools)
	}
}

func TestRemoveToolFromConfigWithStateReturnsUpdatedToolsAndGroups(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost.local")
	stub := &stubProvider{
		name:      "brew",
		available: true,
		installed: []provider.InstalledTool{
			installedTool("fd", "1.0.0", "brew"),
		},
	}
	a, cfgPath := newImportApp(t, stub)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("fd", "brew"),
			logicalTool("ripgrep", "brew"),
		),
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Tools: groupTools("fd", "ripgrep")},
		},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.RefreshInstalled(context.Background(), nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}
	if err := a.DB().Upsert(context.Background(), &database.ToolCache{
		Name:      "ripgrep",
		Provider:  "system",
		Package:   "ripgrep",
		Installed: false,
		Tracked:   true,
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	result, err := a.RemoveToolFromConfigWithState(context.Background(), "ripgrep", "brew")
	if err != nil {
		t.Fatalf("RemoveToolFromConfigWithState: %v", err)
	}

	deletedKey := "ripgrep\x00system"
	if _, ok := result.State.ToolGroups[deletedKey]; ok {
		t.Fatalf("ToolGroups[%q] still present after config delete: %v", deletedKey, result.State.ToolGroups)
	}
	if _, ok := result.State.ToolMemberships[deletedKey]; ok {
		t.Fatalf("ToolMemberships[%q] still present after config delete: %v", deletedKey, result.State.ToolMemberships)
	}
	foundFD := false
	for _, tool := range result.Tools {
		if tool.Name == "fd" && tool.Provider == "system" {
			foundFD = true
		}
		if tool.Name == "ripgrep" {
			t.Fatalf("Tools include deleted ripgrep: %+v", result.Tools)
		}
	}
	if !foundFD {
		t.Fatalf("Tools = %+v, want remaining fd/system", result.Tools)
	}
}

func TestRemoveToolFromConfig_RejectsProviderTool(t *testing.T) {
	node := &stubProvider{name: "node", available: true}
	a, cfgPath := newImportApp(t, node)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalFixtureTool{Name: "bun", Provider: "node", InstallWith: "bun"}),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("bun"),
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	err := a.RemoveToolFromConfig(context.Background(), "bun", "node")
	if err == nil || !strings.Contains(err.Error(), "package manager/provider") {
		t.Fatalf("RemoveToolFromConfig err = %v, want protected provider tool error", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !hasTool(cfg, "bun", "node") {
		t.Fatalf("bun config entry was removed despite protected provider guard: %+v", cfg.Groups)
	}
}

// ─── Upgrade ─────────────────────────────────────────────────────────────────

func TestUpgrade_Success(t *testing.T) {
	stub := &stubProvider{name: "brew", available: true}
	a, _ := newImportApp(t, stub)

	if err := a.Install(context.Background(), "ripgrep", "brew"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := a.Upgrade(context.Background(), "ripgrep", "brew"); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
}

func TestUpgradeWithStateReturnsUpdatedTools(t *testing.T) {
	node := &managerUpgradeStub{stubProvider: stubProvider{name: "node", available: true}, verifyInstalled: true}
	a, _ := newImportApp(t, node)
	ctx := context.Background()
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "typescript",
		Provider:      "node",
		Package:       "typescript",
		Installed:     true,
		InstalledWith: "npm",
		Outdated:      true,
		Tracked:       true,
	}); err != nil {
		t.Fatalf("seed tool: %v", err)
	}

	result, err := a.UpgradeWithState(ctx, "typescript", "node")
	if err != nil {
		t.Fatalf("UpgradeWithState: %v", err)
	}

	for _, tool := range result.Tools {
		if tool.Name != "typescript" || tool.Provider != "node" {
			continue
		}
		if tool.Outdated {
			t.Fatalf("upgraded tool remains outdated: %+v", tool)
		}
		if tool.Version.String != "npm-version" {
			t.Fatalf("version = %q, want npm-version", tool.Version.String)
		}
		return
	}
	t.Fatalf("Tools = %+v, want upgraded typescript/node", result.Tools)
}

func TestUpgrade_UnknownProvider(t *testing.T) {
	a, _ := newImportApp(t)
	if err := a.Upgrade(context.Background(), "ripgrep", "unknown"); err == nil {
		t.Error("expected error for unknown provider, got nil")
	}
}

type managerUpgradeStub struct {
	stubProvider
	fallbackUpgrades  int
	managerUpgrades   []string
	verifyInstalled   bool
	verifyErr         error
	outdatedByManager map[string]map[string]string
	outdatedErr       error
}

func (s *managerUpgradeStub) Upgrade(_ context.Context, _ provider.Tool) error {
	s.fallbackUpgrades++
	return nil
}

func (s *managerUpgradeStub) UpgradeWithManager(_ context.Context, _ provider.Tool, manager string) error {
	s.managerUpgrades = append(s.managerUpgrades, manager)
	return nil
}

func (s *managerUpgradeStub) IsInstalledWithManager(_ context.Context, _ provider.Tool, manager string) (bool, string, error) {
	if s.verifyErr != nil {
		return false, "", s.verifyErr
	}
	if !s.verifyInstalled {
		return false, "", nil
	}
	return true, manager + "-version", nil
}

func (s *managerUpgradeStub) OutdatedMap(_ context.Context) (map[string]string, error) {
	if s.outdatedErr != nil {
		return nil, s.outdatedErr
	}
	out := make(map[string]string)
	for _, entries := range s.outdatedByManager {
		for name, latest := range entries {
			if _, exists := out[name]; !exists {
				out[name] = latest
			}
		}
	}
	return out, nil
}

func (s *managerUpgradeStub) OutdatedByManager(_ context.Context) (map[string]map[string]string, error) {
	if s.outdatedErr != nil {
		return nil, s.outdatedErr
	}
	return s.outdatedByManager, nil
}

type clearingManagerUpgradeStub struct {
	managerUpgradeStub
	outdatedCalls int
}

func (s *clearingManagerUpgradeStub) OutdatedByManager(_ context.Context) (map[string]map[string]string, error) {
	s.outdatedCalls++
	if s.outdatedCalls == 1 {
		return map[string]map[string]string{"npm": {"typescript": "5.0.0"}}, nil
	}
	return map[string]map[string]string{"npm": {}}, nil
}

func TestUpgrade_UsesInstalledWithManager(t *testing.T) {
	node := &managerUpgradeStub{stubProvider: stubProvider{name: "node", available: true}, verifyInstalled: true}
	a, _ := newImportApp(t, node)
	ctx := context.Background()
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "typescript",
		Provider:      "node",
		Package:       "typescript",
		Installed:     true,
		InstalledWith: "npm",
		Outdated:      true,
		Tracked:       true,
	}); err != nil {
		t.Fatalf("seed tool: %v", err)
	}

	if err := a.Upgrade(ctx, "typescript", "node"); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	if node.fallbackUpgrades != 0 {
		t.Fatalf("fallback Upgrade called %d times, want manager-specific upgrade", node.fallbackUpgrades)
	}
	if len(node.managerUpgrades) != 1 || node.managerUpgrades[0] != "npm" {
		t.Fatalf("manager upgrades = %v, want [npm]", node.managerUpgrades)
	}
	got, err := a.DB().Get(ctx, "typescript", "node", "typescript")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Outdated {
		t.Fatal("successful manager-specific upgrade should clear outdated flag")
	}
	if got.Version.String != "npm-version" {
		t.Fatalf("version = %q, want npm-version", got.Version.String)
	}
}

func TestUpgrade_RechecksOutdatedAfterVerification(t *testing.T) {
	node := &managerUpgradeStub{
		stubProvider:      stubProvider{name: "node", available: true},
		verifyInstalled:   true,
		outdatedByManager: map[string]map[string]string{"npm": {"typescript": "5.0.0"}},
	}
	a, _ := newImportApp(t, node)
	ctx := context.Background()
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "typescript",
		Provider:      "node",
		Package:       "typescript",
		Installed:     true,
		InstalledWith: "npm",
		Outdated:      true,
		LatestVersion: sql.NullString{String: "5.0.0", Valid: true},
		Tracked:       true,
	}); err != nil {
		t.Fatalf("seed tool: %v", err)
	}

	if err := a.Upgrade(ctx, "typescript", "node"); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	got, err := a.DB().Get(ctx, "typescript", "node", "typescript")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Outdated || got.LatestVersion.String != "5.0.0" {
		t.Fatalf("outdated/latest = %v/%q, want preserved real provider status 5.0.0", got.Outdated, got.LatestVersion.String)
	}
	if got.Version.String != "npm-version" {
		t.Fatalf("version = %q, want npm-version from post-upgrade verification", got.Version.String)
	}
}

func TestUpgrade_UsesConfiguredInstallWithWhenCacheMissing(t *testing.T) {
	node := &managerUpgradeStub{stubProvider: stubProvider{name: "node", available: true}, verifyInstalled: true}
	a, cfgPath := newImportApp(t, node)
	ctx := context.Background()
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalFixtureTool{Name: "typescript", Provider: "node", InstallWith: "npm"}),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("typescript"),
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.Upgrade(ctx, "typescript", "node"); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	if node.fallbackUpgrades != 0 {
		t.Fatalf("fallback Upgrade called %d times, want manager-specific upgrade", node.fallbackUpgrades)
	}
	if len(node.managerUpgrades) != 1 || node.managerUpgrades[0] != "npm" {
		t.Fatalf("manager upgrades = %v, want [npm]", node.managerUpgrades)
	}
	got, err := a.DB().Get(ctx, "typescript", "node", "typescript")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.InstalledWith != "npm" || got.Version.String != "npm-version" {
		t.Fatalf("cache installed_with/version = %q/%q, want npm/npm-version", got.InstalledWith, got.Version.String)
	}
}

func TestUpgrade_VerificationFailureKeepsOutdatedState(t *testing.T) {
	node := &managerUpgradeStub{stubProvider: stubProvider{name: "node", available: true}, verifyErr: errors.New("status failed")}
	a, _ := newImportApp(t, node)
	ctx := context.Background()
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "typescript",
		Provider:      "node",
		Package:       "typescript",
		Installed:     true,
		InstalledWith: "npm",
		Outdated:      true,
		LatestVersion: sql.NullString{String: "5.0.0", Valid: true},
		Tracked:       true,
	}); err != nil {
		t.Fatalf("seed tool: %v", err)
	}

	err := a.Upgrade(ctx, "typescript", "node")
	if err == nil || !strings.Contains(err.Error(), "verify typescript after upgrade") {
		t.Fatalf("Upgrade error = %v, want verification failure", err)
	}
	got, getErr := a.DB().Get(ctx, "typescript", "node", "typescript")
	if getErr != nil {
		t.Fatalf("Get: %v", getErr)
	}
	if !got.Outdated || got.LatestVersion.String != "5.0.0" {
		t.Fatalf("outdated/latest = %v/%q, want preserved 5.0.0", got.Outdated, got.LatestVersion.String)
	}
}

// ─── UpgradeAll ───────────────────────────────────────────────────────────────

func TestUpgradeAll_OnlyUpgradesOutdated(t *testing.T) {
	stub := &stubProvider{name: "brew", available: true}
	a, _ := newImportApp(t, stub)

	ctx := context.Background()
	if err := a.Install(ctx, "ripgrep", "brew"); err != nil {
		t.Fatalf("Install ripgrep: %v", err)
	}
	if err := a.Install(ctx, "git", "brew"); err != nil {
		t.Fatalf("Install git: %v", err)
	}
	// Mark only ripgrep outdated.
	if err := a.DB().UpdateOutdated(ctx, "ripgrep", "brew", "ripgrep", true, "15.0.0"); err != nil {
		t.Fatalf("UpdateOutdated: %v", err)
	}

	progressCalls := 0
	if err := a.UpgradeAll(ctx, func(_ string) { progressCalls++ }); err != nil {
		t.Fatalf("UpgradeAll: %v", err)
	}
	if progressCalls != 1 {
		t.Errorf("progress called %d times, want 1 (only ripgrep is outdated)", progressCalls)
	}
}

func TestUpgradeAllDetailedWithStateReturnsUpdatedTools(t *testing.T) {
	node := &clearingManagerUpgradeStub{
		managerUpgradeStub: managerUpgradeStub{
			stubProvider:    stubProvider{name: "node", available: true},
			verifyInstalled: true,
		},
	}
	a, _ := newImportApp(t, node)
	ctx := context.Background()
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "typescript",
		Provider:      "node",
		Package:       "typescript",
		Installed:     true,
		InstalledWith: "npm",
		Outdated:      true,
		LatestVersion: sql.NullString{String: "5.0.0", Valid: true},
		Tracked:       true,
	}); err != nil {
		t.Fatalf("seed tool: %v", err)
	}

	result, err := a.UpgradeAllDetailedWithState(ctx, nil, nil, app.UpgradeAllOptions{})
	if err != nil {
		t.Fatalf("UpgradeAllDetailedWithState: %v", err)
	}
	if result.Result == nil || len(result.Result.Upgraded) != 1 || result.Result.Upgraded[0] != "typescript" {
		t.Fatalf("UpgradeAll result = %+v, want typescript upgraded", result.Result)
	}
	for _, tool := range result.Tools {
		if tool.Name != "typescript" || tool.Provider != "node" {
			continue
		}
		if tool.Outdated {
			t.Fatalf("typescript remains outdated in returned tools: %+v", tool)
		}
		return
	}
	t.Fatalf("Tools = %+v, want typescript/node", result.Tools)
}

func TestUpgradeAll_NothingOutdated(t *testing.T) {
	stub := &stubProvider{name: "brew", available: true}
	a, _ := newImportApp(t, stub)

	if err := a.Install(context.Background(), "git", "brew"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	progressCalls := 0
	if err := a.UpgradeAll(context.Background(), func(_ string) { progressCalls++ }); err != nil {
		t.Fatalf("UpgradeAll: %v", err)
	}
	if progressCalls != 0 {
		t.Errorf("progress called %d times, want 0 (nothing outdated)", progressCalls)
	}
}

func TestUpgradeAll_SkipsUninstalledOutdatedRows(t *testing.T) {
	stub := &stubProvider{name: "brew", available: true}
	a, _ := newImportApp(t, stub)
	ctx := context.Background()
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:      "ripgrep",
		Provider:  "brew",
		Package:   "ripgrep",
		Installed: false,
		Outdated:  true,
		Tracked:   true,
	}); err != nil {
		t.Fatalf("seed tool: %v", err)
	}

	progressCalls := 0
	if err := a.UpgradeAll(ctx, func(_ string) { progressCalls++ }); err != nil {
		t.Fatalf("UpgradeAll: %v", err)
	}
	if progressCalls != 0 {
		t.Fatalf("progress called %d times, want uninstalled row skipped", progressCalls)
	}
}

func TestReconcile_SyncsAndUpgradesTools(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	stub := &reconcileUpgradeStub{
		stubProvider: stubProvider{
			name:      "system",
			available: true,
			installed: []provider.InstalledTool{
				installedTool("ripgrep", "1.0.0", "system"),
			},
		},
	}
	a, cfgPath := newImportApp(t, stub)
	if err := config.Save(cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"fd":      {Provider: "system"},
			"ripgrep": {Provider: "system"},
		},
		Hosts: map[string][]string{"testhost": {"base"}},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "base", Tools: []config.ToolEntry{{Name: "fd"}, {Name: "ripgrep"}}},
		},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	ctx := context.Background()
	if err := a.RefreshInstalled(ctx, nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}
	if err := a.DB().UpdateOutdated(ctx, "ripgrep", "system", "ripgrep", true, "2.0.0"); err != nil {
		t.Fatalf("UpdateOutdated: %v", err)
	}

	var upgradeTargets []string
	result, err := a.Reconcile(ctx, app.ReconcileOptions{
		ToolProgress: func(event syncprogress.ProgressEvent) {
			if strings.HasPrefix(event.Message, "Upgrading ripgrep") {
				upgradeTargets = append(upgradeTargets, event.TargetVersion)
			}
		},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if result == nil || result.SyncAll == nil || result.UpgradeAll == nil {
		t.Fatalf("result = %#v, want sync and upgrade results", result)
	}
	if got := len(result.SyncAll.SyncResult.Installed()); got != 1 {
		t.Fatalf("installed count = %d, want 1", got)
	}
	if got := len(result.UpgradeAll.Upgraded); got != 1 {
		t.Fatalf("upgraded count = %d, want 1", got)
	}
	if len(upgradeTargets) != 1 || upgradeTargets[0] != "2.0.0" {
		t.Fatalf("upgrade target versions = %#v, want [2.0.0]", upgradeTargets)
	}
	foundFD := false
	for _, installed := range stub.installed {
		if installed.Name == "fd" {
			foundFD = true
			break
		}
	}
	if !foundFD {
		t.Fatalf("fd was not installed during reconcile: %#v", stub.installed)
	}
	tools, err := a.ListTools(ctx, "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range tools {
		if tool.Name == "ripgrep" && tool.Outdated {
			t.Fatal("ripgrep should no longer be outdated after reconcile upgrade")
		}
	}
}

type reconcileUpgradeStub struct {
	stubProvider
	upgraded map[string]bool
}

func (s *reconcileUpgradeStub) Upgrade(_ context.Context, tool provider.Tool) error {
	if s.upgraded == nil {
		s.upgraded = make(map[string]bool)
	}
	s.upgraded[tool.Name] = true
	return nil
}

func (s *reconcileUpgradeStub) OutdatedMap(_ context.Context) (map[string]string, error) {
	if s.upgraded["ripgrep"] {
		return nil, nil
	}
	return map[string]string{"ripgrep": "2.0.0"}, nil
}

type selectiveUpgradeStub struct {
	stubProvider
	failName string
	upgraded []string
	outdated []string
}

func (s *selectiveUpgradeStub) Upgrade(_ context.Context, tool provider.Tool) error {
	s.upgraded = append(s.upgraded, tool.Name)
	if tool.Name == s.failName {
		return errors.New("upgrade failed")
	}
	return nil
}

func (s *selectiveUpgradeStub) IsInstalled(_ context.Context, tool provider.Tool) (bool, string, error) {
	return true, tool.Name + "-new", nil
}

func (s *selectiveUpgradeStub) OutdatedMap(_ context.Context) (map[string]string, error) {
	out := make(map[string]string)
	alreadyUpgraded := make(map[string]bool, len(s.upgraded))
	for _, name := range s.upgraded {
		alreadyUpgraded[name] = true
	}
	for _, name := range s.outdated {
		if name == s.failName || !alreadyUpgraded[name] {
			out[name] = name + "-latest"
		}
	}
	return out, nil
}

func TestUpgradeAll_ContinuesAfterToolFailure(t *testing.T) {
	stub := &selectiveUpgradeStub{
		stubProvider: stubProvider{name: "brew", available: true},
		failName:     "ripgrep",
		outdated:     []string{"ripgrep", "git"},
	}
	a, _ := newImportApp(t, stub)
	ctx := context.Background()
	for _, name := range []string{"ripgrep", "git"} {
		if err := a.DB().Upsert(ctx, &database.ToolCache{
			Name:      name,
			Provider:  "brew",
			Package:   name,
			Installed: true,
			Outdated:  true,
			Tracked:   true,
		}); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	err := a.UpgradeAll(ctx, nil)
	if err == nil {
		t.Fatal("UpgradeAll returned nil, want joined error for failed tool")
	}
	if len(stub.upgraded) != 2 {
		t.Fatalf("upgraded = %v, want both outdated tools attempted", stub.upgraded)
	}
	result, err := a.UpgradeAllDetailed(ctx, nil, nil)
	if err == nil {
		t.Fatal("UpgradeAllDetailed returned nil error, want joined error for failed tool")
	}
	if len(result.Failures) != 1 {
		t.Fatalf("Failures = %d, want 1", len(result.Failures))
	}
	if got := result.Failures[0]; got.Name != "ripgrep" || got.Provider != "brew" || got.Message == "" {
		t.Fatalf("Failures[0] = %+v, want ripgrep/brew with message", got)
	}
	git, err := a.DB().Get(ctx, "git", "brew", "git")
	if err != nil {
		t.Fatalf("Get git: %v", err)
	}
	if git.Outdated {
		t.Fatal("successful tool should be marked no longer outdated")
	}
	rg, err := a.DB().Get(ctx, "ripgrep", "brew", "ripgrep")
	if err != nil {
		t.Fatalf("Get ripgrep: %v", err)
	}
	if !rg.Outdated {
		t.Fatal("failed tool should remain outdated")
	}
}

// ─── Search ───────────────────────────────────────────────────────────────────

func TestSearch_FansOut(t *testing.T) {
	s := &searchStub{
		stubProvider: stubProvider{name: "brew", available: true},
		results:      []provider.SearchResult{{Name: "ripgrep", Provider: "brew"}},
	}
	a, _ := newImportApp(t, s)

	results, err := a.Search(context.Background(), "ripgrep", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].Name != "ripgrep" {
		t.Errorf("Search = %v, want 1 result named ripgrep", results)
	}
	if results[0].Provider != "system" || results[0].SourceProvider != "brew" {
		t.Fatalf("Search provider = %q source = %q, want system source brew", results[0].Provider, results[0].SourceProvider)
	}
}

func TestSearchResultDisplayProviderKeepsConcreteWithinSameEcosystem(t *testing.T) {
	tests := []struct {
		name string
		in   provider.SearchResult
		want string
	}{
		{
			name: "system result from brew",
			in:   provider.SearchResult{Provider: "system", SourceProvider: "brew"},
			want: "brew",
		},
		{
			name: "python result from pip",
			in:   provider.SearchResult{Provider: "python", SourceProvider: "pip"},
			want: "pip",
		},
		{
			name: "same provider",
			in:   provider.SearchResult{Provider: "brew", SourceProvider: "brew"},
		},
		{
			name: "different ecosystem",
			in:   provider.SearchResult{Provider: "python", SourceProvider: "brew"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := app.SearchResultDisplayProvider(tt.in); got != tt.want {
				t.Fatalf("SearchResultDisplayProvider = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSearch_CachesResultMetadata(t *testing.T) {
	ctx := context.Background()
	s := &searchStub{
		stubProvider: stubProvider{name: "brew", available: true},
		results: []provider.SearchResult{{
			Name:        "ripgrep",
			Provider:    "brew",
			Version:     "14.1.0",
			Description: "fast grep",
			Privilege: provider.PrivilegePlan{
				Requirement: provider.PrivilegeMaybe,
				Reason:      "cask may run installer package",
			},
			Source: provider.SourceMetadata{
				Type:  provider.SourceTypeGitHub,
				Owner: "BurntSushi",
				Repo:  "ripgrep",
				URL:   "https://github.com/BurntSushi/ripgrep",
			},
		}},
	}
	a, _ := newImportApp(t, s)

	results, err := a.Search(ctx, "ripgrep", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].Provider != "system" {
		t.Fatalf("Search results = %+v, want one system result", results)
	}

	meta, err := a.DB().GetMetadata(ctx, "ripgrep", "system", "ripgrep")
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	if !meta.Description.Valid || meta.Description.String != "fast grep" {
		t.Fatalf("metadata description = %+v, want fast grep", meta.Description)
	}
	if !meta.Version.Valid || meta.Version.String != "14.1.0" {
		t.Fatalf("metadata version = %+v, want 14.1.0", meta.Version)
	}
	if meta.Privilege != string(provider.PrivilegeMaybe) {
		t.Fatalf("metadata privilege = %q, want maybe", meta.Privilege)
	}
	if meta.SourceType != provider.SourceTypeGitHub || meta.SourceOwner != "BurntSushi" || meta.SourceRepo != "ripgrep" {
		t.Fatalf("metadata source = %s/%s/%s, want github/BurntSushi/ripgrep", meta.SourceType, meta.SourceOwner, meta.SourceRepo)
	}

	tools, err := a.ListTools(ctx, "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 0 {
		t.Fatalf("ListTools returned %d metadata-only rows, want 0", len(tools))
	}
}

func TestAdd_UsesCachedSearchSourceMetadataForGit(t *testing.T) {
	ctx := context.Background()
	s := &searchStub{
		stubProvider: stubProvider{name: "brew", available: true},
		results: []provider.SearchResult{{
			Name:     "ripgrep",
			Provider: "brew",
			Source: provider.SourceMetadata{
				Type:  provider.SourceTypeGitHub,
				Owner: "BurntSushi",
				Repo:  "ripgrep",
				URL:   "https://github.com/BurntSushi/ripgrep",
			},
		}},
	}
	a, cfgPath := newImportApp(t, s)

	if _, err := a.Search(ctx, "ripgrep", ""); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if err := a.Add(ctx, "system", "ripgrep", "ripgrep", "work", "brew"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got := cfg.Tools["ripgrep"].Git; got != "https://github.com/BurntSushi/ripgrep" {
		t.Fatalf("tool git = %q, want cached search GitHub source", got)
	}
}

func TestSearch_ProviderFilter(t *testing.T) {
	brew := &searchStub{
		stubProvider: stubProvider{name: "brew", available: true},
		results:      []provider.SearchResult{{Name: "ripgrep", Provider: "brew"}},
	}
	npm := &searchStub{
		stubProvider: stubProvider{name: "npm", available: true},
		results:      []provider.SearchResult{{Name: "typescript", Provider: "npm"}},
	}
	a, _ := newImportApp(t, brew, npm)

	results, err := a.Search(context.Background(), "q", "brew")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, r := range results {
		if r.Provider != "system" {
			t.Errorf("got result provider %q, expected system config provider", r.Provider)
		}
	}
}

func TestSearch_EcosystemProviderFilterUsesConcreteSearcher(t *testing.T) {
	pip := &searchStub{
		stubProvider: stubProvider{name: "pip", available: true},
		results:      []provider.SearchResult{{Name: "black", Provider: "pip"}},
	}
	a, _ := newImportApp(t, pip)

	results, err := a.Search(context.Background(), "black", "python")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Search returned %d results, want 1", len(results))
	}
	if results[0].Provider != "python" {
		t.Fatalf("result provider = %q, want python", results[0].Provider)
	}
}

func TestSearch_SkipsNonSearcher(t *testing.T) {
	stub := &stubProvider{name: "brew", available: true}
	a, _ := newImportApp(t, stub)

	results, err := a.Search(context.Background(), "anything", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results from non-Searcher provider, got %d", len(results))
	}
}

// ─── ListTools ────────────────────────────────────────────────────────────────

func TestListTools_Empty(t *testing.T) {
	a, _ := newImportApp(t)
	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("expected empty list, got %d", len(tools))
	}
}

func TestListTools_ProviderFilter(t *testing.T) {
	brew := &stubProvider{name: "brew", available: true}
	npm := &stubProvider{name: "npm", available: true}
	a, _ := newImportApp(t, brew, npm)

	ctx := context.Background()
	if err := a.Install(ctx, "ripgrep", "brew"); err != nil {
		t.Fatalf("Install ripgrep: %v", err)
	}
	if err := a.Install(ctx, "typescript", "npm"); err != nil {
		t.Fatalf("Install typescript: %v", err)
	}

	tools, err := a.ListTools(ctx, "brew")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Provider != "brew" {
		t.Errorf("expected 1 brew tool, got %v", tools)
	}
}

func TestQueryTools_ProviderFilterMatchesInstalledWith(t *testing.T) {
	system := &stubProvider{name: "system", available: true}
	brew := &stubProvider{name: "brew", available: true}
	a, _ := newImportApp(t, system, brew)

	if err := a.DB().Upsert(context.Background(), &database.ToolCache{
		Name:          "ripgrep",
		Provider:      "system",
		Package:       "rg",
		Installed:     true,
		InstalledWith: "brew",
	}); err != nil {
		t.Fatalf("db.Upsert: %v", err)
	}

	items, err := a.QueryTools(context.Background(), app.ToolListOptions{Provider: "brew"})
	if err != nil {
		t.Fatalf("QueryTools: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if got := items[0].Tool; got.Name != "ripgrep" || got.Provider != "system" || got.InstalledWith != "brew" {
		t.Fatalf("item = %+v, want system row installed_with brew", got)
	}
}

// ─── Providers ────────────────────────────────────────────────────────────────

func TestProviders_ListsAll(t *testing.T) {
	brew := &stubProvider{name: "brew", available: true}
	npm := &stubProvider{name: "npm", available: false}
	a, _ := newImportApp(t, brew, npm)

	infos, err := a.Providers(context.Background())
	if err != nil {
		t.Fatalf("Providers: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(infos))
	}
	byName := make(map[string]app.ProviderInfo)
	for _, p := range infos {
		byName[p.Name] = p
	}
	if !byName["brew"].Available {
		t.Error("brew should be available")
	}
	if byName["npm"].Available {
		t.Error("npm should not be available")
	}
}

// ─── LoadSettings / SaveSettings ─────────────────────────────────────────────

func TestLoadSettings_MissingConfig(t *testing.T) {
	a, _ := newImportApp(t)
	s, err := a.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if s.EcosystemManager("node") != "" || s.EcosystemManager("python") != "" {
		t.Errorf("expected zero-value Settings for missing config, got %+v", s)
	}
}

func TestLoadSettings_WithConfig(t *testing.T) {
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Settings: testSettingsWithNodePython("bun", "uv"),
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	s, err := a.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if s.EcosystemManager("node") != "bun" || s.EcosystemManager("python") != "uv" {
		t.Errorf("Settings = %+v, want node=bun python=uv", s)
	}
}

func TestSaveSettings_PreservesTools(t *testing.T) {
	a, cfgPath := newImportApp(t)
	// Write tools to the current host group.
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{testHostToolGroup("ripgrep")},
	}); err != nil {
		t.Fatalf("saving tools: %v", err)
	}

	if err := a.SaveSettings(context.Background(), testSettingsWithManager("node", "bun")); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	// Tools must still be present in settings.json.
	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	tools := toolsFromConfig(updated)
	if len(tools) != 1 || tools[0].Name != "ripgrep" {
		t.Errorf("tools lost after SaveSettings: got %v, want [ripgrep]", tools)
	}
	// Node manager is host-specific — verify via EffectiveSettings (LoadSettings).
	s, err := a.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if s.EcosystemManager("node") != "bun" {
		t.Errorf("node manager = %q, want bun", s.EcosystemManager("node"))
	}
}

func TestSaveSettings_CreatesConfigWhenMissing(t *testing.T) {
	a, _ := newImportApp(t)

	if err := a.SaveSettings(context.Background(), testSettingsWithManager("python", "uv")); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	// Python manager is host-specific — verify via EffectiveSettings (LoadSettings).
	s, err := a.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if s.EcosystemManager("python") != "uv" {
		t.Errorf("python manager = %q, want uv", s.EcosystemManager("python"))
	}
}

func TestSaveSettings_NormalizesDotsRepoUnderHome(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	home := t.TempDir()
	t.Setenv("HOME", home)
	a, cfgPath := newImportApp(t)

	settings := config.Settings{DotsRepo: filepath.Join(home, "Dev", "dotfiles")}
	if err := a.SaveSettings(context.Background(), settings); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got := loaded.HostSettings["testhost"].DotsRepo; got != "~/Dev/dotfiles" {
		t.Fatalf("persisted dots_repo = %q, want ~/Dev/dotfiles", got)
	}
	effective, err := a.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if effective.DotsRepo != "~/Dev/dotfiles" {
		t.Fatalf("effective dots_repo = %q, want ~/Dev/dotfiles", effective.DotsRepo)
	}
}

// ─── ResetSettings ───────────────────────────────────────────────────────────

func TestResetSettings_ClearsSettings(t *testing.T) {
	a, _ := newImportApp(t)
	ctx := context.Background()

	// Start with non-zero settings.
	if err := a.SaveSettings(ctx, testSettingsWithNodePython("bun", "uv")); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	if err := a.ResetSettings(ctx); err != nil {
		t.Fatalf("ResetSettings: %v", err)
	}

	s, err := a.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings after reset: %v", err)
	}
	if s.EcosystemManager("node") != "" || s.EcosystemManager("python") != "" {
		t.Errorf("settings not cleared: got %+v", s)
	}
}

func TestResetSettings_PreservesTools(t *testing.T) {
	a, cfgPath := newImportApp(t)
	ctx := context.Background()

	// Write settings.json with one tool.
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}); err != nil {
		t.Fatalf("saving tools: %v", err)
	}
	if err := a.SaveSettings(ctx, testSettingsWithManager("node", "bun")); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	if err := a.ResetSettings(ctx); err != nil {
		t.Fatalf("ResetSettings: %v", err)
	}

	// Tools must survive the settings reset.
	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	tools := toolsFromConfig(updated)
	if len(tools) != 1 || tools[0].Name != "ripgrep" {
		t.Errorf("tools lost after ResetSettings: got %v", tools)
	}
}

// ─── ResetCache ───────────────────────────────────────────────────────────────

func TestResetCache_DBIsUsableAfterReset(t *testing.T) {
	a, _ := newImportApp(t)
	ctx := context.Background()

	if err := a.ResetCache(ctx); err != nil {
		t.Fatalf("ResetCache: %v", err)
	}

	// Listing tools should work without error on a freshly reset cache.
	tools, err := a.ListTools(ctx, "")
	if err != nil {
		t.Fatalf("ListTools after ResetCache: %v", err)
	}
	// No tools configured, so the list should be empty (not nil-err).
	_ = tools
}

func TestResetCache_IdempotentOnEmptyDB(t *testing.T) {
	a, _ := newImportApp(t)
	ctx := context.Background()

	// Two resets in a row should both succeed.
	if err := a.ResetCache(ctx); err != nil {
		t.Fatalf("first ResetCache: %v", err)
	}
	if err := a.ResetCache(ctx); err != nil {
		t.Fatalf("second ResetCache: %v", err)
	}
}

// ─── ResolveProvider ─────────────────────────────────────────────────────────

func TestResolveProvider_DefaultOrder(t *testing.T) {
	// Only "node" is available; brew is registered but unavailable.
	brew := &stubProvider{name: "brew", available: false}
	node := &stubProvider{name: "node", available: true}
	a, _ := newImportApp(t, brew, node)

	got, err := a.ResolveProvider(context.Background(), nil)
	if err != nil {
		t.Fatalf("ResolveProvider: %v", err)
	}
	if got != "node" {
		t.Errorf("ResolveProvider = %q, want %q", got, "node")
	}
}

func TestResolveProvider_CustomPriority(t *testing.T) {
	brew := &stubProvider{name: "brew", available: true}
	node := &stubProvider{name: "node", available: true}
	a, _ := newImportApp(t, brew, node)

	// Caller prefers node over brew.
	got, err := a.ResolveProvider(context.Background(), []string{"node", "brew"})
	if err != nil {
		t.Fatalf("ResolveProvider: %v", err)
	}
	if got != "node" {
		t.Errorf("ResolveProvider = %q, want %q", got, "node")
	}
}

func TestResolveProvider_NoneAvailable(t *testing.T) {
	brew := &stubProvider{name: "brew", available: false}
	a, _ := newImportApp(t, brew)

	_, err := a.ResolveProvider(context.Background(), []string{"brew"})
	if err == nil {
		t.Error("expected error when no provider is available, got nil")
	}
}

func TestResolveProvider_UnregisteredSkipped(t *testing.T) {
	// Priority list names a provider that isn't registered; falls through.
	node := &stubProvider{name: "node", available: true}
	a, _ := newImportApp(t, node)

	got, err := a.ResolveProvider(context.Background(), []string{"brew", "node"})
	if err != nil {
		t.Fatalf("ResolveProvider: %v", err)
	}
	if got != "node" {
		t.Errorf("ResolveProvider = %q, want %q", got, "node")
	}
}

// ─── Install (auto-resolve) ───────────────────────────────────────────────────

func TestInstall_AutoResolve(t *testing.T) {
	brew := &stubProvider{name: "brew", available: true}
	a, _ := newImportApp(t, brew)

	// No provider specified — should auto-select "brew" via default priority.
	if err := a.Install(context.Background(), "ripgrep", ""); err != nil {
		t.Fatalf("Install with empty provider: %v", err)
	}
	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Provider != "brew" {
		t.Errorf("DB = %v, want ripgrep/brew", tools)
	}
}

func TestInstall_AutoResolveNoneAvailable(t *testing.T) {
	a, _ := newImportApp(t) // no providers registered
	if err := a.Install(context.Background(), "ripgrep", ""); err == nil {
		t.Error("expected error when no provider available, got nil")
	}
}

func TestInstall_AutoResolveFromSettings(t *testing.T) {
	brew := &stubProvider{name: "brew", available: true}
	node := &stubProvider{name: "node", available: true}
	a, _ := newImportApp(t, brew, node)

	// Configure settings to prefer "node".
	settings := config.Settings{}
	settings.SetEcosystemPriority("system", []string{"node", "brew"})
	if err := a.SaveSettings(context.Background(), settings); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	if err := a.Install(context.Background(), "typescript", ""); err != nil {
		t.Fatalf("Install: %v", err)
	}
	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Provider != "node" {
		t.Errorf("DB = %v, want typescript/node", tools)
	}
}

func TestDefaultInstallProviderUsesSettingsPriority(t *testing.T) {
	brew := &stubProvider{name: "brew", available: true}
	node := &stubProvider{name: "node", available: true}
	a, _ := newImportApp(t, brew, node)

	settings := config.Settings{}
	settings.SetEcosystemPriority("system", []string{"node", "brew"})
	if err := a.SaveSettings(context.Background(), settings); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	got, err := a.DefaultInstallProvider(context.Background())
	if err != nil {
		t.Fatalf("DefaultInstallProvider: %v", err)
	}
	if got != "node" {
		t.Fatalf("DefaultInstallProvider = %q, want node", got)
	}
}

func TestDefaultInstallProviderNoAvailableProvider(t *testing.T) {
	a, _ := newImportApp(t)

	settings := config.Settings{}
	settings.SetEcosystemPriority("system", []string{"__nonexistent__"})
	if err := a.SaveSettings(context.Background(), settings); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	_, err := a.DefaultInstallProvider(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no provider available") {
		t.Fatalf("DefaultInstallProvider err = %v, want no provider available", err)
	}
}

package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
)

// Redirects ConfigPath to a read-only directory so any later saveConfig fails (atomicWrite cannot create temp files there); the open DB handle is unaffected.
func redirectToReadOnlyConfig(t *testing.T, a *app.App) {
	t.Helper()
	parent := t.TempDir()
	cfgDir := filepath.Join(parent, "readonly")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	roConfig := filepath.Join(cfgDir, "settings.json")
	if err := saveTUIConfig(t, roConfig, &config.RootConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cfgDir, 0o555); err != nil {
		t.Skipf("cannot chmod directory in this environment: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(cfgDir, 0o755) })
	a.ConfigPath = roConfig
}

func TestDoClaim_Success(t *testing.T) {
	t.Parallel()
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)

	msg := m.doClaim("ripgrep", "brew", "", "work")()
	got, ok := msg.(claimDoneMsg)
	if !ok {
		t.Fatalf("expected claimDoneMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	if got.name != "ripgrep" {
		t.Errorf("name = %q, want ripgrep", got.name)
	}
	if got.groupName != "work" {
		t.Errorf("groupName = %q, want work", got.groupName)
	}
	// tools may be nil when DB is empty — just verify no error and name is set.
}

func TestDoClaim_RefreshesToolMembershipState(t *testing.T) {
	t.Parallel()
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)
	key := toolKey("ripgrep", "brew")
	m.toolGroups = map[string]string{key: ""}
	m.toolMemberships = map[string][]string{}

	msg := m.doClaim("ripgrep", "brew", "", "work")()
	got, ok := msg.(claimDoneMsg)
	if !ok {
		t.Fatalf("expected claimDoneMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}
	if got.toolGroups[key] != "work" {
		t.Fatalf("claim toolGroups[%q] = %q, want work", key, got.toolGroups[key])
	}
	if !slices.Contains(got.toolMemberships[key], "work") {
		t.Fatalf("claim memberships = %v, want work", got.toolMemberships[key])
	}

	m.handleClaimDoneMsg(got)
	if m.toolGroups[key] != "work" {
		t.Fatalf("model toolGroups[%q] = %q, want work", key, m.toolGroups[key])
	}
	if !slices.Contains(m.toolMemberships[key], "work") {
		t.Fatalf("model memberships = %v, want work", m.toolMemberships[key])
	}
}

func TestHandleClaimDoneMsg_ErrorStillRefreshesClaim(t *testing.T) {
	t.Parallel()
	m := modelForCmds(nil)
	key := toolKey("ripgrep", "system")
	m.discoveredTools = []*app.ToolView{{Name: "ripgrep", Provider: "system", Installed: true}}
	m.rebuildDiscoveredKeys()
	msg := claimDoneMsg{
		err:             errors.New("host update failed"),
		name:            "ripgrep",
		groupName:       "work",
		tools:           []*app.ToolView{{Name: "ripgrep", Provider: "system", Installed: true, Tracked: true}},
		toolGroups:      map[string]string{key: "work"},
		toolMemberships: map[string][]string{key: {"work"}},
		groupNames:      []string{"work"},
	}

	m.handleClaimDoneMsg(msg)
	if m.toolGroups[key] != "work" {
		t.Fatalf("model toolGroups[%q] = %q, want work despite host error", key, m.toolGroups[key])
	}
	if len(m.discoveredTools) != 0 {
		t.Fatalf("claimed tool should leave discovered list after partial success: %+v", m.discoveredTools)
	}
}

func TestHandleClaimDoneMsg_RemovesDiscoveredBeforeFiltering(t *testing.T) {
	t.Parallel()
	m := modelForCmds(nil)
	key := toolKey("swiftlint", "system")
	swiftformat := &app.ToolView{Name: "swiftformat", Provider: "system", Package: "swiftformat", Installed: true, Tracked: true}
	orphan := &app.ToolView{Name: "swiftlint", Provider: "system", Package: "swiftlint", Installed: true, Tracked: false}
	m.allTools = []*app.ToolView{swiftformat, orphan}
	m.discoveredTools = []*app.ToolView{orphan}
	m.rebuildDiscoveredKeys()
	m.applyFilter()
	if len(m.visibleTools) != 2 || m.visibleTools[0].Name != "swiftlint" {
		t.Fatalf("precondition visible order = %+v, want out-of-sync swiftlint first", m.visibleTools)
	}

	claimed := &app.ToolView{Name: "swiftlint", Provider: "system", Package: "swiftlint", Installed: true, Tracked: true}
	m.handleClaimDoneMsg(claimDoneMsg{
		name:            "swiftlint",
		groupName:       "dev",
		tools:           []*app.ToolView{swiftformat, claimed},
		toolGroups:      map[string]string{key: "dev"},
		toolMemberships: map[string][]string{key: {"dev"}},
		groupNames:      []string{"dev"},
	})
	if len(m.discoveredTools) != 0 {
		t.Fatalf("claimed tool should leave discovered list: %+v", m.discoveredTools)
	}
	if got := m.countSection(sectionOutOfSync); got != 0 {
		t.Fatalf("out-of-sync count after claim = %d, want 0", got)
	}
	if len(m.visibleTools) != 2 || m.visibleTools[0].Name != "swiftformat" || m.visibleTools[1].Name != "swiftlint" {
		t.Fatalf("visible order after claim = %+v, want installed alphabetical order", m.visibleTools)
	}
}

func TestDoClaim_AddError(t *testing.T) {
	t.Parallel(
	// Redirect ConfigPath to a read-only directory so Add's saveConfig fails.
	)

	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)
	redirectToReadOnlyConfig(t, a)

	m := modelForCmds(a)
	msg := m.doClaim("bat", "brew", "", "")()
	got, ok := msg.(claimDoneMsg)
	if !ok {
		t.Fatalf("expected claimDoneMsg, got %T", msg)
	}
	if got.err == nil {
		t.Error("expected error when config is read-only")
	}
	if got.name != "bat" {
		t.Errorf("name = %q on error, want bat", got.name)
	}
	if got.groupName != "" {
		t.Errorf("groupName = %q on error, want base/empty", got.groupName)
	}
}

// Claiming an orphan node tool must write the concrete "bun" provider, never the "node" family, and the resulting config must validate clean.
func TestDoClaim_NodeToolWritesConcreteProvider(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	bun := &okProvider{name: "bun"}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Groups: []*config.GroupConfig{tuiTestHostGroup()},
	}); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath)
	a.CacheDir = dir
	if err := a.InitTestMode(context.Background(), bun); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	m := modelForCmds(a)
	msg := m.doClaim("tsx", "bun", "bun", "testhost")()
	got, ok := msg.(claimDoneMsg)
	if !ok {
		t.Fatalf("expected claimDoneMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("doClaim error: %v", got.err)
	}
	if got.name != "tsx" {
		t.Errorf("name = %q, want tsx", got.name)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	spec := cfg.Tools["tsx"]
	if len(spec.Providers) != 1 || spec.Providers[0].Provider != "bun" {
		t.Fatalf("tsx spec = %+v, want concrete bun provider (not node family)", spec)
	}
	if errs := config.ValidateRoot(cfg, config.ProviderValidation{}); len(errs) > 0 {
		t.Fatalf("config did not validate clean after single claim: %v", errs)
	}
}

// A single TUI claim must produce the same config shape as the bulk SyncAll path: provider="bun" and no install_with pin.
func TestDoClaim_NodeToolMatchesBulkPath(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	bun := &okProvider{name: "bun"}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Groups: []*config.GroupConfig{tuiTestHostGroup()},
	}); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath)
	a.CacheDir = dir
	if err := a.InitTestMode(context.Background(), bun); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	m := modelForCmds(a)
	msg := m.doClaim("tsx", "bun", "bun", "testhost")()
	got, ok := msg.(claimDoneMsg)
	if !ok {
		t.Fatalf("expected claimDoneMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("doClaim error: %v", got.err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	spec := cfg.Tools["tsx"]

	// Concrete provider written — not the "node" family.
	if len(spec.Providers) != 1 || spec.Providers[0].Provider != "bun" {
		t.Fatalf("tsx spec providers = %+v, want [{Provider:bun}]", spec.Providers)
	}
	// configProvider == installedWith (both "bun"), so ClaimInstallWith returns "" and no explicit pin is needed.
	if spec.Providers[0].InstallWith != "" {
		t.Fatalf("tsx spec install_with = %q, want empty (bun==bun, no pin needed)", spec.Providers[0].InstallWith)
	}
	// Config must validate clean — no "node" family provider lurking.
	if errs := config.ValidateRoot(cfg, config.ProviderValidation{}); len(errs) > 0 {
		t.Fatalf("config did not validate clean after single claim: %v", errs)
	}
	var found bool
	for _, g := range cfg.Groups {
		for _, tool := range g.Tools {
			if tool.Name == "tsx" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("tsx not found in any group after claim; groups = %+v", cfg.Groups)
	}
}

// An orphan installed via pip is claimed as provider="pip", not as the "python" family.
func TestDoClaim_PythonToolWritesConcreteProvider(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	pip := &okProvider{name: "pip"}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Groups: []*config.GroupConfig{tuiTestHostGroup()},
	}); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath)
	a.CacheDir = dir
	if err := a.InitTestMode(context.Background(), pip); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	m := modelForCmds(a)
	msg := m.doClaim("black", "pip", "pip", "testhost")()
	got, ok := msg.(claimDoneMsg)
	if !ok {
		t.Fatalf("expected claimDoneMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("doClaim error: %v", got.err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	spec := cfg.Tools["black"]
	if len(spec.Providers) != 1 || spec.Providers[0].Provider != "pip" {
		t.Fatalf("black spec = %+v, want concrete pip provider (not python family)", spec)
	}
	if errs := config.ValidateRoot(cfg, config.ProviderValidation{}); len(errs) > 0 {
		t.Fatalf("config did not validate clean after pip claim: %v", errs)
	}
}

type installOptionCaptureProvider struct {
	okProvider
	installed []provider.Tool
}

func (p *installOptionCaptureProvider) Install(_ context.Context, tool provider.Tool) error {
	p.installed = append(p.installed, tool)
	return nil
}

func TestDoInstallAndAddTool_PassesSearchOptions(t *testing.T) {
	t.Parallel()
	brew := &installOptionCaptureProvider{okProvider: okProvider{name: "brew"}}
	a, cfgPath := newCmdApp(t, brew, nil)
	m := modelForCmds(a)
	row := &app.ToolView{
		Name:     "visual-studio-code",
		Provider: "brew",
		Options:  map[string]string{"brew_kind": "cask"},
	}

	msg := m.doInstallAndAddTool(row, "work")()
	got, ok := msg.(opCompleteMsg)
	if !ok {
		t.Fatalf("expected opCompleteMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}
	if len(brew.installed) != 1 {
		t.Fatalf("brew installs = %d, want 1", len(brew.installed))
	}
	installed := brew.installed[0]
	if installed.Provider != "brew" {
		t.Fatalf("installed.Provider = %q, want brew", installed.Provider)
	}
	if installed.Options["brew_kind"] != "cask" {
		t.Fatalf("installed.Options[brew_kind] = %q, want cask", installed.Options["brew_kind"])
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	spec := cfg.Tools["visual-studio-code"]
	if len(spec.Providers) != 1 || spec.Providers[0].Provider != "brew" || spec.Providers[0].Options["brew_kind"] != "cask" {
		t.Fatalf("config tool spec = %+v, want brew cask provider-list entry", spec)
	}
}

func TestHandleOpCompleteMsg_ErrorStillRefreshesToolMembershipState(t *testing.T) {
	t.Parallel()
	m := modelForCmds(nil)
	key := toolKey("ripgrep", "system")
	msg := opCompleteMsg{
		err:             errors.New("host update failed"),
		tools:           []*app.ToolView{{Name: "ripgrep", Provider: "system", Installed: true, Tracked: true}},
		toolGroups:      map[string]string{key: "work"},
		toolMemberships: map[string][]string{key: {"work"}},
		groupNames:      []string{"work"},
	}

	m.handleOpCompleteMsg(msg)
	if m.toolGroups[key] != "work" {
		t.Fatalf("model toolGroups[%q] = %q, want work despite host error", key, m.toolGroups[key])
	}
	if !slices.Contains(m.toolMemberships[key], "work") {
		t.Fatalf("model memberships = %v, want work despite host error", m.toolMemberships[key])
	}
}

func TestHandleOpCompleteMsg_ProviderPinsRefreshBeforeFiltering(t *testing.T) {
	t.Parallel()
	tool := &app.ToolView{
		Name:          "ripgrep",
		Provider:      "system",
		Installed:     true,
		InstalledWith: "brew",
		Tracked:       true,
	}
	m := modelForCmds(nil)
	m.allTools = []*app.ToolView{tool}
	m.effectiveSystemManager = "apt"
	m.applyFilter()
	if got := m.countSection(sectionOutOfSync); got != 1 {
		t.Fatalf("precondition out-of-sync count = %d, want 1", got)
	}

	m.handleOpCompleteMsg(opCompleteMsg{
		message:          "pinned ripgrep via this tool everywhere",
		tools:            []*app.ToolView{tool},
		toolProviderPins: map[string]string{"ripgrep": "brew"},
	})
	if got := m.countSection(sectionOutOfSync); got != 0 {
		t.Fatalf("out-of-sync count after pin = %d, want 0", got)
	}
	if got := m.countSection(sectionInstalled); got != 1 {
		t.Fatalf("installed count after pin = %d, want 1", got)
	}
}

func TestDoSetProviderScope_ToolSuccess(t *testing.T) {
	t.Parallel()
	prov := &okProvider{name: "brew"}
	a, cfgPath := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("ripgrep", "brew")})
	m := modelForCmds(a)

	msg := m.doSetProviderScope("ripgrep", scopeOption{kind: "provider-tool", label: "this tool everywhere"}, &app.ToolView{Name: "ripgrep", Provider: "brew", Package: "ripgrep", InstalledWith: "brew"})()
	got, ok := msg.(opCompleteMsg)
	if !ok {
		t.Fatalf("expected opCompleteMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	if !strings.Contains(got.message, "pinned") {
		t.Errorf("message %q does not contain 'pinned'", got.message)
	}
	if !strings.Contains(got.message, "ripgrep") {
		t.Errorf("message %q does not contain tool name", got.message)
	}
	if got.toolProviderPins["ripgrep"] != "" {
		t.Fatalf("toolProviderPins = %v, want reload-equivalent canonical state", got.toolProviderPins)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	spec := cfg.Tools["ripgrep"]
	if len(spec.Providers) != 1 || spec.Providers[0].Provider != "brew" || spec.Providers[0].Package != "ripgrep" {
		t.Fatalf("provider scope was not persisted: %+v", spec)
	}
	if spec.Provider != "" || spec.Package != "" || spec.InstallWith != "" {
		t.Fatalf("legacy fields survived provider scope pin: %+v", spec)
	}
}

func TestDoSetProviderScope_PersistsPackageAlias(t *testing.T) {
	t.Parallel()
	prov := &okProvider{name: "brew"}
	a, cfgPath := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("ripgrep", "brew")})
	m := modelForCmds(a)

	msg := m.doSetProviderScope("ripgrep", scopeOption{kind: "provider-tool", label: "this tool everywhere"}, &app.ToolView{Name: "ripgrep", Provider: "brew", Package: "rg", InstalledWith: "brew"})()
	got, ok := msg.(opCompleteMsg)
	if !ok {
		t.Fatalf("expected opCompleteMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	spec := cfg.Tools["ripgrep"]
	if len(spec.Providers) != 1 || spec.Providers[0].Provider != "brew" || spec.Providers[0].Package != "rg" {
		t.Fatalf("provider scope with package alias was not persisted: %+v", spec)
	}
}

func TestDoSetProviderScope_Error(t *testing.T) {
	t.Parallel()
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil) // empty config — no tools
	m := modelForCmds(a)

	msg := m.doSetProviderScope("notexist", scopeOption{kind: "provider-tool"}, &app.ToolView{Name: "notexist", Provider: "system", InstalledWith: "brew"})()
	got, ok := msg.(opCompleteMsg)
	if !ok {
		t.Fatalf("expected opCompleteMsg, got %T", msg)
	}
	if got.err == nil {
		t.Error("expected error for tool not in config")
	}
}

type describingOKProvider struct {
	okProvider
	desc string
}

func (p *describingOKProvider) Describe(_ context.Context, _ provider.Tool) (string, error) {
	return p.desc, nil
}

func TestDoMigrateProvider_Success(t *testing.T) {
	t.Parallel()
	brew := &okProvider{name: "brew"}
	pip := &describingOKProvider{okProvider: okProvider{name: "pip"}, desc: "Python package installer"}
	// Config declares "pip" but the tool is installed via "brew", so MigrateInstallation calls migrateWrongProvider: install via pip, uninstall from brew, config entry unchanged.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"black": tuiToolSpec("pip"),
		},
		Groups: []*config.GroupConfig{tuiTestHostGroup("black")},
	}); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath)
	a.CacheDir = dir
	if err := a.InitTestMode(context.Background(), brew, pip); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	m := modelForCmds(a)
	msg := m.doMigrateProvider("black", "pip", "brew")()
	got, ok := msg.(migrateProviderDoneMsg)
	if !ok {
		t.Fatalf("expected migrateProviderDoneMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	if got.name != "black" {
		t.Errorf("name = %q, want black", got.name)
	}
	if got.fromProvider != "brew" {
		t.Errorf("fromProvider = %q, want brew", got.fromProvider)
	}
	if got.toProvider != "pip" {
		t.Errorf("toProvider = %q, want pip", got.toProvider)
	}
	if got.tools == nil {
		t.Error("expected non-nil tools list on success")
	}
	if len(got.tools) != 1 || got.tools[0].Description != "Python package installer" {
		t.Fatalf("migrated tools description = %+v, want refreshed provider description", got.tools)
	}
}

func TestDoMigrateProvider_SwitchError(t *testing.T) {
	t.Parallel(
	// "pip" is not registered as a provider → Switch returns "unknown provider" error.
	)

	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("black", "pip")})
	m := modelForCmds(a)

	// installedWith="pip" is not registered → Switch fails immediately.
	msg := m.doMigrateProvider("black", "brew", "pip")()
	got, ok := msg.(migrateProviderDoneMsg)
	if !ok {
		t.Fatalf("expected migrateProviderDoneMsg, got %T", msg)
	}
	if got.err == nil {
		t.Error("expected error for unregistered provider")
	}
	if got.name != "black" {
		t.Errorf("name = %q on error, want black", got.name)
	}
	if got.fromProvider != "pip" {
		t.Errorf("fromProvider = %q on error, want pip", got.fromProvider)
	}
	if got.toProvider != "brew" {
		t.Errorf("toProvider = %q on error, want brew", got.toProvider)
	}
}

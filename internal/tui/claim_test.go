package tui

// Tests for doClaim, scoped ignore/provider actions, doInstallAndAdd, and doMigrateProvider.

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
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

// redirectToReadOnlyConfig redirects a's ConfigPath to a freshly created
// read-only directory so that any subsequent saveConfig call fails (atomicWrite
// cannot create temp files in the locked directory).  The open DB handle is
// unaffected.  Returns a cleanup func that restores directory permissions.
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

// ── doClaim ───────────────────────────────────────────────────────────────────

func TestDoClaim_Success(t *testing.T) {
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
	m := modelForCmds(nil)
	key := toolKey("ripgrep", "system")
	m.discoveredTools = []*database.ToolCache{{Name: "ripgrep", Provider: "system", Installed: true}}
	m.rebuildDiscoveredKeys()
	msg := claimDoneMsg{
		err:             errors.New("host update failed"),
		name:            "ripgrep",
		groupName:       "work",
		tools:           []*database.ToolCache{{Name: "ripgrep", Provider: "system", Installed: true, Tracked: true}},
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
	m := modelForCmds(nil)
	key := toolKey("swiftlint", "system")
	swiftformat := &database.ToolCache{Name: "swiftformat", Provider: "system", Package: "swiftformat", Installed: true, Tracked: true}
	orphan := &database.ToolCache{Name: "swiftlint", Provider: "system", Package: "swiftlint", Installed: true, Tracked: false}
	m.allTools = []*database.ToolCache{swiftformat, orphan}
	m.discoveredTools = []*database.ToolCache{orphan}
	m.rebuildDiscoveredKeys()
	m.applyFilter()
	if len(m.visibleTools) != 2 || m.visibleTools[0].Name != "swiftlint" {
		t.Fatalf("precondition visible order = %+v, want out-of-sync swiftlint first", m.visibleTools)
	}

	claimed := &database.ToolCache{Name: "swiftlint", Provider: "system", Package: "swiftlint", Installed: true, Tracked: true}
	m.handleClaimDoneMsg(claimDoneMsg{
		name:            "swiftlint",
		groupName:       "dev",
		tools:           []*database.ToolCache{swiftformat, claimed},
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
	// Redirect ConfigPath to a read-only directory so Add's saveConfig fails.
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

// ── doClaim — ecosystem tool coverage ────────────────────────────────────────

// TestDoClaim_NodeToolWritesConcreteProvider verifies that claiming an orphan
// node tool (Provider=bun, InstalledWith=bun) writes the concrete "bun" provider
// to config — never the "node" family — and that the resulting config validates
// clean. This mirrors the assertion in the bulk TestSyncAll_ClaimNodeToolWritesConcreteNotFamily.
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

// TestDoClaim_NodeToolMatchesBulkPath confirms that a single TUI claim for a
// node ecosystem orphan produces identical config shape as the bulk SyncAll path
// tested in TestSyncAll_ClaimNodeToolWritesConcreteNotFamily: provider="bun",
// no install_with pin (bun==bun so ClaimInstallWith returns ""), valid config.
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
	// When configProvider==installedWith (both "bun"), ClaimInstallWith returns ""
	// — no explicit install_with pin is needed.
	if spec.Providers[0].InstallWith != "" {
		t.Fatalf("tsx spec install_with = %q, want empty (bun==bun, no pin needed)", spec.Providers[0].InstallWith)
	}
	// Config must validate clean — no "node" family provider lurking.
	if errs := config.ValidateRoot(cfg, config.ProviderValidation{}); len(errs) > 0 {
		t.Fatalf("config did not validate clean after single claim: %v", errs)
	}
	// Tool appears in the requested group.
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

// TestDoClaim_PythonToolWritesConcreteProvider exercises the pip ecosystem:
// an orphan installed via pip is claimed as provider="pip" and must not be
// written as the "python" family.
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

// ── doSetIgnoreScope ─────────────────────────────────────────────────────────

func TestDoSetIgnoreScope_ToolSuccess(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, cfgPath := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("curl", "brew")})
	m := modelForCmds(a)

	msg := m.doSetIgnoreScope("curl", scopeOption{kind: "tool"})()
	got, ok := msg.(ignoreDoneMsg)
	if !ok {
		t.Fatalf("expected ignoreDoneMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	if got.name != "curl" {
		t.Errorf("name = %q, want curl", got.name)
	}
	if !got.ignored {
		t.Error("ignored = false, want true")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !cfg.Tools["curl"].Ignore {
		t.Fatalf("tool-level ignore was not persisted: %+v", cfg.Tools["curl"])
	}
}

func TestDoSetIgnoreScope_HostUsesGlobalIgnore(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("curl", "brew")})
	m := modelForCmds(a)
	m.hostInfo = &app.HostInfo{Active: "nonexistent"}

	msg := m.doSetIgnoreScope("curl", scopeOption{kind: "host"})()
	got, ok := msg.(ignoreDoneMsg)
	if !ok {
		t.Fatalf("expected ignoreDoneMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}
	if got.name != "curl" {
		t.Errorf("name = %q on error, want curl", got.name)
	}
	if !got.ignored {
		t.Errorf("ignored = %v, want true (mirrors the request)", got.ignored)
	}
}

func TestDoSetIgnoreScope_GroupSuccess(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, cfgPath := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("wget", "brew")})
	m := modelForCmds(a)

	msg := m.doSetIgnoreScope("wget", scopeOption{kind: "group", group: shortHostname()})()
	got, ok := msg.(ignoreDoneMsg)
	if !ok {
		t.Fatalf("expected ignoreDoneMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	if got.name != "wget" {
		t.Errorf("name = %q, want wget", got.name)
	}
	if !got.ignored {
		t.Error("ignored = false, want true")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(cfg.Ignore.Tools) != 1 || cfg.Ignore.Tools[0] != "wget" {
		t.Fatalf("group ignore was not persisted globally: %+v", cfg.Ignore.Tools)
	}
}

// ── doInstallAndAdd ───────────────────────────────────────────────────────────

func TestDoInstallAndAdd_InstallError(t *testing.T) {
	prov := &errProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)

	msg := m.doInstallAndAdd("ripgrep", "brew")()
	got, ok := msg.(opCompleteMsg)
	if !ok {
		t.Fatalf("expected opCompleteMsg, got %T", msg)
	}
	if got.err == nil {
		t.Error("expected error from failing install")
	}
}

func TestDoInstallAndAdd_AddError(t *testing.T) {
	// Install succeeds (okProvider) but Add (config write) fails because
	// ConfigPath is redirected to a read-only directory.
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)
	redirectToReadOnlyConfig(t, a)

	m := modelForCmds(a)
	msg := m.doInstallAndAdd("ripgrep", "brew")()
	got, ok := msg.(opCompleteMsg)
	if !ok {
		t.Fatalf("expected opCompleteMsg, got %T", msg)
	}
	// Install succeeded but config save failed. Surfaced as err so the status
	// bar renders red — pretending success would hide the recoverable failure.
	if got.err == nil {
		t.Fatal("expected error field for partial-success (install ok, config save failed)")
	}
	if !strings.Contains(got.err.Error(), "config save failed") {
		t.Errorf("err %q does not contain 'config save failed'", got.err.Error())
	}
	if !strings.Contains(got.err.Error(), "installed ripgrep") {
		t.Errorf("err %q does not preserve install-succeeded context", got.err.Error())
	}
}

func TestDoInstallAndAdd_RefreshesToolMembershipState(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)
	key := toolKey("ripgrep", "brew")

	msg := m.doInstallAndAdd("ripgrep", "brew", "work")()
	got, ok := msg.(opCompleteMsg)
	if !ok {
		t.Fatalf("expected opCompleteMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}
	if !slices.Contains(got.removeDiscoveredKeys, toolKey("ripgrep", "brew")) {
		t.Fatalf("removeDiscoveredKeys = %v, want ripgrep/brew", got.removeDiscoveredKeys)
	}
	if got.toolGroups[key] != "work" {
		t.Fatalf("install-and-add toolGroups[%q] = %q, want work", key, got.toolGroups[key])
	}
	if !slices.Contains(got.toolMemberships[key], "work") {
		t.Fatalf("install-and-add memberships = %v, want work", got.toolMemberships[key])
	}

	m.handleOpCompleteMsg(got)
	if m.toolGroups[key] != "work" {
		t.Fatalf("model toolGroups[%q] = %q, want work", key, m.toolGroups[key])
	}
	if !slices.Contains(m.toolMemberships[key], "work") {
		t.Fatalf("model memberships = %v, want work", m.toolMemberships[key])
	}
}

func TestDoInstallAndAdd_PreservesCachedSearchMetadata(t *testing.T) {
	ctx := context.Background()
	prov := &okProvider{name: "brew"}
	a, cfgPath := newCmdApp(t, prov, nil)
	if err := a.DB().UpsertMetadataBatch(ctx, []database.MetadataUpdate{{
		Name:        "ripgrep",
		Provider:    "brew",
		Package:     "ripgrep",
		Description: "fast grep",
		SourceType:  provider.SourceTypeGitHub,
		SourceOwner: "BurntSushi",
		SourceRepo:  "ripgrep",
		SourceURL:   "https://github.com/BurntSushi/ripgrep",
	}}); err != nil {
		t.Fatalf("UpsertMetadataBatch: %v", err)
	}
	m := modelForCmds(a)

	msg := m.doInstallAndAdd("ripgrep", "brew")()
	got, ok := msg.(opCompleteMsg)
	if !ok {
		t.Fatalf("expected opCompleteMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}
	if len(got.tools) != 1 {
		t.Fatalf("tools = %d, want one installed tool", len(got.tools))
	}
	if !got.tools[0].Description.Valid || got.tools[0].Description.String != "fast grep" {
		t.Fatalf("installed row description = %+v, want cached search metadata", got.tools[0].Description)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg.Tools["ripgrep"].Git; got != "https://github.com/BurntSushi/ripgrep" {
		t.Fatalf("tool git = %q, want cached search GitHub source", got)
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
	brew := &installOptionCaptureProvider{okProvider: okProvider{name: "brew"}}
	a, cfgPath := newCmdApp(t, brew, nil)
	m := modelForCmds(a)
	row := &database.ToolCache{
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
	m := modelForCmds(nil)
	key := toolKey("ripgrep", "system")
	msg := opCompleteMsg{
		err:             errors.New("host update failed"),
		tools:           []*database.ToolCache{{Name: "ripgrep", Provider: "system", Installed: true, Tracked: true}},
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
	tool := &database.ToolCache{
		Name:          "ripgrep",
		Provider:      "system",
		Installed:     true,
		InstalledWith: "brew",
		Tracked:       true,
	}
	m := modelForCmds(nil)
	m.allTools = []*database.ToolCache{tool}
	m.effectiveSystemManager = "apt"
	m.applyFilter()
	if got := m.countSection(sectionOutOfSync); got != 1 {
		t.Fatalf("precondition out-of-sync count = %d, want 1", got)
	}

	m.handleOpCompleteMsg(opCompleteMsg{
		message:          "pinned ripgrep via this tool everywhere",
		tools:            []*database.ToolCache{tool},
		toolProviderPins: map[string]string{"ripgrep": "brew"},
	})
	if got := m.countSection(sectionOutOfSync); got != 0 {
		t.Fatalf("out-of-sync count after pin = %d, want 0", got)
	}
	if got := m.countSection(sectionInstalled); got != 1 {
		t.Fatalf("installed count after pin = %d, want 1", got)
	}
}

// ── doSetProviderScope ────────────────────────────────────────────────────────

func TestDoSetProviderScope_ToolSuccess(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, cfgPath := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("ripgrep", "brew")})
	m := modelForCmds(a)

	msg := m.doSetProviderScope("ripgrep", scopeOption{kind: "provider-tool", label: "this tool everywhere"}, &database.ToolCache{Name: "ripgrep", Provider: "brew", Package: "ripgrep", InstalledWith: "brew"})()
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
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	spec := cfg.Tools["ripgrep"]
	if len(spec.Providers) != 1 || spec.Providers[0].Provider != "brew" || spec.Providers[0].Package != "ripgrep" {
		t.Fatalf("provider scope was not persisted: %+v", spec)
	}
}

func TestDoSetProviderScope_PersistsPackageAlias(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, cfgPath := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("ripgrep", "brew")})
	m := modelForCmds(a)

	msg := m.doSetProviderScope("ripgrep", scopeOption{kind: "provider-tool", label: "this tool everywhere"}, &database.ToolCache{Name: "ripgrep", Provider: "brew", Package: "rg", InstalledWith: "brew"})()
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
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil) // empty config — no tools
	m := modelForCmds(a)

	msg := m.doSetProviderScope("notexist", scopeOption{kind: "provider-tool"}, &database.ToolCache{Name: "notexist", Provider: "system", InstalledWith: "brew"})()
	got, ok := msg.(opCompleteMsg)
	if !ok {
		t.Fatalf("expected opCompleteMsg, got %T", msg)
	}
	if got.err == nil {
		t.Error("expected error for tool not in config")
	}
}

// ── doMigrateProvider ─────────────────────────────────────────────────────────

type describingOKProvider struct {
	okProvider
	desc string
}

func (p *describingOKProvider) Describe(_ context.Context, _ provider.Tool) (string, error) {
	return p.desc, nil
}

func TestDoMigrateProvider_Success(t *testing.T) {
	brew := &okProvider{name: "brew"}
	pip := &describingOKProvider{okProvider: okProvider{name: "pip"}, desc: "Python package installer"}
	// Real-world scenario: config already declares "pip" as the intended provider
	// (configProv), but the tool is physically installed via "brew" (installedWith).
	// MigrateInstallation detects that "brew" is a registered-but-different provider,
	// so it calls migrateWrongProvider: install via pip, uninstall from brew, config
	// entry stays unchanged (already "pip").
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
	// configProv="pip" (config-declared provider), installedWith="brew" (wrong provider).
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
	if len(got.tools) != 1 || !got.tools[0].Description.Valid || got.tools[0].Description.String != "Python package installer" {
		t.Fatalf("migrated tools description = %+v, want refreshed provider description", got.tools)
	}
}

func TestDoMigrateProvider_SwitchError(t *testing.T) {
	// "pip" is not registered as a provider → Switch returns "unknown provider" error.
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

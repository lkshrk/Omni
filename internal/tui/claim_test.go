package tui

// Tests for doClaim, scoped ignore/provider actions, doInstallAndAdd, and doMigrateProvider.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
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
	prov := &okProvider{name: "system"}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)

	msg := m.doClaim("ripgrep", "system", "work")()
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

func TestDoClaim_AddError(t *testing.T) {
	// Redirect ConfigPath to a read-only directory so Add's saveConfig fails.
	prov := &okProvider{name: "system"}
	a, _ := newCmdApp(t, prov, nil)
	redirectToReadOnlyConfig(t, a)

	m := modelForCmds(a)
	msg := m.doClaim("bat", "system", "")()
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

func TestDoSetIgnoreScope_ProfileError(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("curl", "brew")})
	m := modelForCmds(a)
	m.profileInfo = &app.ProfileInfo{Active: "nonexistent"}

	msg := m.doSetIgnoreScope("curl", scopeOption{kind: "profile"})()
	got, ok := msg.(ignoreDoneMsg)
	if !ok {
		t.Fatalf("expected ignoreDoneMsg, got %T", msg)
	}
	if got.err == nil {
		t.Error("expected error for unknown profile")
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

	msg := m.doSetIgnoreScope("wget", scopeOption{kind: "group", group: "base"})()
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
	base := findTestGroup(cfg, "base")
	if base == nil || len(base.Ignore) != 1 || base.Ignore[0] != "wget" {
		t.Fatalf("group ignore was not persisted: %+v", base)
	}
}

// ── doInstallAndAdd ───────────────────────────────────────────────────────────

// TestDoInstallAndAdd_AddRejectsConcrete verifies that passing a concrete
// provider name (e.g. "brew") to Add returns the partial-success error path:
// install runs, Add rejects the provider as non-ecosystem, and the resulting
// opCompleteMsg surfaces the failure via err — not as a green ✓ message.
func TestDoInstallAndAdd_AddRejectsConcrete(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)

	msg := m.doInstallAndAdd("ripgrep", "brew")()
	got, ok := msg.(opCompleteMsg)
	if !ok {
		t.Fatalf("expected opCompleteMsg, got %T", msg)
	}
	if got.err == nil {
		t.Fatal("expected err for partial-success (install ok, Add rejected concrete provider)")
	}
	if !strings.Contains(got.err.Error(), "ripgrep") {
		t.Errorf("err %q does not mention ripgrep", got.err.Error())
	}
	if !strings.Contains(got.err.Error(), "config save failed") {
		t.Errorf("err %q does not mention config save failure", got.err.Error())
	}
}

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

// ── doSetProviderScope ────────────────────────────────────────────────────────

func TestDoSetProviderScope_ToolSuccess(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, cfgPath := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("ripgrep", "brew")})
	m := modelForCmds(a)

	msg := m.doSetProviderScope("ripgrep", scopeOption{kind: "provider-tool", label: "this tool everywhere"}, &database.ToolCache{Name: "ripgrep", Provider: "system", Package: "ripgrep", InstalledWith: "brew"})()
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
	if spec.Provider != "system" || spec.InstallWith != "brew" {
		t.Fatalf("provider scope was not persisted: %+v", spec)
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

func TestDoMigrateProvider_Success(t *testing.T) {
	brew := &okProvider{name: "brew"}
	pip := &okProvider{name: "pip"}
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
		Groups: []*config.GroupConfig{{
			Tools: []config.ToolEntry{{Name: "black"}},
		}},
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

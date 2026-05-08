package python

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

// uvRule is a shorthand for a successful uv --version probe.
func uvOK() executor.MatchRule {
	return executor.MatchRule{Pattern: "uv --version", Response: executor.MockCall{Stdout: "uv 0.1.0"}}
}

func uvMissing() executor.MatchRule {
	return executor.MatchRule{Pattern: "uv --version", Response: executor.MockCall{Err: errors.New("not found")}}
}

func pip3OK() executor.MatchRule {
	return executor.MatchRule{Pattern: "pip3 --version", Response: executor.MockCall{Stdout: "pip 23.0"}}
}

func pip3Missing() executor.MatchRule {
	return executor.MatchRule{Pattern: "pip3 --version", Response: executor.MockCall{Err: errors.New("not found")}}
}

func tool(name string) provider.Tool {
	return provider.Tool{Name: name, Provider: "python", Package: name}
}

// ── New / Name / Description ──────────────────────────────────────────────────

func TestNew_ReturnsNonNil(t *testing.T) {
	m := executor.NewMatchMock()
	p := New(m, "")
	if p == nil {
		t.Fatal("New() returned nil")
	}
}

func TestName(t *testing.T) {
	p := New(executor.NewMatchMock(), "")
	if got := p.Name(); got != "python" {
		t.Errorf("Name() = %q, want python", got)
	}
}

func TestDescription_NonEmpty(t *testing.T) {
	p := New(executor.NewMatchMock(), "")
	if p.Description() == "" {
		t.Error("Description() is empty")
	}
}

// ── Available ─────────────────────────────────────────────────────────────────

func TestAvailable_UVFound(t *testing.T) {
	m := executor.NewMatchMock(uvOK())
	p := New(m, "uv")
	ok, err := p.Available(context.Background())
	if err != nil || !ok {
		t.Errorf("Available() = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestAvailable_FallbackToPip3(t *testing.T) {
	m := executor.NewMatchMock(uvMissing(), pip3OK())
	p := New(m, "")
	ok, err := p.Available(context.Background())
	if err != nil || !ok {
		t.Errorf("Available() = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestAvailable_NoneFound(t *testing.T) {
	m := executor.NewMatchMock().WithFallback(executor.MockCall{Err: errors.New("not found")})
	p := New(m, "")
	ok, err := p.Available(context.Background())
	if err != nil || ok {
		t.Errorf("Available() = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestAvailable_HintMissing(t *testing.T) {
	m := executor.NewMatchMock(executor.MatchRule{
		Pattern:  "pip3 --version",
		Response: executor.MockCall{Err: errors.New("not found")},
	})
	p := New(m, "pip3")
	ok, err := p.Available(context.Background())
	if err != nil || ok {
		t.Errorf("Available() = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestAvailable_HintUnknown(t *testing.T) {
	m := executor.NewMatchMock().WithFallback(executor.MockCall{Stdout: "ok"})
	p := New(m, "badmanager")
	ok, err := p.Available(context.Background())
	if err != nil || ok {
		t.Errorf("Available() = (%v, %v), want (false, nil)", ok, err)
	}
}

// ── Install ───────────────────────────────────────────────────────────────────

func TestInstall_UV(t *testing.T) {
	m := executor.NewMatchMock(
		uvOK(),
		executor.MatchRule{Pattern: "uv tool install black", Response: executor.MockCall{}},
	)
	p := New(m, "uv")
	if err := p.Install(context.Background(), tool("black")); err != nil {
		t.Fatalf("Install: %v", err)
	}
	m.AssertCalled(t, "uv tool install black")
}

func TestInstall_Pip3(t *testing.T) {
	m := executor.NewMatchMock(
		uvMissing(),
		pip3OK(),
		executor.MatchRule{Pattern: "pip3 install black", Response: executor.MockCall{}},
	)
	p := New(m, "")
	if err := p.Install(context.Background(), tool("black")); err != nil {
		t.Fatalf("Install: %v", err)
	}
	m.AssertCalled(t, "pip3 install black")
}

func TestInstall_Error(t *testing.T) {
	m := executor.NewMatchMock(
		uvOK(),
		executor.MatchRule{Pattern: "uv tool install bad", Response: executor.MockCall{Err: errors.New("exit 1"), Stderr: "package not found"}},
	)
	p := New(m, "uv")
	if err := p.Install(context.Background(), tool("bad")); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestInstall_Pip3ExternallyManagedPython(t *testing.T) {
	m := executor.NewMatchMock(
		pip3OK(),
		executor.MatchRule{
			Pattern:  "pip3 install black",
			Response: executor.MockCall{Err: errors.New("exit 1"), Stderr: "error: externally-managed-environment\n\n× This environment is externally managed"},
		},
	)
	p := New(m, "pip3")
	err := p.Install(context.Background(), tool("black"))
	if err == nil {
		t.Fatal("expected externally managed error")
	}
	actionErr, ok := provider.ActionErrorFrom(err)
	if !ok {
		t.Fatalf("ActionErrorFrom ok = false for %T", err)
	}
	if actionErr.Code != provider.ErrorExternallyManagedPython {
		t.Fatalf("Code = %q, want %q", actionErr.Code, provider.ErrorExternallyManagedPython)
	}
	if len(actionErr.Solutions) == 0 || actionErr.Solutions[0].Command != "omni switch black --from python --to uv" {
		t.Fatalf("solutions = %#v, want uv switch command", actionErr.Solutions)
	}
	if actionErr.Solutions[0].Action != provider.ErrorSolutionActionSwitchProvider || actionErr.Solutions[0].TargetProvider != "uv" {
		t.Fatalf("solution action = %#v, want switch to uv", actionErr.Solutions[0])
	}
	if strings.Contains(err.Error(), "This environment") {
		t.Fatalf("summary should not include raw stderr: %q", err.Error())
	}
}

// ── Uninstall ─────────────────────────────────────────────────────────────────

func TestUninstall_UV(t *testing.T) {
	m := executor.NewMatchMock(
		uvOK(),
		executor.MatchRule{Pattern: "uv tool uninstall black", Response: executor.MockCall{}},
	)
	p := New(m, "uv")
	if err := p.Uninstall(context.Background(), tool("black")); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	m.AssertCalled(t, "uv tool uninstall black")
}

func TestUninstall_Pip3(t *testing.T) {
	m := executor.NewMatchMock(
		uvMissing(),
		pip3OK(),
		executor.MatchRule{Pattern: "pip3 uninstall -y black", Response: executor.MockCall{}},
	)
	p := New(m, "")
	if err := p.Uninstall(context.Background(), tool("black")); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	m.AssertCalled(t, "pip3 uninstall -y black")
}

// ── Upgrade ───────────────────────────────────────────────────────────────────

func TestUpgrade_UV(t *testing.T) {
	m := executor.NewMatchMock(
		uvOK(),
		executor.MatchRule{Pattern: "uv tool upgrade black", Response: executor.MockCall{}},
	)
	p := New(m, "uv")
	if err := p.Upgrade(context.Background(), tool("black")); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	m.AssertCalled(t, "uv tool upgrade black")
}

func TestUpgrade_Pip3(t *testing.T) {
	m := executor.NewMatchMock(
		uvMissing(),
		pip3OK(),
		executor.MatchRule{Pattern: "pip3 install --upgrade black", Response: executor.MockCall{}},
	)
	p := New(m, "")
	if err := p.Upgrade(context.Background(), tool("black")); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	m.AssertCalled(t, "pip3 install --upgrade black")
}

func TestUpgradeWithManager_UsesInstalledManager(t *testing.T) {
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "pip3 install --upgrade black", Response: executor.MockCall{}},
	)
	p := New(m, "uv")
	if err := p.UpgradeWithManager(context.Background(), tool("black"), "pip3"); err != nil {
		t.Fatalf("UpgradeWithManager: %v", err)
	}
	m.AssertCalled(t, "pip3 install --upgrade black")
	if len(m.CallsMatching("uv tool upgrade")) > 0 {
		t.Fatal("should not upgrade through active uv manager when installed manager is pip3")
	}
}

func TestUpgradeWithManager_Pip3ExternallyManagedPython(t *testing.T) {
	m := executor.NewMatchMock(
		executor.MatchRule{
			Pattern:  "pip3 install --upgrade black",
			Response: executor.MockCall{Err: errors.New("exit 1"), Stderr: "error: externally-managed-environment"},
		},
	)
	p := New(m, "uv")
	err := p.UpgradeWithManager(context.Background(), tool("black"), "pip3")
	if err == nil {
		t.Fatal("expected externally managed error")
	}
	if !provider.HasErrorCode(err, provider.ErrorExternallyManagedPython) {
		t.Fatalf("expected ErrorExternallyManagedPython, got %T %v", err, err)
	}
}

// ── IsInstalled ───────────────────────────────────────────────────────────────

const uvToolListOutput = "black v23.12.1\n  - black\nruff v0.1.0\n  - ruff\n"

func TestIsInstalled_UV_Found(t *testing.T) {
	m := executor.NewMatchMock(
		uvOK(),
		executor.MatchRule{Pattern: "uv tool list", Response: executor.MockCall{Stdout: uvToolListOutput}},
	)
	p := New(m, "uv")
	ok, ver, err := p.IsInstalled(context.Background(), tool("black"))
	if err != nil || !ok || ver != "23.12.1" {
		t.Errorf("IsInstalled() = (%v, %q, %v), want (true, 23.12.1, nil)", ok, ver, err)
	}
}

func TestIsInstalled_UV_NotFound(t *testing.T) {
	m := executor.NewMatchMock(
		uvOK(),
		executor.MatchRule{Pattern: "uv tool list", Response: executor.MockCall{Stdout: uvToolListOutput}},
	)
	p := New(m, "uv")
	ok, _, err := p.IsInstalled(context.Background(), tool("nonexistent"))
	if err != nil || ok {
		t.Errorf("IsInstalled() = (%v, _, %v), want (false, nil)", ok, err)
	}
}

func TestIsInstalled_Pip3_Found(t *testing.T) {
	m := executor.NewMatchMock(
		uvMissing(),
		pip3OK(),
		executor.MatchRule{
			Pattern:  "pip3 show black",
			Response: executor.MockCall{Stdout: "Name: black\nVersion: 23.12.1\nSummary: Formatter\n"},
		},
	)
	p := New(m, "")
	ok, ver, err := p.IsInstalled(context.Background(), tool("black"))
	if err != nil || !ok || ver != "23.12.1" {
		t.Errorf("IsInstalled() = (%v, %q, %v), want (true, 23.12.1, nil)", ok, ver, err)
	}
}

func TestIsInstalled_Pip3_NotFound(t *testing.T) {
	m := executor.NewMatchMock(
		uvMissing(),
		pip3OK(),
		executor.MatchRule{
			Pattern:  "pip3 show nonexistent",
			Response: executor.MockCall{Err: errors.New("exit 1")},
		},
	)
	p := New(m, "")
	ok, _, err := p.IsInstalled(context.Background(), tool("nonexistent"))
	if err != nil || ok {
		t.Errorf("IsInstalled() = (%v, _, %v), want (false, nil)", ok, err)
	}
}

// ── ListInstalled ─────────────────────────────────────────────────────────────

func TestListInstalled_UV(t *testing.T) {
	m := executor.NewMatchMock(
		uvOK(),
		executor.MatchRule{Pattern: "uv tool list", Response: executor.MockCall{Stdout: uvToolListOutput}},
	)
	p := New(m, "uv")
	tools, err := p.ListInstalled(context.Background())
	if err != nil {
		t.Fatalf("ListInstalled: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(tools))
	}
	if tools[0].Name != "black" || tools[0].Version != "23.12.1" {
		t.Errorf("tools[0] = {%s %s}, want {black 23.12.1}", tools[0].Name, tools[0].Version)
	}
}

func TestListInstalled_Pip3(t *testing.T) {
	out := `[{"name":"black","version":"23.12.1"},{"name":"flake8","version":"6.1.0"}]`
	m := executor.NewMatchMock(
		uvMissing(),
		pip3OK(),
		executor.MatchRule{Pattern: "pip3 list --not-required --format=json", Response: executor.MockCall{Stdout: out}},
	)
	p := New(m, "")
	tools, err := p.ListInstalled(context.Background())
	if err != nil {
		t.Fatalf("ListInstalled: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(tools))
	}
	if tools[0].Name != "black" || tools[0].Version != "23.12.1" {
		t.Errorf("tools[0] = {%s %s}, want {black 23.12.1}", tools[0].Name, tools[0].Version)
	}
}

func TestListInstalled_Pip3_InvalidJSONReturnsError(t *testing.T) {
	m := executor.NewMatchMock(
		uvMissing(),
		pip3OK(),
		executor.MatchRule{Pattern: "pip3 list --not-required --format=json", Response: executor.MockCall{Stdout: "not json"}},
	)
	p := New(m, "")
	if _, err := p.ListInstalled(context.Background()); err == nil {
		t.Fatal("expected invalid pip list JSON error, got nil")
	}
}

// ── InstalledMap ──────────────────────────────────────────────────────────────

func TestInstalledMap_UV(t *testing.T) {
	m := executor.NewMatchMock(
		uvOK(),
		executor.MatchRule{Pattern: "uv tool list", Response: executor.MockCall{Stdout: uvToolListOutput}},
	)
	p := New(m, "uv")
	got, err := p.InstalledMap(context.Background())
	if err != nil {
		t.Fatalf("InstalledMap: %v", err)
	}
	if got["black"] != "23.12.1" {
		t.Errorf("map[black] = %q, want 23.12.1", got["black"])
	}
	if got["ruff"] != "0.1.0" {
		t.Errorf("map[ruff] = %q, want 0.1.0", got["ruff"])
	}
}

func TestInstalledMap_Pip3(t *testing.T) {
	out := `[{"name":"Black","version":"23.12.1"}]`
	m := executor.NewMatchMock(
		uvMissing(),
		pip3OK(),
		executor.MatchRule{Pattern: "pip3 list --not-required --format=json", Response: executor.MockCall{Stdout: out}},
	)
	p := New(m, "")
	got, err := p.InstalledMap(context.Background())
	if err != nil {
		t.Fatalf("InstalledMap: %v", err)
	}
	if got["black"] != "23.12.1" {
		t.Errorf("map[black] = %q, want 23.12.1 (should be lowercased)", got["black"])
	}
}

func TestInstalledMap_Pip3_InvalidJSONReturnsError(t *testing.T) {
	m := executor.NewMatchMock(
		uvMissing(),
		pip3OK(),
		executor.MatchRule{Pattern: "pip3 list --not-required --format=json", Response: executor.MockCall{Stdout: "not json"}},
	)
	p := New(m, "")
	if _, err := p.InstalledMap(context.Background()); err == nil {
		t.Fatal("expected invalid pip list JSON error, got nil")
	}
}

// ── OutdatedMap ───────────────────────────────────────────────────────────────

func TestOutdatedMap_UV_Found(t *testing.T) {
	m := executor.NewMatchMock(
		uvOK(),
		executor.MatchRule{Pattern: "uv tool list --outdated", Response: executor.MockCall{Stdout: "black v24.1.0 (update available: v24.2.0)\n  - black\n"}},
		executor.MatchRule{Pattern: "pip3 --version", Response: executor.MockCall{Err: errors.New("not found")}},
		executor.MatchRule{Pattern: "pip --version", Response: executor.MockCall{Err: errors.New("not found")}},
	)
	p := New(m, "uv")
	got, err := p.OutdatedMap(context.Background())
	if err != nil {
		t.Fatalf("OutdatedMap: %v", err)
	}
	if got["black"] != "24.2.0" {
		t.Errorf("map[black] = %q, want 24.2.0", got["black"])
	}
}

func TestOutdatedMap_UV_FlagNotSupported(t *testing.T) {
	m := executor.NewMatchMock(
		uvOK(),
		executor.MatchRule{
			Pattern:  "uv tool list --outdated",
			Response: executor.MockCall{Err: errors.New("unknown flag")},
		},
		executor.MatchRule{Pattern: "pip3 --version", Response: executor.MockCall{Err: errors.New("not found")}},
		executor.MatchRule{Pattern: "pip --version", Response: executor.MockCall{Err: errors.New("not found")}},
	)
	p := New(m, "uv")
	got, err := p.OutdatedMap(context.Background())
	if err != nil {
		t.Fatalf("expected nil error for unsupported flag, got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil map, got %v", got)
	}
}

func TestOutdatedMap_Pip3(t *testing.T) {
	out := `[{"name":"black","latest_version":"24.1.0"}]`
	m := executor.NewMatchMock(
		uvMissing(),
		pip3OK(),
		executor.MatchRule{Pattern: "pip3 list --outdated --format=json", Response: executor.MockCall{Stdout: out}},
		executor.MatchRule{Pattern: "pip --version", Response: executor.MockCall{Err: errors.New("not found")}},
	)
	p := New(m, "")
	got, err := p.OutdatedMap(context.Background())
	if err != nil {
		t.Fatalf("OutdatedMap: %v", err)
	}
	if got["black"] != "24.1.0" {
		t.Errorf("map[black] = %q, want 24.1.0", got["black"])
	}
}

func TestOutdatedMap_Pip3_InvalidJSONReturnsError(t *testing.T) {
	m := executor.NewMatchMock(
		uvMissing(),
		pip3OK(),
		executor.MatchRule{Pattern: "pip3 list --outdated --format=json", Response: executor.MockCall{Stdout: "not json"}},
	)
	p := New(m, "")
	if _, err := p.OutdatedMap(context.Background()); err == nil {
		t.Fatal("expected invalid pip outdated JSON error, got nil")
	}
}

func TestOutdatedByManager_PreservesManagerAttribution(t *testing.T) {
	uvOut := "black v24.1.0 (update available: v24.2.0)\n  - black\n"
	pip3Out := `[{"name":"ruff","latest_version":"0.5.0"}]`
	m := executor.NewMatchMock(
		uvOK(),
		executor.MatchRule{Pattern: "uv tool list --outdated", Response: executor.MockCall{Stdout: uvOut}},
		pip3OK(),
		executor.MatchRule{Pattern: "pip3 list --outdated --format=json", Response: executor.MockCall{Stdout: pip3Out}},
		executor.MatchRule{Pattern: "pip --version", Response: executor.MockCall{Err: errors.New("not found")}},
	)
	p := New(m, "uv")
	got, err := p.OutdatedByManager(context.Background())
	if err != nil {
		t.Fatalf("OutdatedByManager: %v", err)
	}
	if got["uv"]["black"] != "24.2.0" {
		t.Fatalf("uv black latest = %q, want 24.2.0", got["uv"]["black"])
	}
	if got["pip3"]["ruff"] != "0.5.0" {
		t.Fatalf("pip3 ruff latest = %q, want 0.5.0", got["pip3"]["ruff"])
	}
}

// ── Describe ──────────────────────────────────────────────────────────────────

func TestDescribe_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := map[string]any{"info": map[string]any{"summary": "The uncompromising code formatter."}}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	m := executor.NewMatchMock()
	p := newWithPyPI(m, "uv", srv.URL, srv.Client())
	desc, err := p.Describe(context.Background(), tool("black"))
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if desc != "The uncompromising code formatter." {
		t.Errorf("Describe() = %q", desc)
	}
}

func TestDescribe_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	m := executor.NewMatchMock()
	p := newWithPyPI(m, "uv", srv.URL, srv.Client())
	desc, err := p.Describe(context.Background(), tool("nonexistent"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if desc != "" {
		t.Errorf("expected empty desc on 404, got %q", desc)
	}
}

func TestDescribe_HTTPError(t *testing.T) {
	m := executor.NewMatchMock()
	p := newWithPyPI(m, "uv", "http://127.0.0.1:0", &http.Client{})
	_, err := p.Describe(context.Background(), tool("black"))
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}

// ── ResolvedName ──────────────────────────────────────────────────────────────

func TestResolvedName_UV(t *testing.T) {
	m := executor.NewMatchMock(uvOK())
	p := New(m, "uv")
	name, err := p.ResolvedName(context.Background())
	if err != nil {
		t.Fatalf("ResolvedName: %v", err)
	}
	if name != "uv" {
		t.Errorf("ResolvedName() = %q, want uv", name)
	}
}

func TestResolvedName_Pip3(t *testing.T) {
	m := executor.NewMatchMock(uvMissing(), pip3OK())
	p := New(m, "")
	name, err := p.ResolvedName(context.Background())
	if err != nil {
		t.Fatalf("ResolvedName: %v", err)
	}
	if name != "pip3" {
		t.Errorf("ResolvedName() = %q, want pip3", name)
	}
}

func TestResolvedName_NoneFound(t *testing.T) {
	m := executor.NewMatchMock().WithFallback(executor.MockCall{Err: errors.New("not found")})
	p := New(m, "")
	name, err := p.ResolvedName(context.Background())
	if err == nil {
		t.Fatal("expected error when no backend found, got nil")
	}
	if name != "" {
		t.Errorf("ResolvedName() = %q on error, want empty", name)
	}
}

// ── UninstallFrom ─────────────────────────────────────────────────────────────

func TestUninstallFrom_UV(t *testing.T) {
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "uv tool uninstall black", Response: executor.MockCall{}},
	)
	p := New(m, "uv")
	if err := p.UninstallFrom(context.Background(), tool("black"), "uv"); err != nil {
		t.Fatalf("UninstallFrom uv: %v", err)
	}
	m.AssertCalled(t, "uv tool uninstall black")
}

func TestUninstallFrom_Pip3(t *testing.T) {
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "pip3 uninstall -y black", Response: executor.MockCall{}},
	)
	p := New(m, "")
	if err := p.UninstallFrom(context.Background(), tool("black"), "pip3"); err != nil {
		t.Fatalf("UninstallFrom pip3: %v", err)
	}
	m.AssertCalled(t, "pip3 uninstall -y black")
}

func TestUninstallFrom_UnknownBinary(t *testing.T) {
	m := executor.NewMatchMock()
	p := New(m, "")
	// Unknown binary is a no-op; should return nil.
	if err := p.UninstallFrom(context.Background(), tool("black"), "cargo"); err != nil {
		t.Fatalf("UninstallFrom unknown binary: expected nil, got %v", err)
	}
}

// ── BulkDescribe ──────────────────────────────────────────────────────────────

const pipShowMulti = "Name: black\nVersion: 23.12.1\nSummary: The uncompromising code formatter.\n---\nName: ruff\nVersion: 0.1.0\nSummary: An extremely fast Python linter.\n"

func TestBulkDescribe_UV(t *testing.T) {
	m := executor.NewMatchMock(
		uvOK(),
		executor.MatchRule{Pattern: "uv pip show black ruff", Response: executor.MockCall{Stdout: pipShowMulti}},
	)
	p := New(m, "uv")
	tools := []provider.Tool{
		{Name: "black", Provider: "python", Package: "black"},
		{Name: "ruff", Provider: "python", Package: "ruff"},
	}
	got, err := p.BulkDescribe(context.Background(), tools)
	if err != nil {
		t.Fatalf("BulkDescribe UV: %v", err)
	}
	if got["black"] != "The uncompromising code formatter." {
		t.Errorf("map[black] = %q", got["black"])
	}
	if got["ruff"] != "An extremely fast Python linter." {
		t.Errorf("map[ruff] = %q", got["ruff"])
	}
}

func TestBulkDescribe_Pip3(t *testing.T) {
	m := executor.NewMatchMock(
		uvMissing(),
		pip3OK(),
		executor.MatchRule{Pattern: "pip3 show black ruff", Response: executor.MockCall{Stdout: pipShowMulti}},
	)
	p := New(m, "")
	tools := []provider.Tool{
		{Name: "black", Provider: "python", Package: "black"},
		{Name: "ruff", Provider: "python", Package: "ruff"},
	}
	got, err := p.BulkDescribe(context.Background(), tools)
	if err != nil {
		t.Fatalf("BulkDescribe Pip3: %v", err)
	}
	if got["black"] != "The uncompromising code formatter." {
		t.Errorf("map[black] = %q", got["black"])
	}
}

func TestBulkDescribe_Empty(t *testing.T) {
	p := New(executor.NewMatchMock(), "")
	got, err := p.BulkDescribe(context.Background(), nil)
	if err != nil || got != nil {
		t.Errorf("BulkDescribe(nil) = (%v, %v), want (nil, nil)", got, err)
	}
}

// ── CLIToolSet ────────────────────────────────────────────────────────────────

func TestCLIToolSet_UV(t *testing.T) {
	m := executor.NewMatchMock(
		uvOK(),
		executor.MatchRule{Pattern: "uv tool list", Response: executor.MockCall{Stdout: uvToolListOutput}},
	)
	p := New(m, "uv")
	set, err := p.CLIToolSet(context.Background())
	if err != nil {
		t.Fatalf("CLIToolSet: %v", err)
	}
	if !set["black"] || !set["ruff"] {
		t.Errorf("expected black and ruff in CLI set, got %v", set)
	}
}

func TestCLIToolSet_Pip3(t *testing.T) {
	script := `{"black":1,"flake8":1}`
	m := executor.NewMatchMock(
		uvMissing(),
		pip3OK(),
		executor.MatchRule{Pattern: "python3 -c", Response: executor.MockCall{Stdout: script}},
	)
	p := New(m, "")
	set, err := p.CLIToolSet(context.Background())
	if err != nil {
		t.Fatalf("CLIToolSet: %v", err)
	}
	if !set["black"] || !set["flake8"] {
		t.Errorf("expected black and flake8 in CLI set, got %v", set)
	}
}

func TestCLIToolSet_Pip3_Error(t *testing.T) {
	m := executor.NewMatchMock(
		uvMissing(),
		pip3OK(),
		executor.MatchRule{Pattern: "python3 -c", Response: executor.MockCall{Err: errors.New("exit 1")}},
	)
	p := New(m, "")
	if _, err := p.CLIToolSet(context.Background()); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCLIToolSet_PipUsesPythonInterpreter(t *testing.T) {
	script := `{"black":1}`
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "pip --version", Response: executor.MockCall{Stdout: "pip 23.0"}},
		executor.MatchRule{Pattern: "python -c", Response: executor.MockCall{Stdout: script}},
	)
	p := New(m, "pip")
	set, err := p.CLIToolSet(context.Background())
	if err != nil {
		t.Fatalf("CLIToolSet: %v", err)
	}
	if !set["black"] {
		t.Errorf("expected black in CLI set, got %v", set)
	}
	m.AssertCalled(t, "python -c")
	if len(m.CallsMatching("python3 -c")) > 0 {
		t.Fatal("pip manager should not probe CLI entry points through python3")
	}
}

// ── InstalledByManager ────────────────────────────────────────────────────────

func TestInstalledByManager_UvOnly(t *testing.T) {
	uvList := "black v24.3.0\n  - black\nruff v0.4.0\n  - ruff\n"
	m := executor.NewMatchMock(
		uvOK(),
		executor.MatchRule{Pattern: "uv tool list", Response: executor.MockCall{Stdout: uvList}},
		executor.MatchRule{Pattern: "pip3 --version", Response: executor.MockCall{Err: errors.New("not found")}},
		executor.MatchRule{Pattern: "pip --version", Response: executor.MockCall{Err: errors.New("not found")}},
	)
	p := New(m, "uv")
	got, err := p.InstalledByManager(context.Background())
	if err != nil {
		t.Fatalf("InstalledByManager: %v", err)
	}
	if e := got["black"]; e.ConcreteManager != "uv" {
		t.Errorf("black.ConcreteManager = %q, want uv", e.ConcreteManager)
	}
	if e := got["ruff"]; e.ConcreteManager != "uv" {
		t.Errorf("ruff.ConcreteManager = %q, want uv", e.ConcreteManager)
	}
}

func TestInstalledByManager_Pip3Only(t *testing.T) {
	pip3List := `[{"name":"black","version":"24.3.0"},{"name":"ruff","version":"0.4.0"}]`
	m := executor.NewMatchMock(
		uvMissing(),
		pip3OK(),
		executor.MatchRule{Pattern: "pip3 list --not-required --format=json", Response: executor.MockCall{Stdout: pip3List}},
		executor.MatchRule{Pattern: "pip --version", Response: executor.MockCall{Err: errors.New("not found")}},
	)
	p := New(m, "")
	got, err := p.InstalledByManager(context.Background())
	if err != nil {
		t.Fatalf("InstalledByManager: %v", err)
	}
	if e := got["black"]; e.ConcreteManager != "pip3" {
		t.Errorf("black.ConcreteManager = %q, want pip3", e.ConcreteManager)
	}
	if e := got["ruff"]; e.ConcreteManager != "pip3" {
		t.Errorf("ruff.ConcreteManager = %q, want pip3", e.ConcreteManager)
	}
}

// TestInstalledByManager_EffectivePriority verifies that when both uv and pip3
// are available and both report the same tool, the effective (configured) manager
// is credited. Also verifies pip3-only tools are attributed to pip3 when uv is
// the effective manager but doesn't own them.
func TestInstalledByManager_EffectivePriority(t *testing.T) {
	uvList := "black v24.3.0\n  - black\n"
	pip3List := `[{"name":"black","version":"23.0.0"},{"name":"ruff","version":"0.4.0"}]`
	m := executor.NewMatchMock(
		uvOK(),
		executor.MatchRule{Pattern: "uv tool list", Response: executor.MockCall{Stdout: uvList}},
		pip3OK(),
		executor.MatchRule{Pattern: "pip3 list --not-required --format=json", Response: executor.MockCall{Stdout: pip3List}},
		executor.MatchRule{Pattern: "pip --version", Response: executor.MockCall{Err: errors.New("not found")}},
	)
	p := New(m, "uv") // effective = uv
	got, err := p.InstalledByManager(context.Background())
	if err != nil {
		t.Fatalf("InstalledByManager: %v", err)
	}
	// black: uv is effective and reports it first — uv wins
	if e := got["black"]; e.ConcreteManager != "uv" {
		t.Errorf("black.ConcreteManager = %q, want uv (effective priority)", e.ConcreteManager)
	}
	if e := got["black"]; e.Version != "24.3.0" {
		t.Errorf("black.Version = %q, want 24.3.0", e.Version)
	}
	// ruff: only pip3 has it
	if e := got["ruff"]; e.ConcreteManager != "pip3" {
		t.Errorf("ruff.ConcreteManager = %q, want pip3 (only manager with it)", e.ConcreteManager)
	}
}

func TestInstalledByManager_ReturnsUVListError(t *testing.T) {
	m := executor.NewMatchMock(
		uvOK(),
		executor.MatchRule{Pattern: "uv tool list", Response: executor.MockCall{Err: errors.New("uv failed")}},
	)
	p := New(m, "uv")
	if _, err := p.InstalledByManager(context.Background()); err == nil {
		t.Fatal("expected uv tool list error, got nil")
	}
}

func TestInstalledByManager_ReturnsPipListError(t *testing.T) {
	m := executor.NewMatchMock(
		uvMissing(),
		pip3OK(),
		executor.MatchRule{Pattern: "pip3 list --not-required --format=json", Response: executor.MockCall{Err: errors.New("pip failed")}},
	)
	p := New(m, "")
	if _, err := p.InstalledByManager(context.Background()); err == nil {
		t.Fatal("expected pip3 list error, got nil")
	}
}

func TestInstalledByManager_ReturnsPipListParseError(t *testing.T) {
	m := executor.NewMatchMock(
		uvMissing(),
		pip3OK(),
		executor.MatchRule{Pattern: "pip3 list --not-required --format=json", Response: executor.MockCall{Stdout: "not json"}},
	)
	p := New(m, "")
	if _, err := p.InstalledByManager(context.Background()); err == nil {
		t.Fatal("expected pip3 list parse error, got nil")
	}
}

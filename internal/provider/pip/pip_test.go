package pip_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/provider/pip"
)

func newPip(responses ...executor.MockCall) (*pip.Provider, *executor.MockExecutor) {
	m := &executor.MockExecutor{Responses: responses}
	return pip.New(m), m
}

func tool(name string) provider.Tool {
	return provider.Tool{Name: name, Provider: "pip", Package: name}
}

// --- Available ---

func TestAvailable_True(t *testing.T) {
	p, _ := newPip(executor.MockCall{Stdout: "pip 23.3.1"})
	ok, err := p.Available(context.Background())
	if err != nil || !ok {
		t.Errorf("Available() = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestAvailable_False(t *testing.T) {
	p, _ := newPip(executor.MockCall{Err: errors.New("not found")})
	ok, err := p.Available(context.Background())
	if err != nil || ok {
		t.Errorf("Available() = (%v, %v), want (false, nil)", ok, err)
	}
}

// --- IsInstalled ---

func TestIsInstalled_Found(t *testing.T) {
	output := "Name: black\nVersion: 23.12.1\nSummary: The uncompromising code formatter.\n"
	p, _ := newPip(executor.MockCall{Stdout: output})
	ok, ver, err := p.IsInstalled(context.Background(), tool("black"))
	if err != nil || !ok || ver != "23.12.1" {
		t.Errorf("IsInstalled() = (%v, %q, %v), want (true, 23.12.1, nil)", ok, ver, err)
	}
}

func TestIsInstalled_NotFound(t *testing.T) {
	p, _ := newPip(executor.MockCall{Err: errors.New("exit 1"), Stderr: "not found"})
	ok, _, err := p.IsInstalled(context.Background(), tool("nonexistent"))
	if err != nil || ok {
		t.Errorf("expected (false, nil), got (%v, _, %v)", ok, err)
	}
}

// --- Install ---

func TestInstall_CallsCorrectCommand(t *testing.T) {
	p, m := newPip(executor.MockCall{})
	if err := p.Install(context.Background(), tool("black")); err != nil {
		t.Fatalf("Install: %v", err)
	}
	args := m.Calls[0].Args
	if args[0] != "install" || args[1] != "black" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestInstall_Error(t *testing.T) {
	p, _ := newPip(executor.MockCall{Err: errors.New("exit 1")})
	if err := p.Install(context.Background(), tool("bad")); err == nil {
		t.Fatal("expected error")
	}
}

// --- Uninstall ---

func TestUninstall_CallsYesFlag(t *testing.T) {
	p, m := newPip(executor.MockCall{})
	_ = p.Uninstall(context.Background(), tool("black"))
	args := m.Calls[0].Args
	if args[0] != "uninstall" || args[1] != "-y" {
		t.Errorf("expected 'uninstall -y', got %v", args)
	}
}

// --- Upgrade ---

func TestUpgrade_CallsInstallUpgrade(t *testing.T) {
	p, m := newPip(executor.MockCall{})
	_ = p.Upgrade(context.Background(), tool("black"))
	args := m.Calls[0].Args
	if args[0] != "install" || args[1] != "--upgrade" {
		t.Errorf("expected 'install --upgrade', got %v", args)
	}
}

// --- ListInstalled ---

func TestListInstalled(t *testing.T) {
	// pip list --not-required --format=json output.
	output := `[{"name":"black","version":"23.12.1"},{"name":"flake8","version":"6.1.0"}]`
	p, m := newPip(executor.MockCall{Stdout: output})
	tools, err := p.ListInstalled(context.Background())
	if err != nil {
		t.Fatalf("ListInstalled: %v", err)
	}
	if len(tools) != 2 {
		t.Errorf("got %d tools, want 2", len(tools))
	}
	if tools[0].Name != "black" || tools[0].Version != "23.12.1" {
		t.Errorf("unexpected tool: %+v", tools[0])
	}
	// Verify --not-required and --format=json flags were passed.
	args := m.Calls[0].Args
	if args[0] != "list" || args[1] != "--not-required" || args[2] != "--format=json" {
		t.Errorf("unexpected args: %v", args)
	}
}

// --- CLIToolSet ---

func TestCLIToolSet(t *testing.T) {
	output := `{"black":1,"flake8":1}`
	p, m := newPip(executor.MockCall{Stdout: output})
	set, err := p.CLIToolSet(context.Background())
	if err != nil {
		t.Fatalf("CLIToolSet: %v", err)
	}
	if !set["black"] || !set["flake8"] {
		t.Errorf("expected black and flake8 in CLI set, got %v", set)
	}
	call := m.Calls[0]
	if call.Name != "python3" || call.Args[0] != "-c" {
		t.Errorf("expected python3 -c ..., got %s %v", call.Name, call.Args)
	}
}

// --- BulkDescribe ---

func TestBulkDescribe(t *testing.T) {
	output := "Name: black\nVersion: 23.12.1\nSummary: The uncompromising code formatter.\n---\nName: flake8\nVersion: 6.1.0\nSummary: The modular source code checker.\n"
	p, m := newPip(executor.MockCall{Stdout: output})
	tools := []provider.Tool{
		{Name: "black", Provider: "pip", Package: "black"},
		{Name: "flake8", Provider: "pip", Package: "flake8"},
	}
	got, err := p.BulkDescribe(context.Background(), tools)
	if err != nil {
		t.Fatalf("BulkDescribe: %v", err)
	}
	if got["black"] != "The uncompromising code formatter." {
		t.Errorf("map[black] = %q", got["black"])
	}
	if got["flake8"] != "The modular source code checker." {
		t.Errorf("map[flake8] = %q", got["flake8"])
	}
	args := m.Calls[0].Args
	if args[0] != "show" || args[1] != "black" || args[2] != "flake8" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestBulkDescribe_Empty(t *testing.T) {
	p, _ := newPip()
	got, err := p.BulkDescribe(context.Background(), nil)
	if err != nil || got != nil {
		t.Errorf("BulkDescribe(nil) = (%v, %v), want (nil, nil)", got, err)
	}
}

func TestBulkDescribe_Error(t *testing.T) {
	p, _ := newPip(executor.MockCall{Err: errors.New("exit 1")})
	tools := []provider.Tool{{Name: "bad", Provider: "pip", Package: "bad"}}
	if _, err := p.BulkDescribe(context.Background(), tools); err == nil {
		t.Fatal("expected error, got nil")
	}
}

// --- Name / Description ---

func TestName(t *testing.T) {
	p, _ := newPip()
	if got := p.Name(); got != "pip" {
		t.Errorf("Name() = %q, want pip", got)
	}
}

func TestDescription_NonEmpty(t *testing.T) {
	p, _ := newPip()
	if p.Description() == "" {
		t.Error("Description() is empty")
	}
}

// --- InstalledMap ---

func TestInstalledMap_ReturnsTopLevel(t *testing.T) {
	out := `[{"name":"black","version":"23.12.1"},{"name":"flake8","version":"6.1.0"}]`
	p, _ := newPip(executor.MockCall{Stdout: out})
	got, err := p.InstalledMap(context.Background())
	if err != nil {
		t.Fatalf("InstalledMap: %v", err)
	}
	if got["black"] != "23.12.1" {
		t.Errorf("map[black] = %q, want 23.12.1", got["black"])
	}
	if got["flake8"] != "6.1.0" {
		t.Errorf("map[flake8] = %q, want 6.1.0", got["flake8"])
	}
}

func TestInstalledMap_UsesNotRequired(t *testing.T) {
	p, m := newPip(executor.MockCall{Stdout: `[]`})
	if _, err := p.InstalledMap(context.Background()); err != nil {
		t.Fatalf("InstalledMap: %v", err)
	}
	if len(m.Calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(m.Calls))
	}
	call := m.Calls[0]
	if call.Name != "pip3" {
		t.Fatalf("command = %q, want pip3", call.Name)
	}
	want := []string{"list", "--not-required", "--format=json"}
	if len(call.Args) != len(want) {
		t.Fatalf("args = %v, want %v", call.Args, want)
	}
	for i := range want {
		if call.Args[i] != want[i] {
			t.Fatalf("args = %v, want %v", call.Args, want)
		}
	}
}

func TestInstalledMap_Error(t *testing.T) {
	p, _ := newPip(executor.MockCall{Err: errors.New("exit 1")})
	if _, err := p.InstalledMap(context.Background()); err == nil {
		t.Fatal("expected error, got nil")
	}
}

// --- OutdatedMap ---

func TestOutdatedMap_Found(t *testing.T) {
	out := `[{"name":"black","latest_version":"24.1.0"},{"name":"flake8","latest_version":"7.0.0"}]`
	p, _ := newPip(executor.MockCall{Stdout: out})
	got, err := p.OutdatedMap(context.Background())
	if err != nil {
		t.Fatalf("OutdatedMap: %v", err)
	}
	if got["black"] != "24.1.0" {
		t.Errorf("map[black] = %q, want 24.1.0", got["black"])
	}
}

func TestOutdatedMap_Error(t *testing.T) {
	p, _ := newPip(executor.MockCall{Err: errors.New("exit 1")})
	if _, err := p.OutdatedMap(context.Background()); err == nil {
		t.Fatal("expected error, got nil")
	}
}

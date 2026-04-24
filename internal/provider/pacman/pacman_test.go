package pacman_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/provider/pacman"
)

func newPacman(responses ...executor.MockCall) (*pacman.Provider, *executor.MockExecutor) {
	m := &executor.MockExecutor{Responses: responses}
	return pacman.New(m), m
}

func tool(name string) provider.Tool {
	return provider.Tool{Name: name, Provider: "pacman", Package: name}
}

// --- Available ---

func TestAvailable_True(t *testing.T) {
	p, _ := newPacman(executor.MockCall{Stdout: "Pacman v6.0.2"})
	ok, err := p.Available(context.Background())
	if err != nil || !ok {
		t.Errorf("Available() = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestAvailable_False(t *testing.T) {
	p, _ := newPacman(executor.MockCall{Err: errors.New("not found")})
	ok, err := p.Available(context.Background())
	if err != nil || ok {
		t.Errorf("Available() = (%v, %v), want (false, nil)", ok, err)
	}
}

// --- Install ---

func TestInstall_Success(t *testing.T) {
	p, m := newPacman(executor.MockCall{Stdout: "resolving dependencies..."})
	if err := p.Install(context.Background(), tool("ripgrep")); err != nil {
		t.Fatalf("Install: %v", err)
	}
	args := m.Calls[0].Args
	if args[0] != "-S" || args[1] != "--noconfirm" || args[2] != "ripgrep" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestInstall_Error(t *testing.T) {
	p, _ := newPacman(executor.MockCall{Err: errors.New("exit 1"), Stderr: "error: target not found"})
	if err := p.Install(context.Background(), tool("badpkg")); err == nil {
		t.Fatal("expected error, got nil")
	}
}

// --- Name / Description ---

func TestName(t *testing.T) {
	p, _ := newPacman()
	if p.Name() != "pacman" {
		t.Errorf("Name() = %q, want %q", p.Name(), "pacman")
	}
}

func TestDescription(t *testing.T) {
	p, _ := newPacman()
	if p.Description() == "" {
		t.Error("Description() should not be empty")
	}
}

// --- Uninstall ---

func TestUninstall_Success(t *testing.T) {
	p, m := newPacman(executor.MockCall{})
	if err := p.Uninstall(context.Background(), tool("ripgrep")); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if m.Calls[0].Args[0] != "-R" {
		t.Errorf("expected '-R', got %q", m.Calls[0].Args[0])
	}
}

func TestUninstall_Error(t *testing.T) {
	p, _ := newPacman(executor.MockCall{Err: errors.New("exit 1")})
	if err := p.Uninstall(context.Background(), tool("badpkg")); err == nil {
		t.Fatal("expected error, got nil")
	}
}

// --- Upgrade ---

func TestUpgrade_Success(t *testing.T) {
	p, m := newPacman(executor.MockCall{})
	if err := p.Upgrade(context.Background(), tool("ripgrep")); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	// pacman upgrade is -S
	if m.Calls[0].Args[0] != "-S" {
		t.Errorf("expected '-S', got %q", m.Calls[0].Args[0])
	}
}

func TestUpgrade_Error(t *testing.T) {
	p, _ := newPacman(executor.MockCall{Err: errors.New("exit 1")})
	if err := p.Upgrade(context.Background(), tool("badpkg")); err == nil {
		t.Fatal("expected error, got nil")
	}
}

// --- IsInstalled ---

func TestIsInstalled_Found(t *testing.T) {
	p, _ := newPacman(executor.MockCall{Stdout: "ripgrep 15.1.0-2"})
	ok, ver, err := p.IsInstalled(context.Background(), tool("ripgrep"))
	if err != nil || !ok || ver != "15.1.0-2" {
		t.Errorf("IsInstalled() = (%v, %q, %v), want (true, 15.1.0-2, nil)", ok, ver, err)
	}
}

func TestIsInstalled_NotFound(t *testing.T) {
	p, _ := newPacman(executor.MockCall{Err: errors.New("exit 1")})
	ok, _, err := p.IsInstalled(context.Background(), tool("nonexistent"))
	if err != nil || ok {
		t.Errorf("IsInstalled() = (%v, _, %v), want (false, nil)", ok, err)
	}
}

// --- ListInstalled ---

func TestListInstalled(t *testing.T) {
	explicit := "ripgrep\n"
	output := "ripgrep 15.1.0-2\ngit 2.45.0-1\n"
	p, _ := newPacman(executor.MockCall{Stdout: explicit}, executor.MockCall{Stdout: output})
	tools, err := p.ListInstalled(context.Background())
	if err != nil {
		t.Fatalf("ListInstalled: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 explicit tool, got %d", len(tools))
	}
	if tools[0].Name != "ripgrep" || tools[0].Version != "15.1.0-2" {
		t.Errorf("unexpected tool[0]: %+v", tools[0])
	}
}

// --- InstalledMap ---

func TestInstalledMap(t *testing.T) {
	explicit := "Ripgrep\n"
	output := "Ripgrep 15.1.0-2\nGit 2.45.0-1\n"
	p, _ := newPacman(executor.MockCall{Stdout: explicit}, executor.MockCall{Stdout: output})
	m, err := p.InstalledMap(context.Background())
	if err != nil {
		t.Fatalf("InstalledMap: %v", err)
	}
	if v, ok := m["ripgrep"]; !ok || v != "15.1.0-2" {
		t.Errorf("expected ripgrep in map, got %v", m)
	}
	if _, ok := m["git"]; ok {
		t.Errorf("transitive package should not be in map: %v", m)
	}
}

// --- parsePacmanQLine ---

func TestParsePacmanQLine(t *testing.T) {
	tests := []struct {
		line    string
		name    string
		version string
	}{
		{"ripgrep 15.1.0-2", "ripgrep", "15.1.0-2"},
		{"git 2.45.0-1", "git", "2.45.0-1"},
		{"", "", ""},
		{"noversion", "", ""},
	}
	for _, tc := range tests {
		p, _ := newPacman(executor.MockCall{Stdout: tc.name + "\n"}, executor.MockCall{Stdout: tc.line + "\n"})
		tools, _ := p.ListInstalled(context.Background())
		if tc.name == "" {
			if len(tools) != 0 {
				t.Errorf("line %q: expected 0 tools, got %v", tc.line, tools)
			}
		} else {
			if len(tools) != 1 || tools[0].Name != tc.name || tools[0].Version != tc.version {
				t.Errorf("line %q: got %v, want name=%q version=%q", tc.line, tools, tc.name, tc.version)
			}
		}
	}
}

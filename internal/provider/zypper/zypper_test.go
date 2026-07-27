package zypper_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/provider/zypper"
)

func newZypper(responses ...executor.MockCall) (*zypper.Provider, *executor.MockExecutor) {
	m := &executor.MockExecutor{Responses: responses}
	return zypper.New(m), m
}

func tool(name string) provider.Tool {
	return provider.Tool{Name: name, Provider: "zypper", Package: name}
}

func TestAvailable_True(t *testing.T) {
	p, _ := newZypper(executor.MockCall{Stdout: "zypper 1.14.70"})
	ok, err := p.Available(context.Background())
	if err != nil || !ok {
		t.Errorf("Available() = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestAvailable_False(t *testing.T) {
	p, _ := newZypper(executor.MockCall{Err: errors.New("not found")})
	ok, err := p.Available(context.Background())
	if err != nil || ok {
		t.Errorf("Available() = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestInstall_Success(t *testing.T) {
	p, m := newZypper(executor.MockCall{Stdout: "Installing: ripgrep"})
	if err := p.Install(context.Background(), tool("ripgrep")); err != nil {
		t.Fatalf("Install: %v", err)
	}
	args := m.Calls[0].Args
	if args[0] != "install" || args[1] != "-y" || args[2] != "ripgrep" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestInstall_Error(t *testing.T) {
	p, _ := newZypper(executor.MockCall{Err: errors.New("exit 1"), Stderr: "Package not found"})
	if err := p.Install(context.Background(), tool("badpkg")); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestName(t *testing.T) {
	p, _ := newZypper()
	if p.Name() != "zypper" {
		t.Errorf("Name() = %q, want %q", p.Name(), "zypper")
	}
}

func TestDescription(t *testing.T) {
	p, _ := newZypper()
	if p.Description() == "" {
		t.Error("Description() should not be empty")
	}
}

func TestUninstall_Success(t *testing.T) {
	p, m := newZypper(executor.MockCall{})
	if err := p.Uninstall(context.Background(), tool("ripgrep")); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if m.Calls[0].Args[0] != "remove" {
		t.Errorf("expected 'remove', got %q", m.Calls[0].Args[0])
	}
}

func TestUninstall_Error(t *testing.T) {
	p, _ := newZypper(executor.MockCall{Err: errors.New("exit 1")})
	if err := p.Uninstall(context.Background(), tool("badpkg")); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestUpgrade_Success(t *testing.T) {
	p, m := newZypper(executor.MockCall{})
	if err := p.Upgrade(context.Background(), tool("ripgrep")); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if m.Calls[0].Args[0] != "update" {
		t.Errorf("expected 'update', got %q", m.Calls[0].Args[0])
	}
}

func TestUpgrade_Error(t *testing.T) {
	p, _ := newZypper(executor.MockCall{Err: errors.New("exit 1")})
	if err := p.Upgrade(context.Background(), tool("badpkg")); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestIsInstalled_EmptyOutput(t *testing.T) {
	p, _ := newZypper(executor.MockCall{Stdout: ""})
	ok, _, err := p.IsInstalled(context.Background(), tool("pkg"))
	if err != nil || ok {
		t.Errorf("expected not installed for empty output")
	}
}

func TestIsInstalled_Found(t *testing.T) {
	p, _ := newZypper(executor.MockCall{Stdout: "14.1.1-4"})
	ok, ver, err := p.IsInstalled(context.Background(), tool("ripgrep"))
	if err != nil || !ok || ver != "14.1.1-4" {
		t.Errorf("IsInstalled() = (%v, %q, %v), want (true, 14.1.1-4, nil)", ok, ver, err)
	}
}

func TestIsInstalled_NotFound(t *testing.T) {
	p, _ := newZypper(executor.MockCall{Err: errors.New("exit 1")})
	ok, _, err := p.IsInstalled(context.Background(), tool("nonexistent"))
	if err != nil || ok {
		t.Errorf("IsInstalled() = (%v, _, %v), want (false, nil)", ok, err)
	}
}

func TestListInstalled(t *testing.T) {
	output := "S | Name    | Type    | Version  | Arch   | Repository\n" +
		"--+---------+---------+----------+--------+-----------\n" +
		"i+ | ripgrep | package | 14.1.1-4 | x86_64 | repo\n" +
		"i  | bash    | package | 5.2.15-1 | x86_64 | repo\n"
	p, _ := newZypper(executor.MockCall{Stdout: output})
	tools, err := p.ListInstalled(context.Background())
	if err != nil {
		t.Fatalf("ListInstalled: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 user-installed tool, got %d", len(tools))
	}
	if tools[0].Name != "ripgrep" || tools[0].Version != "14.1.1-4" {
		t.Errorf("unexpected tool[0]: %+v", tools[0])
	}
}

func TestInstalledMap(t *testing.T) {
	output := "S | Name    | Type    | Version  | Arch   | Repository\n" +
		"--+---------+---------+----------+--------+-----------\n" +
		"i+ | Ripgrep | package | 14.1.1-4 | x86_64 | repo\n" +
		"i  | Bash    | package | 5.2.15-1 | x86_64 | repo\n"
	p, _ := newZypper(executor.MockCall{Stdout: output})
	m, err := p.InstalledMap(context.Background())
	if err != nil {
		t.Fatalf("InstalledMap: %v", err)
	}
	if v, ok := m["ripgrep"]; !ok || v != "14.1.1-4" {
		t.Errorf("expected ripgrep in map, got %v", m)
	}
	if _, ok := m["bash"]; ok {
		t.Errorf("auto-installed package should not be in map: %v", m)
	}
}

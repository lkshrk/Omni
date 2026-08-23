package apk_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/provider/apk"
)

func newAPK(responses ...executor.MockCall) (*apk.Provider, *executor.MockExecutor) {
	m := &executor.MockExecutor{Responses: responses}
	return apk.New(m), m
}

func tool(name string) provider.Tool {
	return provider.Tool{Name: name, Provider: "apk", Package: name}
}

func TestAvailable_True(t *testing.T) {
	p, _ := newAPK(executor.MockCall{Stdout: "apk-tools 2.14.0"})
	ok, err := p.Available(context.Background())
	if err != nil || !ok {
		t.Errorf("Available() = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestAvailable_False(t *testing.T) {
	p, _ := newAPK(executor.MockCall{Err: errors.New("not found")})
	ok, err := p.Available(context.Background())
	if err != nil || ok {
		t.Errorf("Available() = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestInstall_Success(t *testing.T) {
	p, m := newAPK(executor.MockCall{Stdout: "Installing ripgrep"})
	if err := p.Install(context.Background(), tool("ripgrep")); err != nil {
		t.Fatalf("Install: %v", err)
	}
	args := m.Calls[0].Args
	if args[0] != "add" || args[1] != "ripgrep" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestInstall_Error(t *testing.T) {
	p, _ := newAPK(executor.MockCall{Err: errors.New("exit 1"), Stderr: "ERROR: unable to select packages"})
	if err := p.Install(context.Background(), tool("badpkg")); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestName(t *testing.T) {
	p, _ := newAPK()
	if p.Name() != "apk" {
		t.Errorf("Name() = %q, want %q", p.Name(), "apk")
	}
}

func TestDescription(t *testing.T) {
	p, _ := newAPK()
	if p.Description() == "" {
		t.Error("Description() should not be empty")
	}
}

func TestUninstall_Success(t *testing.T) {
	p, m := newAPK(executor.MockCall{})
	if err := p.Uninstall(context.Background(), tool("ripgrep")); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if m.Calls[0].Args[0] != "del" {
		t.Errorf("expected 'del', got %q", m.Calls[0].Args[0])
	}
}

func TestUninstall_Error(t *testing.T) {
	p, _ := newAPK(executor.MockCall{Err: errors.New("exit 1")})
	if err := p.Uninstall(context.Background(), tool("badpkg")); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestUpgrade_Success(t *testing.T) {
	p, m := newAPK(executor.MockCall{})
	if err := p.Upgrade(context.Background(), tool("ripgrep")); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if m.Calls[0].Args[0] != "upgrade" {
		t.Errorf("expected 'upgrade', got %q", m.Calls[0].Args[0])
	}
}

func TestUpgrade_Error(t *testing.T) {
	p, _ := newAPK(executor.MockCall{Err: errors.New("exit 1")})
	if err := p.Upgrade(context.Background(), tool("badpkg")); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestIsInstalled_Found(t *testing.T) {
	// apk info -e exits 0 with the package name; apk info -v gives version.
	p, _ := newAPK(
		executor.MockCall{Stdout: "ripgrep"},
		executor.MockCall{Stdout: "ripgrep-14.1.1-r0"},
	)
	ok, ver, err := p.IsInstalled(context.Background(), tool("ripgrep"))
	if err != nil || !ok || ver != "14.1.1-r0" {
		t.Errorf("IsInstalled() = (%v, %q, %v), want (true, 14.1.1-r0, nil)", ok, ver, err)
	}
}

func TestIsInstalled_NotFound(t *testing.T) {
	p, _ := newAPK(executor.MockCall{Err: errors.New("exit 1")})
	ok, _, err := p.IsInstalled(context.Background(), tool("nonexistent"))
	if err != nil || ok {
		t.Errorf("IsInstalled() = (%v, _, %v), want (false, nil)", ok, err)
	}
}

func TestIsInstalled_EmptyOutput(t *testing.T) {
	p, _ := newAPK(executor.MockCall{Stdout: ""})
	ok, _, err := p.IsInstalled(context.Background(), tool("pkg"))
	if err != nil || ok {
		t.Errorf("expected not installed for empty output")
	}
}

func TestListInstalled(t *testing.T) {
	world := filepath.Join(t.TempDir(), "world")
	if err := os.WriteFile(world, []byte("ripgrep\n"), 0o644); err != nil {
		t.Fatalf("write world: %v", err)
	}
	t.Setenv("OMNI_APK_WORLD", world)
	output := "ripgrep-14.1.1-r0\nbash-5.2.21-r0\n"
	p, _ := newAPK(executor.MockCall{Stdout: output})
	tools, err := p.ListInstalled(context.Background())
	if err != nil {
		t.Fatalf("ListInstalled: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 explicit tool, got %d", len(tools))
	}
	if tools[0].Name != "ripgrep" || tools[0].Version != "14.1.1-r0" {
		t.Errorf("unexpected tool[0]: %+v", tools[0])
	}
}

func TestInstalledMap(t *testing.T) {
	world := filepath.Join(t.TempDir(), "world")
	if err := os.WriteFile(world, []byte("Ripgrep\n"), 0o644); err != nil {
		t.Fatalf("write world: %v", err)
	}
	t.Setenv("OMNI_APK_WORLD", world)
	output := "Ripgrep-14.1.1-r0\nBash-5.2.21-r0\n"
	p, _ := newAPK(executor.MockCall{Stdout: output})
	m, err := p.InstalledMap(context.Background())
	if err != nil {
		t.Fatalf("InstalledMap: %v", err)
	}
	if v, ok := m["ripgrep"]; !ok || v != "14.1.1-r0" {
		t.Errorf("expected ripgrep in map, got %v", m)
	}
	if _, ok := m["bash"]; ok {
		t.Errorf("transitive package should not be in map: %v", m)
	}
}

func TestParseAPKInfoLine(t *testing.T) {
	tests := []struct {
		line    string
		name    string
		version string
	}{
		{"ripgrep-14.1.1-r0", "ripgrep", "14.1.1-r0"},
		{"py3-requests-2.31.0-r1", "py3-requests", "2.31.0-r1"},
		{"bash-5.2.21-r0", "bash", "5.2.21-r0"},
		{"", "", ""},
	}
	for _, tc := range tests {
		world := filepath.Join(t.TempDir(), "world")
		if err := os.WriteFile(world, []byte(tc.name+"\n"), 0o644); err != nil {
			t.Fatalf("write world: %v", err)
		}
		t.Setenv("OMNI_APK_WORLD", world)
		p, _ := newAPK(executor.MockCall{Stdout: tc.line + "\n"})
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

func TestListInstalledSurfacesCommandOutputDetail(t *testing.T) {
	sentinel := errors.New("exit status 1")
	p, _ := newAPK(executor.MockCall{Err: sentinel, Stderr: "boom: repo unreachable\n"})
	if _, err := p.ListInstalled(context.Background()); err == nil || !errors.Is(err, sentinel) {
		t.Fatalf("ListInstalled() error = %v, want wrapped sentinel", err)
	} else if !strings.Contains(err.Error(), "boom: repo unreachable") {
		t.Fatalf("ListInstalled() error = %v, want stderr detail", err)
	}
}

func TestListInstalledSurfacesStdoutDetailWhenStderrEmpty(t *testing.T) {
	sentinel := errors.New("exit status 1")
	p, _ := newAPK(executor.MockCall{Err: sentinel, Stdout: "fail written to stdout\n"})
	if _, err := p.ListInstalled(context.Background()); err == nil || !errors.Is(err, sentinel) {
		t.Fatalf("ListInstalled() error = %v, want wrapped sentinel", err)
	} else if !strings.Contains(err.Error(), "fail written to stdout") {
		t.Fatalf("ListInstalled() error = %v, want stdout detail", err)
	}
}

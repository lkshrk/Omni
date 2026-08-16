package apt_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/provider/apt"
)

func newAPT(responses ...executor.MockCall) (*apt.Provider, *executor.MockExecutor) {
	m := &executor.MockExecutor{Responses: responses}
	return apt.New(m), m
}

func tool(name string) provider.Tool {
	return provider.Tool{Name: name, Provider: "apt", Package: name}
}

func TestAvailable_True(t *testing.T) {
	p, _ := newAPT(executor.MockCall{Stdout: "apt 2.4.10"})
	ok, err := p.Available(context.Background())
	if err != nil || !ok {
		t.Errorf("Available() = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestAvailable_False(t *testing.T) {
	p, _ := newAPT(executor.MockCall{Err: errors.New("not found")})
	ok, err := p.Available(context.Background())
	if err != nil || ok {
		t.Errorf("Available() = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestInstall_Success(t *testing.T) {
	p, m := newAPT(executor.MockCall{Stdout: "Setting up ripgrep..."})
	if err := p.Install(context.Background(), tool("ripgrep")); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(m.Calls) != 1 || m.Calls[0].Name != "apt-get" {
		t.Errorf("unexpected calls: %+v", m.Calls)
	}
	args := m.Calls[0].Args
	if args[0] != "install" || args[1] != "-y" || args[2] != "ripgrep" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestInstall_Error(t *testing.T) {
	p, _ := newAPT(executor.MockCall{Err: errors.New("exit 1"), Stderr: "E: Unable to locate package"})
	err := p.Install(context.Background(), tool("badpkg"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestUninstall_Success(t *testing.T) {
	p, m := newAPT(executor.MockCall{})
	if err := p.Uninstall(context.Background(), tool("ripgrep")); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if m.Calls[0].Args[0] != "remove" {
		t.Errorf("expected 'remove', got %q", m.Calls[0].Args[0])
	}
}

func TestUninstall_Error(t *testing.T) {
	p, _ := newAPT(executor.MockCall{Err: errors.New("exit 1"), Stderr: "E: Unable to locate package"})
	if err := p.Uninstall(context.Background(), tool("badpkg")); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestUpgrade_Success(t *testing.T) {
	p, m := newAPT(executor.MockCall{})
	if err := p.Upgrade(context.Background(), tool("ripgrep")); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	args := m.Calls[0].Args
	if args[0] != "install" || args[1] != "--only-upgrade" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestUpgrade_Error(t *testing.T) {
	p, _ := newAPT(executor.MockCall{Err: errors.New("exit 1"), Stderr: "E: Unable to locate package"})
	if err := p.Upgrade(context.Background(), tool("badpkg")); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestName(t *testing.T) {
	p, _ := newAPT()
	if p.Name() != "apt" {
		t.Errorf("Name() = %q, want %q", p.Name(), "apt")
	}
}

func TestDescription(t *testing.T) {
	p, _ := newAPT()
	if p.Description() == "" {
		t.Error("Description() should not be empty")
	}
}

func TestIsInstalled_Found(t *testing.T) {
	p, _ := newAPT(executor.MockCall{Stdout: "14.1.1-1+b4"})
	ok, ver, err := p.IsInstalled(context.Background(), tool("ripgrep"))
	if err != nil || !ok || ver != "14.1.1-1+b4" {
		t.Errorf("IsInstalled() = (%v, %q, %v), want (true, 14.1.1-1+b4, nil)", ok, ver, err)
	}
}

func TestIsInstalled_NotFound(t *testing.T) {
	p, _ := newAPT(executor.MockCall{Err: errors.New("exit 1")})
	ok, _, err := p.IsInstalled(context.Background(), tool("nonexistent"))
	if err != nil || ok {
		t.Errorf("IsInstalled() = (%v, _, %v), want (false, nil)", ok, err)
	}
}

func TestIsInstalled_EmptyOutput(t *testing.T) {
	p, _ := newAPT(executor.MockCall{Stdout: ""})
	ok, _, err := p.IsInstalled(context.Background(), tool("pkg"))
	if err != nil || ok {
		t.Errorf("expected not installed for empty output")
	}
}

func TestListInstalled(t *testing.T) {
	manual := "ripgrep\nfd-find\n"
	output := "ripgrep\t14.1.1-1\tii \nfoo\t1.0\trc \nfd-find\t9.0.0\tii \n"
	p, _ := newAPT(executor.MockCall{Stdout: manual}, executor.MockCall{Stdout: output})
	tools, err := p.ListInstalled(context.Background())
	if err != nil {
		t.Fatalf("ListInstalled: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools (rc status filtered), got %d", len(tools))
	}
	if tools[0].Name != "ripgrep" || tools[0].Version != "14.1.1-1" {
		t.Errorf("unexpected tool[0]: %+v", tools[0])
	}
	if tools[1].Name != "fd-find" {
		t.Errorf("unexpected tool[1]: %+v", tools[1])
	}
}

func TestInstalledMap(t *testing.T) {
	manual := "Ripgrep\n"
	output := "Ripgrep\t14.1.1-1\tii \nFoo\t1.0\tun \n"
	p, _ := newAPT(executor.MockCall{Stdout: manual}, executor.MockCall{Stdout: output})
	m, err := p.InstalledMap(context.Background())
	if err != nil {
		t.Fatalf("InstalledMap: %v", err)
	}
	if v, ok := m["ripgrep"]; !ok || v != "14.1.1-1" {
		t.Errorf("expected ripgrep in map, got %v", m)
	}
	if _, ok := m["foo"]; ok {
		t.Error("foo should not be in map (status=un)")
	}
}

func TestParseAPTListLine(t *testing.T) {
	tests := []struct {
		line    string
		name    string
		version string
		ok      bool
	}{
		{"ripgrep\t14.1.1-1\tii ", "ripgrep", "14.1.1-1", true},
		{"foo\t1.0\trc ", "", "", false},
		{"bar\t2.0\tun ", "", "", false},
		{"", "", "", false},
		{"only-one-field", "", "", false},
	}
	for _, tc := range tests {
		p, _ := newAPT()
		resp := executor.MockCall{Stdout: tc.line + "\n"}
		p2, _ := newAPT(executor.MockCall{Stdout: tc.name + "\n"}, resp)
		tools, _ := p2.ListInstalled(context.Background())
		if tc.ok {
			if len(tools) != 1 || tools[0].Name != tc.name || tools[0].Version != tc.version {
				t.Errorf("line %q: got %v, want name=%q version=%q", tc.line, tools, tc.name, tc.version)
			}
		} else {
			if len(tools) != 0 {
				t.Errorf("line %q: expected 0 tools, got %v", tc.line, tools)
			}
		}
		_ = p
	}
}

func TestListInstalledSurfacesCommandOutputDetail(t *testing.T) {
	sentinel := errors.New("exit status 1")
	p, _ := newAPT(executor.MockCall{Err: sentinel, Stderr: "boom: repo unreachable\n"})
	if _, err := p.ListInstalled(context.Background()); err == nil || !errors.Is(err, sentinel) {
		t.Fatalf("ListInstalled() error = %v, want wrapped sentinel", err)
	} else if !strings.Contains(err.Error(), "boom: repo unreachable") {
		t.Fatalf("ListInstalled() error = %v, want stderr detail", err)
	}
}

func TestListInstalledSurfacesStdoutDetailWhenStderrEmpty(t *testing.T) {
	sentinel := errors.New("exit status 1")
	p, _ := newAPT(executor.MockCall{Err: sentinel, Stdout: "fail written to stdout\n"})
	if _, err := p.ListInstalled(context.Background()); err == nil || !errors.Is(err, sentinel) {
		t.Fatalf("ListInstalled() error = %v, want wrapped sentinel", err)
	} else if !strings.Contains(err.Error(), "fail written to stdout") {
		t.Fatalf("ListInstalled() error = %v, want stdout detail", err)
	}
}

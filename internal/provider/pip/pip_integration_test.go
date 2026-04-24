//go:build integration

package pip_test

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/provider/pip"
)

func newPipProvider(t *testing.T) *pip.Provider {
	t.Helper()
	return pip.New(executor.New())
}

func TestPipProvider_Available(t *testing.T) {
	p := newPipProvider(t)
	ctx := context.Background()

	ok, err := p.Available(ctx)
	if err != nil {
		t.Fatalf("Available() returned unexpected error: %v", err)
	}
	if !ok {
		t.Skip("pip3 not available on this system")
	}
}

func TestPipProvider_InstalledMap_ContainsCowsay(t *testing.T) {
	p := newPipProvider(t)
	ctx := context.Background()

	ok, err := p.Available(ctx)
	if err != nil {
		t.Fatalf("Available() error: %v", err)
	}
	if !ok {
		t.Skip("pip3 not available on this system")
	}

	m, err := p.InstalledMap(ctx)
	if err != nil {
		t.Fatalf("InstalledMap() error: %v", err)
	}

	ver, found := m["cowsay"]
	if !found {
		t.Errorf("expected cowsay in InstalledMap, not found (map has %d entries)", len(m))
		return
	}
	if ver == "" {
		t.Errorf("expected non-empty version for cowsay")
	}
}

func TestPipProvider_IsInstalled_Cowsay(t *testing.T) {
	p := newPipProvider(t)
	ctx := context.Background()

	ok, err := p.Available(ctx)
	if err != nil {
		t.Fatalf("Available() error: %v", err)
	}
	if !ok {
		t.Skip("pip3 not available on this system")
	}

	installed, version, err := p.IsInstalled(ctx, provider.Tool{Name: "cowsay"})
	if err != nil {
		t.Fatalf("IsInstalled(cowsay) error: %v", err)
	}
	if !installed {
		t.Errorf("expected cowsay to be installed")
	}
	if version == "" {
		t.Errorf("expected non-empty version for cowsay")
	}
}

func TestPipProvider_IsInstalled_Unknown(t *testing.T) {
	p := newPipProvider(t)
	ctx := context.Background()

	ok, err := p.Available(ctx)
	if err != nil {
		t.Fatalf("Available() error: %v", err)
	}
	if !ok {
		t.Skip("pip3 not available on this system")
	}

	installed, _, err := p.IsInstalled(ctx, provider.Tool{Name: "omni-fake-pkg-zzz-pip"})
	if err != nil {
		t.Fatalf("IsInstalled(unknown) unexpected error: %v", err)
	}
	if installed {
		t.Errorf("expected omni-fake-pkg-zzz-pip to NOT be installed")
	}
}

func TestPipProvider_ListInstalled_NonEmpty(t *testing.T) {
	p := newPipProvider(t)
	ctx := context.Background()

	ok, err := p.Available(ctx)
	if err != nil {
		t.Fatalf("Available() error: %v", err)
	}
	if !ok {
		t.Skip("pip3 not available on this system")
	}

	tools, err := p.ListInstalled(ctx)
	if err != nil {
		t.Fatalf("ListInstalled() error: %v", err)
	}
	if len(tools) < 1 {
		t.Errorf("expected at least 1 installed package, got %d", len(tools))
	}
}

func TestPipProvider_Describe_Cowsay(t *testing.T) {
	p := newPipProvider(t)
	ctx := context.Background()

	ok, err := p.Available(ctx)
	if err != nil {
		t.Fatalf("Available() error: %v", err)
	}
	if !ok {
		t.Skip("pip3 not available on this system")
	}

	desc, err := p.Describe(ctx, provider.Tool{Name: "cowsay"})
	if err != nil {
		t.Fatalf("Describe(cowsay) error: %v", err)
	}
	if desc == "" {
		t.Errorf("expected non-empty description for cowsay from PyPI")
	}
}

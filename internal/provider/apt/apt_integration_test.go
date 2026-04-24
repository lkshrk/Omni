//go:build integration

package apt_test

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/provider/apt"
)

func newAptProvider(t *testing.T) *apt.Provider {
	t.Helper()
	return apt.New(executor.New())
}

func TestAptProvider_Available(t *testing.T) {
	p := newAptProvider(t)
	ctx := context.Background()

	ok, err := p.Available(ctx)
	if err != nil {
		t.Fatalf("Available() returned unexpected error: %v", err)
	}
	if !ok {
		t.Skip("apt not available on this system")
	}
}

func TestAptProvider_InstalledMap_ContainsKnownPackages(t *testing.T) {
	p := newAptProvider(t)
	ctx := context.Background()

	ok, err := p.Available(ctx)
	if err != nil {
		t.Fatalf("Available() error: %v", err)
	}
	if !ok {
		t.Skip("apt not available on this system")
	}

	m, err := p.InstalledMap(ctx)
	if err != nil {
		t.Fatalf("InstalledMap() error: %v", err)
	}

	for _, pkg := range []string{"git", "stow", "python3"} {
		ver, found := m[pkg]
		if !found {
			t.Errorf("expected package %q in InstalledMap, not found", pkg)
			continue
		}
		if ver == "" {
			t.Errorf("expected non-empty version for package %q", pkg)
		}
	}
}

func TestAptProvider_BulkDescribe(t *testing.T) {
	p := newAptProvider(t)
	ctx := context.Background()

	ok, err := p.Available(ctx)
	if err != nil {
		t.Fatalf("Available() error: %v", err)
	}
	if !ok {
		t.Skip("apt not available on this system")
	}

	tools := []provider.Tool{
		{Name: "git"},
		{Name: "stow"},
	}
	descs, err := p.BulkDescribe(ctx, tools)
	if err != nil {
		t.Fatalf("BulkDescribe() error: %v", err)
	}
	for _, name := range []string{"git", "stow"} {
		desc, found := descs[name]
		if !found {
			t.Errorf("BulkDescribe: expected description for %q, not found", name)
			continue
		}
		if desc == "" {
			t.Errorf("BulkDescribe: expected non-empty description for %q", name)
		}
	}
}

func TestAptProvider_IsInstalled_KnownPackage(t *testing.T) {
	p := newAptProvider(t)
	ctx := context.Background()

	ok, err := p.Available(ctx)
	if err != nil {
		t.Fatalf("Available() error: %v", err)
	}
	if !ok {
		t.Skip("apt not available on this system")
	}

	installed, version, err := p.IsInstalled(ctx, provider.Tool{Name: "git"})
	if err != nil {
		t.Fatalf("IsInstalled(git) error: %v", err)
	}
	if !installed {
		t.Errorf("expected git to be installed")
	}
	if version == "" {
		t.Errorf("expected non-empty version for git")
	}
}

func TestAptProvider_IsInstalled_UnknownPackage(t *testing.T) {
	p := newAptProvider(t)
	ctx := context.Background()

	ok, err := p.Available(ctx)
	if err != nil {
		t.Fatalf("Available() error: %v", err)
	}
	if !ok {
		t.Skip("apt not available on this system")
	}

	installed, _, err := p.IsInstalled(ctx, provider.Tool{Name: "omni-fake-pkg-zzz"})
	if err != nil {
		t.Fatalf("IsInstalled(unknown) unexpected error: %v", err)
	}
	if installed {
		t.Errorf("expected omni-fake-pkg-zzz to NOT be installed")
	}
}

func TestAptProvider_ListInstalled_NonEmpty(t *testing.T) {
	p := newAptProvider(t)
	ctx := context.Background()

	ok, err := p.Available(ctx)
	if err != nil {
		t.Fatalf("Available() error: %v", err)
	}
	if !ok {
		t.Skip("apt not available on this system")
	}

	tools, err := p.ListInstalled(ctx)
	if err != nil {
		t.Fatalf("ListInstalled() error: %v", err)
	}
	if len(tools) <= 10 {
		t.Errorf("expected more than 10 installed packages, got %d", len(tools))
	}
}

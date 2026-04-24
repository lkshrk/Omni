//go:build integration

package python_test

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider/python"
)

func newPythonProvider(t *testing.T) *python.Provider {
	t.Helper()
	// hint="" auto-detects from PATH (uv → pip3 → pip).
	return python.New(executor.New(), "")
}

func TestPythonProvider_Available(t *testing.T) {
	p := newPythonProvider(t)
	ctx := context.Background()

	ok, err := p.Available(ctx)
	if err != nil {
		t.Fatalf("Available() returned unexpected error: %v", err)
	}
	if !ok {
		t.Skip("no Python package manager (uv/pip3/pip) available on this system")
	}
}

func TestPythonProvider_ResolvedName(t *testing.T) {
	p := newPythonProvider(t)
	ctx := context.Background()

	ok, err := p.Available(ctx)
	if err != nil {
		t.Fatalf("Available() error: %v", err)
	}
	if !ok {
		t.Skip("no Python package manager available on this system")
	}

	name, err := p.ResolvedName(ctx)
	if err != nil {
		t.Fatalf("ResolvedName() error: %v", err)
	}
	if name == "" {
		t.Errorf("expected non-empty resolved manager name")
	}
}

func TestPythonProvider_InstalledByManager_ContainsCowsay(t *testing.T) {
	p := newPythonProvider(t)
	ctx := context.Background()

	ok, err := p.Available(ctx)
	if err != nil {
		t.Fatalf("Available() error: %v", err)
	}
	if !ok {
		t.Skip("no Python package manager available on this system")
	}

	entries, err := p.InstalledByManager(ctx)
	if err != nil {
		t.Fatalf("InstalledByManager() error: %v", err)
	}

	entry, found := entries["cowsay"]
	if !found {
		t.Errorf("expected cowsay in InstalledByManager result (got %d entries)", len(entries))
		return
	}
	if entry.Version == "" {
		t.Errorf("expected non-empty version for cowsay")
	}
	if entry.ConcreteManager == "" {
		t.Errorf("expected non-empty ConcreteManager for cowsay")
	}
}

//go:build integration

package node_test

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/provider/node"
)

func newNodeProvider(t *testing.T) *node.Provider {
	t.Helper()
	// hint="" auto-detects from PATH (bun → pnpm → npm).
	return node.New(executor.New(), "")
}

func TestNodeProvider_Available(t *testing.T) {
	p := newNodeProvider(t)
	ctx := context.Background()

	ok, err := p.Available(ctx)
	if err != nil {
		t.Fatalf("Available() returned unexpected error: %v", err)
	}
	if !ok {
		t.Skip("no Node.js package manager (bun/pnpm/npm) available on this system")
	}
}

func TestNodeProvider_InstalledMap_NonEmpty(t *testing.T) {
	p := newNodeProvider(t)
	ctx := context.Background()

	ok, err := p.Available(ctx)
	if err != nil {
		t.Fatalf("Available() error: %v", err)
	}
	if !ok {
		t.Skip("no Node.js package manager available on this system")
	}

	// An empty map is fine; the point is that parsing succeeds.
	_, err = p.InstalledMap(ctx)
	if err != nil {
		t.Errorf("InstalledMap() unexpected error: %v", err)
	}
}

func TestNodeProvider_IsInstalled_Unknown(t *testing.T) {
	p := newNodeProvider(t)
	ctx := context.Background()

	ok, err := p.Available(ctx)
	if err != nil {
		t.Fatalf("Available() error: %v", err)
	}
	if !ok {
		t.Skip("no Node.js package manager available on this system")
	}

	installed, _, err := p.IsInstalled(ctx, provider.Tool{Name: "omni-fake-pkg-zzz-node"})
	if err != nil {
		t.Fatalf("IsInstalled(unknown) unexpected error: %v", err)
	}
	if installed {
		t.Errorf("expected omni-fake-pkg-zzz-node to NOT be installed")
	}
}

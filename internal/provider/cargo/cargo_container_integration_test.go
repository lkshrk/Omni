//go:build pmcontainer

package cargo_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/provider/cargo"
	"github.com/lkshrk/omni/internal/provider/pmtest"
)

func TestCargoProvider_RustImageLifecycle(t *testing.T) {
	ctx, cancel := pmtest.Context(t, "cargo")
	defer cancel()
	t.Setenv("CARGO_INSTALL_ROOT", t.TempDir())

	p := cargo.New(executor.New())
	pmtest.RequireAvailable(t, ctx, p, "rust:1.88-slim-bookworm")

	const name = "cowsay"
	const packageSpec = name + "@0.14.0"
	results, err := p.Search(ctx, name)
	if err != nil {
		t.Fatalf("Search(%s) error: %v", name, err)
	}
	found := false
	for _, result := range results {
		if result.Name == name && strings.TrimSpace(result.Version) != "" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Search(%s) missing exact result with version: %#v", name, results)
	}

	tool := provider.Tool{Name: name, Provider: p.Name(), Package: packageSpec}
	pmtest.RequireMissing(t, ctx, p, name)
	if err := p.Install(ctx, tool); err != nil {
		t.Fatalf("Install(%s) error: %v", name, err)
	}
	installed := true
	defer func() {
		if installed {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cleanupCancel()
			if err := p.Uninstall(cleanupCtx, tool); err != nil {
				t.Logf("cleanup Uninstall(%s) error: %v", name, err)
			}
		}
	}()

	if version := pmtest.RequireInstalled(t, ctx, p, name); version != "0.14.0" {
		t.Fatalf("installed version = %q, want 0.14.0", version)
	}
	pmtest.RequireInstalledMap(t, ctx, p, name)
	pmtest.RequireListInstalled(t, ctx, p, name)

	if err := p.Upgrade(ctx, tool); err != nil {
		t.Fatalf("Upgrade(%s) error: %v", name, err)
	}
	if err := p.Uninstall(ctx, tool); err != nil {
		t.Fatalf("Uninstall(%s) error: %v", name, err)
	}
	installed = false
	pmtest.RequireMissing(t, ctx, p, name)
}

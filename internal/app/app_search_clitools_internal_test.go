package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/provider"
)

// cliToolErrorProviderStub implements provider.CLIToolProvider but always
// fails, simulating a broken python/pip dist-info scan.
type cliToolErrorProviderStub struct {
	internalProviderStub
}

func (s *cliToolErrorProviderStub) CLIToolSet(context.Context) (map[string]bool, error) {
	return nil, errors.New("cli tool set scan failed")
}

func TestDiscoverCLIToolSetsFailsClosedWhenProviderErrors(t *testing.T) {
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	a.registry = provider.NewRegistry()
	broken := &cliToolErrorProviderStub{internalProviderStub: internalProviderStub{name: "pip"}}
	a.registry.RegisterWithMetadata(broken, provider.BuiltinMetadata("pip"))

	cliSets := a.discoverCLIToolSets(context.Background())

	if discoverCLIToolAllowed(cliSets, "pip", "blinker") {
		t.Fatal("discoverCLIToolAllowed = true for erroring CLIToolProvider, want fail-closed false")
	}
}

func TestDiscoverCLIToolSetsAllowsAllForNonCLIToolProvider(t *testing.T) {
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	a.registry = provider.NewRegistry()
	brew := &internalProviderStub{name: "brew"}
	a.registry.RegisterWithMetadata(brew, provider.BuiltinMetadata("brew"))

	cliSets := a.discoverCLIToolSets(context.Background())

	if !discoverCLIToolAllowed(cliSets, "brew", "ripgrep") {
		t.Fatal("discoverCLIToolAllowed = false for non-CLIToolProvider, want fail-open true")
	}
}

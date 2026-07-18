package all_test

import (
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	_ "github.com/lkshrk/omni/internal/provider/all"
)

// wantConcrete is the set of built-in concrete providers that self-register a
// factory and are linked by package all. The node/python named managers
// (bun/pnpm/npm/uv) are parameterized and wired explicitly in internal/app, so
// they are intentionally absent here.
var wantConcrete = []string{
	"apk", "apt", "apt_repo", "brew", "cargo", "dnf", "pacman", "pip", "script", "zypper",
}

// TestConcreteFactoriesLinked guards the registration seam against drift: a new
// concrete provider must both register a factory (its init) and be linked by
// package all (its blank import). If either is missing, this list diverges.
func TestConcreteFactoriesLinked(t *testing.T) {
	got := provider.RegisteredConcreteNames() // sorted
	if len(got) != len(wantConcrete) {
		t.Fatalf("registered concrete providers = %v, want %v", got, wantConcrete)
	}
	for i, name := range wantConcrete {
		if got[i] != name {
			t.Fatalf("registered concrete providers = %v, want %v", got, wantConcrete)
		}
	}
}

// TestEachConcreteHasCatalogMetadata ensures every self-registered provider is
// also described in the catalog (the third edit adding a provider requires).
func TestEachConcreteHasCatalogMetadata(t *testing.T) {
	for _, name := range provider.RegisteredConcreteNames() {
		if meta := provider.BuiltinMetadata(name); meta.Kind != provider.ProviderKindConcrete {
			t.Errorf("provider %q: catalog Kind = %q, want concrete", name, meta.Kind)
		}
	}
}

// TestBuildConcreteProvidersConstructsAll checks every factory builds a
// non-nil provider whose Name matches its registration key.
func TestBuildConcreteProvidersConstructsAll(t *testing.T) {
	built := provider.BuildConcreteProviders(executor.New())
	for _, name := range provider.RegisteredConcreteNames() {
		p, ok := built[name]
		if !ok || p == nil {
			t.Errorf("provider %q: not built", name)
			continue
		}
		if p.Name() != name {
			t.Errorf("provider %q: built provider Name() = %q", name, p.Name())
		}
	}
}

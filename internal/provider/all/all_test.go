package all_test

import (
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	_ "github.com/lkshrk/omni/internal/provider/all"
)

// The node/python named managers are parameterized and wired in internal/app, so they are absent here.
var wantConcrete = []string{
	"apk", "apt", "apt_repo", "brew", "cargo", "dnf", "pacman", "pip", "script", "zypper",
}

// A new provider must both register a factory and be blank-imported by package all, or this list diverges.
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

// The catalog entry is the third edit adding a provider requires.
func TestEachConcreteHasCatalogMetadata(t *testing.T) {
	for _, name := range provider.RegisteredConcreteNames() {
		if meta := provider.BuiltinMetadata(name); meta.Kind != provider.ProviderKindConcrete {
			t.Errorf("provider %q: catalog Kind = %q, want concrete", name, meta.Kind)
		}
	}
}

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

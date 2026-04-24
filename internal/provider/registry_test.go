package provider_test

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/provider"
)

// fakeProvider is a minimal Provider used in tests.
type fakeProvider struct {
	name      string
	available bool
}

func (f *fakeProvider) Name() string        { return f.name }
func (f *fakeProvider) Description() string { return "fake " + f.name }
func (f *fakeProvider) Available(_ context.Context) (bool, error) {
	return f.available, nil
}
func (f *fakeProvider) Install(_ context.Context, _ provider.Tool) error   { return nil }
func (f *fakeProvider) Uninstall(_ context.Context, _ provider.Tool) error { return nil }
func (f *fakeProvider) Upgrade(_ context.Context, _ provider.Tool) error   { return nil }
func (f *fakeProvider) IsInstalled(_ context.Context, _ provider.Tool) (bool, string, error) {
	return false, "", nil
}
func (f *fakeProvider) ListInstalled(_ context.Context) ([]provider.InstalledTool, error) {
	return nil, nil
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	reg := provider.NewRegistry()

	p := &fakeProvider{name: "brew"}
	reg.Register(p)

	got, ok := reg.Get("brew")
	if !ok {
		t.Fatal("expected brew to be found")
	}
	if got.Name() != "brew" {
		t.Errorf("got name %q, want %q", got.Name(), "brew")
	}
}

func TestRegistry_Get_NotFound(t *testing.T) {
	reg := provider.NewRegistry()

	_, ok := reg.Get("nonexistent")
	if ok {
		t.Fatal("expected not found")
	}
}

func TestRegistry_All(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&fakeProvider{name: "brew"})
	reg.Register(&fakeProvider{name: "npm"})
	reg.Register(&fakeProvider{name: "pip"})

	all := reg.All()
	if len(all) != 3 {
		t.Errorf("got %d providers, want 3", len(all))
	}
}

func TestRegistry_Register_Overwrite(t *testing.T) {
	reg := provider.NewRegistry()

	reg.Register(&fakeProvider{name: "brew", available: false})
	reg.Register(&fakeProvider{name: "brew", available: true})

	got, _ := reg.Get("brew")
	ok, _ := got.Available(context.Background())
	if !ok {
		t.Error("expected overwritten provider (available=true)")
	}
}

func TestRegistry_Metadata(t *testing.T) {
	reg := provider.NewRegistry()
	reg.RegisterWithMetadata(&fakeProvider{name: "system"}, provider.Metadata{
		Kind:                           provider.ProviderKindEcosystem,
		Ecosystem:                      "system",
		ImportSkipsRegisteredDelegates: true,
	})
	reg.RegisterWithMetadata(&fakeProvider{name: "brew"}, provider.Metadata{
		Kind:      provider.ProviderKindConcrete,
		Ecosystem: "system",
	})

	if !reg.IsEcosystemProvider("system") {
		t.Fatal("system should be registered as an ecosystem provider")
	}
	if reg.IsEcosystemProvider("brew") {
		t.Fatal("brew should not be registered as an ecosystem provider")
	}
	if !reg.ImportSkipsProvider("system") {
		t.Fatal("system should be skipped by full import")
	}
	if reg.ImportSkipsProvider("brew") {
		t.Fatal("brew should not be skipped by full import")
	}
	meta, ok := reg.Metadata("brew")
	if !ok {
		t.Fatal("brew metadata missing")
	}
	if meta.Ecosystem != "system" {
		t.Fatalf("brew ecosystem = %q, want system", meta.Ecosystem)
	}
	if got := reg.EcosystemProviders(); len(got) != 1 || got[0].Name() != "system" {
		t.Fatalf("ecosystem providers = %+v, want [system]", got)
	}
}

func TestRegistry_Available(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&fakeProvider{name: "brew", available: true})
	reg.Register(&fakeProvider{name: "npm", available: false})
	reg.Register(&fakeProvider{name: "pip", available: true})

	avail, err := reg.Available(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(avail) != 2 {
		t.Errorf("got %d available providers, want 2", len(avail))
	}
}

func TestTool_EffectivePackage(t *testing.T) {
	if got := (provider.Tool{Name: "git", Package: "git-core"}).EffectivePackage(); got != "git-core" {
		t.Errorf("got %q, want git-core", got)
	}
	if got := (provider.Tool{Name: "git"}).EffectivePackage(); got != "git" {
		t.Errorf("got %q, want git (fallback to Name)", got)
	}
}

func TestRegistry_Empty(t *testing.T) {
	reg := provider.NewRegistry()

	all := reg.All()
	if len(all) != 0 {
		t.Errorf("expected empty, got %d", len(all))
	}

	avail, err := reg.Available(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(avail) != 0 {
		t.Errorf("expected empty available, got %d", len(avail))
	}
}

// registryWithMix builds a registry with one ecosystem provider (system),
// two concrete providers in that ecosystem (apt, brew), and one concrete
// provider in a different ecosystem (pip). Useful for kind/ecosystem queries.
func registryWithMix(t *testing.T) *provider.Registry {
	t.Helper()
	reg := provider.NewRegistry()
	reg.RegisterWithMetadata(&fakeProvider{name: "system"}, provider.Metadata{
		Kind:                           provider.ProviderKindEcosystem,
		Ecosystem:                      "system",
		ImportSkipsRegisteredDelegates: true,
		DisplayOrder:                   100,
		ManagerOptions: []provider.ManagerOption{
			{Name: "apt", Provider: "apt", Order: 1},
			{Name: "brew", Provider: "brew", Order: 2},
		},
	})
	reg.RegisterWithMetadata(&fakeProvider{name: "apt"}, provider.Metadata{
		Kind:                provider.ProviderKindConcrete,
		Ecosystem:           "system",
		DisplayOrder:        110,
		DefaultInstallOrder: 2,
	})
	reg.RegisterWithMetadata(&fakeProvider{name: "brew"}, provider.Metadata{
		Kind:                provider.ProviderKindConcrete,
		Ecosystem:           "system",
		DisplayOrder:        160,
		DefaultInstallOrder: 1,
	})
	reg.RegisterWithMetadata(&fakeProvider{name: "pip"}, provider.Metadata{
		Kind:         provider.ProviderKindConcrete,
		Ecosystem:    "python",
		DisplayOrder: 300,
	})
	return reg
}

func TestRegistry_IsEcosystemProvider(t *testing.T) {
	reg := registryWithMix(t)
	if !reg.IsEcosystemProvider("system") {
		t.Error("system should be ecosystem")
	}
	if reg.IsEcosystemProvider("apt") {
		t.Error("apt is concrete, not ecosystem")
	}
	if reg.IsEcosystemProvider("nonexistent") {
		t.Error("missing provider should not be ecosystem")
	}
}

func TestRegistry_EcosystemProviders(t *testing.T) {
	reg := registryWithMix(t)
	got := reg.EcosystemProviders()
	if len(got) != 1 {
		t.Fatalf("got %d providers, want 1", len(got))
	}
	if got[0].Name() != "system" {
		t.Errorf("got %q, want system", got[0].Name())
	}
}

func TestRegistry_ImportSkipsProvider(t *testing.T) {
	reg := registryWithMix(t)
	if !reg.ImportSkipsProvider("system") {
		t.Error("system has ImportSkipsRegisteredDelegates=true")
	}
	if reg.ImportSkipsProvider("apt") {
		t.Error("apt does not skip imports")
	}
	if reg.ImportSkipsProvider("nonexistent") {
		t.Error("missing provider should not skip imports")
	}
}

func TestRegistry_NamesAndKnownNames(t *testing.T) {
	reg := registryWithMix(t)

	names := reg.Names()
	if len(names) != 4 {
		t.Errorf("Names() len = %d, want 4", len(names))
	}

	known := reg.KnownNames()
	// KnownNames is in display order: system(100), apt(110), brew(160), pip(300).
	want := []string{"system", "apt", "brew", "pip"}
	if len(known) != len(want) {
		t.Fatalf("KnownNames len = %d, want %d", len(known), len(want))
	}
	for i, n := range want {
		if known[i] != n {
			t.Errorf("KnownNames[%d] = %q, want %q", i, known[i], n)
		}
	}
}

func TestRegistry_EcosystemNames(t *testing.T) {
	reg := registryWithMix(t)
	got := reg.EcosystemNames()
	if len(got) != 1 || got[0] != "system" {
		t.Errorf("EcosystemNames = %v, want [system]", got)
	}
}

func TestRegistry_ConcreteEcosystems(t *testing.T) {
	reg := registryWithMix(t)
	got := reg.ConcreteEcosystems()
	wantPairs := map[string]string{
		"apt":  "system",
		"brew": "system",
		"pip":  "python",
	}
	for name, eco := range wantPairs {
		if got[name] != eco {
			t.Errorf("ConcreteEcosystems[%q] = %q, want %q", name, got[name], eco)
		}
	}
	// system is an ecosystem, not a concrete entry — should not appear.
	if _, ok := got["system"]; ok {
		t.Error("ConcreteEcosystems should not include ecosystem providers")
	}
}

func TestRegistry_EcosystemFor(t *testing.T) {
	reg := registryWithMix(t)
	tests := []struct {
		input string
		want  string
		found bool
	}{
		{"system", "system", true}, // ecosystem name itself
		{"apt", "system", true},    // concrete in system
		{"brew", "system", true},   // concrete in system via ManagerOption
		{"pip", "python", true},    // concrete in python
		{"nonexistent", "", false},
	}
	for _, tt := range tests {
		got, ok := reg.EcosystemFor(tt.input)
		if ok != tt.found {
			t.Errorf("EcosystemFor(%q) found=%v, want %v", tt.input, ok, tt.found)
		}
		if got != tt.want {
			t.Errorf("EcosystemFor(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestRegistry_ManagerOptionsAndManagerOption(t *testing.T) {
	reg := registryWithMix(t)
	opts := reg.ManagerOptions("system")
	if len(opts) != 2 {
		t.Fatalf("ManagerOptions(system) len = %d, want 2", len(opts))
	}
	// Sorted by Order: apt(1), brew(2).
	if opts[0].Name != "apt" || opts[1].Name != "brew" {
		t.Errorf("ManagerOptions order = %v, want [apt brew]", []string{opts[0].Name, opts[1].Name})
	}

	if got, ok := reg.ManagerOption("system", "brew"); !ok || got.Name != "brew" {
		t.Errorf("ManagerOption(system, brew) = (%+v, %v)", got, ok)
	}
	if _, ok := reg.ManagerOption("system", "nonexistent"); ok {
		t.Error("ManagerOption should not find missing manager")
	}
	if _, ok := reg.ManagerOption("nonexistent-eco", "brew"); ok {
		t.Error("ManagerOption should not find manager in missing ecosystem")
	}
}

func TestRegistry_DefaultInstallProviderNames(t *testing.T) {
	reg := registryWithMix(t)
	got := reg.DefaultInstallProviderNames()
	// brew has DefaultInstallOrder=1, apt has 2; pip + system have 0 (excluded).
	want := []string{"brew", "apt"}
	if len(got) != len(want) {
		t.Fatalf("DefaultInstallProviderNames len = %d, want %d (got %v)", len(got), len(want), got)
	}
	for i, n := range want {
		if got[i] != n {
			t.Errorf("DefaultInstallProviderNames[%d] = %q, want %q", i, got[i], n)
		}
	}
}

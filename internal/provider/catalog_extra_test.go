package provider_test

import (
	"slices"
	"testing"

	"github.com/lkshrk/omni/internal/provider"
)

func TestBuiltinKnownNames_OrderedByDisplayOrder(t *testing.T) {
	got := provider.BuiltinKnownNames()
	want := []string{
		"system", "node", "python", "brew", "apt_repo", "apt", "apk", "dnf",
		"pacman", "zypper", "bun", "pnpm", "npm", "uv", "pip3", "pip", "cargo", "script",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("BuiltinKnownNames = %v, want %v", got, want)
	}
}

func TestBuiltinEcosystemNames(t *testing.T) {
	got := provider.BuiltinEcosystemNames()
	want := []string{"system", "node", "python"}
	if !slices.Equal(got, want) {
		t.Fatalf("BuiltinEcosystemNames = %v, want %v", got, want)
	}
}

func TestBuiltinConcreteEcosystems(t *testing.T) {
	got := provider.BuiltinConcreteEcosystems()
	for name, want := range map[string]string{
		"brew": "system",
		"apt":  "system",
		"bun":  "node",
		"npm":  "node",
		"pnpm": "node",
		"uv":   "python",
		"pip":  "python",
		"pip3": "python",
	} {
		if got[name] != want {
			t.Errorf("ecosystem[%q] = %q, want %q", name, got[name], want)
		}
	}
	// cargo has no ecosystem; script is excluded from the map.
	if _, ok := got["cargo"]; ok {
		t.Errorf("cargo must not carry an ecosystem, got %q", got["cargo"])
	}
	if _, ok := got["script"]; ok {
		t.Errorf("script must not carry an ecosystem, got %q", got["script"])
	}
}

func TestBuiltinConcreteConfigNames(t *testing.T) {
	got := provider.BuiltinConcreteConfigNames()
	if slices.Contains(got, "script") {
		t.Errorf("config names must exclude script: %v", got)
	}
	for _, want := range []string{"brew", "apt", "pip", "pip3", "cargo"} {
		if !slices.Contains(got, want) {
			t.Errorf("config names must include %q: %v", want, got)
		}
	}
	// Ecosystem parents are not concrete and must be absent.
	for _, eco := range []string{"system", "node", "python"} {
		if slices.Contains(got, eco) {
			t.Errorf("config names must exclude ecosystem %q: %v", eco, got)
		}
	}
}

func TestBuiltinEcosystemFor(t *testing.T) {
	for _, tc := range []struct {
		name    string
		wantEco string
		wantOK  bool
	}{
		{"node", "node", true},   // ecosystem kind resolves to itself
		{"brew", "system", true}, // concrete with explicit ecosystem
		{"pnpm", "node", true},   // concrete manager with ecosystem
		{"cargo", "", false},     // concrete without ecosystem
		{"unknown", "", false},   // absent entirely
	} {
		eco, ok := provider.BuiltinEcosystemFor(tc.name)
		if eco != tc.wantEco || ok != tc.wantOK {
			t.Errorf("BuiltinEcosystemFor(%q) = (%q, %v), want (%q, %v)", tc.name, eco, ok, tc.wantEco, tc.wantOK)
		}
	}
}

func TestBuiltinIsEcosystem(t *testing.T) {
	for name, want := range map[string]bool{
		"node":    true,
		"system":  true,
		"python":  true,
		"brew":    false,
		"cargo":   false,
		"unknown": false,
	} {
		if got := provider.BuiltinIsEcosystem(name); got != want {
			t.Errorf("BuiltinIsEcosystem(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestBuiltinManagerOptions(t *testing.T) {
	node := provider.BuiltinManagerOptions("node")
	if len(node) != 3 || node[0].Name != "bun" || node[1].Name != "pnpm" || node[2].Name != "npm" {
		t.Fatalf("node manager options = %+v, want bun,pnpm,npm in order", node)
	}
	python := provider.BuiltinManagerOptions("python")
	if len(python) != 3 || python[0].Name != "uv" || python[1].Name != "pip3" || python[2].Name != "pip" {
		t.Fatalf("python manager options = %+v, want uv,pip3,pip in order", python)
	}
	if opts := provider.BuiltinManagerOptions("system"); len(opts) != 0 {
		t.Fatalf("system manager options = %+v, want none", opts)
	}
}

func TestBuiltinManagerOption(t *testing.T) {
	opt, ok := provider.BuiltinManagerOption("node", "npm")
	if !ok || opt.Name != "npm" || opt.SettingsValue != "npm" {
		t.Fatalf("BuiltinManagerOption(node, npm) = (%+v, %v), want npm", opt, ok)
	}
	if _, ok := provider.BuiltinManagerOption("node", "cargo"); ok {
		t.Fatal("BuiltinManagerOption(node, cargo) should not be found")
	}
	if _, ok := provider.BuiltinManagerOption("unknown", "npm"); ok {
		t.Fatal("BuiltinManagerOption(unknown, npm) should not be found")
	}
}

func TestBuiltinManagerNames_IncludesAliases(t *testing.T) {
	got := provider.BuiltinManagerNames("python")
	want := []string{"uv", "pip3", "pip"}
	if !slices.Equal(got, want) {
		t.Fatalf("BuiltinManagerNames(python) = %v, want %v", got, want)
	}
}

func TestBuiltinSettingsManagerNames_ExcludesAliases(t *testing.T) {
	got := provider.BuiltinSettingsManagerNames("python")
	want := []string{"uv", "pip3"}
	if !slices.Equal(got, want) {
		t.Fatalf("BuiltinSettingsManagerNames(python) = %v, want %v (pip alias must drop)", got, want)
	}
}

func TestBuiltinDefaultInstallProviderNames(t *testing.T) {
	got := provider.BuiltinDefaultInstallProviderNames()
	want := []string{"brew", "node", "python", "pip", "cargo"}
	if !slices.Equal(got, want) {
		t.Fatalf("BuiltinDefaultInstallProviderNames = %v, want %v", got, want)
	}
}

func TestBuiltinSystemProviderPriorityNames(t *testing.T) {
	got := provider.BuiltinSystemProviderPriorityNames()
	want := []string{"apt", "apk", "dnf", "zypper", "pacman", "brew"}
	if !slices.Equal(got, want) {
		t.Fatalf("BuiltinSystemProviderPriorityNames = %v, want %v", got, want)
	}
	// Must be a copy: mutating the result must not affect a later call.
	got[0] = "mutated"
	if again := provider.BuiltinSystemProviderPriorityNames(); again[0] != "apt" {
		t.Fatalf("result should be a defensive copy, second call = %v", again)
	}
}

func TestBuiltinConcreteProvidersForEcosystem(t *testing.T) {
	if got := provider.BuiltinConcreteProvidersForEcosystem(""); got != nil {
		t.Fatalf("empty ecosystem = %v, want nil", got)
	}
	system := provider.BuiltinConcreteProvidersForEcosystem("system")
	want := []string{"brew", "apt", "apk", "dnf", "pacman", "zypper"}
	if !slices.Equal(system, want) {
		t.Fatalf("system concrete providers = %v, want %v", system, want)
	}
	if slices.Contains(system, "apt_repo") {
		t.Errorf("apt_repo must be excluded: %v", system)
	}
	node := provider.BuiltinConcreteProvidersForEcosystem("node")
	if !slices.Equal(node, []string{"bun", "pnpm", "npm"}) {
		t.Fatalf("node concrete providers = %v, want bun,pnpm,npm", node)
	}
	python := provider.BuiltinConcreteProvidersForEcosystem("python")
	if slices.Contains(python, "pip3") {
		t.Errorf("pip3 alias must be excluded from python: %v", python)
	}
}

func TestMergeKnownNames(t *testing.T) {
	builtin := provider.BuiltinKnownNames()
	got := provider.MergeKnownNames([]string{"custom", "brew", ""})
	if len(got) != len(builtin)+1 {
		t.Fatalf("merged len = %d, want %d (dup brew and empty dropped)", len(got), len(builtin)+1)
	}
	// Unknown registered name has no display order and sorts to the end.
	if got[len(got)-1] != "custom" {
		t.Fatalf("custom should sort last, got tail %q (full %v)", got[len(got)-1], got)
	}
	if !slices.Contains(got, "brew") {
		t.Fatalf("merged should still contain builtin brew: %v", got)
	}
}

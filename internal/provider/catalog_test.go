package provider_test

import (
	"testing"

	"github.com/lkshrk/omni/internal/provider"
)

func TestBuiltinConcreteProviderPriorityNames(t *testing.T) {
	got := provider.BuiltinConcreteProviderPriorityNames()
	want := []string{"brew", "apt", "apk", "dnf", "pacman", "zypper", "bun", "pnpm", "npm", "uv", "pip", "cargo"}
	if len(got) != len(want) {
		t.Fatalf("got %v (len %d), want %v (len %d)", got, len(got), want, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
	for _, n := range got {
		if n == "script" || n == "pip3" {
			t.Errorf("priority list must exclude %q", n)
		}
	}
}

func TestBuiltinMetadata_RequiresPrivilege(t *testing.T) {
	// Single source of truth for the privilege display marker: only sudo-backed system PMs carry the flag.
	privileged := map[string]bool{"apt": true, "apk": true, "dnf": true, "pacman": true, "zypper": true}
	for _, name := range []string{"apt", "apk", "dnf", "pacman", "zypper", "brew", "system", "pip", "pip3", "uv", "npm", "bun", "cargo", "script", "unknown"} {
		want := privileged[name]
		if got := provider.BuiltinMetadata(name).RequiresPrivilege; got != want {
			t.Errorf("BuiltinMetadata(%q).RequiresPrivilege = %v, want %v", name, got, want)
		}
	}
}

func TestBuiltinMetadata_Script(t *testing.T) {
	meta := provider.BuiltinMetadata("script")
	if meta.Kind != provider.ProviderKindConcrete {
		t.Errorf("script Kind = %v, want ProviderKindConcrete", meta.Kind)
	}
	if meta.DisplayOrder != 400 {
		t.Errorf("script DisplayOrder = %d, want 400", meta.DisplayOrder)
	}
	if meta.Ecosystem != "" {
		t.Errorf("script Ecosystem = %q, want empty", meta.Ecosystem)
	}
}

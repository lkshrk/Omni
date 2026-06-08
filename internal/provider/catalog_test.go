package provider_test

import (
	"testing"

	"github.com/lkshrk/omni/internal/provider"
)

func TestBuiltinConcreteProviderPriorityNames(t *testing.T) {
	got := provider.BuiltinConcreteProviderPriorityNames()
	want := []string{"brew", "apt", "apk", "dnf", "pacman", "zypper", "bun", "pnpm", "npm", "uv", "pip"}
	if len(got) != len(want) {
		t.Fatalf("got %v (len %d), want %v (len %d)", got, len(got), want, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
	// script (special) and pip3 (alias of pip) must be excluded.
	for _, n := range got {
		if n == "script" || n == "pip3" {
			t.Errorf("priority list must exclude %q", n)
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

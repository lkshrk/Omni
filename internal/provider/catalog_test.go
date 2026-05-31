package provider_test

import (
	"testing"

	"github.com/lkshrk/omni/internal/provider"
)

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

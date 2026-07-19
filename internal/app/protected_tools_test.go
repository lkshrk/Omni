package app_test

import (
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/provider"
)

func TestValidateToolDeleteRejectsProviderAndManagerNames(t *testing.T) {
	t.Parallel()
	a, _ := newImportApp(t,
		&stubProvider{name: "brew", available: true},
		&stubProvider{name: "apt", available: true},
		&stubProvider{name: provider.EcosystemSystem, available: true},
	)

	for _, name := range []string{"brew", " apt ", provider.EcosystemSystem} {
		err := a.ValidateToolDelete(name)
		if err == nil {
			t.Fatalf("ValidateToolDelete(%q) = nil, want provider guard error", name)
		}
		if !strings.Contains(err.Error(), "package manager/provider") {
			t.Fatalf("ValidateToolDelete(%q) = %v, want provider guard message", name, err)
		}
	}

	for _, name := range []string{"ripgrep", " docker-desktop ", ""} {
		if err := a.ValidateToolDelete(name); err != nil {
			t.Fatalf("ValidateToolDelete(%q): %v", name, err)
		}
	}
}

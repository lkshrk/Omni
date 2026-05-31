package config_test

import (
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	_ "github.com/lkshrk/omni/internal/testguard"
)

func provVal() config.ProviderValidation {
	return config.ProviderValidation{
		Known:      []string{"brew", "uv", "bun", "apt", "pip3", "system", "node", "python"},
		Ecosystems: []string{"system", "node", "python"},
	}
}

func hasErrPath(errs []config.ValidationError, path string) bool {
	for _, e := range errs {
		if e.Path == path {
			return true
		}
	}
	return false
}

func TestValidateRoot_AcceptsConcreteProvider(t *testing.T) {
	root := &config.RootConfig{
		Settings: config.Settings{Providers: []config.ProviderEntry{{Name: "uv", Provider: "brew"}}},
	}
	if errs := config.ValidateRoot(root, provVal()); len(errs) != 0 {
		t.Errorf("concrete provider rejected: %v", errs)
	}
}

func TestValidateRoot_UnknownProviderInDefault(t *testing.T) {
	root := &config.RootConfig{
		Settings: config.Settings{Providers: []config.ProviderEntry{{Name: "uv", Provider: "nope"}}},
	}
	errs := config.ValidateRoot(root, provVal())
	if !hasErrPath(errs, "$.settings.providers[0].provider") {
		t.Errorf("missing error at default provider path: %v", errs)
	}
}

func TestValidateRoot_UnknownProviderInVariant(t *testing.T) {
	root := &config.RootConfig{
		Settings: config.Settings{Providers: []config.ProviderEntry{{
			Name: "uv", Provider: "brew",
			Variants: []config.ToolInstallSpec{{Provider: "nope"}},
		}}},
	}
	errs := config.ValidateRoot(root, provVal())
	if !hasErrPath(errs, "$.settings.providers[0].variants[0].provider") {
		t.Errorf("missing error at variant provider path: %v", errs)
	}
}

func TestValidateRoot_UnknownInstallWithInHost(t *testing.T) {
	root := &config.RootConfig{
		Settings: config.Settings{Providers: []config.ProviderEntry{{
			Name: "uv", Provider: "brew",
			Hosts: map[string]config.ToolInstallSpec{"box": {Provider: "brew", InstallWith: "nope"}},
		}}},
	}
	errs := config.ValidateRoot(root, provVal())
	found := false
	for _, e := range errs {
		if strings.HasPrefix(e.Path, "$.settings.providers[0].hosts.") && strings.HasSuffix(e.Path, ".install_with") {
			found = true
		}
	}
	if !found {
		t.Errorf("missing error at host install_with path: %v", errs)
	}
}

func TestValidateRoot_MissingProviderName(t *testing.T) {
	root := &config.RootConfig{
		Settings: config.Settings{Providers: []config.ProviderEntry{{Name: "", Provider: "brew"}}},
	}
	errs := config.ValidateRoot(root, provVal())
	if !hasErrPath(errs, "$.settings.providers[0].name") {
		t.Errorf("missing error at name path: %v", errs)
	}
}

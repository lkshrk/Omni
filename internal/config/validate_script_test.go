package config_test

import (
	"testing"

	"github.com/lkshrk/omni/internal/config"
	_ "github.com/lkshrk/omni/internal/testguard"
)

// scriptValidation validates a single logical tool "bun" using the script
// provider through the public ValidateRoot API. validateInstall is an
// unexported closure, so the script branch is exercised end-to-end here.
func scriptValidation(opts map[string]string) []config.ValidationError {
	root := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"bun": {Provider: "script", Options: opts},
		},
	}
	return config.ValidateRoot(root, scriptProvVal())
}

// scriptProvVal includes provider families so a passing script tool proves
// the ecosystem-only rule is skipped for the script provider.
func scriptProvVal() config.ProviderValidation {
	return config.ProviderValidation{
		Known:      []string{"script", "brew", "system", "node", "python"},
		Ecosystems: []string{"system", "node", "python"},
	}
}

func scriptHasErrorAt(errs []config.ValidationError, path string) bool {
	for _, e := range errs {
		if e.Path == path {
			return true
		}
	}
	return false
}

func TestScriptToolAccepted(t *testing.T) {
	errs := scriptValidation(map[string]string{
		"install": "curl -fsSL https://bun.sh/install | bash",
		"detect":  "bun",
	})
	if len(errs) != 0 {
		t.Errorf("valid script tool produced errors: %v", errs)
	}
}

func TestScriptMissingInstall(t *testing.T) {
	errs := scriptValidation(map[string]string{"detect": "bun"})
	if !scriptHasErrorAt(errs, `$.tools."bun".options.install`) {
		t.Errorf(`missing install: want error at $.tools."bun".options.install, got %v`, errs)
	}
}

func TestScriptMissingDetectAndCheck(t *testing.T) {
	errs := scriptValidation(map[string]string{"install": "x"})
	if !scriptHasErrorAt(errs, `$.tools."bun".options`) {
		t.Errorf(`missing detect/check: want error at $.tools."bun".options, got %v`, errs)
	}
}

func TestScriptCheckSatisfiesDetectRequirement(t *testing.T) {
	errs := scriptValidation(map[string]string{
		"install": "x",
		"check":   "test -x /root/.bun/bin/bun",
	})
	if len(errs) != 0 {
		t.Errorf("check should satisfy the detect-or-check rule: %v", errs)
	}
}

func TestScriptLatestRequiresVersion(t *testing.T) {
	errs := scriptValidation(map[string]string{
		"install": "x",
		"detect":  "bun",
		"latest":  "bun-latest",
	})
	if !scriptHasErrorAt(errs, `$.tools."bun".options.latest`) {
		t.Errorf(`latest without version: want error at $.tools."bun".options.latest, got %v`, errs)
	}
}

func TestRecipeBackedScriptLatestRequiresVersion(t *testing.T) {
	root := &config.RootConfig{Tools: map[string]config.ToolSpec{
		"gh": {Providers: []config.ToolInstallSpec{{
			Provider: "script", Options: map[string]string{"latest": "gh-latest"},
			Source: &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "cli", Repo: "cli"},
			Recipe: &config.FallbackRecipe{Type: config.FallbackRecipeGitHubReleaseAsset, AssetPattern: "gh_{version}_linux_amd64.tar.gz"},
		}}},
	}}
	errs := config.ValidateRoot(root, scriptProvVal())
	if !scriptHasErrorAt(errs, `$.tools."gh".providers[0].options.latest`) {
		t.Fatalf("recipe latest without version: want options.latest error, got %v", errs)
	}
}

// scriptProviderValidation drives a Settings.Providers entry using the script
// provider through ValidateRoot, exercising the validateProviderSpec branch.
func scriptProviderValidation(opts map[string]string) []config.ValidationError {
	root := &config.RootConfig{
		Settings: config.Settings{Providers: []config.ProviderEntry{
			{Name: "bun", Provider: "script", Options: opts},
		}},
	}
	return config.ValidateRoot(root, scriptProvVal())
}

func TestScriptProviderEntryAccepted(t *testing.T) {
	errs := scriptProviderValidation(map[string]string{
		"install": "curl -fsSL https://bun.sh/install | bash",
		"detect":  "bun",
	})
	if len(errs) != 0 {
		t.Errorf("valid script provider entry produced errors: %v", errs)
	}
}

func TestScriptProviderEntryMissingInstall(t *testing.T) {
	errs := scriptProviderValidation(map[string]string{"detect": "bun"})
	if !scriptHasErrorAt(errs, "$.settings.providers[0].options.install") {
		t.Errorf("missing install: want error at $.settings.providers[0].options.install, got %v", errs)
	}
}

func TestScriptProviderEntryMissingDetectAndCheck(t *testing.T) {
	errs := scriptProviderValidation(map[string]string{"install": "x"})
	if !scriptHasErrorAt(errs, "$.settings.providers[0].options") {
		t.Errorf("missing detect/check: want error at $.settings.providers[0].options, got %v", errs)
	}
}

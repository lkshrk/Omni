package config_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func aptRepoRecipeSpec(provider string) *config.RootConfig {
	return &config.RootConfig{
		Version: config.CurrentVersion,
		Tools: map[string]config.ToolSpec{"example-cli": {Providers: []config.ToolInstallSpec{{
			Provider: provider,
			Options: map[string]string{
				"key_url":        "https://example.com/key.asc",
				"signed_by":      "/etc/apt/keyrings/example.asc",
				"sources_format": "deb [arch={arch} signed-by={signed_by}] https://example.com/debian {suite} stable",
				"packages":       "example-cli",
			},
			Recipe: &config.FallbackRecipe{Type: config.FallbackRecipeAptRepo},
		}}}},
	}
}

func TestValidateRoot_AptRepoProviderWithRecipeIsValid(t *testing.T) {
	errs := config.ValidateRoot(aptRepoRecipeSpec("apt_repo"), config.ProviderValidation{})
	if len(errs) != 0 {
		t.Fatalf("errors = %v, want none for an apt_repo entry carrying an apt_repo recipe", errs)
	}
}

func TestValidateRoot_AptRepoRecipeValidatesTheSameUnderEitherProvider(t *testing.T) {
	underScript := config.ValidateRoot(aptRepoRecipeSpec("script"), config.ProviderValidation{})
	underAptRepo := config.ValidateRoot(aptRepoRecipeSpec("apt_repo"), config.ProviderValidation{})
	if fmt.Sprintf("%v", underScript) != fmt.Sprintf("%v", underAptRepo) {
		t.Fatalf("script errors = %v, apt_repo errors = %v; want the same recipe validation", underScript, underAptRepo)
	}
}

func TestValidateRoot_AptRepoRecipeMissingKeyURLIsRejected(t *testing.T) {
	cfg := aptRepoRecipeSpec("apt_repo")
	spec := cfg.Tools["example-cli"]
	delete(spec.Providers[0].Options, "key_url")
	cfg.Tools["example-cli"] = spec

	errs := config.ValidateRoot(cfg, config.ProviderValidation{})
	if !strings.Contains(fmt.Sprintf("%v", errs), "key_url") {
		t.Fatalf("errors = %v, want a key_url error", errs)
	}
}

// A plain-HTTP key URL lets an on-path attacker choose the signing key written to apt's root-owned keyring.
func TestValidateRoot_AptRepoRecipeInsecureKeyURLIsRejected(t *testing.T) {
	for _, keyURL := range []string{
		"http://apt.example.com/key.asc",
		"HTTP://apt.example.com/key.asc",
		"ftp://apt.example.com/key.asc",
		"file:///tmp/key.asc",
		"apt.example.com/key.asc",
		"https:///key.asc",
	} {
		t.Run(keyURL, func(t *testing.T) {
			cfg := aptRepoRecipeSpec("apt_repo")
			cfg.Tools["example-cli"].Providers[0].Options["key_url"] = keyURL

			errs := config.ValidateRoot(cfg, config.ProviderValidation{})
			if !strings.Contains(fmt.Sprintf("%v", errs), "key_url must use https") {
				t.Fatalf("errors = %v, want an https key_url error", errs)
			}
		})
	}
}

func TestValidateRoot_AptRepoRecipeHTTPSKeyURLIsAccepted(t *testing.T) {
	cfg := aptRepoRecipeSpec("apt_repo")
	cfg.Tools["example-cli"].Providers[0].Options["key_url"] = "HTTPS://apt.example.com/key.asc"

	if errs := config.ValidateRoot(cfg, config.ProviderValidation{}); len(errs) != 0 {
		t.Fatalf("errors = %v, want none for an https key_url", errs)
	}
}

// Without packages the recipe installs the logical name "example-cli" instead of the real distribution packages.
func TestValidateRoot_AptRepoRecipeMissingPackagesIsRejected(t *testing.T) {
	cfg := aptRepoRecipeSpec("apt_repo")
	spec := cfg.Tools["example-cli"]
	delete(spec.Providers[0].Options, "packages")
	cfg.Tools["example-cli"] = spec

	errs := config.ValidateRoot(cfg, config.ProviderValidation{})
	if !strings.Contains(fmt.Sprintf("%v", errs), "options.packages") {
		t.Fatalf("errors = %v, want an options.packages error", errs)
	}
}

func TestValidateRoot_AptRepoWithoutRecipeStillRequiresSetup(t *testing.T) {
	cfg := &config.RootConfig{
		Version: config.CurrentVersion,
		Tools: map[string]config.ToolSpec{"example-cli": {Providers: []config.ToolInstallSpec{{
			Provider: "apt_repo",
			Options:  map[string]string{"packages": "example-cli"},
		}}}},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{})
	if !strings.Contains(fmt.Sprintf("%v", errs), "options.setup") {
		t.Fatalf("errors = %v, want an options.setup error", errs)
	}
}

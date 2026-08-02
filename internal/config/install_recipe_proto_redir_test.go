package config_test

import (
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestShellRecipeFetchesRefuseRedirectDowngrade(t *testing.T) {
	for _, tc := range []struct {
		name, option string
		spec         config.ToolInstallSpec
	}{
		{"curl install script", "install", config.ToolInstallSpec{
			Options: map[string]string{"url": "https://example.com/install.sh"},
			Recipe:  &config.FallbackRecipe{Type: config.FallbackRecipeCurlInstallScript},
		}},
		{"apt key", "setup", config.ToolInstallSpec{
			Options: map[string]string{
				"key_url": "https://example.com/key.asc", "signed_by": "/etc/apt/keyrings/example.asc",
				"sources_format": "deb [signed-by={signed_by}] https://example.com/debian {suite} stable", "packages": "tool",
			},
			Recipe: &config.FallbackRecipe{Type: config.FallbackRecipeAptRepo},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := config.MaterializeInstallSpec("tool", tc.spec, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			command := got.Options[tc.option]
			for _, invocation := range strings.Split(command, "curl ")[1:] {
				if !strings.HasPrefix(invocation, "-fsSL --proto-redir =https ") {
					t.Fatalf("%s = %q", tc.option, command)
				}
			}
		})
	}
}

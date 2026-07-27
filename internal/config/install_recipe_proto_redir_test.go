package config_test

import (
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

// HTTPS origins must also reject plaintext redirects because fetched bytes may reach bash, executables, or apt's root keyring.
func TestMaterializedFetchesRefuseRedirectDowngrade(t *testing.T) {
	t.Parallel()
	binDir := t.TempDir()

	for _, tc := range []struct {
		name   string
		option string
		spec   config.ToolInstallSpec
	}{
		{
			name:   "curl_install_script is piped to bash",
			option: "install",
			spec: config.ToolInstallSpec{
				Options: map[string]string{"url": "https://example.com/install.sh"},
				Recipe:  &config.FallbackRecipe{Type: config.FallbackRecipeCurlInstallScript},
			},
		},
		{
			name:   "apt_repo key lands in the root keyring",
			option: "setup",
			spec: config.ToolInstallSpec{
				Options: map[string]string{
					"key_url":        "https://example.com/key.asc",
					"signed_by":      "/etc/apt/keyrings/example.asc",
					"sources_format": "deb [arch={arch} signed-by={signed_by}] https://example.com/debian {suite} stable",
					"packages":       "example-cli",
				},
				Recipe: &config.FallbackRecipe{Type: config.FallbackRecipeAptRepo},
			},
		},
		{
			name:   "apt_repo with a pinned suite",
			option: "setup",
			spec: config.ToolInstallSpec{
				Options: map[string]string{
					"key_url":        "https://example.com/key.asc",
					"signed_by":      "/etc/apt/keyrings/example.asc",
					"sources_format": "deb [arch={arch} signed-by={signed_by}] https://example.com/debian {suite} stable",
					"packages":       "example-cli",
					"suite":          "bookworm",
				},
				Recipe: &config.FallbackRecipe{Type: config.FallbackRecipeAptRepo},
			},
		},
		{
			name:   "resolved asset download is chmod +x'd",
			option: "install",
			spec: config.ToolInstallSpec{
				Source: &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "owner", Repo: "repo"},
				Recipe: &config.FallbackRecipe{
					Type:             config.FallbackRecipeGitHubReleaseAsset,
					AssetPattern:     "tool",
					AssetDownloadURL: "https://example.com/tool",
				},
			},
		},
		{
			name:   "resolved archive download is extracted",
			option: "install",
			spec: config.ToolInstallSpec{
				Source: &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "owner", Repo: "repo"},
				Recipe: &config.FallbackRecipe{
					Type:             config.FallbackRecipeGitHubReleaseAsset,
					AssetPattern:     "tool.tar.gz",
					AssetDownloadURL: "https://example.com/tool.tar.gz",
				},
			},
		},
		{
			name:   "resolved archive download into an extract dir",
			option: "install",
			spec: config.ToolInstallSpec{
				Options: map[string]string{"extract_dir": "/opt/tool"},
				Source:  &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "owner", Repo: "repo"},
				Recipe: &config.FallbackRecipe{
					Type:             config.FallbackRecipeGitHubReleaseAsset,
					AssetPattern:     "tool.tar.gz",
					AssetDownloadURL: "https://example.com/tool.tar.gz",
				},
			},
		},
		{
			name:   "arch-aware raw asset",
			option: "install",
			spec: config.ToolInstallSpec{
				Source: &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "owner", Repo: "repo"},
				Recipe: &config.FallbackRecipe{
					Type:         config.FallbackRecipeGitHubReleaseAsset,
					AssetPattern: "tool_{arch}",
				},
			},
		},
		{
			name:   "arch-aware archive asset",
			option: "install",
			spec: config.ToolInstallSpec{
				Source: &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "owner", Repo: "repo"},
				Recipe: &config.FallbackRecipe{
					Type:         config.FallbackRecipeGitHubReleaseAsset,
					AssetPattern: "tool_{arch}.tar.gz",
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := config.MaterializeInstallSpec("tool", tc.spec, binDir)
			if err != nil {
				t.Fatalf("MaterializeInstallSpec: %v", err)
			}
			command := got.Options[tc.option]
			if !strings.Contains(command, "curl ") {
				t.Fatalf("%s = %q, want a curl fetch", tc.option, command)
			}
			for _, invocation := range strings.Split(command, "curl ")[1:] {
				if !strings.HasPrefix(invocation, "-fsSL --proto-redir =https ") {
					t.Fatalf("%s = %q, want every curl fetch to refuse a redirect downgrade", tc.option, command)
				}
			}
		})
	}
}

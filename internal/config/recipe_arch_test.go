package config_test

import (
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	_ "github.com/lkshrk/omni/internal/testguard"
)

// The generated case statement is the only place one host can check the other architecture's asset name.
func TestMaterializeInstallSpec_ArchCaseCoversBothArchitectures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		archMap  string
		pattern  string
		expected []string
	}{
		{
			name:     "go-task",
			archMap:  "aarch64:arm64,x86_64:amd64,arm64:arm64,amd64:amd64",
			pattern:  "task_linux_{arch}.tar.gz",
			expected: []string{"a=arm64", "a=amd64", "x86_64", "aarch64"},
		},
		{
			name:     "rtk",
			archMap:  "aarch64:aarch64-unknown-linux-gnu,arm64:aarch64-unknown-linux-gnu,x86_64:x86_64-unknown-linux-musl,amd64:x86_64-unknown-linux-musl",
			pattern:  "rtk-{arch}.tar.gz",
			expected: []string{"a=aarch64-unknown-linux-gnu", "a=x86_64-unknown-linux-musl", "x86_64", "aarch64"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := config.MaterializeInstallSpec(tc.name, config.ToolInstallSpec{
				Provider: "script",
				Bin:      tc.name,
				Options:  map[string]string{"arch_map": tc.archMap},
				Source:   &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "o", Repo: "r"},
				Recipe:   &config.FallbackRecipe{Type: config.FallbackRecipeGitHubReleaseAsset, AssetPattern: tc.pattern},
			}, "~/.local/bin")
			if err != nil {
				t.Fatalf("MaterializeInstallSpec: %v", err)
			}
			install := got.Options["install"]
			for _, want := range tc.expected {
				if !strings.Contains(install, want) {
					t.Errorf("install command missing %q:\n%s", want, install)
				}
			}
			// An unpinned recipe must not bake "latest" into the asset name; it downloads from latest/download.
			if strings.Contains(install, "_latest") || strings.Contains(install, "-latest.") {
				t.Errorf("install command expanded {version} to \"latest\":\n%s", install)
			}
			if !strings.Contains(install, "releases/latest/download") {
				t.Errorf("install command should use the latest-download endpoint:\n%s", install)
			}
		})
	}
}

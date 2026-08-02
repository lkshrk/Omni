package config_test

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestMaterializeInstallSpec_CurlInstallScript(t *testing.T) {
	spec := config.ToolInstallSpec{
		Provider: "brew",
		Options:  map[string]string{"url": "https://example.com/install.sh", "check_path": "/usr/local/bin/tool"},
		Recipe:   &config.FallbackRecipe{Type: config.FallbackRecipeCurlInstallScript},
	}
	got, err := config.MaterializeInstallSpec("tool", spec, "")
	if err != nil {
		t.Fatalf("MaterializeInstallSpec: %v", err)
	}
	if got.Provider != "script" || !strings.Contains(got.Options["install"], "https://example.com/install.sh") {
		t.Fatalf("materialized curl recipe = %+v", got)
	}
}

func TestMaterializeInstallSpec_GitHubReleaseAssetUsesNativeProvider(t *testing.T) {
	spec := config.ToolInstallSpec{
		Provider: "brew",
		Source:   &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "opentofu", Repo: "opentofu"},
		Recipe:   &config.FallbackRecipe{Type: config.FallbackRecipeGitHubReleaseAsset, AssetPattern: "tofu_{version}_{os}_{arch}.zip"},
		Bin:      "tofu",
	}
	got, err := config.MaterializeInstallSpec("opentofu", spec, "~/.local/bin")
	if err != nil {
		t.Fatalf("MaterializeInstallSpec: %v", err)
	}
	if got.Provider != "script" {
		t.Fatalf("provider = %q, want script identity", got.Provider)
	}
	if got.InstallWith != config.ProviderGitHubReleaseAsset {
		t.Fatalf("install_with = %q, want native GitHub installer", got.InstallWith)
	}
	if got.Options["install"] != "" || got.Options["upgrade"] != "" {
		t.Fatalf("github recipe materialized shell commands: %+v", got.Options)
	}
	if got.Options["arch_map"] != "" {
		t.Fatalf("unexpected arch_map = %q", got.Options["arch_map"])
	}
	var encoded config.ToolInstallSpec
	if err := json.Unmarshal([]byte(got.Options[config.OptionGitHubReleaseAssetSpec]), &encoded); err != nil {
		t.Fatalf("decode native recipe: %v", err)
	}
	if encoded.Bin != "tofu" || encoded.BinDir != "~/.local/bin" || encoded.Recipe.AssetPattern != spec.Recipe.AssetPattern {
		t.Fatalf("encoded recipe = %+v", encoded)
	}
}

func TestMaterializeInstallSpec_GitHubReleaseAssetAcceptsLegacyExtractionOptions(t *testing.T) {
	for _, option := range []string{"extract_dir", "strip_components"} {
		spec := config.ToolInstallSpec{
			Source:  &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "o", Repo: "r"},
			Recipe:  &config.FallbackRecipe{Type: config.FallbackRecipeGitHubReleaseAsset, AssetPattern: "tool.tar.gz"},
			Options: map[string]string{option: "1"},
		}
		got, err := config.MaterializeInstallSpec("tool", spec, "")
		if err != nil || got.InstallWith != config.ProviderGitHubReleaseAsset {
			t.Fatalf("%s materialization = %+v, %v", option, got, err)
		}
	}
}

func TestMaterializeInstallSpec_AptRepo(t *testing.T) {
	spec := config.ToolInstallSpec{
		Provider: "script",
		Options: map[string]string{
			"key_url": "https://example.com/key.asc", "signed_by": "/etc/apt/keyrings/example.asc",
			"sources_format": "deb [arch={arch} signed-by={signed_by}] https://example.com/debian {suite} stable",
			"packages":       "example-cli",
		},
		Recipe: &config.FallbackRecipe{Type: config.FallbackRecipeAptRepo},
	}
	got, err := config.MaterializeInstallSpec("example-cli", spec, "")
	if err != nil {
		t.Fatalf("MaterializeInstallSpec: %v", err)
	}
	if got.Provider != "apt_repo" || got.Options["setup"] == "" || got.Options["packages"] != "example-cli" {
		t.Fatalf("materialized apt recipe = %+v", got)
	}
}

func TestGitHubReleaseAssetNameUsesArchitectureAlias(t *testing.T) {
	spec := config.ToolInstallSpec{
		Recipe:  &config.FallbackRecipe{Type: config.FallbackRecipeGitHubReleaseAsset, AssetPattern: "tool_{version}_{os}_{arch}"},
		Options: map[string]string{"arch_map": "amd64:x86_64,arm64:aarch64"},
	}
	name, err := config.GitHubReleaseAssetName("tool", spec, "v1.2.3")
	if err != nil {
		t.Fatalf("GitHubReleaseAssetName: %v", err)
	}
	wantArch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	if wantArch == "" {
		wantArch = runtime.GOARCH
	}
	if want := "tool_1.2.3_" + runtime.GOOS + "_" + wantArch; name != want {
		t.Fatalf("asset name = %q, want %q", name, want)
	}
}

func TestMaterializeInstallSpec_PreservesExplicitInstall(t *testing.T) {
	spec := config.ToolInstallSpec{
		Provider: "script",
		Options:  map[string]string{"install": "custom install"},
		Recipe:   &config.FallbackRecipe{Type: config.FallbackRecipeCurlInstallScript},
	}
	got, err := config.MaterializeInstallSpec("tool", spec, "")
	if err != nil || got.Options["install"] != "custom install" || got.Provider != "script" {
		t.Fatalf("MaterializeInstallSpec = %+v, %v", got, err)
	}
}

func TestMaterializeInstallSpec_GitHubRecipeIgnoresExplicitShellInstall(t *testing.T) {
	spec := config.ToolInstallSpec{
		Provider: "script",
		Options:  map[string]string{"install": "exit 1"},
		Source:   &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "o", Repo: "r"},
		Recipe:   &config.FallbackRecipe{Type: config.FallbackRecipeGitHubReleaseAsset, AssetPattern: "tool"},
	}
	got, err := config.MaterializeInstallSpec("tool", spec, "")
	if err != nil || got.InstallWith != config.ProviderGitHubReleaseAsset {
		t.Fatalf("MaterializeInstallSpec = %+v, %v", got, err)
	}
}

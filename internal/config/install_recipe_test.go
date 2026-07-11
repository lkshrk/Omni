package config_test

import (
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestMaterializeInstallSpec_CurlInstallScript(t *testing.T) {
	spec := config.ToolInstallSpec{
		Provider: "script",
		Options: map[string]string{
			"url":        "https://example.com/install.sh",
			"check_path": "/usr/local/bin/tool",
			"env":        "FOO=bar",
		},
		Recipe: &config.FallbackRecipe{Type: config.FallbackRecipeCurlInstallScript},
	}
	got, err := config.MaterializeInstallSpec("tool", spec, "")
	if err != nil {
		t.Fatalf("MaterializeInstallSpec: %v", err)
	}
	if got.Provider != "script" {
		t.Fatalf("provider = %q, want script", got.Provider)
	}
	if !strings.Contains(got.Options["install"], "https://example.com/install.sh") {
		t.Fatalf("install = %q, want curl install script", got.Options["install"])
	}
	if !strings.Contains(got.Options["check"], "/usr/local/bin/tool") {
		t.Fatalf("check = %q, want check_path", got.Options["check"])
	}
	if !strings.HasPrefix(got.Options["install"], "export FOO=bar; ") {
		t.Fatalf("install = %q, want env export prefix", got.Options["install"])
	}
}

func TestMaterializeInstallSpec_GitHubReleaseAssetArchAware(t *testing.T) {
	spec := config.ToolInstallSpec{
		Provider: "script",
		Source: &config.FallbackSource{
			Type:  config.FallbackSourceGitHub,
			Owner: "eza-community",
			Repo:  "eza",
		},
		Recipe: &config.FallbackRecipe{
			Type:         config.FallbackRecipeGitHubReleaseAsset,
			AssetPattern: "eza_{arch}-unknown-linux-gnu.tar.gz",
		},
	}
	got, err := config.MaterializeInstallSpec("eza", spec, "~/.local/bin")
	if err != nil {
		t.Fatalf("MaterializeInstallSpec: %v", err)
	}
	if !strings.Contains(got.Options["install"], "uname -m") {
		t.Fatalf("install = %q, want arch detection", got.Options["install"])
	}
	if !strings.Contains(got.Options["install"], "eza-community/eza/releases/latest/download") {
		t.Fatalf("install = %q, want latest release URL", got.Options["install"])
	}
}

func TestMaterializeInstallSpec_AptRepo(t *testing.T) {
	spec := config.ToolInstallSpec{
		Provider: "script",
		Options: map[string]string{
			"key_url":        "https://example.com/key.asc",
			"signed_by":      "/etc/apt/keyrings/example.asc",
			"sources_format": "deb [arch={arch} signed-by={signed_by}] https://example.com/debian {suite} stable",
			"packages":       "example-cli",
		},
		Recipe: &config.FallbackRecipe{Type: config.FallbackRecipeAptRepo},
	}
	got, err := config.MaterializeInstallSpec("example-cli", spec, "")
	if err != nil {
		t.Fatalf("MaterializeInstallSpec: %v", err)
	}
	if got.Provider != "apt_repo" {
		t.Fatalf("provider = %q, want apt_repo", got.Provider)
	}
	if got.Options["setup"] == "" || got.Options["packages"] != "example-cli" {
		t.Fatalf("options = %+v, want generated setup and packages", got.Options)
	}
	if strings.Contains(got.Options["setup"], "${line//") {
		t.Fatalf("setup = %q, want POSIX shell syntax", got.Options["setup"])
	}
	if !strings.Contains(got.Options["setup"], `sed "s/{suite}/$suite/g"`) {
		t.Fatalf("setup = %q, want suite substitution through sed", got.Options["setup"])
	}
}

func TestMaterializeInstallSpec_GitHubReleaseAssetPinnedTag(t *testing.T) {
	spec := config.ToolInstallSpec{
		Provider: "script",
		Source: &config.FallbackSource{
			Type:  config.FallbackSourceGitHub,
			Owner: "jesseduffield",
			Repo:  "lazygit",
		},
		Recipe: &config.FallbackRecipe{
			Type:         config.FallbackRecipeGitHubReleaseAsset,
			AssetPattern: "lazygit_0.62.2_linux_{arch}.tar.gz",
			TagName:      "v0.62.2",
		},
		Bin: "lazygit",
		Options: map[string]string{
			"arch_map": "aarch64:arm64,x86_64:x86_64,arm64:arm64,amd64:x86_64",
		},
	}
	got, err := config.MaterializeInstallSpec("lazygit", spec, "~/.local/bin")
	if err != nil {
		t.Fatalf("MaterializeInstallSpec: %v", err)
	}
	if !strings.Contains(got.Options["install"], "releases/download/v0.62.2") {
		t.Fatalf("install = %q, want pinned tag URL", got.Options["install"])
	}
}

func TestMaterializeInstallSpec_GitHubReleaseAssetRawBinaryArchMap(t *testing.T) {
	spec := config.ToolInstallSpec{
		Provider: "script",
		Source: &config.FallbackSource{
			Type:  config.FallbackSourceGitHub,
			Owner: "mikefarah",
			Repo:  "yq",
		},
		Recipe: &config.FallbackRecipe{
			Type:         config.FallbackRecipeGitHubReleaseAsset,
			AssetPattern: "yq_{arch}",
		},
		Bin: "yq",
		Options: map[string]string{
			"arch_map": "aarch64:linux_arm64,x86_64:linux_amd64,arm64:linux_arm64,amd64:linux_amd64",
		},
	}
	got, err := config.MaterializeInstallSpec("yq", spec, "~/.local/bin")
	if err != nil {
		t.Fatalf("MaterializeInstallSpec: %v", err)
	}
	if strings.Contains(got.Options["install"], "mktemp -d") {
		t.Fatalf("install = %q, want raw binary install without archive extract", got.Options["install"])
	}
	if !strings.Contains(got.Options["install"], "linux_amd64") {
		t.Fatalf("install = %q, want arch alias in generated asset name", got.Options["install"])
	}
}

func TestMaterializeInstallSpec_GitHubReleaseAssetOSArch(t *testing.T) {
	spec := config.ToolInstallSpec{
		Provider: "script",
		Source: &config.FallbackSource{
			Type:  config.FallbackSourceGitHub,
			Owner: "lkshrk",
			Repo:  "omni",
		},
		Recipe: &config.FallbackRecipe{
			Type:         config.FallbackRecipeGitHubReleaseAsset,
			AssetPattern: "omni_{os}_{arch}.tar.gz",
		},
		Bin: "omni",
		Options: map[string]string{
			"arch_map": "aarch64:arm64,x86_64:x86_64,arm64:arm64,amd64:x86_64",
		},
	}
	got, err := config.MaterializeInstallSpec("omni", spec, "~/.local/bin")
	if err != nil {
		t.Fatalf("MaterializeInstallSpec: %v", err)
	}
	if !strings.Contains(got.Options["install"], "uname -s") {
		t.Fatalf("install = %q, want runtime os detection", got.Options["install"])
	}
}

func TestMaterializeInstallSpec_PreservesExplicitInstall(t *testing.T) {
	spec := config.ToolInstallSpec{
		Provider: "script",
		Options:  map[string]string{"install": "true"},
		Recipe:   &config.FallbackRecipe{Type: config.FallbackRecipeCurlInstallScript},
	}
	got, err := config.MaterializeInstallSpec("tool", spec, "")
	if err != nil {
		t.Fatalf("MaterializeInstallSpec: %v", err)
	}
	if got.Options["install"] != "true" {
		t.Fatalf("install = %q, want unchanged", got.Options["install"])
	}
}

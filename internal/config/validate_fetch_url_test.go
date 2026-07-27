package config_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

var insecureFetchURLs = []string{
	"http://example.com/payload",
	"HTTP://example.com/payload",
	"ftp://example.com/payload",
	"file:///tmp/payload",
	"example.com/payload",
	"https:///payload",
}

func curlInstallScriptSpec(url string) *config.RootConfig {
	return &config.RootConfig{
		Version: config.CurrentVersion,
		Tools: map[string]config.ToolSpec{"example-cli": {Providers: []config.ToolInstallSpec{{
			Provider: "script",
			Options:  map[string]string{"url": url},
			Recipe:   &config.FallbackRecipe{Type: config.FallbackRecipeCurlInstallScript},
		}}}},
	}
}

// A plain-HTTP script piped to bash gives an on-path attacker the installer's privileges.
func TestValidateRoot_CurlInstallScriptInsecureURLIsRejected(t *testing.T) {
	for _, url := range insecureFetchURLs {
		t.Run(url, func(t *testing.T) {
			errs := config.ValidateRoot(curlInstallScriptSpec(url), config.ProviderValidation{})
			if !strings.Contains(fmt.Sprintf("%v", errs), "options.url must use https") {
				t.Fatalf("errors = %v, want an https options.url error", errs)
			}
		})
	}
}

func TestValidateRoot_CurlInstallScriptInsecureSourceURLIsRejected(t *testing.T) {
	cfg := curlInstallScriptSpec("")
	spec := cfg.Tools["example-cli"]
	spec.Providers[0].Options = nil
	spec.Providers[0].Source = &config.FallbackSource{Type: config.FallbackSourceGitHub, URL: "http://example.com/install.sh"}
	cfg.Tools["example-cli"] = spec

	errs := config.ValidateRoot(cfg, config.ProviderValidation{})
	if !strings.Contains(fmt.Sprintf("%v", errs), "options.url must use https") {
		t.Fatalf("errors = %v, want an https error for source.url", errs)
	}
}

func TestValidateRoot_CurlInstallScriptHTTPSURLIsAccepted(t *testing.T) {
	if errs := config.ValidateRoot(curlInstallScriptSpec("https://example.com/install.sh"), config.ProviderValidation{}); len(errs) != 0 {
		t.Fatalf("errors = %v, want none for an https url", errs)
	}
}

func TestMaterializeInstallSpec_CurlInstallScriptRejectsInsecureURL(t *testing.T) {
	spec := config.ToolInstallSpec{
		Provider: "script",
		Options:  map[string]string{"url": "http://example.com/install.sh"},
		Recipe:   &config.FallbackRecipe{Type: config.FallbackRecipeCurlInstallScript},
	}
	got, err := config.MaterializeInstallSpec("example-cli", spec, "")
	if err == nil {
		t.Fatalf("MaterializeInstallSpec = %+v, want an error for a plain-http url", got)
	}
	if !strings.Contains(err.Error(), "options.url must use https") {
		t.Fatalf("err = %v, want an https options.url error", err)
	}
	if strings.Contains(got.Options["install"], "http://example.com/install.sh") {
		t.Fatalf("install = %q, want no materialized command for a rejected url", got.Options["install"])
	}
}

func githubReleaseAssetSpec(downloadURL string) *config.RootConfig {
	return &config.RootConfig{
		Version: config.CurrentVersion,
		Tools: map[string]config.ToolSpec{"example-cli": {Providers: []config.ToolInstallSpec{{
			Provider: "script",
			Source:   &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "o", Repo: "r"},
			Recipe: &config.FallbackRecipe{
				Type:             config.FallbackRecipeGitHubReleaseAsset,
				AssetPattern:     "example-cli-{version}.tar.gz",
				AssetDownloadURL: downloadURL,
			},
		}}}},
	}
}

// A plain-HTTP asset lets an on-path attacker choose the binary later marked executable.
func TestValidateRoot_GitHubReleaseAssetInsecureDownloadURLIsRejected(t *testing.T) {
	for _, url := range insecureFetchURLs {
		t.Run(url, func(t *testing.T) {
			errs := config.ValidateRoot(githubReleaseAssetSpec(url), config.ProviderValidation{})
			if !strings.Contains(fmt.Sprintf("%v", errs), "asset_download_url must use https") {
				t.Fatalf("errors = %v, want an https asset_download_url error", errs)
			}
		})
	}
}

func TestValidateRoot_GitHubReleaseAssetHTTPSDownloadURLIsAccepted(t *testing.T) {
	for _, url := range []string{"", "https://example.com/example-cli.tar.gz"} {
		if errs := config.ValidateRoot(githubReleaseAssetSpec(url), config.ProviderValidation{}); len(errs) != 0 {
			t.Fatalf("errors = %v, want none for asset_download_url %q", errs, url)
		}
	}
}

func TestMaterializeInstallSpec_GitHubReleaseAssetRejectsInsecureDownloadURL(t *testing.T) {
	spec := config.ToolInstallSpec{
		Provider: "script",
		Source:   &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "o", Repo: "r"},
		Recipe: &config.FallbackRecipe{
			Type:             config.FallbackRecipeGitHubReleaseAsset,
			AssetPattern:     "example-cli-{version}.tar.gz",
			AssetDownloadURL: "http://example.com/example-cli.tar.gz",
		},
	}
	got, err := config.MaterializeInstallSpec("example-cli", spec, "")
	if err == nil {
		t.Fatalf("MaterializeInstallSpec = %+v, want an error for a plain-http asset_download_url", got)
	}
	if !strings.Contains(err.Error(), "asset_download_url must use https") {
		t.Fatalf("err = %v, want an https asset_download_url error", err)
	}
	if strings.Contains(got.Options["install"], "http://example.com/example-cli.tar.gz") {
		t.Fatalf("install = %q, want no materialized command for a rejected url", got.Options["install"])
	}
}

func toolFallbackSpec(downloadURL string) *config.RootConfig {
	return &config.RootConfig{
		Version: config.CurrentVersion,
		Tools: map[string]config.ToolSpec{"rg": {
			Providers: []config.ToolInstallSpec{{Provider: "brew"}},
			Fallback: &config.FallbackSpec{
				Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
				Status: config.FallbackStatusUnverified,
				Binary: "rg",
				Recipe: config.FallbackRecipe{
					Type:             config.FallbackRecipeGitHubReleaseAsset,
					AssetPattern:     "ripgrep-{version}-{os}-{arch}.tar.gz",
					AssetDownloadURL: downloadURL,
				},
			},
		}},
	}
}

// FallbackSpec.Recipe bypasses validateRecipeInstallSpec, so this check makes doctor report plaintext fallback assets before app-side sink guards.
func TestValidateRoot_ToolFallbackInsecureAssetDownloadURLIsRejected(t *testing.T) {
	insecure := append([]string{"//example.com/payload", "http://127.0.0.1:8080/payload", "http://localhost:8080/payload"}, insecureFetchURLs...)
	for _, url := range insecure {
		t.Run(url, func(t *testing.T) {
			errs := config.ValidateRoot(toolFallbackSpec(url), config.ProviderValidation{Known: []string{"brew"}})
			if !strings.Contains(fmt.Sprintf("%v", errs), "asset_download_url must use https") {
				t.Fatalf("errors = %v, want an https asset_download_url error", errs)
			}
			if !strings.Contains(fmt.Sprintf("%v", errs), `.fallback.recipe.asset_download_url`) {
				t.Fatalf("errors = %v, want the error pinned to the fallback recipe path", errs)
			}
		})
	}
}

func TestValidateRoot_ToolFallbackHTTPSAssetDownloadURLIsAccepted(t *testing.T) {
	for _, url := range []string{"", "https://example.com/rg.tar.gz", "HTTPS://example.com/rg.tar.gz"} {
		if errs := config.ValidateRoot(toolFallbackSpec(url), config.ProviderValidation{Known: []string{"brew"}}); len(errs) != 0 {
			t.Fatalf("errors = %v, want none for asset_download_url %q", errs, url)
		}
	}
}

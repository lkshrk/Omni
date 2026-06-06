package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"runtime"
	"strings"
	"time"

	"github.com/lkshrk/omni/internal/config"
)

const defaultGitHubAPIBase = "https://api.github.com"

type githubRelease struct {
	TagName    string        `json:"tag_name"`
	Draft      bool          `json:"draft"`
	Prerelease bool          `json:"prerelease"`
	Assets     []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func (a *App) SetGitHubFallbackAPIForTest(baseURL string, client *http.Client) {
	a.githubAPI = strings.TrimRight(baseURL, "/")
	a.githubClient = client
}

func (a *App) resolveGitHubFallback(ctx context.Context, name, owner, repoName string) (config.FallbackSpec, bool, error) {
	release, err := a.fetchLatestGitHubRelease(ctx, owner, repoName)
	if err != nil {
		return config.FallbackSpec{}, false, err
	}
	asset, ok := bestGitHubReleaseAsset(release.Assets, name)
	if !ok {
		return config.FallbackSpec{}, false, nil
	}
	binary := strings.TrimSpace(name)
	if binary == "" {
		binary = repoName
	}
	return config.FallbackSpec{
		Status:         config.FallbackStatusUnverified,
		Binary:         binary,
		ReleaseChannel: "stable",
		Recipe: config.FallbackRecipe{
			Type:         config.FallbackRecipeGitHubReleaseAsset,
			AssetPattern: asset.Name,
		},
		Commands: config.FallbackCommands{
			Install:   githubReleaseAssetInstallCommand(asset.BrowserDownloadURL),
			Check:     `test -x "{{bin_dir}}/{{binary}}"`,
			Uninstall: `rm -f "{{bin_dir}}/{{binary}}"`,
			Upgrade:   githubReleaseAssetInstallCommand(asset.BrowserDownloadURL),
			Version:   `"{{bin_dir}}/{{binary}}" --version`,
		},
	}, true, nil
}

func (a *App) fetchLatestGitHubRelease(ctx context.Context, owner, repoName string) (githubRelease, error) {
	client := a.githubClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	baseURL := strings.TrimRight(a.githubAPI, "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(os.Getenv("OMNI_GITHUB_API_BASE"), "/")
	}
	if baseURL == "" {
		baseURL = defaultGitHubAPIBase
	}
	url := baseURL + "/repos/" + owner + "/" + repoName + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return githubRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "omni")
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return githubRelease{}, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode == http.StatusNotFound {
		return githubRelease{}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return githubRelease{}, fmt.Errorf("github release lookup failed: %s", resp.Status)
	}
	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return githubRelease{}, err
	}
	return release, nil
}

func bestGitHubReleaseAsset(assets []githubAsset, binary string) (githubAsset, bool) {
	osNames := githubOSNames()
	archNames := githubArchNames()
	var fallback githubAsset
	var platformFallback githubAsset
	for _, asset := range assets {
		name := strings.ToLower(asset.Name)
		if asset.BrowserDownloadURL == "" || strings.Contains(name, "checksum") || strings.Contains(name, "sha256") {
			continue
		}
		if fallback.Name == "" {
			fallback = asset
		}
		if !containsAny(name, osNames) || !containsAny(name, archNames) {
			continue
		}
		if binary != "" && !strings.Contains(name, strings.ToLower(binary)) {
			continue
		}
		if platformFallback.Name == "" {
			platformFallback = asset
		}
		if githubAssetExtractable(name) {
			return asset, true
		}
	}
	if platformFallback.Name != "" {
		return platformFallback, true
	}
	if fallback.Name != "" {
		return fallback, true
	}
	return githubAsset{}, false
}

func githubAssetExtractable(name string) bool {
	for _, suffix := range []string{".tar.gz", ".tgz", ".zip"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func githubOSNames() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"darwin", "macos", "mac", "apple"}
	case "windows":
		return []string{"windows", "win"}
	default:
		return []string{runtime.GOOS}
	}
}

func githubArchNames() []string {
	switch runtime.GOARCH {
	case "amd64":
		return []string{"amd64", "x86_64", "x64"}
	case "arm64":
		return []string{"arm64", "aarch64"}
	default:
		return []string{runtime.GOARCH}
	}
}

func containsAny(s string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

func githubReleaseAssetInstallCommand(downloadURL string) string {
	assetName := path.Base(downloadURL)
	if strings.TrimSpace(assetName) == "" || assetName == "." || assetName == "/" {
		assetName = "{{asset_path}}"
	}
	return `mkdir -p "{{cache_dir}}" "{{bin_dir}}" && ` +
		`asset="{{cache_dir}}/` + assetName + `" && ` +
		`curl -fsSL "` + downloadURL + `" -o "$asset" && ` +
		`tmp="$(mktemp -d)" && ` +
		`case "$asset" in *.zip) unzip -q "$asset" -d "$tmp" ;; *.tar.gz|*.tgz) tar -xzf "$asset" -C "$tmp" ;; *) cp "$asset" "$tmp/{{binary}}" ;; esac && ` +
		`found="$(find "$tmp" -type f -perm -111 -name "{{binary}}" | head -n 1)" && ` +
		`test -n "$found" && cp "$found" "{{bin_dir}}/{{binary}}" && chmod +x "{{bin_dir}}/{{binary}}"`
}

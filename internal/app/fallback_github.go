package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"runtime"
	"strings"
	"time"

	"github.com/lkshrk/omni/internal/config"
)

const defaultGitHubAPIBase = "https://api.github.com"

type githubRelease struct {
	ID          json.Number   `json:"id"`
	TagName     string        `json:"tag_name"`
	PublishedAt string        `json:"published_at"`
	Draft       bool          `json:"draft"`
	Prerelease  bool          `json:"prerelease"`
	Assets      []githubAsset `json:"assets"`
}

type githubAsset struct {
	ID                 json.Number `json:"id"`
	Name               string      `json:"name"`
	BrowserDownloadURL string      `json:"browser_download_url"`
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
	publishedAt, err := normalizedGitHubPublishedAt(release.PublishedAt)
	if err != nil {
		return config.FallbackSpec{}, false, err
	}
	releaseID := strings.TrimSpace(release.ID.String())
	if releaseID == "" {
		return config.FallbackSpec{}, false, fmt.Errorf("github release %s/%s is missing id", owner, repoName)
	}
	tagName := strings.TrimSpace(release.TagName)
	if tagName == "" {
		return config.FallbackSpec{}, false, fmt.Errorf("github release %s/%s is missing tag_name", owner, repoName)
	}
	asset, ok := bestGitHubReleaseAsset(release.Assets, name)
	if !ok {
		binary := strings.TrimSpace(name)
		if binary == "" {
			binary = repoName
		}
		return config.FallbackSpec{
			Status:         config.FallbackStatusUnsupported,
			Binary:         binary,
			ReleaseChannel: "stable",
			Recipe: config.FallbackRecipe{
				ReleaseID:   releaseID,
				TagName:     tagName,
				PublishedAt: publishedAt,
			},
		}, true, nil
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
			Type:             config.FallbackRecipeGitHubReleaseAsset,
			AssetPattern:     asset.Name,
			ReleaseID:        releaseID,
			TagName:          tagName,
			PublishedAt:      publishedAt,
			AssetID:          strings.TrimSpace(asset.ID.String()),
			AssetName:        asset.Name,
			AssetDownloadURL: asset.BrowserDownloadURL,
		},
		Commands: config.FallbackCommands{
			Install:   githubReleaseAssetInstallCommand(asset.BrowserDownloadURL),
			Check:     `test -x {{bin_dir}}/{{binary}}`,
			Uninstall: `rm -f {{bin_dir}}/{{binary}}`,
			Upgrade:   githubReleaseAssetInstallCommand(asset.BrowserDownloadURL),
			Version:   `{{bin_dir}}/{{binary}} --version`,
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
	apiURL := baseURL + "/repos/" + owner + "/" + repoName + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return githubRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "omni")
	// Only attach the token to GitHub's own API to prevent credential leakage
	// when OMNI_GITHUB_API_BASE points at a non-GitHub host (e.g. a local test stub).
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		parsed, parseErr := url.Parse(baseURL)
		if parseErr == nil && isGitHubHost(parsed.Host) {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return githubRelease{}, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode == http.StatusNotFound {
		return githubRelease{}, fmt.Errorf("github latest release not found for %s/%s", owner, repoName)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return githubRelease{}, fmt.Errorf("github release lookup failed: %s", resp.Status)
	}
	var release githubRelease
	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&release); err != nil {
		return githubRelease{}, err
	}
	return release, nil
}

func bestGitHubReleaseAsset(assets []githubAsset, binary string) (githubAsset, bool) {
	osNames := githubOSNames()
	archNames := githubArchNames()
	for _, asset := range assets {
		name := strings.ToLower(asset.Name)
		if strings.TrimSpace(asset.ID.String()) == "" || strings.TrimSpace(asset.Name) == "" || strings.TrimSpace(asset.BrowserDownloadURL) == "" {
			continue
		}
		if githubReleaseAssetIgnored(name) {
			continue
		}
		if !containsAny(name, osNames) || !containsAny(name, archNames) {
			continue
		}
		if !githubAssetExtractable(name) {
			continue
		}
		if binary != "" && !strings.Contains(name, strings.ToLower(binary)) {
			continue
		}
		return asset, true
	}
	return githubAsset{}, false
}

func normalizedGitHubPublishedAt(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("github latest release is missing published_at")
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return "", fmt.Errorf("github latest release published_at %q is not RFC3339: %w", value, err)
	}
	return parsed.UTC().Format(time.RFC3339), nil
}

func githubFallbackHasSavedReleaseMetadata(fallback *config.FallbackSpec) bool {
	if fallback == nil {
		return false
	}
	if fallback.Source.Type != config.FallbackSourceGitHub {
		return false
	}
	if strings.TrimSpace(fallback.Source.Owner) == "" || strings.TrimSpace(fallback.Source.Repo) == "" {
		return false
	}
	recipe := fallback.Recipe
	if recipe.Type != config.FallbackRecipeGitHubReleaseAsset {
		return false
	}
	if strings.TrimSpace(recipe.ReleaseID) == "" ||
		strings.TrimSpace(recipe.TagName) == "" ||
		strings.TrimSpace(recipe.PublishedAt) == "" ||
		strings.TrimSpace(recipe.AssetID) == "" ||
		strings.TrimSpace(recipe.AssetName) == "" ||
		strings.TrimSpace(recipe.AssetDownloadURL) == "" {
		return false
	}
	if _, err := normalizedGitHubPublishedAt(recipe.PublishedAt); err != nil {
		return false
	}
	return true
}

func githubReleaseAssetIgnored(name string) bool {
	if strings.Contains(name, "checksum") || strings.Contains(name, "sha256") || strings.Contains(name, "sha512") {
		return true
	}
	if strings.Contains(name, "signature") || strings.HasSuffix(name, ".sig") || strings.HasSuffix(name, ".asc") {
		return true
	}
	if strings.Contains(name, "readme") || strings.Contains(name, "license") || strings.Contains(name, "docs") {
		return true
	}
	for _, suffix := range []string{".deb", ".rpm", ".pkg", ".msi", ".dmg"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
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

// isGitHubHost reports whether host is a recognized GitHub API hostname,
// ensuring we only send auth tokens to GitHub's own servers.
func isGitHubHost(host string) bool {
	host = strings.ToLower(host)
	return host == "api.github.com" || host == "github.com"
}

// shellSingleQuote wraps s in single quotes so it is treated as a literal
// value by sh -c, regardless of special characters it may contain.
func shellSingleQuote(s string) string {
	// Replace every ' with '\'' (close quote, literal single-quote, reopen quote).
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func githubReleaseAssetInstallCommand(downloadURL string) string {
	assetName := path.Base(downloadURL)
	if strings.TrimSpace(assetName) == "" || assetName == "." || assetName == "/" {
		// Fall back to the template variable; quoting is applied at render time.
		return `mkdir -p '{{cache_dir}}' '{{bin_dir}}' && ` +
			`asset='{{cache_dir}}/{{asset_path}}' && ` +
			`curl -fsSL '{{asset_path}}' -o "$asset" && ` +
			`tmp="$(mktemp -d)" && ` +
			`case "$asset" in *.zip) unzip -q "$asset" -d "$tmp" ;; *.tar.gz|*.tgz) tar -xzf "$asset" -C "$tmp" ;; *) cp "$asset" "$tmp/"'{{binary}}' ;; esac && ` +
			`found="$(find "$tmp" -type f -perm -111 -name '{{binary}}' | head -n 1)" && ` +
			`test -n "$found" && cp "$found" '{{bin_dir}}'/'{{binary}}' && chmod +x '{{bin_dir}}'/'{{binary}}'`
	}
	// Both downloadURL (from GitHub API browser_download_url) and assetName
	// (derived from it via path.Base) are shell-quoted so a malicious or
	// compromised release URL cannot inject shell commands.
	return `mkdir -p '{{cache_dir}}' '{{bin_dir}}' && ` +
		`asset='{{cache_dir}}'/` + shellSingleQuote(assetName) + ` && ` +
		`curl -fsSL ` + shellSingleQuote(downloadURL) + ` -o "$asset" && ` +
		`tmp="$(mktemp -d)" && ` +
		`case "$asset" in *.zip) unzip -q "$asset" -d "$tmp" ;; *.tar.gz|*.tgz) tar -xzf "$asset" -C "$tmp" ;; *) cp "$asset" "$tmp/"'{{binary}}' ;; esac && ` +
		`found="$(find "$tmp" -type f -perm -111 -name '{{binary}}' | head -n 1)" && ` +
		`test -n "$found" && cp "$found" '{{bin_dir}}'/'{{binary}}' && chmod +x '{{bin_dir}}'/'{{binary}}'`
}

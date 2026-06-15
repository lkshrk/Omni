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
		client = &http.Client{Timeout: 15 * time.Second, CheckRedirect: stripAuthOnRedirect}
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

// scoreGitHubAsset returns a relevance score for a release asset name against
// the current runtime platform. A score ≤ 0 means the asset is not usable.
// Higher scores are preferred. The function is deterministic and pure.
//
// Scoring breakdown:
//   - OS match: exact GOOS=20, alias=15; no match → 0 (reject)
//   - Arch match: exact GOARCH=10, alias=7; no match → 0 (reject)
//   - Archive type: .tar.gz/.tgz=4, .zip=3, .tar.xz=2, .tar.bz2=1, bare binary=1
//   - libc (linux only): musl=2, gnu/glibc=1
//   - ignored suffix (.sig/.asc/.sbom/…) → 0
func scoreGitHubAsset(assetName string) int {
	name := strings.ToLower(assetName)

	// Rejected up-front: ignored suffixes score zero.
	if githubReleaseAssetIgnored(name) {
		return 0
	}

	// OS score — exact GOOS beats alias.
	osScore := 0
	switch runtime.GOOS {
	case "darwin":
		if strings.Contains(name, "darwin") {
			osScore = 20
		} else if strings.Contains(name, "macos") || strings.Contains(name, "mac") || strings.Contains(name, "apple") {
			osScore = 15
		}
	case "windows":
		if strings.Contains(name, "windows") {
			osScore = 20
		} else if strings.Contains(name, "win") {
			osScore = 15
		}
	default:
		// linux and other GOOS: only exact match.
		if strings.Contains(name, runtime.GOOS) {
			osScore = 20
		}
	}
	if osScore == 0 {
		return 0
	}

	// Arch score — exact GOARCH beats alias.
	archScore := 0
	switch runtime.GOARCH {
	case "amd64":
		if strings.Contains(name, "amd64") {
			archScore = 10
		} else if strings.Contains(name, "x86_64") || strings.Contains(name, "x64") {
			archScore = 7
		}
	case "arm64":
		if strings.Contains(name, "arm64") {
			archScore = 10
		} else if strings.Contains(name, "aarch64") {
			archScore = 7
		}
	case "386":
		if strings.Contains(name, "386") {
			archScore = 10
		} else if strings.Contains(name, "i386") || strings.Contains(name, "x86") {
			archScore = 7
		}
	default:
		if strings.Contains(name, runtime.GOARCH) {
			archScore = 10
		}
	}
	if archScore == 0 {
		return 0
	}

	// Archive-type score.
	archiveScore := 0
	switch {
	case strings.HasSuffix(name, ".tar.gz"), strings.HasSuffix(name, ".tgz"):
		archiveScore = 4
	case strings.HasSuffix(name, ".zip"):
		archiveScore = 3
	case strings.HasSuffix(name, ".tar.xz"):
		archiveScore = 2
	case strings.HasSuffix(name, ".tar.bz2"):
		archiveScore = 1
	default:
		// Bare binary or unknown archive — include with minimal weight only when
		// no extension flags it as a package format (deb/rpm/msi/…).
		if !strings.ContainsAny(name, ".") || strings.HasSuffix(name, ".gz") {
			archiveScore = 1
		}
	}
	if archiveScore == 0 {
		return 0
	}

	// libc preference on linux: musl is statically linked and more portable.
	libcScore := 0
	if runtime.GOOS == "linux" {
		if strings.Contains(name, "musl") {
			libcScore = 2
		} else if strings.Contains(name, "gnu") || strings.Contains(name, "glibc") {
			libcScore = 1
		}
	}

	return osScore + archScore + archiveScore + libcScore
}

func bestGitHubReleaseAsset(assets []githubAsset, binary string) (githubAsset, bool) {
	best := githubAsset{}
	bestScore := 0
	found := false

	for _, asset := range assets {
		if strings.TrimSpace(asset.ID.String()) == "" || strings.TrimSpace(asset.Name) == "" || strings.TrimSpace(asset.BrowserDownloadURL) == "" {
			continue
		}
		name := strings.ToLower(asset.Name)
		if githubReleaseAssetIgnored(name) {
			continue
		}
		if binary != "" && !strings.Contains(name, strings.ToLower(binary)) {
			continue
		}
		score := scoreGitHubAsset(asset.Name)
		if score <= 0 {
			continue
		}
		// Pick the highest-scoring asset; on an equal-score tie, prefer the
		// lexicographically smaller name so the choice is deterministic regardless
		// of the order GitHub returns assets.
		if !found || score > bestScore || (score == bestScore && asset.Name < best.Name) {
			best = asset
			bestScore = score
			found = true
		}
	}
	return best, found
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
	// Detached signatures and SBOM attestations are metadata, not installable assets.
	if strings.Contains(name, "signature") || strings.HasSuffix(name, ".sig") ||
		strings.HasSuffix(name, ".asc") || strings.HasSuffix(name, ".sbom") {
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
	for _, suffix := range []string{".tar.gz", ".tgz", ".tar.xz", ".zip"} {
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
		// No URL available: asset_path (already absolute, quoted at render time)
		// is used directly as the local archive to extract.
		return `mkdir -p {{cache_dir}} {{bin_dir}} && ` +
			`asset={{asset_path}} && ` +
			`tmp="$(mktemp -d)" && ` +
			`case "$asset" in *.zip) unzip -q "$asset" -d "$tmp" ;; *.tar.gz|*.tgz) tar -xzf "$asset" -C "$tmp" ;; *) cp "$asset" "$tmp/"{{binary}} ;; esac && ` +
			`found="$(find "$tmp" -type f -perm -111 -name {{binary}} | head -n 1)" && ` +
			`test -n "$found" && cp "$found" {{bin_dir}}/{{binary}} && chmod +x {{bin_dir}}/{{binary}}`
	}
	// downloadURL and assetName (from GitHub API browser_download_url) are
	// shell-quoted at construction time; {{var}} placeholders are quoted at
	// render time — so no placeholder appears inside a surrounding quote pair.
	return `mkdir -p {{cache_dir}} {{bin_dir}} && ` +
		`asset={{cache_dir}}/` + shellSingleQuote(assetName) + ` && ` +
		`curl -fsSL ` + shellSingleQuote(downloadURL) + ` -o "$asset" && ` +
		`tmp="$(mktemp -d)" && ` +
		`case "$asset" in *.zip) unzip -q "$asset" -d "$tmp" ;; *.tar.gz|*.tgz) tar -xzf "$asset" -C "$tmp" ;; *) cp "$asset" "$tmp/"{{binary}} ;; esac && ` +
		`found="$(find "$tmp" -type f -perm -111 -name {{binary}} | head -n 1)" && ` +
		`test -n "$found" && cp "$found" {{bin_dir}}/{{binary}} && chmod +x {{bin_dir}}/{{binary}}`
}

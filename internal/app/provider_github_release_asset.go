package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/version"
)

type githubReleaseAssetProvider struct {
	app  *App
	next provider.Provider
}

func (p *githubReleaseAssetProvider) Name() string { return config.ProviderGitHubReleaseAsset }

func (p *githubReleaseAssetProvider) Description() string { return "Install GitHub release assets" }

func (p *githubReleaseAssetProvider) Available(context.Context) (bool, error) { return true, nil }

func (p *githubReleaseAssetProvider) Install(ctx context.Context, tool provider.Tool) error {
	fallback, err := p.resolve(ctx, tool)
	if err != nil {
		return err
	}
	return p.app.nativeGitHubInstallPipeline(ctx, tool.Name, fallback)
}

func (p *githubReleaseAssetProvider) Upgrade(ctx context.Context, tool provider.Tool) error {
	return p.Install(ctx, tool)
}

func (p *githubReleaseAssetProvider) Uninstall(ctx context.Context, tool provider.Tool) error {
	spec, err := configuredGitHubRecipeSpec(tool)
	if err != nil {
		return err
	}
	if strings.TrimSpace(spec.Options["uninstall"]) != "" {
		if p.next == nil {
			return fmt.Errorf("github_release_asset %s: script provider unavailable for custom uninstall", tool.Name)
		}
		tool.Options = spec.Options
		return p.next.Uninstall(ctx, tool)
	}
	return p.app.nativeUninstallFallback(ctx, tool.Name, configuredGitHubFallback(tool.Name, spec))
}

func (p *githubReleaseAssetProvider) IsInstalled(ctx context.Context, tool provider.Tool) (bool, string, error) {
	spec, err := configuredGitHubRecipeSpec(tool)
	if err != nil {
		return false, "", err
	}
	check := strings.TrimSpace(spec.Options["check"])
	detect := strings.TrimSpace(spec.Options["detect"])
	versionCommand := strings.TrimSpace(spec.Options["version"])
	if check != "" || detect != "" || versionCommand != "" {
		if p.next == nil {
			return false, "", fmt.Errorf("github_release_asset %s: script provider unavailable for custom check", tool.Name)
		}
		tool.Options = cloneOptionMap(spec.Options)
		if strings.TrimSpace(spec.Bin) != "" {
			tool.Options[config.OptionBin] = spec.Bin
		}
		if versionCommand == "" {
			tool.Options[config.OptionRecordedVersion] = configuredGitHubRecipeVersion(spec)
		}
		if check == "" && detect == "" {
			installed, err := p.configuredBinaryInstalled(tool.Name, spec)
			if !installed || err != nil {
				return installed, "", err
			}
			tool.Options["check"] = "exit 0"
		}
		return p.next.IsInstalled(ctx, tool)
	}
	installed, err := p.configuredBinaryInstalled(tool.Name, spec)
	if !installed || err != nil {
		return installed, "", err
	}
	configuredVersion := configuredGitHubRecipeVersion(spec)
	if configuredVersion == "" {
		configuredVersion = p.configuredBinaryVersion(ctx, tool.Name, spec)
	}
	return true, configuredVersion, nil
}

func (p *githubReleaseAssetProvider) configuredBinaryInstalled(name string, spec config.ToolInstallSpec) (bool, error) {
	binPath, err := p.configuredBinaryPath(name, spec)
	if err != nil {
		return false, err
	}
	info, err := os.Stat(binPath)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil && info.Mode().IsRegular(), err
}

func (p *githubReleaseAssetProvider) configuredBinaryVersion(ctx context.Context, name string, spec config.ToolInstallSpec) string {
	binPath, err := p.configuredBinaryPath(name, spec)
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx = executor.WithOutputLimit(ctx, 64<<10)
	stdout, stderr, err := p.app.fallbackExecutor().Run(ctx, binPath, "--version")
	if err != nil {
		return ""
	}
	if detected := version.Extract(stdout); detected != "" {
		return detected
	}
	return version.Extract(stderr)
}

func (p *githubReleaseAssetProvider) configuredBinaryPath(name string, spec config.ToolInstallSpec) (string, error) {
	fallback := configuredGitHubFallback(name, spec)
	cacheDir, err := p.app.fallbackCacheDir()
	if err != nil {
		return "", err
	}
	binDir, err := p.app.fallbackBinDir(fallback, cacheDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(binDir, filepath.Base(fallback.Binary)), nil
}

func configuredGitHubRecipeVersion(spec config.ToolInstallSpec) string {
	version := strings.TrimSpace(spec.Recipe.InstalledVersion)
	if version == "" {
		version = strings.TrimSpace(spec.Recipe.TagName)
	}
	return normalizeFallbackVersion(version)
}

func (p *githubReleaseAssetProvider) ListInstalled(ctx context.Context) ([]provider.InstalledTool, error) {
	return nil, nil
}

func (p *githubReleaseAssetProvider) CheckOutdated(ctx context.Context, tool provider.Tool, currentVersion string) (string, bool, bool, error) {
	spec, err := configuredGitHubRecipeSpec(tool)
	if err != nil {
		return "", false, true, err
	}
	if strings.TrimSpace(spec.Options["latest"]) == "" {
		return "", false, false, nil
	}
	checker, ok := p.next.(provider.ToolOutdatedChecker)
	if !ok {
		return "", false, true, fmt.Errorf("github_release_asset %s: script provider unavailable for custom latest check", tool.Name)
	}
	tool.Options = spec.Options
	return checker.CheckOutdated(ctx, tool, currentVersion)
}

func (p *githubReleaseAssetProvider) resolve(ctx context.Context, tool provider.Tool) (*config.FallbackSpec, error) {
	spec, err := configuredGitHubRecipeSpec(tool)
	if err != nil {
		return nil, err
	}
	fallback := configuredGitHubFallback(tool.Name, spec)
	tag := strings.TrimSpace(fallback.Recipe.TagName)
	if tag == "" {
		tag = strings.TrimSpace(spec.Options["release_tag"])
	}
	downloadResolved := strings.TrimSpace(fallback.Recipe.AssetDownloadURL) != ""
	needsRelease := !downloadResolved || (strings.TrimSpace(fallback.Recipe.ChecksumAssetPattern) != "" && tag == "")
	var release githubRelease
	if needsRelease {
		if tag == "" {
			release, err = p.app.fetchLatestGitHubRelease(ctx, fallback.Source.Owner, fallback.Source.Repo)
		} else {
			release, err = p.app.fetchGitHubReleaseByTag(ctx, fallback.Source.Owner, fallback.Source.Repo, tag)
		}
		if err != nil {
			return nil, err
		}
		fallback.Recipe.ReleaseID = strings.TrimSpace(release.ID.String())
		fallback.Recipe.TagName = strings.TrimSpace(release.TagName)
		fallback.Recipe.PublishedAt = strings.TrimSpace(release.PublishedAt)
		if !downloadResolved {
			asset, ok := configuredGitHubReleaseAsset(tool.Name, spec, release)
			if !ok {
				return nil, fmt.Errorf("GitHub release %s for %s does not contain configured asset %q", release.TagName, tool.Name, fallback.Recipe.AssetPattern)
			}
			fallback.Recipe.AssetID = strings.TrimSpace(asset.ID.String())
			fallback.Recipe.AssetName = asset.Name
			fallback.Recipe.AssetDownloadURL = asset.BrowserDownloadURL
		}
	} else if tag != "" {
		fallback.Recipe.TagName = tag
	}
	if fallback.Recipe.AssetName == "" {
		fallback.Recipe.AssetName = filepath.Base(fallback.Recipe.AssetDownloadURL)
	}
	if err := expandConfiguredChecksumAssetPattern(tool.Name, spec, fallback); err != nil {
		return nil, err
	}
	return fallback, nil
}

func expandConfiguredChecksumAssetPattern(name string, spec config.ToolInstallSpec, fallback *config.FallbackSpec) error {
	if strings.TrimSpace(fallback.Recipe.ChecksumAssetPattern) == "" {
		return nil
	}
	recipe := *spec.Recipe
	recipe.AssetPattern = recipe.ChecksumAssetPattern
	spec.Recipe = &recipe
	resolved, err := config.GitHubReleaseAssetName(name, spec, fallback.Recipe.TagName)
	if err != nil {
		return err
	}
	fallback.Recipe.ChecksumAssetPattern = resolved
	return nil
}

func configuredGitHubRecipeSpec(tool provider.Tool) (config.ToolInstallSpec, error) {
	encoded := strings.TrimSpace(tool.Options[config.OptionGitHubReleaseAssetSpec])
	if encoded == "" {
		return config.ToolInstallSpec{}, fmt.Errorf("github_release_asset %s: missing native recipe", tool.Name)
	}
	var spec config.ToolInstallSpec
	if err := json.Unmarshal([]byte(encoded), &spec); err != nil {
		return config.ToolInstallSpec{}, fmt.Errorf("github_release_asset %s: decode native recipe: %w", tool.Name, err)
	}
	return spec, nil
}

func configuredGitHubFallback(name string, spec config.ToolInstallSpec) *config.FallbackSpec {
	binary := strings.TrimSpace(spec.Bin)
	if binary == "" {
		binary = name
	}
	return &config.FallbackSpec{
		Source: *spec.Source,
		Binary: binary,
		BinDir: spec.BinDir,
		Recipe: *spec.Recipe,
	}
}

var _ provider.Provider = (*githubReleaseAssetProvider)(nil)
var _ provider.ToolOutdatedChecker = (*githubReleaseAssetProvider)(nil)

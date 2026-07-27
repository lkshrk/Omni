package app

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/lkshrk/omni/internal/config"
	isync "github.com/lkshrk/omni/internal/sync"
)

// An unpinned github_release_asset recipe installs from releases/latest/download, so which
// release it landed on is knowable only while installing. Recording it is what keeps the tool
// from reporting an empty version forever; the returned value is the normalized version, or
// "" when nothing was recorded.
func (a *App) recordConfiguredGitHubRecipeVersion(ctx context.Context, name string, configured config.ToolInstallSpec) (string, error) {
	if !unpinnedGitHubRecipe(configured) {
		return "", nil
	}
	release, err := a.fetchLatestGitHubReleaseCached(ctx, nil, configured.Source.Owner, configured.Source.Repo)
	if err != nil {
		return "", nil
	}
	installed := normalizeFallbackVersion(release.TagName)
	if installed == "" {
		return "", nil
	}
	if _, ok := configuredGitHubReleaseAsset(name, configured, release); !ok {
		return "", nil
	}
	// withConfig turns errSkipSave into a nil error, so what is left is a real write failure — swallowing
	// it left the tool reporting an unknown version forever with nothing said about why.
	err = a.withConfig(func(cfg *config.RootConfig) error {
		spec, ok := cfg.Tools[name]
		if !ok || !setGitHubRecipeInstalledVersion(&spec, configured, installed) {
			return errSkipSave
		}
		cfg.Tools[name] = spec
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("recording installed version for %s: %w", name, err)
	}
	return installed, nil
}

func (a *App) recordInstalledGitHubRecipeVersions(ctx context.Context, result *isync.SyncResult, resolved []resolvedTool) {
	if result == nil {
		return
	}
	byName := make(map[string]resolvedTool, len(resolved))
	for _, t := range resolved {
		byName[t.entry.Name] = t
	}
	for _, op := range result.Installed() {
		tool, ok := byName[op.Tool.Name]
		if !ok {
			continue
		}
		installed, err := a.recordConfiguredGitHubRecipeVersion(ctx, op.Tool.Name, tool.route.ConfiguredInstall)
		if err != nil {
			result.Warnings = append(result.Warnings, err.Error())
			continue
		}
		if installed == "" {
			continue
		}
		cached, err := a.readDB().Get(ctx, op.Tool.Name, tool.entry.Provider, tool.entry.EffectivePackage())
		if err != nil {
			result.Warnings = append(result.Warnings, "reading tool cache for "+op.Tool.Name+": "+err.Error())
			continue
		}
		if cached == nil {
			continue
		}
		cached.Version = sql.NullString{String: installed, Valid: true}
		if err := a.readDB().Upsert(ctx, cached); err != nil {
			result.Warnings = append(result.Warnings, "recording installed version for "+op.Tool.Name+": "+err.Error())
		}
	}
}

func unpinnedGitHubRecipe(spec config.ToolInstallSpec) bool {
	if spec.Source == nil || spec.Source.Type != config.FallbackSourceGitHub {
		return false
	}
	if spec.Recipe == nil || spec.Recipe.Type != config.FallbackRecipeGitHubReleaseAsset {
		return false
	}
	if strings.TrimSpace(spec.Source.Owner) == "" || strings.TrimSpace(spec.Source.Repo) == "" {
		return false
	}
	if strings.TrimSpace(spec.Options["install"]) != "" {
		return false
	}
	return strings.TrimSpace(spec.Recipe.TagName) == "" && strings.TrimSpace(spec.Options["release_tag"]) == ""
}

func setGitHubRecipeInstalledVersion(spec *config.ToolSpec, configured config.ToolInstallSpec, installed string) bool {
	changed := false
	apply := func(entry *config.ToolInstallSpec) {
		if !sameGitHubRecipeTarget(*entry, configured) || entry.Recipe.InstalledVersion == installed {
			return
		}
		recipe := *entry.Recipe
		recipe.InstalledVersion = installed
		entry.Recipe = &recipe
		changed = true
	}
	for i := range spec.Providers {
		apply(&spec.Providers[i])
	}
	for i := range spec.Variants {
		apply(&spec.Variants[i])
	}
	for host, entry := range spec.Hosts {
		apply(&entry)
		spec.Hosts[host] = entry
	}
	return changed
}

func sameGitHubRecipeTarget(entry, configured config.ToolInstallSpec) bool {
	if !unpinnedGitHubRecipe(entry) {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(entry.Source.Owner), strings.TrimSpace(configured.Source.Owner)) &&
		strings.EqualFold(strings.TrimSpace(entry.Source.Repo), strings.TrimSpace(configured.Source.Repo)) &&
		strings.TrimSpace(entry.Recipe.AssetPattern) == strings.TrimSpace(configured.Recipe.AssetPattern)
}

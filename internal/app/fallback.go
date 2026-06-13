package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/dots"
	"github.com/lkshrk/omni/internal/executor"
)

const fallbackInstalledWithGitHub = "gh"

var errFallbackNotConfigured = errors.New("fallback not configured")

var fallbackTemplatePattern = regexp.MustCompile(`\{\{\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\}\}`)

var fallbackTemplateVariables = map[string]struct{}{
	"arch":       {},
	"asset_path": {},
	"binary":     {},
	"bin_dir":    {},
	"cache_dir":  {},
	"os":         {},
	"repo":       {},
	"version":    {},
}

func (a *App) SetFallbackExecutor(exec executor.Executor) {
	a.fallbackExec = exec
}

// SaveToolFallback stores a global fallback recipe for an existing logical tool.
// It only mutates settings.json; install/sync actions decide later whether to use it.
func (a *App) SaveToolFallback(_ context.Context, name string, fallback config.FallbackSpec) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("tool name is required")
	}
	if err := validateFallbackCommandTemplates(name, fallback.Commands); err != nil {
		return err
	}
	return a.withConfig(func(cfg *config.RootConfig) error {
		if cfg.Tools == nil {
			return fmt.Errorf("tool %q not found", name)
		}
		spec, ok := cfg.Tools[name]
		if !ok {
			return fmt.Errorf("tool %q not found", name)
		}
		spec.Fallback = &fallback
		cfg.Tools[name] = spec
		return nil
	})
}

func (a *App) ToolFallback(name string) (*config.FallbackSpec, bool, error) {
	_, fallback, err := a.configuredFallback(name)
	if err != nil {
		if errors.Is(err, errFallbackNotConfigured) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return fallback, true, nil
}

func (a *App) SaveToolFallbackFromGitHub(ctx context.Context, name, repo string) error {
	owner, repoName, err := a.githubRepoForTool(ctx, name, repo)
	if err != nil {
		return err
	}
	binary := strings.TrimSpace(name)
	fallback, resolved, err := a.resolveGitHubFallback(ctx, name, owner, repoName)
	if err != nil {
		return err
	}
	if !resolved {
		return fmt.Errorf("github fallback %s/%s: no supported release asset for %s on %s/%s", owner, repoName, binary, runtime.GOOS, runtime.GOARCH)
	}
	fallback.Source = config.FallbackSource{
		Type:  config.FallbackSourceGitHub,
		Owner: owner,
		Repo:  repoName,
		URL:   "https://github.com/" + owner + "/" + repoName,
	}
	return a.SaveToolFallback(ctx, name, fallback)
}

func (a *App) githubRepoForTool(_ context.Context, name, repo string) (string, string, error) {
	repo = strings.TrimSpace(repo)
	if repo != "" {
		return parseGitHubRepo(repo)
	}
	name = strings.TrimSpace(name)
	cfg, err := a.loadConfig()
	if err != nil {
		return "", "", err
	}
	spec, ok := cfg.Tools[name]
	if !ok {
		return "", "", fmt.Errorf("tool %q not found", name)
	}
	if strings.TrimSpace(spec.Git) == "" {
		return "", "", fmt.Errorf("tools fallback requires --from-github owner/repo or a GitHub git URL in tool config")
	}
	owner, repoName, err := parseGitHubRepo(spec.Git)
	if err != nil {
		return "", "", fmt.Errorf("tool %q git URL is not a GitHub repo; pass --from-github owner/repo", name)
	}
	return owner, repoName, nil
}

func (a *App) SaveToolFallbackFromGitHubSpec(ctx context.Context, name, repo string, fallback config.FallbackSpec) error {
	owner, repoName, err := parseGitHubRepo(repo)
	if err != nil {
		return err
	}
	fallback.Source = config.FallbackSource{
		Type:  config.FallbackSourceGitHub,
		Owner: owner,
		Repo:  repoName,
		URL:   "https://github.com/" + owner + "/" + repoName,
	}
	return a.SaveToolFallback(ctx, name, fallback)
}

func (a *App) InstallToolFallback(ctx context.Context, name string) error {
	spec, fallback, err := a.configuredFallback(name)
	if err != nil {
		return err
	}
	if err := a.runFallbackCommand(ctx, name, "install", spec, fallback, fallback.Commands.Install); err != nil {
		_ = a.setToolFallbackStatus(name, config.FallbackStatusFailed)
		_ = a.readDB().MarkFailed(ctx, name, fallbackProvider(spec), fallbackPackage(name, spec), err.Error())
		return err
	}
	installed, err := a.CheckToolFallback(ctx, name)
	if err != nil {
		_ = a.setToolFallbackStatus(name, config.FallbackStatusFailed)
		_ = a.readDB().MarkFailed(ctx, name, fallbackProvider(spec), fallbackPackage(name, spec), err.Error())
		return err
	}
	if !installed {
		_ = a.setToolFallbackStatus(name, config.FallbackStatusFailed)
		_ = a.readDB().MarkFailed(ctx, name, fallbackProvider(spec), fallbackPackage(name, spec), "fallback install verification failed")
		return fmt.Errorf("fallback install verification failed for %s: check command did not pass", name)
	}
	if err := a.setToolFallbackStatus(name, config.FallbackStatusVerified); err != nil {
		return err
	}
	if err := a.readDB().Upsert(ctx, &database.ToolCache{
		Name:          name,
		Provider:      fallbackProvider(spec),
		Package:       fallbackPackage(name, spec),
		Installed:     true,
		InstalledWith: fallbackInstalledWith(fallback),
		Version:       sql.NullString{},
		LastChecked:   time.Now(),
	}); err != nil {
		return err
	}
	return a.readDB().UpdateOutdated(ctx, name, fallbackProvider(spec), fallbackPackage(name, spec), false, "")
}

func (a *App) CheckToolFallback(ctx context.Context, name string) (bool, error) {
	spec, fallback, err := a.configuredFallback(name)
	if err != nil {
		return false, err
	}
	return a.checkToolFallbackWithSpec(ctx, name, spec, fallback)
}

func (a *App) checkToolFallbackWithSpec(ctx context.Context, name string, spec config.ToolSpec, fallback *config.FallbackSpec) (bool, error) {
	command := strings.TrimSpace(fallback.Commands.Check)
	if command == "" {
		return false, fmt.Errorf("fallback %s: missing check command", name)
	}
	command, err := a.renderFallbackCommand(name, spec, fallback, command)
	if err != nil {
		return false, err
	}
	ctx = traceReason(ctx, "checking fallback", name, "gh")
	_, _, err = a.fallbackExecutor().Run(ctx, "sh", "-c", command)
	return err == nil, nil
}

func (a *App) UpgradeToolFallback(ctx context.Context, name string) error {
	spec, fallback, err := a.configuredFallback(name)
	if err != nil {
		return err
	}
	upgradeFallback, refreshed, err := a.githubFallbackUpgradeCandidate(ctx, name, spec, fallback)
	if err != nil {
		_ = a.setToolFallbackStatus(name, config.FallbackStatusFailed)
		return err
	}
	command := upgradeFallback.Commands.Upgrade
	if strings.TrimSpace(command) == "" {
		command = upgradeFallback.Commands.Install
	}
	if err := a.runFallbackCommand(ctx, name, "upgrade", spec, upgradeFallback, command); err != nil {
		_ = a.setToolFallbackStatus(name, config.FallbackStatusFailed)
		return err
	}
	installed, err := a.checkToolFallbackWithSpec(ctx, name, spec, upgradeFallback)
	if err != nil {
		_ = a.setToolFallbackStatus(name, config.FallbackStatusFailed)
		return err
	}
	if !installed {
		_ = a.setToolFallbackStatus(name, config.FallbackStatusFailed)
		return fmt.Errorf("fallback upgrade verification failed for %s: check command did not pass", name)
	}
	if refreshed {
		verified := *upgradeFallback
		verified.Status = config.FallbackStatusVerified
		if err := a.SaveToolFallback(ctx, name, verified); err != nil {
			return err
		}
	} else {
		if err := a.setToolFallbackStatus(name, config.FallbackStatusVerified); err != nil {
			return err
		}
	}
	if err := a.readDB().Upsert(ctx, &database.ToolCache{
		Name:          name,
		Provider:      fallbackProvider(spec),
		Package:       fallbackPackage(name, spec),
		Installed:     true,
		InstalledWith: fallbackInstalledWith(upgradeFallback),
		Version:       sql.NullString{},
		LastChecked:   time.Now(),
	}); err != nil {
		return err
	}
	return a.readDB().UpdateOutdated(ctx, name, fallbackProvider(spec), fallbackPackage(name, spec), false, "")
}

func (a *App) githubFallbackUpgradeCandidate(ctx context.Context, name string, spec config.ToolSpec, fallback *config.FallbackSpec) (*config.FallbackSpec, bool, error) {
	if !githubFallbackHasSavedReleaseMetadata(fallback) {
		return fallback, false, nil
	}
	cached, err := a.readDB().Get(ctx, name, fallbackProvider(spec), fallbackPackage(name, spec))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fallback, false, nil
		}
		return nil, false, err
	}
	if !cached.Installed || cached.InstalledWith != fallbackInstalledWithGitHub || !cached.Outdated {
		return fallback, false, nil
	}
	resolved, ok, err := a.resolveGitHubFallback(ctx, name, strings.TrimSpace(fallback.Source.Owner), strings.TrimSpace(fallback.Source.Repo))
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, fmt.Errorf("github fallback %s/%s: no supported release asset for %s on %s/%s", strings.TrimSpace(fallback.Source.Owner), strings.TrimSpace(fallback.Source.Repo), name, runtime.GOOS, runtime.GOARCH)
	}
	resolved.Source = fallback.Source
	resolved.BinDir = fallback.BinDir
	return &resolved, true, nil
}

func (a *App) UninstallToolFallback(ctx context.Context, name string) error {
	spec, fallback, err := a.configuredFallback(name)
	if err != nil {
		return err
	}
	if strings.TrimSpace(fallback.Commands.Uninstall) == "" {
		return fmt.Errorf("fallback uninstall is not available for %s", name)
	}
	if err := a.runFallbackCommand(ctx, name, "uninstall", spec, fallback, fallback.Commands.Uninstall); err != nil {
		return err
	}
	return a.readDB().Delete(ctx, name, fallbackProvider(spec), fallbackPackage(name, spec))
}

func (a *App) configuredFallback(name string) (config.ToolSpec, *config.FallbackSpec, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return config.ToolSpec{}, nil, fmt.Errorf("tool name is required")
	}
	cfg, err := a.loadConfig()
	if err != nil {
		return config.ToolSpec{}, nil, fmt.Errorf("loading config: %w", err)
	}
	spec, ok := cfg.Tools[name]
	if !ok {
		return config.ToolSpec{}, nil, fmt.Errorf("tool %q not found", name)
	}
	if spec.Fallback == nil {
		return config.ToolSpec{}, nil, fmt.Errorf("%w: tool %q has no fallback", errFallbackNotConfigured, name)
	}
	return spec, spec.Fallback, nil
}

func (a *App) runFallbackCommand(ctx context.Context, name, action string, spec config.ToolSpec, fallback *config.FallbackSpec, command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return fmt.Errorf("fallback %s: missing %s command", name, action)
	}
	rendered, err := a.renderFallbackCommand(name, spec, fallback, command)
	if err != nil {
		return err
	}
	ctx = traceReason(ctx, action+" fallback", name, "gh")
	_, stderr, err := a.fallbackExecutor().Run(ctx, "sh", "-c", rendered)
	if err != nil {
		return fmt.Errorf("fallback %s %s: %w (stderr: %s)", name, action, err, strings.TrimSpace(stderr))
	}
	return nil
}

func (a *App) renderFallbackCommand(name string, spec config.ToolSpec, fallback *config.FallbackSpec, command string) (string, error) {
	vars, err := a.fallbackCommandVars(name, spec, fallback)
	if err != nil {
		return "", err
	}
	var renderErr error
	rendered := fallbackTemplatePattern.ReplaceAllStringFunc(command, func(match string) string {
		if renderErr != nil {
			return match
		}
		parts := fallbackTemplatePattern.FindStringSubmatch(match)
		value, ok := vars[parts[1]]
		if !ok {
			renderErr = fmt.Errorf("fallback %s: unknown fallback template variable %q", name, parts[1])
			return match
		}
		return value
	})
	if renderErr != nil {
		return "", renderErr
	}
	return rendered, nil
}

func validateFallbackCommandTemplates(name string, commands config.FallbackCommands) error {
	for _, command := range []string{commands.Install, commands.Check, commands.Uninstall, commands.Upgrade, commands.Version} {
		for _, match := range fallbackTemplatePattern.FindAllStringSubmatch(command, -1) {
			if len(match) < 2 {
				continue
			}
			if _, ok := fallbackTemplateVariables[match[1]]; !ok {
				return fmt.Errorf("fallback %s: unknown fallback template variable %q", name, match[1])
			}
		}
	}
	return nil
}

func (a *App) fallbackCommandVars(name string, spec config.ToolSpec, fallback *config.FallbackSpec) (map[string]string, error) {
	binary := strings.TrimSpace(fallback.Binary)
	if binary == "" {
		binary = name
	}
	cacheDir, err := a.fallbackCacheDir()
	if err != nil {
		return nil, err
	}
	binDir, err := a.fallbackBinDir(fallback, cacheDir)
	if err != nil {
		return nil, err
	}
	repo := ""
	if fallback.Source.Owner != "" && fallback.Source.Repo != "" {
		repo = fallback.Source.Owner + "/" + fallback.Source.Repo
	}
	assetPath := filepath.Join(cacheDir, fallbackPackage(name, spec))
	if assetName := strings.TrimSpace(fallback.Recipe.AssetPattern); assetName != "" {
		assetPath = filepath.Join(cacheDir, filepath.Base(assetName))
	}
	return map[string]string{
		"arch":       runtime.GOARCH,
		"asset_path": assetPath,
		"binary":     binary,
		"bin_dir":    binDir,
		"cache_dir":  cacheDir,
		"os":         runtime.GOOS,
		"repo":       repo,
		"version":    "",
	}, nil
}

func (a *App) fallbackCacheDir() (string, error) {
	root := strings.TrimSpace(a.CacheDir)
	if root == "" {
		root = a.configDir()
	}
	root, err := dots.ExpandTilde(root)
	if err != nil {
		return "", fmt.Errorf("resolving fallback cache dir: %w", err)
	}
	return filepath.Join(root, "fallback"), nil
}

func (a *App) fallbackBinDir(fallback *config.FallbackSpec, cacheDir string) (string, error) {
	binDir := strings.TrimSpace(fallback.BinDir)
	if binDir == "" {
		if cfg, err := a.loadConfig(); err == nil {
			binDir = strings.TrimSpace(a.effectiveSettings(cfg).FallbackBinDir)
		}
	}
	if binDir == "" {
		binDir = filepath.Join(cacheDir, "bin")
	}
	expanded, err := dots.ExpandTilde(binDir)
	if err != nil {
		return "", fmt.Errorf("resolving fallback bin dir: %w", err)
	}
	return expanded, nil
}

func (a *App) fallbackExecutor() executor.Executor {
	if a.fallbackExec != nil {
		return a.fallbackExec
	}
	return a.newExecutor()
}

func (a *App) setToolFallbackStatus(name, status string) error {
	return a.withConfig(func(cfg *config.RootConfig) error {
		spec, ok := cfg.Tools[name]
		if !ok || spec.Fallback == nil {
			return nil
		}
		spec.Fallback.Status = status
		cfg.Tools[name] = spec
		return nil
	})
}

func (a *App) automaticFallbackUsableForTool(name string, allowFailed bool) (bool, error) {
	_, fallback, err := a.configuredFallback(name)
	if err == nil {
		return automaticFallbackUsable(fallback, allowFailed), nil
	}
	if errors.Is(err, errFallbackNotConfigured) {
		return false, nil
	}
	return false, err
}

func automaticFallbackUsable(fallback *config.FallbackSpec, allowFailed bool) bool {
	if fallback == nil {
		return false
	}
	switch fallback.Status {
	case config.FallbackStatusFailed:
		if !allowFailed {
			return false
		}
	case config.FallbackStatusUnresolved, config.FallbackStatusUnsupported:
		return false
	}
	return strings.TrimSpace(fallback.Commands.Install) != "" && strings.TrimSpace(fallback.Commands.Check) != ""
}

func fallbackLifecycleOwner(installedWith string) bool {
	return installedWith == fallbackInstalledWithGitHub || installedWith == "fallback"
}

func fallbackInstalledWith(fallback *config.FallbackSpec) string {
	if fallback != nil && fallback.Source.Type == config.FallbackSourceGitHub {
		return fallbackInstalledWithGitHub
	}
	return "fallback"
}

func fallbackPackage(name string, spec config.ToolSpec) string {
	install := spec.DefaultInstallSpec()
	if strings.TrimSpace(install.Package) != "" {
		return install.Package
	}
	if strings.TrimSpace(spec.Package) != "" {
		return spec.Package
	}
	return name
}

func fallbackProvider(spec config.ToolSpec) string {
	install := spec.DefaultInstallSpec()
	if strings.TrimSpace(install.Provider) != "" {
		return install.Provider
	}
	return spec.Provider
}

func parseGitHubRepo(repo string) (string, string, error) {
	repo = strings.TrimSpace(repo)
	if strings.ContainsAny(repo, "?#") {
		return "", "", fmt.Errorf("github repo must be owner/repo")
	}
	if strings.HasPrefix(repo, "git@github.com:") {
		return parseGitHubRepoPath(strings.TrimPrefix(repo, "git@github.com:"))
	}
	if strings.HasPrefix(repo, "http://") || strings.HasPrefix(repo, "https://") {
		parsed, err := url.Parse(repo)
		if err != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", "", fmt.Errorf("github repo must be owner/repo")
		}
		host := strings.TrimPrefix(strings.ToLower(parsed.Host), "www.")
		if host != "github.com" {
			return "", "", fmt.Errorf("github repo must be owner/repo")
		}
		return parseGitHubRepoPath(parsed.Path)
	}
	repo = strings.TrimPrefix(repo, "github.com/")
	repo = strings.TrimPrefix(repo, "www.github.com/")
	return parseGitHubRepoPath(repo)
}

func parseGitHubRepoPath(repo string) (string, string, error) {
	repo = strings.Trim(strings.TrimSpace(repo), "/")
	repo = strings.TrimSuffix(repo, ".git")
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", fmt.Errorf("github repo must be owner/repo")
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), nil
}

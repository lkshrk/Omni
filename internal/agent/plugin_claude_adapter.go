package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lkshrk/omni/internal/config"
)

type claudeCodePluginAdapter struct {
	exec      func(context.Context, string, ...string) (string, string, error)
	lookupEnv func(string) (string, bool)
}

func NewClaudeCodePluginAdapter(
	execFn func(context.Context, string, ...string) (string, string, error),
	lookupEnv func(string) (string, bool),
) PluginAdapter {
	return &claudeCodePluginAdapter{exec: execFn, lookupEnv: lookupEnv}
}

func (a *claudeCodePluginAdapter) ID() string { return "claude-code" }

func (a *claudeCodePluginAdapter) Available() bool {
	_, err := lookPath("claude")
	return err == nil
}

func (a *claudeCodePluginAdapter) InstallPlugin(ctx context.Context, p config.Plugin) error {
	id := p.Name + "@" + p.Marketplace
	stdout, stderr, err := a.exec(ctx, "claude", "plugins", "install", id)
	if err != nil {
		return fmt.Errorf("claude plugins install %s: %w: %s", id, err, stderr)
	}
	if strings.Contains(stdout, claudeInstallFailureMarker) || strings.Contains(stderr, claudeInstallFailureMarker) {
		return fmt.Errorf("claude plugins install %s: %s", id, strings.TrimSpace(stdout+stderr))
	}
	return nil
}

// Needs the full name@marketplace identity (a bare name is ambiguous across marketplaces) and --yes, which the CLI demands off a TTY.
func (a *claudeCodePluginAdapter) RemovePlugin(ctx context.Context, p config.Plugin) error {
	id := p.Name + "@" + p.Marketplace
	stdout, stderr, err := a.exec(ctx, "claude", "plugins", "uninstall", id, "--yes")
	if err != nil {
		return fmt.Errorf("claude plugins uninstall %s: %w: %s", id, err, stderr)
	}
	if strings.Contains(stdout, claudeUninstallFailureMarker) || strings.Contains(stderr, claudeUninstallFailureMarker) {
		return fmt.Errorf("claude plugins uninstall %s: %s", id, strings.TrimSpace(stdout+stderr))
	}
	return nil
}

// `claude plugin update` exits 0 on failure too, so only this output substring distinguishes them; the ✘ glyph is excluded as it renders inconsistently.
const claudeUpdateFailureMarker = "Failed to update"

// Install, uninstall and marketplace add share the same exit-0-on-failure contract, distinguishable only by a "Failed to ..." output line.
const (
	claudeInstallFailureMarker        = "Failed to install"
	claudeUninstallFailureMarker      = "Failed to uninstall"
	claudeMarketplaceAddFailureMarker = "Failed to add marketplace"
)

// The live CLI rejects a bare name as not found despite its help text showing a single <plugin> positional.
func (a *claudeCodePluginAdapter) UpdatePlugin(ctx context.Context, name, marketplace string) error {
	id := name + "@" + marketplace
	stdout, stderr, err := a.exec(ctx, "claude", "plugin", "update", id)
	if err != nil {
		return fmt.Errorf("claude plugin update %s: %w: %s", id, err, stderr)
	}
	if strings.Contains(stdout, claudeUpdateFailureMarker) || strings.Contains(stderr, claudeUpdateFailureMarker) {
		return fmt.Errorf("claude plugin update %s: %s", id, strings.TrimSpace(stdout+stderr))
	}
	return nil
}

// Source only: claude derives its own marketplace name, which may not match config.Marketplace.Name, and omni never issues `marketplace remove`.
func (a *claudeCodePluginAdapter) AddMarketplace(ctx context.Context, m config.Marketplace) error {
	stdout, stderr, err := a.exec(ctx, "claude", "plugins", "marketplace", "add", m.Source)
	if err != nil {
		return fmt.Errorf("claude plugins marketplace add %s: %w: %s", m.Source, err, stderr)
	}
	if strings.Contains(stdout, claudeMarketplaceAddFailureMarker) || strings.Contains(stderr, claudeMarketplaceAddFailureMarker) {
		return fmt.Errorf("claude plugins marketplace add %s: %s", m.Source, strings.TrimSpace(stdout+stderr))
	}
	return nil
}

// Mirrors an element of `claude plugins list --json`, which --available rewraps unchanged inside {installed, available}.
type claudePluginListEntry struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Scope   string `json:"scope"`
	Enabled bool   `json:"enabled"`
}

// The "source" field is either an object or a bare string that is itself the plugin's subdirectory within the marketplace clone.
type claudeAvailableSource struct {
	Sha  string
	Path string
}

func (s *claudeAvailableSource) UnmarshalJSON(data []byte) error {
	var obj struct {
		Sha  string `json:"sha"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal(data, &obj); err == nil {
		s.Sha = obj.Sha
		s.Path = obj.Path
		return nil
	}
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		s.Sha = ""
		s.Path = str
		return nil
	}
	return nil
}

// Real payloads carry no version field, so both spellings are read defensively and LatestVersion stays empty rather than being faked from Source.Ref.
type claudeAvailableEntry struct {
	Name            string                `json:"name"`
	MarketplaceName string                `json:"marketplaceName"`
	Version         string                `json:"version"`
	LatestVersion   string                `json:"latestVersion"`
	Source          claudeAvailableSource `json:"source"`
}

func (e claudeAvailableEntry) versionLike() string {
	if e.Version != "" {
		return e.Version
	}
	return e.LatestVersion
}

type claudePluginListAvailableResponse struct {
	Installed *[]claudePluginListEntry `json:"installed"`
	Available *[]claudeAvailableEntry  `json:"available"`
}

func (a *claudeCodePluginAdapter) ListPlugins(ctx context.Context) ([]InstalledPlugin, error) {
	installedShas := readClaudeInstalledPluginShas()
	home, homeErr := os.UserHomeDir()

	stdout, _, err := a.exec(ctx, "claude", "plugins", "list", "--json", "--available")
	if err == nil {
		var resp *claudePluginListAvailableResponse
		installed, isArray, jsonErr := decodeCLIList[claudePluginListEntry](stdout, &resp)
		if jsonErr != nil {
			return nil, fmt.Errorf("claude plugins list --available: parse json: %w", jsonErr)
		}
		// The bare-array form carries only installed entries, so no latest version or sha can be derived.
		var available []claudeAvailableEntry
		if !isArray {
			if resp == nil {
				return nil, fmt.Errorf("claude plugins list --available: parse json: expected object, got null")
			}
			if resp.Installed == nil || resp.Available == nil {
				return nil, fmt.Errorf("claude plugins list --available: parse json: expected installed and available arrays")
			}
			installed, available = *resp.Installed, *resp.Available
		}
		latestByIdentity := make(map[string]string, len(available))
		latestShaByIdentity := make(map[string]string, len(available))
		pathByIdentity := make(map[string]string, len(available))
		for _, e := range available {
			identity := e.Name + "@" + e.MarketplaceName
			if v := e.versionLike(); v != "" {
				latestByIdentity[identity] = v
			}
			if e.Source.Sha != "" {
				latestShaByIdentity[identity] = e.Source.Sha
			}
			if e.Source.Path != "" {
				pathByIdentity[identity] = e.Source.Path
			}
		}
		plugins := make([]InstalledPlugin, 0, len(installed))
		for _, e := range installed {
			name, marketplace := splitPluginIdentity(e.ID)
			identity := name + "@" + marketplace
			plugin := InstalledPlugin{
				Name:          name,
				Marketplace:   marketplace,
				Version:       e.Version,
				LatestVersion: latestByIdentity[identity],
				Sha:           installedShas[identity],
				LatestSha:     latestShaByIdentity[identity],
			}
			if homeErr == nil {
				if path, ok := pathByIdentity[identity]; ok {
					plugin.PathOutdated = a.pathOutdated(ctx, home, marketplace, path, plugin.Sha)
				}
			}
			plugins = append(plugins, plugin)
		}
		return plugins, nil
	}

	stdout, stderr, err := a.exec(ctx, "claude", "plugins", "list", "--json")
	if err != nil {
		return nil, fmt.Errorf("claude plugins list: %w: %s", err, stderr)
	}
	var entries []claudePluginListEntry
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		return nil, fmt.Errorf("claude plugins list: parse json: %w", err)
	}
	if entries == nil {
		return nil, fmt.Errorf("claude plugins list: parse json: expected array, got null")
	}
	plugins := make([]InstalledPlugin, 0, len(entries))
	for _, e := range entries {
		name, marketplace := splitPluginIdentity(e.ID)
		identity := name + "@" + marketplace
		plugins = append(plugins, InstalledPlugin{
			Name:        name,
			Marketplace: marketplace,
			Version:     e.Version,
			Sha:         installedShas[identity],
		})
	}
	return plugins, nil
}

// Compares the path's last-touched commit at HEAD and at sha, so unrelated commits elsewhere in the marketplace repo cannot read as outdated.
func (a *claudeCodePluginAdapter) pathOutdated(ctx context.Context, home, marketplace, path, sha string) *bool {
	if sha == "" {
		return nil
	}
	repoRoot := claudeMarketplaceRepoRoot(home, marketplace)
	latest := a.gitPluginPathCommit(ctx, repoRoot, "HEAD", path)
	if latest == "" {
		return nil
	}
	asOfInstall := a.gitPluginPathCommit(ctx, repoRoot, sha, path)
	if asOfInstall == "" {
		return nil
	}
	outdated := latest != asOfInstall
	return &outdated
}

func claudeMarketplaceRepoRoot(home, marketplace string) string {
	return filepath.Join(home, ".claude", "plugins", "marketplaces", marketplace)
}

// Best-effort: an undeterminable sha comes back as "" rather than an error.
func (a *claudeCodePluginAdapter) gitPluginPathCommit(ctx context.Context, repoRoot, rev, path string) string {
	stdout, _, err := a.exec(ctx, "git", "-C", repoRoot, "log", "-1", "--format=%H", rev, "--", path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(stdout)
}

// Mirrors ~/.claude/plugins/installed_plugins.json, keyed like splitPluginIdentity; gitCommitSha is optional per scope entry.
type claudeInstalledPluginsFile struct {
	Plugins map[string][]struct {
		GitCommitSha string `json:"gitCommitSha"`
	} `json:"plugins"`
}

// `claude plugins list` exposes no sha, so this file is the only source; a missing or malformed one yields an empty map, never an error.
func readClaudeInstalledPluginShas() map[string]string {
	shas := map[string]string{}
	path, err := claudeInstalledPluginsPath()
	if err != nil {
		return shas
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return shas
	}
	var file claudeInstalledPluginsFile
	if err := json.Unmarshal(data, &file); err != nil {
		return shas
	}
	for identity, scopes := range file.Plugins {
		for _, s := range scopes {
			if s.GitCommitSha != "" {
				shas[identity] = s.GitCommitSha
				break
			}
		}
	}
	return shas
}

func claudeInstalledPluginsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), nil
}

// Source is a type discriminator, not the re-addable value, which lives in Repo; the clone's mtime is the only last-update signal this JSON offers.
type claudeMarketplaceListEntry struct {
	Name            string `json:"name"`
	Source          string `json:"source"`
	Repo            string `json:"repo"`
	URL             string `json:"url"`
	InstallLocation string `json:"installLocation"`
}

func (e claudeMarketplaceListEntry) realSource() string {
	if e.Source == "github" && e.Repo != "" {
		return e.Repo
	}
	return e.URL
}

func (a *claudeCodePluginAdapter) ListMarketplaces(ctx context.Context) ([]InstalledMarketplace, error) {
	stdout, stderr, err := a.exec(ctx, "claude", "plugins", "marketplace", "list", "--json")
	if err != nil {
		return nil, fmt.Errorf("claude plugins marketplace list: %w: %s", err, stderr)
	}
	var entries []claudeMarketplaceListEntry
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		return nil, fmt.Errorf("claude plugins marketplace list: parse json: %w", err)
	}
	if entries == nil {
		return nil, fmt.Errorf("claude plugins marketplace list: parse json: expected array, got null")
	}
	out := make([]InstalledMarketplace, 0, len(entries))
	for _, e := range entries {
		out = append(out, InstalledMarketplace{Name: e.Name, Source: e.realSource(), UpdatedAt: dirModTime(e.InstallLocation)})
	}
	return out, nil
}

// `claude plugin marketplace update` with no name updates every marketplace.
func (a *claudeCodePluginAdapter) UpdateMarketplaces(ctx context.Context) error {
	_, stderr, err := a.exec(ctx, "claude", "plugin", "marketplace", "update")
	if err != nil {
		return fmt.Errorf("claude plugin marketplace update: %w: %s", err, stderr)
	}
	return nil
}

func splitPluginIdentity(id string) (name, marketplace string) {
	i := strings.LastIndex(id, "@")
	if i < 0 {
		return id, ""
	}
	return id[:i], id[i+1:]
}

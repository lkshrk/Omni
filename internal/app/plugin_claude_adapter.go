package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	osExec "os/exec"
	"path/filepath"
	"strings"

	"github.com/lkshrk/omni/internal/config"
)

type claudeCodePluginAdapter struct {
	exec      func(context.Context, string, ...string) (string, string, error)
	lookupEnv func(string) (string, bool)
}

// NewClaudeCodePluginAdapter returns a PluginAdapter that delegates to the claude CLI.
func NewClaudeCodePluginAdapter(
	execFn func(context.Context, string, ...string) (string, string, error),
	lookupEnv func(string) (string, bool),
) PluginAdapter {
	return &claudeCodePluginAdapter{exec: execFn, lookupEnv: lookupEnv}
}

func (a *claudeCodePluginAdapter) ID() string { return "claude-code" }

func (a *claudeCodePluginAdapter) Available() bool {
	_, err := osExec.LookPath("claude")
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

// RemovePlugin uninstalls by the full name@marketplace identity — a bare name
// is ambiguous when the same plugin name exists in more than one marketplace.
// --yes is required because the capture doc's help text says -y/--yes is
// mandatory when stdin/stdout is not a TTY, which is always true for omni.
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

// claudeUpdateFailureMarker is the substring `claude plugin update` prints on
// its own failure line (e.g. `✘ Failed to update plugin "x": Plugin "x" not
// found`). Verified live 2026-07-10: the command exits 0 on both success and
// this failure, so the exit code cannot distinguish them — output must be
// parsed instead. Matched without the ✘ glyph, which renders inconsistently
// across terminals.
const claudeUpdateFailureMarker = "Failed to update"

// Install, uninstall, and marketplace add share the same exit-0-on-failure
// contract (verified live 2026-07-12 on 2.1.197): only a "Failed to ..."
// output line distinguishes failure from success.
const (
	claudeInstallFailureMarker        = "Failed to install"
	claudeUninstallFailureMarker      = "Failed to uninstall"
	claudeMarketplaceAddFailureMarker = "Failed to add marketplace"
)

// UpdatePlugin updates a plugin by its full name@marketplace identity: a bare
// name is rejected by the live CLI as not found (verified 2026-07-10), even
// though the help text shows a single <plugin> positional.
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

// AddMarketplace declares a marketplace by source only — claude derives its
// own name for the marketplace (the GitHub owner segment, per the probe
// transcript), which may not match config.Marketplace.Name. omni never issues
// `marketplace remove`; a marketplace it didn't explicitly add is never torn
// down by omni.
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

// claudePluginListEntry mirrors one element of the bare array returned by
// `claude plugins list --json` (without --available), per the probe transcript
// in docs/superpowers/research/2026-07-02-plugin-cli-probe.md. It also matches
// the "installed" array element when --available succeeds (verified live
// 2026-07-10: that call wraps the same installed shape in {installed, available}).
type claudePluginListEntry struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Scope   string `json:"scope"`
	Enabled bool   `json:"enabled"`
}

// claudeAvailableSource mirrors the "source" field of an available entry,
// which is either an object ({source, url, path, ref, sha, ...}) or a bare
// string (e.g. "./plugins/agent-sdk-dev") depending on the marketplace entry
// shape — verified live 2026-07-10. Sha is empty for the bare-string form.
// Path is the plugin's own subdirectory within the marketplace clone: for
// the object form it's the "path" field; for the bare-string form the string
// itself is that path.
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

// claudeAvailableEntry mirrors one element of the "available" array returned
// by `claude plugins list --json --available`, verified live 2026-07-10. Real
// payloads carry no version-like field; Version and LatestVersion are read
// defensively in case a future CLI build adds one under either name — if
// neither is present, the joined LatestVersion stays empty rather than being
// fabricated from Source.Ref (a git ref, not a comparable version).
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
	Installed []claudePluginListEntry `json:"installed"`
	Available []claudeAvailableEntry  `json:"available"`
}

func (a *claudeCodePluginAdapter) ListPlugins(ctx context.Context) ([]InstalledPlugin, error) {
	installedShas := readClaudeInstalledPluginShas()
	home, homeErr := os.UserHomeDir()

	stdout, _, err := a.exec(ctx, "claude", "plugins", "list", "--json", "--available")
	if err == nil {
		var resp claudePluginListAvailableResponse
		if jsonErr := json.Unmarshal([]byte(stdout), &resp); jsonErr != nil {
			return nil, fmt.Errorf("claude plugins list --available: parse json: %w", jsonErr)
		}
		latestByIdentity := make(map[string]string, len(resp.Available))
		latestShaByIdentity := make(map[string]string, len(resp.Available))
		pathByIdentity := make(map[string]string, len(resp.Available))
		for _, e := range resp.Available {
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
		plugins := make([]InstalledPlugin, 0, len(resp.Installed))
		for _, e := range resp.Installed {
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

// pathOutdated determines whether path (the plugin's own subdirectory within
// the marketplace clone) has changed since sha (the commit the plugin was
// installed at) by comparing the path's last-touched commit at HEAD against
// the same query pinned to sha — equal means the plugin's own files haven't
// moved since install, regardless of unrelated commits elsewhere in the
// marketplace repo (the false-positive mode a raw repo-HEAD sha comparison
// hits, see plugin_rows.go's Outdated doc comment). Returns nil (unknown)
// whenever the clone is missing, git fails, or sha is unknown — never guesses.
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

// gitPluginPathCommit returns the sha of the most recent commit reachable
// from rev that touched path within repoRoot, or "" if it cannot be
// determined (missing clone, path never committed, git unavailable) —
// best-effort, mirroring readClaudeInstalledPluginShas' never-an-error
// contract.
func (a *claudeCodePluginAdapter) gitPluginPathCommit(ctx context.Context, repoRoot, rev, path string) string {
	stdout, _, err := a.exec(ctx, "git", "-C", repoRoot, "log", "-1", "--format=%H", rev, "--", path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(stdout)
}

// claudeInstalledPluginsFile mirrors ~/.claude/plugins/installed_plugins.json,
// keyed by "name@marketplace" (the same identity form as splitPluginIdentity).
// gitCommitSha is optional per scope entry.
type claudeInstalledPluginsFile struct {
	Plugins map[string][]struct {
		GitCommitSha string `json:"gitCommitSha"`
	} `json:"plugins"`
}

// readClaudeInstalledPluginShas reads the installed-side git commit sha for
// each plugin identity from installed_plugins.json. `claude plugins list`
// exposes no sha at all, so this file is the only source; sha enrichment is
// best-effort — a missing or malformed file yields an empty map, never an error.
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

// claudeMarketplaceListEntry mirrors one element of the JSON array returned
// by `claude plugins marketplace list --json`, per the probe transcript
// (only the "github" source shape was captured live: `{name, source: "github",
// repo, installLocation}`). Source is a type discriminator, not the real
// value — the real re-addable source string lives in Repo for that shape;
// URL is a defensive fallback for any other shape a future capture reveals.
// InstallLocation is the on-disk clone directory; its mtime is the only
// last-update signal available, since this JSON carries no date field
// (unlike the plugin list JSON's installedAt/lastUpdated).
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
	out := make([]InstalledMarketplace, 0, len(entries))
	for _, e := range entries {
		out = append(out, InstalledMarketplace{Name: e.Name, Source: e.realSource(), UpdatedAt: dirModTime(e.InstallLocation)})
	}
	return out, nil
}

// UpdateMarketplaces refreshes every configured marketplace from its source
// (`claude plugin marketplace update` with no name updates all, per --help).
func (a *claudeCodePluginAdapter) UpdateMarketplaces(ctx context.Context) error {
	_, stderr, err := a.exec(ctx, "claude", "plugin", "marketplace", "update")
	if err != nil {
		return fmt.Errorf("claude plugin marketplace update: %w: %s", err, stderr)
	}
	return nil
}

// splitPluginIdentity splits a claude plugin id of the form "name@marketplace"
// on the last '@', per the probe transcript (e.g. "useful-skills@lkshrk").
func splitPluginIdentity(id string) (name, marketplace string) {
	i := strings.LastIndex(id, "@")
	if i < 0 {
		return id, ""
	}
	return id[:i], id[i+1:]
}

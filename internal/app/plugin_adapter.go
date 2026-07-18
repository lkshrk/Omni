package app

import (
	"context"
	"os"
	"time"

	"github.com/lkshrk/omni/internal/config"
)

// PluginAdapter manages plugins and marketplaces in one target agent by
// delegating to that agent's own CLI. omni never edits agent config files
// directly, and never removes a marketplace it did not add (see AddMarketplace
// doc comment) — there is deliberately no RemoveMarketplace here.
type PluginAdapter interface {
	ID() string
	Available() bool
	ListPlugins(ctx context.Context) ([]InstalledPlugin, error)
	InstallPlugin(ctx context.Context, p config.Plugin) error
	RemovePlugin(ctx context.Context, p config.Plugin) error
	UpdatePlugin(ctx context.Context, name, marketplace string) error
	ListMarketplaces(ctx context.Context) ([]InstalledMarketplace, error)
	AddMarketplace(ctx context.Context, m config.Marketplace) error
	// UpdateMarketplaces refreshes every configured marketplace from its
	// source, so plugin installs/updates that follow see current metadata.
	// Adapters with no such CLI operation return a descriptive error rather
	// than silently no-op'ing (mirrors codexPluginAdapter.UpdatePlugin).
	UpdateMarketplaces(ctx context.Context) error
}

// dirModTime returns dir's modification time, or the zero time if dir is
// empty or unreadable — a best-effort fallback, never an error, since a
// marketplace's clone directory is not guaranteed to exist or be readable at
// scan time (see InstalledMarketplace.UpdatedAt's doc comment).
func dirModTime(dir string) time.Time {
	if dir == "" {
		return time.Time{}
	}
	info, err := os.Stat(dir)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// InstalledPlugin is one plugin as reported by an agent's list output.
// Version is informational only — omni does not pin or reconcile versions.
// LatestVersion feeds the Updates section; empty when the agent CLI does not
// expose a comparable version for the marketplace's available entry.
type InstalledPlugin struct {
	Name          string
	Marketplace   string
	Version       string
	LatestVersion string
	Sha           string
	LatestSha     string
	// PathOutdated is a precise outdated signal for plugins with no usable
	// Version/LatestVersion pair (the common case — see plugin_rows.go's
	// Outdated doc comment for why the marketplace-repo-HEAD sha can't be
	// compared directly). nil means the adapter could not determine it
	// (e.g. no source path, no installed commit sha, git unavailable);
	// non-nil is authoritative. Populated by comparing the plugin's own
	// source path's last-touched commit at HEAD against the same query run
	// at the installed commit — equal means nothing has changed since
	// install, regardless of unrelated commits elsewhere in the repo.
	PathOutdated *bool
}

// Update returns the display-ready update verdict for an unmanaged installed
// plugin, using the same projection as managed PluginRow.Update.
func (p InstalledPlugin) Update() PluginUpdate {
	return pluginUpdateDisplay(p.Version, p.LatestVersion, p.Sha, p.LatestSha, p.PathOutdated)
}

// InstalledMarketplace is one marketplace as reported by an agent's list
// output. Source is the real, re-addable source string when the agent CLI's
// list output exposes one; it is empty when it does not, and omni must never
// fabricate a replacement (see FindUndeclaredMarketplace's doc comment).
// UpdatedAt is the marketplace's last-update time; zero means unknown. Neither
// claude's nor codex's marketplace list JSON exposes a date field (unlike
// their plugin list JSON, which carries installedAt/lastUpdated), so
// UpdatedAt is derived from the mtime of the marketplace's on-disk clone
// directory (installLocation/root in each CLI's list output) by the adapter.
type InstalledMarketplace struct {
	Name      string
	Source    string
	UpdatedAt time.Time
}

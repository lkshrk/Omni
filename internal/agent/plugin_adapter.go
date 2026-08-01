package agent

import (
	"context"
	"os"
	"time"

	"github.com/lkshrk/omni/internal/config"
)

// PluginAdapter — Delegates to each agent's own CLI rather than editing its config files, and deliberately offers no RemoveMarketplace.
type PluginAdapter interface {
	ID() string
	Available() bool
	ListPlugins(ctx context.Context) ([]InstalledPlugin, error)
	InstallPlugin(ctx context.Context, p config.Plugin) error
	RemovePlugin(ctx context.Context, p config.Plugin) error
	UpdatePlugin(ctx context.Context, name, marketplace string) error
	ListMarketplaces(ctx context.Context) ([]InstalledMarketplace, error)
	AddMarketplace(ctx context.Context, m config.Marketplace) error
	// Adapters whose CLI has no such operation return a descriptive error rather than silently no-op'ing.
	UpdateMarketplaces(ctx context.Context) error
}

// PluginAdapterCapabilities distinguishes marketplace plugins from direct-source plugins.
// Adapters that do not implement it retain the original marketplace-only behavior.
type PluginAdapterCapabilities interface {
	SupportsMarketplaces() bool
	SupportsDirectSources() bool
}

// Best-effort: a marketplace clone need not exist or be readable at scan time, so an unreadable dir yields the zero time.
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

// InstalledPlugin — Version is informational only, since omni pins and reconciles nothing; LatestVersion is empty when the CLI exposes no comparable version.
type InstalledPlugin struct {
	Name          string
	Marketplace   string
	Version       string
	LatestVersion string
	Sha           string
	LatestSha     string
	// The precise signal when no usable Version/LatestVersion pair exists: nil means undeterminable, non-nil is authoritative.
	PathOutdated *bool
}

// InstalledMarketplace — Source is empty when the CLI exposes no re-addable string, and omni must never fabricate one; UpdatedAt comes from the clone directory's mtime because no CLI reports a date.
type InstalledMarketplace struct {
	Name      string
	Source    string
	UpdatedAt time.Time
}

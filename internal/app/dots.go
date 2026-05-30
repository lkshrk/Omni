package app

import (
	"fmt"
	"strings"

	"github.com/lkshrk/omni/internal/config"
)

// DotsAddOptions controls the behaviour of DotsAdd.
type DotsAddOptions struct {
	// Name overrides the name inferred from the path.
	Name string
	// Group is the config group name to write the entry to.
	// Defaults to the current host group when empty.
	Group string
	// Adopt moves the existing file or directory into the dots repo before linking.
	Adopt bool
	// Ignore is a list of per-entry ignore patterns.
	Ignore []string
}

// DotsDeleteOptions controls the behaviour of DotsDeleteWithOptions.
type DotsDeleteOptions struct {
	// KeepLocal preserves a real local copy before deleting the repo package.
	KeepLocal bool
}

type DotVariantInfo struct {
	Name    string `json:"name"`
	Host    string `json:"host,omitempty"`
	Package string `json:"package"`
	Default bool   `json:"default,omitempty"`
	Active  bool   `json:"active,omitempty"`
}

type DotsAddVariantOptions struct {
	// Host is the short hostname for the variant. Defaults to the current host.
	Host string
	// Package is the stow package directory for this variant. Defaults to
	// "<name>@<host>".
	Package string
	// Sync immediately syncs the entry when Host is the current host.
	Sync bool
}

type DotsRemoveVariantOptions struct {
	// Host is the short hostname for the variant. Defaults to the current host.
	Host string
}

// DotHealth summarises the symlink health of a dots entry.
type DotHealth string

const (
	HealthOK       DotHealth = "ok"        // all symlinks correct
	HealthMissing  DotHealth = "missing"   // some/all links not yet created
	HealthConflict DotHealth = "conflict"  // real files at one or more target paths
	HealthNoSource DotHealth = "no-source" // source does not exist in repo
)

// DotStatus describes the current state of a single dots entry.
type DotStatus struct {
	Name       string        `json:"name"`
	Package    string        `json:"package,omitempty"`
	Variant    bool          `json:"variant,omitempty"`
	SourcePath string        `json:"source_path"`
	TargetPath string        `json:"target_path"`
	ConfigPath string        `json:"path,omitempty"`
	Health     DotHealth     `json:"health"`
	State      DotState      `json:"state"`
	Actions    []DotAction   `json:"actions,omitempty"`
	Group      string        `json:"group,omitempty"` // config group name (for example "work" or the current host group)
	FileCount  int           `json:"file_count,omitempty"`
	Counts     DotFileCounts `json:"counts,omitempty"`
	IsDir      bool          `json:"is_dir,omitempty"`
	Children   []DotChild    `json:"children,omitempty"`

	ignoredChildren []DotChild
}

type DotChild struct {
	Name      string        `json:"name"`
	RelPath   string        `json:"rel_path"`
	Path      string        `json:"path"`
	State     DotState      `json:"state,omitempty"`
	IsDir     bool          `json:"is_dir"`
	Depth     int           `json:"depth,omitempty"`
	Ignored   bool          `json:"ignored,omitempty"`
	FileCount int           `json:"file_count,omitempty"`
	Counts    DotFileCounts `json:"counts,omitempty"`
	Children  []DotChild    `json:"children,omitempty"`
}

type DotFileCounts struct {
	Synced    int `json:"synced,omitempty"`
	OutOfSync int `json:"out_of_sync,omitempty"`
	Ignored   int `json:"ignored,omitempty"`
}

func (c DotFileCounts) Managed() int {
	return c.Synced + c.OutOfSync
}

func (c DotFileCounts) Total() int {
	return c.Managed() + c.Ignored
}

// DotsStatusResult bundles symlink health with the git status string.
type DotsStatusResult struct {
	Entries         []DotStatus         `json:"entries"`
	GitStatus       string              `json:"git_status"` // output of "git status --short" in the dots repo
	DotMemberships  map[string][]string `json:"-"`
	DiscoveredCount int                 `json:"discovered_count,omitempty"`
}

type DotsQueryOptions struct {
	Name  string
	State string
}

type DotsResolveStrategy string

const (
	DotResolveUseRepo  DotsResolveStrategy = "use-repo"
	DotResolveUseLocal DotsResolveStrategy = "use-local"
)

type DotsSyncAvailabilityReason string

const (
	DotsSyncAvailabilityReady    DotsSyncAvailabilityReason = "ready"
	DotsSyncAvailabilityNoRepo   DotsSyncAvailabilityReason = "no-repo"
	DotsSyncAvailabilityDisabled DotsSyncAvailabilityReason = "disabled"
	DotsSyncAvailabilityUnknown  DotsSyncAvailabilityReason = "unknown"
)

type DotsSyncAvailability struct {
	Configured bool
	Reason     DotsSyncAvailabilityReason
	RepoPath   string
}

const (
	dotsContentDirName  = "dotfiles"
	dotChildrenMaxDepth = 4
)

// ─── public API ───────────────────────────────────────────────────────────────

// DotsConfigured reports whether dots_repo is set in settings.json.
func (a *App) DotsConfigured() bool {
	return DotsConfiguredInSettings(config.Settings{DotsRepo: a.dotsRepoPath()})
}

// DotsConfiguredInSettings reports whether settings contains a dots repo path.
func DotsConfiguredInSettings(settings config.Settings) bool {
	return strings.TrimSpace(settings.DotsRepo) != ""
}

// DotsSyncAvailabilityInSettings reports dotfile sync availability from settings.
func DotsSyncAvailabilityInSettings(settings config.Settings) DotsSyncAvailability {
	repoPath := strings.TrimSpace(settings.DotsRepo)
	if !DotsConfiguredInSettings(settings) {
		return DotsSyncAvailability{Reason: DotsSyncAvailabilityNoRepo}
	}
	if config.BoolVal(settings.DotsDisabled) {
		return DotsSyncAvailability{Reason: DotsSyncAvailabilityDisabled, RepoPath: repoPath}
	}
	return DotsSyncAvailability{Configured: true, Reason: DotsSyncAvailabilityReady, RepoPath: repoPath}
}

func (a *App) DotsSyncAvailability() (DotsSyncAvailability, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return DotsSyncAvailability{Reason: DotsSyncAvailabilityUnknown}, err
	}
	return DotsSyncAvailabilityInSettings(a.effectiveSettings(cfg)), nil
}

func (a *App) DotsSyncConfigured() (bool, error) {
	availability, err := a.DotsSyncAvailability()
	if err != nil {
		return false, err
	}
	return availability.Configured, nil
}

func (a *App) requireDotsEnabled(rootCfg *config.RootConfig) error {
	if config.BoolVal(a.effectiveSettings(rootCfg).DotsDisabled) {
		return fmt.Errorf("dots are disabled for this host")
	}
	return nil
}

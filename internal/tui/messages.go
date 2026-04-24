package tui

import (
	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

// toolsLoadedMsg is sent when the initial tool list and settings have been fetched.
type toolsLoadedMsg struct {
	tools                  []*database.ToolCache
	discovered             []*database.ToolCache // locally installed but not in config
	settings               config.Settings
	taps                   []string
	groupNames             []string          // ordered non-base group names
	toolGroups             map[string]string // "name\x00provider" → baseName
	toolMemberships        map[string][]string
	dotMemberships         map[string][]string
	ignoreLabels           map[string]string // logical tool name → compact ignore source label
	toolIgnoreSet          map[string]bool
	groupIgnoreSet         map[string]map[string]bool
	toolProviderPins       map[string]string
	profileInfo            *app.ProfileInfo
	ignoreList             []string // tool names ignored by the active profile
	noConfig               bool     // true when no settings.json was found
	noProfile              bool     // true when settings.json exists but no profile is mapped to this machine
	err                    error
	effectivePythonManager string             // binary actually used (uv, pip3, pip) — empty if not found
	effectiveNodeManager   string             // binary actually used (bun, pnpm, npm) — empty if not found
	effectiveSystemManager string             // concrete PM backing the system ecosystem provider (e.g. "brew", "apt") — empty if not resolved
	stowInstalled          bool               // true when GNU Stow is reachable on PATH
	allPythonManagers      []string           // all python managers found on PATH (for setup wizard)
	allNodeManagers        []string           // all node managers found on PATH (for setup wizard)
	setupProviders         []setupProviderRow // pre-built provider rows for the setup wizard
	ecosystemProviders     []string           // ordered ecosystem provider names for the tools-list provider filter
	configuredProviders    []string           // unique provider names declared in config groups (may differ from DB rows on first run)
}

// providerScannedMsg is sent when the per-provider parallel scan goroutine
// completes (both install-status and outdated-status passes). Tools are NOT
// fetched here to avoid concurrent ListTools calls racing each other and
// producing stale snapshots. The handler launches a single allProvidersDoneMsg
// fetch once the set empties.
type providerScannedMsg struct {
	gen      int
	provider string
	err      error
}

// allProvidersDoneMsg is sent after all per-provider goroutines have finished
// and a single, consistent ListTools call has captured the final DB state.
type allProvidersDoneMsg struct {
	gen                    int
	tools                  []*database.ToolCache
	effectiveSystemManager string
	err                    error
}

// discoveredRefreshedMsg is sent when the background RefreshDiscovered pass
// (orphan scan) completes.
type discoveredRefreshedMsg struct {
	gen        int
	discovered []*database.ToolCache
	err        error
}

// createGroupDoneMsg is sent after a new named group has been created.
type createGroupDoneMsg struct {
	err        error
	name       string
	groupNames []string
}

type profileActivatedMsg struct {
	err     error
	profile string
	info    *app.ProfileInfo
}

// opCompleteMsg is sent after an async operation (install/uninstall/upgrade) finishes.
// key is the upgradingKeys entry to remove ("name\x00provider"); empty for non-upgrade ops.
type opCompleteMsg struct {
	key              string
	message          string
	err              error
	tools            []*database.ToolCache // refreshed list after op
	toolProviderPins map[string]string
}

// settingsSavedMsg is sent after an async settings save completes.
type settingsSavedMsg struct {
	gen int
	err error
}

type progressUpdate struct {
	gen       int
	text      string
	rowKey    string
	rowStatus string
	rowErr    string
	rowDone   bool
}

// progressMsg carries one progress update from a background operation.
type progressMsg progressUpdate

// progressStreamClosedMsg signals that the progress update channel closed. It
// is not an operation result; the corresponding operation returns
// progressDoneMsg separately.
type progressStreamClosedMsg struct {
	gen int
}

// progressDoneMsg signals that a progress-emitting operation has finished.
// key is the upgradingKeys entry to remove ("*" for upgrade-all); empty for sync.
type progressDoneMsg struct {
	gen             int
	key             string
	message         string
	err             error
	tools           []*database.ToolCache
	claimedNames    []string
	toolGroups      map[string]string
	toolMemberships map[string][]string
	groupNames      []string
	rowErrors       map[string]string
}

// descRefreshDoneMsg is sent when the background bulk-description refresh
// finishes. tools is the refreshed list (nil on error or empty config).
type descRefreshDoneMsg struct {
	gen   int
	tools []*database.ToolCache
}

// setupImportDoneMsg is sent after the setup-wizard import step completes.
type setupImportDoneMsg struct {
	added      int
	err        error
	tools      []*database.ToolCache
	toolGroups map[string]string
	groupNames []string
}

// setupProvidersDoneMsg is sent after the setup-wizard provider selection step saves.
type setupProvidersDoneMsg struct{ err error }

// setupNodeMgrDoneMsg is sent after the setup-wizard node manager step saves.
type setupNodeMgrDoneMsg struct{ err error }

// setupProfileDoneMsg is sent after the setup-wizard profile step completes.
type setupProfileDoneMsg struct {
	profileName string
	err         error
}

type stowInstallDoneMsg struct {
	action stowInstallAction
	err    error
}

// clearStatusMsg is sent by a timer to erase the transient status message.
// gen must match Model.statusGen — stale timers from overwritten messages are ignored.
type clearStatusMsg struct{ gen int }

// dotsLoadedMsg is sent when the dots status (entries + git status) is fetched.
type dotsLoadedMsg struct {
	gen            int
	entries        []app.DotStatus
	gitStatus      string
	dotMemberships map[string][]string
	detail         string
	err            error
}

// dotsSyncedMsg is sent after a dots sync completes.
type dotsSyncedMsg struct {
	gen            int
	entries        []app.DotStatus
	gitStatus      string
	dotMemberships map[string][]string
	err            error
}

// dotsDiscoveredMsg is sent after non-mutating dotfile candidate discovery.
type dotsDiscoveredMsg struct {
	gen             int
	entries         []app.DotStatus
	gitStatus       string
	dotMemberships  map[string][]string
	discoveredCount int
	err             error
}

// dotsPulledMsg is sent after a dots pull+resync completes.
type dotsPulledMsg struct {
	gen            int
	entries        []app.DotStatus
	gitStatus      string
	dotMemberships map[string][]string
	err            error
}

// dotsPushedMsg is sent after a dots push completes.
type dotsPushedMsg struct {
	gen            int
	entries        []app.DotStatus
	gitStatus      string
	dotMemberships map[string][]string
	err            error
}

// dotsDeletedMsg is sent after a dots entry delete completes.
type dotsDeletedMsg struct {
	gen            int
	name           string
	entries        []app.DotStatus
	gitStatus      string
	dotMemberships map[string][]string
	err            error
}

// dotsFixedMsg is sent after a conflict entry has been resolved by backing up
// the original file and creating the symlink.
type dotsFixedMsg struct {
	gen            int
	name           string
	entries        []app.DotStatus
	gitStatus      string
	dotMemberships map[string][]string
	err            error
}

// dotsAddedMsg is sent after a path has been adopted into the dots repo.
type dotsAddedMsg struct {
	gen            int
	path           string // the tilde-form path that was added
	entries        []app.DotStatus
	gitStatus      string
	dotMemberships map[string][]string
	err            error
}

// searchResultsMsg is sent when a provider search completes.
type searchResultsMsg struct {
	query          string                // the query that produced these results (for caching)
	providerFilter string                // provider tab filter active when the search started
	gen            int                   // generation counter — stale results (gen mismatch) are dropped
	tools          []*database.ToolCache // results converted to ToolCache (not installed)
	err            error
}

// debouncedSearchMsg is sent after the search debounce delay to trigger a provider search.
type debouncedSearchMsg struct {
	query string
	gen   int
}

// dangerOpDoneMsg is sent after a high-impact maintenance action completes.
// action is a short label (e.g. "delete-profile", "reset-settings").
type dangerOpDoneMsg struct {
	action  string
	dotsGen int
	err     error
	detail  string                // optional human-readable summary
	tools   []*database.ToolCache // refreshed list (nil when not applicable)
	reload  bool                  // true when the tools list should be reloaded
	mode    viewMode              // optional top-level view to show after reload
}

// groupChangedMsg is sent after a group rename or delete completes.
type groupChangedMsg struct {
	err             error
	detail          string
	tools           []*database.ToolCache // refreshed tool list (non-nil on delete)
	groupNames      []string              // refreshed non-base group names
	toolGroups      map[string]string     // refreshed "name\x00provider" → baseName
	toolMemberships map[string][]string
	info            *app.ProfileInfo // refreshed profile info
}

// groupToolsChangedMsg is sent after a profile group tools popup save completes.
type groupToolsChangedMsg struct {
	err             error
	detail          string
	tools           []*database.ToolCache
	groupNames      []string
	toolGroups      map[string]string
	toolMemberships map[string][]string
	ignoreLabels    map[string]string
	toolIgnoreSet   map[string]bool
	groupIgnoreSet  map[string]map[string]bool
}

// groupDotsChangedMsg is sent after a profile group dots popup save completes.
type groupDotsChangedMsg struct {
	err            error
	detail         string
	entries        []app.DotStatus
	gitStatus      string
	dotMemberships map[string][]string
}

// profileGroupChangedMsg is sent after a profile group add/remove or profile delete.
type profileGroupChangedMsg struct {
	err     error
	profile string
	group   string           // empty for profile delete
	added   bool             // true=added, false=removed/deleted
	detail  string           // optional status text for rename/bulk edits
	info    *app.ProfileInfo // refreshed profile info on success
}

// claimDoneMsg is sent after an orphan tool has been added to the config.
type claimDoneMsg struct {
	err       error
	name      string
	groupName string
	tools     []*database.ToolCache
}

// ignoreDoneMsg is sent after a tool's ignore status has been toggled.
type ignoreDoneMsg struct {
	err            error
	name           string
	ignored        bool // true = was added to ignore list, false = was removed
	tools          []*database.ToolCache
	profileScope   bool
	ignoreLabels   map[string]string
	toolIgnoreSet  map[string]bool
	groupIgnoreSet map[string]map[string]bool
}

// migrateProviderDoneMsg is sent after a wrong-provider tool has been migrated
// (installed via the correct provider and removed from the old one).
type migrateProviderDoneMsg struct {
	err          error
	name         string
	fromProvider string
	toProvider   string
	tools        []*database.ToolCache
}

// profileCreatedMsg is sent after a new profile has been created from the Profiles tab.
type profileCreatedMsg struct {
	err     error
	profile string
	info    *app.ProfileInfo
}

// dotsIgnoredMsg is sent after DotsAddIgnorePattern completes.
type dotsIgnoredMsg struct {
	gen            int
	name           string
	pattern        string
	ignored        bool
	entries        []app.DotStatus
	gitStatus      string
	dotMemberships map[string][]string
	err            error
}

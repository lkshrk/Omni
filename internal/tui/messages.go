package tui

import (
	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

// toolsLoadedMsg is sent when the initial tool list and settings have been fetched.
type toolsLoadedMsg struct {
	tools                  []*database.ToolCache
	discovered             []*database.ToolCache // locally installed but not in config
	settings               config.Settings
	taps                   []string
	groupNames             []string          // ordered reusable group names
	toolGroups             map[string]string // "name\x00provider" → group name
	toolMemberships        map[string][]string
	dotMemberships         map[string][]string
	ignoreLabels           map[string]string // logical tool name → compact ignore source label
	toolIgnoreSet          map[string]bool
	groupIgnoreSet         map[string]map[string]bool
	toolProviderPins       map[string]string
	toolFallbacks          map[string]config.FallbackSpec
	toolGit                map[string]string
	hostInfo               *app.HostInfo
	ignoreList             []string // tool names ignored by the active host
	dotsHistory            []app.DotsHistoryEntry
	dotsHistoryErr         string
	dotsState              *app.DotsState
	noConfig               bool // true when no settings.json was found
	noHost                 bool // true when settings.json exists but no host entry matches this machine
	err                    error
	effectivePythonManager string // binary actually used (uv, pip3, pip) — empty if not found
	effectiveNodeManager   string // binary actually used (bun, pnpm, npm) — empty if not found
	effectiveSystemManager string // concrete PM backing the system ecosystem provider (e.g. "brew", "apt") — empty if not resolved
	stowInstalled          bool   // true when GNU Stow is reachable on PATH
	dotsReminderService    *app.DotsReminderService
	dotsReminderServiceErr string
	dotsWatchService       *app.DotsWatchService
	dotsWatchServiceErr    string
	dotsConfigured         bool
	dotsConfiguredKnown    bool
	dotsSyncAvail          app.DotsSyncAvailability
	dotsSyncAvailKnown     bool
	setupProviders         []app.SetupProviderOption
	ecosystemProviders     []string
}

type setupConfigImportDoneMsg struct {
	path string
	err  error
}

// providerScannedMsg is sent when the per-provider installed-state scan
// goroutine completes. Tools are NOT fetched here to avoid concurrent ListTools
// calls racing each other and producing stale snapshots. The handler launches a
// single allProvidersDoneMsg fetch once the set empties.
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

type providerOutdatedCheckedMsg struct {
	gen      int
	provider string
	err      error
}

type outdatedProvidersDoneMsg struct {
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
	err             error
	name            string
	groupNames      []string
	toolGroups      map[string]string
	toolMemberships map[string][]string
	hostInfo        *app.HostInfo
}

type hostCopiedMsg struct {
	err  error
	host string
	info *app.HostInfo
}

type setupHostCopyDoneMsg struct {
	err    error
	source string
	target string
	info   *app.HostInfo
}

type setupHostGroupsDoneMsg struct {
	err    error
	groups []string
	info   *app.HostInfo
}

// opCompleteMsg is sent after an async operation (install/uninstall/upgrade) finishes.
// key is the upgradingKeys entry to remove ("name\x00provider"); empty for non-upgrade ops.
type opCompleteMsg struct {
	key                    string
	message                string
	err                    error
	tools                  []*database.ToolCache // refreshed list after op
	removeDiscoveredKeys   []string              // exact "name\x00provider" orphan rows consumed by the op
	toolProviderPins       map[string]string
	toolGroups             map[string]string
	toolMemberships        map[string][]string
	groupNames             []string
	hostInfo               *app.HostInfo
	preserveOtherRowErrors bool
}

// settingsSavedMsg is sent after an async settings save completes.
type settingsSavedMsg struct {
	gen         int
	settings    config.Settings
	hasSettings bool
	err         error
}

type doctorDoneMsg struct {
	result *app.DoctorResult
	err    error
}

type fixIgnoreDoneMsg struct {
	modified []string
	err      error
}

type dotsServiceKind string

const (
	dotsReminderServiceKind dotsServiceKind = "reminder"
	dotsWatchServiceKind    dotsServiceKind = "watch"
)

type dotsServiceChangedMsg struct {
	kind     dotsServiceKind
	enabled  bool
	reminder *app.DotsReminderService
	watch    *app.DotsWatchService
	err      error
}

type dotsServicesStatusMsg struct {
	reminder    *app.DotsReminderService
	reminderErr string
	watch       *app.DotsWatchService
	watchErr    string
}

type dotsHistoryLoadedMsg struct {
	entries []app.DotsHistoryEntry
	err     error
}

type progressUpdate struct {
	gen                  int
	text                 string
	rowKey               string
	rowStatus            string
	rowErr               string
	rowDone              bool
	refreshProvider      string
	refreshProviderLabel string
	refreshToolName      string
	tools                []*database.ToolCache
	claimedNames         []string
	toolGroups           map[string]string
	toolMemberships      map[string][]string
	groupNames           []string
}

// progressMsg carries one progress update from a background operation.
type progressMsg progressUpdate

// progressStreamClosedMsg signals that the progress update channel closed. It
// is not an operation result; the corresponding operation returns
// progressDoneMsg separately.
type progressStreamClosedMsg struct {
	gen int
}

type dotsProgressUpdate struct {
	gen            int
	text           string
	name           string
	done           bool
	entries        []app.DotStatus
	gitStatus      string
	dotMemberships map[string][]string
}

// dotsProgressMsg carries one dots sync progress update.
type dotsProgressMsg dotsProgressUpdate

// dotsProgressStreamClosedMsg signals that the dots progress channel closed.
type dotsProgressStreamClosedMsg struct {
	gen int
}

// progressDoneMsg signals that a progress-emitting operation has finished.
// key is the upgradingKeys entry to remove ("*" for upgrade-all); empty for sync.
type progressDoneMsg struct {
	gen                     int
	key                     string
	message                 string
	err                     error
	tools                   []*database.ToolCache
	claimedNames            []string
	toolGroups              map[string]string
	toolMemberships         map[string][]string
	groupNames              []string
	rowErrors               map[string]string
	rowActionErrors         map[string]*provider.ActionError
	promptPrivilegedActions map[string]provider.PrivilegeAction
}

// descRefreshDoneMsg is sent when the background bulk-description refresh
// finishes. tools/discovered are refreshed snapshots (nil on error or empty config).
type descRefreshDoneMsg struct {
	gen        int
	err        error
	tools      []*database.ToolCache
	discovered []*database.ToolCache
}

// setupImportDoneMsg is sent after the setup-wizard import step completes.
type setupImportDoneMsg struct {
	added    int
	err      error
	hostInfo *app.HostInfo
}

// setupProvidersDoneMsg is sent after the setup-wizard provider selection step saves.
type setupProvidersDoneMsg struct{ err error }

// setupNodeMgrDoneMsg is sent after the setup-wizard node manager step saves.
type setupNodeMgrDoneMsg struct{ err error }

// setupBootstrapDoneMsg is sent after the existing-host bootstrap activation
// applies one optional action.
type setupBootstrapDoneMsg struct {
	action  string
	message string
	err     error
}

// setupHostDoneMsg is sent after the setup-wizard host step completes.
type setupHostDoneMsg struct {
	hostName string
	info     *app.HostInfo
	err      error
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

type dotsPeekLoadedMsg struct {
	gen    int
	result app.DotsPeekResult
	err    error
}

// dotsPreparedMsg is sent when the launch-time, non-mutating dots snapshot is fetched.
type dotsPreparedMsg struct {
	gen            int
	opGen          int
	entries        []app.DotStatus
	gitStatus      string
	dotMemberships map[string][]string
	err            error
}

// dotsSyncedMsg is sent after a dots sync completes.
type dotsSyncedMsg struct {
	gen            int
	entries        []app.DotStatus
	gitStatus      string
	dotMemberships map[string][]string
	settings       config.Settings
	hasSettings    bool
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

// dotsCommittedMsg is sent after a dots commit completes.
type dotsCommittedMsg struct {
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

// dotsVariantChangedMsg is sent after this host's dots package variant changes.
type dotsVariantChangedMsg struct {
	gen            int
	name           string
	info           app.DotVariantInfo
	removed        bool
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
// action is a short label (e.g. "delete-host", "reset-settings").
type dangerOpDoneMsg struct {
	action        string
	dotsGen       int
	err           error
	detail        string                // optional human-readable summary
	tools         []*database.ToolCache // refreshed list (nil when not applicable)
	reload        bool                  // true when the tools list should be reloaded
	mode          viewMode              // optional top-level view to show after reload
	setupComplete bool                  // true when an onboarding action should leave setup
}

// groupChangedMsg is sent after a group rename or delete completes.
type groupChangedMsg struct {
	err             error
	detail          string
	tools           []*database.ToolCache // refreshed tool list (non-nil on delete)
	groupNames      []string              // refreshed reusable group names
	toolGroups      map[string]string     // refreshed "name\x00provider" → group name
	toolMemberships map[string][]string
	info            *app.HostInfo // refreshed host info
}

// groupToolsChangedMsg is sent after a group tools popup save completes.
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

// groupDotsChangedMsg is sent after a group dots popup save completes.
type groupDotsChangedMsg struct {
	err            error
	detail         string
	entries        []app.DotStatus
	gitStatus      string
	dotMemberships map[string][]string
}

// hostGroupChangedMsg is sent after a host group add/remove or host delete.
type hostGroupChangedMsg struct {
	err             error
	host            string
	group           string // empty for host delete
	added           bool   // true=added, false=removed/deleted
	detail          string // optional status text for rename/bulk edits
	info            *app.HostInfo
	toolGroups      map[string]string
	toolMemberships map[string][]string
	groupNames      []string
}

// claimDoneMsg is sent after an orphan tool has been added to the config.
type claimDoneMsg struct {
	err             error
	name            string
	groupName       string
	tools           []*database.ToolCache
	toolGroups      map[string]string
	toolMemberships map[string][]string
	groupNames      []string
	hostInfo        *app.HostInfo
}

// ignoreDoneMsg is sent after a tool's ignore status has been toggled.
type ignoreDoneMsg struct {
	err            error
	name           string
	ignored        bool // true = was added to ignore list, false = was removed
	tools          []*database.ToolCache
	hostScope      bool
	ignoreLabels   map[string]string
	toolIgnoreSet  map[string]bool
	groupIgnoreSet map[string]map[string]bool
}

// migrateProviderDoneMsg is sent after a wrong-provider tool has been migrated
// (installed via the correct provider and removed from the old one).
type migrateProviderDoneMsg struct {
	err                     error
	name                    string
	fromProvider            string
	toProvider              string
	tools                   []*database.ToolCache
	toolProviderPins        map[string]string
	clearedProviderOverride bool
}

type fallbackSavedMsg struct {
	err           error
	name          string
	repo          string
	toolFallbacks map[string]config.FallbackSpec
}

// dotsIgnoredMsg is sent after a dots ignore/include operation completes.
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

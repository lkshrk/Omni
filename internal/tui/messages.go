package tui

import (
	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/dots"
)

type toolsLoadedMsg struct {
	tools                  []*app.ToolView
	discovered             []*app.ToolView // locally installed but not in config
	settings               app.Settings
	taps                   []string
	groupNames             []string          // ordered reusable group names
	toolGroups             map[string]string // "name\x00provider" → group name
	toolMemberships        map[string][]string
	hostInventoryTools     map[string]bool
	dotMemberships         map[string][]string
	ignoreLabels           map[string]string // logical tool name → compact ignore source label
	toolIgnoreSet          map[string]bool
	groupIgnoreSet         map[string]map[string]bool
	toolProviderPins       map[string]string
	toolProviderCandidates map[string][]app.ToolInstallSpec
	toolFallbacks          map[string]app.FallbackSpec
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
	nvmManaged             map[string]bool
	stowInstalled          bool // true when GNU Stow is reachable on PATH
	dotsReminderService    *app.DotsReminderService
	dotsReminderServiceErr string
	dotsWatchService       *app.DotsWatchService
	dotsWatchServiceErr    string
	dotsConfigured         bool
	dotsConfiguredKnown    bool
	agentsEnabled          bool
	skillsEnabled          bool
	mcpEnabled             bool
	pluginsEnabled         bool
	agentsRows             *app.CachedAgentsRows
	dotsSyncAvail          app.DotsSyncAvailability
	dotsSyncAvailKnown     bool
	setupProviders         []app.SetupProviderOption
	ecosystemProviders     []string
	enabledAgents          []string
	agentsIgnore           app.AgentsIgnore
}

type agentsSummaryLoadedMsg struct {
	summary app.DashboardAgentsSummary
	err     error
}

type nvmManagedLoadedMsg struct {
	nvmManaged map[string]bool
	err        error
}

type skillsManifestLoadedMsg struct {
	rows      []app.SkillPackageRow
	unmanaged []app.SkillPackageRow
	err       error
}

type skillsGroupsUpdatedMsg struct {
	rows []app.SkillPackageRow
	err  error
}

type skillsUpdatedMsg struct {
	updated bool
	err     error
}

// Carries one error per feature: the sequence runs skills, mcp, and plugins as independently-tracked sub-steps.
type agentsProgressDoneMsg struct {
	gen            int
	skills         bool
	mcp            bool
	plugin         bool
	marketplace    bool
	skillsErr      error
	mcpErr         error
	pluginErr      error
	marketplaceErr error
	// The per-feature errors above only carry failures; a run that skipped a drifted entry and installed everything else has nothing to say without this.
	report *app.AgentsSyncAllResult
}

type agentsToggledMsg struct {
	enabled bool
	err     error
}

type skillsFeatureToggledMsg struct {
	enabled bool
	err     error
}

type mcpFeatureToggledMsg struct {
	enabled bool
	err     error
}

type pluginsFeatureToggledMsg struct {
	enabled bool
	err     error
}

type agentsUseSavedMsg struct {
	ids []string
	err error
}

type skillsRestoredMsg struct {
	res app.RestoreSkillsResult
	err error
}

type skillsImportedMsg struct {
	diff app.ImportDiff
	err  error
}

type skillsFoundMsg struct {
	results []app.FindResult
	err     error
}

type skillAddedMsg struct {
	err     error
	warning string
}

type skillAgentsSavedMsg struct {
	rows []app.SkillPackageRow
	err  error
}

type setupConfigImportDoneMsg struct {
	path string
	err  error
}

// Tools are NOT fetched here: concurrent ListTools calls race and produce stale snapshots. The handler launches one allProvidersDoneMsg fetch once the set empties.
type providerScannedMsg struct {
	gen      int
	provider string
	err      error
}

type allProvidersDoneMsg struct {
	gen                    int
	tools                  []*app.ToolView
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
	tools                  []*app.ToolView
	effectiveSystemManager string
	err                    error
}

type discoveredRefreshedMsg struct {
	gen        int
	discovered []*app.ToolView
	err        error
}

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

// key is the upgradingKeys entry to remove ("name\x00provider"); empty for non-upgrade ops.
type opCompleteMsg struct {
	key                    string
	message                string
	loadingGen             int // gate generation at dispatch; 0 when unstamped
	err                    error
	tools                  []*app.ToolView // refreshed list after op
	removeDiscoveredKeys   []string        // exact "name\x00provider" orphan rows consumed by the op
	toolProviderPins       map[string]string
	toolGroups             map[string]string
	toolMemberships        map[string][]string
	groupNames             []string
	hostInfo               *app.HostInfo
	preserveOtherRowErrors bool
}

type settingsSavedMsg struct {
	gen         int
	settings    app.Settings
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

type configOptimizeDoneMsg struct {
	report      *app.OptimizeReport
	modified    []string
	optimizeErr error
	ignoreErr   error
}

type fixNvmDoneMsg struct {
	result     *app.NvmManagedMigrationBatchResult
	tools      []*app.ToolView
	nvmManaged map[string]bool
	err        error
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
	tools                []*app.ToolView
	claimedNames         []string
	toolGroups           map[string]string
	toolMemberships      map[string][]string
	groupNames           []string
}

type progressMsg progressUpdate

// Not an operation result; the operation returns progressDoneMsg separately.
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

type dotsProgressMsg dotsProgressUpdate

type dotsProgressStreamClosedMsg struct {
	gen int
}

// key is the upgradingKeys entry to remove ("*" for upgrade-all); empty for sync.
type progressDoneMsg struct {
	gen                     int
	key                     string
	message                 string
	err                     error
	tools                   []*app.ToolView
	claimedNames            []string
	toolGroups              map[string]string
	toolMemberships         map[string][]string
	groupNames              []string
	rowErrors               map[string]string
	rowActionErrors         map[string]*app.ActionError
	promptPrivilegedActions map[string]app.PrivilegeAction
}

// tools/discovered are refreshed snapshots, nil on error or empty config.
type descRefreshDoneMsg struct {
	gen        int
	err        error
	tools      []*app.ToolView
	discovered []*app.ToolView
}

type setupImportDoneMsg struct {
	added    int
	err      error
	hostInfo *app.HostInfo
}

type setupProvidersDoneMsg struct{ err error }

type setupBootstrapDoneMsg struct {
	action  string
	message string
	err     error
}

type setupHostDoneMsg struct {
	hostName string
	info     *app.HostInfo
	err      error
}

type setupAgentsDiffMsg struct {
	unmanagedSkills  int
	unmanagedMcp     int
	unmanagedPlugins int
	err              error
}

type setupAgentsImportDoneMsg struct {
	skills  int
	mcp     int
	plugins int
	// Servers omni declined to claim, and servers claimed while carrying a literal header value: an
	// adopted header is copied verbatim into settings.json, so silence here hides a written secret.
	advisories []string
	err        error
}

type stowInstallDoneMsg struct {
	action stowInstallAction
	err    error
}

// gen must match Model.statusGen — stale timers from overwritten messages are ignored.
type clearStatusMsg struct{ gen int }

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

type dotsChildrenLoadedMsg struct {
	gen        int
	entryName  string
	entryState dots.State
	relPath    string
	children   []app.DotChild
	err        error
}

type traceLogLoadedMsg struct {
	gen    int
	traces []app.CommandTraceView
	err    error
}

type dotsPreparedMsg struct {
	gen            int
	opGen          int
	entries        []app.DotStatus
	gitStatus      string
	dotMemberships map[string][]string
	err            error
}

type dotsSyncedMsg struct {
	gen            int
	entries        []app.DotStatus
	gitStatus      string
	dotMemberships map[string][]string
	settings       app.Settings
	hasSettings    bool
	err            error
}

type dotsDiscoveredMsg struct {
	gen             int
	entries         []app.DotStatus
	gitStatus       string
	dotMemberships  map[string][]string
	discoveredCount int
	err             error
}

type dotsPulledMsg struct {
	gen            int
	entries        []app.DotStatus
	gitStatus      string
	dotMemberships map[string][]string
	err            error
}

type dotsPushedMsg struct {
	gen            int
	entries        []app.DotStatus
	gitStatus      string
	dotMemberships map[string][]string
	err            error
}

type dotsCommittedMsg struct {
	gen            int
	entries        []app.DotStatus
	gitStatus      string
	dotMemberships map[string][]string
	err            error
}

type dotsDeletedMsg struct {
	gen            int
	name           string
	entries        []app.DotStatus
	gitStatus      string
	dotMemberships map[string][]string
	err            error
}

type dotsFixedMsg struct {
	gen            int
	name           string
	entries        []app.DotStatus
	gitStatus      string
	dotMemberships map[string][]string
	err            error
}

type dotsAddedMsg struct {
	gen            int
	path           string // the tilde-form path that was added
	entries        []app.DotStatus
	gitStatus      string
	dotMemberships map[string][]string
	err            error
}

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

type searchResultsMsg struct {
	query          string          // the query that produced these results (for caching)
	providerFilter string          // provider tab filter active when the search started
	gen            int             // generation counter — stale results (gen mismatch) are dropped
	tools          []*app.ToolView // results converted to ToolCache (not installed)
	err            error
}

type debouncedSearchMsg struct {
	query string
	gen   int
}

// action is a short label (e.g. "delete-host", "reset-settings").
type dangerOpDoneMsg struct {
	action        string
	dotsGen       int
	err           error
	detail        string          // optional human-readable summary
	tools         []*app.ToolView // refreshed list (nil when not applicable)
	reload        bool            // true when the tools list should be reloaded
	mode          viewMode        // optional top-level view to show after reload
	setupComplete bool            // true when an onboarding action should leave setup
}

type groupChangedMsg struct {
	err             error
	detail          string
	tools           []*app.ToolView   // refreshed tool list (non-nil on delete)
	groupNames      []string          // refreshed reusable group names
	toolGroups      map[string]string // refreshed "name\x00provider" → group name
	toolMemberships map[string][]string
	info            *app.HostInfo // refreshed host info
}

type groupToolsChangedMsg struct {
	err             error
	detail          string
	tools           []*app.ToolView
	groupNames      []string
	toolGroups      map[string]string
	toolMemberships map[string][]string
	ignoreLabels    map[string]string
	toolIgnoreSet   map[string]bool
	groupIgnoreSet  map[string]map[string]bool
}

type groupDotsChangedMsg struct {
	err            error
	detail         string
	entries        []app.DotStatus
	gitStatus      string
	dotMemberships map[string][]string
}

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

type claimDoneMsg struct {
	err             error
	name            string
	groupName       string
	tools           []*app.ToolView
	toolGroups      map[string]string
	toolMemberships map[string][]string
	groupNames      []string
	hostInfo        *app.HostInfo
}

type ignoreDoneMsg struct {
	err            error
	name           string
	ignored        bool // true = was added to ignore list, false = was removed
	tools          []*app.ToolView
	hostScope      bool
	ignoreLabels   map[string]string
	toolIgnoreSet  map[string]bool
	groupIgnoreSet map[string]map[string]bool
}

type migrateProviderDoneMsg struct {
	err                     error
	name                    string
	fromProvider            string
	toProvider              string
	tools                   []*app.ToolView
	toolProviderPins        map[string]string
	clearedProviderOverride bool
	removedFromConfig       bool
	nvmManaged              map[string]bool
}

type fallbackSavedMsg struct {
	err           error
	name          string
	repo          string
	toolFallbacks map[string]app.FallbackSpec
}

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

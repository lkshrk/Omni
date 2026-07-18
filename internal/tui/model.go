// Package tui contains the Bubbletea TUI.
package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/profile"
)

// viewMode is the active top-level view.
type viewMode int

const (
	viewStatus          viewMode = iota // read-only health/status dashboard tab (zero value = default)
	viewList                            // tools list
	viewSearch                          // search input active
	viewSettings                        // options/settings tab
	viewSetup                           // first-run: no settings.json found
	viewCommand                         // command palette active
	viewGroups                          // dedicated group-assignment tab
	viewGroupPicker                     // inline move-to-group picker
	viewGroupMembership                 // logical tool group membership toggles
	viewGroupTools                      // group tool membership/ignore editor
	viewGroupDots                       // group dotfile membership editor
	viewIgnoreScope                     // explicit ignore scope picker
	viewProviderScope                   // explicit provider pin scope picker
	viewFallbackEditor                  // GitHub fallback source editor
	viewAdminTerminal                   // privileged package action terminal handoff
	viewDots                            // dotfiles management tab
	viewSkills                          // agent skills management tab
)

// section groups tools into visual categories in the list view.
type section int

const (
	sectionUpdates     section = iota // 0 - tools with updates available
	sectionQuarantined                // 1 - tools with deferred updates
	sectionOutOfSync                  // 2 - config-missing / orphan / wrong-provider
	sectionInstalled                  // 3 - installed, up-to-date
	sectionAvailable                  // 4 - available to install (declared in config or found via search)
	sectionIgnored                    // 5 - in the active host's ignore list (rendered last, dimmed)
)

// syncStatus is the out-of-sync sub-category for a tool in sectionOutOfSync.
type syncStatus int

const (
	syncOK         syncStatus = iota // in sync (not in sectionOutOfSync)
	syncMissing                      // ↓ in config, not installed locally
	syncOrphan                       // + installed locally, not in config
	syncWrongProv                    // ⚠ installed with wrong concrete provider
	syncNvmManaged                   // ⚠ system-provider but active binary is nvm-managed
)

const searchCacheTTL = 5 * time.Minute

// searchCacheEntry holds cached provider search results.
type searchCacheEntry struct {
	tools []*app.ToolView
	at    time.Time
}

func searchCacheKey(query, providerFilter string) string {
	return query + "\x00" + providerFilter
}

type stowInstallAction int

const (
	stowInstallNone stowInstallAction = iota
	stowInstallLaunchSync
	stowInstallEnableDots
	stowInstallSaveSettingsSync
	stowInstallSetupDotsRepo
	stowInstallDotVariant
	stowInstallDotsWatch
)

type dotsVariantMode int

const (
	dotsVariantNone dotsVariantMode = iota
	dotsVariantCreate
	dotsVariantRemove
)

type dotsVariantRequest struct {
	name       string
	remove     bool
	discovered bool
	// parentName and subpath are set only for the child-row path: the subpath is
	// first extracted out of parentName into its own entry (named name), then a
	// host variant is created for it.
	parentName string
	subpath    string
}

type dotsPeekState struct {
	result app.DotsPeekResult
	scroll int
}

type traceLogState struct {
	traces []app.CommandTraceView
	scroll int
	err    error
}

type groupToolSection int

const (
	groupToolSectionEnabled groupToolSection = iota
	groupToolSectionDisabled
	groupToolSectionIgnored
)

type groupToolRow struct {
	tool        *app.ToolView
	section     groupToolSection
	enabled     bool
	groupIgnore bool
	toolIgnore  bool
}

type groupDotSection int

const (
	groupDotSectionEnabled groupDotSection = iota
	groupDotSectionDisabled
	groupDotSectionIgnored
)

type groupDotRow struct {
	name    string
	target  string
	section groupDotSection
	enabled bool
	ignored bool
}

type groupAssignmentEditor struct {
	group              string
	cursor             int
	search             string
	searchActive       bool
	membership         map[string]bool
	originalMembership map[string]bool
}

type scopeOption struct {
	kind           string
	label          string
	detail         string
	group          string
	checked        bool
	initialChecked bool
}

type dashboardReconcilePlanKind = app.DashboardReconcileStepID

const (
	dashboardReconcilePlanSyncTools     dashboardReconcilePlanKind = app.ReconcileStepSyncTools
	dashboardReconcilePlanUpgradeTools  dashboardReconcilePlanKind = app.ReconcileStepUpgradeTools
	dashboardReconcilePlanSyncDots      dashboardReconcilePlanKind = app.ReconcileStepSyncDots
	dashboardReconcilePlanCommitDots    dashboardReconcilePlanKind = app.ReconcileStepCommitDots
	dashboardReconcilePlanFixIgnore     dashboardReconcilePlanKind = app.ReconcileStepFixIgnore
	dashboardReconcilePlanFixNvmManaged dashboardReconcilePlanKind = app.ReconcileStepFixNvmManaged
	dashboardReconcilePlanSyncAgents    dashboardReconcilePlanKind = app.ReconcileStepSyncAgents
)

// Model is the root Bubbletea model.
type Model struct {
	app     *app.App
	ctx     context.Context
	cancel  context.CancelFunc
	keys    KeyMap
	spinner spinner.Model
	help    help.Model

	mode   viewMode
	filter textinput.Model

	settings             app.Settings
	settingsCursor       int // which settings row is selected
	settingsDetailScroll int
	taps                 []string

	allTools        []*app.ToolView // unfiltered
	discoveredTools []*app.ToolView // locally installed but not in config
	discoveredKeys  map[string]bool // "name\x00provider" set for fast orphan lookup
	visibleTools    []*app.ToolView // after filter + sort
	searchTools     []*app.ToolView // results from last provider search (not in allTools)
	// sectionCounts caches the per-section tool count computed once in applyFilter.
	// Keyed by section constant (sectionUpdates, sectionInstalled, etc.).
	// Avoids iterating visibleTools on every View() call (e.g. during active typing).
	sectionCounts       map[section]int
	searching           bool                        // true while a provider search is in flight
	searchGen           int                         // incremented on each new search; stale results are dropped
	searchCancel        context.CancelFunc          // cancels the in-flight HTTP search; nil when idle
	searchCache         map[string]searchCacheEntry // query → cached results
	descRefreshGen      int                         // generation for background description refreshes
	cursor              int
	loading             bool
	confirmQuit         bool            // true after first quit key; second press exits
	quitConfirmKey      string          // key used to arm confirmQuit ("q" or "ctrl+c")
	confirmGen          int             // increments whenever a timed confirmation is armed
	upgradingKeys       map[string]bool // set of in-flight upgrade keys ("name\x00provider" or "*")
	bulkPendingKeys     map[string]bool // tool keys waiting for their turn in a bulk operation
	rowOpKey            string          // selected row operation key ("name\x00provider") for install/delete/reinstall work
	rowOpStatus         string          // inline status shown where row action hints normally render
	activeActionCancel  context.CancelFunc
	rowErrors           map[string]string // tool key -> last failed row action message; survives filtering/search
	rowActionErrors     map[string]*app.ActionError
	listConfirm         listConfirmation
	suppressFooterHints bool
	statusMsg           string
	statusIsErr         bool // true when statusMsg is an error (shown red, 2× duration)
	statusGen           int  // incremented on each setStatus call; stale clearStatusMsg events are dropped
	err                 error
	launchBatchActive   bool
	launchBatchErrors   []string
	launchBatchStatus   int

	settingsSaveRunning       bool
	settingsSaveQueued        bool
	settingsSaveQueuedChanges []app.SettingsChange
	settingsSaveQueuedGen     int
	settingsSaveGen           int
	settingsSaveInFlightGen   int
	adminTerminal             *adminTerminalState
	adminTerminalQueue        []adminTerminalState
	adminTerminalGen          int

	scanningProviders      map[string]bool
	outdatedProviders      map[string]bool
	providerScanToolCounts map[string]int
	providerScanToolDone   map[string]int
	providerScanLabels     map[string]string
	refreshToolDone        int
	refreshToolTotal       int
	scanGen                int
	// scanProgressCh is the progress stream owned by the in-flight provider
	// scan fan-out. The per-provider scan commands share it, so no producer
	// can close it; the settle branch of handleProviderScannedMsg closes this
	// field instead of m.progressCh, which may meanwhile belong to a newer
	// stream started by an unrelated operation (e.g. agents update all).
	scanProgressCh             chan progressUpdate
	discoveryGen               int
	providerSnapshotRefreshing bool
	outdatedSnapshotRefreshing bool
	discoveryRefreshing        bool
	descRefreshing             bool
	// migrating is true while a doMigrateProvider command is in flight.
	// Prevents progressDoneMsg (from a concurrent launch scan) from prematurely
	// clearing m.loading, and prevents installedRefreshedMsg/updatesRefreshedMsg
	// from wiping the "Migrating…" status before migration completes.
	migrating bool

	// progress streaming from background operations
	progressCh   chan progressUpdate
	progressGen  int
	progressText string

	// command palette
	commandInput         textinput.Model
	commandSuggestions   []palCmd
	commandCursor        int
	dotsConfiguredCached bool
	dotsSyncAvailCached  app.DotsSyncAvailability
	consolidateOptions   []app.EcosystemMigration // cached at load time; registry lookup, no IO

	// group subtabs — used by group picker (move tool to group), not for list filtering
	groupNames              []string          // ordered reusable group names
	toolGroups              map[string]string // "name\x00provider" → group name
	toolMemberships         map[string][]string
	hostInventoryTools      map[string]bool
	ignoreLabels            map[string]string // logical tool name → compact ignore source label
	toolIgnoreSet           map[string]bool
	groupIgnoreSet          map[string]map[string]bool
	toolProviderPins        map[string]string
	toolProviderCandidates  map[string][]app.ToolInstallSpec
	providerCandidateCursor int
	toolFallbacks           map[string]app.FallbackSpec
	toolGit                 map[string]string
	nvmManaged              map[string]bool

	// provider filter — [All] [system] [node] [python] …
	providerNames  []string // ordered provider-family names from the app/provider registry
	providerTabIdx int      // 0=All, 1+=providerNames[idx-1]

	// effective package-manager binaries resolved at load time via PATH probing.
	effectivePythonManager string // e.g. "uv", "pip3"
	effectiveNodeManager   string // e.g. "bun", "pnpm", "npm"
	effectiveSystemManager string // e.g. "brew", "apt" — concrete PM backing the system provider family

	// group-assignment tab
	hostInfo    *app.HostInfo
	hostCursor  int
	ignoreSet   map[string]bool // tool names ignored by the active host
	groupFilter string          // non-empty: only show tools belonging to this group
	groupTabIdx int             // 0=all, 1=current host, 2+=reusable groups; mirrors groupFilter

	// host group-assignment editing (inline picker in Groups tab)
	hostEditMode       int      // 0=none, 1=editGroups
	hostGroupPicker    []string // groups shown in the host assignment picker
	hostGroupIdx       int      // cursor in the host assignment picker
	hostGroupDraft     []string // staged group memberships for selected host
	hostOriginalGroups []string // group memberships before staged edit
	hostEditName       string   // host captured when group editor was opened
	hostCopyConfirm    bool     // true when awaiting second Space to copy groups to current host
	hostCopyName       string   // host captured when copy confirmation was armed
	hostDeleteConfirm  bool     // true when awaiting second Enter to confirm delete
	hostDeleteName     string   // host captured when delete confirmation was armed
	hostRenameMode     bool     // true when the inline host rename text input is open
	hostRenameName     string   // host captured when inline rename was opened

	// group-assignment tab section focus
	// 0 = hosts list, 1 = groups list
	assignmentSection  int
	groupCursor        int  // cursor within the allGroupNames list
	groupDeleteConfirm bool // true when awaiting second Enter to confirm group delete
	groupDeleteName    string
	groupDeleteChoice  int  // 0=move last-membership tools to this host, 1=delete last-membership specs
	groupRenameMode    bool // true when inline rename text input is open
	groupRenameName    string
	groupCreating      bool // true when the shared new-group popup is open

	// group tools editor popup
	groupToolsEditor         groupAssignmentEditor
	groupToolsProviderIdx    int
	groupToolsIgnore         map[string]bool // logical tool name -> staged group-level ignore in groupToolsEditor.group
	groupToolsOriginalIgnore map[string]bool // logical tool name -> original group-level ignore in groupToolsEditor.group

	// group dotfiles editor popup
	groupDotsEditor groupAssignmentEditor

	// inline group-picker
	pickerGroups           []string
	pickerCursor           int
	pickerCreatingGroup    bool // true when the user selected the "+ new group…" sentinel
	pickerPurposeClaim     bool // true when the picker is for claiming an orphan tool
	pickerPurposeInstall   bool // true when the picker is for install-and-add
	pickerPurposeDotAdd    bool // true when the picker is for dots add
	pickerDotAddPath       string
	pickerDotAddRawPath    string
	pickerPurposeReassign  bool     // true when iterating claimed tools for group reassignment
	pendingGroupReassign   []string // queue of tool names awaiting group reassignment
	reassignCreatedGroups  []string // groups created during the reassign sequence (carried across pickers)
	pickerMembershipKind   string
	pickerMembershipName   string
	pickerMembershipKey    string
	mcpMemberships         map[string][]string // mcp server name → current group memberships (picker draft)
	pluginMemberships      map[string][]string // plugin name → current group memberships (picker draft)
	marketplaceMemberships map[string][]string // marketplace name → current group memberships (picker draft)
	// pickerDotExtract* are set when the group popup was opened on a child
	// sub-path row: confirming extracts the subtree into a new entry assigned to
	// the picked groups instead of editing an existing entry's membership.
	pickerDotExtractParent string
	pickerDotExtractSub    string
	pickerActionTool       app.ToolView
	pickerActionToolSet    bool
	// pickerClaimAgents payload for claiming an agents-tab orphan row: which
	// feature (pickerMembershipSkill/Mcp/Plugin, carried in
	// pickerMembershipKind while pickerPurposeClaim is set for this flow) and
	// identity to adopt on confirm, then assign to the chosen group.
	pickerClaimAgentsRow agentsAllRow
	pickerClaimAgentsSet bool
	pickerOriginalGroups []string
	pickerCreatedGroups  []string
	scopeOptions         []scopeOption
	scopeCursor          int
	scopeTarget          app.ToolView
	scopeTargetSet       bool
	fallbackTarget       app.ToolView
	fallbackTargetSet    bool
	fallbackEditor       fallbackEditorState

	// setup wizard step (0 = create config?, 1 = import tools?, 2 = provider
	// selection, 3 = node manager, 4 = agents onboarding, 5 = enable dotfiles?,
	// 6 = dots repo path, 7 = copy host?, 8 = host picker, 9 = reusable groups,
	// 10 = existing-host activation)
	setupStep int
	// setupAgentsList is the detected-agents snapshot rendered by step 4.
	setupAgentsList []app.AgentInfo
	// setupAgentsDiffLoading/Loaded track the async unmanaged-count fetch for
	// step 4; Loaded gates rendering the diff line and the [i]/[s] footer.
	setupAgentsDiffLoading      bool
	setupAgentsDiffLoaded       bool
	setupAgentsUnmanagedSkills  int
	setupAgentsUnmanagedMcp     int
	setupAgentsUnmanagedPlugins int
	// setupBackgroundMode is the main tab rendered behind setup/onboarding
	// popups. Zero value keeps first-run setup on the tools tab.
	setupBackgroundMode viewMode
	setupProviders      []app.SetupProviderOption
	setupProviderIdx    int
	// setupCopyHostIdx is the cursor for the host-copy picker in step 8.
	setupCopyHostIdx int
	// setupGroupIdx/draft are the final reusable-group selection in step 9.
	setupGroupIdx   int
	setupGroupDraft map[string]bool
	// setupActivationIdx is the cursor for existing-host bootstrap activation.
	setupActivationIdx int
	// setupComplete is set after onboarding has completed so a follow-up reload
	// cannot reopen setup from a stale no-host snapshot.
	setupComplete bool
	// setupReloading keeps a centered loading overlay visible after onboarding has
	// switched back to the main UI but before the first post-setup reload
	// finishes.
	setupReloading bool
	// skipLaunchDotsSyncOnce prevents a post-bootstrap reload from repeating a
	// dot sync that the bootstrap flow just completed. The launch snapshot still
	// hydrates dots status asynchronously.
	skipLaunchDotsSyncOnce bool
	// setupHostReturnStep is the setup step to restore if automatic host
	// creation fails after the UI has advanced to the dotfile decision step.
	setupHostReturnStep int

	// hostRequired is true when the config exists but no host entry matches
	// this machine. All navigation is locked until a host is active.
	hostRequired bool

	// agents-use editor (active when editing the Agents row in settings)
	editingAgents    bool
	agentsEditRows   []app.AgentPickerRow
	agentsEditCursor int

	// provider priority editor (active when editing the Priority row in settings)
	refreshScanErrors              []string
	editingPriority                bool
	priorityHolding                bool
	priorityCursor                 int
	priorityDraft                  []string
	priorityDisabled               map[string]bool
	priorityAvailable              map[string]bool
	editingServiceDuration         bool
	serviceDurationRow             int
	serviceDurationIdx             int
	doctorResult                   *app.DoctorResult
	doctorErr                      string
	doctorRunning                  bool
	doctorRefreshPending           bool // set when a fix needs a doctor rerun but doctor is in flight or not ready
	doctorPreserveStatus           bool // keep the triggering operation's result visible through its doctor refresh
	cursorHidden                   bool // true until user navigates after tab switch
	statusCursor                   int
	dashboardReconcilePlanOpen     bool
	dashboardReconcilePlanCursor   int
	dashboardReconcilePlanSelected map[dashboardReconcilePlanKind]bool
	dashboardReconcileRunning      bool
	dashboardReconcileCurrent      dashboardReconcilePlanKind
	dashboardReconcileQueue        []dashboardReconcilePlanKind
	dashboardReconcileErrors       []string

	// file picker popup (reusable for any path selection)
	dotsFilePicker       pathPickerModel
	showFilePicker       bool
	filePickerTitle      string
	filePickerAllowFiles bool
	filePickerForConfig  bool
	settingsInput        textinput.Model // used by settings/group text inputs

	// dots tab
	dotsEntries            []app.DotStatus
	dotsGitStatus          string
	dotsHistory            []app.DotsHistoryEntry
	dotsHistoryErr         string
	dotMemberships         map[string][]string
	dotsCursor             int
	dotsExpandedName       string
	dotsExpandedState      app.DotState
	dotsExpandedChildren   map[string]bool
	dotsLoading            bool
	dotsLoaded             bool // true after first lazy load
	dotsPreparing          bool // true while the non-mutating launch snapshot is in flight
	dotsPrepareGen         int  // increments for each launch snapshot; stale results are dropped
	dotsOpGen              int  // increments for each async dots operation; stale results are dropped
	dotsCtx                context.Context
	dotsCancel             context.CancelFunc
	dotsProgressCh         chan dotsProgressUpdate
	dotsPendingNames       map[string]bool
	dotsActiveName         string
	dotsConfirmIdx         int    // index of entry pending delete confirm; -1 = none
	dotsOverwriteIdx       int    // index of conflict entry pending use-repo confirm; -1 = none
	dotsLocalIdx           int    // index of conflict entry pending use-local confirm; -1 = none
	dotsForceResolve       string // "use_repo"/"use_local" when a force-resolve-all is armed; "" = none
	dotsIgnoreIdx          int    // index of child path pending ignore/include confirm; -1 = none
	dotsVariantIdx         int    // index of entry pending host variant choice/removal; -1 = none
	dotsVariantMode        dotsVariantMode
	dotsPeek               *dotsPeekState
	dotsPeekLoading        bool
	dotsPeekGen            int
	traceLog               *traceLogState
	traceLogLoading        bool
	traceLogGen            int
	dotsSearchActive       bool // true when dots search bar is open
	filePickerForDotAdd    bool // true when file picker opened for "add path" on dots tab
	stowInstalled          bool
	dotsReminderService    *app.DotsReminderService
	dotsReminderServiceErr string
	dotsReminderInterval   time.Duration
	dotsWatchService       *app.DotsWatchService
	dotsWatchServiceErr    string
	dotsWatchDebounce      time.Duration
	dotsWatchDebounceNext  time.Duration
	dotsServicesRefreshing bool
	stowInstallPrompt      bool
	stowInstallAction      stowInstallAction
	stowInstallDotsRepo    string
	stowInstallPath        string
	stowInstallVariant     dotsVariantRequest

	// agents (skills) tab
	agentsEnabled  bool
	skillsEnabled  bool
	mcpEnabled     bool
	pluginsEnabled bool
	agentsSummary  app.DashboardAgentsSummary
	// *RowsKnown flags mark sections whose rows reflect reality — either
	// seeded from the persisted cache or landed from a live load. Empty rows
	// with the flag unset mean "not loaded yet" and must render as loading,
	// not as the onboarding empty state.
	skillsRowsKnown      bool
	mcpRowsKnown         bool
	pluginRowsKnown      bool
	marketplaceRowsKnown bool
	enabledAgents        []string
	agentsIgnore         app.AgentsIgnore
	skillsRows           []app.SkillPackageRow
	skillsUnmanagedRows  []app.SkillPackageRow
	skillsCursor         int
	agentsAllCursor      int
	skillsMemberships    map[string][]string // source → current group memberships (picker draft)

	// agentsOpKey identifies the agents-all row (see agentsRowRunKey) with an
	// op in flight, mirroring rowOpKey's tools-tab row-spinner substitution.
	agentsOpKey string

	// agents-all row delete confirm (two-step, tools-tab style, spans all three features)
	agentsDeleteConfirm   bool
	agentsDeleteUninstall bool // skills orphan rows uninstall from disk, not manifest delete
	agentsDeleteFeature   agentsSection
	agentsDeleteName      string
	agentsDeleteOpKey     string

	// agents-all row ignore/unignore confirm (two-step, mirrors the delete confirm above)
	agentsIgnoreConfirm bool
	agentsIgnoreFeature agentsSection
	agentsIgnoreName    string
	agentsIgnoreOpKey   string

	// plugin claim's offer to also claim its undeclared marketplace: armed by
	// pluginNeedsMarketplaceMsg, confirmed with Confirm/y, cancelled with
	// Back/any other key (see handlePluginMarketplaceOfferConfirmKeyMsg). Not
	// a same-key press-again pattern like the delete/ignore confirms above,
	// since this is a genuine yes/no decision surfaced after an async
	// round-trip rather than a second press of the triggering key.
	pluginMarketplaceOfferConfirm bool
	pluginMarketplaceOfferAgentID string
	pluginMarketplaceOfferPlugin  app.InstalledPlugin
	pluginMarketplaceOfferGroup   string
	pluginMarketplaceOfferMarket  string
	pluginMarketplaceOfferSource  string
	pluginMarketplaceOfferOpKey   string
	skillsLoaded                  bool
	skillsRunning                 bool
	skillsResult                  *app.RestoreSkillsResult
	skillsImport                  *app.ImportDiff
	skillsErr                     error
	skillTypeIdx                  int // 0=all, 1=skills, 2=mcp, 3=plugin
	skillAgentIdx                 int // 0=all, 1+=installed agent IDs derived from row.Agents

	// per-package agents picker popup
	skillAgentsPicker bool
	skillAgentsSource string
	skillAgentsRows   []app.SkillAgentRow
	skillAgentsCursor int

	// mcp tab
	mcpRows          []app.McpServerRow
	mcpUnmanaged     map[string][]app.InstalledMcpServer
	mcpCursor        int
	mcpErr           error
	mcpRunning       bool
	mcpLoaded        bool
	mcpDeleteConfirm bool
	mcpDeleteName    string

	// mcpCursorAgentID disambiguates which per-agent rendered row is selected
	// when mcpCursor's item (localIdx) expands into multiple agent rows —
	// see agentsChipRowPosition/agentsChipMoveRow in agents_all.go.
	mcpCursorAgentID string

	// mcp per-server agents picker popup (reuses skillAgentsRows/skillAgentsCursor)
	mcpAgentsPicker bool
	mcpAgentsRow    app.McpServerRow

	// plugin tab
	pluginRows          []app.PluginRow
	pluginUnmanaged     map[string][]app.InstalledPlugin
	pluginCursor        int
	pluginErr           error
	pluginRunning       bool
	pluginLoaded        bool
	pluginDeleteConfirm bool
	pluginDeleteName    string

	// pluginCursorAgentID mirrors mcpCursorAgentID for the plugin chip.
	pluginCursorAgentID string

	// plugin per-item agents picker popup (reuses skillAgentsRows/skillAgentsCursor)
	pluginAgentsPicker bool
	pluginAgentsRow    app.PluginRow

	// plugin add-form popup
	pluginFormOpen        bool
	pluginFormField       int // 0=name,1=marketplace,2=agents
	pluginFormName        textinput.Model
	pluginFormMarketplace textinput.Model
	pluginFormAgents      textinput.Model
	pluginFormErr         error

	// marketplace tab
	marketplaceRows          []app.MarketplaceRow
	marketplaceUnmanaged     map[string][]app.InstalledMarketplace
	marketplaceCursor        int
	marketplaceErr           error
	marketplaceRunning       bool
	marketplaceLoaded        bool
	marketplaceDeleteConfirm bool
	marketplaceDeleteName    string

	// marketplaceCursorAgentID mirrors pluginCursorAgentID for the marketplace chip.
	marketplaceCursorAgentID string

	// mcp add-form popup
	mcpFormOpen      bool
	mcpFormField     int // 0=name,1=transport,2=command/url,3=env,4=env_literal
	mcpFormTransport int // 0=stdio,1=http,2=sse; cycles with ←/→
	mcpFormName      textinput.Model
	mcpFormCommand   textinput.Model
	mcpFormURL       textinput.Model
	mcpFormEnv       textinput.Model
	mcpFormEnvLit    textinput.Model
	mcpFormErr       error

	// skill search/find flow
	skillsSearchActive bool
	skillFindResults   []app.FindResult
	skillFindCursor    int
	skillAddRunning    bool

	// danger zone (settings tab)
	dangerConfirmRow int // settings row awaiting inline confirmation; -1 = none

	width  int
	height int

	// palette holds all colours and pre-built lipgloss styles for the active
	// terminal theme. Initialised to the dark default in New(); rebuilt on
	// tea.BackgroundColorMsg via applyTheme. Each Model owns its own palette
	// so parallel test instances do not share mutable state.
	palette palette

	// isDark is true when the terminal reports a dark background colour.
	// Detected once at startup via tea.RequestBackgroundColor; defaults to
	// true (our palette is dark-optimised) until the terminal responds.
	isDark bool

	// focused tracks whether the terminal window has focus.
	// When false the spinner tick chain is suspended to avoid burning CPU
	// in the background. Defaults to true until a BlurMsg is received.
	focused bool
}

type listConfirmation struct {
	action   string
	name     string
	provider string
	// pinnedProvider carries an explicit provider pin for clear-override; provider
	// stays the declared tool provider so row keys and prompt matching keep working.
	pinnedProvider string
	installed      bool
	installedWith  string
}

// New creates the initial Model.
func New(ctx context.Context, a *app.App) Model {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	fi := textinput.New()
	fi.Placeholder = "search…"
	fi.CharLimit = 64

	ci := textinput.New()
	ci.Placeholder = "type a command…"
	ci.CharLimit = 64

	si := textinput.New()
	si.Placeholder = "~/dotfiles"
	si.CharLimit = 256

	mfn := textinput.New()
	mfn.Placeholder = "server-name"
	mfn.CharLimit = 64

	mfc := textinput.New()
	mfc.Placeholder = "npx -y @server/mcp"
	mfc.CharLimit = 256

	mfu := textinput.New()
	mfu.Placeholder = "https://mcp.example.com"
	mfu.CharLimit = 256

	mfe := textinput.New()
	mfe.Placeholder = "API_KEY,TOKEN"
	mfe.CharLimit = 256

	mfl := textinput.New()
	mfl.Placeholder = "LOG_LEVEL=info"
	mfl.CharLimit = 256

	pfn := textinput.New()
	pfn.Placeholder = "caveman"
	pfn.CharLimit = 64

	pfm := textinput.New()
	pfm.Placeholder = "caveman"
	pfm.CharLimit = 256

	pfa := textinput.New()
	pfa.Placeholder = "claude-code,codex"
	pfa.CharLimit = 256

	return Model{
		app:                   a,
		ctx:                   ctx,
		cancel:                cancel,
		agentsEnabled:         true, // enabled by default; the startup snapshot corrects it
		skillsEnabled:         true,
		mcpEnabled:            true,
		pluginsEnabled:        true,
		keys:                  DefaultKeyMap(),
		spinner:               sp,
		help:                  newHelp(),
		filter:                fi,
		commandInput:          ci,
		settingsInput:         si,
		mcpFormName:           mfn,
		mcpFormCommand:        mfc,
		mcpFormURL:            mfu,
		mcpFormEnv:            mfe,
		mcpFormEnvLit:         mfl,
		pluginFormName:        pfn,
		pluginFormMarketplace: pfm,
		pluginFormAgents:      pfa,
		mode:                  viewStatus,
		commandCursor:         -1,
		loading:               true,
		cursor:                -1, // nothing selected until the user navigates
		cursorHidden:          true,
		upgradingKeys:         make(map[string]bool),
		searchCache:           make(map[string]searchCacheEntry),
		dotsConfirmIdx:        -1,
		dotsOverwriteIdx:      -1,
		dotsLocalIdx:          -1,
		dotsIgnoreIdx:         -1,
		dotsVariantIdx:        -1,
		dangerConfirmRow:      -1,
		isDark:                true,             // assume dark until terminal replies
		focused:               true,             // assume focused until a BlurMsg says otherwise
		palette:               defaultPalette(), // dark default; rebuilt on BackgroundColorMsg
		doctorRunning:         true,             // Init fires doctor; prevents double-run on status tab
	}
}

func (m *Model) shutdown() {
	m.cancelSearch()
	m.cancelDotsOperation()
	if m.activeActionCancel != nil {
		m.activeActionCancel()
		m.activeActionCancel = nil
	}
	if m.cancel != nil {
		m.cancel()
	}
	m.loading = false
	m.dotsLoading = false
	m.dotsPreparing = false
	m.clearDotsProgressState()
	m.searching = false
	m.scanningProviders = nil
	m.providerScanToolCounts = nil
	m.providerScanToolDone = nil
	m.providerScanLabels = nil
	m.refreshToolDone = 0
	m.refreshToolTotal = 0
	m.upgradingKeys = make(map[string]bool)
}

// Init kicks off the initial data load.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		loadTools(m.app, m.ctx),
		m.doRunDoctor(),
		tea.RequestBackgroundColor,
	)
}

// loadTools fetches tools, settings, taps, groups, and hosts from the DB
// cache without probing providers. Returns immediately so the list renders
// on the first frame. A background doRefreshInstalled cmd updates install
// status afterwards.
func loadTools(a *app.App, ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		defer profile.Start("tui.load_tools.total")()

		stop := profile.Start("tui.load_tools.has_config")
		if !a.HasConfig() {
			stop()
			return toolsLoadedMsg{noConfig: true}
		}
		stop()
		stop = profile.Start("tui.load_tools.startup_snapshot")
		snapshot, err := a.StartupSnapshot(ctx)
		stop()
		if err != nil {
			return toolsLoadedMsg{err: fmt.Errorf("loading startup state: %w", err)}
		}
		stop = profile.Start("tui.load_tools.build_message")
		msg := toolsLoadedMsgFromStartupState(snapshot)
		stop()
		return msg
	}
}

func (m *Model) doLoadNvmManaged() tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		nvmManaged, err := a.NvmManagedSystemToolNames(ctx)
		return nvmManagedLoadedMsg{nvmManaged: nvmManaged, err: err}
	}
}

func toolsLoadedMsgFromStartupState(snapshot *app.StartupSnapshot) toolsLoadedMsg {
	hostInfo := snapshot.HostInfo
	ignoreLabels := snapshot.IgnoreLabels
	ignoreList := make([]string, 0, len(ignoreLabels))
	for name := range ignoreLabels {
		ignoreList = append(ignoreList, name)
	}
	return toolsLoadedMsg{
		tools:                  snapshot.Tools,
		discovered:             snapshot.Discovered,
		settings:               snapshot.Settings,
		taps:                   snapshot.Taps,
		groupNames:             snapshot.GroupNames,
		toolGroups:             snapshot.ToolGroups,
		toolMemberships:        snapshot.ToolMemberships,
		hostInventoryTools:     snapshot.HostInventoryTools,
		dotMemberships:         snapshot.DotMemberships,
		ignoreLabels:           ignoreLabels,
		toolIgnoreSet:          snapshot.ToolIgnores,
		groupIgnoreSet:         snapshot.GroupIgnores,
		toolProviderPins:       snapshot.ToolProviderPins,
		toolProviderCandidates: snapshot.ToolProviderCandidates,
		toolFallbacks:          snapshot.ToolFallbacks,
		toolGit:                snapshot.ToolGit,
		hostInfo:               hostInfo,
		ignoreList:             ignoreList,
		dotsHistory:            snapshot.DotsHistory,
		dotsHistoryErr:         errorString(snapshot.DotsHistoryErr),
		dotsState:              snapshot.DotsState,
		noHost:                 hostInfo != nil && hostInfo.Active == "",
		effectivePythonManager: snapshot.EffectivePythonManager,
		effectiveNodeManager:   snapshot.EffectiveNodeManager,
		effectiveSystemManager: snapshot.EffectiveSystemManager,
		nvmManaged:             snapshot.NvmManaged,
		stowInstalled:          snapshot.StowInstalled,
		dotsReminderService:    snapshot.DotsReminderService,
		dotsReminderServiceErr: errorString(snapshot.DotsReminderServiceErr),
		dotsWatchService:       snapshot.DotsWatchService,
		dotsWatchServiceErr:    errorString(snapshot.DotsWatchServiceErr),
		dotsConfigured:         snapshot.DotsConfigured,
		dotsConfiguredKnown:    true,
		agentsEnabled:          snapshot.AgentsEnabled,
		skillsEnabled:          snapshot.SkillsEnabled,
		mcpEnabled:             snapshot.McpEnabled,
		pluginsEnabled:         snapshot.PluginsEnabled,
		agentsRows:             snapshot.AgentsRows,
		dotsSyncAvail:          snapshot.DotsSyncAvailability,
		dotsSyncAvailKnown:     true,
		setupProviders:         snapshot.SetupProviders,
		ecosystemProviders:     snapshot.EcosystemProviderNames,
		enabledAgents:          snapshot.EnabledAgents,
		agentsIgnore:           snapshot.AgentsIgnore,
	}
}

func (m *Model) setSettings(settings app.Settings) {
	m.settings = settings
	m.dotsConfiguredCached = app.DotsConfiguredInSettings(settings)
	m.dotsSyncAvailCached = app.DotsSyncAvailabilityInSettings(settings)
}

// toolKey returns the composite key used to identify a tool in maps.
func toolKey(name, provider string) string {
	return name + "\x00" + provider
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func toolNameFromKey(key string) string {
	name, _, _ := strings.Cut(key, "\x00")
	return name
}

func visibleGroupNames(m Model) []string {
	return app.VisibleGroupNamesForCurrentMachine(m.groupNames, m.hostInfo)
}

func prioritizedPickerGroups(m Model) []string {
	return app.GroupPickerNamesForCurrentMachine(m.groupNames, m.hostInfo, m.pickerCreatedGroups)
}

func prioritizePickerGroupList(m Model, groups []string) []string {
	return app.PrioritizeGroupNamesForCurrentMachine(groups, m.hostInfo, m.pickerCreatedGroups)
}

func groupInActiveHost(m Model, group string) bool {
	return app.GroupInActiveHostForCurrentMachinePicker(group, m.hostInfo, m.pickerCreatedGroups)
}

func groupHasActiveHostContext(m Model) bool {
	return app.HasActiveHostGroupContextForCurrentMachine(m.hostInfo)
}

func toolMembershipKey(t *app.ToolView) string {
	if t == nil {
		return ""
	}
	return toolKey(t.Name, t.Provider)
}

// buildAllGroupNames returns the local host group plus reusable group names.
func buildAllGroupNames(groupNames []string) []string {
	return app.AllGroupNamesForCurrentMachine(groupNames)
}

func (m *Model) displaySection(t *app.ToolView) section {
	classification := app.ClassifyToolView(t, toolClassificationContext(*m, t))
	return sectionFromToolView(classification.Section)
}

// rebuildDiscoveredKeys rebuilds the discoveredKeys map from the current
// discoveredTools slice. Call this whenever discoveredTools is reassigned;
// do NOT call it from applyFilter (which runs on every keystroke).
func (m *Model) rebuildDiscoveredKeys() {
	m.discoveredKeys = make(map[string]bool, len(m.discoveredTools))
	for _, t := range m.discoveredTools {
		m.discoveredKeys[toolKey(t.Name, t.Provider)] = true
	}
}

// setGroupFilterFromIdx syncs m.groupFilter from m.groupTabIdx using the given
// allGroups slice ([current-host, "work", ...]). groupTabIdx 0 means "all" and
// clears the filter; any other index selects the corresponding group name.
func (m *Model) setGroupFilterFromIdx(allGroups []string) {
	if m.groupTabIdx == 0 {
		m.groupFilter = ""
	} else if m.groupTabIdx <= len(allGroups) {
		m.groupFilter = allGroups[m.groupTabIdx-1]
	}
}

func (m *Model) applyFilter() {
	if len(m.providerNames) == 0 {
		m.providerNames = m.ecosystemProviderNames()
	}
	if m.providerTabIdx > len(m.providerNames) {
		m.providerTabIdx = 0
	}

	var targetProvider string
	if m.providerTabIdx > 0 && m.providerTabIdx <= len(m.providerNames) {
		targetProvider = m.providerNames[m.providerTabIdx-1]
	}

	result := app.BuildToolViewList(app.ToolViewListOptions{
		Tools:           filterHostInventoryTools(m.allTools, m.hostInventoryTools),
		DiscoveredTools: filterHostInventoryTools(m.discoveredTools, m.hostInventoryTools),
		SearchTools:     filterHostInventoryTools(m.searchTools, m.hostInventoryTools),
		Query:           m.filter.Value(),
		ProviderFilter:  targetProvider,
		GroupFilter:     m.groupFilter,
		ToolMemberships: m.toolMemberships,
		IgnoredTools:    m.ignoredToolsForView(),
		IgnoreLabels:    m.ignoreLabels,
		Classification:  toolClassificationContext(*m, nil),
	})
	m.visibleTools = result.Tools
	m.clampToolCursor()

	counts := make(map[section]int, 5)
	for sectionName, count := range result.Counts {
		counts[sectionFromToolView(sectionName)] = count
	}
	m.sectionCounts = counts
}

// filterHostInventoryTools drops tools that belong to the provider-inventory
// group (auto-discovered system packages), identified by the hostInventory map.
// Membership in a regular host group is a first-class, user-visible assignment
// and is NOT filtered here.
func filterHostInventoryTools(tools []*app.ToolView, hostInventory map[string]bool) []*app.ToolView {
	filtered := make([]*app.ToolView, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		if hostInventory[tool.Name] {
			continue
		}
		filtered = append(filtered, tool)
	}
	return filtered
}

func (m Model) skillsSectionEnabled() bool  { return m.agentsEnabled && m.skillsEnabled }
func (m Model) mcpSectionEnabled() bool     { return m.agentsEnabled && m.mcpEnabled }
func (m Model) pluginsSectionEnabled() bool { return m.agentsEnabled && m.pluginsEnabled }

func (m Model) marketplacesSectionEnabled() bool { return m.agentsEnabled && m.pluginsEnabled }

func (m Model) ecosystemProviderNames() []string {
	if m.app == nil {
		return app.DefaultEcosystemProviderNames()
	}
	return m.app.EcosystemProviderNames()
}

func (m Model) ignoredToolsForView() map[string]bool {
	ignored := make(map[string]bool)
	for name := range m.ignoreSet {
		ignored[name] = true
	}
	for name, ok := range m.toolIgnoreSet {
		if ok {
			ignored[name] = true
		}
	}
	for name := range m.ignoreLabels {
		ignored[name] = true
	}
	return ignored
}

func (m *Model) repositionCursorToTool(name string) {
	for i, tool := range m.visibleTools {
		if tool != nil && tool.Name == name {
			m.cursor = i
			return
		}
	}
}

func (m *Model) clampToolCursor() {
	if len(m.visibleTools) == 0 {
		m.cursor = 0
		m.providerCandidateCursor = 0
		return
	}
	if m.cursor < 0 {
		m.cursor = 0
	} else if m.cursor >= len(m.visibleTools) {
		m.cursor = len(m.visibleTools) - 1
	}
	m.clampProviderCandidateCursor()
}

// sectionOf is context-free; prefer displaySection when model state is available.
func sectionOf(t *app.ToolView) section {
	classification := app.ClassifyToolView(t, app.ToolClassificationContext{})
	return sectionFromToolView(classification.Section)
}

// syncStatusOf returns the out-of-sync sub-category for a tool in sectionOutOfSync.
func (m *Model) syncStatusOf(t *app.ToolView) syncStatus {
	return syncStatusFromToolView(app.ToolSyncStatusForTool(t, toolClassificationContext(*m, t)))
}

func toolClassificationContext(m Model, t *app.ToolView) app.ToolClassificationContext {
	ignored := false
	discovered := false
	if t != nil {
		ignored = m.ignoreLabels[t.Name] != "" || m.ignoreSet[t.Name]
		discovered = m.discoveredKeys[toolKey(t.Name, t.Provider)]
	}
	return app.ToolClassificationContext{
		Ignored:                ignored,
		Discovered:             discovered,
		ToolProviderPins:       m.toolProviderPins,
		EffectiveSystemManager: m.effectiveSystemManager,
		EffectivePythonManager: m.effectivePythonManager,
		EffectiveNodeManager:   m.effectiveNodeManager,
		NvmManaged:             m.nvmManaged,
	}
}

func sectionFromToolView(sectionName app.ToolViewSection) section {
	switch sectionName {
	case app.ToolViewSectionUpdates:
		return sectionUpdates
	case app.ToolViewSectionQuarantined:
		return sectionQuarantined
	case app.ToolViewSectionOutOfSync:
		return sectionOutOfSync
	case app.ToolViewSectionInstalled:
		return sectionInstalled
	case app.ToolViewSectionIgnored:
		return sectionIgnored
	default:
		return sectionAvailable
	}
}

func syncStatusFromToolView(status app.ToolSyncStatus) syncStatus {
	switch status {
	case app.ToolSyncMissing:
		return syncMissing
	case app.ToolSyncUnclaimed:
		return syncOrphan
	case app.ToolSyncWrongProvider:
		return syncWrongProv
	case app.ToolSyncNvmManaged:
		return syncNvmManaged
	default:
		return syncOK
	}
}

func isProviderRepairSync(ss syncStatus) bool {
	return ss == syncWrongProv || ss == syncNvmManaged
}

func (m Model) effectiveNodeManagerLabel() string {
	if m.effectiveNodeManager != "" {
		return m.effectiveNodeManager
	}
	return "pnpm"
}

// selectedTool returns the currently highlighted tool or nil.
func (m *Model) selectedTool() *app.ToolView {
	if len(m.visibleTools) == 0 || m.cursor < 0 || m.cursor >= len(m.visibleTools) {
		return nil
	}
	return m.visibleTools[m.cursor]
}

func (m *Model) selectedProviderCandidateTool(t *app.ToolView) *app.ToolView {
	candidates := providerCandidateOptions(*m, t)
	if len(candidates) == 0 {
		return t
	}
	idx := clampIndex(m.providerCandidateCursor, len(candidates))
	candidate := candidates[idx]
	out := *t
	out.Provider = candidate.Provider
	out.Package = candidate.EffectivePackage(t.Name)
	return &out
}

func (m *Model) clampProviderCandidateCursor() {
	count := len(providerCandidateOptions(*m, m.selectedTool()))
	if count == 0 {
		m.providerCandidateCursor = 0
		return
	}
	m.providerCandidateCursor = clampIndex(m.providerCandidateCursor, count)
}

func providerCandidateOptions(m Model, t *app.ToolView) []app.ToolInstallSpec {
	if t == nil || t.Installed || !t.Tracked {
		return nil
	}
	candidates := m.toolProviderCandidates[t.Name]
	if len(candidates) < 2 {
		return nil
	}
	out := make([]app.ToolInstallSpec, 0, len(candidates))
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.Provider) == "" {
			continue
		}
		out = append(out, candidate)
	}
	if len(out) < 2 {
		return nil
	}
	sort.SliceStable(out[1:], func(i, j int) bool {
		left := strings.ToLower(strings.TrimSpace(out[i+1].Provider) + "/" + out[i+1].EffectivePackage(t.Name))
		right := strings.ToLower(strings.TrimSpace(out[j+1].Provider) + "/" + out[j+1].EffectivePackage(t.Name))
		return left < right
	})
	return out
}

// countSection returns the number of visible tools in a given section.
func (m *Model) countSection(s section) int {
	n := 0
	for _, t := range m.visibleTools {
		if m.displaySection(t) == s {
			n++
		}
	}
	return n
}

type spinnerActivityState struct {
	loading                    bool
	dotsLoading                bool
	dotsPeekLoading            bool
	doctorRunning              bool
	settingsSaveRunning        bool
	dashboardReconcileRunning  bool
	setupAgentsDiffLoading     bool
	dotsServicesRefreshing     bool
	searching                  bool
	traceLogLoading            bool
	skillsRunning              bool
	skillAddRunning            bool
	mcpRunning                 bool
	pluginRunning              bool
	marketplaceRunning         bool
	agentsOpKey                string
	scanningProviders          int
	outdatedProviders          int
	providerSnapshotRefreshing bool
	outdatedSnapshotRefreshing bool
	discoveryRefreshing        bool
	descRefreshing             bool
	upgradingKeys              int
}

func (m Model) spinnerActivityState() spinnerActivityState {
	return spinnerActivityState{
		loading:                    m.loading,
		dotsLoading:                m.dotsLoading,
		dotsPeekLoading:            m.dotsPeekLoading,
		doctorRunning:              m.doctorRunning,
		settingsSaveRunning:        m.settingsSaveRunning,
		dashboardReconcileRunning:  m.dashboardReconcileRunning,
		setupAgentsDiffLoading:     m.setupAgentsDiffLoading,
		dotsServicesRefreshing:     m.dotsServicesRefreshing,
		searching:                  m.searching,
		traceLogLoading:            m.traceLogLoading,
		skillsRunning:              m.skillsRunning,
		skillAddRunning:            m.skillAddRunning,
		mcpRunning:                 m.mcpRunning,
		pluginRunning:              m.pluginRunning,
		marketplaceRunning:         m.marketplaceRunning,
		agentsOpKey:                m.agentsOpKey,
		scanningProviders:          len(m.scanningProviders),
		outdatedProviders:          len(m.outdatedProviders),
		providerSnapshotRefreshing: m.providerSnapshotRefreshing,
		outdatedSnapshotRefreshing: m.outdatedSnapshotRefreshing,
		discoveryRefreshing:        m.discoveryRefreshing,
		descRefreshing:             m.descRefreshing,
		upgradingKeys:              len(m.upgradingKeys),
	}
}

func (s spinnerActivityState) startedSince(before spinnerActivityState) bool {
	return s.loading && !before.loading ||
		s.dotsLoading && !before.dotsLoading ||
		s.dotsPeekLoading && !before.dotsPeekLoading ||
		s.doctorRunning && !before.doctorRunning ||
		s.settingsSaveRunning && !before.settingsSaveRunning ||
		s.dashboardReconcileRunning && !before.dashboardReconcileRunning ||
		s.setupAgentsDiffLoading && !before.setupAgentsDiffLoading ||
		s.dotsServicesRefreshing && !before.dotsServicesRefreshing ||
		s.searching && !before.searching ||
		s.traceLogLoading && !before.traceLogLoading ||
		s.skillsRunning && !before.skillsRunning ||
		s.skillAddRunning && !before.skillAddRunning ||
		s.mcpRunning && !before.mcpRunning ||
		s.pluginRunning && !before.pluginRunning ||
		s.marketplaceRunning && !before.marketplaceRunning ||
		s.agentsOpKey != "" && before.agentsOpKey == "" ||
		s.scanningProviders > before.scanningProviders ||
		s.outdatedProviders > before.outdatedProviders ||
		s.providerSnapshotRefreshing && !before.providerSnapshotRefreshing ||
		s.outdatedSnapshotRefreshing && !before.outdatedSnapshotRefreshing ||
		s.discoveryRefreshing && !before.discoveryRefreshing ||
		s.descRefreshing && !before.descRefreshing ||
		s.upgradingKeys > before.upgradingKeys
}

// spinnerActivityActive reports whether any async activity that drives the
// shared spinner is in flight. Every tick-rescheduling and spinner-gating
// site must use this state so new action flags cannot miss either lifecycle.
func (m Model) spinnerActivityActive() bool {
	return m.spinnerActivityState() != (spinnerActivityState{})
}

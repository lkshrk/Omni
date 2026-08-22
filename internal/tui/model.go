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

type section int

const (
	sectionUpdates     section = iota // 0 - tools with updates available
	sectionQuarantined                // 1 - tools with deferred updates
	sectionOutOfSync                  // 2 - config-missing / orphan / wrong-provider
	sectionInstalled                  // 3 - installed, up-to-date
	sectionAvailable                  // 4 - available to install (declared in config or found via search)
	sectionIgnored                    // 5 - in the active host's ignore list (rendered last, dimmed)
)

type syncStatus int

const (
	syncOK         syncStatus = iota // in sync (not in sectionOutOfSync)
	syncMissing                      // ↓ in config, not installed locally
	syncOrphan                       // + installed locally, not in config
	syncWrongProv                    // ⚠ installed with wrong concrete provider
	syncNvmManaged                   // ⚠ system-provider but active binary is nvm-managed
)

const searchCacheTTL = 5 * time.Minute

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
	// Set only on the child-row path: subpath is extracted out of parentName into its own entry, then given a host variant.
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
	// Cached once in applyFilter so View() does not re-iterate visibleTools on every keystroke.
	sectionCounts       map[section]int
	searching           bool                        // true while a provider search is in flight
	searchGen           int                         // incremented on each new search; stale results are dropped
	searchCancel        context.CancelFunc          // cancels the in-flight HTTP search; nil when idle
	searchCache         map[string]searchCacheEntry // query → cached results
	descRefreshGen      int                         // generation for background description refreshes
	cursor              int
	loading             bool
	loadingOwner        loadingOwner    // who raised m.loading; see loading_gate.go
	loadingGen          int             // bumped on every raise; stamped onto opCompleteMsg
	confirmQuit         bool            // true after the first q; a matching second press exits
	quitConfirmKey      string          // key used to arm confirmQuit ("q")
	confirmGen          int             // increments whenever a timed confirmation is armed
	ctrlCConfirm        bool            // independent quit overlay; must not disturb action confirmations
	ctrlCConfirmGen     int             // invalidates only the ctrl+c confirmation timer
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
	startupLoadErr      bool
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
	outdatedTotal          int
	refreshIssue           string
	refreshIssuePriority   int
	providerScanToolCounts map[string]int
	providerScanToolDone   map[string]int
	providerScanLabels     map[string]string
	refreshToolDone        int
	refreshToolTotal       int
	scanGen                int
	// Owned by the scan fan-out, so no producer may close it; handleProviderScannedMsg closes this rather than m.progressCh, which may belong to a newer stream.
	scanProgressCh chan progressUpdate
	// The orphan scan closes its own channel before its result message lands, so the model must release m.progressCh on that message; waiting for progressStreamClosedMsg would leave the description phase without a stream.
	discoveryProgressCh chan progressUpdate
	// Description results can race their buffered progress messages too; keep ownership explicit so the result invalidates only its own stream.
	descriptionProgressCh      chan progressUpdate
	discoveryGen               int
	providerSnapshotRefreshing bool
	outdatedSnapshotRefreshing bool
	discoveryRefreshing        bool
	descRefreshing             bool
	// Gates progressDoneMsg and the refresh msgs from clearing m.loading or the "Migrating…" status mid-migration.
	migrating bool

	progressCh   chan progressUpdate
	progressGen  int
	progressText string

	commandInput         textinput.Model
	commandSuggestions   []palCmd
	commandCursor        int
	commandOrigin        viewMode // tab the palette was opened from; restored on close
	dotsConfiguredCached bool
	dotsSyncAvailCached  app.DotsSyncAvailability
	consolidateOptions   []app.EcosystemMigration // cached at load time; registry lookup, no IO

	// Used by the group picker (move tool to group), not for list filtering.
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

	providerNames  []string // ordered provider-family names from the app/provider registry
	providerTabIdx int      // 0=All, 1+=providerNames[idx-1]

	effectivePythonManager string // e.g. "uv", "pip3"
	effectiveNodeManager   string // e.g. "bun", "pnpm", "npm"
	effectiveSystemManager string // e.g. "brew", "apt" — concrete PM backing the system provider family

	hostInfo    *app.HostInfo
	hostCursor  int
	ignoreSet   map[string]bool // tool names ignored by the active host
	groupFilter string          // non-empty: only show tools belonging to this group
	groupTabIdx int             // 0=all, 1=current host, 2+=reusable groups; mirrors groupFilter

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

	// 0 = hosts list, 1 = groups list
	assignmentSection  int
	groupCursor        int  // cursor within the allGroupNames list
	groupDeleteConfirm bool // true when awaiting second Enter to confirm group delete
	groupDeleteName    string
	groupDeleteChoice  int  // 0=move last-membership tools to this host, 1=delete last-membership specs
	groupRenameMode    bool // true when inline rename text input is open
	groupRenameName    string
	groupCreating      bool // true when the shared new-group/new-host name popup is open
	hostCreateStep     int  // 0=none, 1=copy-or-fresh prompt, 2=source-host picker
	hostCreateName     string

	groupToolsEditor         groupAssignmentEditor
	groupToolsProviderIdx    int
	groupToolsIgnore         map[string]bool // logical tool name -> staged group-level ignore in groupToolsEditor.group
	groupToolsOriginalIgnore map[string]bool // logical tool name -> original group-level ignore in groupToolsEditor.group

	groupDotsEditor groupAssignmentEditor

	pickerGroups          []string
	pickerCursor          int
	pickerCreatingGroup   bool // true when the user selected the "+ new group…" sentinel
	pickerPurposeClaim    bool // true when the picker is for claiming an orphan tool
	pickerPurposeInstall  bool // true when the picker is for install-and-add
	pickerPurposeDotAdd   bool // true when the picker is for dots add
	pickerDotAddPath      string
	pickerDotAddRawPath   string
	pickerPurposeReassign bool     // true when iterating claimed tools for group reassignment
	pendingGroupReassign  []string // queue of tool names awaiting group reassignment
	reassignCreatedGroups []string // groups created during the reassign sequence (carried across pickers)
	pickerMembershipKind  string
	pickerMembershipName  string
	pickerMembershipKey   string
	// Set when the group popup opened on a child sub-path row: confirming extracts the subtree into a new entry instead of editing an existing entry's membership.
	pickerDotExtractParent string
	pickerDotExtractSub    string
	pickerActionTool       app.ToolView
	pickerActionToolSet    bool
	pickerOriginalGroups   []string
	pickerCreatedGroups    []string
	scopeOptions           []scopeOption
	scopeCursor            int
	scopeTarget            app.ToolView
	scopeTargetSet         bool
	fallbackTarget         app.ToolView
	fallbackTargetSet      bool
	fallbackEditor         fallbackEditorState

	// 0=create config, 1=import tools, 2=providers, 3=node manager, 5=enable dotfiles, 6=dots repo path, 7=copy host, 8=host picker, 9=reusable groups, 10=existing-host activation, 11=create config
	setupStep int
	// Zero value keeps first-run setup on the tools tab.
	setupBackgroundMode viewMode
	setupProviders      []app.SetupProviderOption
	setupProviderIdx    int
	setupCopyHostIdx    int
	setupGroupIdx       int
	setupGroupDraft     map[string]bool
	setupActivationIdx  int
	// Set after onboarding so a follow-up reload cannot reopen setup from a stale no-host snapshot.
	setupComplete bool
	// Keeps the loading overlay up between switching back to the main UI and the first post-setup reload finishing.
	setupReloading bool
	// Invalidates the armed overlay timeout when the reload finishes or is dismissed before it fires.
	setupReloadGen int
	// Prevents a post-bootstrap reload from repeating the dot sync the bootstrap flow just completed.
	skipLaunchDotsSyncOnce bool
	// Step to restore if automatic host creation fails after the UI advanced to the dotfile decision step.
	setupHostReturnStep int

	// Config exists but no host entry matches this machine; all navigation is locked until a host is active.
	hostRequired bool

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

	dotsFilePicker       pathPickerModel
	showFilePicker       bool
	filePickerTitle      string
	filePickerAllowFiles bool
	filePickerForConfig  bool
	settingsInput        textinput.Model // used by settings/group text inputs

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

	apmRunning                  bool
	apmCommand                  string
	apmOutput                   string
	apmErr                      error
	agentsOnboardPlan           *app.AgentsOnboardResult
	agentsOnboardConfirm        bool
	agentsOnboardItem           int
	agentsOnboardOperation      string
	agentsOnboardCleanupConfirm bool

	dangerConfirmRow int // settings row awaiting inline confirmation; -1 = none

	width  int
	height int

	// Each Model owns its palette so parallel test instances do not share mutable state.
	palette palette

	// Defaults to true (the palette is dark-optimised) until the terminal answers tea.RequestBackgroundColor.
	isDark bool

	// When false the spinner tick chain is suspended to avoid burning CPU in the background.
	focused bool
}

type listConfirmation struct {
	action   string
	name     string
	provider string
	// Explicit provider pin for clear-override; provider stays the declared tool provider so row keys and prompt matching keep working.
	pinnedProvider string
	installed      bool
	installedWith  string
}

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

	return Model{
		app:              a,
		ctx:              ctx,
		cancel:           cancel,
		keys:             DefaultKeyMap(),
		spinner:          sp,
		help:             newHelp(),
		filter:           fi,
		commandInput:     ci,
		settingsInput:    si,
		mode:             viewStatus,
		commandCursor:    -1,
		commandOrigin:    viewList,
		loading:          true,
		cursor:           -1, // nothing selected until the user navigates
		cursorHidden:     true,
		upgradingKeys:    make(map[string]bool),
		searchCache:      make(map[string]searchCacheEntry),
		dotsConfirmIdx:   -1,
		dotsOverwriteIdx: -1,
		dotsLocalIdx:     -1,
		dotsIgnoreIdx:    -1,
		dotsVariantIdx:   -1,
		dangerConfirmRow: -1,
		// Async results can land before the first WindowSizeMsg; without a sane size those frames overrun the real terminal.
		width:         defaultTerminalWidth,
		height:        defaultTerminalHeight,
		isDark:        true,             // assume dark until terminal replies
		focused:       true,             // assume focused until a BlurMsg says otherwise
		palette:       defaultPalette(), // dark default; rebuilt on BackgroundColorMsg
		doctorRunning: true,             // Init fires doctor; prevents double-run on status tab
	}
}

func (m *Model) shutdown() {
	m.closeAdminTerminalSession()
	m.adminTerminalQueue = nil
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
	m.outdatedProviders = nil
	m.outdatedTotal = 0
	m.refreshIssue = ""
	m.refreshIssuePriority = 0
	m.providerScanToolCounts = nil
	m.providerScanToolDone = nil
	m.providerScanLabels = nil
	m.descriptionProgressCh = nil
	m.refreshToolDone = 0
	m.refreshToolTotal = 0
	m.upgradingKeys = make(map[string]bool)
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		loadTools(m.app, m.ctx),
		m.doRunDoctor(),
		tea.RequestBackgroundColor,
	)
}

// Reads the DB cache without probing providers so the list renders on the first frame; a background doRefreshInstalled updates install status.
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
		dotsSyncAvail:          snapshot.DotsSyncAvailability,
		dotsSyncAvailKnown:     true,
		setupProviders:         snapshot.SetupProviders,
		ecosystemProviders:     snapshot.EcosystemProviderNames,
	}
}

func (m *Model) setSettings(settings app.Settings) {
	m.settings = settings
	m.dotsConfiguredCached = app.DotsConfiguredInSettings(settings)
	m.dotsSyncAvailCached = app.DotsSyncAvailabilityInSettings(settings)
}

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

func buildAllGroupNames(groupNames []string) []string {
	return app.AllGroupNamesForCurrentMachine(groupNames)
}

func (m *Model) displaySection(t *app.ToolView) section {
	classification := app.ClassifyToolView(t, toolClassificationContext(*m, t))
	return sectionFromToolView(classification.Section)
}

// Call whenever discoveredTools is reassigned; do NOT call from applyFilter, which runs on every keystroke.
func (m *Model) rebuildDiscoveredKeys() {
	m.discoveredKeys = make(map[string]bool, len(m.discoveredTools))
	for _, t := range m.discoveredTools {
		m.discoveredKeys[toolKey(t.Name, t.Provider)] = true
	}
}

// groupTabIdx 0 means "all" and clears the filter; any other index selects that group name in allGroups.
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

// Membership in a regular host group is a first-class user-visible assignment and is deliberately NOT filtered here.
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

// Context-free; prefer displaySection when model state is available.
func sectionOf(t *app.ToolView) section {
	classification := app.ClassifyToolView(t, app.ToolClassificationContext{})
	return sectionFromToolView(classification.Section)
}

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
	dotsServicesRefreshing     bool
	searching                  bool
	traceLogLoading            bool
	apmRunning                 bool
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
		dotsServicesRefreshing:     m.dotsServicesRefreshing,
		searching:                  m.searching,
		traceLogLoading:            m.traceLogLoading,
		apmRunning:                 m.apmRunning,
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
		s.dotsServicesRefreshing && !before.dotsServicesRefreshing ||
		s.searching && !before.searching ||
		s.traceLogLoading && !before.traceLogLoading ||
		s.apmRunning && !before.apmRunning ||
		s.scanningProviders > before.scanningProviders ||
		s.outdatedProviders > before.outdatedProviders ||
		s.providerSnapshotRefreshing && !before.providerSnapshotRefreshing ||
		s.outdatedSnapshotRefreshing && !before.outdatedSnapshotRefreshing ||
		s.discoveryRefreshing && !before.discoveryRefreshing ||
		s.descRefreshing && !before.descRefreshing ||
		s.upgradingKeys > before.upgradingKeys
}

// Every tick-rescheduling and spinner-gating site must use this so new action flags cannot miss either lifecycle.
func (m Model) spinnerActivityActive() bool {
	return m.spinnerActivityState() != (spinnerActivityState{})
}

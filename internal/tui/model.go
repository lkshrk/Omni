// Package tui contains the Bubbletea TUI.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/profile"
	"github.com/lkshrk/omni/internal/provider"
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
	syncOK        syncStatus = iota // in sync (not in sectionOutOfSync)
	syncMissing                     // ↓ in config, not installed locally
	syncOrphan                      // + installed locally, not in config
	syncWrongProv                   // ⚠ installed with wrong concrete provider
)

const searchCacheTTL = 5 * time.Minute

// searchCacheEntry holds cached provider search results.
type searchCacheEntry struct {
	tools []*database.ToolCache
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
	name   string
	remove bool
}

type dotsPeekState struct {
	result app.DotsPeekResult
	scroll int
}

type groupToolSection int

const (
	groupToolSectionEnabled groupToolSection = iota
	groupToolSectionDisabled
	groupToolSectionIgnored
)

type groupToolRow struct {
	tool        *database.ToolCache
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
	dashboardReconcilePlanSyncTools    dashboardReconcilePlanKind = app.ReconcileStepSyncTools
	dashboardReconcilePlanUpgradeTools dashboardReconcilePlanKind = app.ReconcileStepUpgradeTools
	dashboardReconcilePlanSyncDots     dashboardReconcilePlanKind = app.ReconcileStepSyncDots
	dashboardReconcilePlanCommitDots   dashboardReconcilePlanKind = app.ReconcileStepCommitDots
	dashboardReconcilePlanFixIgnore    dashboardReconcilePlanKind = app.ReconcileStepFixIgnore
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

	settings             config.Settings
	settingsCursor       int // which settings row is selected
	settingsDetailScroll int
	taps                 []string

	allTools        []*database.ToolCache // unfiltered
	discoveredTools []*database.ToolCache // locally installed but not in config
	discoveredKeys  map[string]bool       // "name\x00provider" set for fast orphan lookup
	visibleTools    []*database.ToolCache // after filter + sort
	searchTools     []*database.ToolCache // results from last provider search (not in allTools)
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
	rowActionErrors     map[string]*provider.ActionError
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

	scanningProviders          map[string]bool
	outdatedProviders          map[string]bool
	providerScanToolCounts     map[string]int
	providerScanToolDone       map[string]int
	providerScanLabels         map[string]string
	refreshToolDone            int
	refreshToolTotal           int
	scanGen                    int
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
	groupNames       []string          // ordered reusable group names
	toolGroups       map[string]string // "name\x00provider" → group name
	toolMemberships  map[string][]string
	ignoreLabels     map[string]string // logical tool name → compact ignore source label
	toolIgnoreSet    map[string]bool
	groupIgnoreSet   map[string]map[string]bool
	toolProviderPins map[string]string
	toolFallbacks    map[string]config.FallbackSpec

	// provider filter — [All] [system] [node] [python] …
	providerNames  []string // ordered ecosystem provider names from the app/provider registry
	providerTabIdx int      // 0=All, 1+=providerNames[idx-1]

	// effective package-manager binaries resolved at load time via PATH probing.
	effectivePythonManager string // e.g. "uv", "pip3"
	effectiveNodeManager   string // e.g. "bun", "pnpm", "npm"
	effectiveSystemManager string // e.g. "brew", "apt" — concrete PM backing the system ecosystem provider

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
	pickerActionTool      database.ToolCache
	pickerActionToolSet   bool
	pickerOriginalGroups  []string
	pickerCreatedGroups   []string
	scopeOptions          []scopeOption
	scopeCursor           int
	scopeTarget           database.ToolCache
	scopeTargetSet        bool
	fallbackTarget        database.ToolCache
	fallbackTargetSet     bool
	fallbackEditor        fallbackEditorState

	// setup wizard step (0 = create config?, 1 = import tools?, 2 = provider
	// selection, 3 = node manager, 4 = unused, 5 = enable dotfiles?, 6 = dots
	// repo path, 7 = copy host?, 8 = host picker, 9 = reusable groups,
	// 10 = existing-host activation)
	setupStep int
	// setupBackgroundMode is the main tab rendered behind setup/onboarding
	// popups. Zero value keeps first-run setup on the tools tab.
	setupBackgroundMode viewMode
	setupProviders      []app.SetupProviderOption
	setupProviderIdx    int
	// setupNodeMgrIdx is the cursor for node manager selection in step 3.
	// 0=auto, 1=bun, 2=pnpm, 3=npm
	setupNodeMgrIdx int
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

	// provider priority editor (active when editing the Priority row in settings)
	editingPriority                bool
	priorityCursor                 int
	priorityDraft                  []string
	editingServiceDuration         bool
	serviceDurationRow             int
	serviceDurationIdx             int
	doctorResult                   *app.DoctorResult
	doctorErr                      string
	doctorRunning                  bool
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
	dotsConfirmIdx         int // index of entry pending delete confirm; -1 = none
	dotsOverwriteIdx       int // index of conflict entry pending use-repo confirm; -1 = none
	dotsLocalIdx           int // index of conflict entry pending use-local confirm; -1 = none
	dotsIgnoreIdx          int // index of child path pending ignore/include confirm; -1 = none
	dotsVariantIdx         int // index of entry pending host variant choice/removal; -1 = none
	dotsVariantMode        dotsVariantMode
	dotsPeek               *dotsPeekState
	dotsPeekLoading        bool
	dotsPeekGen            int
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
	action        string
	name          string
	provider      string
	installed     bool
	installedWith string
}

func (m Model) setupNodeManagerChoices() []app.SettingsManagerOption {
	choices := app.DefaultSetupNodeManagerOptions()
	if m.app != nil {
		choices = m.app.SetupNodeManagerOptions()
	}
	return choices
}

// New creates the initial Model.
func New(a *app.App, ctx context.Context) Model {
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
		isDark:           true,             // assume dark until terminal replies
		focused:          true,             // assume focused until a BlurMsg says otherwise
		palette:          defaultPalette(), // dark default; rebuilt on BackgroundColorMsg
		doctorRunning:    true,             // Init fires doctor; prevents double-run on status tab
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
		dotMemberships:         snapshot.DotMemberships,
		ignoreLabels:           ignoreLabels,
		toolIgnoreSet:          snapshot.ToolIgnores,
		groupIgnoreSet:         snapshot.GroupIgnores,
		toolProviderPins:       snapshot.ToolProviderPins,
		toolFallbacks:          snapshot.ToolFallbacks,
		hostInfo:               hostInfo,
		ignoreList:             ignoreList,
		dotsHistory:            snapshot.DotsHistory,
		dotsHistoryErr:         errorString(snapshot.DotsHistoryErr),
		dotsState:              snapshot.DotsState,
		noHost:                 hostInfo != nil && hostInfo.Active == "",
		effectivePythonManager: snapshot.EffectivePythonManager,
		effectiveNodeManager:   snapshot.EffectiveNodeManager,
		effectiveSystemManager: snapshot.EffectiveSystemManager,
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

func (m *Model) setSettings(settings config.Settings) {
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

func toolMembershipKey(t *database.ToolCache) string {
	if t == nil {
		return ""
	}
	return toolKey(t.Name, t.Provider)
}

// buildAllGroupNames returns the local host group plus reusable group names.
func buildAllGroupNames(groupNames []string) []string {
	return app.AllGroupNamesForCurrentMachine(groupNames)
}

func (m *Model) displaySection(t *database.ToolCache) section {
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
		Tools:           m.allTools,
		DiscoveredTools: m.discoveredTools,
		SearchTools:     m.searchTools,
		Query:           m.filter.Value(),
		ProviderFilter:  targetProvider,
		GroupFilter:     m.groupFilter,
		ToolMemberships: m.toolMemberships,
		IgnoredTools:    m.ignoreSet,
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

func (m Model) ecosystemProviderNames() []string {
	if m.app == nil {
		return app.DefaultEcosystemProviderNames()
	}
	return m.app.EcosystemProviderNames()
}

func (m *Model) clampToolCursor() {
	if len(m.visibleTools) == 0 {
		m.cursor = 0
		return
	}
	if m.cursor < 0 {
		m.cursor = 0
		return
	}
	if m.cursor >= len(m.visibleTools) {
		m.cursor = len(m.visibleTools) - 1
	}
}

// sectionOf is context-free; prefer displaySection when model state is available.
func sectionOf(t *database.ToolCache) section {
	classification := app.ClassifyToolView(t, app.ToolClassificationContext{})
	return sectionFromToolView(classification.Section)
}

// syncStatusOf returns the out-of-sync sub-category for a tool in sectionOutOfSync.
func (m *Model) syncStatusOf(t *database.ToolCache) syncStatus {
	return syncStatusFromToolView(app.ToolSyncStatusForTool(t, toolClassificationContext(*m, t)))
}

func toolClassificationContext(m Model, t *database.ToolCache) app.ToolClassificationContext {
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
	default:
		return syncOK
	}
}

// selectedTool returns the currently highlighted tool or nil.
func (m *Model) selectedTool() *database.ToolCache {
	if len(m.visibleTools) == 0 || m.cursor < 0 || m.cursor >= len(m.visibleTools) {
		return nil
	}
	return m.visibleTools[m.cursor]
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

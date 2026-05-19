// Package tui contains the Bubbletea TUI.
package tui

import (
	"context"
	"fmt"
	"slices"
	"sort"
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
	viewAdminTerminal                   // privileged package action terminal handoff
	viewDots                            // dotfiles management tab
)

// section groups tools into visual categories in the list view.
type section int

const (
	sectionUpdates   section = iota // 0 - tools with updates available
	sectionOutOfSync                // 1 - config-missing / orphan / wrong-provider
	sectionInstalled                // 2 - installed, up-to-date
	sectionAvailable                // 3 - available to install (declared in config or found via search)
	sectionIgnored                  // 4 - in the active host's ignore list (rendered last, dimmed)
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

// setupProviderRow is one row in the provider selection step of the setup wizard.
type setupProviderRow struct {
	name    string // provider name: "system", "node", "python"
	label   string // display label: e.g. "system(brew)", "node(bun)", "python(uv)"
	enabled bool   // true = will be active; false = disabled on this machine
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

type dashboardReconcilePlanKind string

const (
	dashboardReconcilePlanSyncTools    dashboardReconcilePlanKind = "sync-tools"
	dashboardReconcilePlanUpgradeTools dashboardReconcilePlanKind = "upgrade-tools"
	dashboardReconcilePlanSyncDots     dashboardReconcilePlanKind = "sync-dots"
	dashboardReconcilePlanCommitDots   dashboardReconcilePlanKind = "commit-dots"
	dashboardReconcilePlanFixIgnore    dashboardReconcilePlanKind = "fix-ignore"
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

	settings       config.Settings
	settingsCursor int // which settings row is selected
	taps           []string

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

	settingsSaveRunning        bool
	settingsSaveQueued         bool
	settingsSaveQueuedSnapshot config.Settings
	settingsSaveQueuedGen      int
	settingsSaveGen            int
	settingsSaveInFlightGen    int
	adminTerminal              *adminTerminalState
	adminTerminalQueue         []adminTerminalState
	adminTerminalGen           int

	// scanningProviders holds the names of providers whose parallel scan goroutines
	// are still in flight. The spinner is shown while the set is non-empty; each
	// providerScannedMsg removes one entry. Initialized to the set of unique
	// provider names from allTools on every scan kick-off.
	scanningProviders          map[string]bool
	outdatedProviders          map[string]bool
	providerScanToolCounts     map[string]int
	providerScanToolDone       map[string]int
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
	commandInput       textinput.Model
	commandSuggestions []palCmd
	commandCursor      int
	consolidateOptions []app.EcosystemMigration // cached at load time; registry lookup, no IO

	// group subtabs — used by group picker (move tool to group), not for list filtering
	groupNames          []string          // ordered reusable group names
	toolGroups          map[string]string // "name\x00provider" → group name
	toolMemberships     map[string][]string
	ignoreLabels        map[string]string // logical tool name → compact ignore source label
	toolIgnoreSet       map[string]bool
	groupIgnoreSet      map[string]map[string]bool
	toolProviderPins    map[string]string
	configuredProviders []string
	providerToolCounts  map[string]int

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

	// setup wizard step (0 = create config?, 1 = import tools?, 2 = provider
	// selection, 3 = node manager, 4 = unused, 5 = enable dotfiles?, 6 = dots
	// repo path, 7 = copy host?, 8 = host picker, 9 = reusable groups,
	// 10 = existing-host activation)
	setupStep int
	// setupBackgroundMode is the main tab rendered behind setup/onboarding
	// popups. Zero value keeps first-run setup on the tools tab.
	setupBackgroundMode viewMode
	// setupProviders holds the provider rows shown in setup step 2.
	setupProviders   []setupProviderRow
	setupProviderIdx int
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
	stowInstallSettings    config.Settings
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

// buildSetupProvidersFromManagers creates provider rows from already-resolved manager data.
// Used by loadTools (which already calls AllAvailableManagers + ResolvedEcosystemProviders) to
// avoid a redundant round-trip on launch.
//
// Labels show all detected managers: "node(pnpm • npm)" when multiple found,
// "node(pnpm)" when one, plain "node" when none detected.
//
// Rows respect the existing settings.DisabledProviders so that re-running the
// setup wizard (noHost=true path) does not re-enable providers the user has
// previously disabled.
func buildSetupProvidersFromManagers(metaMap map[string]string, allPyBins, allNodeBins []string, settings config.Settings) []setupProviderRow {
	managerLabel := func(meta string, bins []string) string {
		if len(bins) == 0 {
			return meta
		}
		return meta + "(" + strings.Join(bins, " • ") + ")"
	}

	type entry struct {
		name  string
		label string
	}
	entries := []entry{
		{provider.EcosystemSystem, provider.EcosystemSystem},
		{provider.EcosystemNode, managerLabel(provider.EcosystemNode, allNodeBins)},
		{provider.EcosystemPython, managerLabel(provider.EcosystemPython, allPyBins)},
	}
	if concrete := metaMap[provider.EcosystemSystem]; concrete != "" {
		entries[0].label = provider.EcosystemSystem + "(" + concrete + ")"
	}

	rows := make([]setupProviderRow, 0, len(entries))
	for _, e := range entries {
		isEnabled := !slices.Contains(settings.DisabledProviders, e.name)
		rows = append(rows, setupProviderRow{name: e.name, label: e.label, enabled: isEnabled})
	}
	return rows
}

// isNodeProviderEnabled reports whether the node ecosystem provider is enabled in
// the current setup wizard provider list.
func isNodeProviderEnabled(rows []setupProviderRow) bool {
	for _, r := range rows {
		if r.name == provider.EcosystemNode && r.enabled {
			return true
		}
	}
	return false
}

// nodeMgrChoices is the ordered list of node manager options shown in the
// setup wizard. Index 0 = auto (empty string = bun preferred, then pnpm, npm).
type managerChoice struct {
	value string
	label string
	desc  string
}

var nodeMgrChoices = append([]managerChoice{{value: "", label: "auto", desc: "use the provider's preferred available manager"}}, managerChoices(provider.EcosystemNode)...)

func managerChoices(ecosystem string) []managerChoice {
	names := provider.BuiltinSettingsManagerNames(ecosystem)
	choices := make([]managerChoice, 0, len(names))
	for _, name := range names {
		choices = append(choices, managerChoice{value: name, label: name, desc: managerDescription(name)})
	}
	return choices
}

func managerDescription(name string) string {
	switch name {
	case "bun":
		return "fast runtime + package manager"
	case "pnpm":
		return "disk-efficient, workspace-native"
	case "npm":
		return "bundled with every Node.js install"
	default:
		return "available manager"
	}
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
		hostInfo := snapshot.HostInfo
		noHost := hostInfo != nil && hostInfo.Active == ""
		var activeHost string
		var hostIgnore []string
		if hostInfo != nil && hostInfo.Active != "" {
			activeHost = hostInfo.Active
			if prof, ok := hostInfo.Hosts[activeHost]; ok {
				hostIgnore = prof.Ignore
			}
		}
		groups := snapshot.Groups
		toolMemberships := snapshot.ToolMemberships
		dotMemberships := snapshot.DotMemberships
		toolGroups := compactToolGroupMapForHost(toolMemberships, hostInfo)
		groupNames := buildGroupNames(groups)
		ignoreLabels := buildIgnoreLabelsFromState(hostIgnore, snapshot.GlobalIgnoredTools, snapshot.ToolIgnores)
		toolIgnoreSet, groupIgnoreSet, toolProviderPins := buildToolScopeStateFromState(snapshot.GlobalIgnoredTools, snapshot.ToolIgnores, snapshot.ToolProviderPins)
		ignoreList := make([]string, 0, len(ignoreLabels))
		for name := range ignoreLabels {
			ignoreList = append(ignoreList, name)
		}
		ecosystemMap := snapshot.EcosystemProviders
		effectiveSystemManager := ecosystemMap[provider.EcosystemSystem]
		// Build setup provider rows from already-fetched manager data — no extra calls needed.
		spRows := buildSetupProvidersFromManagers(ecosystemMap, snapshot.AllPythonManagers, snapshot.AllNodeManagers, snapshot.Settings)
		msg := toolsLoadedMsg{
			tools:                  snapshot.Tools,
			discovered:             snapshot.Discovered,
			settings:               snapshot.Settings,
			taps:                   snapshot.Taps,
			groupNames:             groupNames,
			toolGroups:             toolGroups,
			toolMemberships:        toolMemberships,
			dotMemberships:         dotMemberships,
			ignoreLabels:           ignoreLabels,
			toolIgnoreSet:          toolIgnoreSet,
			groupIgnoreSet:         groupIgnoreSet,
			toolProviderPins:       toolProviderPins,
			hostInfo:               hostInfo,
			ignoreList:             ignoreList,
			dotsHistory:            snapshot.DotsHistory,
			dotsHistoryErr:         errorString(snapshot.DotsHistoryErr),
			noHost:                 noHost,
			effectivePythonManager: snapshot.EffectivePythonManager,
			effectiveNodeManager:   snapshot.EffectiveNodeManager,
			effectiveSystemManager: effectiveSystemManager,
			stowInstalled:          snapshot.StowInstalled,
			dotsReminderService:    snapshot.DotsReminderService,
			dotsReminderServiceErr: errorString(snapshot.DotsReminderServiceErr),
			dotsWatchService:       snapshot.DotsWatchService,
			dotsWatchServiceErr:    errorString(snapshot.DotsWatchServiceErr),
			bootstrapRequired:      snapshot.BootstrapRequired,
			allPythonManagers:      snapshot.AllPythonManagers,
			allNodeManagers:        snapshot.AllNodeManagers,
			setupProviders:         spRows,
			ecosystemProviders:     snapshot.EcosystemProviderNames,
			configuredProviders:    snapshot.ConfiguredProviders,
			providerToolCounts:     snapshot.ProviderToolCounts,
		}
		stop()
		return msg
	}
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

// buildToolGroups maps toolKey → group baseName for every tool in groups.
func buildToolGroups(groups []*config.GroupConfig) map[string]string {
	tg := make(map[string]string)
	for _, g := range groups {
		for _, t := range g.Tools {
			tg[toolKey(t.Name, t.Provider)] = g.BaseName()
		}
	}
	return tg
}

func compactToolGroupMapForHost(memberships map[string][]string, info *app.HostInfo) map[string]string {
	return compactToolGroupMapWithFilter(memberships, activeHostGroupSet(info))
}

func compactToolGroupMapWithFilter(memberships map[string][]string, allowed map[string]bool) map[string]string {
	out := make(map[string]string, len(memberships))
	for key, groups := range memberships {
		out[key] = compactGroupLabel(filterGroupsForHost(groups, allowed))
	}
	return out
}

func activeHostGroupSet(info *app.HostInfo) map[string]bool {
	if info == nil || info.Active == "" {
		return nil
	}
	host, ok := info.Hosts[info.Active]
	if !ok {
		return nil
	}
	allowed := make(map[string]bool, len(host.Groups))
	for _, group := range host.Groups {
		allowed[group] = true
	}
	// Machine groups are runtime-active for the current host but are not stored
	// in host.Groups. Keep them visible in the list/filter when configured.
	if host := shortHostname(); host != "" {
		allowed[host] = true
	}
	return allowed
}

func filterGroupsForHost(groups []string, allowed map[string]bool) []string {
	if allowed == nil {
		return groups
	}
	filtered := make([]string, 0, len(groups))
	for _, group := range groups {
		if allowed[group] {
			filtered = append(filtered, group)
		}
	}
	return filtered
}

func visibleGroupNames(m Model) []string {
	allowed := activeHostGroupSet(m.hostInfo)
	if allowed == nil {
		return m.groupNames
	}
	names := make([]string, 0, len(m.groupNames))
	for _, name := range m.groupNames {
		if allowed[name] {
			names = append(names, name)
		}
	}
	return names
}

func prioritizedPickerGroups(m Model) []string {
	return prioritizePickerGroupList(m, buildAllGroupNames(m.groupNames))
}

func prioritizePickerGroupList(m Model, groups []string) []string {
	allowed := pickerActiveHostGroupSet(m)
	if allowed == nil {
		return groups
	}
	active := make([]string, 0, len(groups))
	inactive := make([]string, 0, len(groups))
	for _, group := range groups {
		if allowed[group] {
			active = append(active, group)
		} else {
			inactive = append(inactive, group)
		}
	}
	return append(active, inactive...)
}

func groupInActiveHost(m Model, group string) bool {
	allowed := pickerActiveHostGroupSet(m)
	return allowed != nil && allowed[group]
}

func groupHasActiveHostContext(m Model) bool {
	return pickerActiveHostGroupSet(m) != nil
}

func pickerActiveHostGroupSet(m Model) map[string]bool {
	allowed := activeHostGroupSet(m.hostInfo)
	if len(m.pickerCreatedGroups) == 0 {
		return allowed
	}
	if allowed == nil {
		return nil
	}
	cp := make(map[string]bool, len(allowed)+len(m.pickerCreatedGroups))
	for group := range allowed {
		cp[group] = true
	}
	allowed = cp
	for _, group := range m.pickerCreatedGroups {
		allowed[group] = true
	}
	return allowed
}

func compactGroupLabel(groups []string) string {
	if len(groups) == 0 {
		return ""
	}
	cp := append([]string(nil), groups...)
	sort.Strings(cp)
	if len(cp) <= 2 {
		return strings.Join(cp, ",")
	}
	return cp[0] + "," + cp[1] + fmt.Sprintf("+%d", len(cp)-2)
}

func toolMembershipKey(t *database.ToolCache) string {
	if t == nil {
		return ""
	}
	return toolKey(t.Name, t.Provider)
}

func toolInGroup(m Model, t *database.ToolCache, group string) bool {
	for _, name := range m.toolMemberships[toolMembershipKey(t)] {
		if name == group {
			return true
		}
	}
	return false
}

func buildToolScopeState(configPath string, groups []*config.GroupConfig) (map[string]bool, map[string]map[string]bool, map[string]string) {
	var globalIgnoredTools []string
	toolIgnores := map[string]bool{}
	pins := make(map[string]string)
	if configPath != "" {
		if cfg, err := config.Load(configPath); err == nil {
			shortHost := shortHostname()
			globalIgnoredTools = append([]string(nil), cfg.Ignore.Tools...)
			for name, spec := range cfg.Tools {
				if spec.Ignore {
					toolIgnores[name] = true
				}
				if hostSpec, ok := spec.Hosts[shortHost]; ok {
					if hostSpec.InstallWith != "" {
						pins[name] = hostSpec.InstallWith
					}
				} else if spec.InstallWith != "" {
					pins[name] = spec.InstallWith
				}
			}
		}
	}
	return buildToolScopeStateFromState(globalIgnoredTools, toolIgnores, pins)
}

func buildIgnoreLabels(configPath string, groups []*config.GroupConfig, hostIgnore []string) map[string]string {
	var globalIgnoredTools []string
	toolIgnores := map[string]bool{}
	if configPath != "" {
		if cfg, err := config.Load(configPath); err == nil {
			globalIgnoredTools = append([]string(nil), cfg.Ignore.Tools...)
			for name, spec := range cfg.Tools {
				if spec.Ignore {
					toolIgnores[name] = true
				}
			}
		}
	}
	return buildIgnoreLabelsFromState(hostIgnore, globalIgnoredTools, toolIgnores)
}

func buildToolScopeStateFromState(globalIgnoredTools []string, toolIgnores map[string]bool, pins map[string]string) (map[string]bool, map[string]map[string]bool, map[string]string) {
	toolIgnoreCopy := make(map[string]bool, len(toolIgnores))
	for name, ignored := range toolIgnores {
		if ignored {
			toolIgnoreCopy[name] = true
		}
	}
	groupIgnores := make(map[string]map[string]bool)
	for _, name := range globalIgnoredTools {
		if name == "" {
			continue
		}
		if groupIgnores[name] == nil {
			groupIgnores[name] = make(map[string]bool)
		}
		groupIgnores[name]["global"] = true
	}
	pinCopy := make(map[string]string, len(pins))
	for name, pin := range pins {
		if pin != "" {
			pinCopy[name] = pin
		}
	}
	return toolIgnoreCopy, groupIgnores, pinCopy
}

func buildIgnoreLabelsFromState(hostIgnore, globalIgnoredTools []string, toolIgnores map[string]bool) map[string]string {
	labels := make(map[string]string)
	for _, name := range hostIgnore {
		if name != "" {
			labels[name] = "global"
		}
	}
	for _, name := range globalIgnoredTools {
		if name != "" {
			labels[name] = "global"
		}
	}
	for name, ignored := range toolIgnores {
		if ignored {
			labels[name] = "tool"
		}
	}
	return labels
}

// buildGroupNames returns an ordered slice of unique reusable group names.
// Host groups are excluded and added by buildAllGroupNames for host-local UI.
func buildGroupNames(groups []*config.GroupConfig) []string {
	var names []string
	seen := make(map[string]bool)
	for _, g := range groups {
		if g.IsHost() {
			continue
		}
		bn := g.BaseName()
		if bn == "" || seen[bn] {
			continue
		}
		seen[bn] = true
		names = append(names, bn)
	}
	sort.Strings(names)
	return names
}

// buildAllGroupNames returns the local host group plus reusable group names.
func buildAllGroupNames(groupNames []string) []string {
	names := make([]string, 0, len(groupNames)+1)
	host := shortHostname()
	if host == "" {
		host = "localhost"
	}
	seen := map[string]bool{host: true}
	names = append(names, host)
	reusable := append([]string(nil), groupNames...)
	sort.Strings(reusable)
	for _, name := range reusable {
		if seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

// displaySection returns the visual section for a tool, taking into account the
// active host's ignore list. Ignored tools always land in sectionIgnored.
func (m *Model) displaySection(t *database.ToolCache) section {
	if m.ignoreLabels[t.Name] != "" || m.ignoreSet[t.Name] {
		return sectionIgnored
	}
	// Updates take priority: an installed+outdated tool appears in Updates
	// regardless of its sync status (orphan, wrong-provider, etc.).
	if t.Installed && t.Outdated {
		return sectionUpdates
	}
	if m.discoveredKeys[toolKey(t.Name, t.Provider)] {
		return sectionOutOfSync // orphan: installed locally, not in config
	}
	// Config tool not installed → out of sync (missing locally).
	// t.Tracked distinguishes config tools from in-memory search results (Tracked=false).
	if t.Tracked && !t.Installed {
		return sectionOutOfSync
	}
	// Wrong concrete provider → out of sync.
	if m.syncStatusOf(t) == syncWrongProv {
		return sectionOutOfSync
	}
	return sectionOf(t)
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

// applyFilter filters allTools by the active group filter, provider subtab, and
// current search input (live local filter). Search results (searchTools) are
// merged in without text-filtering — they already matched the query.
func (m *Model) applyFilter() {
	if len(m.providerNames) == 0 {
		m.providerNames = provider.BuiltinEcosystemNames()
	}
	// Clamp providerTabIdx in case the tool list shrank.
	if m.providerTabIdx > len(m.providerNames) {
		m.providerTabIdx = 0
	}

	q := strings.ToLower(m.filter.Value())

	var targetProvider string
	if m.providerTabIdx > 0 && m.providerTabIdx <= len(m.providerNames) {
		targetProvider = m.providerNames[m.providerTabIdx-1]
	}

	normal := make([]*database.ToolCache, 0, len(m.allTools))
	ignored := make([]*database.ToolCache, 0, 4)
	for _, t := range m.allTools {
		// Group filter: only show tools that belong to the selected group.
		if m.groupFilter != "" {
			if !toolInGroup(*m, t, m.groupFilter) {
				continue
			}
		}
		if targetProvider != "" && providerEcosystem(t.Provider) != targetProvider {
			continue
		}
		if q != "" &&
			!strings.Contains(strings.ToLower(t.Name), q) &&
			!strings.Contains(strings.ToLower(t.Provider), q) {
			continue
		}
		if m.ignoreSet[t.Name] {
			ignored = append(ignored, t)
		} else {
			normal = append(normal, t)
		}
	}

	// Merge discovered tools into the visible list (when no group/provider filter).
	// discoveredKeys is maintained by rebuildDiscoveredKeys(), not rebuilt here.
	if len(m.discoveredTools) > 0 {
		// Build a set of config-tracked names to avoid duplicates.
		configNames := make(map[string]bool, len(m.allTools))
		for _, t := range m.allTools {
			configNames[t.Name] = true
		}
		for _, t := range m.discoveredTools {
			if configNames[t.Name] {
				continue // already shown under the config tool entry
			}
			if q != "" &&
				!strings.Contains(strings.ToLower(t.Name), q) &&
				!strings.Contains(strings.ToLower(t.Provider), q) {
				continue
			}
			// Only show orphans when no group/provider filter is active.
			if m.groupFilter == "" && targetProvider == "" {
				normal = append(normal, t)
			}
		}
	}

	// Merge search results — skip any already present in allTools or discoveredTools.
	// Search results appear before ignored so the section order is:
	// Updates → OutOfSync → Installed → Available → Ignored.
	if len(m.searchTools) > 0 {
		existing := make(map[string]bool, len(m.allTools))
		for _, t := range m.allTools {
			existing[toolKey(t.Name, t.Provider)] = true
		}
		for _, dk := range m.discoveredTools {
			existing[toolKey(dk.Name, dk.Provider)] = true
		}
		for _, t := range m.searchTools {
			if !existing[toolKey(t.Name, t.Provider)] {
				if targetProvider == "" || providerEcosystem(t.Provider) == targetProvider {
					normal = append(normal, t)
				}
			}
		}
	}

	m.visibleTools = append(normal, ignored...)
	sort.SliceStable(m.visibleTools, func(i, j int) bool {
		sectionI := m.displaySection(m.visibleTools[i])
		sectionJ := m.displaySection(m.visibleTools[j])
		if sectionI != sectionJ {
			return sectionI < sectionJ
		}
		nameI := strings.ToLower(m.visibleTools[i].Name)
		nameJ := strings.ToLower(m.visibleTools[j].Name)
		if nameI != nameJ {
			return nameI < nameJ
		}
		return strings.ToLower(m.visibleTools[i].Provider) < strings.ToLower(m.visibleTools[j].Provider)
	})
	m.clampToolCursor()

	// Cache section counts so View() can read m.sectionCounts[s] instead of
	// iterating visibleTools on every render frame (including during typing).
	counts := make(map[section]int, 5)
	for _, t := range m.visibleTools {
		counts[m.displaySection(t)]++
	}
	m.sectionCounts = counts
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

// sectionOf returns the display section for a cached tool.
// Does NOT account for orphan status or config-missing classification —
// callers with model access should use displaySection instead.
func sectionOf(t *database.ToolCache) section {
	if t.Installed && t.Outdated {
		return sectionUpdates
	}
	if t.Installed {
		return sectionInstalled
	}
	return sectionAvailable // not installed (e.g. search result)
}

// syncStatusOf returns the out-of-sync sub-category for a tool in sectionOutOfSync.
func (m *Model) syncStatusOf(t *database.ToolCache) syncStatus {
	if !t.Tracked {
		return syncOrphan
	}
	if !t.Installed {
		return syncMissing
	}
	if pin := m.toolProviderPins[t.Name]; pin != "" {
		if t.InstalledWith == pin {
			return syncOK
		}
		if t.InstalledWith != "" {
			return syncWrongProv
		}
	}
	// ⚠ InstalledWith doesn't match the current effective concrete for the ecosystem provider.
	// All three ecosystem providers are treated equally.
	if t.InstalledWith != "" {
		switch t.Provider {
		case provider.EcosystemSystem:
			if m.effectiveSystemManager != "" && t.InstalledWith != m.effectiveSystemManager {
				return syncWrongProv
			}
		case provider.EcosystemPython:
			if m.effectivePythonManager != "" && t.InstalledWith != m.effectivePythonManager {
				return syncWrongProv
			}
		case provider.EcosystemNode:
			if m.effectiveNodeManager != "" && t.InstalledWith != m.effectiveNodeManager {
				return syncWrongProv
			}
		}
	}
	return syncOK
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

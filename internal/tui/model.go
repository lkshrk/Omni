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
	"github.com/lkshrk/omni/internal/provider"
)

// viewMode is the active top-level view.
type viewMode int

const (
	viewList              viewMode = iota
	viewSearch                     // search input active
	viewSettings                   // options/settings tab
	viewSetup                      // first-run: no settings.json found
	viewCommand                    // command palette active
	viewProfiles                   // dedicated profiles tab
	viewGroupPicker                // inline move-to-group picker
	viewGroupMembership            // logical tool group membership toggles
	viewProfileGroupTools          // profile group tool membership/ignore editor
	viewProfileGroupDots           // profile group dotfile membership editor
	viewIgnoreScope                // explicit ignore scope picker
	viewProviderScope              // explicit provider pin scope picker
	viewDots                       // dotfiles management tab
)

// section groups tools into visual categories in the list view.
type section int

const (
	sectionUpdates   section = iota // 0 - tools with updates available
	sectionOutOfSync                // 1 - config-missing / orphan / wrong-provider
	sectionInstalled                // 2 - installed, up-to-date
	sectionAvailable                // 3 - available to install (declared in config or found via search)
	sectionIgnored                  // 4 - in the active profile's ignore list (rendered last, dimmed)
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
)

type profileGroupToolSection int

const (
	profileGroupToolSectionEnabled profileGroupToolSection = iota
	profileGroupToolSectionDisabled
	profileGroupToolSectionIgnored
)

type profileGroupToolRow struct {
	tool        *database.ToolCache
	section     profileGroupToolSection
	enabled     bool
	groupIgnore bool
	toolIgnore  bool
}

type profileGroupDotSection int

const (
	profileGroupDotSectionEnabled profileGroupDotSection = iota
	profileGroupDotSectionDisabled
	profileGroupDotSectionIgnored
)

type profileGroupDotRow struct {
	name    string
	target  string
	section profileGroupDotSection
	enabled bool
	ignored bool
}

type profileGroupAssignmentEditor struct {
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
	confirmQuit         bool              // true after first quit key; second press exits
	quitConfirmKey      string            // key used to arm confirmQuit ("q" or "ctrl+c")
	confirmGen          int               // increments whenever a timed confirmation is armed
	upgradingKeys       map[string]bool   // set of in-flight upgrade keys ("name\x00provider" or "*")
	bulkPendingKeys     map[string]bool   // tool keys waiting for their turn in a bulk operation
	rowOpKey            string            // selected row operation key ("name\x00provider") for install/delete/reinstall work
	rowOpStatus         string            // inline status shown where row action hints normally render
	rowErrors           map[string]string // tool key -> last failed row action message; survives filtering/search
	listConfirm         listConfirmation
	suppressFooterHints bool
	statusMsg           string
	statusIsErr         bool // true when statusMsg is an error (shown red, 2× duration)
	statusGen           int  // incremented on each setStatus call; stale clearStatusMsg events are dropped
	err                 error

	settingsSaveRunning        bool
	settingsSaveQueued         bool
	settingsSaveQueuedSnapshot config.Settings
	settingsSaveQueuedGen      int
	settingsSaveGen            int
	settingsSaveInFlightGen    int

	// scanningProviders holds the names of providers whose parallel scan goroutines
	// are still in flight. The spinner is shown while the set is non-empty; each
	// providerScannedMsg removes one entry. Initialized to the set of unique
	// provider names from allTools on every scan kick-off.
	scanningProviders map[string]bool
	scanGen           int
	discoveryGen      int
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
	groupNames          []string          // ordered non-base group names (excludes "base")
	toolGroups          map[string]string // "name\x00provider" → baseName
	toolMemberships     map[string][]string
	ignoreLabels        map[string]string // logical tool name → compact ignore source label
	toolIgnoreSet       map[string]bool
	groupIgnoreSet      map[string]map[string]bool
	toolProviderPins    map[string]string
	configuredProviders []string

	// provider filter — [All] [system] [node] [python] …
	providerNames  []string // ordered ecosystem provider names from the app/provider registry
	providerTabIdx int      // 0=All, 1+=providerNames[idx-1]

	// effective package-manager binaries resolved at load time via PATH probing.
	effectivePythonManager string // e.g. "uv", "pip3"
	effectiveNodeManager   string // e.g. "bun", "pnpm", "npm"
	effectiveSystemManager string // e.g. "brew", "apt" — concrete PM backing the system ecosystem provider

	// profiles tab
	profileInfo   *app.ProfileInfo
	profileCursor int
	ignoreSet     map[string]bool // tool names ignored by the active profile
	groupFilter   string          // non-empty: only show tools belonging to this group
	groupTabIdx   int             // 0=all, 1=base, 2+=named groups; mirrors groupFilter for pill navigation

	// profile group editing (inline picker in profiles tab)
	profileEditMode       int               // 0=none, 1=editGroups, 2=editHosts
	profileGroupPicker    []string          // groups shown in the profile group picker
	profileGroupIdx       int               // cursor in the profile group picker
	profileGroupDraft     []string          // staged group memberships for selected profile
	profileOriginalGroups []string          // group memberships before staged edit
	profileHostPicker     []string          // hosts shown in the profile host picker
	profileHostIdx        int               // cursor in the profile host picker
	profileHostDraft      map[string]string // staged hostname → profile mapping; empty value means unmapped
	profileHostOriginal   map[string]string // hostname mappings before staged edit
	profileEditName       string            // profile captured when group/host editor was opened
	profileDeleteConfirm  bool              // true when awaiting second Enter to confirm delete
	profileDeleteName     string            // profile captured when delete confirmation was armed
	profileCreating       bool              // true when the shared new-profile popup is open
	profileRenameMode     bool              // true when the inline profile rename text input is open
	profileRenameName     string            // profile captured when inline rename was opened

	// profiles tab section focus
	// 0 = profiles list, 1 = groups list
	profileSection     int
	groupCursor        int  // cursor within the allGroupNames list
	groupDeleteConfirm bool // true when awaiting second Enter to confirm group delete
	groupDeleteName    string
	groupDeleteChoice  int  // 0=move last-membership tools to base, 1=delete last-membership specs
	groupRenameMode    bool // true when inline rename text input is open
	groupRenameName    string
	groupCreating      bool // true when the shared new-group popup is open

	// profile group tools editor popup
	groupToolsEditor         profileGroupAssignmentEditor
	groupToolsProviderIdx    int
	groupToolsIgnore         map[string]bool // logical tool name -> staged group-level ignore in groupToolsEditor.group
	groupToolsOriginalIgnore map[string]bool // logical tool name -> original group-level ignore in groupToolsEditor.group

	// profile group dotfiles editor popup
	groupDotsEditor profileGroupAssignmentEditor

	// inline group-picker
	pickerGroups         []string
	pickerCursor         int
	pickerCreatingGroup  bool // true when the user selected the "+ new group…" sentinel
	pickerPurposeClaim   bool // true when the picker is for claiming an orphan tool
	pickerPurposeInstall bool // true when the picker is for install-and-add
	pickerMembershipKind string
	pickerMembershipName string
	pickerMembershipKey  string
	pickerActionTool     database.ToolCache
	pickerActionToolSet  bool
	pickerOriginalGroups []string
	pickerCreatedGroups  []string
	scopeOptions         []scopeOption
	scopeCursor          int
	scopeTarget          database.ToolCache
	scopeTargetSet       bool

	// setup wizard step (0 = create config?, 1 = import tools?, 2 = provider selection, 3 = node manager, 4 = profile name, 5 = enable dotfiles?, 6 = dots repo path)
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
	// setupExitConfirm is true when the user tried to skip profile creation in
	// setup step 4 and is shown the "exit omni?" confirmation prompt.
	setupExitConfirm bool

	// profileRequired is true when the config exists but no profile is mapped
	// to this machine. All navigation is locked until a profile is active.
	profileRequired bool

	// provider priority editor (active when editing the Priority row in settings)
	editingPriority bool
	priorityCursor  int
	priorityDraft   []string

	// file picker popup (reusable for any path selection)
	dotsFilePicker       pathPickerModel
	showFilePicker       bool
	filePickerTitle      string
	filePickerAllowFiles bool
	settingsInput        textinput.Model // still used for profile name, group rename

	// dots tab
	dotsEntries         []app.DotStatus
	dotsGitStatus       string
	dotMemberships      map[string][]string
	dotsCursor          int
	dotsExpandedName    string
	dotsLoading         bool
	dotsLoaded          bool // true after first lazy load
	dotsOpGen           int  // increments for each async dots operation; stale results are dropped
	dotsCtx             context.Context
	dotsCancel          context.CancelFunc
	dotsConfirmIdx      int    // index of entry pending delete confirm; -1 = none
	dotsOverwriteIdx    int    // index of conflict entry pending use-repo confirm; -1 = none
	dotsLocalIdx        int    // index of conflict entry pending use-local confirm; -1 = none
	dotsIgnoreIdx       int    // index of child path pending ignore/include confirm; -1 = none
	dotsGroupFilter     string // "" = all groups; "config"/"home"/"custom"/etc = filtered
	dotsSearchActive    bool   // true when dots search bar is open
	filePickerForDotAdd bool   // true when file picker opened for "add path" on dots tab
	stowInstalled       bool
	stowInstallPrompt   bool
	stowInstallAction   stowInstallAction
	stowInstallSettings config.Settings
	stowInstallPath     string

	// danger zone (settings tab)
	dangerConfirmRow int // settings row awaiting inline confirmation; -1 = none

	width  int
	height int

	// palette holds all colours and pre-built lipgloss styles for the active
	// terminal theme. Initialised to the dark default in New(); rebuilt on
	// the first tea.BackgroundColorMsg via applyTheme. Each Model owns its
	// own palette so parallel test instances do not share mutable state.
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
// setup wizard (noProfile=true path) does not re-enable providers the user has
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
		commandCursor:    -1,
		loading:          true,
		cursor:           -1, // nothing selected until the user navigates
		upgradingKeys:    make(map[string]bool),
		searchCache:      make(map[string]searchCacheEntry),
		dotsConfirmIdx:   -1,
		dotsOverwriteIdx: -1,
		dotsLocalIdx:     -1,
		dotsIgnoreIdx:    -1,
		dangerConfirmRow: -1,
		isDark:           true,             // assume dark until terminal replies
		focused:          true,             // assume focused until a BlurMsg says otherwise
		palette:          defaultPalette(), // dark default; rebuilt on BackgroundColorMsg
	}
}

func (m *Model) shutdown() {
	m.cancelSearch()
	m.cancelDotsOperation()
	if m.cancel != nil {
		m.cancel()
	}
	m.loading = false
	m.dotsLoading = false
	m.searching = false
	m.scanningProviders = nil
	m.upgradingKeys = make(map[string]bool)
}

// Init kicks off the initial data load.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		loadTools(m.app, m.ctx),
		tea.RequestBackgroundColor,
	)
}

// loadTools fetches tools, settings, taps, groups, and profiles from the DB
// cache without probing providers. Returns immediately so the list renders
// on the first frame. A background doRefreshInstalled cmd updates install
// status afterwards.
func loadTools(a *app.App, ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		if !a.HasConfig() {
			return toolsLoadedMsg{noConfig: true}
		}
		tools, err := a.ListTools(ctx, "")
		if err != nil {
			return toolsLoadedMsg{err: err}
		}
		settings, _ := a.LoadSettings()
		taps, _ := a.LoadTaps()
		profileInfo, _ := a.ProfileStatus()
		noProfile := profileInfo != nil && profileInfo.Active == ""
		var activeProfile string
		var profileIgnore []string
		if profileInfo != nil && profileInfo.Active != "" {
			activeProfile = profileInfo.Active
			if prof, ok := profileInfo.Profiles[activeProfile]; ok {
				profileIgnore = prof.Ignore
			}
		}
		groups, _ := a.Groups(ctx)
		toolMemberships, _ := a.ToolMembershipMap(ctx)
		dotMemberships, _ := a.DotMembershipMap(ctx)
		toolGroups := compactToolGroupMapForProfile(toolMemberships, profileInfo)
		groupNames := buildGroupNames(groups)
		ignoreLabels := buildIgnoreLabels(a.ConfigPath, groups, profileIgnore)
		toolIgnoreSet, groupIgnoreSet, toolProviderPins := buildToolScopeState(a.ConfigPath, groups)
		ignoreList := make([]string, 0, len(ignoreLabels))
		for name := range ignoreLabels {
			ignoreList = append(ignoreList, name)
		}
		pythonBin, nodeBin := a.EffectiveManagers()
		allPyBins, allNodeBins := a.AllAvailableManagers()
		ecosystemMap := a.ResolvedEcosystemProviders(ctx)
		effectiveSystemManager := ecosystemMap[provider.EcosystemSystem]
		stowInstalled := a.DotsStowInstalled(ctx)
		discovered, _ := a.ListDiscovered(ctx)
		// Build setup provider rows from already-fetched manager data — no extra calls needed.
		spRows := buildSetupProvidersFromManagers(ecosystemMap, allPyBins, allNodeBins, settings)
		// Collect unique provider names from config groups so the toolsLoadedMsg
		// handler can launch scan goroutines even when the DB is empty (e.g. after
		// a fresh import where Import() only writes to config, not the DB).
		ecosystemProviders := a.EcosystemProviderNames()
		configuredProviders, _ := a.ConfiguredProviders(ctx)
		return toolsLoadedMsg{
			tools:                  tools,
			discovered:             discovered,
			settings:               settings,
			taps:                   taps,
			groupNames:             groupNames,
			toolGroups:             toolGroups,
			toolMemberships:        toolMemberships,
			dotMemberships:         dotMemberships,
			ignoreLabels:           ignoreLabels,
			toolIgnoreSet:          toolIgnoreSet,
			groupIgnoreSet:         groupIgnoreSet,
			toolProviderPins:       toolProviderPins,
			profileInfo:            profileInfo,
			ignoreList:             ignoreList,
			noProfile:              noProfile,
			effectivePythonManager: pythonBin,
			effectiveNodeManager:   nodeBin,
			effectiveSystemManager: effectiveSystemManager,
			stowInstalled:          stowInstalled,
			allPythonManagers:      allPyBins,
			allNodeManagers:        allNodeBins,
			setupProviders:         spRows,
			ecosystemProviders:     ecosystemProviders,
			configuredProviders:    configuredProviders,
		}
	}
}

// toolKey returns the composite key used to identify a tool in maps.
func toolKey(name, provider string) string {
	return name + "\x00" + provider
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

func compactToolGroupMap(memberships map[string][]string) map[string]string {
	return compactToolGroupMapWithFilter(memberships, nil)
}

func compactToolGroupMapForProfile(memberships map[string][]string, info *app.ProfileInfo) map[string]string {
	return compactToolGroupMapWithFilter(memberships, activeProfileGroupSet(info))
}

func compactToolGroupMapWithFilter(memberships map[string][]string, allowed map[string]bool) map[string]string {
	out := make(map[string]string, len(memberships))
	for key, groups := range memberships {
		out[key] = compactGroupLabel(filterGroupsForProfile(groups, allowed))
	}
	return out
}

func activeProfileGroupSet(info *app.ProfileInfo) map[string]bool {
	if info == nil || info.Active == "" {
		return nil
	}
	profile, ok := info.Profiles[info.Active]
	if !ok {
		return nil
	}
	allowed := make(map[string]bool, len(profile.Groups))
	for _, group := range profile.Groups {
		allowed[group] = true
	}
	// Machine groups are runtime-active for the current host but are not stored
	// in profile.Groups. Keep them visible in the list/filter when configured.
	if host := shortHostname(); host != "" {
		allowed[host] = true
	}
	return allowed
}

func filterGroupsForProfile(groups []string, allowed map[string]bool) []string {
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
	allowed := activeProfileGroupSet(m.profileInfo)
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
	allowed := pickerActiveGroupSet(m)
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

func groupInActiveProfile(m Model, group string) bool {
	allowed := pickerActiveGroupSet(m)
	return allowed != nil && allowed[group]
}

func groupHasActiveProfileContext(m Model) bool {
	return pickerActiveGroupSet(m) != nil
}

func pickerActiveGroupSet(m Model) map[string]bool {
	allowed := activeProfileGroupSet(m.profileInfo)
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
	toolIgnores := make(map[string]bool)
	groupIgnores := make(map[string]map[string]bool)
	for _, g := range groups {
		if g == nil {
			continue
		}
		groupName := g.BaseName()
		for _, name := range g.Ignore {
			if groupIgnores[name] == nil {
				groupIgnores[name] = make(map[string]bool)
			}
			groupIgnores[name][groupName] = true
		}
	}
	pins := make(map[string]string)
	if configPath != "" {
		if cfg, err := config.Load(configPath); err == nil {
			shortHost := shortHostname()
			for name, spec := range cfg.Tools {
				if spec.Ignore {
					toolIgnores[name] = true
				}
				if hostSpec, ok := spec.Hosts[shortHost]; ok && hostSpec.InstallWith != "" {
					pins[name] = hostSpec.InstallWith
				} else if spec.InstallWith != "" {
					pins[name] = spec.InstallWith
				}
			}
		}
	}
	return toolIgnores, groupIgnores, pins
}

func buildIgnoreLabels(configPath string, groups []*config.GroupConfig, profileIgnore []string) map[string]string {
	labels := make(map[string]string)
	for _, name := range profileIgnore {
		if name != "" {
			labels[name] = "profile"
		}
	}
	groupSources := make(map[string][]string)
	for _, g := range groups {
		if g == nil {
			continue
		}
		groupName := g.BaseName()
		for _, name := range g.Ignore {
			if name == "" {
				continue
			}
			groupSources[name] = append(groupSources[name], groupName)
		}
	}
	for name, sources := range groupSources {
		if len(sources) == 1 {
			labels[name] = sources[0]
		} else {
			labels[name] = sources[0] + fmt.Sprintf("+%d", len(sources)-1)
		}
	}
	if configPath != "" {
		if cfg, err := config.Load(configPath); err == nil {
			for name, spec := range cfg.Tools {
				if spec.Ignore {
					labels[name] = "tool"
				}
			}
		}
	}
	return labels
}

// buildGroupNames returns an ordered slice of unique non-base group names.
// "base" group is excluded — the [All] tab covers it implicitly.
func buildGroupNames(groups []*config.GroupConfig) []string {
	var names []string
	seen := make(map[string]bool)
	for _, g := range groups {
		bn := g.BaseName()
		if bn == "base" || seen[bn] {
			continue
		}
		seen[bn] = true
		names = append(names, bn)
	}
	sort.Strings(names)
	return names
}

// buildAllGroupNames returns all configured group filter names, including base,
// in display order.
func buildAllGroupNames(groupNames []string) []string {
	names := make([]string, 0, len(groupNames)+1)
	seen := map[string]bool{"base": true}
	names = append(names, "base")
	for _, name := range groupNames {
		if seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// displaySection returns the visual section for a tool, taking into account the
// active profile's ignore list. Ignored tools always land in sectionIgnored.
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
// allGroups slice (["base", "work", ...]).  groupTabIdx 0 means "all" → clears
// the filter; any other index selects the corresponding group name.
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
	if pin := m.toolProviderPins[t.Name]; pin != "" && t.InstalledWith == pin {
		return syncOK
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

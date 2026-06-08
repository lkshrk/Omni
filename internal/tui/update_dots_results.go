package tui

import (
	"context"
	"fmt"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/dots"
)

func (m *Model) handleDotsLoadedMsg(msg dotsLoadedMsg) []tea.Cmd {
	var cmds []tea.Cmd

	if !m.finishDotsOperation(msg.gen) {
		return cmds
	}
	m.dotsLoaded = true
	if msg.entries != nil {
		m.applyDotsSnapshot(msg.entries, msg.gitStatus, msg.dotMemberships)
	}
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
	} else if msg.detail != "" {
		cmds = append(cmds, setStatus(m, msg.detail, false))
	}
	m.refreshDotsHistory(&cmds)
	return cmds
}

func (m *Model) handleDotsPeekLoadedMsg(msg dotsPeekLoadedMsg) []tea.Cmd {
	if msg.gen != m.dotsPeekGen || !m.dotsPeekLoading {
		return nil
	}
	m.dotsPeekLoading = false
	if msg.err != nil {
		return []tea.Cmd{setStatus(m, "✗ "+msg.err.Error(), true)}
	}
	m.dotsPeek = &dotsPeekState{result: msg.result}
	return nil
}

func (m *Model) handleDotsPreparedMsg(msg dotsPreparedMsg) []tea.Cmd {
	var cmds []tea.Cmd

	if msg.gen != m.dotsPrepareGen || !m.dotsPreparing {
		return cmds
	}
	m.dotsPreparing = false
	if msg.opGen != m.dotsOpGen && !m.dotsLoading {
		return cmds
	}
	if msg.entries != nil {
		m.dotsLoaded = true
		m.applyDotsSnapshot(msg.entries, msg.gitStatus, msg.dotMemberships)
	}
	if msg.err != nil && msg.entries == nil && m.mode == viewDots && !m.dotsLoading && !m.stowInstallPrompt {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
	}
	return cmds
}

func (m *Model) handleDotsSyncedMsg(msg dotsSyncedMsg) []tea.Cmd {
	var cmds []tea.Cmd

	if !m.finishDotsOperation(msg.gen) {
		return cmds
	}
	m.dotsLoaded = true
	// Always update entries when available — health must reflect conflict state
	// even when stow partially failed.
	if msg.entries != nil {
		m.applyDotsSnapshot(msg.entries, msg.gitStatus, msg.dotMemberships)
	}
	if msg.hasSettings {
		m.setSettings(msg.settings)
	}
	if msg.err != nil {
		if !m.collectLaunchBatchError(msg.err.Error()) {
			cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
		}
	} else {
		if !m.launchBatchActive {
			cmds = append(cmds, setStatus(m, "✓ dots synced", false))
		}
	}
	if cmd := m.finishLaunchBatchIfIdle(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	m.continueDashboardReconcile(dashboardReconcilePlanSyncDots, msg.err, &cmds)
	if !m.dashboardReconcileRunning && !m.launchBatchActive && m.mode == viewStatus {
		m.refreshDoctorAfterFix(&cmds)
	}
	m.refreshDotsHistory(&cmds)
	return cmds
}

func (m *Model) handleDotsProgressMsg(msg dotsProgressMsg) []tea.Cmd {
	if msg.gen != m.dotsOpGen || !m.dotsLoading {
		return nil
	}
	if msg.text != "" {
		m.progressText = msg.text
	}
	if msg.name != "" {
		if msg.done {
			delete(m.dotsPendingNames, msg.name)
			if m.dotsActiveName == msg.name {
				m.dotsActiveName = ""
			}
		} else {
			delete(m.dotsPendingNames, msg.name)
			m.dotsActiveName = msg.name
		}
	}
	if msg.entries != nil {
		m.applyDotsSnapshot(msg.entries, msg.gitStatus, msg.dotMemberships)
	}
	if m.dotsProgressCh != nil {
		return []tea.Cmd{waitForDotsProgress(m.dotsProgressCh, m.dotsOpGen)}
	}
	return nil
}

func (m *Model) handleDotsProgressStreamClosedMsg(msg dotsProgressStreamClosedMsg) {
	if msg.gen != m.dotsOpGen {
		return
	}
	m.dotsProgressCh = nil
}

func (m *Model) handleDotsDiscoveredMsg(msg dotsDiscoveredMsg) []tea.Cmd {
	var cmds []tea.Cmd

	if !m.finishDotsOperation(msg.gen) {
		return cmds
	}
	m.dotsLoaded = true
	if msg.discoveredCount > 0 {
		m.clearDotsFilters()
	}
	if msg.entries != nil {
		m.applyDotsSnapshot(msg.entries, msg.gitStatus, msg.dotMemberships)
	}
	if msg.discoveredCount > 0 {
		m.selectFirstDiscoveredDotCandidate()
	}
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
		return cmds
	}
	count := msg.discoveredCount
	if count == 0 {
		cmds = append(cmds, setStatus(m, "✓ no untracked dotfile candidates", false))
		return cmds
	}
	cmds = append(cmds, setStatus(m, fmt.Sprintf("✓ discovered %s", compactCount(count, "candidate")), false))
	return cmds
}

func (m *Model) clearDotsFilters() {
	m.dotsSearchActive = false
	m.filter.SetValue("")
	m.filter.Blur()
	m.dotsCursor = 0
	m.dotsExpandedName = ""
	m.clearDotsExpandedChildren("")
}

func (m *Model) selectFirstDiscoveredDotCandidate() {
	for i, row := range dotsVisibleRows(*m) {
		if row.isChild || !app.DotStatusTransientCandidate(row.entry) {
			continue
		}
		m.dotsCursor = i
		m.dotsExpandedName = ""
		m.clearDotsExpandedChildren("")
		return
	}
}

func (m *Model) handleDotsPulledMsg(msg dotsPulledMsg) []tea.Cmd {
	var cmds []tea.Cmd

	if !m.finishDotsOperation(msg.gen) {
		return cmds
	}
	// Always update entries when available — conflicts from the pull must be visible.
	if msg.entries != nil {
		m.applyDotsSnapshot(msg.entries, msg.gitStatus, msg.dotMemberships)
	}
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
	} else {
		cmds = append(cmds, setStatus(m, "✓ pulled", false))
	}
	m.refreshDotsHistory(&cmds)
	return cmds
}

func (m *Model) handleDotsPushedMsg(msg dotsPushedMsg) []tea.Cmd {
	var cmds []tea.Cmd

	if !m.finishDotsOperation(msg.gen) {
		return cmds
	}
	if msg.entries != nil {
		m.applyDotsSnapshot(msg.entries, msg.gitStatus, msg.dotMemberships)
	}
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
	} else {
		cmds = append(cmds, setStatus(m, "✓ pushed", false))
	}
	m.refreshDotsHistory(&cmds)
	return cmds
}

func (m *Model) handleDotsCommittedMsg(msg dotsCommittedMsg) []tea.Cmd {
	var cmds []tea.Cmd

	if !m.finishDotsOperation(msg.gen) {
		return cmds
	}
	if msg.entries != nil {
		m.applyDotsSnapshot(msg.entries, msg.gitStatus, msg.dotMemberships)
	}
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
	} else {
		cmds = append(cmds, setStatus(m, "✓ committed", false))
	}
	m.continueDashboardReconcile(dashboardReconcilePlanCommitDots, msg.err, &cmds)
	if !m.dashboardReconcileRunning && m.mode == viewStatus {
		m.refreshDoctorAfterFix(&cmds)
	}
	m.refreshDotsHistory(&cmds)
	return cmds
}

func (m *Model) handleDotsDeletedMsg(msg dotsDeletedMsg) []tea.Cmd {
	var cmds []tea.Cmd

	if !m.finishDotsOperation(msg.gen) {
		return cmds
	}
	m.dotsConfirmIdx = -1
	m.dotsOverwriteIdx = -1
	m.dotsLocalIdx = -1
	m.dotsIgnoreIdx = -1
	m.dotsVariantIdx = -1
	m.dotsVariantMode = dotsVariantNone
	if msg.entries != nil {
		m.applyDotsSnapshot(msg.entries, msg.gitStatus, msg.dotMemberships)
	}
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
	} else {
		cmds = append(cmds, setStatus(m, "✓ deleted "+msg.name, false))
	}
	m.refreshDotsHistory(&cmds)
	return cmds
}

func (m *Model) handleDotsFixedMsg(msg dotsFixedMsg) []tea.Cmd {
	var cmds []tea.Cmd

	if !m.finishDotsOperation(msg.gen) {
		return cmds
	}
	m.dotsOverwriteIdx = -1
	m.dotsLocalIdx = -1
	m.dotsIgnoreIdx = -1
	if msg.entries != nil {
		m.applyDotsSnapshot(msg.entries, msg.gitStatus, msg.dotMemberships)
	}
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
	} else {
		cmds = append(cmds, setStatus(m, "✓ resolved "+msg.name, false))
	}
	m.refreshDotsHistory(&cmds)
	return cmds
}

func (m *Model) handleDotsAddedMsg(msg dotsAddedMsg) []tea.Cmd {
	var cmds []tea.Cmd

	if !m.finishDotsOperation(msg.gen) {
		return cmds
	}
	if msg.entries != nil {
		m.applyDotsSnapshot(msg.entries, msg.gitStatus, msg.dotMemberships)
	}
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
	} else {
		addedName := filepath.Base(msg.path)
		for i, e := range filteredDotsEntries(*m) {
			if e.Name == addedName {
				m.dotsCursor = i
				break
			}
		}
		cmds = append(cmds, setStatus(m, "✓ added "+msg.path, false))
	}
	m.refreshDotsHistory(&cmds)
	return cmds
}

func (m *Model) handleDotsVariantChangedMsg(msg dotsVariantChangedMsg) []tea.Cmd {
	var cmds []tea.Cmd

	if !m.finishDotsOperation(msg.gen) {
		return cmds
	}
	m.dotsVariantIdx = -1
	m.dotsVariantMode = dotsVariantNone
	if msg.entries != nil {
		m.applyDotsSnapshot(msg.entries, msg.gitStatus, msg.dotMemberships)
	}
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
		return cmds
	}
	pkg := msg.info.Package
	if pkg == "" {
		pkg = msg.name
	}
	if msg.removed {
		cmds = append(cmds, setStatus(m, "✓ removed variant "+pkg+" for "+msg.name, false))
	} else {
		cmds = append(cmds, setStatus(m, "✓ created variant "+pkg+" for "+msg.name, false))
	}
	m.refreshDotsHistory(&cmds)
	return cmds
}

func (m *Model) applyDotsSnapshot(entries []app.DotStatus, gitStatus string, memberships map[string][]string) {
	m.dotsConfirmIdx = -1
	m.dotsOverwriteIdx = -1
	m.dotsLocalIdx = -1
	m.dotsIgnoreIdx = -1
	m.dotsVariantIdx = -1
	m.dotsVariantMode = dotsVariantNone
	app.SortDotStatuses(entries)
	m.dotsEntries = entries
	m.dotsGitStatus = gitStatus
	if memberships != nil {
		m.dotMemberships = memberships
	}
	m.syncDotsExpandedName(dotsVisibleRows(*m))
}

func (m *Model) prepareDotsSnapshotOnLaunch(cmds *[]tea.Cmd) {
	if !m.dotsConfigured() || m.dotsLoaded || m.dotsPreparing {
		return
	}
	m.dotsPreparing = true
	m.dotsPrepareGen++
	*cmds = append(*cmds, m.doPrepareDotsSnapshot(m.dotsPrepareGen, m.dotsOpGen))
}

// doLoadDots fetches dots status (entries + git status) for the Dots tab.
func (m *Model) doLoadDots() tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	return func() tea.Msg {
		result, err := a.RefreshDotsState(ctx)
		if err != nil {
			if result != nil {
				return dotsLoadedMsg{gen: gen, entries: result.Entries, gitStatus: result.GitStatus, dotMemberships: result.DotMemberships, err: err}
			}
			return dotsLoadedMsg{gen: gen, err: err}
		}
		return dotsLoadedMsg{gen: gen, entries: result.Entries, gitStatus: result.GitStatus, dotMemberships: result.DotMemberships}
	}
}

func (m *Model) doPrepareDotsSnapshot(gen, opGen int) tea.Cmd {
	a := m.app
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		entries, gitStatus, memberships, err := refreshDotsSnapshot(a, ctx)
		return dotsPreparedMsg{gen: gen, opGen: opGen, entries: entries, gitStatus: gitStatus, dotMemberships: memberships, err: err}
	}
}

// doDotsSyncOnly repairs symlinks for all dots entries without a git pull.
func (m *Model) doDotsSyncOnly() tea.Cmd {
	return m.doDotsSyncOnlyWithProgress(nil, nil)
}

func (m *Model) doDotsSyncOnlyWithProgress(progressCh chan dotsProgressUpdate, entryOrder []string) tea.Cmd {
	return m.doDotsSyncOnlyWithOptions(progressCh, entryOrder, false)
}

func (m *Model) doLaunchDotsSyncOnly() tea.Cmd {
	return m.doDotsSyncOnlyWithOptions(nil, nil, true)
}

func (m *Model) doDotsSyncOnlyWithOptions(progressCh chan dotsProgressUpdate, entryOrder []string, suppressUnchangedHistory bool) tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	return func() tea.Msg {
		if progressCh != nil {
			defer close(progressCh)
		}
		opts := dots.SyncOptions{
			EntryOrder:               append([]string(nil), entryOrder...),
			SuppressUnchangedHistory: suppressUnchangedHistory,
		}
		var progress func(app.DotsOperationStateProgressEvent)
		if progressCh != nil {
			progress = func(event app.DotsOperationStateProgressEvent) {
				update := dotsProgressUpdate{
					gen:  gen,
					text: event.Text,
					name: event.Entry,
					done: event.Done,
				}
				if event.Done && event.State != nil {
					update.entries, update.gitStatus, update.dotMemberships = dotsSnapshot(event.State)
				}
				sendDotsProgressUpdate(progressCh, update)
			}
		}
		result, err := a.DotsSyncContextWithStateProgress(ctx, opts, progress)
		entries, gitStatus, memberships := dotsSnapshotFromState(result)
		return dotsSyncedMsg{gen: gen, entries: entries, gitStatus: gitStatus, dotMemberships: memberships, err: err}
	}
}

func (m *Model) doDotsSyncEntry(name string) tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	return func() tea.Msg {
		result, err := a.DotsSyncEntryWithState(ctx, name, dots.SyncOptions{})
		entries, gitStatus, memberships := dotsSnapshotFromState(result)
		return dotsSyncedMsg{gen: gen, entries: entries, gitStatus: gitStatus, dotMemberships: memberships, err: err}
	}
}

func (m *Model) doDotsSyncDiscovered(status app.DotStatus) tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	return func() tea.Msg {
		result, err := a.DotsSyncDiscoveredWithState(ctx, status.Name, status.Group)
		entries, gitStatus, memberships := dotsSnapshotFromDiscoveredState(result)
		return dotsSyncedMsg{gen: gen, entries: entries, gitStatus: gitStatus, dotMemberships: memberships, err: err}
	}
}

// doDotsRefresh re-scans dot entries, refreshes git status, and discovers
// untracked candidates without mutating config, the repo, or local files.
func (m *Model) doDotsRefresh() tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	return func() tea.Msg {
		result, err := a.RefreshDotsState(ctx)
		if result != nil {
			return dotsDiscoveredMsg{
				gen:             gen,
				entries:         result.Entries,
				gitStatus:       result.GitStatus,
				dotMemberships:  result.DotMemberships,
				discoveredCount: result.DiscoveredCount,
				err:             err,
			}
		}
		return dotsDiscoveredMsg{gen: gen, err: err}
	}
}

func refreshDotsSnapshot(a *app.App, ctx context.Context) ([]app.DotStatus, string, map[string][]string, error) {
	result, err := a.RefreshDotsState(ctx)
	if result == nil {
		return nil, "", nil, err
	}
	return result.Entries, result.GitStatus, result.DotMemberships, err
}

func dotsSnapshotFromState(result *app.DotsOperationStateResult) ([]app.DotStatus, string, map[string][]string) {
	if result == nil || result.State == nil {
		return nil, "", nil
	}
	return dotsSnapshot(result.State)
}

func dotsSnapshotFromDiscoveredState(result *app.DotsDiscoveredOperationStateResult) ([]app.DotStatus, string, map[string][]string) {
	if result == nil || result.State == nil {
		return nil, "", nil
	}
	return dotsSnapshot(result.State)
}

func dotsSnapshotFromVariantState(result *app.DotsVariantStateResult) ([]app.DotStatus, string, map[string][]string) {
	if result == nil || result.State == nil {
		return nil, "", nil
	}
	return dotsSnapshot(result.State)
}

func dotsSnapshot(state *app.DotsState) ([]app.DotStatus, string, map[string][]string) {
	return state.Entries, state.GitStatus, state.DotMemberships
}

func (m *Model) doRefreshDotsHistory() tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		if a == nil {
			return dotsHistoryLoadedMsg{}
		}
		if ctx == nil {
			ctx = context.Background()
		}
		entries, err := a.RecentDotsHistory(ctx, 3)
		return dotsHistoryLoadedMsg{entries: entries, err: err}
	}
}

func (m *Model) refreshDotsHistory(cmds *[]tea.Cmd) {
	if m.app == nil {
		return
	}
	*cmds = append(*cmds, m.doRefreshDotsHistory())
}

func (m *Model) handleDotsHistoryLoadedMsg(msg dotsHistoryLoadedMsg) {
	if msg.err != nil {
		m.dotsHistoryErr = msg.err.Error()
		return
	}
	m.dotsHistory = append([]app.DotsHistoryEntry(nil), msg.entries...)
	m.dotsHistoryErr = ""
}

// doDotsPull runs git pull + resync and refreshes dots status.
func (m *Model) doDotsPull() tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	return func() tea.Msg {
		result, err := a.DotsPullWithState(ctx)
		entries, gitStatus, memberships := dotsSnapshotFromState(result)
		return dotsPulledMsg{gen: gen, entries: entries, gitStatus: gitStatus, dotMemberships: memberships, err: err}
	}
}

// doDotsPush commits all changes with an auto-generated message and pushes.
func (m *Model) doDotsPush() tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	return func() tea.Msg {
		result, err := a.DotsPushWithState(ctx, "")
		entries, gitStatus, memberships := dotsSnapshotFromState(result)
		return dotsPushedMsg{gen: gen, entries: entries, gitStatus: gitStatus, dotMemberships: memberships, err: err}
	}
}

// doDotsCommit commits all changes with an auto-generated message.
func (m *Model) doDotsCommit() tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	return func() tea.Msg {
		result, err := a.DotsCommitWithState(ctx, "")
		entries, gitStatus, memberships := dotsSnapshotFromState(result)
		return dotsCommittedMsg{gen: gen, entries: entries, gitStatus: gitStatus, dotMemberships: memberships, err: err}
	}
}

// doDotsOverwrite resolves a conflict for the named entry by backing up the
// original file and creating the managed symlink, then refreshes status.
func (m *Model) doDotsOverwrite(name string) tea.Cmd {
	return m.doDotsResolve(name, app.DotResolveUseRepo)
}

func (m *Model) doDotsResolve(name string, strategy app.DotsResolveStrategy) tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	return func() tea.Msg {
		result, err := a.DotsResolveConflictWithState(ctx, name, strategy)
		entries, gitStatus, memberships := dotsSnapshotFromState(result)
		return dotsFixedMsg{gen: gen, name: name, entries: entries, gitStatus: gitStatus, dotMemberships: memberships, err: err}
	}
}

func (m *Model) doDotsForceResolveAll(strategy app.DotsResolveStrategy) tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	return func() tea.Msg {
		result, err := a.DotsForceResolveAllWithState(ctx, strategy)
		entries, gitStatus, memberships := dotsSnapshotFromState(result)
		return dotsFixedMsg{gen: gen, name: "all conflicts", entries: entries, gitStatus: gitStatus, dotMemberships: memberships, err: err}
	}
}

func (m *Model) doDotsResolveDiscovered(status app.DotStatus, strategy app.DotsResolveStrategy) tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	return func() tea.Msg {
		result, err := a.DotsResolveDiscoveredWithState(ctx, status.Name, status.Group, strategy)
		name := status.Name
		if result != nil && result.Added.Name != "" {
			name = result.Added.Name
		}
		entries, gitStatus, memberships := dotsSnapshotFromDiscoveredState(result)
		return dotsFixedMsg{gen: gen, name: name, entries: entries, gitStatus: gitStatus, dotMemberships: memberships, err: err}
	}
}

// doDotsAdd adopts the file/dir at absPath into the dots repo and refreshes status.
// rawPath is the tilde-form path used for display (e.g. "~/.config/myapp").
func (m *Model) doDotsAdd(absPath, rawPath, group string) tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	return func() tea.Msg {
		result, err := a.DotsAddWithState(ctx, absPath, app.DotsAddOptions{Adopt: true, Group: group})
		entries, gitStatus, memberships := dotsSnapshotFromState(result)
		return dotsAddedMsg{gen: gen, path: rawPath, entries: entries, gitStatus: gitStatus, dotMemberships: memberships, err: err}
	}
}

// doDotsIgnore toggles a per-entry ignore glob for the named dots entry in config.
func (m *Model) doDotsIgnore(name, pattern string, ignored bool) tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	return func() tea.Msg {
		var result *app.DotsOperationStateResult
		var err error
		if ignored {
			result, err = a.DotsAddIgnorePatternWithState(ctx, name, pattern)
		} else {
			result, err = a.DotsIncludeIgnoredPathWithState(ctx, name, pattern)
		}
		entries, gitStatus, memberships := dotsSnapshotFromState(result)
		return dotsIgnoredMsg{gen: gen, name: name, pattern: pattern, ignored: ignored, entries: entries, gitStatus: gitStatus, dotMemberships: memberships, err: err}
	}
}

func (m *Model) doDotsEntryIgnore(status app.DotStatus, ignored bool) tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	return func() tea.Msg {
		path := status.ConfigPath
		if path == "" {
			path = status.TargetPath
		}
		result, err := a.DotsSetEntryIgnoredWithState(ctx, status.Name, path, ignored)
		entries, gitStatus, memberships := dotsSnapshotFromState(result)
		return dotsIgnoredMsg{gen: gen, name: status.Name, pattern: status.Name, ignored: ignored, entries: entries, gitStatus: gitStatus, dotMemberships: memberships, err: err}
	}
}

func (m *Model) doDotsVariantChange(req dotsVariantRequest) tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	return func() tea.Msg {
		var (
			result *app.DotsVariantStateResult
			info   app.DotVariantInfo
			err    error
		)
		if req.remove {
			result, err = a.DotsRemoveHostVariantWithState(ctx, req.name, app.DotsRemoveVariantOptions{})
		} else {
			result, err = a.DotsAddHostVariantWithState(ctx, req.name, app.DotsAddVariantOptions{
				Sync: true,
			})
		}
		if result != nil {
			info = result.Info
		}
		entries, gitStatus, memberships := dotsSnapshotFromVariantState(result)
		return dotsVariantChangedMsg{
			gen:            gen,
			name:           req.name,
			info:           info,
			removed:        req.remove,
			entries:        entries,
			gitStatus:      gitStatus,
			dotMemberships: memberships,
			err:            err,
		}
	}
}

// doDotsDelete removes the named dots entry and refreshes status.
func (m *Model) doDotsDelete(name string, keepLocal bool) tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	return func() tea.Msg {
		result, err := a.DotsDeleteWithState(ctx, name, app.DotsDeleteOptions{KeepLocal: keepLocal})
		entries, gitStatus, memberships := dotsSnapshotFromState(result)
		return dotsDeletedMsg{gen: gen, name: name, entries: entries, gitStatus: gitStatus, dotMemberships: memberships, err: err}
	}
}

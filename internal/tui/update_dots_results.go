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
		if row.isChild || !isTransientDotCandidate(row.entry) {
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
	sortDotsEntries(entries)
	m.dotsEntries = entries
	m.dotsGitStatus = gitStatus
	if memberships != nil {
		m.dotMemberships = memberships
	}
	m.syncDotsExpandedName(dotsVisibleRows(*m))
}

func (m *Model) prepareDotsSnapshotOnLaunch(cmds *[]tea.Cmd) {
	if m.app == nil || m.settings.DotsRepo == "" || m.dotsLoaded || m.dotsPreparing {
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
		result, err := a.DiscoverDotsStatus(ctx)
		if err != nil {
			if result != nil {
				return dotsLoadedMsg{gen: gen, entries: result.Entries, gitStatus: result.GitStatus, dotMemberships: loadDotMemberships(a, ctx), err: err}
			}
			return dotsLoadedMsg{gen: gen, err: err}
		}
		return dotsLoadedMsg{gen: gen, entries: result.Entries, gitStatus: result.GitStatus, dotMemberships: loadDotMemberships(a, ctx)}
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
	a := m.app
	ctx, gen := m.currentDotsOperation()
	return func() tea.Msg {
		if progressCh != nil {
			defer close(progressCh)
		}
		opts := dots.SyncOptions{EntryOrder: append([]string(nil), entryOrder...)}
		if progressCh != nil {
			opts.Progress = func(event dots.SyncProgressEvent) {
				update := dotsProgressUpdate{
					gen:  gen,
					text: dotsSyncProgressText(event.Entry, event.Index, event.Total, event.Done, event.Err),
					name: event.Entry,
					done: event.Done,
				}
				if event.Done {
					update.entries, update.gitStatus, update.dotMemberships, _ = refreshDotsSnapshot(a, ctx)
				}
				sendDotsProgressUpdate(progressCh, update)
			}
		}
		// Capture sync error but always refresh status so health reflects
		// conflict state even after a partial stow failure.
		_, syncErr := a.DotsSyncContext(ctx, opts)
		result, err := a.DiscoverDotsStatus(ctx)
		if err != nil {
			if result != nil {
				return dotsSyncedMsg{gen: gen, entries: result.Entries, gitStatus: result.GitStatus, dotMemberships: loadDotMemberships(a, ctx), err: combineDotsErrors(syncErr, err)}
			}
			return dotsSyncedMsg{gen: gen, err: combineDotsErrors(syncErr, err)}
		}
		return dotsSyncedMsg{gen: gen, entries: result.Entries, gitStatus: result.GitStatus, dotMemberships: loadDotMemberships(a, ctx), err: syncErr}
	}
}

func (m *Model) doDotsSyncEntry(name string) tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	return func() tea.Msg {
		_, syncErr := a.DotsSyncEntry(ctx, name, dots.SyncOptions{})
		result, err := a.DiscoverDotsStatus(ctx)
		if err != nil {
			if result != nil {
				return dotsSyncedMsg{gen: gen, entries: result.Entries, gitStatus: result.GitStatus, dotMemberships: loadDotMemberships(a, ctx), err: combineDotsErrors(syncErr, err)}
			}
			return dotsSyncedMsg{gen: gen, err: combineDotsErrors(syncErr, err)}
		}
		return dotsSyncedMsg{gen: gen, entries: result.Entries, gitStatus: result.GitStatus, dotMemberships: loadDotMemberships(a, ctx), err: syncErr}
	}
}

func (m *Model) doDotsSyncDiscovered(status app.DotStatus) tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	return func() tea.Msg {
		added, addErr := a.DotsAddDiscoveredEntry(status.Name, status.Group)
		name := added.Name
		if name == "" {
			name = status.Name
		}
		var syncErr error
		if addErr == nil {
			_, syncErr = a.DotsSyncEntry(ctx, name, dots.SyncOptions{})
		}
		entries, gitStatus, memberships, err := refreshDotsSnapshot(a, ctx)
		return dotsSyncedMsg{gen: gen, entries: entries, gitStatus: gitStatus, dotMemberships: memberships, err: combineDotsErrors(combineDotsErrors(addErr, syncErr), err)}
	}
}

// doDotsRefresh re-scans dot entries, refreshes git status, and discovers
// untracked candidates without mutating config, the repo, or local files.
func (m *Model) doDotsRefresh() tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	return func() tea.Msg {
		result, err := a.DiscoverDotsStatus(ctx)
		if result != nil {
			return dotsDiscoveredMsg{
				gen:             gen,
				entries:         result.Entries,
				gitStatus:       result.GitStatus,
				dotMemberships:  loadDotMemberships(a, ctx),
				discoveredCount: result.DiscoveredCount,
				err:             err,
			}
		}
		return dotsDiscoveredMsg{gen: gen, err: err}
	}
}

func combineDotsErrors(primary, secondary error) error {
	switch {
	case primary == nil:
		return secondary
	case secondary == nil:
		return primary
	default:
		return fmt.Errorf("%v; %w", primary, secondary)
	}
}

func refreshDotsSnapshot(a *app.App, ctx context.Context) ([]app.DotStatus, string, map[string][]string, error) {
	result, err := a.DiscoverDotsStatus(ctx)
	if result == nil {
		return nil, "", nil, err
	}
	return result.Entries, result.GitStatus, loadDotMemberships(a, ctx), err
}

func loadDotMemberships(a *app.App, ctx context.Context) map[string][]string {
	memberships, err := a.DotMembershipMap(ctx)
	if err != nil {
		return nil
	}
	return memberships
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
		// Capture pull/sync error but always refresh status so health reflects
		// any conflicts introduced by the pull.
		_, pullErr := a.DotsPull(ctx)
		entries, gitStatus, memberships, err := refreshDotsSnapshot(a, ctx)
		return dotsPulledMsg{gen: gen, entries: entries, gitStatus: gitStatus, dotMemberships: memberships, err: combineDotsErrors(pullErr, err)}
	}
}

// doDotsPush commits all changes with an auto-generated message and pushes.
func (m *Model) doDotsPush() tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	return func() tea.Msg {
		pushErr := a.DotsPush(ctx, "")
		entries, gitStatus, memberships, err := refreshDotsSnapshot(a, ctx)
		return dotsPushedMsg{gen: gen, entries: entries, gitStatus: gitStatus, dotMemberships: memberships, err: combineDotsErrors(pushErr, err)}
	}
}

// doDotsCommit commits all changes with an auto-generated message.
func (m *Model) doDotsCommit() tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	return func() tea.Msg {
		commitErr := a.DotsCommit(ctx, "")
		entries, gitStatus, memberships, err := refreshDotsSnapshot(a, ctx)
		return dotsCommittedMsg{gen: gen, entries: entries, gitStatus: gitStatus, dotMemberships: memberships, err: combineDotsErrors(commitErr, err)}
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
		_, resolveErr := a.DotsResolveConflict(ctx, name, strategy)
		entries, gitStatus, memberships, err := refreshDotsSnapshot(a, ctx)
		return dotsFixedMsg{gen: gen, name: name, entries: entries, gitStatus: gitStatus, dotMemberships: memberships, err: combineDotsErrors(resolveErr, err)}
	}
}

func (m *Model) doDotsResolveDiscovered(status app.DotStatus, strategy app.DotsResolveStrategy) tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	return func() tea.Msg {
		added, addErr := a.DotsAddDiscoveredEntry(status.Name, status.Group)
		name := added.Name
		if name == "" {
			name = status.Name
		}
		var resolveErr error
		if addErr == nil {
			_, resolveErr = a.DotsResolveConflict(ctx, name, strategy)
		}
		entries, gitStatus, memberships, err := refreshDotsSnapshot(a, ctx)
		return dotsFixedMsg{gen: gen, name: name, entries: entries, gitStatus: gitStatus, dotMemberships: memberships, err: combineDotsErrors(combineDotsErrors(addErr, resolveErr), err)}
	}
}

// doDotsAdd adopts the file/dir at absPath into the dots repo and refreshes status.
// rawPath is the tilde-form path used for display (e.g. "~/.config/myapp").
func (m *Model) doDotsAdd(absPath, rawPath, group string) tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	return func() tea.Msg {
		_, addErr := a.DotsAdd(ctx, absPath, app.DotsAddOptions{Adopt: true, Group: group})
		entries, gitStatus, memberships, err := refreshDotsSnapshot(a, ctx)
		return dotsAddedMsg{gen: gen, path: rawPath, entries: entries, gitStatus: gitStatus, dotMemberships: memberships, err: combineDotsErrors(addErr, err)}
	}
}

// doDotsIgnore toggles a per-entry ignore glob for the named dots entry in config.
func (m *Model) doDotsIgnore(name, pattern string, ignored bool) tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	return func() tea.Msg {
		var err error
		if ignored {
			err = a.DotsAddIgnorePattern(name, pattern)
		} else {
			err = a.DotsRemoveIgnorePattern(name, pattern)
		}
		entries, gitStatus, memberships, refreshErr := refreshDotsSnapshot(a, ctx)
		return dotsIgnoredMsg{gen: gen, name: name, pattern: pattern, ignored: ignored, entries: entries, gitStatus: gitStatus, dotMemberships: memberships, err: combineDotsErrors(err, refreshErr)}
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
		ignoreErr := a.DotsSetEntryIgnored(status.Name, path, ignored)
		entries, gitStatus, memberships, err := refreshDotsSnapshot(a, ctx)
		return dotsIgnoredMsg{gen: gen, name: status.Name, pattern: status.Name, ignored: ignored, entries: entries, gitStatus: gitStatus, dotMemberships: memberships, err: combineDotsErrors(ignoreErr, err)}
	}
}

func (m *Model) doDotsVariantChange(req dotsVariantRequest) tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	return func() tea.Msg {
		var (
			info  app.DotVariantInfo
			opErr error
		)
		if req.remove {
			info, opErr = a.DotsRemoveHostVariant(ctx, req.name, app.DotsRemoveVariantOptions{})
		} else {
			info, _, opErr = a.DotsAddHostVariant(ctx, req.name, app.DotsAddVariantOptions{
				Sync: true,
			})
		}
		entries, gitStatus, memberships, err := refreshDotsSnapshot(a, ctx)
		return dotsVariantChangedMsg{
			gen:            gen,
			name:           req.name,
			info:           info,
			removed:        req.remove,
			entries:        entries,
			gitStatus:      gitStatus,
			dotMemberships: memberships,
			err:            combineDotsErrors(opErr, err),
		}
	}
}

// doDotsDelete removes the named dots entry and refreshes status.
func (m *Model) doDotsDelete(name string, keepLocal bool) tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	return func() tea.Msg {
		deleteErr := a.DotsDeleteWithOptions(ctx, name, app.DotsDeleteOptions{KeepLocal: keepLocal})
		entries, gitStatus, memberships, err := refreshDotsSnapshot(a, ctx)
		return dotsDeletedMsg{gen: gen, name: name, entries: entries, gitStatus: gitStatus, dotMemberships: memberships, err: combineDotsErrors(deleteErr, err)}
	}
}

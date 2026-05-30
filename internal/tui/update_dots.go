package tui

import (
	"context"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/dots"
)

func (m *Model) beginDotsOperation(status string) {
	m.cancelDotsOperation()
	m.clearDotsProgressState()
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	m.dotsOpGen++
	m.dotsCtx = ctx
	m.dotsCancel = cancel
	m.dotsLoading = true
	startOp(m, status)
	setActivityStatus(m, status)
}

func (m *Model) cancelDotsOperation() {
	if m.dotsCancel != nil {
		m.dotsCancel()
		m.dotsCancel = nil
	}
	m.dotsCtx = nil
}

func (m *Model) currentDotsOperation() (context.Context, int) {
	if m.dotsCtx != nil {
		return m.dotsCtx, m.dotsOpGen
	}
	if m.ctx == nil {
		return context.Background(), m.dotsOpGen
	}
	return m.ctx, m.dotsOpGen
}

func (m *Model) finishDotsOperation(gen int) bool {
	if gen != m.dotsOpGen {
		return false
	}
	m.cancelDotsOperation()
	m.dotsLoading = false
	m.clearDotsProgressState()
	if !m.launchBatchActive {
		m.progressText = ""
	}
	return true
}

func (m *Model) clearDotsProgressState() {
	m.dotsProgressCh = nil
	m.dotsPendingNames = nil
	m.dotsActiveName = ""
}

func (m *Model) markDotsPendingSyncAll() int {
	pending := app.DotSyncAllPendingNames(m.dotsEntries)
	m.dotsPendingNames = pending
	return len(pending)
}

func dotsSyncAllEntryOrder(m Model) []string {
	return app.DotSyncAllEntryOrder(filteredDotsEntries(m))
}

func (m *Model) handleDotsSubmodeKeyMsg(msg tea.KeyPressMsg) (bool, []tea.Cmd) {
	switch {
	case m.dotsSearchActive:
		return true, m.handleDotsSearchKeyMsg(msg)
	default:
		return false, nil
	}
}

func (m *Model) beginDotsVariantOperation(req dotsVariantRequest) {
	if req.remove {
		m.beginDotsOperation("Removing variant for " + req.name + "…")
		return
	}
	m.beginDotsOperation("Creating variant for " + req.name + "…")
}

func (m *Model) handleDotsSearchKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd

	switch {
	case key.Matches(msg, m.keys.Back):
		m.dotsSearchActive = false
		m.filter.SetValue("")
		m.filter.Blur()
		m.dotsCursor = 0
		m.dotsExpandedName = ""
		m.clearDotsExpandedChildren("")
		m.syncDotsExpandedName(dotsVisibleRows(*m))
		m.clearDotsConfirmState()
	case key.Matches(msg, m.keys.Confirm):
		m.filter.Blur()
	default:
		prev := m.filter.Value()
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		cmds = append(cmds, cmd)
		if m.filter.Value() != prev {
			m.dotsCursor = 0
			m.dotsExpandedName = ""
			m.clearDotsExpandedChildren("")
			m.syncDotsExpandedName(dotsVisibleRows(*m))
			m.clearDotsConfirmState()
		}
	}

	return cmds
}

func (m *Model) handleDotsNavigationKeyMsg(msg tea.KeyPressMsg, visible []dotsVisibleRow, cmds *[]tea.Cmd) bool {
	switch {
	case key.Matches(msg, m.keys.Back):
		m.handleDotsBackKey()
	case key.Matches(msg, m.keys.Up):
		m.clearDotsConfirmState()
		m.moveDotsCursor(-1, visible)
	case key.Matches(msg, m.keys.Down):
		m.clearDotsConfirmState()
		m.moveDotsCursor(1, visible)
	case m.dotsConfirmIdx >= 0 || m.dotsOverwriteIdx >= 0 || m.dotsLocalIdx >= 0 || m.dotsIgnoreIdx >= 0 || m.dotsVariantIdx >= 0:
		return false
	case key.Matches(msg, m.keys.Search):
		m.dotsSearchActive = true
		m.filter.SetValue("")
		m.filter.Focus()
		m.dotsCursor = 0
		m.clearDotsConfirmState()
		*cmds = append(*cmds, textinput.Blink)
	case key.Matches(msg, m.keys.DotAdd):
		if msg.IsRepeat {
			return true
		}
		if m.dotsSyncAvailability().Reason == app.DotsSyncAvailabilityDisabled {
			return true
		}
		m.filePickerForDotAdd = true
		*cmds = append(*cmds, m.openFilePicker("Add dotfile path", "", true))
	default:
		return false
	}
	return true
}

func (m *Model) handleDotsBackKey() {
	if m.dotsConfirmIdx >= 0 || m.dotsOverwriteIdx >= 0 || m.dotsLocalIdx >= 0 || m.dotsIgnoreIdx >= 0 || m.dotsVariantIdx >= 0 {
		m.clearDotsConfirmState()
	} else if m.dotsSearchActive {
		m.dotsSearchActive = false
		m.filter.SetValue("")
		m.dotsCursor = 0
		m.dotsExpandedName = ""
		m.clearDotsExpandedChildren("")
	}
}

func (m *Model) syncDotsExpandedName(visible []dotsVisibleRow) {
	if len(visible) == 0 {
		m.dotsExpandedName = ""
		m.clearDotsExpandedChildren("")
		m.dotsCursor = 0
		return
	}
	if m.dotsCursor < 0 {
		m.dotsCursor = 0
	}
	if m.dotsCursor >= len(visible) {
		m.dotsCursor = len(visible) - 1
	}
	if m.dotsExpandedName != "" {
		found := false
		var expanded app.DotStatus
		for _, row := range visible {
			if !row.isChild && row.entry.Name == m.dotsExpandedName && app.DotStatusState(row.entry) == m.dotsExpandedState {
				found = true
				expanded = row.entry
				break
			}
		}
		if !found {
			m.clearDotsExpandedChildren(m.dotsExpandedName)
			m.dotsExpandedName = ""
		} else {
			m.pruneDotsExpandedChildren(expanded)
		}
	}
}

func (m *Model) moveDotsCursor(delta int, visible []dotsVisibleRow) {
	if len(visible) == 0 {
		m.dotsCursor = 0
		m.dotsExpandedName = ""
		m.clearDotsExpandedChildren("")
		return
	}
	n := len(visible)
	next := (m.dotsCursor + delta + n) % n
	target := visible[next]
	if m.dotsExpandedName != "" && (target.entry.Name != m.dotsExpandedName || app.DotStatusState(target.entry) != m.dotsExpandedState) {
		m.clearDotsExpandedChildren(m.dotsExpandedName)
		m.dotsExpandedName = ""
		rows := dotsVisibleRows(*m)
		if idx := dotsRowIndex(rows, target); idx >= 0 {
			m.dotsCursor = idx
			return
		}
		m.syncDotsExpandedName(rows)
		return
	}
	m.dotsCursor = next
}

func dotsRowIndex(rows []dotsVisibleRow, target dotsVisibleRow) int {
	for i, row := range rows {
		if row.entry.Name != target.entry.Name || row.isChild != target.isChild {
			continue
		}
		if !row.isChild || row.child.RelPath == target.child.RelPath {
			return i
		}
	}
	return -1
}

func (m *Model) clearDotsExpandedChildren(entryName string) {
	if len(m.dotsExpandedChildren) == 0 {
		return
	}
	if entryName == "" {
		m.dotsExpandedChildren = nil
		return
	}
	prefix := dotsChildExpandKey(entryName, "")
	for key := range m.dotsExpandedChildren {
		if strings.HasPrefix(key, prefix) {
			delete(m.dotsExpandedChildren, key)
		}
	}
	if len(m.dotsExpandedChildren) == 0 {
		m.dotsExpandedChildren = nil
	}
}

func (m *Model) pruneDotsExpandedChildren(entry app.DotStatus) {
	if len(m.dotsExpandedChildren) == 0 {
		return
	}
	prefix := dotsChildExpandKey(entry.Name, "")
	valid := make(map[string]bool)
	var collect func([]app.DotChild)
	collect = func(children []app.DotChild) {
		for _, child := range children {
			if child.IsDir && len(child.Children) > 0 {
				valid[dotsChildExpandKey(entry.Name, child.RelPath)] = true
				collect(child.Children)
			}
		}
	}
	collect(entry.Children)
	for key := range m.dotsExpandedChildren {
		if !strings.HasPrefix(key, prefix) || !valid[key] {
			delete(m.dotsExpandedChildren, key)
		}
	}
	if len(m.dotsExpandedChildren) == 0 {
		m.dotsExpandedChildren = nil
	}
}

func (m *Model) clearDotsConfirmState() {
	if m.dotsConfirmIdx >= 0 || m.dotsOverwriteIdx >= 0 || m.dotsLocalIdx >= 0 || m.dotsIgnoreIdx >= 0 || m.dotsVariantIdx >= 0 {
		m.cancelConfirmationTimeout()
	}
	m.dotsConfirmIdx = -1
	m.dotsOverwriteIdx = -1
	m.dotsLocalIdx = -1
	m.dotsIgnoreIdx = -1
	m.dotsVariantIdx = -1
	m.dotsVariantMode = dotsVariantNone
}

func (m *Model) handleDotsActionKeyMsg(msg tea.KeyPressMsg, visible []dotsVisibleRow) []tea.Cmd {
	var cmds []tea.Cmd

	if m.dotsSyncAvailability().Reason == app.DotsSyncAvailabilityDisabled {
		switch {
		case key.Matches(msg, m.keys.Confirm):
			cmds = append(cmds, m.handleDotsConfirmKeyMsg(visible)...)
		case key.Matches(msg, m.keys.Toggle):
			if !msg.IsRepeat {
				m.handleDotsToggleKeyMsg(visible)
			}
		default:
			m.clearDotsConfirmState()
		}
		return cmds
	}

	if m.dotsConfirmIdx >= 0 {
		return m.handleDotsDeleteChoiceKeyMsg(msg, visible)
	}
	if m.dotsVariantIdx >= 0 {
		return m.handleDotsVariantChoiceKeyMsg(msg, visible)
	}
	if m.dotsOverwriteIdx >= 0 && !key.Matches(msg, m.keys.DotUseRepo) {
		return cmds
	}
	if m.dotsLocalIdx >= 0 && !key.Matches(msg, m.keys.DotUseLocal) {
		return cmds
	}
	if m.dotsIgnoreIdx >= 0 && !key.Matches(msg, m.keys.DotIgnore) {
		return cmds
	}

	switch {
	case key.Matches(msg, m.keys.Confirm):
		cmds = append(cmds, m.handleDotsConfirmKeyMsg(visible)...)
	case key.Matches(msg, m.keys.Toggle):
		if msg.IsRepeat {
			break
		}
		m.handleDotsToggleKeyMsg(visible)
	case key.Matches(msg, m.keys.Sync):
		if msg.IsRepeat || len(visible) == 0 || m.dotsCursor >= len(visible) {
			break
		}
		row := visible[m.dotsCursor]
		if row.isChild {
			break
		}
		entry := row.entry
		if !app.DotStatusHasAction(entry, app.DotActionSync) {
			break
		}
		m.beginDotsOperation("Syncing " + entry.Name + "…")
		if app.DotStatusTransientCandidate(entry) {
			cmds = append(cmds, m.spinner.Tick, m.doDotsSyncDiscovered(entry))
		} else {
			cmds = append(cmds, m.spinner.Tick, m.doDotsSyncEntry(entry.Name))
		}
	case key.Matches(msg, m.keys.SyncAll):
		if msg.IsRepeat {
			break
		}
		m.beginDotsOperation("Syncing dots…")
		total := m.markDotsPendingSyncAll()
		setActivityStatus(m, app.DotsSyncActivityProgressText(dots.SyncProgressEvent{Total: total}))
		order := dotsSyncAllEntryOrder(*m)
		ch := m.beginDotsProgressStream()
		cmds = append(cmds, m.spinner.Tick, m.doDotsSyncOnlyWithProgress(ch, order), waitForDotsProgress(ch, m.dotsOpGen))
	case key.Matches(msg, m.keys.DotRefresh):
		if msg.IsRepeat {
			break
		}
		m.beginDotsOperation("Refreshing dots…")
		cmds = append(cmds, m.spinner.Tick, m.doDotsRefresh())
	case key.Matches(msg, m.keys.MoveGroup):
		if msg.IsRepeat {
			break
		}
		m.openDotGroupMembershipPicker(visible)
	case key.Matches(msg, m.keys.DotUseRepo):
		cmds = append(cmds, m.handleDotsResolveKeyMsg(visible, app.DotResolveUseRepo)...)
	case key.Matches(msg, m.keys.DotUseLocal):
		cmds = append(cmds, m.handleDotsResolveKeyMsg(visible, app.DotResolveUseLocal)...)
	case key.Matches(msg, m.keys.DotDelete):
		cmds = append(cmds, m.handleDotsDeleteKeyMsg(visible)...)
	case key.Matches(msg, m.keys.DotVariant):
		if msg.IsRepeat {
			break
		}
		cmds = append(cmds, m.handleDotsVariantKeyMsg(visible)...)
	case key.Matches(msg, m.keys.DotIgnore):
		if msg.IsRepeat {
			break
		}
		cmds = append(cmds, m.handleDotsIgnoreActionKeyMsg(visible)...)
	}

	return cmds
}

func dotsVariantEligible(row dotsVisibleRow) bool {
	if row.isChild {
		return false
	}
	return app.DotStatusVariantEligible(row.entry)
}

func (m *Model) handleDotsVariantKeyMsg(visible []dotsVisibleRow) []tea.Cmd {
	var cmds []tea.Cmd
	if m.app == nil || len(visible) == 0 || m.dotsCursor < 0 || m.dotsCursor >= len(visible) {
		return cmds
	}
	row := visible[m.dotsCursor]
	if !dotsVariantEligible(row) {
		return cmds
	}
	mode := dotsVariantCreate
	hasActiveVariant, err := m.app.DotsHasActiveHostVariant(row.entry.Name)
	if err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+err.Error(), true))
		return cmds
	}
	if hasActiveVariant {
		mode = dotsVariantRemove
	}
	m.dotsConfirmIdx = -1
	m.dotsOverwriteIdx = -1
	m.dotsLocalIdx = -1
	m.dotsIgnoreIdx = -1
	m.dotsVariantIdx = m.dotsCursor
	m.dotsVariantMode = mode
	cmds = append(cmds, m.armConfirmationTimeout())
	return cmds
}

func (m *Model) handleDotsVariantChoiceKeyMsg(msg tea.KeyPressMsg, visible []dotsVisibleRow) []tea.Cmd {
	var cmds []tea.Cmd
	if m.dotsVariantIdx < 0 || m.dotsVariantIdx >= len(visible) {
		return cmds
	}
	row := visible[m.dotsVariantIdx]
	if !dotsVariantEligible(row) {
		return cmds
	}
	name := row.entry.Name
	switch m.dotsVariantMode {
	case dotsVariantCreate:
		if !key.Matches(msg, m.keys.DotVariant) {
			return cmds
		}
		return m.startDotsVariantChange(dotsVariantRequest{name: name})
	case dotsVariantRemove:
		if !key.Matches(msg, m.keys.DotVariant) {
			return cmds
		}
		return m.startDotsVariantChange(dotsVariantRequest{name: name, remove: true})
	default:
		return cmds
	}
}

func (m *Model) startDotsVariantChange(req dotsVariantRequest) []tea.Cmd {
	var cmds []tea.Cmd
	if req.name == "" || m.app == nil {
		return cmds
	}
	m.cancelConfirmationTimeout()
	m.dotsVariantIdx = -1
	m.dotsVariantMode = dotsVariantNone
	m.stowInstallVariant = req
	if m.promptForStowInstall(stowInstallDotVariant) {
		return cmds
	}
	m.stowInstallVariant = dotsVariantRequest{}
	m.beginDotsVariantOperation(req)
	cmds = append(cmds, m.spinner.Tick, m.doDotsVariantChange(req))
	return cmds
}

func (m *Model) handleDotsToggleKeyMsg(visible []dotsVisibleRow) {
	if len(visible) == 0 || m.dotsCursor < 0 || m.dotsCursor >= len(visible) {
		return
	}
	m.clearDotsConfirmState()
	row := visible[m.dotsCursor]
	name := row.entry.Name
	if row.isChild {
		if !dotsRowExpandable(row) {
			return
		}
		if m.dotsExpandedChildren == nil {
			m.dotsExpandedChildren = make(map[string]bool)
		}
		key := dotsChildExpandKey(name, row.child.RelPath)
		if m.dotsExpandedChildren[key] {
			delete(m.dotsExpandedChildren, key)
		} else {
			m.dotsExpandedChildren[key] = true
		}
		if len(m.dotsExpandedChildren) == 0 {
			m.dotsExpandedChildren = nil
		}
		if idx := dotsRowIndex(dotsVisibleRows(*m), row); idx >= 0 {
			m.dotsCursor = idx
		}
		return
	}
	if len(row.entry.Children) == 0 {
		return
	}
	state := app.DotStatusState(row.entry)
	if m.dotsExpandedName == name && m.dotsExpandedState == state {
		m.clearDotsExpandedChildren(name)
		m.dotsExpandedName = ""
		return
	}
	if m.dotsExpandedName != "" {
		m.clearDotsExpandedChildren(m.dotsExpandedName)
	}
	m.dotsExpandedName = name
	m.dotsExpandedState = state
}

func (m *Model) openDotGroupMembershipPicker(visible []dotsVisibleRow) {
	if m.app == nil {
		return
	}
	if len(visible) == 0 || m.dotsCursor < 0 || m.dotsCursor >= len(visible) {
		return
	}
	row := visible[m.dotsCursor]
	if row.isChild {
		return
	}
	name := row.entry.Name
	if len(m.dotMemberships[name]) == 0 {
		return
	}
	m.mode = viewGroupMembership
	m.pickerGroups = append(prioritizedPickerGroups(*m), groupPickerNewSentinel)
	m.pickerCursor = 0
	m.pickerCreatingGroup = false
	m.pickerCreatedGroups = nil
	m.pickerMembershipKind = pickerMembershipDot
	m.pickerMembershipName = name
	m.pickerOriginalGroups = append([]string(nil), m.dotMemberships[name]...)
}

func (m *Model) handleDotsConfirmKeyMsg(visible []dotsVisibleRow) []tea.Cmd {
	var cmds []tea.Cmd

	switch m.dotsSyncAvailability().Reason {
	case app.DotsSyncAvailabilityDisabled:
		if m.promptForStowInstall(stowInstallEnableDots) {
			return cmds
		}
		m.beginDotsOperation("Enabling dots…")
		cmds = append(cmds, m.spinner.Tick, m.doEnableDots())
		return cmds
	case app.DotsSyncAvailabilityNoRepo:
		m.mode = viewSetup
		m.setupBackgroundMode = viewDots
		m.setupStep = 5
		return cmds
	}
	return cmds
}

func (m *Model) handleDotsResolveKeyMsg(visible []dotsVisibleRow, strategy app.DotsResolveStrategy) []tea.Cmd {
	var cmds []tea.Cmd
	if len(visible) == 0 || m.dotsCursor < 0 || m.dotsCursor >= len(visible) {
		return cmds
	}
	row := visible[m.dotsCursor]
	if row.isChild {
		return cmds
	}
	entry := row.entry
	if strategy == app.DotResolveUseRepo && !app.DotStatusHasAction(entry, app.DotActionUseRepo) {
		return cmds
	}
	if strategy == app.DotResolveUseLocal && !app.DotStatusHasAction(entry, app.DotActionUseLocal) {
		return cmds
	}
	idx := &m.dotsOverwriteIdx
	label := "repo"
	if strategy == app.DotResolveUseLocal {
		idx = &m.dotsLocalIdx
		label = "local"
	}
	if *idx == m.dotsCursor {
		name := entry.Name
		m.cancelConfirmationTimeout()
		m.dotsOverwriteIdx = -1
		m.dotsLocalIdx = -1
		m.beginDotsOperation("Using " + label + " for " + name + "…")
		if app.DotStatusTransientCandidate(entry) {
			cmds = append(cmds, m.spinner.Tick, m.doDotsResolveDiscovered(entry, strategy))
		} else {
			cmds = append(cmds, m.spinner.Tick, m.doDotsResolve(name, strategy))
		}
		return cmds
	}
	m.dotsOverwriteIdx = -1
	m.dotsLocalIdx = -1
	m.dotsIgnoreIdx = -1
	*idx = m.dotsCursor
	m.dotsConfirmIdx = -1
	cmds = append(cmds, m.armConfirmationTimeout())
	return cmds
}

func (m *Model) handleDotsDeleteKeyMsg(visible []dotsVisibleRow) []tea.Cmd {
	var cmds []tea.Cmd

	if len(visible) == 0 || m.dotsCursor < 0 || m.dotsCursor >= len(visible) {
		return cmds
	}
	if visible[m.dotsCursor].isChild {
		return cmds
	}
	if !app.DotStatusHasAction(visible[m.dotsCursor].entry, app.DotActionRemove) {
		return cmds
	}
	m.dotsConfirmIdx = m.dotsCursor
	m.dotsOverwriteIdx = -1
	m.dotsLocalIdx = -1
	m.dotsIgnoreIdx = -1
	cmds = append(cmds, m.armConfirmationTimeout())
	return cmds
}

func (m *Model) handleDotsDeleteChoiceKeyMsg(msg tea.KeyPressMsg, visible []dotsVisibleRow) []tea.Cmd {
	var cmds []tea.Cmd
	if m.dotsConfirmIdx < 0 || m.dotsConfirmIdx >= len(visible) {
		return cmds
	}
	row := visible[m.dotsConfirmIdx]
	if row.isChild {
		return cmds
	}

	switch strings.ToLower(msg.String()) {
	case "y":
		cmds = append(cmds, m.confirmDotsDelete(row.entry.Name, true)...)
	case "n":
		cmds = append(cmds, m.confirmDotsDelete(row.entry.Name, false)...)
	}
	return cmds
}

func (m *Model) confirmDotsDelete(name string, keepLocal bool) []tea.Cmd {
	m.cancelConfirmationTimeout()
	m.dotsConfirmIdx = -1
	m.dotsIgnoreIdx = -1
	m.beginDotsOperation("Deleting " + name + "…")
	return []tea.Cmd{m.spinner.Tick, m.doDotsDelete(name, keepLocal)}
}

func (m *Model) handleDotsIgnoreActionKeyMsg(visible []dotsVisibleRow) []tea.Cmd {
	var cmds []tea.Cmd
	if len(visible) == 0 || m.dotsCursor < 0 || m.dotsCursor >= len(visible) {
		return cmds
	}
	row := visible[m.dotsCursor]
	if row.isChild && row.child.RelPath == "" {
		return cmds
	}
	pattern := row.child.RelPath
	if m.dotsIgnoreIdx == m.dotsCursor {
		m.cancelConfirmationTimeout()
		m.dotsIgnoreIdx = -1
		m.dotsConfirmIdx = -1
		m.dotsOverwriteIdx = -1
		m.dotsLocalIdx = -1
		if row.isChild && row.child.Ignored {
			m.beginDotsOperation("Including " + pattern + "…")
			cmds = append(cmds, m.spinner.Tick, m.doDotsIgnore(row.entry.Name, pattern, false))
		} else if !row.isChild && app.DotStatusIgnored(row.entry) {
			if len(row.entry.Children) > 0 {
				// Merged ignored-child tree: expand to show individual children
				// instead of toggling the entire entry.
				m.dotsIgnoreIdx = -1
				m.dotsConfirmIdx = -1
				m.dotsOverwriteIdx = -1
				m.dotsLocalIdx = -1
				entryState := app.DotStatusState(row.entry)
				if m.dotsExpandedName == row.entry.Name && m.dotsExpandedState == entryState {
					m.clearDotsExpandedChildren(row.entry.Name)
					m.dotsExpandedName = ""
				} else {
					if m.dotsExpandedName != "" {
						m.clearDotsExpandedChildren(m.dotsExpandedName)
					}
					m.dotsExpandedName = row.entry.Name
					m.dotsExpandedState = entryState
				}
				return cmds
			}
			m.beginDotsOperation("Including " + row.entry.Name + "…")
			cmds = append(cmds, m.spinner.Tick, m.doDotsEntryIgnore(row.entry, false))
		} else if !row.isChild {
			m.beginDotsOperation("Ignoring " + row.entry.Name + "…")
			cmds = append(cmds, m.spinner.Tick, m.doDotsEntryIgnore(row.entry, true))
		} else {
			m.beginDotsOperation("Ignoring " + pattern + "…")
			cmds = append(cmds, m.spinner.Tick, m.doDotsIgnore(row.entry.Name, pattern, true))
		}
		return cmds
	}
	m.dotsIgnoreIdx = m.dotsCursor
	m.dotsConfirmIdx = -1
	m.dotsOverwriteIdx = -1
	m.dotsLocalIdx = -1
	cmds = append(cmds, m.armConfirmationTimeout())
	return cmds
}

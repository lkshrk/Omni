package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/profile"
	gosync "github.com/lkshrk/omni/internal/sync"
)

const actionFirstFrameDelay = 2 * time.Second / 60

type runAfterRenderMsg struct {
	cmd tea.Cmd
}

// runAfterRender gives the renderer a complete frame to publish newly-set
// loading, row-operation, and status state before any action work can start.
func runAfterRender(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return tea.Tick(actionFirstFrameDelay, func(time.Time) tea.Msg {
		return runAfterRenderMsg{cmd: cmd}
	})
}

// waitForProgress blocks on one receive from ch and emits a progressMsg.
// Channel close is reported as a stream event, not operation completion; the
// background operation returns progressDoneMsg with the final result.
func waitForProgress(ch chan progressUpdate, gen int) tea.Cmd {
	return func() tea.Msg {
		update, ok := <-ch
		if !ok {
			return progressStreamClosedMsg{gen: gen}
		}
		return progressMsg(update)
	}
}

func waitForDotsProgress(ch chan dotsProgressUpdate, gen int) tea.Cmd {
	return func() tea.Msg {
		update, ok := <-ch
		if !ok {
			return dotsProgressStreamClosedMsg{gen: gen}
		}
		return dotsProgressMsg(update)
	}
}

func (m *Model) beginProgressStream() (chan progressUpdate, int) {
	m.progressCh = nil
	m.progressGen++
	ch := make(chan progressUpdate, 16)
	m.progressCh = ch
	return ch, m.progressGen
}

func (m *Model) beginDotsProgressStream() chan dotsProgressUpdate {
	m.dotsProgressCh = nil
	ch := make(chan dotsProgressUpdate, 16)
	m.dotsProgressCh = ch
	return ch
}

func sendProgress(ch chan progressUpdate, gen int, text string) {
	select {
	case ch <- progressUpdate{gen: gen, text: text}:
	default:
	}
}

func sendDotsProgressUpdate(ch chan dotsProgressUpdate, update dotsProgressUpdate) {
	if ch == nil {
		return
	}
	select {
	case ch <- update:
	default:
	}
}

func (m *Model) sendToolProgressUpdate(ch chan progressUpdate, gen int, event gosync.ProgressEvent) {
	update := toolProgressUpdate(gen, event)
	sendProgressUpdate(ch, update)
}

func (m *Model) sendSyncAllToolProgressUpdate(ch chan progressUpdate, gen int, event gosync.ProgressEvent, text string) {
	update := toolProgressUpdate(gen, event)
	if text != "" {
		update.text = text
	}
	if event.Done {
		if strings.HasPrefix(event.Message, "Added ") || strings.HasPrefix(event.Message, "Would add ") {
			update.claimedNames = []string{event.Tool.Name}
		}
	}
	sendProgressUpdate(ch, update)
}

func (m *Model) sendUpgradeAllToolProgressUpdate(ch chan progressUpdate, gen int, event gosync.ProgressEvent, text string) {
	update := toolProgressUpdate(gen, event)
	if text != "" {
		update.text = text
	}
	sendProgressUpdate(ch, update)
}

func toolProgressUpdate(gen int, event gosync.ProgressEvent) progressUpdate {
	key := toolKey(event.Tool.Name, event.Tool.Provider)
	update := progressUpdate{gen: gen, text: event.Message, rowKey: key}
	if isContextCanceled(event.Err) {
		update.rowDone = true
	} else if event.Err != nil {
		update.rowErr = event.Err.Error()
		update.rowDone = true
	} else if event.Done {
		update.rowDone = true
	} else {
		update.rowStatus = event.Message
	}
	return update
}

func isContextCanceled(err error) bool {
	return errors.Is(err, context.Canceled)
}

func sendProgressUpdate(ch chan progressUpdate, update progressUpdate) {
	select {
	case ch <- update:
	default:
	}
}

const (
	statusDurationOK  = 1500 * time.Millisecond
	statusDurationErr = 8000 * time.Millisecond // errors need more time to read
)

// setStatus sets the status message with appropriate styling and schedules
// auto-clear. Error messages are shown in red for twice as long as successful
// ones. Incrementing statusGen ensures stale timers from prior messages are
// ignored — newer activities always win.
func setStatus(m *Model, text string, isErr bool) tea.Cmd {
	return setStatusFor(m, text, isErr, statusDurationOK)
}

func setStatusFor(m *Model, text string, isErr bool, okDuration time.Duration) tea.Cmd {
	m.statusGen++
	m.progressText = ""
	if isErr {
		// Normalize multi-line errors (e.g. stow stderr) to a single readable line.
		text = strings.ReplaceAll(text, "\n", "  ")
		text = strings.ReplaceAll(text, "\r", "")
		if runes := []rune(text); len(runes) > 160 {
			text = string(runes[:157]) + "…"
		}
	}
	m.statusMsg = text
	m.statusIsErr = isErr
	d := okDuration
	if isErr {
		d = statusDurationErr
	}
	gen := m.statusGen
	return func() tea.Msg {
		time.Sleep(d)
		return clearStatusMsg{gen: gen}
	}
}

func startOp(m *Model, message string) {
	m.statusGen++
	m.progressText = ""
	m.statusMsg = message
	m.statusIsErr = false
}

func finishOpOK(m *Model, message string) tea.Cmd {
	return setStatus(m, "✓ "+message, false)
}

func clearStatus(m *Model) {
	m.statusGen++
	m.progressText = ""
	m.statusMsg = ""
	m.statusIsErr = false
}

func setActivityStatus(m *Model, text string) {
	m.progressText = text
}

func (m *Model) beginLaunchBatchIfPending() {
	if !m.launchBatchPending() {
		return
	}
	m.launchBatchActive = true
	m.launchBatchErrors = nil
	m.launchBatchStatus = m.statusGen
}

func (m Model) launchBatchPending() bool {
	return m.dotsLoading ||
		len(m.scanningProviders) > 0 ||
		m.providerSnapshotRefreshing ||
		m.discoveryRefreshing ||
		m.descRefreshing
}

func (m *Model) collectLaunchBatchError(text string) bool {
	if !m.launchBatchActive {
		return false
	}
	text = strings.TrimSpace(text)
	if text != "" {
		m.launchBatchErrors = append(m.launchBatchErrors, text)
	}
	return true
}

func (m *Model) finishLaunchBatchIfIdle() tea.Cmd {
	if !m.launchBatchActive || m.launchBatchPending() {
		return nil
	}
	errors := append([]string(nil), m.launchBatchErrors...)
	statusGen := m.launchBatchStatus
	m.launchBatchActive = false
	m.launchBatchErrors = nil
	m.launchBatchStatus = 0
	m.progressText = ""
	if len(errors) == 0 {
		if m.statusGen == statusGen {
			clearStatus(m)
		}
		return nil
	}
	return setStatus(m, launchBatchErrorStatus(errors), true)
}

func launchBatchErrorStatus(errors []string) string {
	if len(errors) == 1 {
		return "✗ " + errors[0]
	}
	return fmt.Sprintf("✗ launch completed with %d errors: %s", len(errors), strings.Join(errors, "; "))
}

// doSyncWithProgress triggers a background sync with progress streaming.
func (m *Model) doSyncWithProgress(ch chan progressUpdate, gen int) tea.Cmd {
	a, ctx := m.app, m.beginCancellableAction()
	return func() tea.Msg {
		defer close(ch)
		stateResult, err := a.SyncWithState(ctx, gosync.SyncOptions{
			Progress: func(s string) {
				sendProgress(ch, gen, s)
			},
			ToolProgress: func(event gosync.ProgressEvent) {
				m.sendToolProgressUpdate(ch, gen, event)
			},
			SkipPrivileged: true,
		})
		if stateResult == nil {
			if err == nil {
				err = fmt.Errorf("sync result unavailable")
			}
			return progressDoneMsg{gen: gen, err: err}
		}
		result := stateResult.Result
		if result == nil {
			if err == nil {
				err = fmt.Errorf("sync result unavailable")
			}
			return progressDoneMsg{gen: gen, err: err}
		}
		msg := app.SyncResultSummaryText(result, "install complete")
		groupState := stateResult.State
		rows := app.SyncFailureRows(result)
		if len(rows.RowErrors) > 0 {
			msg = app.BulkToolFailureSummaryText(msg, rows)
			err = nil
		}
		return progressDoneMsg{
			gen:                     gen,
			message:                 msg,
			err:                     err,
			tools:                   stateResult.Tools,
			groupNames:              groupState.GroupNames,
			toolGroups:              groupState.ToolGroups,
			toolMemberships:         groupState.ToolMemberships,
			rowErrors:               rows.RowErrors,
			rowActionErrors:         rows.RowActionErrors,
			promptPrivilegedActions: rows.PrivilegedActions,
		}
	}
}

// doSyncAllWithProgress installs configured missing tools and adds currently
// discovered local tools to this machine's hostname group.
func (m *Model) doSyncAllWithProgress(ch chan progressUpdate, gen int, discovered []*app.ToolView) tea.Cmd {
	a, ctx := m.app, m.beginCancellableAction()
	total := app.SyncAllProgressTotal(m.allTools, discovered)
	return func() tea.Msg {
		defer close(ch)
		current := 0
		started := make(map[string]bool)
		stateResult, err := a.SyncAllWithState(ctx, app.SyncAllOptions{
			Discovered:     discovered,
			SkipPrivileged: true,
			Progress: func(s string) {
				sendProgress(ch, gen, app.SyncAllPhaseProgressText(s, total))
			},
			ToolProgress: func(event gosync.ProgressEvent) {
				key := toolKey(event.Tool.Name, event.Tool.Provider)
				if !event.Done && event.Err == nil {
					if !started[key] {
						current++
						started[key] = true
					}
				} else if event.Done && !started[key] {
					current++
					started[key] = true
				}
				m.sendSyncAllToolProgressUpdate(ch, gen, event, app.SyncAllToolProgressText(event, current, total))
			},
		})
		if stateResult == nil {
			if err == nil {
				err = fmt.Errorf("sync result unavailable")
			}
			return progressDoneMsg{gen: gen, err: err}
		}
		result := stateResult.Result
		groupState := stateResult.State
		claimedNames := syncAllClaimedNames(result)
		msg := app.SyncAllSummaryText(result, "sync complete")
		rows := app.SyncAllFailureRows(result)
		if len(rows.RowErrors) > 0 {
			msg = app.BulkToolFailureSummaryText(msg, rows)
			// SyncAll returns errors.Join(claimErr, syncErr); each joined error
			// is already represented in result.Failures and rows.RowErrors.
			// Clear err so the status bar shows the summary message instead of
			// duplicating per-tool failures already attached to row entries.
			err = nil
		}
		return progressDoneMsg{
			gen:                     gen,
			message:                 msg,
			err:                     err,
			tools:                   stateResult.Tools,
			claimedNames:            claimedNames,
			toolGroups:              groupState.ToolGroups,
			toolMemberships:         groupState.ToolMemberships,
			groupNames:              groupState.GroupNames,
			rowErrors:               rows.RowErrors,
			rowActionErrors:         rows.RowActionErrors,
			promptPrivilegedActions: rows.PrivilegedActions,
		}
	}
}

func syncAllClaimedNames(result *app.SyncAllResult) []string {
	if result == nil {
		return nil
	}
	return result.ClaimedNames
}

// anyMissingDescription reports whether any tool in the list lacks a cached
// description. Used to skip the background description warm-up on launches
// where every tool is already populated.
func anyMissingDescription(tools []*app.ToolView) bool {
	for _, t := range tools {
		if t.Description == "" {
			return true
		}
	}
	return false
}

// doScanProvider scans a single named provider's installed state. A 45-second
// timeout prevents slow local package-manager calls from blocking the UI
// indefinitely. Does NOT call ListTools — all goroutines must finish their
// upserts before a consistent snapshot can be read. The final ListTools is done
// by doFetchFinalTools once every providerScannedMsg has arrived.
func (m *Model) doScanProvider(provName string, gen int, progressCh chan progressUpdate, progressGen int) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		defer profile.Start("tui.refresh.installed.provider." + provName)()

		installCtx, cancelInstall := context.WithTimeout(ctx, 45*time.Second)
		installErr := a.RefreshProviderInstalledWithProgress(installCtx, provName, func(event app.RefreshInstalledProgressEvent) {
			sendProgressUpdate(progressCh, progressUpdate{
				gen:                  progressGen,
				refreshProvider:      event.Provider,
				refreshProviderLabel: event.ProviderLabel,
				refreshToolName:      event.Name,
			})
		})
		cancelInstall()

		return providerScannedMsg{gen: gen, provider: provName, err: installErr}
	}
}

// doFetchFinalTools fetches a consistent tool list after all per-provider scan
// goroutines have completed their upserts. This single call avoids the race
// where concurrent ListTools calls produce snapshots that are missing each
// other's upserts.
func (m *Model) doFetchFinalTools(gen int) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		defer profile.Start("tui.refresh.installed.final_tools")()

		snapshot, err := a.ToolDisplaySnapshot(ctx)
		if err != nil {
			return allProvidersDoneMsg{gen: gen, err: err}
		}
		return allProvidersDoneMsg{
			gen:                    gen,
			tools:                  snapshot.Tools,
			effectiveSystemManager: snapshot.EffectiveSystemManager,
		}
	}
}

func (m *Model) doCheckProviderOutdated(provName string, gen int) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		defer profile.Start("tui.refresh.outdated.provider." + provName)()

		outdatedCtx, cancelOutdated := context.WithTimeout(ctx, 45*time.Second)
		outdatedErr := a.RefreshProviderOutdated(outdatedCtx, provName, true)
		cancelOutdated()
		return providerOutdatedCheckedMsg{gen: gen, provider: provName, err: outdatedErr}
	}
}

func (m *Model) doFetchOutdatedTools(gen int) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		defer profile.Start("tui.refresh.outdated.final_tools")()

		snapshot, err := a.ToolDisplaySnapshot(ctx)
		if err != nil {
			return outdatedProvidersDoneMsg{gen: gen, err: err}
		}
		return outdatedProvidersDoneMsg{
			gen:                    gen,
			tools:                  snapshot.Tools,
			effectiveSystemManager: snapshot.EffectiveSystemManager,
		}
	}
}

// doRefreshDiscovered scans all providers for locally-installed tools that are
// not in the config (orphan scan). Runs as a separate background pass so it
// does not delay the installedRefreshedMsg signal.
func (m *Model) doRefreshDiscovered(gen int, progressCh chan progressUpdate, progressGen int) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		defer profile.Start("tui.refresh.discovered.total")()

		if progressCh != nil {
			defer close(progressCh)
		}
		if err := a.RefreshDiscoveredWithProgress(ctx, func(event app.RefreshDiscoveredProgressEvent) {
			sendProgress(progressCh, progressGen, app.RefreshDiscoveredProgressText(event))
		}); err != nil {
			return discoveredRefreshedMsg{gen: gen, err: err}
		}
		snapshot, err := a.ToolDisplaySnapshot(ctx)
		if err != nil {
			return discoveredRefreshedMsg{gen: gen, err: err}
		}
		return discoveredRefreshedMsg{gen: gen, discovered: snapshot.Discovered}
	}
}

// doRefreshDescriptions pre-warms the description cache for configured and
// discovered tools that don't have one yet.
func (m *Model) startDescriptionRefresh() tea.Cmd {
	m.descRefreshGen++
	m.descRefreshing = true
	// Background refresh: take over the shared status stream only when no
	// other operation owns it, mirroring the scan-settle branch in
	// handleProviderScannedMsg — beginning a stream here would bump
	// progressGen and drop the active operation's remaining progress updates.
	if m.progressCh != nil {
		return m.doRefreshDescriptions(m.descRefreshGen, nil, 0)
	}
	setActivityStatus(m, "Refreshing tool descriptions…")
	ch, progressGen := m.beginProgressStream()
	return tea.Batch(m.doRefreshDescriptions(m.descRefreshGen, ch, progressGen), waitForProgress(ch, progressGen))
}

func (m *Model) doRefreshDescriptions(gen int, progressCh chan progressUpdate, progressGen int) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		defer profile.Start("tui.refresh.descriptions.total")()

		if progressCh != nil {
			defer close(progressCh)
		}
		if err := a.RefreshDescriptionsWithProgress(ctx, 0, func(event app.RefreshDescriptionsProgressEvent) {
			sendProgress(progressCh, progressGen, app.RefreshDescriptionsProgressText(event))
		}); err != nil {
			return descRefreshDoneMsg{gen: gen, err: err}
		}
		snapshot, err := a.ToolDisplaySnapshot(ctx)
		if err != nil {
			return descRefreshDoneMsg{gen: gen, err: err}
		}
		return descRefreshDoneMsg{gen: gen, tools: snapshot.Tools, discovered: snapshot.Discovered}
	}
}

// doUpgradeAll upgrades every outdated tool with progress streaming.
func (m *Model) doUpgradeAll(ch chan progressUpdate, gen int) tea.Cmd {
	a, ctx := m.app, m.beginCancellableAction()
	total := app.UpgradeAllProgressTotal(m.allTools)
	return func() tea.Msg {
		defer close(ch)
		current := 0
		started := make(map[string]int)
		stateResult, err := a.UpgradeAllDetailedWithState(ctx, nil, func(event gosync.ProgressEvent) {
			key := toolKey(event.Tool.Name, event.Tool.Provider)
			if !event.Done && event.Err == nil {
				if _, ok := started[key]; !ok {
					current++
					started[key] = current
				}
			} else if event.Done {
				if _, ok := started[key]; !ok {
					current++
					started[key] = current
				}
			}
			m.sendUpgradeAllToolProgressUpdate(ch, gen, event, app.UpgradeAllProgressText(event, started[key], total))
		}, app.UpgradeAllOptions{SkipPrivileged: true})
		if stateResult == nil {
			if err == nil {
				err = fmt.Errorf("upgrade result unavailable")
			}
			return progressDoneMsg{gen: gen, key: "*", err: err}
		}
		result := stateResult.Result
		tools := stateResult.Tools
		groupState := stateResult.State
		rows := app.UpgradeAllFailureRows(result)
		if len(rows.RowErrors) > 0 {
			return progressDoneMsg{
				gen:                     gen,
				key:                     "*",
				message:                 app.BulkToolFailureSummaryText("upgrades complete", rows),
				tools:                   tools,
				groupNames:              groupState.GroupNames,
				toolGroups:              groupState.ToolGroups,
				toolMemberships:         groupState.ToolMemberships,
				rowErrors:               rows.RowErrors,
				rowActionErrors:         rows.RowActionErrors,
				promptPrivilegedActions: rows.PrivilegedActions,
			}
		}
		if err != nil {
			return progressDoneMsg{
				gen:             gen,
				key:             "*",
				err:             err,
				tools:           tools,
				groupNames:      groupState.GroupNames,
				toolGroups:      groupState.ToolGroups,
				toolMemberships: groupState.ToolMemberships,
			}
		}
		return progressDoneMsg{
			gen:             gen,
			key:             "*",
			message:         "upgrades complete",
			tools:           tools,
			groupNames:      groupState.GroupNames,
			toolGroups:      groupState.ToolGroups,
			toolMemberships: groupState.ToolMemberships,
		}
	}
}

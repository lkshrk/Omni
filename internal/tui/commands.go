package tui

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/dots"
	"github.com/lkshrk/omni/internal/provider"
	gosync "github.com/lkshrk/omni/internal/sync"
)

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

func (m *Model) sendToolProgressSnapshot(ch chan progressUpdate, gen int, event gosync.ProgressEvent) {
	update := toolProgressUpdate(gen, event)
	if event.Done {
		update.tools, _ = m.app.ListTools(m.ctx, "")
	}
	sendProgressUpdate(ch, update)
}

func (m *Model) sendSyncAllToolProgressSnapshot(ch chan progressUpdate, gen int, event gosync.ProgressEvent, text string) {
	update := toolProgressUpdate(gen, event)
	if text != "" {
		update.text = text
	}
	if event.Done {
		update.tools, _ = m.app.ListTools(m.ctx, "")
		update.groupNames, update.toolGroups, update.toolMemberships = m.reloadToolGroups()
		if strings.HasPrefix(event.Message, "Added ") || strings.HasPrefix(event.Message, "Would add ") {
			update.claimedNames = []string{event.Tool.Name}
		}
	}
	sendProgressUpdate(ch, update)
}

func (m *Model) sendUpgradeAllToolProgressSnapshot(ch chan progressUpdate, gen int, event gosync.ProgressEvent, text string) {
	update := toolProgressUpdate(gen, event)
	if text != "" {
		update.text = text
	}
	if event.Done {
		update.tools, _ = m.app.ListTools(m.ctx, "")
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

func countSyncAllProgressItems(tools []*database.ToolCache, discovered []*database.ToolCache) int {
	count := 0
	for _, t := range tools {
		if t != nil && t.Tracked && !t.Installed {
			count++
		}
	}
	for _, t := range discovered {
		if t != nil && t.Name != "" && t.Provider != "" {
			count++
		}
	}
	return count
}

func syncAllPhaseProgressText(phase string, total int) string {
	label := strings.TrimSpace(strings.TrimSuffix(phase, "…"))
	switch label {
	case "reading installed packages":
		label = "checking installed state"
	case "checking providers":
		label = "checking providers"
	}
	if label == "" {
		label = "checking installed state"
	}
	if total > 0 {
		return fmt.Sprintf("Syncing tools 0/%d: %s…", total, label)
	}
	return "Syncing tools: " + label + "…"
}

func syncAllToolProgressText(event gosync.ProgressEvent, current, total int) string {
	label := syncAllToolProgressLabel(event)
	if total > 0 {
		return fmt.Sprintf("Syncing tools %d/%d: %s", current, total, label)
	}
	return "Syncing tools: " + label
}

func syncAllToolProgressLabel(event gosync.ProgressEvent) string {
	name := event.Tool.Name
	message := strings.TrimSpace(strings.TrimSuffix(event.Message, "…"))
	switch {
	case strings.HasPrefix(message, "Adding "):
		return "adding discovered " + name + " to config…"
	case strings.HasPrefix(message, "Added "):
		return "added discovered " + name + " to config"
	case strings.HasPrefix(message, "Would add "):
		return "would add discovered " + name + " to config"
	case strings.HasPrefix(message, "Failed adding "):
		return "failed adding discovered " + name + " to config"
	case strings.HasPrefix(message, "Installing "):
		return "installing missing " + name + "…"
	case strings.HasPrefix(message, "Installed "):
		return "installed missing " + name
	case strings.HasPrefix(message, "Admin approval needed for "):
		return "admin approval needed for " + name
	case strings.HasPrefix(message, "Skipped installing "):
		if progressEventNeedsAdmin(event) {
			return "admin approval needed for " + name
		}
		return "skipped missing " + name
	case strings.HasPrefix(message, "Failed installing "):
		return "failed installing missing " + name
	case strings.HasPrefix(message, "Cancelled installing "):
		return "cancelled installing missing " + name
	default:
		if message == "" {
			return name
		}
		return strings.ToLower(message)
	}
}

func progressEventNeedsAdmin(event gosync.ProgressEvent) bool {
	return event.Err != nil && isPrivilegedInstallFailure(event.Err.Error())
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
		result, err := a.Sync(ctx, gosync.SyncOptions{
			Progress: func(s string) {
				sendProgress(ch, gen, s)
			},
			ToolProgress: func(event gosync.ProgressEvent) {
				m.sendToolProgressSnapshot(ch, gen, event)
			},
			SkipPrivileged: true,
		})
		if result == nil {
			if err == nil {
				err = fmt.Errorf("sync result unavailable")
			}
			return progressDoneMsg{gen: gen, err: err}
		}
		installed := result.Installed()
		msg := "install complete"
		if len(installed) > 0 {
			msg = fmt.Sprintf("install complete — %d installed", len(installed))
		}
		tools, _ := a.ListTools(ctx, "") // non-fatal: sync succeeded; stale list retained if refresh fails
		rowErrors := syncResultRowErrors(result)
		rowActionErrors := syncResultRowActionErrors(result)
		privilegedActions := syncResultPrivilegedActions(result)
		if len(rowErrors) > 0 {
			msg = bulkCompletionMessage(msg, rowErrors, privilegedActions)
			err = nil
		}
		return progressDoneMsg{
			gen:                     gen,
			message:                 msg,
			err:                     err,
			tools:                   tools,
			rowErrors:               rowErrors,
			rowActionErrors:         rowActionErrors,
			promptPrivilegedActions: privilegedActions,
		}
	}
}

// doSyncAllWithProgress installs configured missing tools and adds currently
// discovered local tools to this machine's hostname group.
func (m *Model) doSyncAllWithProgress(ch chan progressUpdate, gen int, discovered []*database.ToolCache) tea.Cmd {
	a, ctx := m.app, m.beginCancellableAction()
	total := countSyncAllProgressItems(m.allTools, discovered)
	return func() tea.Msg {
		defer close(ch)
		current := 0
		started := make(map[string]bool)
		result, err := a.SyncAll(ctx, app.SyncAllOptions{
			Discovered:     discovered,
			SkipPrivileged: true,
			Progress: func(s string) {
				sendProgress(ch, gen, syncAllPhaseProgressText(s, total))
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
				m.sendSyncAllToolProgressSnapshot(ch, gen, event, syncAllToolProgressText(event, current, total))
			},
		})

		tools, _ := a.ListTools(ctx, "")
		groupNames, toolGroups, memberships := m.reloadToolGroups()
		claimedNames := syncAllClaimedNames(result)
		msg := syncAllMessage(result, len(claimedNames))
		rowErrors := syncAllRowErrors(result)
		rowActionErrors := syncAllRowActionErrors(result)
		privilegedActions := syncAllPrivilegedActions(result)
		if len(rowErrors) > 0 {
			msg = bulkCompletionMessage(msg, rowErrors, privilegedActions)
			// SyncAll returns errors.Join(claimErr, syncErr); each joined error
			// is already represented in result.Failures (and therefore rowErrors).
			// Clear err so the status bar shows the summary message instead of
			// duplicating per-tool failures already attached to row entries.
			err = nil
		}
		return progressDoneMsg{
			gen:                     gen,
			message:                 msg,
			err:                     err,
			tools:                   tools,
			claimedNames:            claimedNames,
			toolGroups:              toolGroups,
			toolMemberships:         memberships,
			groupNames:              groupNames,
			rowErrors:               rowErrors,
			rowActionErrors:         rowActionErrors,
			promptPrivilegedActions: privilegedActions,
		}
	}
}

func syncAllClaimedNames(result *app.SyncAllResult) []string {
	if result == nil {
		return nil
	}
	return result.ClaimedNames
}

func syncAllRowErrors(result *app.SyncAllResult) map[string]string {
	if result == nil || len(result.Failures) == 0 {
		return nil
	}
	return bulkToolErrorsToRowErrors(result.Failures)
}

func syncAllPrivilegedActions(result *app.SyncAllResult) map[string]provider.PrivilegeAction {
	if result == nil {
		return nil
	}
	return syncResultPrivilegedActions(result.SyncResult)
}

func (m *Model) reloadToolGroups() ([]string, map[string]string, map[string][]string) {
	groups, _ := m.app.Groups(m.ctx)
	memberships, _ := m.app.ToolMembershipMap(m.ctx)
	return buildGroupNames(groups), compactToolGroupMapForHost(memberships, m.hostInfo), memberships
}

func (m *Model) reloadToolContext() ([]string, map[string]string, map[string][]string, *app.HostInfo) {
	groups, _ := m.app.Groups(m.ctx)
	memberships, _ := m.app.ToolMembershipMap(m.ctx)
	info, _ := m.app.HostStatus()
	if info == nil {
		info = m.hostInfo
	}
	return buildGroupNames(groups), compactToolGroupMapForHost(memberships, info), memberships, info
}

func syncResultRowErrors(result *gosync.SyncResult) map[string]string {
	if result == nil {
		return nil
	}
	failed := result.Failed()
	rowErrors := make(map[string]string, len(failed))
	for _, op := range failed {
		if op.Err == nil || op.Tool.Name == "" || op.Tool.Provider == "" {
			continue
		}
		if isContextCanceled(op.Err) {
			continue
		}
		rowErrors[toolKey(op.Tool.Name, op.Tool.Provider)] = op.Err.Error()
	}
	if len(rowErrors) == 0 {
		return nil
	}
	return rowErrors
}

func syncResultRowActionErrors(result *gosync.SyncResult) map[string]*provider.ActionError {
	if result == nil {
		return nil
	}
	failed := result.Failed()
	rowActionErrors := make(map[string]*provider.ActionError, len(failed))
	for _, op := range failed {
		if op.Err == nil || op.Tool.Name == "" || op.Tool.Provider == "" || isContextCanceled(op.Err) {
			continue
		}
		if actionErr, ok := provider.ActionErrorFrom(op.Err); ok {
			rowActionErrors[toolKey(op.Tool.Name, op.Tool.Provider)] = actionErr
		}
	}
	if len(rowActionErrors) == 0 {
		return nil
	}
	return rowActionErrors
}

func syncResultPrivilegedActions(result *gosync.SyncResult) map[string]provider.PrivilegeAction {
	if result == nil {
		return nil
	}
	actions := make(map[string]provider.PrivilegeAction)
	for _, op := range result.Failed() {
		action, ok := syncOpPrivilegedAction(op)
		if !ok || op.Tool.Name == "" || op.Tool.Provider == "" {
			continue
		}
		actions[toolKey(op.Tool.Name, op.Tool.Provider)] = action
	}
	if len(actions) == 0 {
		return nil
	}
	return actions
}

func syncOpPrivilegedAction(op gosync.SyncOp) (provider.PrivilegeAction, bool) {
	if op.Err == nil || isContextCanceled(op.Err) || !isPrivilegedInstallFailure(op.Err.Error()) {
		return "", false
	}
	switch op.Kind {
	case gosync.OpInstall, gosync.OpFailed:
		return provider.PrivilegeActionInstall, true
	case gosync.OpUninstall:
		return provider.PrivilegeActionUninstall, true
	default:
		return "", false
	}
}

func bulkCompletionMessage(base string, rowErrors map[string]string, privilegedActions map[string]provider.PrivilegeAction) string {
	if len(rowErrors) == 0 {
		return base
	}
	adminNeeded := 0
	for key := range rowErrors {
		if _, ok := privilegedActions[key]; ok {
			adminNeeded++
		}
	}
	failed := len(rowErrors) - adminNeeded
	switch {
	case adminNeeded > 0 && failed > 0:
		return fmt.Sprintf("%s, %d need admin approval, %d failed", base, adminNeeded, failed)
	case adminNeeded > 0:
		return fmt.Sprintf("%s, %d need admin approval", base, adminNeeded)
	default:
		return fmt.Sprintf("%s, %d failed", base, len(rowErrors))
	}
}

func syncAllMessage(result *app.SyncAllResult, claimed int) string {
	installed := 0
	normalized := 0
	if result != nil && result.SyncResult != nil {
		installed = len(result.SyncResult.Installed())
	}
	if result != nil {
		normalized = len(result.NormalizedProviderOverrides)
	}
	msg := fmt.Sprintf("sync complete — %d installed, %d added to config", installed, claimed)
	if normalized > 0 {
		msg = fmt.Sprintf("%s, %d provider overrides normalized", msg, normalized)
	}
	return msg
}

// doSaveNodeManager persists the chosen node manager to host_settings.
// Used by setup wizard step 3.
func (m *Model) doSaveNodeManager(manager string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		return setupNodeMgrDoneMsg{err: a.SaveNodeManager(ctx, manager)}
	}
}

// anyMissingDescription reports whether any tool in the list lacks a cached
// description. Used to skip the background description warm-up on launches
// where every tool is already populated.
func anyMissingDescription(tools []*database.ToolCache) bool {
	for _, t := range tools {
		if !t.Description.Valid || t.Description.String == "" {
			return true
		}
	}
	return false
}

// doScanProvider scans a single named provider: updates install status and
// checks for outdated tools. A 45-second timeout prevents slow network calls
// (e.g. brew outdated) from blocking the UI indefinitely. Does NOT call
// ListTools — all goroutines must finish their upserts before a consistent
// snapshot can be read. The final ListTools is done by doFetchFinalTools once
// every providerScannedMsg has arrived.
func (m *Model) doScanProvider(provName string, gen int) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		installCtx, cancelInstall := context.WithTimeout(ctx, 45*time.Second)
		installErr := a.RefreshProviderInstalled(installCtx, provName)
		cancelInstall()

		outdatedCtx, cancelOutdated := context.WithTimeout(ctx, 45*time.Second)
		outdatedErr := a.RefreshProviderOutdated(outdatedCtx, provName)
		cancelOutdated()

		return providerScannedMsg{gen: gen, provider: provName, err: errors.Join(installErr, outdatedErr)}
	}
}

// doFetchFinalTools fetches a consistent tool list after all per-provider scan
// goroutines have completed their upserts. This single call avoids the race
// where concurrent ListTools calls produce snapshots that are missing each
// other's upserts.
func (m *Model) doFetchFinalTools(gen int) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		tools, err := a.ListTools(ctx, "")
		if err != nil {
			return allProvidersDoneMsg{gen: gen, err: err}
		}
		ecosystemMap := a.ResolvedEcosystemProviders(ctx)
		return allProvidersDoneMsg{
			gen:                    gen,
			tools:                  tools,
			effectiveSystemManager: ecosystemMap[provider.EcosystemSystem],
		}
	}
}

// doRefreshDiscovered scans all providers for locally-installed tools that are
// not in the config (orphan scan). Runs as a separate background pass so it
// does not delay the installedRefreshedMsg signal.
func (m *Model) doRefreshDiscovered(gen int) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		if err := a.RefreshDiscovered(ctx); err != nil {
			return discoveredRefreshedMsg{gen: gen, err: err}
		}
		discovered, err := a.ListDiscovered(ctx)
		if err != nil {
			return discoveredRefreshedMsg{gen: gen, err: err}
		}
		return discoveredRefreshedMsg{gen: gen, discovered: discovered}
	}
}

// doRefreshDescriptions pre-warms the description cache for configured and
// discovered tools that don't have one yet.
func (m *Model) startDescriptionRefresh() tea.Cmd {
	m.descRefreshGen++
	m.descRefreshing = true
	setActivityStatus(m, "Refreshing tool descriptions…")
	return m.doRefreshDescriptions(m.descRefreshGen)
}

func (m *Model) doRefreshDescriptions(gen int) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		if err := a.RefreshDescriptions(ctx, 0); err != nil {
			return descRefreshDoneMsg{gen: gen, err: err}
		}
		tools, _ := a.ListTools(ctx, "") // non-fatal: stale descriptions remain if list refresh fails
		return descRefreshDoneMsg{gen: gen, tools: tools}
	}
}

// doUpgradeAll upgrades every outdated tool with progress streaming.
func (m *Model) doUpgradeAll(ch chan progressUpdate, gen int) tea.Cmd {
	a, ctx := m.app, m.beginCancellableAction()
	total := countUpgradeableTools(m.allTools)
	return func() tea.Msg {
		defer close(ch)
		current := 0
		started := make(map[string]int)
		result, err := a.UpgradeAllDetailedWithOptions(ctx, nil, func(event gosync.ProgressEvent) {
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
			m.sendUpgradeAllToolProgressSnapshot(ch, gen, event, upgradeAllProgressText(event, started[key], total))
		}, app.UpgradeAllOptions{SkipPrivileged: true})
		tools, _ := a.ListTools(ctx, "") // non-fatal: upgrade succeeded; stale list retained if refresh fails
		rowErrors := upgradeAllRowErrors(result)
		rowActionErrors := upgradeAllRowActionErrors(result)
		privilegedActions := upgradeAllPrivilegedActions(result)
		if len(rowErrors) > 0 {
			return progressDoneMsg{gen: gen, key: "*", message: bulkCompletionMessage("upgrades complete", rowErrors, privilegedActions), tools: tools, rowErrors: rowErrors, rowActionErrors: rowActionErrors, promptPrivilegedActions: privilegedActions}
		}
		if err != nil {
			return progressDoneMsg{gen: gen, key: "*", err: err, tools: tools}
		}
		return progressDoneMsg{gen: gen, key: "*", message: "upgrades complete", tools: tools}
	}
}

func countUpgradeableTools(tools []*database.ToolCache) int {
	count := 0
	for _, t := range tools {
		if t != nil && t.Installed && t.Outdated {
			count++
		}
	}
	return count
}

func upgradeAllProgressText(event gosync.ProgressEvent, current, total int) string {
	label := upgradeAllProgressLabel(event)
	if total > 0 {
		return fmt.Sprintf("Upgrading tools %d/%d: %s", current, total, label)
	}
	return "Upgrading tools: " + label
}

func upgradeAllProgressLabel(event gosync.ProgressEvent) string {
	name := event.Tool.Name
	message := strings.TrimSpace(strings.TrimSuffix(event.Message, "…"))
	switch {
	case strings.HasPrefix(message, "Upgrading "):
		return name + "…"
	case strings.HasPrefix(message, "Upgraded "):
		return name + " upgraded"
	case strings.HasPrefix(message, "Admin approval needed for "):
		return name + " needs admin approval"
	case strings.HasPrefix(message, "Skipped upgrading "):
		if progressEventNeedsAdmin(event) {
			return name + " needs admin approval"
		}
		return name + " skipped"
	case strings.HasPrefix(message, "Failed upgrading "):
		return name + " failed"
	default:
		if message == "" {
			return name
		}
		return strings.ToLower(message)
	}
}

func upgradeAllRowErrors(result *app.UpgradeAllResult) map[string]string {
	if result == nil || len(result.Failures) == 0 {
		return nil
	}
	return bulkToolErrorsToRowErrors(result.Failures)
}

func upgradeAllRowActionErrors(result *app.UpgradeAllResult) map[string]*provider.ActionError {
	if result == nil || len(result.Failures) == 0 {
		return nil
	}
	return bulkToolErrorsToRowActionErrors(result.Failures)
}

func upgradeAllPrivilegedActions(result *app.UpgradeAllResult) map[string]provider.PrivilegeAction {
	if result == nil || len(result.Failures) == 0 {
		return nil
	}
	actions := make(map[string]provider.PrivilegeAction, len(result.Failures))
	for _, failure := range result.Failures {
		if failure.Name == "" || failure.Provider == "" || !isPrivilegedInstallFailure(failure.Message) {
			continue
		}
		actions[toolKey(failure.Name, failure.Provider)] = provider.PrivilegeActionUpgrade
	}
	if len(actions) == 0 {
		return nil
	}
	return actions
}

func syncAllRowActionErrors(result *app.SyncAllResult) map[string]*provider.ActionError {
	if result == nil || len(result.Failures) == 0 {
		return nil
	}
	return bulkToolErrorsToRowActionErrors(result.Failures)
}

func bulkToolErrorsToRowErrors(failures []app.BulkToolError) map[string]string {
	rowErrors := make(map[string]string, len(failures))
	for _, failure := range failures {
		if failure.Name == "" || failure.Provider == "" || failure.Message == "" {
			continue
		}
		rowErrors[toolKey(failure.Name, failure.Provider)] = failure.Message
	}
	if len(rowErrors) == 0 {
		return nil
	}
	return rowErrors
}

func bulkToolErrorsToRowActionErrors(failures []app.BulkToolError) map[string]*provider.ActionError {
	rowActionErrors := make(map[string]*provider.ActionError, len(failures))
	for _, failure := range failures {
		if failure.Name == "" || failure.Provider == "" || failure.ActionError == nil {
			continue
		}
		rowActionErrors[toolKey(failure.Name, failure.Provider)] = failure.ActionError
	}
	if len(rowActionErrors) == 0 {
		return nil
	}
	return rowActionErrors
}

// doInstall installs a single tool.
func (m *Model) doInstall(name, prov string) tea.Cmd {
	a, ctx := m.app, m.beginCancellableAction()
	return func() tea.Msg {
		if err := a.Install(ctx, name, prov); err != nil {
			return opCompleteMsg{err: err}
		}
		tools, _ := a.ListTools(ctx, "") // non-fatal: install succeeded; stale list retained if refresh fails
		return opCompleteMsg{message: "installed " + name, tools: tools, removeDiscoveredKeys: []string{toolKey(name, prov)}}
	}
}

// doDelete deletes a single tool.
func (m *Model) doDelete(name, prov string) tea.Cmd {
	a, ctx := m.app, m.beginCancellableAction()
	return func() tea.Msg {
		if err := a.Uninstall(ctx, name, prov); err != nil {
			return opCompleteMsg{err: err}
		}
		tools, _ := a.ListTools(ctx, "") // non-fatal: delete succeeded; stale list retained if refresh fails
		groupNames, toolGroups, memberships, info := m.reloadToolContext()
		removeDiscovered := []string{toolKey(name, prov)}
		return opCompleteMsg{message: "deleted " + name, tools: tools, removeDiscoveredKeys: removeDiscovered, groupNames: groupNames, toolGroups: toolGroups, toolMemberships: memberships, hostInfo: info}
	}
}

// doDeleteFromConfig deletes a missing tool from settings.json without
// calling a package manager.
func (m *Model) doDeleteFromConfig(name, prov string) tea.Cmd {
	a, ctx := m.app, m.beginCancellableAction()
	return func() tea.Msg {
		if err := a.RemoveToolFromConfig(ctx, name, prov); err != nil {
			return opCompleteMsg{err: err}
		}
		tools, _ := a.ListTools(ctx, "") // non-fatal: config update succeeded
		groupNames, toolGroups, memberships, info := m.reloadToolContext()
		return opCompleteMsg{message: "deleted " + name + " from config", tools: tools, groupNames: groupNames, toolGroups: toolGroups, toolMemberships: memberships, hostInfo: info}
	}
}

// doUpgrade upgrades a single tool.
func (m *Model) doUpgrade(name, prov string) tea.Cmd {
	a, ctx := m.app, m.beginCancellableAction()
	uk := toolKey(name, prov)
	return func() tea.Msg {
		if err := a.Upgrade(ctx, name, prov); err != nil {
			return opCompleteMsg{key: uk, err: err}
		}
		tools, _ := a.ListTools(ctx, "") // non-fatal: upgrade succeeded; stale list retained if refresh fails
		return opCompleteMsg{key: uk, message: "upgraded " + name, tools: tools}
	}
}

// cancelSearch cancels any in-flight provider search HTTP request.
// Callers are responsible for incrementing searchGen to invalidate pending
// debounced messages.
func (m *Model) cancelSearch() {
	if m.searchCancel != nil {
		m.searchCancel()
		m.searchCancel = nil
	}
}

// startSearch cancels any in-flight search, increments the generation counter,
// sets the searching flag, and returns the commands needed to run doSearch.
// It is the single entry point for triggering a provider search.
func (m *Model) startSearch(query string) []tea.Cmd {
	if m.searchCancel != nil {
		m.searchCancel()
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.searchCancel = cancel
	m.searching = true
	clearStatus(m) // clear stale text so activityLabel renders "Searching…"
	return []tea.Cmd{m.spinner.Tick, m.doSearch(ctx, query, m.searchGen)}
}

// debounceSearch returns a command that sleeps for the debounce delay and then
// emits debouncedSearchMsg. When the user types faster than the delay, the
// generation counter will have advanced and the stale message is dropped.
func debounceSearch(query string, gen int) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(300 * time.Millisecond)
		return debouncedSearchMsg{query: query, gen: gen}
	}
}

// doSearch runs a provider search within ctx and returns searchResultsMsg.
// If ctx is cancelled before the search completes, nil is returned so that
// Bubbletea dispatches no message and the spinner is cleared by the next
// startSearch call instead.
func (m *Model) doSearch(ctx context.Context, query string, gen int) tea.Cmd {
	a := m.app
	// Pass the active provider pill as a filter so the network call is scoped
	// to the selected provider rather than fanning out to all providers.
	providerFilter := m.currentSearchProviderFilter()
	return func() tea.Msg {
		results, err := a.Search(ctx, query, providerFilter)
		if ctx.Err() != nil {
			return nil // cancelled — next search (or Esc) owns the searching flag
		}
		tools := make([]*database.ToolCache, 0, len(results))
		for _, r := range results {
			t := &database.ToolCache{
				Name:          r.Name,
				Provider:      r.Provider,
				InstalledWith: searchResultDisplayProvider(r),
			}
			if r.Version != "" {
				t.Version = sql.NullString{String: r.Version, Valid: true}
			}
			if r.Description != "" {
				t.Description = sql.NullString{String: r.Description, Valid: true}
			}
			if r.Privilege.RequiresPrivilege() {
				t.Privilege = string(r.Privilege.Requirement)
				t.PrivilegeReason = sql.NullString{String: r.Privilege.Reason, Valid: r.Privilege.Reason != ""}
			}
			tools = append(tools, t)
		}
		return searchResultsMsg{gen: gen, query: query, providerFilter: providerFilter, tools: tools, err: err}
	}
}

func searchResultDisplayProvider(r provider.SearchResult) string {
	if r.SourceProvider == "" || r.SourceProvider == r.Provider {
		return ""
	}
	if providerEcosystem(r.SourceProvider) != providerEcosystem(r.Provider) {
		return ""
	}
	return r.SourceProvider
}

// doCreateConfig creates an empty settings.json and reloads.
func (m *Model) doCreateConfig() tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		if err := a.CreateEmptyConfig(); err != nil {
			return toolsLoadedMsg{err: err}
		}
		tools, err := a.ListTools(ctx, "")
		if err != nil {
			return toolsLoadedMsg{err: err}
		}
		settings, _ := a.LoadSettings() // non-fatal: setup proceeds with zero-value settings if this fails
		taps, _ := a.LoadTaps()         // non-fatal: setup proceeds with no taps if this fails
		// Build provider picker rows for setup step 1. These require probing
		// available managers; must be done here because loadTools returns early
		// with noConfig=true when HasConfig() is false (step 0 doesn't need
		// the provider list, step 1 does).
		allPyBins, allNodeBins := a.AllAvailableManagers()
		ecosystemMap := a.ResolvedEcosystemProviders(ctx)
		spRows := buildSetupProvidersFromManagers(ecosystemMap, allPyBins, allNodeBins, settings)
		return toolsLoadedMsg{
			tools:              tools,
			settings:           settings,
			taps:               taps,
			setupProviders:     spRows,
			ecosystemProviders: a.EcosystemProviderNames(),
		}
	}
}

// doSetupImport runs omni import during the setup wizard and returns setupImportDoneMsg.
// enabled is the list of ecosystem provider names the user chose to keep active.
// disabled is the list to persist as host_settings.DisabledProviders.
func (m *Model) doSetupImport(disabled []string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		result, err := a.Import(ctx, app.ImportOptions{SkipEcosystemProviders: disabled})
		if err != nil {
			return setupImportDoneMsg{err: err}
		}
		if len(disabled) > 0 {
			if err := a.SaveDisabledProviders(ctx, disabled); err != nil {
				return setupImportDoneMsg{err: err}
			}
		}
		tools, _ := a.ListTools(ctx, "")
		groupNames, toolGroups, _ := m.reloadToolGroups()
		hostInfo, _ := a.HostStatus()
		return setupImportDoneMsg{
			added:      len(result.Added),
			tools:      tools,
			toolGroups: toolGroups,
			groupNames: groupNames,
			hostInfo:   hostInfo,
		}
	}
}

// shortHostname returns the first label of the machine's hostname (e.g.
// "macbook" from "macbook.local"). Respects OMNI_HOSTNAME override.
func shortHostname() string {
	h := strings.TrimSpace(os.Getenv("OMNI_HOSTNAME"))
	if h == "" {
		host, err := os.Hostname()
		if err != nil {
			return "localhost"
		}
		h = strings.TrimSpace(host)
		if h == "" {
			return "localhost"
		}
	}
	if idx := strings.IndexByte(h, '.'); idx >= 0 {
		return h[:idx]
	}
	return h
}

// doSetupHost creates a host entry for the current machine.
func (m *Model) doSetupHost(name string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		hostname := shortHostname()
		if strings.TrimSpace(name) == "" {
			return setupHostDoneMsg{err: fmt.Errorf("hostname is required")}
		}
		if err := a.EnsureHost(hostname); err != nil {
			return setupHostDoneMsg{err: err}
		}
		info, err := a.HostStatus()
		if err != nil {
			return setupHostDoneMsg{err: err}
		}
		return setupHostDoneMsg{hostName: hostname, info: info}
	}
}

func (m *Model) doCopyHostGroupsFrom(host string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		info, err := a.HostStatus()
		if err != nil {
			return hostCopiedMsg{err: err, host: host}
		}
		source, ok := info.Hosts[host]
		if !ok {
			return hostCopiedMsg{err: fmt.Errorf("host %q not found", host), host: host}
		}
		if err := a.SetHostGroups(shortHostname(), source.Groups); err != nil {
			return hostCopiedMsg{err: err, host: host}
		}
		info, err = a.HostStatus()
		if err != nil {
			return hostCopiedMsg{err: err, host: host}
		}
		return hostCopiedMsg{host: host, info: info}
	}
}

func (m *Model) doSetupCopyHostConfigFrom(source string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		target := shortHostname()
		if err := a.CopyHostConfig(source, target); err != nil {
			return setupHostCopyDoneMsg{err: err, source: source, target: target}
		}
		info, err := a.HostStatus()
		if err != nil {
			return setupHostCopyDoneMsg{err: err, source: source, target: target}
		}
		return setupHostCopyDoneMsg{source: source, target: target, info: info}
	}
}

func (m *Model) doSetupHostGroups(groups []string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		target := shortHostname()
		if err := a.SetHostGroups(target, groups); err != nil {
			return setupHostGroupsDoneMsg{err: err, groups: groups}
		}
		info, err := a.HostStatus()
		if err != nil {
			return setupHostGroupsDoneMsg{err: err, groups: groups}
		}
		return setupHostGroupsDoneMsg{groups: append([]string(nil), groups...), info: info}
	}
}

func (m *Model) doSetupBootstrapTools() tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		result, err := a.Sync(ctx, gosync.SyncOptions{SkipPrivileged: true})
		if err != nil {
			return setupBootstrapDoneMsg{action: "sync-tools", err: err}
		}
		installed := 0
		if result != nil {
			installed = len(result.Installed())
		}
		message := "host tools applied"
		if installed > 0 {
			message = fmt.Sprintf("host tools applied, %d installed", installed)
		}
		return setupBootstrapDoneMsg{action: "sync-tools", message: message}
	}
}

func (m *Model) doSetupBootstrapDots() tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		ops, err := a.DotsSyncContext(ctx, dots.SyncOptions{})
		if err != nil {
			return setupBootstrapDoneMsg{action: "sync-dots", err: err}
		}
		message := "dotfiles applied"
		if len(ops) > 0 {
			message = fmt.Sprintf("dotfiles applied, %d operation(s)", len(ops))
		}
		return setupBootstrapDoneMsg{action: "sync-dots", message: message}
	}
}

// doSetupDotsRepo saves the dots repo path to settings. Dotfile sync itself is
// run from the Dots tab so onboarding stays limited to configuration.
func (m *Model) doSetupDotsRepo(path string) tea.Cmd {
	a, ctx := m.app, m.ctx
	settings := m.settings
	settings.DotsRepo = path
	settings.DotsDisabled = config.BoolPtr(false)
	return func() tea.Msg {
		if err := a.SaveSettings(ctx, settings); err != nil {
			return dangerOpDoneMsg{action: "setup-dots", err: fmt.Errorf("save dots repo: %w", err)}
		}
		if _, err := a.BootstrapDotsEntries(); err != nil {
			return dangerOpDoneMsg{action: "setup-dots", err: fmt.Errorf("bootstrap dots entries: %w", err)}
		}
		return dangerOpDoneMsg{action: "setup-dots", detail: "dots configured"}
	}
}

// doSaveDisabledProviders persists the list of disabled ecosystem providers to
// host_settings for this machine. Used by setup wizard step 2.
func (m *Model) doSaveDisabledProviders(disabled []string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		err := a.SaveDisabledProviders(ctx, disabled)
		return setupProvidersDoneMsg{err: err}
	}
}

func (m *Model) doSetToolGroupMembership(name, group string, add bool) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		var err error
		if add {
			err = a.MoveToolToGroup(name, group)
		} else {
			err = a.RemoveToolFromGroup(name, group)
		}
		if err != nil {
			return groupChangedMsg{err: err}
		}
		groupNames, toolGroups, memberships := m.reloadToolGroups()
		tools, _ := a.ListTools(ctx, "")
		action := "added to"
		if !add {
			action = "removed from"
		}
		return groupChangedMsg{
			detail:          "✓ " + name + " " + action + " " + group,
			tools:           tools,
			toolGroups:      toolGroups,
			toolMemberships: memberships,
			groupNames:      groupNames,
		}
	}
}

func (m *Model) doSetToolGroupMemberships(name string, before, after, createdGroups []string, activeHost string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		for _, group := range createdGroups {
			if err := a.CreateGroup(group); err != nil {
				return groupChangedMsg{err: err}
			}
		}
		beforeSet := stringSet(before)
		afterSet := stringSet(after)
		if err := ensureMembershipGroupsOnHost(a, activeHost, afterSet); err != nil {
			return groupChangedMsg{err: err}
		}
		for group := range beforeSet {
			if !afterSet[group] {
				if err := a.RemoveToolFromGroup(name, group); err != nil {
					return groupChangedMsg{err: err}
				}
			}
		}
		for group := range afterSet {
			if !beforeSet[group] {
				if err := a.MoveToolToGroup(name, group); err != nil {
					return groupChangedMsg{err: err}
				}
			}
		}
		groups, _ := a.Groups(ctx)
		memberships, _ := a.ToolMembershipMap(ctx)
		info, _ := a.HostStatus()
		tools, _ := a.ListTools(ctx, "")
		return groupChangedMsg{
			detail:          groupMoveDetail(name, after),
			tools:           tools,
			toolGroups:      compactToolGroupMapForHost(memberships, info),
			toolMemberships: memberships,
			groupNames:      buildGroupNames(groups),
			info:            info,
		}
	}
}

func (m *Model) doSetDotGroupMemberships(name string, before, after, createdGroups []string, activeHost string) tea.Cmd {
	a, ctx := m.app, m.ctx
	_, gen := m.currentDotsOperation()
	return func() tea.Msg {
		for _, group := range createdGroups {
			if err := a.CreateGroup(group); err != nil {
				return dotsLoadedMsg{gen: gen, err: err}
			}
		}
		beforeSet := stringSet(before)
		afterSet := stringSet(after)
		if err := ensureMembershipGroupsOnHost(a, activeHost, afterSet); err != nil {
			return dotsLoadedMsg{gen: gen, err: err}
		}
		for group := range afterSet {
			if !beforeSet[group] {
				if err := a.MoveDotToGroup(name, group); err != nil {
					return dotsLoadedMsg{gen: gen, err: err}
				}
			}
		}
		for group := range beforeSet {
			if !afterSet[group] {
				if err := a.RemoveDotFromGroup(name, group); err != nil {
					return dotsLoadedMsg{gen: gen, err: err}
				}
			}
		}
		result, err := a.DiscoverDotsStatus(ctx)
		if err != nil {
			return dotsLoadedMsg{gen: gen, err: err}
		}
		return dotsLoadedMsg{
			gen:            gen,
			entries:        result.Entries,
			gitStatus:      result.GitStatus,
			dotMemberships: loadDotMemberships(a, ctx),
			detail:         groupMoveDetail(name, after),
		}
	}
}

func groupMoveDetail(name string, groups []string) string {
	if len(groups) == 1 && groups[0] != "" {
		return "✓ moved " + name + " to " + groups[0]
	}
	return "✓ updated group for " + name
}

func ensureMembershipGroupsOnHost(a *app.App, activeHost string, groups map[string]bool) error {
	if activeHost == "" {
		return nil
	}
	for group := range groups {
		if group == "" {
			continue
		}
		if err := a.AddGroupToHost(activeHost, group); err != nil {
			return err
		}
	}
	return nil
}

func (m *Model) doSetGroupTools(group string, membership, originalMembership, ignores, originalIgnores map[string]bool) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		changed := 0
		for name, desired := range membership {
			if originalMembership[name] == desired {
				continue
			}
			var err error
			if desired {
				err = a.MoveToolToGroup(name, group)
			} else {
				err = a.RemoveToolFromGroup(name, group)
			}
			if err != nil {
				return groupToolsChangedMsg{err: err}
			}
			changed++
		}
		for name, desired := range ignores {
			if originalIgnores[name] == desired {
				continue
			}
			if err := a.SetGroupIgnore(group, name, desired); err != nil {
				return groupToolsChangedMsg{err: err}
			}
			changed++
		}
		groups, _ := a.Groups(ctx)
		memberships, _ := a.ToolMembershipMap(ctx)
		info, _ := a.HostStatus()
		tools, _ := a.ListTools(ctx, "")
		var hostIgnore []string
		if info != nil && info.Active != "" {
			if prof, ok := info.Hosts[info.Active]; ok {
				hostIgnore = prof.Ignore
			}
		}
		labels := buildIgnoreLabels(a.ConfigPath, groups, hostIgnore)
		toolIgnores, groupIgnores, _ := buildToolScopeState(a.ConfigPath, groups)
		return groupToolsChangedMsg{
			detail:          fmt.Sprintf("✓ updated %d tool settings for %s", changed, group),
			tools:           tools,
			toolGroups:      compactToolGroupMapForHost(memberships, info),
			toolMemberships: memberships,
			groupNames:      buildGroupNames(groups),
			ignoreLabels:    labels,
			toolIgnoreSet:   toolIgnores,
			groupIgnoreSet:  groupIgnores,
		}
	}
}

func (m *Model) doSetGroupDots(group string, membership, originalMembership map[string]bool) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		changed := 0
		for name, desired := range membership {
			if originalMembership[name] == desired {
				continue
			}
			var err error
			if desired {
				err = a.MoveDotToGroup(name, group)
			} else {
				err = a.RemoveDotFromGroup(name, group)
			}
			if err != nil {
				return groupDotsChangedMsg{err: err}
			}
			changed++
		}
		memberships := loadDotMemberships(a, ctx)
		msg := groupDotsChangedMsg{
			detail:         fmt.Sprintf("✓ updated %d dotfiles for %s", changed, group),
			dotMemberships: memberships,
		}
		settings, _ := a.LoadSettings()
		if settings.DotsRepo == "" {
			return msg
		}
		result, err := a.DiscoverDotsStatus(ctx)
		if err != nil {
			return groupDotsChangedMsg{err: err, dotMemberships: memberships}
		}
		msg.entries = result.Entries
		msg.gitStatus = result.GitStatus
		return msg
	}
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

// doCreateGroup creates a new empty named group in config.
func (m *Model) doCreateGroup(name string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		if err := a.CreateGroup(name); err != nil {
			return createGroupDoneMsg{err: err, name: name}
		}
		if err := a.AddGroupToHost(shortHostname(), name); err != nil {
			return createGroupDoneMsg{err: err, name: name}
		}
		groupNames, toolGroups, memberships, info := m.reloadToolContext()
		return createGroupDoneMsg{
			name:            name,
			groupNames:      groupNames,
			toolGroups:      toolGroups,
			toolMemberships: memberships,
			hostInfo:        info,
		}
	}
}

// doSaveSettings persists the current settings to the config file.
func (m *Model) doSaveSettings() tea.Cmd {
	m.settingsSaveGen++
	gen := m.settingsSaveGen
	snapshot := cloneSettingsSnapshot(m.settings)
	if m.settingsSaveRunning {
		m.settingsSaveQueued = true
		m.settingsSaveQueuedSnapshot = snapshot
		m.settingsSaveQueuedGen = gen
		return nil
	}
	return m.startSettingsSave(snapshot, gen)
}

func (m *Model) appendSaveSettingsCmd(cmds *[]tea.Cmd) {
	if cmd := m.doSaveSettings(); cmd != nil {
		*cmds = append(*cmds, cmd)
	}
}

func (m *Model) doToggleDotsReminderService(enable bool) tea.Cmd {
	a, ctx := m.app, m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	interval := m.currentDotsReminderInterval()
	return func() tea.Msg {
		if enable {
			service, err := a.InstallDotsReminderService(ctx, app.DotsReminderInstallOptions{Interval: interval, Notify: true, Activate: true})
			return dotsServiceChangedMsg{kind: dotsReminderServiceKind, enabled: true, reminder: service, err: err}
		}
		service, err := a.UninstallDotsReminderService(ctx)
		return dotsServiceChangedMsg{kind: dotsReminderServiceKind, enabled: false, reminder: service, err: err}
	}
}

func (m *Model) doToggleDotsWatchService(enable bool) tea.Cmd {
	a, ctx := m.app, m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	debounce := m.dotsWatchDebounceForServiceInstall()
	return func() tea.Msg {
		if enable {
			service, err := a.InstallDotsWatchService(ctx, app.DotsWatchInstallOptions{Debounce: debounce, Activate: true})
			return dotsServiceChangedMsg{kind: dotsWatchServiceKind, enabled: true, watch: service, err: err}
		}
		service, err := a.UninstallDotsWatchService(ctx)
		return dotsServiceChangedMsg{kind: dotsWatchServiceKind, enabled: false, watch: service, err: err}
	}
}

func (m *Model) startSettingsSave(settings config.Settings, gen int) tea.Cmd {
	m.settingsSaveRunning = true
	m.settingsSaveInFlightGen = gen
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		return settingsSavedMsg{gen: gen, err: a.SaveSettings(ctx, settings)}
	}
}

func cloneSettingsSnapshot(in config.Settings) config.Settings {
	out := in
	if in.Ecosystems != nil {
		out.Ecosystems = make(map[string]config.EcosystemSettings, len(in.Ecosystems))
		for name, eco := range in.Ecosystems {
			eco.Priority = append([]string(nil), eco.Priority...)
			out.Ecosystems[name] = eco
		}
	}
	if in.DotsDisabled != nil {
		dotsDisabled := *in.DotsDisabled
		out.DotsDisabled = &dotsDisabled
	}
	if in.DisabledProviders != nil {
		out.DisabledProviders = append([]string{}, in.DisabledProviders...)
	}
	return out
}

func (m *Model) doSaveSettingsAndDotsSync(settings config.Settings) tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	snapshot := cloneSettingsSnapshot(settings)
	return func() tea.Msg {
		if err := a.SaveSettings(ctx, snapshot); err != nil {
			return dotsSyncedMsg{gen: gen, err: fmt.Errorf("save dots repo: %w", err)}
		}
		_, syncErr := a.DotsSyncContext(ctx, dots.SyncOptions{})
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

// selectedHostName returns the host name at the current host-assignment cursor, or "".
func (m *Model) selectedHostName() string {
	if m.hostInfo == nil || len(m.hostInfo.Hosts) == 0 {
		return ""
	}
	names := make([]string, 0, len(m.hostInfo.Hosts))
	for n := range m.hostInfo.Hosts {
		names = append(names, n)
	}
	sort.Strings(names)
	if m.hostCursor >= 0 && m.hostCursor < len(names) {
		return names[m.hostCursor]
	}
	return ""
}

// doAddGroupToHost appends a group to a host and reloads host info.
func (m *Model) doAddGroupToHost(host, group string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		if err := a.AddGroupToHost(host, group); err != nil {
			return hostGroupChangedMsg{err: err, host: host, group: group, added: true}
		}
		groupNames, toolGroups, memberships, info := m.reloadToolContext()
		return hostGroupChangedMsg{host: host, group: group, added: true, info: info, groupNames: groupNames, toolGroups: toolGroups, toolMemberships: memberships}
	}
}

// doRemoveGroupFromHost removes a group from a host and reloads host info.
func (m *Model) doRemoveGroupFromHost(host, group string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		if err := a.RemoveGroupFromHost(host, group); err != nil {
			return hostGroupChangedMsg{err: err, host: host, group: group, added: false}
		}
		groupNames, toolGroups, memberships, info := m.reloadToolContext()
		return hostGroupChangedMsg{host: host, group: group, added: false, info: info, groupNames: groupNames, toolGroups: toolGroups, toolMemberships: memberships}
	}
}

// doSetHostGroups persists a staged host group-assignment edit.
func (m *Model) doSetHostGroups(host string, before, after, createdGroups []string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		beforeSet := make(map[string]bool, len(before))
		afterSet := make(map[string]bool, len(after))
		for _, group := range before {
			beforeSet[group] = true
		}
		for _, group := range after {
			afterSet[group] = true
		}
		for _, group := range createdGroups {
			if afterSet[group] {
				if err := a.CreateGroup(group); err != nil {
					return hostGroupChangedMsg{err: err, host: host}
				}
			}
		}
		for group := range beforeSet {
			if !afterSet[group] {
				if err := a.RemoveGroupFromHost(host, group); err != nil {
					return hostGroupChangedMsg{err: err, host: host}
				}
			}
		}
		for group := range afterSet {
			if !beforeSet[group] {
				if err := a.AddGroupToHost(host, group); err != nil {
					return hostGroupChangedMsg{err: err, host: host}
				}
			}
		}
		groupNames, toolGroups, memberships, info := m.reloadToolContext()
		return hostGroupChangedMsg{host: host, detail: "✓ groups updated for host " + host, info: info, groupNames: groupNames, toolGroups: toolGroups, toolMemberships: memberships}
	}
}

// doRemoveHostFromTab removes a host from settings.json and reloads host info.
func (m *Model) doRemoveHostFromTab(name string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		if err := a.RemoveHost(name); err != nil {
			return hostGroupChangedMsg{err: err, host: name}
		}
		groupNames, toolGroups, memberships, info := m.reloadToolContext()
		return hostGroupChangedMsg{host: name, info: info, groupNames: groupNames, toolGroups: toolGroups, toolMemberships: memberships}
	}
}

// doRenameHost renames a host and reloads host info.
func (m *Model) doRenameHost(oldName, newName string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		if err := a.RenameHost(oldName, newName); err != nil {
			return hostGroupChangedMsg{err: err, host: oldName}
		}
		groupNames, toolGroups, memberships, info := m.reloadToolContext()
		return hostGroupChangedMsg{host: newName, detail: "✓ " + oldName + " renamed to " + newName, info: info, groupNames: groupNames, toolGroups: toolGroups, toolMemberships: memberships}
	}
}

// doRenameGroup renames a group in config and reloads group/host data.
func (m *Model) doRenameGroup(oldName, newName string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		if err := a.RenameGroup(oldName, newName); err != nil {
			return groupChangedMsg{err: err}
		}
		groupNames, toolGroups, memberships := m.reloadToolGroups()
		info, _ := a.HostStatus()
		return groupChangedMsg{
			detail:          "✓ renamed " + oldName + " → " + newName,
			groupNames:      groupNames,
			toolGroups:      toolGroups,
			toolMemberships: memberships,
			info:            info,
		}
	}
}

// doDeleteGroup removes a group, then reloads group/host data.
func (m *Model) doDeleteGroup(name string, deleteTools bool) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		opts := app.DeleteGroupOptions{MoveTo: shortHostname()}
		detail := "✓ deleted group " + name + " (tools moved to this host)"
		if deleteTools {
			opts = app.DeleteGroupOptions{DeleteTools: true}
			detail = "✓ deleted group " + name + " (last-membership tools deleted)"
		}
		if err := a.DeleteGroup(ctx, name, opts); err != nil {
			return groupChangedMsg{err: err}
		}
		groupNames, toolGroups, memberships := m.reloadToolGroups()
		tools, _ := a.ListTools(ctx, "")
		info, _ := a.HostStatus()
		return groupChangedMsg{
			detail:          detail,
			tools:           tools,
			groupNames:      groupNames,
			toolGroups:      toolGroups,
			toolMemberships: memberships,
			info:            info,
		}
	}
}

// doRemoveHost removes the named host from settings.json then reloads tools.
func (m *Model) doRemoveHost(name string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		if err := a.RemoveHost(name); err != nil {
			return dangerOpDoneMsg{action: "delete-host", err: err}
		}
		return dangerOpDoneMsg{action: "delete-host", detail: name + " deleted", reload: true}
	}
}

// doResetSettings resets settings to defaults then reloads tools.
func (m *Model) doResetSettings() tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		if err := a.ResetSettings(ctx); err != nil {
			return dangerOpDoneMsg{action: "reset-settings", err: err}
		}
		return dangerOpDoneMsg{action: "reset-settings", detail: "settings cleared", reload: true}
	}
}

// doResetCache deletes and reinitialises the tool cache DB, then reloads tools.
func (m *Model) doResetCache() tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		if err := a.ResetCache(ctx); err != nil {
			return dangerOpDoneMsg{action: "reset-cache", err: err}
		}
		return dangerOpDoneMsg{action: "reset-cache", detail: "cache cleared", reload: true}
	}
}

// doDisableDots removes managed symlinks when dots is configured, optionally
// keeping local materialized copies, then persists dots_disabled=true for this
// machine. Regular settings flows trigger a full settings reload; setup can
// suppress that reload while advancing to the next onboarding step. Safe to
// call when dots is not yet configured — the physical unlink step is skipped in
// that case.
func (m *Model) doDisableDots(keepLocal bool, setupComplete ...bool) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		ops, err := a.DisableDotsForHost(ctx, app.DisableDotsOptions{
			KeepExistingLocal: keepLocal,
			RemoveLocal:       !keepLocal,
		})
		detail := dotsDisableDetail(ops)
		done := len(setupComplete) > 0 && setupComplete[0]
		reload := len(setupComplete) == 0 || done
		if err != nil {
			return dangerOpDoneMsg{action: "disable-dots", detail: detail, reload: reload, setupComplete: done, err: err}
		}
		return dangerOpDoneMsg{action: "disable-dots", detail: detail, reload: reload, setupComplete: done}
	}
}

func dotsDisableDetail(ops []dots.Op) string {
	if len(ops) == 0 {
		return "dots disabled"
	}
	var unlinked, conflicts int
	for _, op := range ops {
		switch op.Kind {
		case dots.OpUnlink:
			unlinked++
		case dots.OpUnlinkConflict:
			conflicts++
		}
	}
	detail := fmt.Sprintf("%d unlinked", unlinked)
	if conflicts > 0 {
		detail += fmt.Sprintf(", %d conflicts", conflicts)
	}
	return detail
}

// doEnableDots re-enables dotfile sync: clears the disabled flag and runs a
// sync to restore managed symlinks.
func (m *Model) doEnableDots() tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	return func() tea.Msg {
		if _, err := a.EnableDotsForHost(ctx); err != nil {
			return dangerOpDoneMsg{action: "enable-dots", dotsGen: gen, err: err}
		}
		return dangerOpDoneMsg{action: "enable-dots", dotsGen: gen, detail: "dots enabled", reload: true, mode: viewDots}
	}
}

// doConsolidate switches all tools in ecosystem to manager.
func (m *Model) doConsolidate(ecosystem, manager string) tea.Cmd {
	a, ctx := m.app, m.beginCancellableAction()
	return func() tea.Msg {
		result, err := a.Consolidate(ctx, ecosystem, manager, nil)
		tools, _ := a.ListTools(ctx, "") // non-fatal: consolidate succeeded; stale list retained if refresh fails
		if err != nil {
			return opCompleteMsg{err: err}
		}
		parts := []string{fmt.Sprintf("%d migrated to %s", len(result.Migrated), manager)}
		if len(result.Failed) > 0 {
			parts = append(parts, fmt.Sprintf("%d failed", len(result.Failed)))
		}
		if len(result.UninstallWarnings) > 0 {
			parts = append(parts, fmt.Sprintf("%d uninstall warning(s)", len(result.UninstallWarnings)))
		}
		return opCompleteMsg{message: strings.Join(parts, ", "), tools: tools}
	}
}

// doClaim adds an orphan tool (installed but not in config) to the given group.
// Pass groupName="" to add to the current host group.
func (m *Model) doClaim(name, prov, groupName string, activeHost ...string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		if err := a.Add(ctx, prov, name, name, groupName, ""); err != nil {
			return claimDoneMsg{err: err, name: name, groupName: groupName}
		}
		if groupName != "" {
			if err := a.AddGroupToHost(shortHostname(), groupName); err != nil {
				tools, _ := a.ListTools(ctx, "")
				groupNames, toolGroups, memberships, info := m.reloadToolContext()
				return claimDoneMsg{err: err, name: name, groupName: groupName, tools: tools, groupNames: groupNames, toolGroups: toolGroups, toolMemberships: memberships, hostInfo: info}
			}
		}
		if len(activeHost) > 0 && activeHost[0] != "" && groupName != "" {
			if err := a.AddGroupToHost(activeHost[0], groupName); err != nil {
				tools, _ := a.ListTools(ctx, "")
				groupNames, toolGroups, memberships, info := m.reloadToolContext()
				return claimDoneMsg{err: err, name: name, groupName: groupName, tools: tools, groupNames: groupNames, toolGroups: toolGroups, toolMemberships: memberships, hostInfo: info}
			}
		}
		tools, _ := a.ListTools(ctx, "") // non-fatal: claim succeeded; stale list retained if refresh fails
		groupNames, toolGroups, memberships, info := m.reloadToolContext()
		return claimDoneMsg{name: name, groupName: groupName, tools: tools, groupNames: groupNames, toolGroups: toolGroups, toolMemberships: memberships, hostInfo: info}
	}
}

func (m *Model) doSetIgnoreScope(name string, opt scopeOption) tea.Cmd {
	opt.initialChecked = opt.checked
	opt.checked = !opt.checked
	return m.doSaveIgnoreScopes(name, []scopeOption{opt})
}

func (m *Model) doSaveIgnoreScopes(name string, options []scopeOption) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		changedHost := false
		ignored := false
		for _, opt := range options {
			if opt.checked == opt.initialChecked {
				continue
			}
			desired := opt.checked
			ignored = ignored || desired
			var err error
			switch opt.kind {
			case "tool":
				err = a.SetToolIgnore(name, desired)
			case "group":
				err = a.SetGroupIgnore(opt.group, name, desired)
			case "host":
				changedHost = true
				err = a.SetGlobalToolIgnore(name, desired)
			default:
				err = fmt.Errorf("unknown ignore scope %q", opt.kind)
			}
			if err != nil {
				return ignoreDoneMsg{err: err, name: name, ignored: desired}
			}
		}
		groups, _ := a.Groups(ctx)
		tools, _ := a.ListTools(ctx, "")
		var hostIgnore []string
		if info, _ := a.HostStatus(); info != nil && info.Active != "" {
			if prof, ok := info.Hosts[info.Active]; ok {
				hostIgnore = prof.Ignore
			}
		}
		labels := buildIgnoreLabels(a.ConfigPath, groups, hostIgnore)
		toolIgnores, groupIgnores, _ := buildToolScopeState(a.ConfigPath, groups)
		if labels[name] != "" {
			ignored = true
		}
		return ignoreDoneMsg{name: name, ignored: ignored, tools: tools, hostScope: changedHost, ignoreLabels: labels, toolIgnoreSet: toolIgnores, groupIgnoreSet: groupIgnores}
	}
}

// doInstallAndAdd installs a tool and adds it to the selected config group.
// Used for search results and orphan tools that are not yet in config.
func (m *Model) doInstallAndAdd(name, prov string, groupAndHost ...string) tea.Cmd {
	a, ctx := m.app, m.beginCancellableAction()
	return func() tea.Msg {
		group := ""
		if len(groupAndHost) > 0 {
			group = groupAndHost[0]
		}
		removeDiscovered := []string{toolKey(name, prov)}
		if err := a.Install(ctx, name, prov); err != nil {
			return opCompleteMsg{err: err}
		}
		if err := a.Add(ctx, prov, name, name, group, ""); err != nil {
			// Install succeeded but config save failed. Surface as error so the
			// status bar renders red — config is the recoverable part the user
			// must address; pretending green ✓ would hide the failure.
			return opCompleteMsg{err: fmt.Errorf("installed %s but config save failed: %w", name, err)}
		}
		if group != "" {
			if err := a.AddGroupToHost(shortHostname(), group); err != nil {
				tools, _ := a.ListTools(ctx, "")
				groupNames, toolGroups, memberships, info := m.reloadToolContext()
				return opCompleteMsg{err: fmt.Errorf("installed %s and added to config but host update failed: %w", name, err), tools: tools, removeDiscoveredKeys: removeDiscovered, groupNames: groupNames, toolGroups: toolGroups, toolMemberships: memberships, hostInfo: info}
			}
		}
		if len(groupAndHost) > 1 && groupAndHost[1] != "" && group != "" {
			if err := a.AddGroupToHost(groupAndHost[1], group); err != nil {
				tools, _ := a.ListTools(ctx, "")
				groupNames, toolGroups, memberships, info := m.reloadToolContext()
				return opCompleteMsg{err: fmt.Errorf("installed %s and added to config but host update failed: %w", name, err), tools: tools, removeDiscoveredKeys: removeDiscovered, groupNames: groupNames, toolGroups: toolGroups, toolMemberships: memberships, hostInfo: info}
			}
		}
		tools, _ := a.ListTools(ctx, "") // non-fatal: install+add succeeded; stale list retained if refresh fails
		groupNames, toolGroups, memberships, info := m.reloadToolContext()
		return opCompleteMsg{message: "installed " + name + " and added to config", tools: tools, removeDiscoveredKeys: removeDiscovered, groupNames: groupNames, toolGroups: toolGroups, toolMemberships: memberships, hostInfo: info}
	}
}

func (m *Model) doSetProviderScope(name string, opt scopeOption, t *database.ToolCache) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		if t == nil || t.InstalledWith == "" {
			return opCompleteMsg{err: fmt.Errorf("provider pin: installed provider is unknown")}
		}
		var err error
		switch opt.kind {
		case "provider-host":
			err = a.SetToolHostInstallSpec(name, t.Provider, t.Package, t.InstalledWith)
		case "provider-tool":
			err = a.SetToolDefaultInstallSpec(name, t.Provider, t.Package, t.InstalledWith)
		case "provider-ecosystem":
			err = a.PinEcosystemForHost(ctx, t.Provider, t.InstalledWith)
		default:
			err = fmt.Errorf("unknown provider scope %q", opt.kind)
		}
		if err != nil {
			return opCompleteMsg{err: fmt.Errorf("pin provider: %w", err)}
		}
		tools, err := a.ListTools(ctx, "")
		if err != nil {
			return opCompleteMsg{err: fmt.Errorf("pin provider: list tools: %w", err)}
		}
		groups, _ := a.Groups(ctx)
		_, _, pins := buildToolScopeState(a.ConfigPath, groups)
		if pins == nil {
			pins = make(map[string]string)
		}
		pins[name] = t.InstalledWith
		return opCompleteMsg{message: "pinned " + name + " via " + opt.label, tools: tools, toolProviderPins: pins}
	}
}

func (m *Model) doClearProviderOverride(name, configProv, installedWith string) tea.Cmd {
	a, ctx := m.app, m.beginCancellableAction()
	return func() tea.Msg {
		var (
			err     error
			result  *app.SwitchResult
			cleared app.ClearInstallOverrideResult
		)
		if installedWith != "" {
			result, cleared, err = a.ReinstallWithDefaultAfterClearingInstallOverride(ctx, name, configProv)
		} else {
			cleared, err = a.ClearToolInstallOverride(ctx, name, configProv)
			if err == nil {
				if refreshErr := a.RefreshInstalled(ctx, nil); refreshErr != nil {
					err = fmt.Errorf("refresh installed: %w", refreshErr)
				}
			}
		}
		fromProvider := installedWith
		toProvider := configProv
		if result != nil {
			fromProvider = result.FromProvider
			toProvider = result.ToProvider
		} else if fromProvider == "" {
			fromProvider = cleared.InstallWith
		}
		if err != nil {
			return migrateProviderDoneMsg{err: fmt.Errorf("remove provider override: %w", err), name: name, fromProvider: fromProvider, toProvider: toProvider, clearedProviderOverride: true}
		}
		tools, err := a.ListTools(ctx, "")
		if err != nil {
			return migrateProviderDoneMsg{err: fmt.Errorf("remove provider override: list tools: %w", err), name: name, fromProvider: fromProvider, toProvider: toProvider, clearedProviderOverride: true}
		}
		groups, _ := a.Groups(ctx)
		_, _, pins := buildToolScopeState(a.ConfigPath, groups)
		return migrateProviderDoneMsg{name: name, fromProvider: fromProvider, toProvider: toProvider, tools: tools, toolProviderPins: pins, clearedProviderOverride: true}
	}
}

// doMigrateProvider installs the tool via the config-intended provider, removes
// it from the currently-installed (wrong) provider, and updates the config.
// syncWrongProv means: tool is installed via installedWith but config says configProv.
// Migrate = install via configProv, uninstall from installedWith, rewrite config.
func (m *Model) doMigrateProvider(name, configProv, installedWith string) tea.Cmd {
	a, ctx := m.app, m.beginCancellableAction()
	return func() tea.Msg {
		if _, err := a.MigrateInstallation(ctx, name, installedWith, configProv); err != nil {
			return migrateProviderDoneMsg{err: fmt.Errorf("reinstall with default: %w", err), name: name, fromProvider: installedWith, toProvider: configProv}
		}
		// Re-probe all providers so InstalledWith is updated in the DB before we
		// return the fresh tool list. Runs silently (no progress channel needed here).
		if err := a.RefreshInstalled(ctx, nil); err != nil {
			return migrateProviderDoneMsg{err: fmt.Errorf("reinstall with default: refresh installed: %w", err), name: name, fromProvider: installedWith, toProvider: configProv}
		}
		if err := a.RefreshDescriptions(ctx, 0); err != nil {
			return migrateProviderDoneMsg{err: fmt.Errorf("reinstall with default: refresh descriptions: %w", err), name: name, fromProvider: installedWith, toProvider: configProv}
		}
		tools, err := a.ListTools(ctx, "")
		if err != nil {
			return migrateProviderDoneMsg{err: fmt.Errorf("reinstall with default: list tools: %w", err), name: name, fromProvider: installedWith, toProvider: configProv}
		}
		return migrateProviderDoneMsg{name: name, fromProvider: installedWith, toProvider: configProv, tools: tools}
	}
}

func (m *Model) doApplyProviderSolution(name, fromProvider string, solution provider.ErrorSolution) tea.Cmd {
	a, ctx := m.app, m.beginCancellableAction()
	target := solution.TargetProvider
	return func() tea.Msg {
		key := toolKey(name, fromProvider)
		if target == "" {
			return opCompleteMsg{key: key, err: fmt.Errorf("apply fix: missing target provider")}
		}
		result, err := a.Switch(ctx, name, fromProvider, target)
		if err != nil {
			return opCompleteMsg{key: key, err: fmt.Errorf("apply fix: %w", err)}
		}
		if err := a.RefreshInstalled(ctx, nil); err != nil {
			return opCompleteMsg{key: key, err: fmt.Errorf("apply fix: refresh installed: %w", err)}
		}
		if err := a.RefreshDescriptions(ctx, 0); err != nil {
			return opCompleteMsg{key: key, err: fmt.Errorf("apply fix: refresh descriptions: %w", err)}
		}
		tools, err := a.ListTools(ctx, "")
		if err != nil {
			return opCompleteMsg{key: key, err: fmt.Errorf("apply fix: list tools: %w", err)}
		}
		message := fmt.Sprintf("reinstalled %s with %s", name, target)
		if result != nil && result.UninstallWarning != nil {
			message += ", cleanup warning"
		}
		return opCompleteMsg{key: key, message: message, tools: tools}
	}
}

// openFilePicker configures and opens the file picker popup.
// title is shown as the popup header.
// currentPath sets the starting directory: opens at the path itself if it
// already exists as a directory, otherwise falls back to its parent (or home).
// allowFiles controls whether plain files are selectable in addition to directories.
// Returns a tea.Cmd that starts the async directory read.
func (m *Model) openFilePicker(title, currentPath string, allowFiles bool) tea.Cmd {
	fp, cmd := newPathPicker(currentPath, allowFiles, filePickerContentWidth(*m), filePickerListHeight(*m))

	m.dotsFilePicker = fp
	m.filePickerTitle = title
	m.filePickerAllowFiles = allowFiles
	m.showFilePicker = true
	return cmd
}

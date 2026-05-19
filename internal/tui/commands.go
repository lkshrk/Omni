package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/profile"
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
				m.sendToolProgressUpdate(ch, gen, event)
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
				m.sendSyncAllToolProgressUpdate(ch, gen, event, syncAllToolProgressText(event, current, total))
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
				gen:             progressGen,
				refreshProvider: event.Provider,
				refreshToolName: event.Name,
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

func (m *Model) doCheckProviderOutdated(provName string, gen int) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		defer profile.Start("tui.refresh.outdated.provider." + provName)()

		outdatedCtx, cancelOutdated := context.WithTimeout(ctx, 45*time.Second)
		outdatedErr := a.RefreshProviderOutdated(outdatedCtx, provName)
		cancelOutdated()
		return providerOutdatedCheckedMsg{gen: gen, provider: provName, err: outdatedErr}
	}
}

func (m *Model) doFetchOutdatedTools(gen int) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		defer profile.Start("tui.refresh.outdated.final_tools")()

		tools, err := a.ListTools(ctx, "")
		if err != nil {
			return outdatedProvidersDoneMsg{gen: gen, err: err}
		}
		ecosystemMap := a.ResolvedEcosystemProviders(ctx)
		return outdatedProvidersDoneMsg{
			gen:                    gen,
			tools:                  tools,
			effectiveSystemManager: ecosystemMap[provider.EcosystemSystem],
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
			m.sendUpgradeAllToolProgressUpdate(ch, gen, event, upgradeAllProgressText(event, started[key], total))
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

package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
)

const (
	refreshIssueWarning = iota + 1
	refreshIssueProviderError
	refreshIssueSnapshotError
)

func (m *Model) handleProviderScannedMsg(msg providerScannedMsg) []tea.Cmd {
	var cmds []tea.Cmd

	if msg.gen != m.scanGen {
		return cmds
	}
	if !m.scanningProviders[msg.provider] {
		return cmds
	}
	m.finishProviderRefreshProgress(msg.provider)
	delete(m.scanningProviders, msg.provider)
	if msg.err != nil {
		status := app.ProviderScanFailureStatus(msg.provider, msg.err)
		if !m.collectLaunchBatchError(status) {
			// Accumulate rather than flashing each failure over the last; one aggregated message is shown when the scan settles.
			m.refreshScanErrors = append(m.refreshScanErrors, status)
		}
	}
	if len(m.scanningProviders) > 0 && (msg.err == nil || m.launchBatchActive) {
		setActivityStatus(m, m.toolRefreshStatus(m.refreshToolDone, m.refreshToolTotal))
	}
	// Last provider finished: fetch one consistent snapshot now that all upserts are done, and start the orphan scan in parallel.
	if len(m.scanningProviders) == 0 {
		outdatedProviders := app.RefreshProviderScanProviderNames(m.providerScanToolCounts)
		if len(outdatedProviders) == 0 && msg.provider != "" {
			outdatedProviders = []string{msg.provider}
		}
		m.discoveryGen++
		m.providerSnapshotRefreshing = true
		m.discoveryRefreshing = true
		m.providerScanToolDone = nil
		m.refreshToolDone = 0
		m.refreshToolTotal = 0
		// Close only the scan's own stream: m.progressCh may already belong to a newer stream from an unrelated operation, and closing that would crash its worker goroutine.
		if m.scanProgressCh != nil {
			close(m.scanProgressCh)
			if m.progressCh == m.scanProgressCh {
				m.progressCh = nil
				m.progressGen++
			}
			m.scanProgressCh = nil
		}
		// Take over the shared stream only when no other operation owns it; beginning one here would bump progressGen and drop that operation's remaining updates, so the refresh runs without progress reporting instead.
		var ch chan progressUpdate
		var progressGen int
		if m.progressCh == nil {
			setActivityStatus(m, "Finding local tools…")
			ch, progressGen = m.beginProgressStream()
			m.discoveryProgressCh = ch
			cmds = append(cmds, waitForProgress(ch, progressGen))
		}
		cmds = append(cmds, m.doFetchFinalTools(msg.gen), m.doRefreshDiscovered(m.discoveryGen, ch, progressGen))
		cmds = append(cmds, m.startProviderOutdatedChecks(outdatedProviders, msg.gen)...)
		if len(m.refreshScanErrors) > 0 {
			m.rememberRefreshIssue(aggregateRefreshErrors(m.refreshScanErrors), refreshIssueProviderError)
			m.refreshScanErrors = nil
		}
	}
	if cmd := m.finishLaunchBatchIfIdle(); cmd != nil {
		cmds = append(cmds, cmd)
	}

	return cmds
}

// Combines per-provider scan failures into one readable status so multiple failures do not overwrite each other as transient flashes.
func aggregateRefreshErrors(errs []string) string {
	if len(errs) == 1 {
		return errs[0]
	}
	return fmt.Sprintf("%d provider scans failed — %s", len(errs), strings.Join(errs, " · "))
}

func (m *Model) startProviderOutdatedChecks(providers []string, gen int) []tea.Cmd {
	m.refreshIssue = ""
	m.refreshIssuePriority = 0
	if len(providers) == 0 {
		m.outdatedProviders = nil
		m.outdatedTotal = 0
		return nil
	}
	m.outdatedProviders = make(map[string]bool, len(providers))
	cmds := make([]tea.Cmd, 0, len(providers))
	for _, providerName := range providers {
		if providerName == "" || m.outdatedProviders[providerName] {
			continue
		}
		m.outdatedProviders[providerName] = true
		cmds = append(cmds, m.doCheckProviderOutdated(providerName, gen))
	}
	if len(m.outdatedProviders) == 0 {
		m.outdatedTotal = 0
		return nil
	}
	m.outdatedTotal = len(m.outdatedProviders)
	return cmds
}

func (m *Model) finishProviderRefreshProgress(providerName string) {
	if m.refreshToolTotal == 0 {
		m.refreshToolTotal = app.RefreshProviderScanCountTotal(m.providerScanToolCounts)
	}
	expected := m.providerScanToolCounts[providerName]
	if expected <= 0 {
		expected = 1
	}
	if m.providerScanToolDone == nil {
		m.providerScanToolDone = make(map[string]int)
	}
	done := m.providerScanToolDone[providerName]
	if done >= expected {
		return
	}
	m.refreshToolDone += expected - done
	if m.refreshToolTotal > 0 && m.refreshToolDone > m.refreshToolTotal {
		m.refreshToolDone = m.refreshToolTotal
	}
	m.providerScanToolDone[providerName] = expected
}

func (m *Model) handleAllProvidersDoneMsg(msg allProvidersDoneMsg) tea.Cmd {
	if msg.gen != m.scanGen {
		return nil
	}
	m.providerSnapshotRefreshing = false
	if msg.err != nil {
		status := "refresh failed: " + msg.err.Error()
		if m.collectLaunchBatchError(status) {
			m.settleToolRefresh()
			m.finishSetupReloadIfIdle()
			cmd := m.finishLaunchBatchIfIdle()
			if cmd != nil {
				m.clearRefreshIssue()
			}
			return cmd
		}
		m.rememberRefreshIssue(status, refreshIssueSnapshotError)
		m.settleToolRefresh()
		m.finishSetupReloadIfIdle()
		return m.finishRefreshIssueIfIdle()
	}
	m.settleToolRefresh()
	if msg.tools != nil {
		m.allTools = msg.tools
		m.applyFilter()
	}
	if msg.effectiveSystemManager != "" {
		m.effectiveSystemManager = msg.effectiveSystemManager
	}

	m.finishSetupReloadIfIdle()
	if cmd := m.finishLaunchBatchIfIdle(); cmd != nil {
		m.clearRefreshIssue()
		return cmd
	}
	if cmd := m.finishRefreshIssueIfIdle(); cmd != nil {
		return cmd
	}
	m.clearActivityStatusIfIdle()
	return nil
}

func (m *Model) handleProviderOutdatedCheckedMsg(msg providerOutdatedCheckedMsg) []tea.Cmd {
	var cmds []tea.Cmd
	if msg.gen != m.scanGen {
		return cmds
	}
	if !m.outdatedProviders[msg.provider] {
		return cmds
	}
	delete(m.outdatedProviders, msg.provider)
	if msg.err != nil {
		m.rememberRefreshIssue(app.ProviderScanFailureStatus(msg.provider, msg.err), refreshIssueProviderError)
	} else if msg.warning != "" {
		m.rememberRefreshIssue(msg.warning, refreshIssueWarning)
	}
	if len(m.outdatedProviders) > 0 {
		if !m.providerSnapshotRefreshing && !m.discoveryRefreshing {
			setActivityStatus(m, m.outdatedCheckStatus())
		}
		return cmds
	}
	m.outdatedProviders = nil
	m.outdatedSnapshotRefreshing = true
	cmds = append(cmds, m.doFetchOutdatedTools(msg.gen))
	return cmds
}

func (m *Model) handleOutdatedProvidersDoneMsg(msg outdatedProvidersDoneMsg) tea.Cmd {
	if msg.gen != m.scanGen {
		return nil
	}
	m.outdatedSnapshotRefreshing = false
	// The scan labels and counts outlive the installed phase so the update check can name its providers too; this is where they stop being needed.
	m.providerScanLabels = nil
	m.providerScanToolCounts = nil
	m.outdatedTotal = 0
	if msg.err != nil {
		m.rememberRefreshIssue("update check failed: "+msg.err.Error(), refreshIssueSnapshotError)
	} else if msg.tools != nil {
		m.allTools = msg.tools
		m.applyFilter()
	}
	if msg.effectiveSystemManager != "" {
		m.effectiveSystemManager = msg.effectiveSystemManager
	}
	m.settleToolRefresh()
	m.finishSetupReloadIfIdle()
	var cmds []tea.Cmd
	if cmd := m.finishRefreshIssueIfIdle(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	m.clearActivityStatusIfIdle()
	if m.dashboardReconcileRunning && m.dashboardReconcileCurrent == dashboardReconcilePlanUpgradeTools {
		m.dashboardReconcileCurrent = ""
		m.startNextDashboardReconcileStep(&cmds)
	}
	return tea.Batch(cmds...)
}

func (m *Model) handleDiscoveredRefreshedMsg(msg discoveredRefreshedMsg) tea.Cmd {
	if msg.gen != m.discoveryGen {
		return nil
	}
	m.discoveryRefreshing = false
	m.settleToolRefresh()
	m.releaseDiscoveryProgressStream()
	if msg.err != nil {
		m.finishSetupReloadIfIdle()
		status := "orphan scan failed: " + msg.err.Error()
		if m.collectLaunchBatchError(status) {
			cmd := m.finishLaunchBatchIfIdle()
			if cmd != nil {
				m.clearRefreshIssue()
			}
			return cmd
		}
		m.rememberRefreshIssue(status, refreshIssueSnapshotError)
		return m.finishRefreshIssueIfIdle()
	}
	// A successful scan can legitimately prune the last discovered row, so still overwrite or pruned rows linger in the UI.
	m.discoveredTools = msg.discovered
	m.rebuildDiscoveredKeys()
	m.applyFilter()
	if anyMissingDescription(msg.discovered) || anyMissingDescription(m.allTools) {
		return m.startDescriptionRefresh()
	}

	m.finishSetupReloadIfIdle()
	if cmd := m.finishLaunchBatchIfIdle(); cmd != nil {
		m.clearRefreshIssue()
		return cmd
	}
	if cmd := m.finishRefreshIssueIfIdle(); cmd != nil {
		return cmd
	}
	m.clearActivityStatusIfIdle()
	return nil
}

// The orphan scan closes its channel before discoveredRefreshedMsg is delivered, so by here the stream is already dead; dropping it now lets the description phase open its own instead of running silently until progressStreamClosedMsg happens to arrive.
func (m *Model) releaseDiscoveryProgressStream() {
	if m.discoveryProgressCh == nil {
		return
	}
	if m.progressCh == m.discoveryProgressCh {
		m.progressCh = nil
		m.progressGen++
	}
	m.discoveryProgressCh = nil
}

func (m *Model) clearActivityStatusIfIdle() {
	if m.launchBatchActive || m.setupReloading || m.launchBatchPending() || m.toolRefreshPending() {
		return
	}
	m.progressText = ""
}

func (m *Model) rememberRefreshIssue(text string, priority int) {
	if text == "" || priority <= m.refreshIssuePriority {
		return
	}
	m.refreshIssue = text
	m.refreshIssuePriority = priority
}

func (m *Model) clearRefreshIssue() {
	m.refreshIssue = ""
	m.refreshIssuePriority = 0
}

func (m *Model) finishRefreshIssueIfIdle() tea.Cmd {
	if m.toolRefreshPending() || m.launchBatchActive || m.setupReloading || m.refreshIssue == "" {
		return nil
	}
	status := m.refreshIssue
	m.clearRefreshIssue()
	return setStatus(m, status, true)
}

func (m Model) toolRefreshPending() bool {
	return len(m.scanningProviders) > 0 ||
		len(m.outdatedProviders) > 0 ||
		m.providerSnapshotRefreshing ||
		m.outdatedSnapshotRefreshing ||
		m.discoveryRefreshing ||
		m.descRefreshing
}

// endLoading clears the refresh gate after its final leg, including errors, but never a gate owned by another operation.
func (m *Model) settleToolRefresh() {
	if m.migrating || m.toolRefreshPending() {
		return
	}
	m.endLoading(loadingOwnerToolRefresh)
}

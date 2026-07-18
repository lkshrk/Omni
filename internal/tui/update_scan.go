package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
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
			// Accumulate rather than flashing each failure (which overwrite each
			// other); a single aggregated message is shown when the scan settles.
			m.refreshScanErrors = append(m.refreshScanErrors, status)
		}
	}
	if len(m.scanningProviders) > 0 && (msg.err == nil || m.launchBatchActive) {
		setActivityStatus(m, m.toolRefreshStatus(m.refreshToolDone, m.refreshToolTotal))
	}
	// When the last provider finishes: fetch one consistent snapshot now that
	// all upserts are done, and kick off the orphan scan in parallel.
	if len(m.scanningProviders) == 0 {
		outdatedProviders := app.RefreshProviderScanProviderNames(m.providerScanToolCounts)
		if len(outdatedProviders) == 0 && msg.provider != "" {
			outdatedProviders = []string{msg.provider}
		}
		m.discoveryGen++
		m.providerSnapshotRefreshing = true
		m.discoveryRefreshing = true
		m.providerScanToolCounts = nil
		m.providerScanToolDone = nil
		m.providerScanLabels = nil
		m.refreshToolDone = 0
		m.refreshToolTotal = 0
		// Close only the scan's own progress stream. m.progressCh may already
		// belong to a newer stream begun by an unrelated operation while the
		// scan was still running (e.g. agents update all); closing that channel
		// here would crash its worker goroutine on sendProgress/its own close.
		if m.scanProgressCh != nil {
			close(m.scanProgressCh)
			if m.progressCh == m.scanProgressCh {
				m.progressCh = nil
				m.progressGen++
			}
			m.scanProgressCh = nil
		}
		// The discovered refresh takes over the shared status stream only when
		// no other operation owns it. If a foreign stream is active (m.progressCh
		// survived the block above), beginning a new stream here would bump
		// progressGen and silently drop that operation's remaining progress
		// updates — so the refresh runs without progress reporting instead
		// (doRefreshDiscovered and sendProgress both accept a nil channel).
		var ch chan progressUpdate
		var progressGen int
		if m.progressCh == nil {
			setActivityStatus(m, "Finding local tools…")
			ch, progressGen = m.beginProgressStream()
			cmds = append(cmds, waitForProgress(ch, progressGen))
		}
		cmds = append(cmds, m.doFetchFinalTools(msg.gen), m.doRefreshDiscovered(m.discoveryGen, ch, progressGen))
		cmds = append(cmds, m.startProviderOutdatedChecks(outdatedProviders, msg.gen)...)
		if len(m.refreshScanErrors) > 0 {
			cmds = append(cmds, setStatus(m, aggregateRefreshErrors(m.refreshScanErrors), true))
			m.refreshScanErrors = nil
		}
	}
	if cmd := m.finishLaunchBatchIfIdle(); cmd != nil {
		cmds = append(cmds, cmd)
	}

	return cmds
}

// aggregateRefreshErrors combines per-provider scan failures into a single
// readable status so multiple failures don't overwrite each other as transient
// flashes.
func aggregateRefreshErrors(errs []string) string {
	if len(errs) == 1 {
		return errs[0]
	}
	return fmt.Sprintf("%d provider scans failed — %s", len(errs), strings.Join(errs, " · "))
}

func (m *Model) startProviderOutdatedChecks(providers []string, gen int) []tea.Cmd {
	if len(providers) == 0 {
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
		return nil
	}
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
		m.finishSetupReloadIfIdle()
		status := "refresh failed: " + msg.err.Error()
		if m.collectLaunchBatchError(status) {
			return m.finishLaunchBatchIfIdle()
		}
		return setStatus(m, status, true)
	}
	if msg.tools != nil {
		m.allTools = msg.tools
		m.applyFilter()
	}
	if msg.effectiveSystemManager != "" {
		m.effectiveSystemManager = msg.effectiveSystemManager
	}

	m.finishSetupReloadIfIdle()
	if cmd := m.finishLaunchBatchIfIdle(); cmd != nil {
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
		cmds = append(cmds, setStatus(m, app.ProviderScanFailureStatus(msg.provider, msg.err), true))
	}
	if len(m.outdatedProviders) > 0 {
		if !m.providerSnapshotRefreshing && !m.discoveryRefreshing {
			setActivityStatus(m, "Checking updates…")
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
	if msg.err != nil {
		return setStatus(m, "update check failed: "+msg.err.Error(), true)
	}
	if msg.tools != nil {
		m.allTools = msg.tools
		m.applyFilter()
	}
	if msg.effectiveSystemManager != "" {
		m.effectiveSystemManager = msg.effectiveSystemManager
	}
	m.clearActivityStatusIfIdle()
	return nil
}

func (m *Model) handleDiscoveredRefreshedMsg(msg discoveredRefreshedMsg) tea.Cmd {
	if msg.gen != m.discoveryGen {
		return nil
	}
	m.discoveryRefreshing = false
	if msg.err != nil {
		m.finishSetupReloadIfIdle()
		status := "orphan scan failed: " + msg.err.Error()
		if m.collectLaunchBatchError(status) {
			return m.finishLaunchBatchIfIdle()
		}
		return setStatus(m, status, true)
	}
	// A successful scan can legitimately prune the last discovered row, leaving
	// msg.discovered nil; still overwrite so pruned rows don't linger in the UI.
	m.discoveredTools = msg.discovered
	m.rebuildDiscoveredKeys()
	m.applyFilter()
	if anyMissingDescription(msg.discovered) || anyMissingDescription(m.allTools) {
		return m.startDescriptionRefresh()
	}

	m.finishSetupReloadIfIdle()
	if cmd := m.finishLaunchBatchIfIdle(); cmd != nil {
		return cmd
	}
	m.clearActivityStatusIfIdle()
	return nil
}

func (m *Model) clearActivityStatusIfIdle() {
	if m.launchBatchActive || m.setupReloading || m.launchBatchPending() {
		return
	}
	m.progressText = ""
}

package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/config"
)

func (m *Model) handleToolsLoadedMsg(msg toolsLoadedMsg) []tea.Cmd {
	var cmds []tea.Cmd

	m.loading = false
	if msg.noConfig {
		m.mode = viewSetup
		m.setupBackgroundMode = viewList
		m.setupStep = 0
		return nil
	}
	if msg.err != nil {
		m.err = msg.err
		return nil
	}
	// Config was just created in setup step 0 — advance to provider/import step.
	if m.mode == viewSetup && m.setupStep == 0 {
		m.setupStep = 1
		m.setupProviders = msg.setupProviders
		m.setupProviderIdx = 0
		return nil
	}

	if msg.noProfile {
		// Config exists but no profile is mapped to this machine. Keep the
		// background list as the pre-onboarding snapshot; fresh scans/reloads run
		// only after onboarding exits.
		m.settings = msg.settings
		m.taps = msg.taps
		m.effectivePythonManager = msg.effectivePythonManager
		m.effectiveNodeManager = msg.effectiveNodeManager
		m.effectiveSystemManager = msg.effectiveSystemManager
		m.profileInfo = msg.profileInfo
		m.setupProviders = msg.setupProviders
		m.setupProviderIdx = 0
		m.mode = viewSetup
		m.setupBackgroundMode = viewList
		m.setupStep = 2
		return cmds
	}

	m.allTools = msg.tools
	m.discoveredTools = msg.discovered
	m.rebuildDiscoveredKeys()
	m.settings = msg.settings
	m.taps = msg.taps
	m.groupNames = msg.groupNames
	m.toolGroups = msg.toolGroups
	m.toolMemberships = msg.toolMemberships
	m.dotMemberships = msg.dotMemberships
	m.ignoreLabels = msg.ignoreLabels
	m.toolIgnoreSet = msg.toolIgnoreSet
	m.groupIgnoreSet = msg.groupIgnoreSet
	m.toolProviderPins = msg.toolProviderPins
	m.configuredProviders = append([]string(nil), msg.configuredProviders...)
	m.effectivePythonManager = msg.effectivePythonManager
	m.effectiveNodeManager = msg.effectiveNodeManager
	m.effectiveSystemManager = msg.effectiveSystemManager
	m.stowInstalled = msg.stowInstalled
	if len(msg.ecosystemProviders) > 0 {
		m.providerNames = append([]string(nil), msg.ecosystemProviders...)
	}
	m.profileInfo = msg.profileInfo
	m.ignoreSet = make(map[string]bool, len(msg.ignoreList))
	for _, name := range msg.ignoreList {
		m.ignoreSet[name] = true
	}
	if m.app != nil {
		m.consolidateOptions = m.app.ConsolidateOptions()
	}
	m.applyFilter()
	if m.setupBackgroundMode == viewDots {
		m.mode = viewDots
		m.setupBackgroundMode = viewList
	} else {
		m.mode = viewList
		m.setupBackgroundMode = viewList
	}
	m.profileRequired = false

	if m.settings.DotsRepo != "" && !config.BoolVal(m.settings.DotsDisabled) && !m.dotsLoaded && !m.dotsLoading {
		if m.promptForStowInstall(stowInstallLaunchSync) {
			return cmds
		}
		m.beginDotsOperation("Syncing dots…")
		cmds = append(cmds, m.spinner.Tick, m.doDotsSyncOnly())
	}

	cmds = append(cmds, m.startPostLoadBackgroundTasks()...)
	return cmds
}

func (m *Model) handleSetupImportDoneMsg(msg setupImportDoneMsg) []tea.Cmd {
	var cmds []tea.Cmd

	m.loading = false
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
	} else if msg.added > 0 {
		cmds = append(cmds, setStatus(m, fmt.Sprintf("✓ %d imported", msg.added), false))
	} else {
		cmds = append(cmds, setStatus(m, "✓ nothing to import", false))
	}
	m.advanceSetupPastProviders(&cmds)
	return cmds
}

func (m *Model) handleSetupProvidersDoneMsg(msg setupProvidersDoneMsg) []tea.Cmd {
	var cmds []tea.Cmd

	m.loading = false
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
	}
	m.advanceSetupPastProviders(&cmds)
	return cmds
}

func (m *Model) handleSetupNodeMgrDoneMsg(msg setupNodeMgrDoneMsg) []tea.Cmd {
	var cmds []tea.Cmd

	m.loading = false
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
	}
	m.advanceToSetupProfileStep(&cmds)
	return cmds
}

func (m *Model) handleSetupProfileDoneMsg(msg setupProfileDoneMsg) []tea.Cmd {
	var cmds []tea.Cmd

	m.loading = false
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
	} else if msg.profileName != "" {
		cmds = append(cmds, setStatus(m, "✓ profile "+msg.profileName+" created", false))
	}
	m.settingsInput.Blur()
	m.setupStep = 5
	return cmds
}

func (m *Model) handleSetupKeyMsg(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	var cmds []tea.Cmd

	switch m.setupStep {
	case 0: // Create config?
		switch strings.ToLower(msg.String()) {
		case "y":
			m.loading = true
			startOp(m, "Creating settings.json…")
			cmds = append(cmds, m.spinner.Tick, m.doCreateConfig())
		case "n", "esc":
			m.shutdown()
			return tea.Quit, true
		}
	case 1, 2: // Provider selection (step 1: first-run + import; step 2: no-profile re-run)
		switch {
		case key.Matches(msg, m.keys.Up):
			if m.setupProviderIdx > 0 {
				m.setupProviderIdx--
			}
		case key.Matches(msg, m.keys.Down):
			if m.setupProviderIdx < len(m.setupProviders)-1 {
				m.setupProviderIdx++
			}
		case key.Matches(msg, m.keys.Toggle):
			if m.setupProviderIdx >= 0 && m.setupProviderIdx < len(m.setupProviders) {
				m.setupProviders[m.setupProviderIdx].enabled = !m.setupProviders[m.setupProviderIdx].enabled
			}
		case key.Matches(msg, m.keys.Confirm):
			m.confirmSetupProviders(&cmds)
		}
	case 3: // Node manager selection
		switch {
		case key.Matches(msg, m.keys.Up):
			if m.setupNodeMgrIdx > 0 {
				m.setupNodeMgrIdx--
			}
		case key.Matches(msg, m.keys.Down):
			if m.setupNodeMgrIdx < len(nodeMgrChoices)-1 {
				m.setupNodeMgrIdx++
			}
		case key.Matches(msg, m.keys.Confirm):
			chosen := nodeMgrChoices[m.setupNodeMgrIdx].value
			if chosen == "" {
				m.advanceToSetupProfileStep(&cmds)
			} else {
				m.loading = true
				startOp(m, "Saving…")
				cmds = append(cmds, m.spinner.Tick, m.doSaveNodeManager(chosen))
			}
		case key.Matches(msg, m.keys.Back):
			m.advanceToSetupProfileStep(&cmds)
		}
	case 4: // Profile name for this machine (required)
		if quit := m.handleSetupProfileNameKey(msg, &cmds); quit {
			return tea.Quit, true
		}
	case 5: // Enable dotfile sync? (y/n)
		switch strings.ToLower(msg.String()) {
		case "y":
			m.setupStep = 6
			cmds = append(cmds, m.openFilePicker("Dots repo path", "", false))
		case "n", "esc":
			m.loading = true
			startOp(m, "Disabling dots…")
			cmds = append(cmds, m.spinner.Tick, m.doDisableDots(true))
		}
	case 6: // Dots repo path — handled by showFilePicker routing above.
		if key.Matches(msg, m.keys.Back) {
			m.loading = true
			startOp(m, "Loading…")
			cmds = append(cmds, m.spinner.Tick, loadTools(m.app, m.ctx))
		}
	}

	return tea.Batch(cmds...), false
}

func (m *Model) confirmSetupProviders(cmds *[]tea.Cmd) {
	var disabled []string
	for _, row := range m.setupProviders {
		if !row.enabled {
			disabled = append(disabled, row.name)
		}
	}
	if m.setupStep == 1 {
		m.loading = true
		startOp(m, "Importing tools…")
		*cmds = append(*cmds, m.spinner.Tick, m.doSetupImport(disabled))
		return
	}
	if len(disabled) > 0 {
		m.loading = true
		startOp(m, "Saving providers…")
		*cmds = append(*cmds, m.spinner.Tick, m.doSaveDisabledProviders(disabled))
		return
	}
	m.advanceSetupPastProviders(cmds)
}

func (m *Model) handleSetupProfileNameKey(msg tea.KeyPressMsg, cmds *[]tea.Cmd) bool {
	if m.setupExitConfirm {
		switch strings.ToLower(msg.String()) {
		case "y":
			m.cancelConfirmationTimeout()
			m.shutdown()
			return true
		case "n", "esc":
			m.cancelConfirmationTimeout()
			m.setupExitConfirm = false
			m.settingsInput.Focus()
			*cmds = append(*cmds, textinput.Blink)
		}
		return false
	}
	switch {
	case key.Matches(msg, m.keys.Confirm):
		name := strings.TrimSpace(m.settingsInput.Value())
		m.settingsInput.Blur()
		if name != "" {
			m.loading = true
			startOp(m, "Creating profile…")
			*cmds = append(*cmds, m.spinner.Tick, m.doSetupProfile(name))
		} else {
			m.setupExitConfirm = true
			m.settingsInput.Blur()
			*cmds = append(*cmds, m.armConfirmationTimeout())
		}
	case key.Matches(msg, m.keys.Back):
		m.setupExitConfirm = true
		m.settingsInput.Blur()
		*cmds = append(*cmds, m.armConfirmationTimeout())
	default:
		var cmd tea.Cmd
		m.settingsInput, cmd = m.settingsInput.Update(msg)
		*cmds = append(*cmds, cmd)
	}
	return false
}

func (m *Model) startCurrentProviderScans() []tea.Cmd {
	var cmds []tea.Cmd

	// Use the UNION of DB-row providers and config-declared providers so scans
	// run on first launch after import. Import() writes JSON config, not DB rows.
	m.scanningProviders = make(map[string]bool)
	for _, t := range m.allTools {
		m.scanningProviders[t.Provider] = true
	}
	for _, p := range m.configuredProviders {
		m.scanningProviders[p] = true
	}
	if len(m.scanningProviders) == 0 {
		return nil
	}
	m.scanGen++
	gen := m.scanGen
	cmds = append(cmds, m.spinner.Tick)
	for prov := range m.scanningProviders {
		cmds = append(cmds, m.doScanProvider(prov, gen))
	}
	return cmds
}

func (m *Model) startPostLoadBackgroundTasks() []tea.Cmd {
	cmds := m.startCurrentProviderScans()
	if anyMissingDescription(m.allTools) {
		cmds = append(cmds, m.startDescriptionRefresh())
	}
	return cmds
}

func (m *Model) advanceSetupPastProviders(cmds *[]tea.Cmd) {
	if isNodeProviderEnabled(m.setupProviders) {
		m.setupStep = 3
		return
	}
	m.advanceToSetupProfileStep(cmds)
}

func (m *Model) advanceToSetupProfileStep(cmds *[]tea.Cmd) {
	m.setupStep = 4
	m.settingsInput.Placeholder = "profile name…"
	m.settingsInput.SetValue(m.defaultSetupProfileName())
	m.settingsInput.Focus()
	*cmds = append(*cmds, textinput.Blink)
}

func (m *Model) defaultSetupProfileName() string {
	if m.profileInfo == nil || len(m.profileInfo.Profiles) == 0 {
		return "default"
	}
	return shortHostname()
}

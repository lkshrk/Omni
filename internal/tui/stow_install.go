package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/config"
)

func (m *Model) promptForStowInstall(action stowInstallAction) bool {
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if m.app == nil || m.stowInstalled || m.app.DotsStowInstalled(ctx) {
		m.stowInstalled = true
		return false
	}
	m.stowInstallPrompt = true
	m.stowInstallAction = action
	m.cancelConfirmationTimeout()
	return true
}

func (m *Model) handleStowInstallKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd
	switch strings.ToLower(msg.String()) {
	case "y":
		action := m.stowInstallAction
		m.stowInstallPrompt = false
		m.stowInstallAction = stowInstallNone
		m.loading = true
		startOp(m, "Installing stow...")
		cmds = append(cmds, m.spinner.Tick, m.doInstallStow(action))
	case "n", "esc":
		action := m.stowInstallAction
		m.stowInstallPrompt = false
		m.stowInstallAction = stowInstallNone
		cmds = append(cmds, m.cancelStowInstallAction(action)...)
	default:
		if key.Matches(msg, m.keys.Back) {
			action := m.stowInstallAction
			m.stowInstallPrompt = false
			m.stowInstallAction = stowInstallNone
			cmds = append(cmds, m.cancelStowInstallAction(action)...)
		}
	}
	return cmds
}

func (m *Model) cancelStowInstallAction(action stowInstallAction) []tea.Cmd {
	var cmds []tea.Cmd
	if action == stowInstallLaunchSync {
		cmds = append(cmds, m.startPostLoadBackgroundTasks()...)
	}
	cmds = append(cmds, setStatus(m, "GNU Stow is required for dotfile sync.", true))
	return cmds
}

func (m *Model) doInstallStow(action stowInstallAction) tea.Cmd {
	a, ctx := m.app, m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		return stowInstallDoneMsg{action: action, err: a.InstallDotsStow(ctx)}
	}
}

func (m *Model) handleStowInstallDoneMsg(msg stowInstallDoneMsg) []tea.Cmd {
	m.loading = false
	if msg.err != nil {
		cmds := []tea.Cmd{setStatus(m, "✗ "+msg.err.Error(), true)}
		if msg.action == stowInstallLaunchSync {
			cmds = append(cmds, m.startPostLoadBackgroundTasks()...)
		}
		return cmds
	}
	m.stowInstalled = true
	return m.resumeStowInstallAction(msg.action)
}

func (m *Model) resumeStowInstallAction(action stowInstallAction) []tea.Cmd {
	var cmds []tea.Cmd
	switch action {
	case stowInstallLaunchSync:
		m.beginDotsOperation("Syncing dots...")
		cmds = append(cmds, m.spinner.Tick, m.doDotsSyncOnly())
		cmds = append(cmds, m.startPostLoadBackgroundTasks()...)
	case stowInstallEnableDots:
		m.beginDotsOperation("Enabling dots...")
		cmds = append(cmds, m.spinner.Tick, m.doEnableDots())
	case stowInstallSaveSettingsSync:
		m.settings = m.stowInstallSettings
		m.dotsLoaded = false
		m.beginDotsOperation("Syncing dots...")
		cmds = append(cmds, m.spinner.Tick, m.doSaveSettingsAndDotsSync(m.stowInstallSettings))
		m.stowInstallSettings = config.Settings{}
	case stowInstallSetupDotsRepo:
		path := m.stowInstallPath
		m.stowInstallPath = ""
		m.loading = true
		startOp(m, "Saving...")
		cmds = append(cmds, m.spinner.Tick, m.doSetupDotsRepo(path))
	default:
		cmds = append(cmds, finishOpOK(m, "stow installed"))
	}
	return cmds
}

func renderStowInstallPopup(m Model) string {
	p := m.palette
	var sb strings.Builder
	sb.WriteString(p.styleNormal.Render("GNU Stow is required for dotfile sync."))
	sb.WriteString("\n\n")
	manager := m.effectiveSystemManager
	if manager == "" {
		manager = "system"
	}
	sb.WriteString(p.styleHelp.Render(fmt.Sprintf("Install stow with %s now?", manager)))
	sb.WriteString("\n\n")
	sb.WriteString(p.styleInstalled.Render("  [y] ") + p.styleNormal.Render("Install stow"))
	sb.WriteString("\n")
	sb.WriteString(p.styleMissing.Render("  [n] ") + p.styleNormal.Render("Not now"))
	return sb.String()
}

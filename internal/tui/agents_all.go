package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

const agentsBusyStatus = "⚠ APM busy — wait for the running command to finish"

type apmCommandDoneMsg struct {
	command string
	stdout  string
	stderr  string
	err     error
}

func (m *Model) agentsOpInFlight() bool { return m.apmRunning }

func (m *Model) runAPM(command string, args ...string) []tea.Cmd {
	if m.app == nil {
		return nil
	}
	m.apmRunning = true
	m.apmCommand = command
	m.apmOutput = ""
	m.apmErr = nil
	a, ctx := m.app, m.ctx
	return []tea.Cmd{m.spinner.Tick, func() tea.Msg {
		result, err := a.RunAPM(ctx, args...)
		return apmCommandDoneMsg{command: command, stdout: result.Stdout, stderr: result.Stderr, err: err}
	}}
}

func (m *Model) doAgentsSyncAll() []tea.Cmd {
	return m.runAPM("apm install -g", "install", "-g")
}

func (m *Model) doAgentsUpdateAll() []tea.Cmd {
	return m.runAPM("apm update -g --yes", "update", "-g", "--yes")
}

func (m *Model) doAgentsRefresh() []tea.Cmd {
	return m.runAPM("apm deps list -g", "deps", "list", "-g")
}

func (m *Model) handleAgentsGlobalActionKeyMsg(msg tea.KeyPressMsg) (bool, []tea.Cmd) {
	key := msg.String()
	if key != "U" && key != "S" && key != "R" && key != "e" {
		return false, nil
	}
	if key == "e" {
		return true, []tea.Cmd{m.openTraceLog()}
	}
	if m.agentsOpInFlight() {
		return true, []tea.Cmd{setStatus(m, agentsBusyStatus, true)}
	}
	switch key {
	case "U":
		return true, m.doAgentsUpdateAll()
	case "S":
		return true, m.doAgentsSyncAll()
	default:
		return true, m.doAgentsRefresh()
	}
}

func apmCommandOutput(stdout, stderr string) string {
	parts := make([]string, 0, 2)
	if stdout = strings.TrimSpace(stdout); stdout != "" {
		parts = append(parts, stdout)
	}
	if stderr = strings.TrimSpace(stderr); stderr != "" {
		parts = append(parts, stderr)
	}
	return strings.Join(parts, "\n")
}

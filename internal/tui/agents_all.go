package tui

import (
	"context"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
)

const agentsBusyStatus = "⚠ APM busy — wait for the running command to finish"

const agentsUpdateCheckBusyStatus = "checking APM package updates — wait for it to finish"

type agentsStartupMsg struct{}

type apmCommandDoneMsg struct {
	command string
	stdout  string
	stderr  string
	notices []string
	err     error
}

type agentsRowsMsg struct {
	gen    int
	status app.AgentsStatus
	err    error
}

type agentsOutdatedMsg struct {
	gen    int
	result app.AgentsOutdatedResult
	err    error
}

func (m *Model) parkAgentsFilter() {
	m.agentsFilterQuery = m.filter.Value()
	m.filter.SetValue(m.filterOutsideAgents)
	m.filter.Blur()
}

func (m *Model) restoreAgentsFilter() {
	m.filterOutsideAgents = m.filter.Value()
	m.filter.SetValue(m.agentsFilterQuery)
	m.filter.Blur()
	m.agentsSearchActive = m.agentsFilterQuery != ""
}

func (m *Model) openAgentsFilter() {
	m.agentsSearchActive = true
	m.filter.Focus()
	m.agentsCursor = 0
	// Navigation stays live while typing, so the selection has to be visible from the first keystroke.
	m.cursorHidden = false
}

func (m *Model) closeAgentsFilter() {
	m.agentsSearchActive = false
	m.filter.SetValue("")
	m.filter.Blur()
	m.agentsFilterQuery = ""
	m.agentsCursor = 0
}

func (m Model) agentsFilterText() string {
	if !m.agentsSearchActive {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(m.filter.Value()))
}

// Only the non-printable navigation keys are intercepted while the input has focus; j and k must still type.
func (m *Model) handleAgentsSearchNavKeyMsg(msg tea.KeyPressMsg) bool {
	n := m.agentsRowCount()
	page := max(listAvailableHeight(*m)/2, 1)
	ctrl := msg.Mod&^lockMods == tea.ModCtrl
	switch {
	case msg.Code == tea.KeyUp || (ctrl && msg.Code == 'p'):
		m.agentsCursor = cursorMove(m.agentsCursor, -1, n, true)
	case msg.Code == tea.KeyDown || (ctrl && msg.Code == 'n'):
		m.agentsCursor = cursorMove(m.agentsCursor, 1, n, true)
	case msg.Code == tea.KeyPgUp || (ctrl && msg.Code == 'u'):
		m.agentsCursor = max(m.agentsCursor-page, 0)
	case msg.Code == tea.KeyPgDown || (ctrl && msg.Code == 'd'):
		m.agentsCursor = min(m.agentsCursor+page, max(n-1, 0))
	case msg.Code == tea.KeyHome:
		m.agentsCursor = 0
	case msg.Code == tea.KeyEnd:
		m.agentsCursor = max(n-1, 0)
	default:
		return false
	}
	m.cursorHidden = false
	m.agentsConfirmIdx = -1
	return true
}

func (m *Model) handleAgentsSearchKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	if m.handleAgentsSearchNavKeyMsg(msg) {
		return nil
	}
	switch {
	case key.Matches(msg, m.keys.Back):
		if m.agentsRegistryMode {
			m.closeAgentsRegistry()
			return nil
		}
		m.closeAgentsFilter()
		return nil
	case key.Matches(msg, m.keys.Confirm):
		if m.agentsRegistryMode {
			return m.handleAgentsRegistryEnter()
		}
		m.filter.Blur()
		return nil
	}
	previous := m.filter.Value()
	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(msg)
	if m.filter.Value() != previous {
		m.agentsCursor = 0
		m.agentsConfirmIdx = -1
	}
	m.agentsCursor = clampIndex(m.agentsCursor, m.agentsRowCount())
	return []tea.Cmd{cmd}
}

func (m *Model) doLoadAgentsRows() tea.Cmd {
	if m.app == nil {
		return nil
	}
	m.agentsRowsGen++
	gen, a := m.agentsRowsGen, m.app
	return func() tea.Msg {
		status, err := a.AgentsStatus()
		return agentsRowsMsg{gen: gen, status: status, err: err}
	}
}

func (m *Model) doCheckAgentsOutdated() tea.Cmd {
	if m.app == nil {
		return nil
	}
	m.agentsOutdatedGen++
	gen, a, parent := m.agentsOutdatedGen, m.app, m.ctx
	m.agentsOutdatedChecking = true
	m.agentsOutdatedErr = nil
	m.agentsOutdatedUnknown = 0
	m.agentsOutdatedResult = app.AgentsOutdatedResult{}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 45*time.Second)
		defer cancel()
		result, err := a.AgentsOutdated(ctx)
		return agentsOutdatedMsg{gen: gen, result: result, err: err}
	}
}

func (m *Model) refreshAgents() []tea.Cmd {
	m.agentsRowsKnown = false
	m.agentsRowsErr = nil
	m.agentsSyncActionable = 0
	app.ApplyAgentsOutdated(m.agentsRows, app.AgentsOutdatedResult{})
	cmds := []tea.Cmd{m.doLoadAgentsRows()}
	if m.agentsOutdatedChecking {
		return append(cmds, setStatus(m, agentsUpdateCheckBusyStatus, false))
	}
	return append(cmds, m.doCheckAgentsOutdated())
}

// Returns the spinner plus the caller's work, or nil when there is no app to run against.
func (m *Model) beginAPMOp(command string, work tea.Cmd) []tea.Cmd {
	if m.app == nil {
		return nil
	}
	m.apmRunning = true
	m.apmCommand = command
	m.apmOutput = ""
	m.apmErr = nil
	m.agentsRemovalHint = nil
	m.agentsRowOpSpec = ""
	return []tea.Cmd{m.spinner.Tick, work}
}

func (m *Model) runAPM(command string, args ...string) []tea.Cmd {
	a, ctx := m.app, m.ctx
	return m.beginAPMOp(command, func() tea.Msg {
		result, err := a.RunAPM(ctx, args...)
		output := apmCommandOutput(result.Stdout, result.Stderr)
		return apmCommandDoneMsg{command: command, stdout: result.Stdout, stderr: result.Stderr, notices: capAgentsNotices(apmMarkedLines(output)), err: err}
	})
}

// The TUI has no flags, so sync runs the plain CLI lifecycle: host template first, then install.
func (m *Model) doAgentsSyncAll() []tea.Cmd {
	const command = "omni agents sync"
	a, ctx := m.app, m.ctx
	return m.beginAPMOp(command, func() tea.Msg {
		result, err := a.AgentsSyncAll(ctx, app.AgentsSyncAllOptions{})
		notices := slices.Clone(result.Notices)
		if result.Warning != "" {
			notices = append(notices, "warning: "+result.Warning)
		}
		// apm's own verdict lives in the install output, which the structured result does not carry.
		notices = append(notices, apmMarkedLines(apmCommandOutput(result.Output, result.Stderr))...)
		return apmCommandDoneMsg{command: command, stdout: result.Output, stderr: result.Stderr, notices: capAgentsNotices(notices), err: err}
	})
}

func (m *Model) doAgentsUpdateAll() []tea.Cmd {
	return m.runAPM("apm update -g --yes", "update", "-g", "--yes")
}

func (m *Model) handleAgentsGlobalActionKeyMsg(msg tea.KeyPressMsg) (bool, []tea.Cmd) {
	if !m.agentsRegistryMode {
		if handled, cmds := m.handleAgentsRowOpKeyMsg(msg); handled {
			return true, cmds
		}
	}
	updateAll := key.Matches(msg, m.keys.AgentsUpdateAll)
	syncAll := key.Matches(msg, m.keys.AgentsSync)
	refresh := key.Matches(msg, m.keys.AgentsRefresh)
	add := key.Matches(msg, m.keys.AgentsAdd)
	keyText := msg.String()
	if !updateAll && !syncAll && !refresh && !add && keyText != "e" && keyText != "/" && keyText != "enter" {
		return false, nil
	}
	m.agentsConfirmIdx = -1
	if keyText == "e" {
		return true, []tea.Cmd{m.openTraceLog()}
	}
	if add {
		// Registry mode already owns the input; re-entering it would only reset the query.
		if m.agentsRegistryMode {
			return true, nil
		}
		return true, m.openAgentsRegistry()
	}
	if keyText == "/" {
		if m.agentsRegistryMode {
			return true, nil
		}
		m.openAgentsFilter()
		return true, nil
	}
	if keyText == "enter" {
		if m.agentsRegistryMode {
			return true, m.handleAgentsRegistryEnter()
		}
		return false, nil
	}
	if m.apmRunning {
		return true, []tea.Cmd{setStatus(m, agentsBusyStatus, true)}
	}
	if m.agentsOutdatedChecking && (updateAll || syncAll || refresh) {
		return true, []tea.Cmd{setStatus(m, agentsUpdateCheckBusyStatus, false)}
	}
	switch {
	case updateAll:
		return true, m.doAgentsUpdateAll()
	case syncAll:
		return true, m.doAgentsSyncAll()
	default:
		m.apmCommand, m.apmOutput, m.apmErr = "", "", nil
		return true, m.refreshAgents()
	}
}

func agentsRowMatches(query, name, detail string) bool {
	if query == "" {
		return true
	}
	return strings.Contains(strings.ToLower(name+" "+detail), query)
}

func (m Model) agentsVisiblePackages() []app.AgentsPackageRow {
	query := m.agentsFilterText()
	out := make([]app.AgentsPackageRow, 0, len(m.agentsRows))
	for _, updates := range []bool{true, false} {
		for _, row := range m.agentsRows {
			search := row.Source + " " + strings.Join(row.Issues, " ")
			for _, child := range row.Provides {
				search += " " + child.Kind + " " + child.Name
			}
			if row.UpdateAvailable == updates && agentsRowMatches(query, row.Name, search) {
				out = append(out, row)
			}
		}
	}
	return out
}

func (m Model) agentsVisibleServices(rows []app.AgentsServiceRow) []app.AgentsServiceRow {
	query := m.agentsFilterText()
	if query == "" {
		return rows
	}
	out := make([]app.AgentsServiceRow, 0, len(rows))
	for _, row := range rows {
		if agentsRowMatches(query, row.Name, row.Detail) {
			out = append(out, row)
		}
	}
	return out
}

func (m Model) agentsTotalRowCount() int {
	return len(m.agentsRows) + len(m.agentsMCPRows) + len(m.agentsLSPRows)
}

func (m Model) agentsRowCount() int {
	if m.agentsRegistryMode {
		return len(m.agentsVisibleRegistry())
	}
	return len(m.agentsVisiblePackages()) + len(m.agentsVisibleServices(m.agentsMCPRows)) + len(m.agentsVisibleServices(m.agentsLSPRows))
}

func (m *Model) handleAgentsNavigationKeyMsg(msg tea.KeyPressMsg) bool {
	n := m.agentsRowCount()
	before := m.agentsCursor
	switch {
	case key.Matches(msg, m.keys.Up):
		m.agentsCursor = cursorMove(m.agentsCursor, -1, n, true)
	case key.Matches(msg, m.keys.Down):
		m.agentsCursor = cursorMove(m.agentsCursor, 1, n, true)
	case key.Matches(msg, m.keys.Top):
		m.agentsCursor = 0
	case key.Matches(msg, m.keys.Bottom):
		m.agentsCursor = max(n-1, 0)
	case key.Matches(msg, m.keys.HalfPageDown):
		m.agentsCursor = min(m.agentsCursor+max(listAvailableHeight(*m)/2, 1), max(n-1, 0))
	case key.Matches(msg, m.keys.HalfPageUp):
		m.agentsCursor = max(m.agentsCursor-max(listAvailableHeight(*m)/2, 1), 0)
	case key.Matches(msg, m.keys.PageDown):
		m.agentsCursor = min(m.agentsCursor+max(listAvailableHeight(*m), 1), max(n-1, 0))
	case key.Matches(msg, m.keys.PageUp):
		m.agentsCursor = max(m.agentsCursor-max(listAvailableHeight(*m), 1), 0)
	default:
		return false
	}
	m.cursorHidden = false
	if m.agentsCursor != before {
		m.agentsConfirmIdx = -1
	}
	return true
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

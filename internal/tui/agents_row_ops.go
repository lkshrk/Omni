package tui

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
)

type agentsRowKind uint8

const (
	agentsRowPackage agentsRowKind = iota
	agentsRowService
)

type agentsRowRef struct {
	kind    agentsRowKind
	pkg     app.AgentsPackageRow
	service app.AgentsServiceRow
}

func (m Model) agentsSelectedRow() (agentsRowRef, bool) {
	if m.cursorHidden {
		return agentsRowRef{}, false
	}
	idx := m.agentsCursor
	packages := m.agentsVisiblePackages()
	if idx < len(packages) {
		return agentsRowRef{kind: agentsRowPackage, pkg: packages[idx]}, true
	}
	idx -= len(packages)
	for _, rows := range [][]app.AgentsServiceRow{m.agentsVisibleServices(m.agentsMCPRows), m.agentsVisibleServices(m.agentsLSPRows)} {
		if idx < len(rows) {
			return agentsRowRef{kind: agentsRowService, service: rows[idx]}, true
		}
		idx -= len(rows)
	}
	return agentsRowRef{}, false
}

// The uninstall key resolves the lockfile's own package key; anything else apm refuses to select.
func agentsUninstallSpec(row app.AgentsPackageRow) string {
	if row.LocalPath != "" {
		return row.LocalPath
	}
	return row.Source
}

func agentsUpdateAction(row app.AgentsPackageRow) (spec, status string) {
	switch {
	case row.Status == app.AgentsPackageOrphaned:
		return "", "⚠ " + row.Name + " is not declared in apm.yml — declare it in the host template first"
	case row.Status == app.AgentsPackageMissing:
		return "", "⚠ " + row.Name + " is not installed — run S to sync it first"
	case row.Local():
		return "", "⚠ " + row.Name + " is a local path — apm never updates local dependencies"
	case row.Ref != "":
		return "", "⚠ " + row.Name + " is pinned to " + row.Ref + " — no version picker yet; update the pin in the host template"
	default:
		return row.Source, ""
	}
}

func agentsUninstallAction(row app.AgentsPackageRow) (spec, status string) {
	switch {
	case row.Status == app.AgentsPackageOrphaned:
		return "", "⚠ " + row.Name + " is not declared in apm.yml — apm cannot select it for uninstall"
	case row.Status == app.AgentsPackageMissing:
		return "", "⚠ " + row.Name + " is not installed"
	default:
		return agentsUninstallSpec(row), ""
	}
}

const agentsServiceOpStatus = "⚠ mcp and lsp servers are edited in ~/.apm/apm.yml, then reconciled with S"

// A limitation is a sentence, not a keybind, so it sits beside the hints instead of inside them.
func agentsRowLimitations(m Model) []string {
	row, ok := m.agentsSelectedRow()
	if !ok {
		return nil
	}
	if row.kind == agentsRowService {
		return []string{strings.TrimPrefix(agentsServiceOpStatus, "⚠ ")}
	}
	var out []string
	for _, status := range []string{mustStatus(agentsUpdateAction(row.pkg)), mustStatus(agentsUninstallAction(row.pkg))} {
		if status = strings.TrimPrefix(status, "⚠ "); status != "" && !slices.Contains(out, status) {
			out = append(out, status)
		}
	}
	return out
}

func mustStatus(_ string, status string) string { return status }

func agentsRowHintItems(m Model) []hintItem {
	row, ok := m.agentsSelectedRow()
	if !ok || row.kind == agentsRowService {
		return nil
	}
	var items []hintItem
	if _, status := agentsUpdateAction(row.pkg); status == "" {
		items = append(items, hintFromBinding(agentsRowUpdateBinding()))
	}
	if _, status := agentsUninstallAction(row.pkg); status == "" {
		items = append(items, hintFromBinding(agentsRowUninstallBinding()))
	}
	return items
}

func (m *Model) doAgentsRowUpdate(row app.AgentsPackageRow) []tea.Cmd {
	spec, status := agentsUpdateAction(row)
	if status != "" {
		return []tea.Cmd{setStatus(m, status, false)}
	}
	return m.runAPMRowOp("apm update -g --yes "+spec, spec, "update", "-g", "--yes", spec)
}

func (m *Model) doAgentsRowUninstall(row app.AgentsPackageRow) []tea.Cmd {
	spec, status := agentsUninstallAction(row)
	if status != "" {
		return []tea.Cmd{setStatus(m, status, false)}
	}
	cmds := m.runAPMRowOp("apm uninstall -g "+spec, spec, "uninstall", "-g", spec)
	m.agentsRemovalHint = app.AgentsTemplateHintLines(spec, true)
	return cmds
}

// A row-scoped op still runs apm's scope-wide passes, whose summary counts describe the workspace, not this row.
func agentsRowScopedNotice(line string) bool {
	line = strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(line, "[x]") || strings.HasPrefix(line, "[!]"):
		return true
	case strings.Contains(line, "APM dependencies.") || strings.Contains(line, "APM dependency."):
		return false
	case strings.Contains(line, "(removed)"):
		return false
	}
	return true
}

func agentsRowOpNotices(output string) []string {
	kept := make([]string, 0, 4)
	for _, line := range apmMarkedLines(output) {
		if agentsRowScopedNotice(line) {
			kept = append(kept, line)
		}
	}
	return kept
}

func (m *Model) runAPMRowOp(command, spec string, args ...string) []tea.Cmd {
	a, ctx := m.app, m.ctx
	cmds := m.beginAPMOp(command, func() tea.Msg {
		result, err := a.RunAPM(ctx, args...)
		output := apmCommandOutput(result.Stdout, result.Stderr)
		return apmCommandDoneMsg{command: command, stdout: result.Stdout, stderr: result.Stderr, notices: capAgentsNotices(agentsRowOpNotices(output)), err: err}
	})
	if cmds != nil {
		m.agentsRowOpSpec = spec
	}
	return cmds
}

func (m *Model) handleAgentsRowOpKeyMsg(key string) (bool, []tea.Cmd) {
	if key != "u" && key != "x" {
		return false, nil
	}
	row, ok := m.agentsSelectedRow()
	if !ok {
		return true, nil
	}
	if row.kind == agentsRowService {
		m.agentsConfirmIdx = -1
		return true, []tea.Cmd{setStatus(m, agentsServiceOpStatus, false)}
	}
	if m.apmRunning {
		return true, []tea.Cmd{setStatus(m, agentsBusyStatus, true)}
	}
	if m.agentsOutdatedChecking {
		return true, []tea.Cmd{setStatus(m, agentsUpdateCheckBusyStatus, false)}
	}
	if key == "u" {
		m.agentsConfirmIdx = -1
		return true, m.doAgentsRowUpdate(row.pkg)
	}
	if _, status := agentsUninstallAction(row.pkg); status != "" {
		m.agentsConfirmIdx = -1
		return true, []tea.Cmd{setStatus(m, status, false)}
	}
	if m.agentsConfirmIdx == m.agentsCursor {
		m.agentsConfirmIdx = -1
		return true, m.doAgentsRowUninstall(row.pkg)
	}
	m.agentsConfirmIdx = m.agentsCursor
	return true, []tea.Cmd{m.armConfirmationTimeout()}
}

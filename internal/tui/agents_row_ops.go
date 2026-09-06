package tui

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/actions"
	"github.com/lkshrk/omni/internal/app"
)

type agentsRowKind uint8

const (
	agentsRowPackage agentsRowKind = iota
	agentsRowService
	agentsRowNative
)

type agentsRowRef struct {
	kind    agentsRowKind
	pkg     app.AgentsPackageRow
	service app.AgentsServiceRow
	native  app.AgentsNativeRow
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
	natives := m.agentsVisibleNatives()
	if idx < len(natives) {
		return agentsRowRef{kind: agentsRowNative, native: natives[idx]}, true
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

type agentsQueuedOp struct {
	update bool
	row    app.AgentsPackageRow
}

// runQueuedAgentsRowOp dispatches the op the user asked for while apm was busy.
func (m *Model) runQueuedAgentsRowOp() []tea.Cmd {
	queued := m.agentsQueuedRowOp
	if queued == nil || m.apmRunning || m.agentsOutdatedChecking {
		return nil
	}
	m.agentsQueuedRowOp = nil
	if queued.update {
		return m.doAgentsRowUpdate(queued.row)
	}
	return m.doAgentsRowUninstall(queued.row)
}

func agentsUpdateAction(row app.AgentsPackageRow) (spec, status string) {
	switch {
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
	if parent := row.pkg.ResolvedBy; parent != "" {
		out = append(out, "resolved by "+parent+" — S sync reinstalls it unless you change that package")
	}
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
		items = append(items, hintFromBinding(m.keys.AgentsUpdate))
	}
	if _, status := agentsUninstallAction(row.pkg); status == "" {
		items = append(items, hintFromBinding(m.keys.AgentsRemove))
	}
	return items
}

func agentsNativeHintItems(m Model) []hintItem {
	row, ok := m.agentsSelectedRow()
	if !ok || row.kind != agentsRowNative {
		return nil
	}
	items := []hintItem{hintFromBinding(m.keys.AgentsNativeIgnore)}
	if !row.native.Ignored {
		if row.native.Adoptable {
			items = append(items, hintFromBinding(m.keys.AgentsNativeAdopt))
		}
		// The shared binding is labelled "uninstall" for packages; a native artifact is removed.
		items = append(items, hintFromBindingDesc(m.keys.AgentsRemove, actions.MustTUILabel(actions.AgentsRemoveNative)))
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

// Native artifacts are not APM packages, so they never reach the apm-backed row ops below.
func (m *Model) handleAgentsNativeKeyMsg(msg tea.KeyPressMsg) (bool, []tea.Cmd) {
	ignore := key.Matches(msg, m.keys.AgentsNativeIgnore)
	adopt := key.Matches(msg, m.keys.AgentsNativeAdopt)
	remove := key.Matches(msg, m.keys.AgentsRemove)
	if !ignore && !adopt && !remove {
		return false, nil
	}
	row, ok := m.agentsSelectedRow()
	if !ok || row.kind != agentsRowNative {
		// x also uninstalls APM packages, so only the native-only keys answer here.
		if ignore || adopt {
			return true, []tea.Cmd{setStatus(m, "select a row under "+agentsNativeSectionTitle+" first", false)}
		}
		return false, nil
	}
	native := row.native
	switch {
	case ignore:
		m.agentsConfirmIdx = -1
		return true, m.doAgentsNativeIgnore(native)
	case adopt:
		m.agentsConfirmIdx = -1
		if native.Ignored || !native.Adoptable {
			return true, []tea.Cmd{setStatus(m, agentsNativeAdoptBlocked(native), false)}
		}
		// Adopt writes the host template that sync reads; ignore and remove never touch it.
		if m.apmRunning {
			return true, []tea.Cmd{setStatus(m, agentsBusyStatus, true)}
		}
		return true, m.doAgentsNativeAdopt(native)
	}
	if native.Ignored {
		m.agentsConfirmIdx = -1
		return true, []tea.Cmd{setStatus(m, "⚠ "+native.Identity+" is ignored — press i to unignore before removing", false)}
	}
	if m.agentsConfirmIdx == m.agentsCursor {
		m.agentsConfirmIdx = -1
		return true, m.doAgentsNativeRemove(native)
	}
	m.agentsConfirmIdx = m.agentsCursor
	return true, []tea.Cmd{m.armConfirmationTimeout()}
}

// The adopt result is multi-line for the CLI; the status bar takes its first line.
func agentsAdoptStatusLine(detail, identity string) string {
	if line, _, _ := strings.Cut(strings.TrimSpace(detail), "\n"); line != "" {
		return line
	}
	return "declared " + identity
}

func agentsNativeAdoptBlocked(row app.AgentsNativeRow) string {
	if row.Ignored {
		return "⚠ " + row.Identity + " is ignored — press i to unignore before adopting"
	}
	reason := row.Reason
	if reason == "" {
		reason = "the migration classifier does not import it"
	}
	return "⚠ " + row.Identity + " cannot be adopted: " + reason
}

func (m *Model) doAgentsNativeIgnore(row app.AgentsNativeRow) []tea.Cmd {
	a := m.app
	if a == nil {
		return nil
	}
	sel := row.Selector()
	ignored := row.Ignored
	return []tea.Cmd{func() tea.Msg {
		var err error
		if ignored {
			err = a.AgentUnignore(sel)
		} else {
			err = a.AgentIgnore(sel)
		}
		return agentsNativeOpMsg{err: err, ignored: !ignored, identity: row.Identity}
	}, m.doLoadAgentsNativeRows()}
}

func (m *Model) doAgentsNativeRemove(row app.AgentsNativeRow) []tea.Cmd {
	a, ctx := m.app, m.ctx
	if a == nil {
		return nil
	}
	return []tea.Cmd{func() tea.Msg {
		return agentsNativeOpMsg{err: a.AgentsNativeRemove(ctx, row), removed: true, identity: row.Identity}
	}, m.doLoadAgentsNativeRows()}
}

func (m *Model) doAgentsNativeAdopt(row app.AgentsNativeRow) []tea.Cmd {
	a, ctx, host := m.app, m.ctx, row.IgnoreHost
	if a == nil {
		return nil
	}
	return []tea.Cmd{func() tea.Msg {
		out, err := a.AgentsNativeAdopt(ctx, host, row)
		return agentsNativeOpMsg{err: err, adopted: true, identity: row.Identity, detail: out}
	}, m.doLoadAgentsNativeRows()}
}

func (m *Model) handleAgentsRowOpKeyMsg(msg tea.KeyPressMsg) (bool, []tea.Cmd) {
	update := key.Matches(msg, m.keys.AgentsUpdate)
	remove := key.Matches(msg, m.keys.AgentsRemove)
	if !update && !remove {
		return false, nil
	}
	if m.agentsReadinessPending {
		return true, []tea.Cmd{setStatus(m, "checking APM readiness", false)}
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
	queue := func() []tea.Cmd {
		m.agentsQueuedRowOp = &agentsQueuedOp{update: update, row: row.pkg}
		return []tea.Cmd{setStatus(m, agentsUpdateCheckQueuedStatus, false)}
	}
	if update {
		m.agentsConfirmIdx = -1
		if m.agentsOutdatedChecking {
			return true, queue()
		}
		return true, m.doAgentsRowUpdate(row.pkg)
	}
	if _, status := agentsUninstallAction(row.pkg); status != "" {
		m.agentsConfirmIdx = -1
		return true, []tea.Cmd{setStatus(m, status, false)}
	}
	// Queueing never skips the confirmation: an uninstall is only queued once the second press has confirmed it.
	if m.agentsConfirmIdx == m.agentsCursor {
		m.agentsConfirmIdx = -1
		if m.agentsOutdatedChecking {
			return true, queue()
		}
		return true, m.doAgentsRowUninstall(row.pkg)
	}
	m.agentsConfirmIdx = m.agentsCursor
	return true, []tea.Cmd{m.armConfirmationTimeout()}
}

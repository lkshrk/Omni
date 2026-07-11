package tui

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

// combineMcpErrors folds a top-level error with per-adapter errors so callers that
// only track a single err field (as the mcp done-msg types do today) still surface
// adapter failures instead of silently dropping them.
func combineMcpErrors(err error, adapterErrs []app.McpServerError) error {
	if err == nil && len(adapterErrs) == 0 {
		return nil
	}
	all := make([]error, 0, len(adapterErrs)+1)
	if err != nil {
		all = append(all, err)
	}
	for _, e := range adapterErrs {
		all = append(all, e)
	}
	return errors.Join(all...)
}

type mcpRowsMsg struct {
	rows      []app.McpServerRow
	unmanaged map[string][]app.InstalledMcpServer
	err       error
}

type mcpRestoreDoneMsg struct{ err error }

type mcpRemoveDoneMsg struct {
	name string
	err  error
}

type mcpImportAdoptDoneMsg struct {
	serverName string
	err        error
}

type mcpAgentsSavedMsg struct{ err error }

type mcpAddDoneMsg struct{ err error }

func (m *Model) doLoadMcpRows() tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		rows, unmanaged, err := a.McpServerRows(ctx)
		return mcpRowsMsg{rows: rows, unmanaged: unmanaged, err: err}
	}
}

func (m *Model) doRestoreMcp() tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		_, err := a.RestoreMcpServers(ctx, app.RestoreMcpOptions{})
		return mcpRestoreDoneMsg{err: err}
	}
}

func (m *Model) doRemoveMcp(name string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		res, err := a.RemoveMcpServer(ctx, name)
		return mcpRemoveDoneMsg{name: name, err: combineMcpErrors(err, res.Errors)}
	}
}

// doAddMcp registers a new server built from the add-server form.
func (m *Model) doAddMcp(s config.McpServer) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		res, err := a.AddMcpServer(ctx, s)
		return mcpAddDoneMsg{err: combineMcpErrors(err, res.Errors)}
	}
}

// mcpUnmanagedAgentsFor returns every agent ID whose unmanaged map has an
// entry named name, unioned with clickedAgentID, sorted for determinism.
// Claiming one row of an unmanaged server that is installed under several
// agents must declare all of them in the manifest's Agents field — declaring
// only the clicked row's agent would make the other agents' installs
// unmanaged-by-name-no-longer-true yet un-targeted, so they'd vanish from
// every agents-tab view (see mcp_rows.go's unmanaged detection and
// agents_all.go's skillExpandAgents, both of which key off Agents/manifest
// presence, not actual install state).
func mcpUnmanagedAgentsFor(unmanaged map[string][]app.InstalledMcpServer, name, clickedAgentID string) []string {
	set := map[string]struct{}{clickedAgentID: {}}
	for agentID, srvs := range unmanaged {
		for _, s := range srvs {
			if s.Name == name {
				set[agentID] = struct{}{}
				break
			}
		}
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// mcpUnmanagedConflict reports whether name is unmanaged under more than one
// agent with a differing transport/command/URL, mirroring the CLI's
// importMcpServerByName conflict guard in internal/cli/agents.go so the TUI
// claim path refuses the same ambiguous case instead of silently picking one
// agent's configuration to write to the manifest.
func mcpUnmanagedConflict(unmanaged map[string][]app.InstalledMcpServer, name string, first app.InstalledMcpServer) bool {
	for _, srvs := range unmanaged {
		for _, s := range srvs {
			if s.Name != name {
				continue
			}
			if s.Transport != first.Transport || s.Command != first.Command || s.URL != first.URL {
				return true
			}
		}
	}
	return false
}

// doImportMcpServerWithGroup adopts one unmanaged server then assigns it to
// group in one command, mirroring doAdoptSkillPackageWithGroup. Unlike
// skills, SetMcpGroups assumes the target group already exists (it never
// creates groups), matching the existing mcp group-membership picker's
// behavior — the group-assignment step here does not create new groups.
// Agents declares every agent unmanaged has this server under (not just
// agentID) so claiming from one agent's row does not orphan the same server
// installed under other agents; see mcpUnmanagedAgentsFor.
func (m *Model) doImportMcpServerWithGroup(agentID string, srv app.InstalledMcpServer, group string) tea.Cmd {
	a, ctx := m.app, m.ctx
	unmanaged := m.mcpUnmanaged
	return func() tea.Msg {
		if mcpUnmanagedConflict(unmanaged, srv.Name, srv) {
			return mcpImportAdoptDoneMsg{serverName: srv.Name, err: fmt.Errorf("mcp server %q is unmanaged under multiple agents with conflicting configuration; import each manually", srv.Name)}
		}
		s := config.McpServer{
			Name:      srv.Name,
			Transport: srv.Transport,
			Command:   srv.Command,
			URL:       srv.URL,
			Agents:    mcpUnmanagedAgentsFor(unmanaged, srv.Name, agentID),
		}
		res, err := a.AddMcpServer(ctx, s)
		if err := combineMcpErrors(err, res.Errors); err != nil {
			return mcpImportAdoptDoneMsg{serverName: srv.Name, err: err}
		}
		if group != "" {
			if err := a.SetMcpGroups(ctx, srv.Name, []string{group}); err != nil {
				return mcpImportAdoptDoneMsg{serverName: srv.Name, err: err}
			}
		}
		return mcpImportAdoptDoneMsg{serverName: srv.Name}
	}
}

// doSetMcpAgents re-targets row's Agents via SetMcpServerAgents, which diffs against
// the manifest's current targeting to add newly-selected adapters and remove
// deselected ones (unlike a blanket AddMcpServer re-upsert, which never removes).
func (m *Model) doSetMcpAgents(row app.McpServerRow, ids []string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		res, err := a.SetMcpServerAgents(ctx, row.Name, ids)
		return mcpAgentsSavedMsg{err: combineMcpErrors(err, res.Errors)}
	}
}

// doInstallMcpServer re-installs a manifest server on one targeted-but-missing
// agent via SetMcpServerAgents, which is idempotent for adapters that are
// already correctly targeted and installs on the ones that are not.
func (m *Model) doInstallMcpServer(name, agentID string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		srv, ok, err := a.McpServerByName(name)
		if err != nil {
			return mcpAgentsSavedMsg{err: err}
		}
		if !ok {
			return mcpAgentsSavedMsg{err: errors.New("mcp server " + name + " not found in manifest")}
		}
		ids := srv.Agents
		if !contains(ids, agentID) {
			ids = append(append([]string(nil), ids...), agentID)
		}
		res, err := a.SetMcpServerAgents(ctx, name, ids)
		return mcpAgentsSavedMsg{err: combineMcpErrors(err, res.Errors)}
	}
}

type mcpGroupsSavedMsg struct{ err error }

// doSetMcpGroupMemberships persists group membership for an mcp server via
// App.SetMcpGroups, mirroring doSetSkillGroupMemberships's cmd shape.
func (m *Model) doSetMcpGroupMemberships(name string, groups []string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		err := a.SetMcpGroups(ctx, name, groups)
		return mcpGroupsSavedMsg{err: err}
	}
}

func contains(vals []string, v string) bool {
	for _, s := range vals {
		if s == v {
			return true
		}
	}
	return false
}

// mcpUnmanagedEntry is one flattened row of an unmanaged server, ordered
// deterministically (by agent ID then server name) for cursor indexing.
type mcpUnmanagedEntry struct {
	agentID string
	srv     app.InstalledMcpServer
}

func mcpUnmanagedFlat(unmanaged map[string][]app.InstalledMcpServer) []mcpUnmanagedEntry {
	agentIDs := make([]string, 0, len(unmanaged))
	for id := range unmanaged {
		agentIDs = append(agentIDs, id)
	}
	sort.Strings(agentIDs)
	var out []mcpUnmanagedEntry
	for _, id := range agentIDs {
		srvs := append([]app.InstalledMcpServer(nil), unmanaged[id]...)
		sort.Slice(srvs, func(i, j int) bool { return srvs[i].Name < srvs[j].Name })
		for _, s := range srvs {
			out = append(out, mcpUnmanagedEntry{agentID: id, srv: s})
		}
	}
	return out
}

// mcpTotalRows returns the count of cursor positions across managed and
// unmanaged sections combined.
func mcpTotalRows(m Model) int {
	return len(m.mcpRows) + len(mcpUnmanagedFlat(m.mcpUnmanaged))
}

// mcpHighlightedUnmanaged returns the unmanaged server (and the agent it was
// found on) at the current cursor position, if the cursor is in that section.
func mcpHighlightedUnmanaged(m Model) (app.InstalledMcpServer, string, bool) {
	if m.mcpCursor < len(m.mcpRows) {
		return app.InstalledMcpServer{}, "", false
	}
	flat := mcpUnmanagedFlat(m.mcpUnmanaged)
	idx := m.mcpCursor - len(m.mcpRows)
	if idx < 0 || idx >= len(flat) {
		return app.InstalledMcpServer{}, "", false
	}
	e := flat[idx]
	return e.srv, e.agentID, true
}

func clampMcpCursor(m *Model) {
	n := mcpTotalRows(*m)
	if n == 0 {
		m.mcpCursor = 0
		return
	}
	if m.mcpCursor >= n {
		m.mcpCursor = n - 1
	}
	if m.mcpCursor < 0 {
		m.mcpCursor = 0
	}
}

// mcpGroupsStatusText summarizes a row's group memberships for the "g" key.
// There is no app-layer method yet to edit an mcp server's group membership
// (unlike skills' SetSkillGroupsWithState), so this is display-only.
func mcpGroupsStatusText(row app.McpServerRow) string {
	if len(row.Groups) == 0 {
		return row.Name + ": no group memberships"
	}
	return row.Name + " groups: " + strings.Join(row.Groups, ", ")
}

// resetMcpForm clears the add-server form back to its initial state.
func (m *Model) resetMcpForm() {
	m.mcpFormField = 0
	m.mcpFormTransport = 0
	m.mcpFormName.SetValue("")
	m.mcpFormCommand.SetValue("")
	m.mcpFormURL.SetValue("")
	m.mcpFormEnv.SetValue("")
	m.mcpFormEnvLit.SetValue("")
	m.mcpFormName.Blur()
	m.mcpFormCommand.Blur()
	m.mcpFormURL.Blur()
	m.mcpFormEnv.Blur()
	m.mcpFormEnvLit.Blur()
}

// focusMcpFormField blurs every add-server field then focuses the one at
// m.mcpFormField. Field 1 (transport) has no textinput of its own, so it
// leaves everything blurred while it is selected.
func (m *Model) focusMcpFormField() {
	m.mcpFormName.Blur()
	m.mcpFormCommand.Blur()
	m.mcpFormURL.Blur()
	m.mcpFormEnv.Blur()
	m.mcpFormEnvLit.Blur()
	switch m.mcpFormField {
	case 0:
		m.mcpFormName.Focus()
	case 2:
		if m.mcpFormTransport == 0 {
			m.mcpFormCommand.Focus()
		} else {
			m.mcpFormURL.Focus()
		}
	case 3:
		m.mcpFormEnv.Focus()
	case 4:
		m.mcpFormEnvLit.Focus()
	}
}

// openMcpAgentsPicker builds an agents picker from a managed row's current
// per-adapter status, reusing the skill-agents popup fields/rendering.
func (m *Model) openMcpAgentsPicker(row app.McpServerRow) tea.Cmd {
	ids := make([]string, 0, len(row.PerAgentStatus))
	for id := range row.PerAgentStatus {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	targeted := func(id string) bool {
		if len(row.Agents) == 0 {
			return true
		}
		for _, a := range row.Agents {
			if a == id {
				return true
			}
		}
		return false
	}
	rows := make([]app.SkillAgentRow, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, app.SkillAgentRow{
			ID:        id,
			Display:   id,
			Targeted:  targeted(id),
			Installed: row.PerAgentStatus[id] == app.McpStatusInstalled,
		})
	}
	m.skillAgentsSource = row.Name
	m.skillAgentsRows = rows
	m.skillAgentsCursor = 0
	m.mcpAgentsRow = row
	m.mcpAgentsPicker = true
	return nil
}

package tui

import (
	"errors"
	"fmt"
	"sort"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
)

func combineMarketplaceErrors(err error, adapterErrs []app.PluginError) error {
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

type marketplaceRowsMsg struct {
	rows      []app.MarketplaceRow
	unmanaged map[string][]app.InstalledMarketplace
	err       error
}

type marketplaceRemoveDoneMsg struct {
	name string
	err  error
}

type marketplaceImportAdoptDoneMsg struct {
	marketplaceName string
	err             error
}

type marketplaceGroupsSavedMsg struct{ err error }

func (m *Model) doLoadMarketplaceRows() tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		rows, unmanaged, err := a.MarketplaceRows(ctx)
		return marketplaceRowsMsg{rows: rows, unmanaged: unmanaged, err: err}
	}
}

func (m *Model) doRemoveMarketplace(name string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		err := a.RemoveMarketplace(name)
		return marketplaceRemoveDoneMsg{name: name, err: err}
	}
}

// Claiming one row of a marketplace unmanaged under several agents must declare all of them, or the other agents' installs vanish from every agents-tab view.
func marketplaceUnmanagedAgentsFor(unmanaged map[string][]app.InstalledMarketplace, name, clickedAgentID string) []string {
	set := map[string]struct{}{clickedAgentID: {}}
	for agentID, marketplaces := range unmanaged {
		for _, mk := range marketplaces {
			if mk.Name == name {
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

// Mirrors the mcp/plugin conflict guards so the TUI refuses the same ambiguous case instead of silently picking one agent's source.
func marketplaceUnmanagedConflict(unmanaged map[string][]app.InstalledMarketplace, name string, first app.InstalledMarketplace) bool {
	for _, marketplaces := range unmanaged {
		for _, mk := range marketplaces {
			if mk.Name != name {
				continue
			}
			if mk.Source != first.Source {
				return true
			}
		}
	}
	return false
}

// SetMarketplaceGroups assumes the target group already exists; Agents declares every agent the marketplace is unmanaged under (see marketplaceUnmanagedAgentsFor).
func (m *Model) doImportMarketplaceWithGroup(agentID string, mk app.InstalledMarketplace, group string) tea.Cmd {
	a, ctx := m.app, m.ctx
	unmanaged := m.marketplaceUnmanaged
	return func() tea.Msg {
		if marketplaceUnmanagedConflict(unmanaged, mk.Name, mk) {
			return marketplaceImportAdoptDoneMsg{marketplaceName: mk.Name, err: fmt.Errorf("marketplace %q is unmanaged under multiple agents with conflicting sources; import each manually", mk.Name)}
		}
		res, err := a.AddMarketplace(ctx, app.Marketplace{Name: mk.Name, Source: mk.Source, Agents: marketplaceUnmanagedAgentsFor(unmanaged, mk.Name, agentID)})
		if err := combineMarketplaceErrors(err, res.Errors); err != nil {
			return marketplaceImportAdoptDoneMsg{marketplaceName: mk.Name, err: err}
		}
		if group != "" {
			if err := a.SetMarketplaceGroups(ctx, mk.Name, []string{group}); err != nil {
				return marketplaceImportAdoptDoneMsg{marketplaceName: mk.Name, err: err}
			}
		}
		return marketplaceImportAdoptDoneMsg{marketplaceName: mk.Name}
	}
}

func (m *Model) doSetMarketplaceGroupMemberships(name string, groups []string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		err := a.SetMarketplaceGroups(ctx, name, groups)
		return marketplaceGroupsSavedMsg{err: err}
	}
}

type marketplaceUnmanagedEntry struct {
	agentID     string
	marketplace app.InstalledMarketplace
}

func marketplaceUnmanagedFlat(unmanaged map[string][]app.InstalledMarketplace) []marketplaceUnmanagedEntry {
	agentIDs := make([]string, 0, len(unmanaged))
	for id := range unmanaged {
		agentIDs = append(agentIDs, id)
	}
	sort.Strings(agentIDs)
	var out []marketplaceUnmanagedEntry
	for _, id := range agentIDs {
		markets := append([]app.InstalledMarketplace(nil), unmanaged[id]...)
		sort.Slice(markets, func(i, j int) bool { return markets[i].Name < markets[j].Name })
		for _, mk := range markets {
			out = append(out, marketplaceUnmanagedEntry{agentID: id, marketplace: mk})
		}
	}
	return out
}

func marketplaceTotalRows(m Model) int {
	return len(m.marketplaceRows) + len(marketplaceUnmanagedFlat(m.marketplaceUnmanaged))
}

func marketplaceHighlightedUnmanaged(m Model) (app.InstalledMarketplace, string, bool) {
	if m.marketplaceCursor < len(m.marketplaceRows) {
		return app.InstalledMarketplace{}, "", false
	}
	flat := marketplaceUnmanagedFlat(m.marketplaceUnmanaged)
	idx := m.marketplaceCursor - len(m.marketplaceRows)
	if idx < 0 || idx >= len(flat) {
		return app.InstalledMarketplace{}, "", false
	}
	e := flat[idx]
	return e.marketplace, e.agentID, true
}

func clampMarketplaceCursor(m *Model) {
	n := marketplaceTotalRows(*m)
	if n == 0 {
		m.marketplaceCursor = 0
		return
	}
	if m.marketplaceCursor >= n {
		m.marketplaceCursor = n - 1
	}
	if m.marketplaceCursor < 0 {
		m.marketplaceCursor = 0
	}
}

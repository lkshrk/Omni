package tui

import (
	"sort"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
)

type marketplaceRowsMsg struct {
	rows      []app.MarketplaceRow
	unmanaged map[string][]app.InstalledMarketplace
	err       error
}

type marketplaceRemoveDoneMsg struct {
	err error
}

type marketplaceImportAdoptDoneMsg struct {
	err error
}

type marketplaceGroupsSavedMsg struct{ err error }

func (m *Model) doLoadMarketplaceRows() tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		rows, unmanaged, err := a.MarketplaceRows(ctx)
		return marketplaceRowsMsg{rows: rows, unmanaged: unmanaged, err: err}
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

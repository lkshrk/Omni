package tui

import (
	"context"
	"database/sql"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

// doInstall installs a single tool.
func (m *Model) doInstall(name, prov string) tea.Cmd {
	a, ctx := m.app, m.beginCancellableAction()
	return func() tea.Msg {
		if err := a.Install(ctx, name, prov); err != nil {
			return opCompleteMsg{err: err}
		}
		tools, _ := a.ListTools(ctx, "") // non-fatal: install succeeded; stale list retained if refresh fails
		return opCompleteMsg{message: "installed " + name, tools: tools, removeDiscoveredKeys: []string{toolKey(name, prov)}}
	}
}

// doDelete deletes a single tool.
func (m *Model) doDelete(name, prov string) tea.Cmd {
	a, ctx := m.app, m.beginCancellableAction()
	return func() tea.Msg {
		if err := a.Uninstall(ctx, name, prov); err != nil {
			return opCompleteMsg{err: err}
		}
		tools, _ := a.ListTools(ctx, "") // non-fatal: delete succeeded; stale list retained if refresh fails
		groupNames, toolGroups, memberships, info := m.reloadToolContext()
		removeDiscovered := []string{toolKey(name, prov)}
		return opCompleteMsg{message: "deleted " + name, tools: tools, removeDiscoveredKeys: removeDiscovered, groupNames: groupNames, toolGroups: toolGroups, toolMemberships: memberships, hostInfo: info}
	}
}

// doDeleteFromConfig deletes a missing tool from settings.json without
// calling a package manager.
func (m *Model) doDeleteFromConfig(name, prov string) tea.Cmd {
	a, ctx := m.app, m.beginCancellableAction()
	return func() tea.Msg {
		if err := a.RemoveToolFromConfig(ctx, name, prov); err != nil {
			return opCompleteMsg{err: err}
		}
		tools, _ := a.ListTools(ctx, "") // non-fatal: config update succeeded
		groupNames, toolGroups, memberships, info := m.reloadToolContext()
		return opCompleteMsg{message: "deleted " + name + " from config", tools: tools, groupNames: groupNames, toolGroups: toolGroups, toolMemberships: memberships, hostInfo: info}
	}
}

// doUpgrade upgrades a single tool.
func (m *Model) doUpgrade(name, prov string) tea.Cmd {
	a, ctx := m.app, m.beginCancellableAction()
	uk := toolKey(name, prov)
	return func() tea.Msg {
		if err := a.Upgrade(ctx, name, prov); err != nil {
			return opCompleteMsg{key: uk, err: err}
		}
		tools, _ := a.ListTools(ctx, "") // non-fatal: upgrade succeeded; stale list retained if refresh fails
		return opCompleteMsg{key: uk, message: "upgraded " + name, tools: tools}
	}
}

// cancelSearch cancels any in-flight provider search HTTP request.
// Callers are responsible for incrementing searchGen to invalidate pending
// debounced messages.
func (m *Model) cancelSearch() {
	if m.searchCancel != nil {
		m.searchCancel()
		m.searchCancel = nil
	}
}

// startSearch cancels any in-flight search, increments the generation counter,
// sets the searching flag, and returns the commands needed to run doSearch.
// It is the single entry point for triggering a provider search.
func (m *Model) startSearch(query string) []tea.Cmd {
	if m.searchCancel != nil {
		m.searchCancel()
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.searchCancel = cancel
	m.searching = true
	clearStatus(m) // clear stale text so activityLabel renders "Searching…"
	return []tea.Cmd{m.spinner.Tick, m.doSearch(ctx, query, m.searchGen)}
}

// debounceSearch returns a command that sleeps for the debounce delay and then
// emits debouncedSearchMsg. When the user types faster than the delay, the
// generation counter will have advanced and the stale message is dropped.
func debounceSearch(query string, gen int) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(300 * time.Millisecond)
		return debouncedSearchMsg{query: query, gen: gen}
	}
}

// doSearch runs a provider search within ctx and returns searchResultsMsg.
// If ctx is cancelled before the search completes, nil is returned so that
// Bubbletea dispatches no message and the spinner is cleared by the next
// startSearch call instead.
func (m *Model) doSearch(ctx context.Context, query string, gen int) tea.Cmd {
	a := m.app
	// Pass the active provider pill as a filter so the network call is scoped
	// to the selected provider rather than fanning out to all providers.
	providerFilter := m.currentSearchProviderFilter()
	return func() tea.Msg {
		results, err := a.Search(ctx, query, providerFilter)
		if ctx.Err() != nil {
			return nil // cancelled — next search (or Esc) owns the searching flag
		}
		tools := make([]*database.ToolCache, 0, len(results))
		for _, r := range results {
			t := &database.ToolCache{
				Name:          r.Name,
				Provider:      r.Provider,
				InstalledWith: searchResultDisplayProvider(r),
			}
			if r.Version != "" {
				t.Version = sql.NullString{String: r.Version, Valid: true}
			}
			if r.Description != "" {
				t.Description = sql.NullString{String: r.Description, Valid: true}
			}
			if r.Privilege.RequiresPrivilege() {
				t.Privilege = string(r.Privilege.Requirement)
				t.PrivilegeReason = sql.NullString{String: r.Privilege.Reason, Valid: r.Privilege.Reason != ""}
			}
			tools = append(tools, t)
		}
		return searchResultsMsg{gen: gen, query: query, providerFilter: providerFilter, tools: tools, err: err}
	}
}

func searchResultDisplayProvider(r provider.SearchResult) string {
	if r.SourceProvider == "" || r.SourceProvider == r.Provider {
		return ""
	}
	if providerEcosystem(r.SourceProvider) != providerEcosystem(r.Provider) {
		return ""
	}
	return r.SourceProvider
}

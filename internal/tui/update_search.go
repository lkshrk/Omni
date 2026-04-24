package tui

import (
	"fmt"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

func (m *Model) handleDebouncedSearchMsg(msg debouncedSearchMsg) []tea.Cmd {
	if msg.gen != m.searchGen {
		return nil
	}
	if cmds, ok := m.applySearchCache(msg.query, m.currentSearchProviderFilter()); ok {
		return cmds
	}
	return m.startSearch(msg.query)
}

func (m *Model) handleSearchResultsMsg(msg searchResultsMsg) []tea.Cmd {
	if msg.gen != m.searchGen {
		return nil
	}
	if msg.providerFilter != m.currentSearchProviderFilter() {
		return nil
	}
	m.searching = false
	m.searchCancel = nil
	if msg.err != nil {
		return []tea.Cmd{setStatus(m, "✗ "+msg.err.Error(), true)}
	}
	m.searchCache[searchCacheKey(msg.query, msg.providerFilter)] = searchCacheEntry{tools: msg.tools, at: time.Now()}
	m.searchTools = msg.tools
	m.applyFilter()
	m.cursor = 0
	return m.searchStatus(len(msg.tools))
}

func (m *Model) currentSearchProviderFilter() string {
	if m.providerTabIdx > 0 && m.providerTabIdx <= len(m.providerNames) {
		return m.providerNames[m.providerTabIdx-1]
	}
	return ""
}

func (m *Model) applySearchCache(query, providerFilter string) ([]tea.Cmd, bool) {
	entry, ok := m.searchCache[searchCacheKey(query, providerFilter)]
	if !ok || time.Since(entry.at) >= searchCacheTTL {
		return nil, false
	}
	m.searching = false
	m.searchCancel = nil
	m.searchTools = entry.tools
	m.applyFilter()
	m.cursor = 0
	return m.searchStatus(len(entry.tools)), true
}

func (m *Model) searchStatus(count int) []tea.Cmd {
	if count == 0 {
		return []tea.Cmd{setStatus(m, "no results", false)}
	}
	return []tea.Cmd{setStatus(m, fmt.Sprintf("found %d", count), false)}
}

func (m *Model) handleSearchKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd

	if key.Matches(msg, m.keys.Back) {
		m.cancelSearch()
		m.searchGen++
		m.mode = viewList
		m.filter.SetValue("")
		m.filter.Blur()
		m.searchTools = nil
		m.searching = false
		m.providerTabIdx = 0
		m.applyFilter()
		m.cursor = 0
		return nil
	}
	if !m.filter.Focused() {
		return m.handleBlurredSearchKeyMsg(msg)
	}
	if key.Matches(msg, m.keys.Confirm) {
		query := m.filter.Value()
		m.filter.Blur()
		if len([]rune(query)) >= 2 && !m.searching {
			if cachedCmds, cached := m.applySearchCache(query, m.currentSearchProviderFilter()); cached {
				cmds = append(cmds, cachedCmds...)
			} else {
				cmds = append(cmds, m.restartSearch(query)...)
			}
		}
		return cmds
	}

	prev := m.filter.Value()
	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(msg)
	cmds = append(cmds, cmd)
	q := m.filter.Value()
	if q == prev {
		return cmds
	}
	m.searchTools = nil
	m.applyFilter()
	if len([]rune(q)) >= 2 {
		m.cancelSearch()
		m.searchGen++
		cmds = append(cmds, debounceSearch(q, m.searchGen))
	} else if m.searching {
		m.cancelSearch()
		m.searchGen++
		m.searching = false
	}
	return cmds
}

func (m *Model) handleBlurredSearchKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd

	switch {
	case key.Matches(msg, m.keys.Up):
		if m.cursor > 0 {
			m.cursor--
		}
	case key.Matches(msg, m.keys.Down):
		if m.cursor < len(m.visibleTools)-1 {
			m.cursor++
		}
	case key.Matches(msg, m.keys.Top):
		m.cursor = 0
	case key.Matches(msg, m.keys.Bottom):
		m.cursor = max(len(m.visibleTools)-1, 0)
	case key.Matches(msg, m.keys.HalfPageDown):
		half := max(listAvailableHeight(*m)/2, 1)
		m.cursor = min(m.cursor+half, max(len(m.visibleTools)-1, 0))
	case key.Matches(msg, m.keys.HalfPageUp):
		half := max(listAvailableHeight(*m)/2, 1)
		m.cursor = max(m.cursor-half, 0)
	case key.Matches(msg, m.keys.PageDown):
		page := max(listAvailableHeight(*m), 1)
		m.cursor = min(m.cursor+page, max(len(m.visibleTools)-1, 0))
	case key.Matches(msg, m.keys.PageUp):
		page := max(listAvailableHeight(*m), 1)
		m.cursor = max(m.cursor-page, 0)
	case key.Matches(msg, m.keys.PrevTab):
		cmds = append(cmds, m.moveSearchProviderTab(-1)...)
	case key.Matches(msg, m.keys.NextTab):
		cmds = append(cmds, m.moveSearchProviderTab(1)...)
	default:
		m.filter.Focus()
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		cmds = append(cmds, cmd, textinput.Blink)
	}
	return cmds
}

func (m *Model) moveSearchProviderTab(direction int) []tea.Cmd {
	if len(m.providerNames) == 0 {
		return nil
	}
	if direction < 0 {
		if m.providerTabIdx > 0 {
			m.providerTabIdx--
		} else {
			m.providerTabIdx = len(m.providerNames)
		}
	} else {
		if m.providerTabIdx < len(m.providerNames) {
			m.providerTabIdx++
		} else {
			m.providerTabIdx = 0
		}
	}
	query := m.filter.Value()
	if len([]rune(query)) >= 2 {
		m.cancelSearch()
		m.searchGen++
		if cmds, ok := m.applySearchCache(query, m.currentSearchProviderFilter()); ok {
			return cmds
		}
		m.searchTools = nil
		m.applyFilter()
		m.cursor = 0
		return m.startSearch(query)
	}
	m.cancelSearch()
	m.searchGen++
	m.searching = false
	m.searchTools = nil
	m.applyFilter()
	m.cursor = 0
	return nil
}

func (m *Model) restartSearch(query string) []tea.Cmd {
	m.cancelSearch()
	m.searchGen++
	return m.startSearch(query)
}

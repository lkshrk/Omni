package tui

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

func (m *Model) handleTraceLogLoadedMsg(msg traceLogLoadedMsg) []tea.Cmd {
	// Discard responses that don't belong to the current open: either a stale
	// generation (the popup was closed or re-opened) or loading was already
	// cleared (e.g. by a concurrent Back keypress that raced the goroutine).
	if msg.gen != m.traceLogGen || !m.traceLogLoading {
		return nil
	}
	m.traceLogLoading = false
	if msg.err != nil {
		// Surface the failure both in the popup body and the status bar so it
		// isn't mistaken for an empty log.
		m.traceLog = &traceLogState{err: msg.err}
		return []tea.Cmd{setStatus(m, "command log: "+msg.err.Error(), true)}
	}
	m.traceLog = &traceLogState{traces: msg.traces}
	return nil
}

func (m *Model) handleTraceLogKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	if key.Matches(msg, m.keys.Back) {
		m.traceLogGen++
		m.traceLog = nil
		m.traceLogLoading = false
		return nil
	}
	if m.traceLog == nil {
		return nil
	}
	switch {
	case key.Matches(msg, m.keys.Up):
		m.scrollTraceLogBy(-1)
	case key.Matches(msg, m.keys.Down):
		m.scrollTraceLogBy(1)
	case key.Matches(msg, m.keys.HalfPageUp):
		m.scrollTraceLogBy(-max(traceLogBodyHeight(*m), 1) / 2)
	case key.Matches(msg, m.keys.HalfPageDown):
		m.scrollTraceLogBy(max(traceLogBodyHeight(*m), 1) / 2)
	case key.Matches(msg, m.keys.PageUp):
		m.scrollTraceLogBy(-max(traceLogBodyHeight(*m)-2, 1))
	case key.Matches(msg, m.keys.PageDown):
		m.scrollTraceLogBy(max(traceLogBodyHeight(*m)-2, 1))
	case key.Matches(msg, m.keys.Top):
		m.traceLog.scroll = 0
	case key.Matches(msg, m.keys.Bottom):
		m.traceLog.scroll = traceLogMaxScroll(*m)
	}
	return nil
}

func (m *Model) scrollTraceLogBy(delta int) {
	if m.traceLog == nil || delta == 0 {
		return
	}
	m.traceLog.scroll = clampRange(m.traceLog.scroll+delta, 0, traceLogMaxScroll(*m))
}

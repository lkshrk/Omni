package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

func (m *Model) handleCommandKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd

	switch {
	case key.Matches(msg, m.keys.Back):
		m.closeCommandPalette()
	case paletteNavKey(msg, m.keys.Up):
		if m.commandCursor > 0 {
			m.commandCursor--
		}
	case paletteNavKey(msg, m.keys.Down):
		if m.commandCursor < len(m.commandSuggestions)-1 {
			m.commandCursor++
		}
	case key.Matches(msg, m.keys.Confirm):
		cmds = append(cmds, m.runSelectedCommand()...)
	default:
		var cmd tea.Cmd
		m.commandInput, cmd = m.commandInput.Update(msg)
		m.commandSuggestions = filterPalette(buildPalette(*m), m.commandInput.Value())
		m.commandCursor = -1
		cmds = append(cmds, cmd)
	}

	return cmds
}

func (m *Model) runSelectedCommand() []tea.Cmd {
	input := strings.TrimSpace(m.commandInput.Value())
	chosen := m.selectedPaletteCommand(input)
	m.closeCommandPalette()
	if chosen != nil {
		return []tea.Cmd{chosen.run(m)}
	}
	if input != "" {
		return []tea.Cmd{setStatus(m, "✗ unknown command", true)}
	}
	return nil
}

func (m *Model) selectedPaletteCommand(input string) *palCmd {
	if m.commandCursor >= 0 && m.commandCursor < len(m.commandSuggestions) {
		return &m.commandSuggestions[m.commandCursor]
	}
	if len(m.commandSuggestions) == 1 {
		return &m.commandSuggestions[0]
	}
	for i := range m.commandSuggestions {
		if strings.EqualFold(m.commandSuggestions[i].name, input) {
			return &m.commandSuggestions[i]
		}
	}
	return nil
}

// A key that carries text belongs to the query, otherwise the Up/Down bindings would eat "k" and "j" mid-word.
func paletteNavKey(msg tea.KeyPressMsg, b key.Binding) bool {
	return msg.Text == "" && key.Matches(msg, b)
}

func (m *Model) closeCommandPalette() {
	m.mode = m.commandOrigin
	if m.mode == viewCommand {
		m.mode = viewList
	}
	m.commandInput.SetValue("")
	m.commandInput.Blur()
	m.commandSuggestions = nil
	m.commandCursor = -1
}

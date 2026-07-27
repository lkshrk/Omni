package tui

import (
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// Keyboard lock states, not chords: they never take part in deciding whether a key press carries a shifted character.
const lockMods = tea.ModCapsLock | tea.ModNumLock | tea.ModScrollLock

// Bubble Tea v2 negotiates both modifyOtherKeys=2 and the Kitty protocol, which disagree on shifted keys: a terminal encoding Shift+S as CSI 27;2;115~ decodes to text "s", so every uppercase-letter binding stops matching. Only letters are folded — a punctuation key's shifted form is layout-dependent and the escape sequence does not carry it.
func normalizeKeyPress(msg tea.KeyPressMsg) tea.KeyPressMsg {
	if msg.Mod&^lockMods != tea.ModShift {
		return msg
	}
	shifted := msg.ShiftedCode
	if shifted == 0 {
		shifted = unicode.ToUpper(msg.Code)
	}
	if shifted == msg.Code || !unicode.IsLetter(shifted) {
		return msg
	}
	msg.Text = string(shifted)
	return msg
}

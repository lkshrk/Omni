package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Builds the same logical key press the way different terminals report it once Bubble Tea has negotiated the enhanced keyboard protocols.
type keyEncoding struct {
	name string
	// press takes the unshifted code and the shifted character the user meant.
	press func(base, shifted rune) tea.KeyPressMsg
}

func shiftedKeyEncodings() []keyEncoding {
	return []keyEncoding{
		{
			// Plain pty: the terminal sends the shifted byte and nothing else.
			name: "legacy",
			press: func(_, shifted rune) tea.KeyPressMsg {
				return tea.KeyPressMsg{Code: shifted, Text: string(shifted)}
			},
		},
		{
			// CSI 27;2;115~ — the code is unshifted, the shift lives in the modifier.
			name: "modifyOtherKeys",
			press: func(base, _ rune) tea.KeyPressMsg {
				return tea.KeyPressMsg{Code: base, Mod: tea.ModShift, Text: string(base)}
			},
		},
		{
			// CSI 115;2u — Kitty already folds shift into the reported text.
			name: "kitty",
			press: func(base, shifted rune) tea.KeyPressMsg {
				return tea.KeyPressMsg{Code: base, Mod: tea.ModShift, Text: string(shifted)}
			},
		},
		{
			// CSI 115:83;2u — Kitty with alternate-key reporting.
			name: "kitty alternate keys",
			press: func(base, shifted rune) tea.KeyPressMsg {
				return tea.KeyPressMsg{Code: base, ShiftedCode: shifted, Mod: tea.ModShift, Text: string(shifted)}
			},
		},
	}
}

func TestAgentsGlobalActionsDispatchUnderEveryKeyEncoding(t *testing.T) {
	t.Parallel()
	keys := []struct{ base, shifted rune }{{'u', 'U'}, {'s', 'S'}, {'r', 'R'}}
	for _, enc := range shiftedKeyEncodings() {
		for _, k := range keys {
			t.Run(enc.name+" "+string(k.shifted), func(t *testing.T) {
				t.Parallel()
				m := baseModel(nil)
				m.mode = viewSkills
				// A bulk op in flight makes U/S/R all answer with the busy notice, so the status message proves the key was matched and dispatched without a live app.
				m.skillsRunning = true

				got := drive(m, enc.press(k.base, k.shifted))
				if !strings.Contains(got.statusMsg, "agents busy") {
					t.Fatalf("%q statusMsg = %q, want the busy notice", k.shifted, got.statusMsg)
				}
			})
		}
	}
}

func TestAgentsSyncAllConfirmArmsUnderEveryKeyEncoding(t *testing.T) {
	t.Parallel()
	for _, enc := range shiftedKeyEncodings() {
		t.Run(enc.name, func(t *testing.T) {
			t.Parallel()
			m := baseModel(nil)
			m.mode = viewSkills

			got := drive(m, enc.press('s', 'S'))
			if !got.agentsSyncAllConfirm {
				t.Fatal("S should arm the sync-all confirmation")
			}
			got = drive(got, enc.press('s', 'S'))
			if got.agentsSyncAllConfirm {
				t.Fatal("a second S should consume the confirmation and run the sync")
			}
		})
	}
}

func TestQuitConfirmArmsUnderEveryKeyEncoding(t *testing.T) {
	t.Parallel()
	// q carries no shift, so every protocol reports the same press; the guard is that normalization does not disturb it.
	for _, press := range []tea.KeyPressMsg{
		{Code: 'q', Text: "q"},
		{Code: 'q', Mod: tea.ModNumLock, Text: "q"},
	} {
		m := baseModel(nil)
		m.mode = viewSkills
		got := drive(m, press)
		if !got.confirmQuit {
			t.Fatalf("q (%+v) should arm the quit confirmation", press)
		}
	}
}

func TestNormalizeKeyPressLeavesNonLetterKeysAlone(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   tea.KeyPressMsg
		want string
	}{
		{"shift+tab", tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, "shift+tab"},
		{"shift+enter", tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}, "shift+enter"},
		{"ctrl+c", tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, "ctrl+c"},
		{"ctrl+shift+u", tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl | tea.ModShift}, "ctrl+shift+u"},
		// The shifted form of a punctuation key is layout-dependent and absent from the escape sequence, so it must be left as reported.
		{"shift+semicolon", tea.KeyPressMsg{Code: ';', Mod: tea.ModShift, Text: ";"}, ";"},
		{"plain j", tea.KeyPressMsg{Code: 'j', Text: "j"}, "j"},
		{"colon", tea.KeyPressMsg{Code: ':', Text: ":"}, ":"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := normalizeKeyPress(tc.in).String(); got != tc.want {
				t.Fatalf("normalizeKeyPress(%s).String() = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

package tui

import (
	"strings"
	"testing"
)

func paletteOriginModes() []struct {
	name string
	mode viewMode
} {
	return []struct {
		name string
		mode viewMode
	}{
		{"dashboard", viewStatus},
		{"tools", viewList},
		{"dots", viewDots},
		{"settings", viewSettings},
		{"groups", viewGroups},
		{"agents", viewSkills},
	}
}

// Opening the palette used to move the model to viewCommand outright, which rendered as the Tools tab and dropped the user there on Esc.
func TestPaletteKeepsTheOriginatingTabActive(t *testing.T) {
	t.Parallel()
	for _, tc := range paletteOriginModes() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := baseModel(nil)
			m.mode = tc.mode
			wantTabs := renderTabs(m)

			open := drive(m, pressRune(':'))
			if open.mode != viewCommand {
				t.Fatalf("mode = %v, want viewCommand after ':'", open.mode)
			}
			if open.commandOrigin != tc.mode {
				t.Fatalf("commandOrigin = %v, want %v", open.commandOrigin, tc.mode)
			}
			if got := renderTabs(open); got != wantTabs {
				t.Fatalf("opening the palette changed the active tab:\ngot  %q\nwant %q",
					stripANSIEscapeSequences(got), stripANSIEscapeSequences(wantTabs))
			}

			closed := drive(open, pressEsc())
			if closed.mode != tc.mode {
				t.Fatalf("mode = %v after Esc, want the originating %v", closed.mode, tc.mode)
			}
		})
	}
}

func TestPaletteUnknownCommandReturnsToTheOriginatingTab(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills

	got := drive(m, pressRune(':'))
	for _, r := range "not-a-command" {
		got = drive(got, pressRune(r))
	}
	got = drive(got, pressEnter())

	if got.mode != viewSkills {
		t.Fatalf("mode = %v, want viewSkills after an unknown command", got.mode)
	}
	if !strings.Contains(got.statusMsg, "unknown command") {
		t.Fatalf("statusMsg = %q, want the unknown-command notice", got.statusMsg)
	}
}

// The Up/Down bindings include "k"/"j", so the palette query used to lose those letters: typing "skills" produced "sills".
func TestPaletteTypingKeepsNavigationLetters(t *testing.T) {
	t.Parallel()
	for _, query := range []string{"skills", "jobs", "kk"} {
		t.Run(query, func(t *testing.T) {
			t.Parallel()
			got := drive(baseModel(nil), pressRune(':'))
			for _, r := range query {
				got = drive(got, pressRune(r))
			}
			if got.commandInput.Value() != query {
				t.Fatalf("palette input = %q, want %q", got.commandInput.Value(), query)
			}
			if got.commandCursor != -1 {
				t.Fatalf("commandCursor = %d, typing must not move the suggestion cursor", got.commandCursor)
			}
		})
	}
}

func TestPaletteArrowsStillNavigateWhileTyping(t *testing.T) {
	t.Parallel()
	got := drive(baseModel(nil), pressRune(':'))
	if len(got.commandSuggestions) < 2 {
		t.Skip("need at least 2 suggestions")
	}
	got = drive(got, pressDown())
	if got.commandCursor != 0 {
		t.Fatalf("commandCursor = %d, want 0 after down", got.commandCursor)
	}
	got = drive(got, pressDown())
	if got.commandCursor != 1 {
		t.Fatalf("commandCursor = %d, want 1 after a second down", got.commandCursor)
	}
	if got.commandInput.Value() != "" {
		t.Fatalf("palette input = %q, arrows must not type", got.commandInput.Value())
	}
}

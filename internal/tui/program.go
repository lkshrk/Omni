package tui

import (
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
)

// ProgramOptions — Uses the terminal environment profile directly instead of stdout probing, keeping colors available in PTY wrappers and recorders; bubbletea v2 writes modifyOtherKeys=2 and the Kitty protocol regardless of options, so key matching has to tolerate those encodings instead — see normalizeKeyPress.
func ProgramOptions() []tea.ProgramOption {
	return []tea.ProgramOption{
		tea.WithColorProfile(colorprofile.Env(os.Environ())),
	}
}

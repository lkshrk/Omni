package tui

import (
	"testing"

	"github.com/lkshrk/omni/internal/app"
)

// The guard must not wedge the common case: an operation's own completion always lowers its own gate.
func TestOpComplete_ReleasesItsOwnGate(t *testing.T) {
	m := refreshTestModel(t, &app.ToolView{Name: "ripgrep", Provider: "brew", Installed: true, Outdated: true, Tracked: true})

	upgrading := drive(m, pressRune('u'))
	got := drive(upgrading, opCompleteMsg{key: toolKey("ripgrep", "brew"), loadingGen: upgrading.loadingGen, message: "upgraded ripgrep"})

	if got.loading {
		t.Fatal("an operation's own completion must lower its own gate")
	}
}

// Unstamped producers stay permissive rather than wedging behind an uncleared gate.
func TestOpComplete_UnstampedCompletionStillReleases(t *testing.T) {
	m := refreshTestModel(t, &app.ToolView{Name: "ripgrep", Provider: "brew", Installed: true, Outdated: true, Tracked: true})

	upgrading := drive(m, pressRune('u'))
	got := drive(upgrading, opCompleteMsg{key: toolKey("ripgrep", "brew"), message: "upgraded ripgrep"})

	if got.loading {
		t.Fatal("an unstamped completion must still lower the gate")
	}
}

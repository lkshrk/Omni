package tui

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/app"
)

// A cancelled upgrade's completion message cannot be recalled. The user has already started something
// else by the time it lands, and an unconditional release hands that operation's gate away: input goes
// live while the delete is still running.
func TestOpComplete_StaleCancelledUpgradeDoesNotReleaseTheNextGate(t *testing.T) {
	m := refreshTestModel(t, &app.ToolView{Name: "ripgrep", Provider: "brew", Installed: true, Outdated: true, Tracked: true})

	upgrading := drive(m, pressRune('u'))
	if !upgrading.loading || upgrading.loadingOwner != loadingOwnerProgressOp {
		t.Fatalf("u should raise the gate as a progress op: loading=%v owner=%v", upgrading.loading, upgrading.loadingOwner)
	}
	if upgrading.activeActionCancel == nil {
		t.Fatal("the upgrade should be cancellable")
	}
	upgradeGen := upgrading.loadingGen

	cancelled := drive(upgrading, pressCtrlC())
	if cancelled.loading {
		t.Fatal("ctrl+c should lower the gate")
	}

	deleting := drive(cancelled, pressRune('d'), pressRune('d'))
	if !deleting.loading || deleting.loadingOwner != loadingOwnerLocalOp {
		t.Fatalf("the confirmed delete should own the gate: loading=%v owner=%v", deleting.loading, deleting.loadingOwner)
	}
	if deleting.loadingGen == upgradeGen {
		t.Fatal("the delete should have raised a new gate generation")
	}

	got := drive(deleting, opCompleteMsg{key: toolKey("ripgrep", "brew"), loadingGen: upgradeGen, err: context.Canceled})

	if !got.loading {
		t.Fatal("the cancelled upgrade's completion released the delete's gate; input is live mid-delete")
	}
}

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

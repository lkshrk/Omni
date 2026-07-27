package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// The confirm window has to survive reading the hint that describes it; 5s was measured as too short, so the floor is pinned here.
func TestConfirmTimeout_StaysArmedLongEnoughToRead(t *testing.T) {
	t.Parallel()
	if confirmTimeout < 8*time.Second {
		t.Fatalf("confirmTimeout = %v, want at least 8s so a two-step confirm outlasts reading its hint", confirmTimeout)
	}

	m := baseModel(nil)
	cmd := m.armConfirmationTimeout()
	if cmd == nil {
		t.Fatal("armConfirmationTimeout returned no command")
	}

	fired := make(chan tea.Msg, 1)
	go func() { fired <- cmd() }()
	select {
	case msg := <-fired:
		t.Fatalf("confirmation expired immediately with %#v, want it deferred by %v", msg, confirmTimeout)
	case <-time.After(300 * time.Millisecond):
	}
}

// Every arm bumps confirmGen so a stale timer cannot disarm the confirmation a later key press replaced it with.
func TestConfirmTimeout_StaleTimerLeavesLaterConfirmationArmed(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true

	m.agentsSyncAllConfirm = true
	stale := m.confirmGen
	m.armConfirmationTimeout()

	m.confirmQuit = true
	m.quitConfirmKey = "q"
	m.armConfirmationTimeout()

	m.handleConfirmTimeoutMsg(confirmTimeoutMsg{gen: stale})
	if !m.confirmQuit || !m.agentsSyncAllConfirm {
		t.Fatalf("a stale timer disarmed a live confirmation: quit=%v syncAll=%v", m.confirmQuit, m.agentsSyncAllConfirm)
	}
}

package tui

import (
	"errors"
	"strings"
	"testing"
)

// A failed section load leaves its rows unknown forever; without treating that as settled the dashboard sat on "Loading agents…" for the rest of the session.
func TestDashboardAgentsLoadingClearsWhenSectionLoadErrors(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewStatus
	if !statusAgentsLoading(m) {
		t.Fatal("agents should read as loading before any section has settled")
	}

	boom := errors.New("cannot unmarshal array into Go value")
	got := drive(m,
		skillsManifestLoadedMsg{err: boom},
		mcpRowsMsg{err: boom},
		pluginRowsMsg{err: boom},
		marketplaceRowsMsg{err: boom},
	)

	if statusAgentsLoading(got) {
		t.Fatal("errored section loads should stop the agents loading indicator")
	}
	if plain := stripANSIEscapeSequences(renderStatus(got)); strings.Contains(plain, "Loading agents…") {
		t.Fatalf("dashboard still shows the agents spinner after failed loads:\n%s", plain)
	}
}

func TestDashboardAgentsLoadingStaysWhileASectionIsStillInFlight(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewStatus
	boom := errors.New("boom")
	got := drive(m,
		skillsManifestLoadedMsg{err: boom},
		mcpRowsMsg{err: boom},
		pluginRowsMsg{err: boom},
	)
	if !statusAgentsLoading(got) {
		t.Fatal("marketplaces have not reported yet, agents should still read as loading")
	}
}

func TestAgentsBulkActionWhileBusyReportsInsteadOfSwallowing(t *testing.T) {
	t.Parallel()
	for _, key := range []rune{'U', 'S', 'R'} {
		m := baseModel(nil)
		m.mode = viewSkills
		m.skillsRunning = true

		got := drive(m, pressRune(key))
		if got.agentsSyncAllConfirm {
			t.Fatalf("%q should not arm sync-all while a bulk op is running", key)
		}
		if !strings.Contains(got.statusMsg, "agents busy") {
			t.Fatalf("%q while busy set statusMsg = %q, want a busy notice", key, got.statusMsg)
		}
	}
}

// The inline row hint only renders on a selected row, and at launch the cursor is hidden, so the armed state has to reach the footer to be visible at all.
func TestAgentsSyncAllConfirmHintIsVisibleWithoutASelectedRow(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.width = 120
	m.height = 30
	m.cursorHidden = true

	got := drive(m, pressRune('S'))
	if !got.agentsSyncAllConfirm {
		t.Fatal("S should arm the sync-all confirmation")
	}
	view := stripANSIEscapeSequences(got.View().Content)
	if !strings.Contains(view, "press S again to sync all") {
		t.Fatalf("armed sync-all hint missing from the composed view:\n%s", view)
	}
}

func TestAgentsSyncAllConfirmTimeoutReportsExpiry(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills

	got := drive(m, pressRune('S'))
	if !got.agentsSyncAllConfirm {
		t.Fatal("S should arm the sync-all confirmation")
	}

	got = drive(got, confirmTimeoutMsg{gen: got.confirmGen})
	if got.agentsSyncAllConfirm {
		t.Fatal("sync-all confirmation should disarm on timeout")
	}
	if !strings.Contains(got.statusMsg, "expired") {
		t.Fatalf("statusMsg = %q, want expiry feedback after a silent disarm", got.statusMsg)
	}
}

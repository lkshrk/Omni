package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func startupHostsLoaded() toolsLoadedMsg {
	return toolsLoadedMsg{
		hostInfo: &app.HostInfo{
			Active: "testhost",
			Hosts: map[string]config.HostAssignment{
				"testhost": {Groups: []string{"testhost"}},
				"laptop":   {Groups: []string{"work"}},
			},
		},
	}
}

func TestStartupKeysReplayOnGroupsTabAfterSnapshot(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewStatus
	m.loading = true
	m.startupSnapshotPending = true
	m.cursorHidden = true

	got := drive(m,
		pressTab(), pressTab(), pressTab(), pressTab(),
		agentsReadinessMsg{readiness: app.AgentsReadiness{State: app.AgentsReadinessEmpty, CTA: app.AgentsCTAMigrate}},
		pressRune('j'), pressRune('j'), pressRune('d'),
		startupHostsLoaded(),
	)

	if got.mode != viewGroups {
		t.Fatalf("mode = %v, want viewGroups", got.mode)
	}
	if got.cursorHidden {
		t.Error("navigation typed during startup should reveal the host cursor")
	}
	if got.hostCursor != 1 {
		t.Errorf("hostCursor = %d, want 1", got.hostCursor)
	}
	if !got.hostDeleteConfirm || got.hostDeleteName != "laptop" {
		t.Errorf("hostDeleteConfirm = %v, hostDeleteName = %q, want true and laptop", got.hostDeleteConfirm, got.hostDeleteName)
	}
	if len(got.startupKeyQueue) != 0 {
		t.Errorf("startupKeyQueue = %v, want drained", got.startupKeyQueue)
	}
}

func TestStartupKeysDroppedWhenSnapshotFails(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewStatus
	m.loading = true
	m.startupSnapshotPending = true
	m.cursorHidden = true

	failed := startupHostsLoaded()
	failed.noHost = true

	got := drive(m,
		pressTab(), pressTab(), pressTab(), pressTab(),
		pressRune('j'), pressRune('d'),
		failed,
	)

	if got.hostDeleteConfirm {
		t.Error("keys queued during startup must not replay into an onboarding model")
	}
	if len(got.startupKeyQueue) != 0 {
		t.Errorf("startupKeyQueue = %v, want drained", got.startupKeyQueue)
	}
}

func TestStartupKeyQueueIsBounded(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewGroups
	m.loading = true
	m.startupSnapshotPending = true

	msgs := make([]tea.Msg, 0, startupKeyQueueLimit*2)
	for range startupKeyQueueLimit * 2 {
		msgs = append(msgs, pressRune('j'))
	}
	got := drive(m, msgs...)

	if len(got.startupKeyQueue) != startupKeyQueueLimit {
		t.Errorf("startupKeyQueue length = %d, want %d", len(got.startupKeyQueue), startupKeyQueueLimit)
	}
}

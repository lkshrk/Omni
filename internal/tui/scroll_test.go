package tui

import (
	"testing"

	"github.com/lkshrk/omni/internal/app"
)

func TestScrollDotsPeekBy(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.scrollDotsPeekBy(1) // nil peek: no-op, no panic

	m.dotsPeek = &dotsPeekState{scroll: 5}
	m.scrollDotsPeekBy(0)
	if m.dotsPeek.scroll != 5 {
		t.Errorf("scroll after delta 0 = %d, want unchanged 5", m.dotsPeek.scroll)
	}
	m.scrollDotsPeekBy(-2)
	if m.dotsPeek.scroll != 0 {
		t.Errorf("scroll after clamping = %d, want 0 (empty peek content has no scroll range)", m.dotsPeek.scroll)
	}
}

func TestScrollSetupBy(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.setupStep = 1
	m.setupProviders = []app.SetupProviderOption{{}, {}, {}}
	m.scrollSetupBy(1)
	if m.setupProviderIdx != 1 {
		t.Errorf("setupProviderIdx = %d, want 1", m.setupProviderIdx)
	}
	m.scrollSetupBy(10)
	if m.setupProviderIdx != 2 {
		t.Errorf("setupProviderIdx = %d, want clamped to 2", m.setupProviderIdx)
	}

	m.setupStep = 8
	m.scrollSetupBy(1) // no copy hosts: clamp handles the empty list

	m.setupStep = 9
	m.groupNames = []string{"work", "home"}
	m.scrollSetupBy(1)
	if m.setupGroupIdx != 1 {
		t.Errorf("setupGroupIdx = %d, want 1", m.setupGroupIdx)
	}

	m.setupStep = 10
	m.scrollSetupBy(1)
	if m.setupActivationIdx != 1 {
		t.Errorf("setupActivationIdx = %d, want 1", m.setupActivationIdx)
	}
}

func TestScrollSettingsBy(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.editingPriority = true
	m.priorityDraft = []string{"brew", "node", "python"}
	m.scrollSettingsBy(2)
	if m.priorityCursor != 2 {
		t.Errorf("priorityCursor = %d, want 2", m.priorityCursor)
	}
	m.scrollSettingsBy(5)
	if m.priorityCursor != 2 {
		t.Errorf("priorityCursor = %d, want clamped to 2", m.priorityCursor)
	}
	m.editingPriority = false

	m.editingServiceDuration = true
	m.scrollSettingsBy(1)
	if m.serviceDurationIdx < 0 {
		t.Errorf("serviceDurationIdx = %d, want clamped non-negative", m.serviceDurationIdx)
	}
	m.editingServiceDuration = false

	before := m.settingsCursor
	m.scrollSettingsBy(1)
	if m.settingsCursor == before {
		t.Errorf("settingsCursor = %d, want it to move from %d", m.settingsCursor, before)
	}
}

func TestScrollGroupsBy(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.hostEditMode = 1
	m.hostGroupPicker = []string{"work", "home", "lab"}
	m.scrollGroupsBy(1)
	if m.hostGroupIdx != 1 {
		t.Errorf("hostGroupIdx = %d, want 1", m.hostGroupIdx)
	}
	m.hostEditMode = 0

	m.groupDeleteConfirm = true
	m.scrollGroupsBy(1)
	if m.groupDeleteChoice != 1 {
		t.Errorf("groupDeleteChoice = %d, want 1", m.groupDeleteChoice)
	}
	m.scrollGroupsBy(5)
	if m.groupDeleteChoice != 1 {
		t.Errorf("groupDeleteChoice = %d, want clamped to 1 (two choices)", m.groupDeleteChoice)
	}
	m.groupDeleteConfirm = false

	m.scrollGroupsBy(1)  // default branch down: must not panic without groups
	m.scrollGroupsBy(-1) // default branch up
}

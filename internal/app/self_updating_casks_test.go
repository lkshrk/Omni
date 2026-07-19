package app

import (
	"testing"

	"github.com/lkshrk/omni/internal/database"
)

func TestAnnotateSelfUpdatingCasks(t *testing.T) {
	t.Parallel()
	var a *App // the annotation reads only the tool slice; no app state needed

	selfUpdating := &database.ToolCache{Name: "battle-net", Provider: "brew", Installed: true, Outdated: true, Options: map[string]string{"brew_kind": "cask", "self_updates": "true"}}
	normalCask := &database.ToolCache{Name: "stats", Provider: "brew", Installed: true, Outdated: true, Options: map[string]string{"brew_kind": "cask"}}
	alreadyBlocked := &database.ToolCache{Name: "x", Provider: "brew", Installed: true, Outdated: true, UpdateBlocked: "quarantined", Options: map[string]string{"self_updates": "true"}}

	a.annotateSelfUpdatingCasks([]*database.ToolCache{selfUpdating, normalCask, alreadyBlocked})

	if selfUpdating.UpdateBlocked != UpdateBlockSelfUpdates {
		t.Errorf("battle-net UpdateBlocked = %q, want %q", selfUpdating.UpdateBlocked, UpdateBlockSelfUpdates)
	}
	if normalCask.UpdateBlocked != "" {
		t.Errorf("stats must not be blocked, got %q", normalCask.UpdateBlocked)
	}
	if alreadyBlocked.UpdateBlocked != "quarantined" {
		t.Errorf("existing block must be preserved, got %q", alreadyBlocked.UpdateBlocked)
	}
}

package app

import "github.com/lkshrk/omni/internal/database"

// UpdateBlockSelfUpdates — brew refuses to upgrade a cask that uses a manual installer and updates itself.
const UpdateBlockSelfUpdates = "self-updates"

const selfUpdatesOption = "self_updates"

// Reads a persisted flag, so the classification is stable across launches.
func (a *App) annotateSelfUpdatingCasks(tools []*database.ToolCache) {
	for _, t := range tools {
		if t == nil || !t.Installed || !t.Outdated || t.UpdateBlocked != "" {
			continue
		}
		if t.Options[selfUpdatesOption] == "true" {
			t.UpdateBlocked = UpdateBlockSelfUpdates
		}
	}
}

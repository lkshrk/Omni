package app

import "github.com/lkshrk/omni/internal/database"

// UpdateBlockSelfUpdates marks a cask that brew refuses to upgrade because it
// uses a manual installer and updates itself.
const UpdateBlockSelfUpdates = "self-updates"

// selfUpdatesOption is the persisted cache flag (set from cask metadata during
// the metadata refresh) marking a cask whose installer brew cannot upgrade.
const selfUpdatesOption = "self_updates"

// annotateSelfUpdatingCasks marks outdated casks flagged self-updating in the
// cached metadata so omni skips them instead of attempting a doomed upgrade and
// surfaces them distinctly. This reads a persisted flag — no live lookups — so
// the classification is stable across launches.
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

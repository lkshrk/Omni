package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
)

func dotsDiscoveredLocalOnlyModel() Model {
	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	setDotsRepoForTest(&m, "/repo/dotfiles")
	m.dotsEntries = []app.DotStatus{{
		Name:       "kitty",
		TargetPath: "~/.config/kitty",
		State:      app.DotStateLocalOnly,
		Actions:    []app.DotAction{app.DotActionSync, app.DotActionRemove, app.DotActionIgnore},
	}}
	return m
}

func dotsChildRowModel(parent app.DotStatus, child app.DotChild) Model {
	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	setDotsRepoForTest(&m, "/repo/dotfiles")
	parent.Children = []app.DotChild{child}
	m.dotsEntries = []app.DotStatus{parent}
	m.dotsExpandedName = parent.Name
	m.dotsExpandedState = app.DotStatusState(parent)
	m.dotsCursor = 1
	return m
}

func TestFlow_DotsDiscoveredLocalOnlyDelete(t *testing.T) {
	t.Run("d arms confirm on transient candidate", func(t *testing.T) {
		got := drive(dotsDiscoveredLocalOnlyModel(), pressRune('d'))
		if got.dotsConfirmIdx != 0 {
			t.Fatalf("dotsConfirmIdx = %d, want 0", got.dotsConfirmIdx)
		}
	})

	t.Run("y starts local delete operation", func(t *testing.T) {
		got := drive(dotsDiscoveredLocalOnlyModel(), pressRune('d'), pressRune('y'))
		if !got.dotsLoading {
			t.Fatal("dotsLoading should be true after y confirms local delete")
		}
		if got.statusMsg != "Deleting kitty…" {
			t.Errorf("statusMsg = %q, want %q", got.statusMsg, "Deleting kitty…")
		}
		if got.dotsConfirmIdx != -1 {
			t.Errorf("dotsConfirmIdx = %d, want -1 after confirm", got.dotsConfirmIdx)
		}
	})

	t.Run("n clears confirm without starting an operation", func(t *testing.T) {
		got := drive(dotsDiscoveredLocalOnlyModel(), pressRune('d'), pressRune('n'))
		if got.dotsConfirmIdx != -1 {
			t.Errorf("dotsConfirmIdx = %d, want -1 after n", got.dotsConfirmIdx)
		}
		if got.dotsLoading {
			t.Error("dotsLoading should stay false after n on transient candidate")
		}
	})
}

func TestRender_DotsDeleteKeepLocalPromptVariants(t *testing.T) {
	t.Run("transient candidate asks about disk delete", func(t *testing.T) {
		m := dotsDiscoveredLocalOnlyModel()
		prompt := renderDotsDeleteKeepLocalPrompt(m, m.dotsEntries[0], "")
		if !strings.Contains(prompt, "from disk?") {
			t.Fatalf("prompt = %q, want it to contain %q", prompt, "from disk?")
		}
		if strings.Contains(prompt, ", keep local?") {
			t.Fatalf("prompt = %q, must not contain keep-local question", prompt)
		}
	})

	t.Run("tracked entry keeps keep-local question", func(t *testing.T) {
		m := dotsModel()
		prompt := renderDotsDeleteKeepLocalPrompt(m, m.dotsEntries[0], "")
		if !strings.Contains(prompt, ", keep local?") {
			t.Fatalf("prompt = %q, want it to contain %q", prompt, ", keep local?")
		}
	})
}

func TestDotsRowHints_DiscoveredLocalOnlyIncludesDelete(t *testing.T) {
	m := dotsDiscoveredLocalOnlyModel()
	hints := dotsRowHintItems(m)
	deleteKey := m.keys.DotDelete.Help().Key
	if !slices.ContainsFunc(hints, func(h hintItem) bool { return h.key == deleteKey }) {
		t.Fatalf("row hints for discovered local-only entry lack delete key %q: %#v", deleteKey, hints)
	}
}

func TestDotsChildOutOfSync(t *testing.T) {
	parent := func(state app.DotState) app.DotStatus {
		return app.DotStatus{Name: "config", State: state}
	}
	for _, tc := range []struct {
		name string
		row  dotsVisibleRow
		want bool
	}{
		{name: "non-child row", row: dotsVisibleRow{entry: parent(app.DotStateModified)}, want: false},
		{name: "modified child", row: dotsVisibleRow{entry: parent(app.DotStateSynced), child: app.DotChild{RelPath: "a", State: app.DotStateModified}, isChild: true}, want: true},
		{name: "missing child", row: dotsVisibleRow{entry: parent(app.DotStateSynced), child: app.DotChild{RelPath: "a", State: app.DotStateMissing}, isChild: true}, want: true},
		{name: "child inherits conflict parent state", row: dotsVisibleRow{entry: parent(app.DotStateConflict), child: app.DotChild{RelPath: "a"}, isChild: true}, want: true},
		{name: "synced child", row: dotsVisibleRow{entry: parent(app.DotStateModified), child: app.DotChild{RelPath: "a", State: app.DotStateSynced}, isChild: true}, want: false},
		{name: "ignored child", row: dotsVisibleRow{entry: parent(app.DotStateConflict), child: app.DotChild{RelPath: "a", Ignored: true}, isChild: true}, want: false},
		{name: "child inherits synced parent state", row: dotsVisibleRow{entry: parent(app.DotStateSynced), child: app.DotChild{RelPath: "a"}, isChild: true}, want: false},
		{name: "no-source child", row: dotsVisibleRow{entry: parent(app.DotStateModified), child: app.DotChild{RelPath: "a", State: app.DotStateNoSource}, isChild: true}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := dotsChildOutOfSync(tc.row); got != tc.want {
				t.Fatalf("dotsChildOutOfSync = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDotsRowHints_ChildInlineActions(t *testing.T) {
	hintDescs := func(m Model) []string {
		hints := dotsRowHintItems(m)
		descs := make([]string, len(hints))
		for i, h := range hints {
			descs[i] = h.desc
		}
		return descs
	}

	t.Run("out-of-sync child of syncable entry shows sync hint", func(t *testing.T) {
		m := dotsChildRowModel(
			app.DotStatus{Name: "config", State: app.DotStateModified, Actions: []app.DotAction{app.DotActionSync, app.DotActionRemove, app.DotActionIgnore}},
			app.DotChild{RelPath: "nvim", State: app.DotStateModified},
		)
		descs := hintDescs(m)
		want := app.DotStatusSyncActionLabel(m.dotsEntries[0])
		if !slices.Contains(descs, want) {
			t.Fatalf("child row hints = %v, want sync hint %q", descs, want)
		}
	})

	t.Run("child of conflict entry shows resolve hints", func(t *testing.T) {
		m := dotsChildRowModel(
			app.DotStatus{Name: "config", State: app.DotStateConflict, Actions: []app.DotAction{app.DotActionUseRepo, app.DotActionUseLocal, app.DotActionRemove}},
			app.DotChild{RelPath: "nvim"},
		)
		descs := hintDescs(m)
		for _, want := range []string{"use repo", "use local"} {
			if !slices.Contains(descs, want) {
				t.Fatalf("child row hints = %v, want %q", descs, want)
			}
		}
	})

	t.Run("synced child hides repair hints", func(t *testing.T) {
		m := dotsChildRowModel(
			app.DotStatus{Name: "config", State: app.DotStateModified, Actions: []app.DotAction{app.DotActionSync, app.DotActionUseRepo, app.DotActionUseLocal}},
			app.DotChild{RelPath: "nvim", State: app.DotStateSynced},
		)
		descs := hintDescs(m)
		for _, blocked := range []string{app.DotStatusSyncActionLabel(m.dotsEntries[0]), "use repo", "use local"} {
			if slices.Contains(descs, blocked) {
				t.Fatalf("synced child row hints = %v, must not include %q", descs, blocked)
			}
		}
	})
}

func TestFlow_DotsSyncKeyOnChildRow(t *testing.T) {
	t.Run("out-of-sync child starts parent sync", func(t *testing.T) {
		m := dotsChildRowModel(
			app.DotStatus{Name: "config", State: app.DotStateModified, Actions: []app.DotAction{app.DotActionSync, app.DotActionRemove}},
			app.DotChild{RelPath: "nvim", State: app.DotStateModified},
		)
		got := drive(m, pressRune('s'))
		if !got.dotsLoading {
			t.Fatal("dotsLoading should be true after s on out-of-sync child")
		}
		if got.statusMsg != "Syncing config…" {
			t.Errorf("statusMsg = %q, want %q", got.statusMsg, "Syncing config…")
		}
	})

	t.Run("synced child is a no-op", func(t *testing.T) {
		m := dotsChildRowModel(
			app.DotStatus{Name: "config", State: app.DotStateModified, Actions: []app.DotAction{app.DotActionSync, app.DotActionRemove}},
			app.DotChild{RelPath: "nvim", State: app.DotStateSynced},
		)
		got := drive(m, pressRune('s'))
		if got.dotsLoading {
			t.Fatal("dotsLoading should stay false after s on synced child")
		}
	})
}

func TestFlow_DotsChildConflictResolve(t *testing.T) {
	conflictChildModel := func() Model {
		return dotsChildRowModel(
			app.DotStatus{Name: "config", State: app.DotStateConflict, Actions: []app.DotAction{app.DotActionUseRepo, app.DotActionUseLocal, app.DotActionRemove}},
			app.DotChild{RelPath: "nvim"},
		)
	}

	t.Run("first u on conflict child arms overwrite at child index", func(t *testing.T) {
		got := drive(conflictChildModel(), pressRune('u'))
		if got.dotsOverwriteIdx != 1 {
			t.Fatalf("dotsOverwriteIdx = %d, want 1", got.dotsOverwriteIdx)
		}
	})

	t.Run("second u resolves from child row", func(t *testing.T) {
		got := drive(conflictChildModel(), pressRune('u'), pressRune('u'))
		if !got.dotsLoading {
			t.Fatal("dotsLoading should be true after second u on conflict child")
		}
		if got.statusMsg != "Using repo for config…" {
			t.Errorf("statusMsg = %q, want %q", got.statusMsg, "Using repo for config…")
		}
	})

	t.Run("armed child row renders repo confirm hint", func(t *testing.T) {
		got := drive(conflictChildModel(), pressRune('u'))
		out := renderDots(got)
		if !strings.Contains(out, "confirm use repo") {
			t.Fatalf("renderDots output lacks repo confirm hint for armed child row:\n%s", out)
		}
	})
}

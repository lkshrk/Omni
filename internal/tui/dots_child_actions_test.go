package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
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

func TestDotsRowHints_DiscoveredLocalOnlyIncludesVariant(t *testing.T) {
	m := dotsDiscoveredLocalOnlyModel()
	hints := dotsRowHintItems(m)
	variantKey := m.keys.DotVariant.Help().Key
	if !slices.ContainsFunc(hints, func(h hintItem) bool { return h.key == variantKey }) {
		t.Fatalf("row hints for discovered local-only entry lack variant key %q: %#v", variantKey, hints)
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
		if got.statusMsg != "Press u again to use repo for nvim" {
			t.Fatalf("statusMsg = %q, want visible confirmation prompt", got.statusMsg)
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

	t.Run("first l names the selected child in the confirmation prompt", func(t *testing.T) {
		got := drive(conflictChildModel(), pressRune('l'))
		if got.dotsLocalIdx != 1 {
			t.Fatalf("dotsLocalIdx = %d, want 1", got.dotsLocalIdx)
		}
		if got.statusMsg != "Press l again to use local for nvim" {
			t.Fatalf("statusMsg = %q, want visible confirmation prompt", got.statusMsg)
		}
	})

	t.Run("remapped repo key appears in the confirmation prompt", func(t *testing.T) {
		m := conflictChildModel()
		m.keys.DotUseRepo.SetKeys("r")
		got := drive(m, pressRune('r'))
		if got.dotsOverwriteIdx != 1 {
			t.Fatalf("dotsOverwriteIdx = %d, want 1", got.dotsOverwriteIdx)
		}
		if got.statusMsg != "Press r again to use repo for nvim" {
			t.Fatalf("statusMsg = %q, want remapped key", got.statusMsg)
		}
	})

	t.Run("timeout clears child confirmation and status", func(t *testing.T) {
		armed := drive(conflictChildModel(), pressRune('u'))
		got := drive(armed, confirmTimeoutMsg{gen: armed.confirmGen})
		if got.dotsOverwriteIdx != -1 {
			t.Fatalf("dotsOverwriteIdx = %d, want -1", got.dotsOverwriteIdx)
		}
		if got.statusMsg != "" {
			t.Fatalf("statusMsg = %q, want cleared", got.statusMsg)
		}
	})

	t.Run("escape clears child confirmation and status", func(t *testing.T) {
		armed := drive(conflictChildModel(), pressRune('u'))
		got := drive(armed, pressEsc())
		if got.dotsOverwriteIdx != -1 {
			t.Fatalf("dotsOverwriteIdx = %d, want -1", got.dotsOverwriteIdx)
		}
		if got.statusMsg != "" {
			t.Fatalf("statusMsg = %q, want cleared", got.statusMsg)
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

type dotsChildConflictFixture struct {
	model       Model
	repoAgents  string
	localAgents string
}

func newDotsChildConflictFixture(t *testing.T) dotsChildConflictFixture {
	t.Helper()
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	group := tuiTestHostGroup()
	group.Dots = []config.DotEntry{{Name: "claude", Path: "~/.claude", Ignore: []string{"projects/**"}}}
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Groups:   []*config.GroupConfig{group},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	repoAgents := filepath.Join(repoDir, "dotfiles", "claude", ".claude", "agents")
	localAgents := filepath.Join(homeDir, ".claude", "agents")
	for i := range 24 {
		name := fmt.Sprintf("agent-%02d.md", i)
		if i == 0 {
			name = "explore.md"
		}
		mustWriteTUIDotFile(t, filepath.Join(repoAgents, name), "repo "+name)
		mustWriteTUIDotFile(t, filepath.Join(localAgents, name), "local "+name)
	}
	mustWriteTUIDotFile(t, filepath.Join(homeDir, ".claude", ".cache", "state.json"), "cache state")
	mustWriteTUIDotFile(t, filepath.Join(homeDir, ".claude", "projects", "session.json"), "project state")

	a := app.New(cfgPath)
	a.CacheDir = cfgDir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	result, err := a.DiscoverDotsStatus(context.Background())
	if err != nil {
		t.Fatalf("DiscoverDotsStatus: %v", err)
	}
	m := baseModel(nil)
	m.app = a
	m.ctx = context.Background()
	m.mode = viewDots
	m.dotsLoaded = true
	m.stowInstalled = true
	setDotsRepoForTest(&m, repoDir)
	cacheDotsAvailability(&m, app.DotsSyncAvailability{Configured: true, Reason: app.DotsSyncAvailabilityReady, RepoPath: repoDir})
	m.dotsEntries = result.Entries
	m.dotsExpandedName = "claude"
	m.dotsExpandedState = app.DotStateConflict
	m.dotsExpandedChildren = map[string]bool{dotsChildExpandKey("claude", "agents"): true}
	for i, row := range dotsVisibleRows(m) {
		if row.isChild && filepath.ToSlash(row.child.RelPath) == "agents/explore.md" {
			m.dotsCursor = i
			break
		}
	}
	return dotsChildConflictFixture{model: m, repoAgents: repoAgents, localAgents: localAgents}
}

func executeConfirmedChildResolve(t *testing.T, m Model, action rune) Model {
	t.Helper()
	armed := drive(m, pressRune(action))
	next, cmd := armed.Update(pressRune(action))
	resolving := next.(Model)
	if cmd == nil {
		t.Fatal("confirmed child resolve returned nil command")
	}
	msg := runLastBatchCommand(t, cmd)
	return drive(resolving, msg)
}

func requireResolvedExploreChild(t *testing.T, resolved Model) {
	t.Helper()
	if strings.Contains(resolved.statusMsg, "✗") {
		t.Fatalf("child resolve failed: %s", resolved.statusMsg)
	}
	found := false
	for _, entry := range resolved.dotsEntries {
		if entry.Name != "claude" {
			continue
		}
		for _, child := range entry.Children {
			for _, nested := range child.Children {
				if filepath.ToSlash(nested.RelPath) == "agents/explore.md" {
					found = true
					if nested.State != app.DotStateSynced {
						t.Fatalf("explore.md state = %q, want synced", nested.State)
					}
				}
			}
		}
	}
	if !found {
		t.Fatal("refreshed dots state lacks agents/explore.md")
	}
}

func requireSameResolvedPath(t *testing.T, gotPath, wantPath string) {
	t.Helper()
	resolvedLocal, err := filepath.EvalSymlinks(gotPath)
	if err != nil {
		t.Fatalf("resolve %s: %v", gotPath, err)
	}
	wantSource, err := filepath.EvalSymlinks(wantPath)
	if err != nil {
		t.Fatalf("resolve %s: %v", wantPath, err)
	}
	if resolvedLocal != wantSource {
		t.Fatalf("%s resolves to %q, want %q", gotPath, resolvedLocal, wantSource)
	}
}

func requireFileBody(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}

func TestFlow_DotsChildConflictUseRepoExecutesAndRefreshes(t *testing.T) {
	f := newDotsChildConflictFixture(t)
	resolved := executeConfirmedChildResolve(t, f.model, 'u')
	requireResolvedExploreChild(t, resolved)
	requireSameResolvedPath(t, filepath.Join(f.localAgents, "explore.md"), filepath.Join(f.repoAgents, "explore.md"))
	requireFileBody(t, filepath.Join(filepath.Dir(f.localAgents), ".cache", "state.json"), "cache state")
	requireFileBody(t, filepath.Join(filepath.Dir(f.localAgents), "projects", "session.json"), "project state")
}

func TestFlow_DotsChildConflictUseLocalExecutesAndRefreshes(t *testing.T) {
	f := newDotsChildConflictFixture(t)
	resolved := executeConfirmedChildResolve(t, f.model, 'l')
	requireResolvedExploreChild(t, resolved)
	requireSameResolvedPath(t, filepath.Join(f.localAgents, "explore.md"), filepath.Join(f.repoAgents, "explore.md"))
	requireFileBody(t, filepath.Join(f.repoAgents, "explore.md"), "local explore.md")
	requireFileBody(t, filepath.Join(filepath.Dir(f.localAgents), ".cache", "state.json"), "cache state")
	requireFileBody(t, filepath.Join(filepath.Dir(f.localAgents), "projects", "session.json"), "project state")
}

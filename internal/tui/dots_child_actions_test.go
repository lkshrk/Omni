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
	"github.com/lkshrk/omni/internal/dots"
)

func dotsDiscoveredLocalOnlyModel() Model {
	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	setDotsRepoForTest(&m, "/repo/dotfiles")
	m.dotsEntries = []app.DotStatus{{
		Name:       "kitty",
		TargetPath: "~/.config/kitty",
		State:      dots.StateLocalOnly,
		Actions:    []dots.Action{dots.ActionSync, dots.ActionRemove, dots.ActionIgnore},
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	m := dotsDiscoveredLocalOnlyModel()
	hints := dotsRowHintItems(m)
	deleteKey := m.keys.DotDelete.Help().Key
	if !slices.ContainsFunc(hints, func(h hintItem) bool { return h.key == deleteKey }) {
		t.Fatalf("row hints for discovered local-only entry lack delete key %q: %#v", deleteKey, hints)
	}
}

func TestDotsRowHints_DiscoveredLocalOnlyIncludesVariant(t *testing.T) {
	t.Parallel()
	m := dotsDiscoveredLocalOnlyModel()
	hints := dotsRowHintItems(m)
	variantKey := m.keys.DotVariant.Help().Key
	if !slices.ContainsFunc(hints, func(h hintItem) bool { return h.key == variantKey }) {
		t.Fatalf("row hints for discovered local-only entry lack variant key %q: %#v", variantKey, hints)
	}
}

func TestDotsChildOutOfSync(t *testing.T) {
	t.Parallel()
	parent := func(state dots.State) app.DotStatus {
		return app.DotStatus{Name: "config", State: state}
	}
	for _, tc := range []struct {
		name string
		row  dotsVisibleRow
		want bool
	}{
		{name: "non-child row", row: dotsVisibleRow{entry: parent(dots.StateModified)}, want: false},
		{name: "modified child", row: dotsVisibleRow{entry: parent(dots.StateSynced), child: app.DotChild{RelPath: "a", State: dots.StateModified}, isChild: true}, want: true},
		{name: "missing child", row: dotsVisibleRow{entry: parent(dots.StateSynced), child: app.DotChild{RelPath: "a", State: dots.StateMissing}, isChild: true}, want: true},
		{name: "child inherits conflict parent state", row: dotsVisibleRow{entry: parent(dots.StateConflict), child: app.DotChild{RelPath: "a"}, isChild: true}, want: true},
		{name: "synced child", row: dotsVisibleRow{entry: parent(dots.StateModified), child: app.DotChild{RelPath: "a", State: dots.StateSynced}, isChild: true}, want: false},
		{name: "ignored child", row: dotsVisibleRow{entry: parent(dots.StateConflict), child: app.DotChild{RelPath: "a", Ignored: true}, isChild: true}, want: false},
		{name: "child inherits synced parent state", row: dotsVisibleRow{entry: parent(dots.StateSynced), child: app.DotChild{RelPath: "a"}, isChild: true}, want: false},
		{name: "no-source child", row: dotsVisibleRow{entry: parent(dots.StateModified), child: app.DotChild{RelPath: "a", State: dots.StateNoSource}, isChild: true}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := dotsChildOutOfSync(tc.row); got != tc.want {
				t.Fatalf("dotsChildOutOfSync = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDotsRowHints_ChildInlineActions(t *testing.T) {
	t.Parallel()
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
			app.DotStatus{Name: "config", State: dots.StateModified, Actions: []dots.Action{dots.ActionSync, dots.ActionRemove, dots.ActionIgnore}},
			app.DotChild{RelPath: "nvim", State: dots.StateModified},
		)
		descs := hintDescs(m)
		want := app.DotStatusSyncActionLabel(m.dotsEntries[0])
		if !slices.Contains(descs, want) {
			t.Fatalf("child row hints = %v, want sync hint %q", descs, want)
		}
	})

	t.Run("child of conflict entry shows resolve hints", func(t *testing.T) {
		m := dotsChildRowModel(
			app.DotStatus{Name: "config", State: dots.StateConflict, Actions: []dots.Action{dots.ActionUseRepo, dots.ActionUseLocal, dots.ActionRemove}},
			app.DotChild{RelPath: "nvim"},
		)
		descs := hintDescs(m)
		for _, want := range []string{"use repo", "use local"} {
			if !slices.Contains(descs, want) {
				t.Fatalf("child row hints = %v, want %q", descs, want)
			}
		}
	})

	t.Run("missing child shows install and hides conflict choices", func(t *testing.T) {
		m := dotsChildRowModel(
			app.DotStatus{Name: "config", State: dots.StateConflict, Actions: []dots.Action{dots.ActionUseRepo, dots.ActionUseLocal, dots.ActionRemove}},
			app.DotChild{RelPath: "explore.md", State: dots.StateMissing},
		)
		hints := dotsRowHintItems(m)
		if !slices.ContainsFunc(hints, func(h hintItem) bool { return h.key == "i" && h.desc == "install" }) {
			t.Fatalf("missing child hints = %#v, want i install", hints)
		}
		for _, blocked := range []string{"use repo", "use local"} {
			if slices.ContainsFunc(hints, func(h hintItem) bool { return h.desc == blocked }) {
				t.Fatalf("missing child hints = %#v, must hide %q", hints, blocked)
			}
		}
	})

	t.Run("synced child hides repair hints", func(t *testing.T) {
		m := dotsChildRowModel(
			app.DotStatus{Name: "config", State: dots.StateModified, Actions: []dots.Action{dots.ActionSync, dots.ActionUseRepo, dots.ActionUseLocal}},
			app.DotChild{RelPath: "nvim", State: dots.StateSynced},
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
	t.Parallel()
	t.Run("out-of-sync child starts parent sync", func(t *testing.T) {
		m := dotsChildRowModel(
			app.DotStatus{Name: "config", State: dots.StateModified, Actions: []dots.Action{dots.ActionSync, dots.ActionRemove}},
			app.DotChild{RelPath: "nvim", State: dots.StateModified},
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
			app.DotStatus{Name: "config", State: dots.StateModified, Actions: []dots.Action{dots.ActionSync, dots.ActionRemove}},
			app.DotChild{RelPath: "nvim", State: dots.StateSynced},
		)
		got := drive(m, pressRune('s'))
		if got.dotsLoading {
			t.Fatal("dotsLoading should stay false after s on synced child")
		}
	})
}

func TestFlow_DotsChildConflictResolve(t *testing.T) {
	t.Parallel()
	conflictChildModel := func() Model {
		return dotsChildRowModel(
			app.DotStatus{Name: "config", State: dots.StateConflict, Actions: []dots.Action{dots.ActionUseRepo, dots.ActionUseLocal, dots.ActionRemove}},
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

func TestFlow_DotsMissingChildRejectsUseLocalAndAcceptsInstall(t *testing.T) {
	t.Parallel()
	m := dotsChildRowModel(
		app.DotStatus{Name: "config", State: dots.StateConflict, Actions: []dots.Action{dots.ActionUseRepo, dots.ActionUseLocal, dots.ActionRemove}},
		app.DotChild{RelPath: "explore.md", State: dots.StateMissing},
	)

	local := drive(m, pressRune('l'))
	if local.dotsLocalIdx >= 0 || local.dotsLoading {
		t.Fatalf("missing child accepted use-local: confirm=%d loading=%v", local.dotsLocalIdx, local.dotsLoading)
	}

	install := drive(m, pressRune('i'))
	if !install.dotsLoading {
		t.Fatal("i should start installing a missing child from the repo")
	}
}

func TestDotsPeekSourceLine_MissingLocalIsConciseAndRed(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.width = 100
	m.height = 30
	line := dotsPeekSourceLine(m, "local source", app.DotsPeekSide{Source: app.DotsPeekSourceLocal, Path: "/home/user/.config/explore.md"})
	if plain := stripANSIEscapeSequences(line); plain != "local source: missing" {
		t.Fatalf("missing local source line = %q", plain)
	}
	if !strings.Contains(line, m.palette.styleMissing.Render("missing")) {
		t.Fatalf("missing local source should render missing in red: %q", line)
	}
	m.dotsPeek = &dotsPeekState{result: app.DotsPeekResult{
		Title: "explore.md",
		Mode:  app.DotsPeekModeText,
		Repo:  app.DotsPeekSide{Source: app.DotsPeekSourceRepo, Path: "/repo/agents/explore.md", Exists: true, Size: 12},
		Local: app.DotsPeekSide{Source: app.DotsPeekSourceLocal, Path: "/home/user/.config/explore.md"},
	}}
	popup := renderDotsPeekPopup(m)
	if plain := stripANSIEscapeSequences(popup); !strings.Contains(plain, "local source: missing") || strings.Contains(plain, "/home/user/.config/explore.md") {
		t.Fatalf("missing local source popup should be concise:\n%s", plain)
	}
	if !strings.Contains(popup, m.palette.styleMissing.Render("missing")) {
		t.Fatalf("missing local source popup should render missing in red:\n%s", popup)
	}
}

// TestRenderDots_IgnoredSectionTrackedChildShowsRealStatus verifies that a
// tracked (re-included) child living under an ignored directory renders with its
// real status in the Ignored section instead of the muted "-" reserved for
// synthesized structural containers.
func TestRenderDots_IgnoredSectionTrackedChildShowsRealStatus(t *testing.T) {
	t.Parallel()
	container := app.DotChild{
		Name:    "sub",
		RelPath: "sub",
		State:   dots.StateIgnored, // synthesized container: no real state
		IsDir:   true,
		Depth:   1,
		Children: []app.DotChild{
			{Name: "keep", RelPath: "sub/keep", State: dots.StateSynced, Depth: 2, Counts: app.DotFileCounts{Synced: 1}},
			{Name: "drop", RelPath: "sub/drop", State: dots.StateIgnored, Ignored: true, Depth: 2},
		},
	}
	entry := app.DotStatus{
		Name:       "claude",
		TargetPath: "~/.claude",
		State:      dots.StateIgnored,
		IsDir:      true,
		Actions:    []dots.Action{dots.ActionIgnore, dots.ActionRemove},
		Children:   []app.DotChild{container},
	}
	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	setDotsRepoForTest(&m, "/repo/dotfiles")
	m.dotsEntries = []app.DotStatus{entry}
	m.dotsExpandedName = "claude"
	m.dotsExpandedState = dots.StateIgnored
	m.dotsExpandedChildren = map[string]bool{dotsChildExpandKey("claude", "sub"): true}

	out := renderDots(m)
	if !strings.Contains(out, "keep") {
		t.Fatalf("tracked child 'keep' not rendered:\n%s", out)
	}
	if !strings.Contains(out, "synced") {
		t.Fatalf("tracked child 'keep' should show real 'synced' status, not muted:\n%s", out)
	}
	if !strings.Contains(out, "ignored") {
		t.Fatalf("ignored sibling 'drop' should show 'ignored' status:\n%s", out)
	}
}

// TestDotsExpand_MultipleLayersDeep verifies expansion works at every nested
// directory level: a child is visible only once its parent dir is expanded, all
// the way down the tree, and toggling a deep dir via the key path reveals its
// children.
func TestDotsExpand_MultipleLayersDeep(t *testing.T) {
	t.Parallel()
	leaf := app.DotChild{Name: "c.txt", RelPath: "a/b/c.txt", State: dots.StateSynced, Depth: 3}
	b := app.DotChild{Name: "b", RelPath: "a/b", State: dots.StateSynced, IsDir: true, Depth: 2, Children: []app.DotChild{leaf}}
	a := app.DotChild{Name: "a", RelPath: "a", State: dots.StateSynced, IsDir: true, Depth: 1, Children: []app.DotChild{b}}
	entry := app.DotStatus{Name: "root", TargetPath: "~/root", State: dots.StateSynced, IsDir: true, Children: []app.DotChild{a}}

	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	setDotsRepoForTest(&m, "/repo/dotfiles")
	m.dotsEntries = []app.DotStatus{entry}
	m.dotsExpandedName = "root"
	m.dotsExpandedState = dots.StateSynced

	relOf := func(rows []dotsVisibleRow) []string {
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			if r.isChild {
				out = append(out, r.child.RelPath)
			}
		}
		return out
	}
	has := func(rows []dotsVisibleRow, rel string) bool {
		for _, v := range relOf(rows) {
			if v == rel {
				return true
			}
		}
		return false
	}

	// Top expanded only: 'a' visible, deeper hidden.
	rows := dotsVisibleRows(m)
	if !has(rows, "a") || has(rows, "a/b") || has(rows, "a/b/c.txt") {
		t.Fatalf("with only top expanded want [a], got %v", relOf(rows))
	}

	// Expand 'a' via the toggle key: 'b' appears, leaf still hidden.
	m.dotsCursor = dotsRowIndex(dotsVisibleRows(m), dotsVisibleRow{entry: entry, child: a, isChild: true})
	m.handleDotsToggleKeyMsg(dotsVisibleRows(m))
	rows = dotsVisibleRows(m)
	if !has(rows, "a/b") || has(rows, "a/b/c.txt") {
		t.Fatalf("after expanding 'a' want a/b visible, leaf hidden, got %v", relOf(rows))
	}

	// Expand 'a/b': the depth-3 leaf becomes visible.
	m.dotsCursor = dotsRowIndex(dotsVisibleRows(m), dotsVisibleRow{entry: entry, child: b, isChild: true})
	m.handleDotsToggleKeyMsg(dotsVisibleRows(m))
	rows = dotsVisibleRows(m)
	if !has(rows, "a/b/c.txt") {
		t.Fatalf("after expanding 'a/b' want depth-3 leaf visible, got %v", relOf(rows))
	}
}

func TestFlow_DotsExpandCollapse_MultipleLayersDeep(t *testing.T) {
	t.Parallel()
	leaf := app.DotChild{Name: "c.txt", RelPath: "a/b/c.txt", State: dots.StateSynced, Depth: 3}
	b := app.DotChild{Name: "b", RelPath: "a/b", State: dots.StateSynced, IsDir: true, Depth: 2, Children: []app.DotChild{leaf}}
	a := app.DotChild{Name: "a", RelPath: "a", State: dots.StateSynced, IsDir: true, Depth: 1, Children: []app.DotChild{b}}
	entry := app.DotStatus{Name: "root", TargetPath: "~/root", State: dots.StateSynced, IsDir: true, Children: []app.DotChild{a}}
	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	setDotsRepoForTest(&m, "/repo/dotfiles")
	m.dotsEntries = []app.DotStatus{entry}

	toggle := func(rel string) {
		t.Helper()
		rows := dotsVisibleRows(m)
		idx := -1
		for i, row := range rows {
			if (rel == "" && !row.isChild) || (row.isChild && row.child.RelPath == rel) {
				idx = i
				break
			}
		}
		if idx < 0 {
			t.Fatalf("row %q is not visible", rel)
		}
		m.dotsCursor = idx
		m = drive(m, pressRune(' '))
	}
	visible := func(rel string) bool {
		for _, row := range dotsVisibleRows(m) {
			if row.isChild && row.child.RelPath == rel {
				return true
			}
		}
		return false
	}

	toggle("")
	toggle("a")
	toggle("a/b")
	if !visible("a/b/c.txt") {
		t.Fatal("depth-3 child did not appear after expanding depth-2 directory")
	}
	toggle("a")
	if visible("a/b") {
		t.Fatal("depth-2 directory remained visible after collapsing its parent")
	}
	toggle("a")
	if !visible("a/b") || visible("a/b/c.txt") {
		t.Fatal("re-expanding parent restored stale descendant expansion")
	}
}

func TestFlow_DotsExpandCollapse_IgnoredDirectory(t *testing.T) {
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	target := filepath.Join(homeDir, ".config", "nvim")
	source := filepath.Join(repoDir, "dotfiles", "nvim", ".config", "nvim")
	mustWriteTUIDotFile(t, filepath.Join(source, "node_modules", "pkg", "mod.js"), "module")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, target); err != nil {
		t.Fatal(err)
	}
	group := tuiTestHostGroup()
	group.Dots = []config.DotEntry{{Name: "nvim", Path: target}}
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{Settings: config.Settings{DotsRepo: repoDir}, Groups: []*config.GroupConfig{group}}); err != nil {
		t.Fatalf("save config: %v", err)
	}
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
	setDotsRepoForTest(&m, repoDir)
	m.dotsEntries = result.Entries

	toggle := func(rel string) {
		t.Helper()
		idx := -1
		for i, row := range dotsVisibleRows(m) {
			if (rel == "" && !row.isChild && row.entry.Name == "nvim" && app.DotStatusState(row.entry) == dots.StateIgnored) ||
				(row.isChild && filepath.ToSlash(row.child.RelPath) == rel) {
				idx = i
				break
			}
		}
		if idx < 0 {
			t.Fatalf("ignored row %q is not visible: %+v", rel, dotsVisibleRows(m))
		}
		m.dotsCursor = idx
		m = drive(m, pressRune(' '))
	}
	visible := func(rel string) bool {
		for _, row := range dotsVisibleRows(m) {
			if row.isChild && filepath.ToSlash(row.child.RelPath) == rel {
				return true
			}
		}
		return false
	}

	toggle("")
	toggle("node_modules")
	toggle("node_modules/pkg")
	if !visible("node_modules/pkg/mod.js") {
		t.Fatalf("depth-3 ignored child did not appear: %+v", dotsVisibleRows(m))
	}
	toggle("node_modules")
	toggle("node_modules")
	if !visible("node_modules/pkg") || visible("node_modules/pkg/mod.js") {
		t.Fatal("re-expanding ignored parent restored stale descendant expansion")
	}
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
	for _, entry := range result.Entries {
		if entry.Name == "claude" {
			m.dotsExpandedState = app.DotStatusState(entry)
			break
		}
	}
	m.dotsExpandedChildren = map[string]bool{dotsChildExpandKey("claude", "agents"): true}
	foundSelected := false
	for i, row := range dotsVisibleRows(m) {
		if row.isChild && filepath.ToSlash(row.child.RelPath) == "agents/explore.md" {
			m.dotsCursor = i
			foundSelected = true
			break
		}
	}
	if !foundSelected {
		t.Fatal("fixture did not expose agents/explore.md as a child row")
	}
	return dotsChildConflictFixture{model: m, repoAgents: repoAgents, localAgents: localAgents}
}

// TestFlow_DotsChildVariant_ExtractsThenCreates verifies that pressing the
// variant key on a tracked child row arms a create prompt and, on confirm,
// begins the extract-then-variant operation for that sub-path.
func TestFlow_DotsChildVariant_ExtractsThenCreates(t *testing.T) {
	fx := newDotsChildConflictFixture(t)
	m := fx.model
	m.dotsExpandedChildren = nil
	idx := -1
	for _, e := range m.dotsEntries {
		if !e.IsDir || len(e.Children) == 0 {
			continue
		}
		m.dotsExpandedName = e.Name
		m.dotsExpandedState = app.DotStatusState(e)
		for i, r := range dotsVisibleRows(m) {
			if r.isChild && dotsChildExtractable(r) {
				idx = i
				break
			}
		}
		if idx >= 0 {
			break
		}
	}
	if idx < 0 {
		t.Skip("fixture built no extractable child row")
	}
	m.dotsCursor = idx
	if !dotsVariantEligible(dotsVisibleRows(m)[idx]) {
		t.Fatalf("tracked child row should be variant-eligible")
	}

	armed := drive(m, pressRune('v'))
	if armed.dotsVariantIdx != armed.dotsCursor {
		t.Fatalf("dotsVariantIdx = %d, want cursor %d", armed.dotsVariantIdx, armed.dotsCursor)
	}
	if armed.dotsVariantMode != dotsVariantCreate {
		t.Fatalf("variant mode = %v, want create", armed.dotsVariantMode)
	}
	if out := renderDots(armed); !strings.Contains(out, "create host variant") {
		t.Fatalf("armed child row lacks variant create prompt:\n%s", out)
	}

	got := drive(armed, pressRune('v'))
	if !got.dotsLoading {
		t.Fatal("dotsLoading should be true after confirming child variant")
	}
	if !strings.Contains(got.statusMsg, "Creating variant") {
		t.Fatalf("statusMsg = %q, want a Creating variant… message", got.statusMsg)
	}
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

func executeDotsAction(t *testing.T, m Model, action rune) Model {
	t.Helper()
	next, cmd := m.Update(pressRune(action))
	started := next.(Model)
	if cmd == nil {
		t.Fatalf("dots action %q returned nil command", action)
	}
	return drive(started, runLastBatchCommand(t, cmd))
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
					if nested.State != dots.StateSynced {
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
	requireFileBody(t, filepath.Join(f.localAgents, "agent-01.md"), "local agent-01.md")
	requireFileBody(t, filepath.Join(f.repoAgents, "agent-01.md"), "repo agent-01.md")
	requireFileBody(t, filepath.Join(filepath.Dir(f.localAgents), ".cache", "state.json"), "cache state")
	requireFileBody(t, filepath.Join(filepath.Dir(f.localAgents), "projects", "session.json"), "project state")
}

func TestFlow_DotsChildConflictUseLocalExecutesAndRefreshes(t *testing.T) {
	f := newDotsChildConflictFixture(t)
	resolved := executeConfirmedChildResolve(t, f.model, 'l')
	requireResolvedExploreChild(t, resolved)
	requireSameResolvedPath(t, filepath.Join(f.localAgents, "explore.md"), filepath.Join(f.repoAgents, "explore.md"))
	requireFileBody(t, filepath.Join(f.repoAgents, "explore.md"), "local explore.md")
	requireFileBody(t, filepath.Join(f.localAgents, "agent-01.md"), "local agent-01.md")
	requireFileBody(t, filepath.Join(f.repoAgents, "agent-01.md"), "repo agent-01.md")
	requireFileBody(t, filepath.Join(filepath.Dir(f.localAgents), ".cache", "state.json"), "cache state")
	requireFileBody(t, filepath.Join(filepath.Dir(f.localAgents), "projects", "session.json"), "project state")
}

func TestFlow_DotsMissingChildInstallExecutesAndRefreshes(t *testing.T) {
	f := newDotsChildConflictFixture(t)
	if err := os.Remove(filepath.Join(f.localAgents, "explore.md")); err != nil {
		t.Fatal(err)
	}
	result, err := f.model.app.DiscoverDotsStatus(context.Background())
	if err != nil {
		t.Fatalf("DiscoverDotsStatus: %v", err)
	}
	f.model.dotsEntries = result.Entries
	for _, entry := range result.Entries {
		if entry.Name == "claude" {
			f.model.dotsExpandedState = app.DotStatusState(entry)
			break
		}
	}
	found := false
	for i, row := range dotsVisibleRows(f.model) {
		if row.isChild && filepath.ToSlash(row.child.RelPath) == "agents/explore.md" {
			f.model.dotsCursor = i
			found = true
			break
		}
	}
	if !found {
		t.Fatal("missing agents/explore.md child was not visible")
	}

	installed := executeDotsAction(t, f.model, 'i')
	requireResolvedExploreChild(t, installed)
	requireSameResolvedPath(t, filepath.Join(f.localAgents, "explore.md"), filepath.Join(f.repoAgents, "explore.md"))
	requireFileBody(t, filepath.Join(f.localAgents, "agent-01.md"), "local agent-01.md")
	requireFileBody(t, filepath.Join(f.repoAgents, "agent-01.md"), "repo agent-01.md")
}

func TestFlow_DotsTopLevelInstallExecutesAndRefreshes(t *testing.T) {
	for _, configured := range []bool{false, true} {
		name := "repo-only discovered entry"
		wantState := dots.StateRepoOnly
		if configured {
			name = "missing configured entry"
			wantState = dots.StateMissing
		}
		t.Run(name, func(t *testing.T) {
			cfgDir := t.TempDir()
			cfgPath := filepath.Join(cfgDir, "settings.json")
			repoDir := t.TempDir()
			homeDir := t.TempDir()
			t.Setenv("HOME", homeDir)
			if err := saveTUIConfig(t, cfgPath, &config.RootConfig{Settings: config.Settings{DotsRepo: repoDir}}); err != nil {
				t.Fatalf("save config: %v", err)
			}
			source := filepath.Join(repoDir, "dotfiles", "nvim", ".config", "nvim", "init.lua")
			target := filepath.Join(homeDir, ".config", "nvim", "init.lua")
			mustWriteTUIDotFile(t, source, "-- repo")
			a := app.New(cfgPath)
			a.CacheDir = cfgDir
			if err := a.InitTestMode(context.Background()); err != nil {
				t.Fatalf("InitTestMode: %v", err)
			}
			t.Cleanup(func() { _ = a.Close() })
			if configured {
				if _, err := a.DotsAddDiscoveredEntryContext(context.Background(), "nvim", ""); err != nil {
					t.Fatalf("DotsAddDiscoveredEntryContext: %v", err)
				}
			}
			status, err := a.DiscoverDotsStatus(context.Background())
			if err != nil {
				t.Fatalf("DiscoverDotsStatus: %v", err)
			}
			m := baseModel(nil)
			m.app = a
			m.ctx = context.Background()
			m.mode = viewDots
			m.dotsLoaded = true
			m.stowInstalled = true
			m.dotsEntries = status.Entries
			setDotsRepoForTest(&m, repoDir)
			cacheDotsAvailability(&m, app.DotsSyncAvailability{Configured: true, Reason: app.DotsSyncAvailabilityReady, RepoPath: repoDir})
			found := false
			for i, row := range dotsVisibleRows(m) {
				if row.entry.Name == "nvim" {
					if got := dotsRowState(row); got != wantState {
						t.Fatalf("nvim state = %q, want %q", got, wantState)
					}
					m.dotsCursor = i
					found = true
					break
				}
			}
			if !found {
				t.Fatal("nvim entry was not visible")
			}

			installed := executeDotsAction(t, m, 'i')
			for _, entry := range installed.dotsEntries {
				if entry.Name == "nvim" && app.DotStatusState(entry) != dots.StateSynced {
					t.Fatalf("nvim state = %q, want synced", app.DotStatusState(entry))
				}
			}
			requireSameResolvedPath(t, target, source)
		})
	}
}

func TestFlow_DotsIgnoredEntryIncludeExecutesAndRefreshes(t *testing.T) {
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	targetDir := filepath.Join(homeDir, ".config", "kitty")
	targetFile := filepath.Join(targetDir, "kitty.conf")
	mustWriteTUIDotFile(t, targetFile, "font_size 12")
	group := tuiTestHostGroup()
	group.Dots = []config.DotEntry{{Name: "kitty", Path: targetDir, Ignored: true}}
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{Settings: config.Settings{DotsRepo: repoDir}, Groups: []*config.GroupConfig{group}}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	a := app.New(cfgPath)
	a.CacheDir = cfgDir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	m := baseModel(nil)
	m.app = a
	m.ctx = context.Background()
	m.mode = viewDots
	m.dotsLoaded = true
	m.stowInstalled = true
	m.dotsEntries = []app.DotStatus{{
		Name:       "kitty",
		TargetPath: targetDir,
		ConfigPath: targetDir,
		State:      dots.StateIgnored,
		Actions:    []dots.Action{dots.ActionUnignore, dots.ActionRemove},
	}}
	setDotsRepoForTest(&m, repoDir)
	cacheDotsAvailability(&m, app.DotsSyncAvailability{Configured: true, Reason: app.DotsSyncAvailabilityReady, RepoPath: repoDir})
	armed := drive(m, pressRune('x'))
	if armed.dotsIgnoreIdx != 0 {
		t.Fatalf("dotsIgnoreIdx = %d, want 0", armed.dotsIgnoreIdx)
	}
	included := executeDotsAction(t, armed, 'x')
	foundSynced := false
	for _, entry := range included.dotsEntries {
		if entry.Name == "kitty" {
			foundSynced = app.DotStatusState(entry) == dots.StateSynced
		}
	}
	if !foundSynced {
		t.Fatalf("included kitty entries = %+v, want synced", included.dotsEntries)
	}
	sourceFile := filepath.Join(repoDir, "dotfiles", "kitty", ".config", "kitty", "kitty.conf")
	requireSameResolvedPath(t, targetFile, sourceFile)
}

func TestFlow_DotsNestedIgnoredChildIncludeExecutesAndRefreshes(t *testing.T) {
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	targetDir := filepath.Join(homeDir, ".claude")
	targetFile := filepath.Join(targetDir, "projects", "session.json")
	mustWriteTUIDotFile(t, targetFile, "local session")
	group := tuiTestHostGroup()
	group.Dots = []config.DotEntry{{Name: "claude", Path: targetDir, Ignore: []string{"projects"}}}
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{Settings: config.Settings{DotsRepo: repoDir}, Groups: []*config.GroupConfig{group}}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	a := app.New(cfgPath)
	a.CacheDir = cfgDir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	status, err := a.DiscoverDotsStatus(context.Background())
	if err != nil {
		t.Fatalf("DiscoverDotsStatus: %v", err)
	}

	m := baseModel(nil)
	m.app = a
	m.ctx = context.Background()
	m.mode = viewDots
	m.dotsLoaded = true
	m.stowInstalled = true
	m.dotsEntries = status.Entries
	m.dotsExpandedName = "claude"
	m.dotsExpandedState = dots.StateIgnored
	m.dotsExpandedChildren = map[string]bool{dotsChildExpandKey("claude", "projects"): true}
	setDotsRepoForTest(&m, repoDir)
	cacheDotsAvailability(&m, app.DotsSyncAvailability{Configured: true, Reason: app.DotsSyncAvailabilityReady, RepoPath: repoDir})
	found := false
	for i, row := range dotsVisibleRows(m) {
		if row.isChild && filepath.ToSlash(row.child.RelPath) == "projects/session.json" {
			m.dotsCursor = i
			found = true
			break
		}
	}
	if !found {
		t.Fatal("nested ignored child was not visible")
	}

	armed := drive(m, pressRune('x'))
	if armed.dotsIgnoreIdx != m.dotsCursor {
		t.Fatalf("dotsIgnoreIdx = %d, want %d", armed.dotsIgnoreIdx, m.dotsCursor)
	}
	included := executeDotsAction(t, armed, 'x')
	foundSynced := false
	for _, row := range dotsVisibleRows(included) {
		if row.entry.Name != "claude" || row.isChild {
			continue
		}
		if state := dotsRowState(row); state == dots.StateLocalOnly {
			t.Fatal("included nested child rendered as local-only")
		} else if state == dots.StateSynced {
			foundSynced = true
		}
	}
	if !foundSynced {
		t.Fatalf("included claude rows = %+v, want synced", dotsVisibleRows(included))
	}
	sourceFile := filepath.Join(repoDir, "dotfiles", "claude", ".claude", "projects", "session.json")
	requireSameResolvedPath(t, targetFile, sourceFile)
}

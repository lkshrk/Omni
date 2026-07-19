package tui

import (
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/dots"
)

func TestDotsExpandLoadsUnloadedDirectoryThenExpandsIt(t *testing.T) {
	t.Parallel()
	child := app.DotChild{Name: "a", RelPath: "a", IsDir: true, State: dots.StateSynced}
	entry := app.DotStatus{Name: "nvim", State: dots.StateSynced, IsDir: true, Children: []app.DotChild{child}}
	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	m.dotsEntries = []app.DotStatus{entry}
	m.dotsExpandedName = "nvim"
	m.dotsExpandedState = dots.StateSynced
	m.dotsCursor = 1

	cmds := m.handleDotsToggleKeyMsg(dotsVisibleRows(m))
	if len(cmds) != 2 || !m.dotsLoading {
		t.Fatalf("load start = %d cmds, loading %v; want spinner+load", len(cmds), m.dotsLoading)
	}
	loaded := []app.DotChild{{Name: "b", RelPath: "a/b", IsDir: true, State: dots.StateSynced}}
	if cmds := m.handleDotsChildrenLoadedMsg(dotsChildrenLoadedMsg{
		gen:        m.dotsOpGen,
		entryName:  "nvim",
		entryState: dots.StateSynced,
		relPath:    "a",
		children:   loaded,
	}); len(cmds) != 0 {
		t.Fatalf("load completion returned %d commands", len(cmds))
	}
	got := m.dotsEntries[0].Children[0]
	if len(got.Children) != 1 || got.Children[0].RelPath != "a/b" {
		t.Fatalf("loaded children = %#v, want a/b", got.Children)
	}
	if !m.dotsExpandedChildren[dotsChildExpandKey("nvim", "a")] {
		t.Fatal("loaded directory was not expanded")
	}
}

func TestSetDotsChildChildrenPreservesLoadedEmptySlice(t *testing.T) {
	t.Parallel()
	children := []app.DotChild{{Name: "empty", RelPath: "empty", IsDir: true}}
	loaded := []app.DotChild{}
	if !setDotsChildChildren(children, "empty", loaded) {
		t.Fatal("empty directory not found")
	}
	if children[0].Children == nil {
		t.Fatal("loaded empty directory still looks unloaded")
	}
}

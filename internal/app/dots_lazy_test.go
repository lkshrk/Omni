package app_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/dots"
)

func TestDotsStatusShallowChildrenLoadOneLevelAtATime(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	source := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	if err := os.MkdirAll(filepath.Join(source, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "a", "b", "c.txt"), []byte("c"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{Name: "nvim", Path: filepath.Join(home, ".config", "nvim")}}, home)
	if _, err := a.DotsSyncContext(context.Background(), dots.SyncOptions{}); err != nil {
		t.Fatalf("sync: %v", err)
	}

	result, err := a.DotsStatus(app.WithShallowDotsChildren(context.Background()))
	if err != nil {
		t.Fatalf("shallow status: %v", err)
	}
	status := requireDotsStatusNamed(t, result.Entries, "nvim")
	if len(status.Children) != 1 || status.Children[0].RelPath != "a" {
		t.Fatalf("top-level children = %#v, want only a", status.Children)
	}
	if status.Children[0].Children != nil {
		t.Fatalf("a children = %#v, want unloaded nil", status.Children[0].Children)
	}
	if status.Children[0].Counts.Synced != 1 {
		t.Fatalf("a counts = %#v, want full subtree count", status.Children[0].Counts)
	}
	expandedCtx := app.WithExpandedDotsChildren(
		app.WithShallowDotsChildren(context.Background()),
		map[string][]string{"nvim": {"a"}},
	)
	expandedResult, err := a.DotsStatus(expandedCtx)
	if err != nil {
		t.Fatalf("expanded shallow status: %v", err)
	}
	expandedStatus := requireDotsStatusNamed(t, expandedResult.Entries, "nvim")
	if len(expandedStatus.Children[0].Children) != 1 || expandedStatus.Children[0].Children[0].RelPath != filepath.Join("a", "b") {
		t.Fatalf("expanded a children = %#v, want a/b", expandedStatus.Children[0].Children)
	}
	if expandedStatus.Children[0].Children[0].Children != nil {
		t.Fatalf("a/b children = %#v, want next level unloaded", expandedStatus.Children[0].Children[0].Children)
	}

	children, err := a.DotsChildChildren(context.Background(), "nvim", "a", false)
	if err != nil {
		t.Fatalf("load a: %v", err)
	}
	if len(children) != 1 || children[0].RelPath != filepath.Join("a", "b") || children[0].Children != nil {
		t.Fatalf("a children = %#v, want unloaded a/b", children)
	}
	children, err = a.DotsChildChildren(context.Background(), "nvim", filepath.Join("a", "b"), false)
	if err != nil {
		t.Fatalf("load a/b: %v", err)
	}
	if len(children) != 1 || children[0].RelPath != filepath.Join("a", "b", "c.txt") {
		t.Fatalf("a/b children = %#v, want c.txt", children)
	}

	if _, err := a.DotsChildChildren(context.Background(), "nvim", "../escape", false); err == nil {
		t.Fatal("traversal child path succeeded")
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(source, "a", "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DotsChildChildren(context.Background(), "nvim", filepath.Join("a", "escape"), false); err == nil {
		t.Fatal("symlink escape child path succeeded")
	}
}

func TestDotsChildChildrenLoadsTransientCandidate(t *testing.T) {
	a, _, _ := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	backups := filepath.Join(home, ".claude", "backups", "nested")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backups, "session.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := a.DiscoverDotsStatus(app.WithShallowDotsChildren(context.Background()))
	if err != nil {
		t.Fatalf("discover transient dots: %v", err)
	}
	var candidate app.DotStatus
	for _, status := range result.Entries {
		if status.Name == "claude" && app.DotStatusTransientCandidate(status) {
			candidate = status
			break
		}
	}
	if candidate.Name == "" {
		t.Fatalf("transient claude candidate missing: %#v", result.Entries)
	}
	var child app.DotChild
	for _, current := range candidate.Children {
		if current.RelPath == "backups" {
			child = current
			break
		}
	}
	if child.RelPath == "" || child.Children != nil {
		t.Fatalf("backups child = %#v, want unloaded transient directory", child)
	}

	children, err := a.DotsChildChildren(context.Background(), candidate.Name, child.RelPath, child.Ignored)
	if err != nil {
		t.Fatalf("load transient backups: %v", err)
	}
	if len(children) != 1 || children[0].RelPath != filepath.Join("backups", "nested") {
		t.Fatalf("backups children = %#v, want backups/nested", children)
	}
}

func TestDotsStatusShallowPreservesReincludedChildUnderIgnoredDirectory(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	source := filepath.Join(dotsContentDir(repoDir), "claude", ".claude", "data")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"keep.json", "drop.json"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{
		Name: "claude", Path: filepath.Join(home, ".claude"), Ignore: []string{"*", "!/data/keep.json"},
	}}, home)

	result, err := a.DiscoverDotsStatus(app.WithShallowDotsChildren(context.Background()))
	if err != nil {
		t.Fatalf("shallow discover: %v", err)
	}
	children, err := a.DotsChildChildren(context.Background(), "claude", "data", false)
	if err != nil {
		t.Fatalf("load promoted ignored directory: %v", err)
	}
	if !childTreeContains(children, "data/keep.json", false) || !childTreeContains(children, "data/drop.json", true) {
		t.Fatalf("loaded data children lost ignore ancestry: %#v", children)
	}
	for _, status := range result.Entries {
		if status.Name != "claude" || status.State != dots.StateIgnored {
			continue
		}
		if childTreeContains(status.Children, "data/keep.json", false) {
			return
		}
	}
	t.Fatalf("re-included keep.json missing from shallow ignored section: %#v", result.Entries)
}

func TestDotsStatusShallowPreservesIgnoredAncestorThroughReincludedDirectory(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	source := filepath.Join(dotsContentDir(repoDir), "claude", ".claude", "data", "nested")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"keep.json", "drop.json"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{
		Name: "claude", Path: filepath.Join(home, ".claude"),
		Ignore: []string{"data", "!/data/nested", "!/data/nested/keep.json"},
	}}, home)

	result, err := a.DiscoverDotsStatus(app.WithShallowDotsChildren(context.Background()))
	if err != nil {
		t.Fatalf("shallow discover: %v", err)
	}
	children, err := a.DotsChildChildren(context.Background(), "claude", filepath.Join("data", "nested"), false)
	if err != nil {
		t.Fatalf("load promoted nested directory: %v", err)
	}
	if !childTreeContains(children, "data/nested/keep.json", false) || !childTreeContains(children, "data/nested/drop.json", true) {
		t.Fatalf("loaded nested children lost ignore ancestry: %#v", children)
	}
	for _, status := range result.Entries {
		if status.Name != "claude" || status.State != dots.StateIgnored {
			continue
		}
		if !childTreeContains(status.Children, "data/nested/keep.json", false) {
			t.Fatalf("re-included nested keep.json missing from ignored section: %#v", status.Children)
		}
		if !childTreeContains(status.Children, "data/nested/drop.json", true) {
			t.Fatalf("nested drop.json lost ignored inheritance: %#v", status.Children)
		}
		return
	}
	t.Fatalf("ignored claude section missing: %#v", result.Entries)
}

func childTreeContains(children []app.DotChild, relPath string, ignored bool) bool {
	for _, child := range children {
		if filepath.ToSlash(child.RelPath) == relPath && child.Ignored == ignored {
			return true
		}
		if childTreeContains(child.Children, relPath, ignored) {
			return true
		}
	}
	return false
}

func requireDotsStatusNamed(t *testing.T, statuses []app.DotStatus, name string) app.DotStatus {
	t.Helper()
	for _, status := range statuses {
		if status.Name == name {
			return status
		}
	}
	t.Fatalf("dots status %q not found in %#v", name, statuses)
	return app.DotStatus{}
}

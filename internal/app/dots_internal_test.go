package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/dots"
)

func TestAgentConfigDotCandidateNames_DerivesFromCatalog(t *testing.T) {
	t.Parallel()
	names := agentConfigDotCandidateNames(newAgentRegistry())
	nameSet := make(map[string]struct{}, len(names))
	for _, n := range names {
		nameSet[n] = struct{}{}
	}

	if _, ok := nameSet["agents"]; !ok {
		t.Fatalf("agentConfigDotCandidateNames() = %v, want it to include %q (from supportedAgents .config/agents entries)", names, "agents")
	}
	for _, want := range []string{"crush", "devin", "goose", "opencode"} {
		if _, ok := nameSet[want]; !ok {
			t.Fatalf("agentConfigDotCandidateNames() = %v, want it to include %q", names, want)
		}
	}

	for _, notWant := range []string{"claude", "codex", "grok", "agents-skill-lock", "gemini"} {
		if _, ok := nameSet[notWant]; ok {
			t.Fatalf("agentConfigDotCandidateNames() = %v, must not include home-level dir %q", names, notWant)
		}
	}

}

func TestRollbackDotsAdd_RemovesPartialTargetBeforeRestore(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)

	targetPath := filepath.Join(home, ".zshrc")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	packagePath := filepath.Join(tmp, "repo", "dotfiles", "zshrc", ".zshrc")
	if err := os.MkdirAll(filepath.Dir(packagePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(packagePath, []byte("repo"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := dots.ResolvedEntry{
		Name:       "zshrc",
		Package:    "zshrc",
		SourcePath: packagePath,
		TargetPath: targetPath,
	}
	prep, err := dots.PrepareDotTargetForRestow(context.Background(), nil, entry)
	if err != nil {
		t.Fatalf("PrepareDotTargetForRestow: %v", err)
	}
	if err := os.Symlink(packagePath, targetPath); err != nil {
		t.Fatal(err)
	}

	if err := rollbackDotsAdd(context.Background(), nil, entry, packagePath, prep); err != nil {
		t.Fatalf("rollbackDotsAdd: %v", err)
	}

	info, err := os.Lstat(targetPath)
	if err != nil {
		t.Fatalf("restored target stat: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("target is still a symlink after rollback")
	}
	got, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read restored file: %v", err)
	}
	if string(got) != "original" {
		t.Fatalf("restored file = %q, want original", got)
	}
	if _, err := os.Lstat(packagePath); !os.IsNotExist(err) {
		t.Fatalf("package path still exists after rollback, stat err=%v", err)
	}
}

func TestLinkResolvedDotPath_RejectsIntermediateSymlinkOutsideHome(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	outside := filepath.Join(tmp, "sibling")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	outsideFile := filepath.Join(outside, "settings.json")
	if err := os.WriteFile(outsideFile, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, ".config")); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(tmp, "repo", "settings.json")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("repo"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := linkResolvedDotPath(dots.ResolvedEntry{
		Name:       "settings",
		SourcePath: source,
		TargetPath: filepath.Join(home, ".config", "settings.json"),
	})
	if err == nil {
		t.Error("linkResolvedDotPath accepted an intermediate symlink outside HOME")
	}
	if info, statErr := os.Lstat(outsideFile); statErr != nil {
		t.Fatalf("outside file missing: %v", statErr)
	} else if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("outside file was replaced by a symlink")
	}
	if got, readErr := os.ReadFile(outsideFile); readErr != nil || string(got) != "outside" {
		t.Fatalf("outside file changed: body=%q err=%v", got, readErr)
	}
}

func TestLinkResolvedDotPath_RejectsChildBelowManagedDirectorySymlink(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	targetParent := filepath.Join(home, ".config")
	sourceDir := filepath.Join(tmp, "repo", "nvim", ".config", "nvim")
	if err := os.MkdirAll(targetParent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	sourceFile := filepath.Join(sourceDir, "init.lua")
	if err := os.WriteFile(sourceFile, []byte("managed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sourceDir, filepath.Join(targetParent, "nvim")); err != nil {
		t.Fatal(err)
	}

	err := linkResolvedDotPath(dots.ResolvedEntry{
		Name:       "nvim/init.lua",
		SourcePath: sourceFile,
		TargetPath: filepath.Join(targetParent, "nvim", "init.lua"),
	})
	if err == nil {
		t.Error("linkResolvedDotPath accepted a child below a managed directory symlink")
	}
	if info, statErr := os.Lstat(sourceFile); statErr != nil {
		t.Fatalf("managed source missing: %v", statErr)
	} else if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("managed source was replaced by a symlink")
	}
	if got, readErr := os.ReadFile(sourceFile); readErr != nil || string(got) != "managed" {
		t.Fatalf("managed source changed: body=%q err=%v", got, readErr)
	}
}

func TestLinkResolvedDotPath_CreatesMissingParentsWithinHome(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	source := filepath.Join(tmp, "repo", "nvim", "init.lua")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("managed"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	target := filepath.Join(home, ".config", "nvim", "init.lua")

	if err := linkResolvedDotPath(dots.ResolvedEntry{Name: "nvim/init.lua", SourcePath: source, TargetPath: target}); err != nil {
		t.Fatalf("linkResolvedDotPath with missing HOME-local parents: %v", err)
	}
	if info, err := os.Lstat(target); err != nil {
		t.Fatalf("linked target missing: %v", err)
	} else if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("linked target is not a symlink")
	}
}

func TestDotsTestTargetPath_LeavesUnsupportedTildePrefixUntouched(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)

	startWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(tmp, "cwd")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(startWD)
	})

	got, err := dotsTestTargetPath("~user/.zshrc")
	if err != nil {
		t.Fatalf("dotsTestTargetPath: %v", err)
	}
	if strings.HasPrefix(filepath.Clean(got), filepath.Clean(home)+string(os.PathSeparator)) {
		t.Fatalf("got %q, want path outside HOME %q", got, home)
	}
}

func TestCountIgnoredTree_DepthGuardPreventsRunaway(t *testing.T) {
	t.Parallel()

	leaf := DotChild{Name: "file.txt", IsDir: false}
	for i := range countIgnoredTreeMaxDepth + 10 {
		leaf = DotChild{
			Name:     "d" + string(rune('0'+i%10)),
			IsDir:    true,
			Children: []DotChild{leaf},
		}
	}
	got := countIgnoredTree(leaf)
	if got != 0 {
		t.Fatalf("countIgnoredTree = %d, want 0 for tree deeper than maxDepth", got)
	}
}

func TestCountIgnoredTree_NormalDepthWorks(t *testing.T) {
	t.Parallel()
	tree := DotChild{
		Name:  "root",
		IsDir: true,
		Children: []DotChild{
			{Name: "a.txt"},
			{Name: "sub", IsDir: true, Children: []DotChild{
				{Name: "b.txt"},
				{Name: "c.txt"},
			}},
		},
	}
	got := countIgnoredTree(tree)
	if got != 3 {
		t.Fatalf("countIgnoredTree = %d, want 3", got)
	}
}

func TestCountIgnoredTree_LeafDirCountsAsOne(t *testing.T) {
	t.Parallel()

	tree := DotChild{
		Name:  "root",
		IsDir: true,
		Children: []DotChild{
			{Name: "a.txt"},
			{Name: "node_modules", IsDir: true, Ignored: true},
		},
	}
	got := countIgnoredTree(tree)
	if got != 2 {
		t.Fatalf("countIgnoredTree = %d, want 2 (file + leaf dir)", got)
	}
}

func TestBuildIgnoredChildTree_IntermediateNotIgnored(t *testing.T) {
	t.Parallel()
	flat := []DotChild{
		{Name: "a", RelPath: "claude/a", Path: "/home/.config/claude/a", Ignored: true, Depth: 2},
		{Name: "b", RelPath: "claude/b", Path: "/home/.config/claude/b", Ignored: true, Depth: 2},
	}
	result := buildIgnoredChildTree(flat, "/home/.config")

	if len(result) != 1 {
		t.Fatalf("expected 1 top-level child (claude dir), got %d", len(result))
	}
	claude := result[0]
	if claude.Name != "claude" {
		t.Errorf("top-level child name = %q, want %q", claude.Name, "claude")
	}
	if !claude.IsDir {
		t.Errorf("intermediate dir 'claude' should have IsDir=true")
	}
	if claude.Ignored {
		t.Errorf("intermediate dir 'claude' should have Ignored=false, got Ignored=true")
	}
	if claude.Counts.Ignored != 2 {
		t.Errorf("intermediate dir 'claude' Counts.Ignored = %d, want 2", claude.Counts.Ignored)
	}
	if claude.FileCount != 2 {
		t.Errorf("intermediate dir 'claude' FileCount = %d, want 2", claude.FileCount)
	}
	if len(claude.Children) != 2 {
		t.Fatalf("expected 2 children under claude, got %d", len(claude.Children))
	}
	for _, leaf := range claude.Children {
		if !leaf.Ignored {
			t.Errorf("leaf %q should have Ignored=true", leaf.Name)
		}
		if leaf.Counts.Ignored != 1 {
			t.Errorf("leaf %q Counts.Ignored = %d, want 1", leaf.Name, leaf.Counts.Ignored)
		}
	}
}

func TestBuildIgnoredChildTree_FlatLeaves(t *testing.T) {
	t.Parallel()
	flat := []DotChild{
		{Name: "foo", RelPath: "foo", Path: "/home/foo", Ignored: true, Depth: 1},
		{Name: "bar", RelPath: "bar", Path: "/home/bar", Ignored: true, Depth: 1},
	}
	result := buildIgnoredChildTree(flat, "/home")

	if len(result) != 2 {
		t.Fatalf("expected 2 top-level children, got %d", len(result))
	}
	for _, child := range result {
		if !child.Ignored {
			t.Errorf("flat leaf %q should have Ignored=true", child.Name)
		}
		if child.IsDir {
			t.Errorf("flat leaf %q should have IsDir=false", child.Name)
		}
	}
}

func TestBuildIgnoredChildTree_DeepTree(t *testing.T) {
	t.Parallel()
	flat := []DotChild{
		{Name: "config.toml", RelPath: "a/b/config.toml", Path: "/root/a/b/config.toml", Ignored: true, Depth: 3},
	}
	result := buildIgnoredChildTree(flat, "/root")

	if len(result) != 1 {
		t.Fatalf("expected 1 top-level dir 'a', got %d", len(result))
	}
	a := result[0]
	if a.Ignored {
		t.Errorf("intermediate 'a' should have Ignored=false")
	}
	if len(a.Children) != 1 {
		t.Fatalf("expected 1 child under 'a', got %d", len(a.Children))
	}
	b := a.Children[0]
	if b.Ignored {
		t.Errorf("intermediate 'b' should have Ignored=false")
	}
	if len(b.Children) != 1 {
		t.Fatalf("expected 1 child under 'b', got %d", len(b.Children))
	}
	leaf := b.Children[0]
	if !leaf.Ignored {
		t.Errorf("leaf 'config.toml' should have Ignored=true")
	}
}

func TestIgnoredChildDotStatuses_KeepsTrackedReincludedChildren(t *testing.T) {
	t.Parallel()
	status := DotStatus{
		Name:       "claude",
		TargetPath: "/home/.config",
		State:      dots.StateSynced,
		IsDir:      true,
		Children: []DotChild{
			{
				Name:    "sub",
				RelPath: "sub",
				Ignored: true,
				IsDir:   true,
				Depth:   1,
				Children: []DotChild{
					{Name: "keep", RelPath: "sub/keep", State: dots.StateSynced, Depth: 2, FileCount: 1, Counts: DotFileCounts{Synced: 1}},
					{Name: "drop", RelPath: "sub/drop", State: dots.StateIgnored, Ignored: true, Depth: 2},
				},
			},
		},
		ignoredChildren: []DotChild{
			{Name: "drop", RelPath: "sub/drop", State: dots.StateIgnored, Ignored: true, Depth: 2},
		},
	}

	out := ignoredChildDotStatuses([]DotStatus{status})
	if len(out) != 1 {
		t.Fatalf("expected 1 ignored-section entry, got %d", len(out))
	}
	tree := out[0].Children
	if len(tree) != 1 || tree[0].Name != "sub" {
		t.Fatalf("expected top container 'sub', got %+v", tree)
	}
	sub := tree[0]
	if sub.Ignored {
		t.Errorf("container 'sub' should be synthesized (Ignored=false)")
	}
	var keep, drop *DotChild
	for i := range sub.Children {
		switch sub.Children[i].Name {
		case "keep":
			keep = &sub.Children[i]
		case "drop":
			drop = &sub.Children[i]
		}
	}
	if keep == nil {
		t.Fatalf("tracked child 'keep' missing from ignored-section tree; children=%+v", sub.Children)
	}
	if keep.Ignored {
		t.Errorf("tracked child 'keep' should have Ignored=false")
	}
	if keep.State != dots.StateSynced {
		t.Errorf("tracked child 'keep' state = %q, want %q", keep.State, dots.StateSynced)
	}
	if keep.Counts.Synced != 1 {
		t.Errorf("tracked child 'keep' Counts.Synced = %d, want 1", keep.Counts.Synced)
	}
	if drop == nil || !drop.Ignored {
		t.Errorf("ignored sibling 'drop' should remain Ignored=true, got %+v", drop)
	}
}

func TestClassifyDotPathState_AllIgnoredDirIsIgnoredNotConflict(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "repo", ".claude", "plugins")
	tgt := filepath.Join(tmp, "home", ".claude", "plugins")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "installed_plugins.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tgt, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tgt, "installed_plugins.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	ignores := append(dots.DefaultIgnores(), "*", "!/plugins", "!/agents/")

	state := classifyDotPathState(src, tgt, ignores, dots.StateConflict)
	if state != dots.StateIgnored {
		t.Fatalf("all-ignored dir state = %s, want %s", state, dots.StateIgnored)
	}
}

func TestClassifyDotPathState_UnignoredContentStillConflicts(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "repo", ".claude", "plugins")
	tgt := filepath.Join(tmp, "home", ".claude", "plugins")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "installed_plugins.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tgt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tgt, "installed_plugins.json"), []byte("{\"local\":1}"), 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(tgt, "installed_plugins.json"), past, past); err != nil {
		t.Fatal(err)
	}

	state := classifyDotPathState(src, tgt, dots.DefaultIgnores(), dots.StateConflict)
	if state != dots.StateConflict {
		t.Fatalf("unignored differing content state = %s, want %s", state, dots.StateConflict)
	}
}

func TestClassifyDotEntry_AllIgnoredRootDirIsIgnoredNotConflict(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "repo", "claude", ".claude")
	tgt := filepath.Join(tmp, "home", ".claude")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "state.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tgt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tgt, "state.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := dots.ResolvedEntry{
		Name:       "claude",
		SourcePath: src,
		TargetPath: tgt,
		Ignore:     append(dots.DefaultIgnores(), "*"),
	}

	state, actions := dots.ClassifyEntry(entry)
	if state != dots.StateIgnored {
		t.Fatalf("all-ignored root dir state = %s, want %s", state, dots.StateIgnored)
	}
	want := []dots.Action{dots.ActionUnignore, dots.ActionRemove}
	if len(actions) != len(want) || actions[0] != want[0] || actions[1] != want[1] {
		t.Fatalf("actions = %v, want %v", actions, want)
	}

	op := dots.LstatEntryOp(entry, false)
	if op.Kind != dots.OpSkip {
		t.Fatalf("dots.LstatEntryOp kind = %v, want %v", op.Kind, dots.OpSkip)
	}
}

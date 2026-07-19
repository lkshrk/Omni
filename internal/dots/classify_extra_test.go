package dots_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/dots"
)

func mkFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func makeNewer(t *testing.T, newer, older string) {
	t.Helper()
	info, err := os.Stat(older)
	if err != nil {
		t.Fatal(err)
	}
	future := info.ModTime().Add(2 * time.Hour)
	if err := os.Chtimes(newer, future, future); err != nil {
		t.Fatal(err)
	}
}

func makeOlder(t *testing.T, older, ref string) {
	t.Helper()
	info, err := os.Stat(ref)
	if err != nil {
		t.Fatal(err)
	}
	past := info.ModTime().Add(-2 * time.Hour)
	if err := os.Chtimes(older, past, past); err != nil {
		t.Fatal(err)
	}
}

func TestClassifyEntry_States(t *testing.T) {
	t.Run("ignored", func(t *testing.T) {
		state, actions := dots.ClassifyEntry(dots.ResolvedEntry{Name: "x", Ignored: true})
		if state != dots.StateIgnored {
			t.Fatalf("state = %q, want ignored", state)
		}
		if len(actions) == 0 {
			t.Fatal("ignored entry should still expose actions")
		}
	})

	t.Run("missing", func(t *testing.T) {
		tmp := t.TempDir()
		src := filepath.Join(tmp, "repo", ".zshrc")
		mkFile(t, src, "repo")
		e := dots.ResolvedEntry{Name: "z", SourcePath: src, TargetPath: filepath.Join(tmp, "home", ".zshrc")}
		if state, _ := dots.ClassifyEntry(e); state != dots.StateMissing {
			t.Fatalf("state = %q, want missing", state)
		}
	})

	t.Run("synced", func(t *testing.T) {
		tmp := t.TempDir()
		src := filepath.Join(tmp, "repo", ".zshrc")
		mkFile(t, src, "repo")
		target := filepath.Join(tmp, "home", ".zshrc")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(src, target); err != nil {
			t.Fatal(err)
		}
		e := dots.ResolvedEntry{Name: "z", SourcePath: src, TargetPath: target}
		if state, _ := dots.ClassifyEntry(e); state != dots.StateSynced {
			t.Fatalf("state = %q, want synced", state)
		}
	})

	t.Run("broken", func(t *testing.T) {
		tmp := t.TempDir()
		src := filepath.Join(tmp, "repo", ".zshrc")
		mkFile(t, src, "repo")
		target := filepath.Join(tmp, "home", ".zshrc")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(tmp, "nowhere"), target); err != nil {
			t.Fatal(err)
		}
		e := dots.ResolvedEntry{Name: "z", SourcePath: src, TargetPath: target}
		if state, _ := dots.ClassifyEntry(e); state != dots.StateBroken {
			t.Fatalf("state = %q, want broken", state)
		}
	})

	t.Run("modified", func(t *testing.T) {
		tmp := t.TempDir()
		src := filepath.Join(tmp, "repo", ".zshrc")
		mkFile(t, src, "repo")
		target := filepath.Join(tmp, "home", ".zshrc")
		mkFile(t, target, "local newer")
		makeNewer(t, target, src)
		e := dots.ResolvedEntry{Name: "z", SourcePath: src, TargetPath: target}
		if state, _ := dots.ClassifyEntry(e); state != dots.StateModified {
			t.Fatalf("state = %q, want modified", state)
		}
	})

	t.Run("conflict on non-newer real file", func(t *testing.T) {
		tmp := t.TempDir()
		src := filepath.Join(tmp, "repo", ".zshrc")
		mkFile(t, src, "repo")
		target := filepath.Join(tmp, "home", ".zshrc")
		mkFile(t, target, "local")
		makeOlder(t, target, src)
		e := dots.ResolvedEntry{Name: "z", SourcePath: src, TargetPath: target}
		state, actions := dots.ClassifyEntry(e)
		if state != dots.StateConflict {
			t.Fatalf("state = %q, want conflict", state)
		}
		var sawUseRepo bool
		for _, a := range actions {
			if a == dots.ActionUseRepo {
				sawUseRepo = true
			}
		}
		if !sawUseRepo {
			t.Fatalf("conflict actions = %v, want use-repo option", actions)
		}
	})

	t.Run("local-only when source missing", func(t *testing.T) {
		tmp := t.TempDir()
		target := filepath.Join(tmp, "home", ".zshrc")
		mkFile(t, target, "local")
		e := dots.ResolvedEntry{Name: "z", SourcePath: filepath.Join(tmp, "repo", ".zshrc"), TargetPath: target}
		if state, _ := dots.ClassifyEntry(e); state != dots.StateLocalOnly {
			t.Fatalf("state = %q, want local-only", state)
		}
	})

	t.Run("no-source when both missing", func(t *testing.T) {
		tmp := t.TempDir()
		e := dots.ResolvedEntry{
			Name:       "z",
			SourcePath: filepath.Join(tmp, "repo", ".zshrc"),
			TargetPath: filepath.Join(tmp, "home", ".zshrc"),
		}
		if state, _ := dots.ClassifyEntry(e); state != dots.StateNoSource {
			t.Fatalf("state = %q, want no-source", state)
		}
	})
}

func TestInspectManagedDotDirectory_SyncedAndModified(t *testing.T) {
	buildDir := func(t *testing.T) (dots.ResolvedEntry, string) {
		t.Helper()
		tmp := t.TempDir()
		src := filepath.Join(tmp, "repo", "app")
		if err := os.MkdirAll(src, 0o755); err != nil {
			t.Fatal(err)
		}
		mkFile(t, filepath.Join(src, "config.toml"), "cfg")
		target := filepath.Join(tmp, "home", "app")
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(src, "config.toml"), filepath.Join(target, "config.toml")); err != nil {
			t.Fatal(err)
		}
		return dots.ResolvedEntry{Name: "app", SourcePath: src, TargetPath: target}, tmp
	}

	t.Run("synced links classify as expected link", func(t *testing.T) {
		e, _ := buildDir(t)
		if got := dots.InspectManagedDotDirectory(e); got != dots.LocalExpectedLink {
			t.Fatalf("kind = %d, want LocalExpectedLink", got)
		}
	})

	t.Run("local-only addition classifies as modified", func(t *testing.T) {
		e, _ := buildDir(t)
		mkFile(t, filepath.Join(e.TargetPath, "extra.toml"), "added locally")
		if got := dots.InspectManagedDotDirectory(e); got != dots.LocalModified {
			t.Fatalf("kind = %d, want LocalModified", got)
		}
	})
}

func TestWalkLocalOnlyDotFiles(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "repo", "app")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	mkFile(t, filepath.Join(src, "config.toml"), "cfg")
	target := filepath.Join(tmp, "home", "app")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(src, "config.toml"), filepath.Join(target, "config.toml")); err != nil {
		t.Fatal(err)
	}
	mkFile(t, filepath.Join(target, "local.toml"), "local only")

	e := dots.ResolvedEntry{Name: "app", SourcePath: src, TargetPath: target}

	var collected []string
	found, err := dots.WalkLocalOnlyDotFiles(e, func(sourcePath, targetPath string) error {
		collected = append(collected, filepath.Base(targetPath))
		return nil
	})
	if err != nil {
		t.Fatalf("WalkLocalOnlyDotFiles: %v", err)
	}
	if !found {
		t.Fatal("expected to find a local-only file")
	}
	if len(collected) != 1 || collected[0] != "local.toml" {
		t.Fatalf("collected = %v, want [local.toml]", collected)
	}
}

func TestWalkLocalOnlyDotFiles_NonDirSourceReturnsFalse(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "repo", ".zshrc")
	mkFile(t, src, "repo")
	target := filepath.Join(tmp, "home", ".zshrc")
	mkFile(t, target, "local")

	found, err := dots.WalkLocalOnlyDotFiles(dots.ResolvedEntry{SourcePath: src, TargetPath: target}, nil)
	if err != nil {
		t.Fatalf("WalkLocalOnlyDotFiles: %v", err)
	}
	if found {
		t.Fatal("file source should report no local-only additions")
	}
}

func TestSameResolvedPath(t *testing.T) {
	tmp := t.TempDir()
	real := filepath.Join(tmp, "real.txt")
	mkFile(t, real, "x")
	link := filepath.Join(tmp, "link.txt")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	if !dots.SameResolvedPath(real, real) {
		t.Fatal("identical clean paths should match")
	}
	if !dots.SameResolvedPath(link, real) {
		t.Fatal("symlink and its target should resolve to the same path")
	}
	if dots.SameResolvedPath(real, filepath.Join(tmp, "other.txt")) {
		t.Fatal("distinct nonexistent paths should not match")
	}
}

func TestIsManagedDotFile(t *testing.T) {
	if !dots.IsManagedDotFile(0) {
		t.Fatal("regular file mode should be managed")
	}
	if !dots.IsManagedDotFile(os.ModeSymlink) {
		t.Fatal("symlink mode should be managed")
	}
	if dots.IsManagedDotFile(os.ModeDir) {
		t.Fatal("directory mode should not be managed")
	}
	if dots.IsManagedDotFile(os.ModeNamedPipe) {
		t.Fatal("named pipe mode should not be managed")
	}
}

func TestIsFoldedDotDirectory(t *testing.T) {
	t.Run("folded directory symlink", func(t *testing.T) {
		tmp := t.TempDir()
		src := filepath.Join(tmp, "repo", "app")
		if err := os.MkdirAll(src, 0o755); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(tmp, "home", "app")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(src, target); err != nil {
			t.Fatal(err)
		}
		if !dots.IsFoldedDotDirectory(dots.ResolvedEntry{SourcePath: src, TargetPath: target}) {
			t.Fatal("directory symlink to source should be a folded directory")
		}
	})

	t.Run("real directory target is not folded", func(t *testing.T) {
		tmp := t.TempDir()
		src := filepath.Join(tmp, "repo", "app")
		if err := os.MkdirAll(src, 0o755); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(tmp, "home", "app")
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		if dots.IsFoldedDotDirectory(dots.ResolvedEntry{SourcePath: src, TargetPath: target}) {
			t.Fatal("real directory target should not be folded")
		}
	})

	t.Run("missing source is not folded", func(t *testing.T) {
		tmp := t.TempDir()
		if dots.IsFoldedDotDirectory(dots.ResolvedEntry{
			SourcePath: filepath.Join(tmp, "repo", "app"),
			TargetPath: filepath.Join(tmp, "home", "app"),
		}) {
			t.Fatal("missing source should not be folded")
		}
	})
}

func TestLstatEntryOp(t *testing.T) {
	t.Run("expected link", func(t *testing.T) {
		tmp := t.TempDir()
		src := filepath.Join(tmp, "repo", ".zshrc")
		mkFile(t, src, "repo")
		target := filepath.Join(tmp, "home", ".zshrc")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(src, target); err != nil {
			t.Fatal(err)
		}
		e := dots.ResolvedEntry{Name: "z", SourcePath: src, TargetPath: target}
		if op := dots.LstatEntryOp(e, false); op.Kind != dots.OpLink {
			t.Fatalf("op = %v, want OpLink", op.Kind)
		}
		if op := dots.LstatEntryOp(e, true); op.Kind != dots.OpSkip {
			t.Fatalf("dry-run op = %v, want OpSkip", op.Kind)
		}
	})

	t.Run("missing target", func(t *testing.T) {
		tmp := t.TempDir()
		src := filepath.Join(tmp, "repo", ".zshrc")
		mkFile(t, src, "repo")
		e := dots.ResolvedEntry{Name: "z", SourcePath: src, TargetPath: filepath.Join(tmp, "home", ".zshrc")}
		if op := dots.LstatEntryOp(e, true); op.Kind != dots.OpDryLink {
			t.Fatalf("dry-run op = %v, want OpDryLink", op.Kind)
		}
		op := dots.LstatEntryOp(e, false)
		if op.Kind != dots.OpConflict || op.Err == nil {
			t.Fatalf("op = %v err = %v, want OpConflict with error", op.Kind, op.Err)
		}
	})

	t.Run("real file conflict", func(t *testing.T) {
		tmp := t.TempDir()
		src := filepath.Join(tmp, "repo", ".zshrc")
		mkFile(t, src, "repo")
		target := filepath.Join(tmp, "home", ".zshrc")
		mkFile(t, target, "local")
		makeOlder(t, target, src)
		op := dots.LstatEntryOp(dots.ResolvedEntry{Name: "z", SourcePath: src, TargetPath: target}, false)
		if op.Kind != dots.OpConflict || op.Err == nil {
			t.Fatalf("op = %v err = %v, want OpConflict with error", op.Kind, op.Err)
		}
	})
}

func TestIgnoredDotDirHasIncludedDescendant(t *testing.T) {
	ignores := []string{"config", "!config/keep"}
	if !dots.IgnoredDotDirHasIncludedDescendant("root", "config", ignores) {
		t.Fatal("negated descendant should be reported as included")
	}
	if dots.IgnoredDotDirHasIncludedDescendant("root", "other", ignores) {
		t.Fatal("unrelated dir should have no included descendant")
	}
}

func TestSelfHealDotEntryLinkShape_RewritesAbsoluteLinkToRelative(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "repo", ".zshrc")
	mkFile(t, src, "repo")
	target := filepath.Join(tmp, "home", ".zshrc")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(src, target); err != nil {
		t.Fatal(err)
	}

	e := dots.ResolvedEntry{Name: "z", SourcePath: src, TargetPath: target}
	if err := dots.SelfHealDotEntryLinkShape(e); err != nil {
		t.Fatalf("SelfHealDotEntryLinkShape: %v", err)
	}

	got, err := os.Readlink(target)
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	want, err := dots.StowRelativeSymlinkTarget(target, src)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("healed link = %q, want stow-relative %q", got, want)
	}
	if filepath.IsAbs(got) {
		t.Fatal("healed link should be relative, not absolute")
	}
	// Link must still resolve to the source content.
	body, err := os.ReadFile(target)
	if err != nil || string(body) != "repo" {
		t.Fatalf("healed link content = %q err = %v, want repo", body, err)
	}
}

func TestConflictIsManagedStowLink(t *testing.T) {
	t.Run("wrong link within stow root is managed", func(t *testing.T) {
		tmp := t.TempDir()
		stow := filepath.Join(tmp, "repo")
		src := filepath.Join(stow, "pkg", ".zshrc")
		mkFile(t, src, "repo")
		other := filepath.Join(stow, "pkg", ".other")
		mkFile(t, other, "other")
		target := filepath.Join(tmp, "home", ".zshrc")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(other, target); err != nil {
			t.Fatal(err)
		}
		e := dots.ResolvedEntry{Name: "z", SourcePath: src, TargetPath: target}
		if !dots.ConflictIsManagedStowLink(e, stow) {
			t.Fatal("wrong link pointing inside the stow root should be managed")
		}
	})

	t.Run("link outside stow root is not managed", func(t *testing.T) {
		tmp := t.TempDir()
		stow := filepath.Join(tmp, "repo")
		src := filepath.Join(stow, "pkg", ".zshrc")
		mkFile(t, src, "repo")
		outside := filepath.Join(tmp, "elsewhere.txt")
		mkFile(t, outside, "outside")
		target := filepath.Join(tmp, "home", ".zshrc")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, target); err != nil {
			t.Fatal(err)
		}
		e := dots.ResolvedEntry{Name: "z", SourcePath: src, TargetPath: target}
		if dots.ConflictIsManagedStowLink(e, stow) {
			t.Fatal("link pointing outside the stow root should not be managed")
		}
	})
}

func TestSelfHealDotEntryLinkShape_LeavesForeignLinkUntouched(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "repo", ".zshrc")
	mkFile(t, src, "repo")
	other := filepath.Join(tmp, "other.txt")
	mkFile(t, other, "other")
	target := filepath.Join(tmp, "home", ".zshrc")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, target); err != nil {
		t.Fatal(err)
	}

	if err := dots.SelfHealDotEntryLinkShape(dots.ResolvedEntry{Name: "z", SourcePath: src, TargetPath: target}); err != nil {
		t.Fatalf("SelfHealDotEntryLinkShape: %v", err)
	}
	got, err := os.Readlink(target)
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	if got != other {
		t.Fatalf("foreign link changed to %q, want %q", got, other)
	}
}

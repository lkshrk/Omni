package securefile

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	_ "github.com/lkshrk/omni/internal/testguard"
)

func TestRootPrivateAtomicAndContained(t *testing.T) {
	root, err := NewRoot(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	child, err := root.Child("0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	if err := child.WriteFileAtomic("journal.json", []byte("secret")); err != nil {
		t.Fatal(err)
	}
	if err := child.Verify("journal.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := child.resolve("../escape", false); err == nil {
		t.Fatal("traversal accepted")
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(filepath.Join(child.Path(), "journal.json"))
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", info.Mode())
		}
	}
}

func TestRootRejectsSymlinkAncestor(t *testing.T) {
	base := t.TempDir()
	root, err := NewRoot(filepath.Join(base, "state"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root.Path(), "link")); err != nil {
		t.Skip(err)
	}
	if err := root.WriteFileAtomic(filepath.Join("link", "secret"), []byte("x")); err == nil {
		t.Fatal("symlink ancestor accepted")
	}
}

func TestRootCanonicalizesExistingSymlinkedParent(t *testing.T) {
	base := t.TempDir()
	realParent := filepath.Join(base, "real")
	if err := os.Mkdir(realParent, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(realParent, link); err != nil {
		t.Skip(err)
	}
	root, err := NewRoot(filepath.Join(link, "state"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(root.Path()) != realParent {
		t.Fatalf("canonical root=%s want parent %s", root.Path(), realParent)
	}
	if err := root.WriteFileAtomic("journal", []byte("ok")); err != nil {
		t.Fatal(err)
	}
}

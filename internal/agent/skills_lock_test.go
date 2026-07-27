package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/config"
)

func TestStoreLockSerializesSeparateInstances(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dataDir := filepath.Join(t.TempDir(), "data")
	registry, err := newRegistry([]Target{{ID: "test", configDir: ".test"}})
	if err != nil {
		t.Fatal(err)
	}
	first := NewSkills(registry, home, dataDir, nil, nil, "")
	second := NewSkills(registry, home, dataDir, nil, nil, "")

	held, err := first.lockStore()
	if err != nil {
		t.Fatalf("acquiring the store lock: %v", err)
	}
	acquired := make(chan *storeLock, 1)
	go func() {
		lock, err := second.lockStore()
		if err != nil {
			acquired <- nil
			return
		}
		acquired <- lock
	}()

	select {
	case <-acquired:
		t.Fatal("second instance acquired the store lock while it was held")
	case <-time.After(200 * time.Millisecond):
	}

	held.release()
	select {
	case lock := <-acquired:
		if lock == nil {
			t.Fatal("second instance failed to acquire the released store lock")
		}
		lock.release()
	case <-time.After(10 * time.Second):
		t.Fatal("second instance never acquired the released store lock")
	}
}

// The fixers rewrite target skill dirs that exist without the store, so an absent store must not hand them an unlocked window against a concurrent install.
func TestStoreFixersLockWhenTheStoreIsAbsent(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dataDir := filepath.Join(t.TempDir(), "data")
	registry, err := newRegistry([]Target{{ID: "test", configDir: ".test"}})
	if err != nil {
		t.Fatal(err)
	}
	fixer := NewSkills(registry, home, dataDir, nil, nil, "")
	installer := NewSkills(registry, home, dataDir, nil, nil, "")

	if lock, err := fixer.lockStoreForWrite(true); err != nil || lock != nil {
		t.Fatalf("dry run lock = (%v, %v), want no lock", lock, err)
	}
	held, err := fixer.lockStoreForWrite(false)
	if err != nil {
		t.Fatalf("locking for a fix without a store: %v", err)
	}
	if held == nil {
		t.Fatal("fixer ran unlocked because the store dir did not exist yet")
	}

	acquired := make(chan *storeLock, 1)
	go func() {
		lock, err := installer.lockStore()
		if err != nil {
			acquired <- nil
			return
		}
		acquired <- lock
	}()
	select {
	case <-acquired:
		t.Fatal("an install acquired the store lock while a fixer held it")
	case <-time.After(200 * time.Millisecond):
	}

	held.release()
	select {
	case lock := <-acquired:
		if lock == nil {
			t.Fatal("the install failed to acquire the released store lock")
		}
		lock.release()
	case <-time.After(10 * time.Second):
		t.Fatal("the install never acquired the released store lock")
	}
}

// Install takes the same lock before it touches a target dir, so a store that cannot be created cannot be racing; a fixer must degrade rather than refuse to run.
func TestStoreFixersRunUnlockedWhenTheStoreCannotBeCreated(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	registry, err := newRegistry([]Target{{ID: "test", configDir: ".test"}})
	if err != nil {
		t.Fatal(err)
	}
	fixer := NewSkills(registry, home, filepath.Join(blocker, "data"), nil, nil, "")

	lock, err := fixer.lockStoreForWrite(false)
	if err != nil {
		t.Fatalf("locking for a fix against an uncreatable store: %v", err)
	}
	if lock != nil {
		t.Fatal("a lock was taken in a store that cannot exist")
	}
}

func TestSkillsConcurrentInstallRemoveKeepsStoreConsistent(t *testing.T) {
	home := t.TempDir()
	source := t.TempDir()
	writeTestSkill(t, filepath.Join(source, "skills", "shared"), "shared", "raced skill")
	registry, err := newRegistry([]Target{{ID: "test", configDir: ".test"}})
	if err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(t.TempDir(), "data")
	installer := NewSkills(registry, home, dataDir, nil, nil, "")
	refresher := NewSkills(registry, home, dataDir, nil, nil, "")
	remover := NewSkills(registry, home, dataDir, nil, nil, "")
	fixer := NewSkills(registry, home, dataDir, nil, nil, "")
	pkg := config.SkillPackage{Source: source}

	const rounds = 30
	var wait sync.WaitGroup
	errs := make(chan error, 4*rounds)
	race := func(round func() error) {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range rounds {
				if err := round(); err != nil {
					errs <- err
				}
			}
		}()
	}
	race(func() error {
		_, err := installer.Install(context.Background(), pkg, []string{"test"})
		return err
	})
	race(func() error {
		_, err := refresher.Refresh(context.Background(), pkg, []string{"test"})
		return err
	})
	race(func() error {
		_, err := remover.Remove(context.Background(), source, []string{"test"})
		if err != nil && strings.Contains(err.Error(), "is not installed") {
			return nil
		}
		return err
	})
	race(func() error {
		if _, err := fixer.PruneStoreDebris(false); err != nil {
			return err
		}
		_, err := fixer.PruneDanglingLinks(false)
		return err
	})
	wait.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("racing store operation: %v", err)
	}

	if _, err := installer.Install(context.Background(), pkg, []string{"test"}); err != nil {
		t.Fatalf("settling install after the race: %v", err)
	}
	link := filepath.Join(home, ".test", "skills", "shared")
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Fatalf("resolving the installed link: %v", err)
	}
	if _, err := os.Stat(filepath.Join(resolved, "SKILL.md")); err != nil {
		t.Fatalf("installed SKILL.md: %v", err)
	}

	debris, err := installer.PruneStoreDebris(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(debris) != 0 {
		t.Fatalf("store debris = %v, want none", debris)
	}
	dangling, err := installer.PruneDanglingLinks(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(dangling) != 0 {
		t.Fatalf("dangling links = %v, want none", dangling)
	}
}

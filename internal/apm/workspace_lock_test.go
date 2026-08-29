package apm

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/executor"
	_ "github.com/lkshrk/omni/internal/testguard"
)

func TestAcquireWorkspaceLockAdmitsOneHolderAtATime(t *testing.T) {
	workspace := t.TempDir()
	t.Cleanup(func() { _ = os.Remove(workspaceLockPath(workspace)) })

	var holders, overlaps atomic.Int32
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lock, err := acquireWorkspaceLock(t.Context(), workspace)
			if err != nil {
				t.Errorf("acquire workspace lock: %v", err)
				return
			}
			defer func() { _ = lock.Close() }()
			if holders.Add(1) != 1 {
				overlaps.Add(1)
			}
			time.Sleep(30 * time.Millisecond)
			holders.Add(-1)
		}()
	}
	wg.Wait()

	if got := overlaps.Load(); got != 0 {
		t.Fatalf("workspace lock admitted concurrent holders %d times", got)
	}
}

func TestAcquireWorkspaceLockAbortsWaitOnCanceledContext(t *testing.T) {
	workspace := t.TempDir()
	t.Cleanup(func() { _ = os.Remove(workspaceLockPath(workspace)) })

	held, err := acquireWorkspaceLock(t.Context(), workspace)
	if err != nil {
		t.Fatalf("acquire workspace lock: %v", err)
	}
	defer func() { _ = held.Close() }()

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := acquireWorkspaceLock(ctx, workspace); err == nil {
		t.Fatal("second acquire succeeded while the lock was held")
	}
}

func TestWorkspaceLockPathStaysOutsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	path := workspaceLockPath(workspace)

	if filepath.Dir(path) != filepath.Clean(os.TempDir()) {
		t.Fatalf("lock path %q is not in the temp dir", path)
	}
	if path != workspaceLockPath(workspace) {
		t.Fatal("lock path is not stable for the same workspace")
	}
	if path == workspaceLockPath(workspace+"-other") {
		t.Fatal("distinct workspaces share a lock path")
	}
}

func TestWithGlobalWorkspaceLockRejectsNestedCallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workspace := filepath.Join(home, ".apm")
	t.Cleanup(func() { _ = os.Remove(workspaceLockPath(workspace)) })

	err := WithGlobalWorkspaceLock(t.Context(), func(ctx context.Context) error {
		return WithGlobalWorkspaceLock(ctx, func(context.Context) error { return nil })
	})
	if err == nil {
		t.Fatal("nested workspace lock callback succeeded")
	}
}

func TestWithGlobalWorkspaceLockAcceptsNilContext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	called := false
	if err := WithGlobalWorkspaceLock(nil, func(ctx context.Context) error { //nolint:staticcheck // Regression: the public lock boundary intentionally accepts nil.
		called = ctx != nil
		return nil
	}); err != nil || !called {
		t.Fatalf("called = %v, err = %v", called, err)
	}
}

func TestClientRunUsesHeldGlobalWorkspaceLock(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workspace := filepath.Join(home, ".apm")
	t.Cleanup(func() { _ = os.Remove(workspaceLockPath(workspace)) })
	mock := &executor.MockExecutor{Responses: []executor.MockCall{{Stdout: "ok\n"}}}

	err := WithGlobalWorkspaceLock(t.Context(), func(ctx context.Context) error {
		result, err := New(mock, Global).Run(ctx, "install", "-g")
		if err != nil || result.Stdout != "ok\n" {
			t.Fatalf("result = %#v, err = %v", result, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(mock.Calls) != 1 || mock.Calls[0].Dir != workspace {
		t.Fatalf("calls = %#v", mock.Calls)
	}
}

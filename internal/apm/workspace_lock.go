package apm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/lkshrk/omni/internal/flock"
)

const workspaceLockPoll = 20 * time.Millisecond

type workspaceLock struct {
	file *os.File
}

type workspaceLockContextKey struct{}

// WithGlobalWorkspaceLock serializes Omni-mediated access to APM's global workspace.
// The callback context marks the held lock so Client.Run can use it without reacquiring.
func WithGlobalWorkspaceLock(ctx context.Context, fn func(context.Context) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Value(workspaceLockContextKey{}) != nil {
		return errors.New("APM global workspace lock callback is not reentrant")
	}
	workspace, err := ensureGlobalWorkspaceDir()
	if err != nil {
		return err
	}
	lock, err := acquireWorkspaceLock(ctx, workspace)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	return fn(context.WithValue(ctx, workspaceLockContextKey{}, workspace))
}

func globalWorkspaceLockHeld(ctx context.Context, workspace string) bool {
	held, _ := ctx.Value(workspaceLockContextKey{}).(string)
	return held == workspace
}

// acquireWorkspaceLock serializes global-workspace APM runs across every omni process on the
// host; APM itself does not serialize its own lifecycle (microsoft/apm#2655).
func acquireWorkspaceLock(ctx context.Context, workspace string) (*workspaceLock, error) {
	f, err := os.OpenFile(workspaceLockPath(workspace), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open APM workspace lock: %w", err)
	}
	for {
		locked, err := flock.TryLock(f)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("lock APM workspace: %w", err), f.Close())
		}
		if locked {
			return &workspaceLock{file: f}, nil
		}
		select {
		case <-ctx.Done():
			return nil, errors.Join(ctx.Err(), f.Close())
		case <-time.After(workspaceLockPoll):
		}
	}
}

// The lock lives outside the workspace because APM owns that directory and may not have created it yet.
func workspaceLockPath(workspace string) string {
	sum := sha256.Sum256([]byte(workspace))
	return filepath.Join(os.TempDir(), "omni-apm-"+hex.EncodeToString(sum[:4])+".lock")
}

func (l *workspaceLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := flock.Unlock(l.file)
	closeErr := l.file.Close()
	l.file = nil
	return errors.Join(err, closeErr)
}

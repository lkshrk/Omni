//go:build integration

package integration_test

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestTUIWaitOwnerSurvivesTimeoutCleanup(t *testing.T) {
	wantErr := errors.New("process stopped")
	release := make(chan struct{})
	var calls atomic.Int32
	done := startTUIWait(func() error {
		calls.Add(1)
		<-release
		return wantErr
	})

	if _, ok := awaitTUIWait(done, time.Nanosecond); ok {
		t.Fatal("wait unexpectedly completed before cleanup")
	}
	close(release)
	err, ok := awaitTUIWait(done, time.Second)
	if !ok {
		t.Fatal("cleanup did not receive the waiter result")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("wait error = %v, want %v", err, wantErr)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("Wait called %d times, want 1", got)
	}
}

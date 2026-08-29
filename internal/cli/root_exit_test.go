package cli

import (
	"fmt"
	"testing"
)

type testExitError int

func (e testExitError) Error() string { return fmt.Sprintf("exit %d", e) }
func (e testExitError) ExitCode() int { return int(e) }

func TestCommandExitCodePreservesWrappedChildStatus(t *testing.T) {
	if got := commandExitCode(fmt.Errorf("apm failed: %w", testExitError(7))); got != 7 {
		t.Fatalf("exit code = %d, want 7", got)
	}
	if got := commandExitCode(fmt.Errorf("plain failure")); got != 1 {
		t.Fatalf("plain exit code = %d, want 1", got)
	}
}

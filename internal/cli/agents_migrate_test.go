package cli

import (
	"strings"
	"testing"
)

func TestAgentsMigrateWriteAndDryRunAreMutuallyExclusive(t *testing.T) {
	cmd := newAgentsMigrateCmd(&rootState{})
	cmd.SetArgs([]string{"--host", "h", "--dry-run", "--write"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "[dry-run write]") {
		t.Fatalf("error = %v", err)
	}
}

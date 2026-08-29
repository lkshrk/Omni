package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/apm"
	"github.com/lkshrk/omni/internal/executor"
)

func setupOwnedSync(t *testing.T, template string) (*App, *executor.MockExecutor, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	writeFile(t, filepath.Join(home, ".config", "omni", "apm.yml"), template)
	mock := &executor.MockExecutor{}
	a := New(filepath.Join(home, "settings.json"))
	a.StateDir = t.TempDir()
	a.SetFallbackExecutor(mock)
	return a, mock, home
}

func writeOwnedSyncModule(t *testing.T, home, source, manifest string) {
	t.Helper()
	writeFile(t, filepath.Join(home, ".apm", "apm_modules", filepath.FromSlash(source), "apm.yml"), manifest)
	lockPath := filepath.Join(home, ".apm", "apm.lock.yaml")
	lock, err := os.ReadFile(lockPath)
	if os.IsNotExist(err) {
		lock = []byte("dependencies:\n")
	} else if err != nil {
		t.Fatal(err)
	}
	lock = append(lock, []byte("- repo_url: "+source+"\n  name: "+filepath.Base(source)+"\n  version: 1.0.0\n")...)
	writeFile(t, lockPath, string(lock))
}

func ownedSyncManifest(standalone string, packages ...string) string {
	manifest := "name: host\ndependencies:\n  apm:\n"
	for _, pkg := range packages {
		manifest += "  - git: " + pkg + "\n"
	}
	return manifest + standalone
}

func TestAgentsSyncAllBlocksOwnedExactDuplicateBeforeMutation(t *testing.T) {
	template := ownedSyncManifest("  mcp:\n  - name: shared\n    transport: stdio\n    command: echo\n", "acme/bundle")
	a, mock, home := setupOwnedSync(t, template)
	writeOwnedSyncModule(t, home, "acme/bundle", "name: bundle\ndependencies:\n  mcp:\n  - name: shared\n    transport: stdio\n    command: echo\n")
	live := filepath.Join(home, ".apm", "apm.yml")

	_, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{})
	if err == nil || !strings.Contains(err.Error(), "omni doctor --fix") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(live); !os.IsNotExist(statErr) {
		t.Fatalf("live manifest was materialized: %v", statErr)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("apm was invoked: %#v", mock.Calls)
	}
}

func TestAgentsSyncAllRejectsInvalidUnmarkedTemplateBeforeMutation(t *testing.T) {
	const secret = "TOP_SECRET_INVALID_YAML"
	a, mock, home := setupOwnedSync(t, "dependencies: "+secret+"\n")
	live := filepath.Join(home, ".apm", "apm.yml")

	_, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{})
	if err == nil || err.Error() != "invalid APM manifest" || strings.Contains(err.Error(), secret) {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(live); !os.IsNotExist(statErr) {
		t.Fatalf("live manifest was materialized: %v", statErr)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("apm was invoked: %#v", mock.Calls)
	}
}

func TestAgentsSyncMaterializationRejectsTemplateEditAfterPreflight(t *testing.T) {
	a, mock, home := setupOwnedSync(t, "name: validated\n")
	dir := filepath.Join(home, ".apm")
	candidatePath, candidate, err := agentsSyncCandidate(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, verify, err := checkAgentsOwnershipPreflight(dir, a.StateDir, candidatePath, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if err := verify(); err != nil {
		t.Fatal(err)
	}

	edited := make(chan error, 1)
	go func() {
		edited <- os.WriteFile(candidatePath, []byte("name: attacker\n"), 0o644)
	}()
	if err := <-edited; err != nil {
		t.Fatal(err)
	}
	_, _, err = materializeAgentsTemplateCandidate(dir, a.StateDir, false, candidatePath, candidate)
	if err == nil || !strings.Contains(err.Error(), "template changed during ownership preflight") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "apm.yml")); !os.IsNotExist(statErr) {
		t.Fatalf("live manifest was materialized: %v", statErr)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("apm was invoked: %#v", mock.Calls)
	}
}

func TestAgentsSyncAllRedactsOwnedConflictValues(t *testing.T) {
	const secret = "TOP_SECRET_STANDALONE"
	template := ownedSyncManifest("  mcp:\n  - name: shared\n    transport: stdio\n    command: echo\n    env:\n      TOKEN: "+secret+"\n", "acme/bundle")
	a, mock, home := setupOwnedSync(t, template)
	writeOwnedSyncModule(t, home, "acme/bundle", "name: bundle\ndependencies:\n  mcp:\n  - name: shared\n    transport: stdio\n    command: echo\n    env:\n      TOKEN: OTHER_SECRET\n")

	_, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{})
	if err == nil || !strings.Contains(err.Error(), `MCP "shared"`) || !strings.Contains(err.Error(), `package "bundle"`) || !strings.Contains(err.Error(), "env differ") || strings.Contains(err.Error(), secret) {
		t.Fatalf("err = %v", err)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("apm was invoked: %#v", mock.Calls)
	}
}

func TestAgentsSyncAllBlocksMultipleOwnedChildOwners(t *testing.T) {
	a, mock, home := setupOwnedSync(t, ownedSyncManifest("", "acme/one", "acme/two"))
	module := "dependencies:\n  lsp:\n  - name: shared\n    command: lsp\n"
	writeOwnedSyncModule(t, home, "acme/one", "name: one\n"+module)
	writeOwnedSyncModule(t, home, "acme/two", "name: two\n"+module)

	_, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{})
	if err == nil || !strings.Contains(err.Error(), `LSP "shared" has multiple package owners: one, two`) {
		t.Fatalf("err = %v", err)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("apm was invoked: %#v", mock.Calls)
	}
}

func TestAgentsSyncAllBlocksConflictingDefinitionsFromOneOwner(t *testing.T) {
	a, mock, home := setupOwnedSync(t, ownedSyncManifest("", "acme/bundle"))
	writeOwnedSyncModule(t, home, "acme/bundle", "name: bundle\ndependencies:\n  mcp:\n  - name: shared\n    command: first\n  - name: shared\n    command: second\n")

	_, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{})
	if err == nil || !strings.Contains(err.Error(), `MCP "shared" has conflicting definitions in package "bundle"`) {
		t.Fatalf("err = %v", err)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("apm was invoked: %#v", mock.Calls)
	}
}

func TestAgentsSyncAllFirstInstallBlocksStandaloneWhenPackageEvidenceUnavailable(t *testing.T) {
	template := ownedSyncManifest("  mcp:\n  - name: standalone\n    transport: stdio\n    command: echo\n", "acme/missing")
	for _, dryRun := range []bool{false, true} {
		t.Run(map[bool]string{false: "install", true: "dry-run"}[dryRun], func(t *testing.T) {
			a, mock, _ := setupOwnedSync(t, template)
			_, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{DryRun: dryRun})
			if err == nil || !strings.Contains(err.Error(), "cannot verify package-owned MCP/LSP declarations") {
				t.Fatalf("err = %v", err)
			}
			if len(mock.Calls) != 0 {
				t.Fatalf("apm was invoked: %#v", mock.Calls)
			}
		})
	}
}

func TestAgentsSyncAllFirstInstallAllowsPackageOnlyManifest(t *testing.T) {
	a, mock, _ := setupOwnedSync(t, ownedSyncManifest("", "acme/missing"))
	mock.Responses = []executor.MockCall{
		{Stdout: "APM CLI version " + apmVersionPin + "\n"},
		{Stdout: "installed\n"},
	}

	result, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{})
	if err != nil || result.Output != "installed\n" {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	if len(mock.Calls) != 2 {
		t.Fatalf("calls = %#v", mock.Calls)
	}
}

func TestAgentsSyncAllHoldsGlobalWorkspaceLockThroughInstall(t *testing.T) {
	state := t.TempDir()
	wrapper := writeMigrationSyncWrapper(t, state, "owner", "")
	a, _ := setupMigrationSyncGuard(t, state, migrationGuardManifest(wrapper, ""))
	exec := &blockingMigrationSyncExecutor{
		MockExecutor: &executor.MockExecutor{Responses: []executor.MockCall{
			{Stdout: "APM CLI version " + apmVersionPin + "\n"},
			{Stdout: "installed\n"},
		}},
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	a.SetFallbackExecutor(exec)
	done := make(chan error, 1)
	go func() {
		_, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{})
		done <- err
	}()
	<-exec.started
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	acquired := false
	err := apm.WithGlobalWorkspaceLock(ctx, func(context.Context) error {
		acquired = true
		return nil
	})
	close(exec.release)
	if syncErr := <-done; syncErr != nil {
		t.Fatal(syncErr)
	}
	if err == nil || acquired {
		t.Fatalf("workspace lock acquired during sync: acquired=%v err=%v", acquired, err)
	}
}

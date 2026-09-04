package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
)

func migrationWriteFixture(t *testing.T) string {
	t.Helper()
	snapshot := t.TempDir()
	mustWriteBundleFile(t, filepath.Join(snapshot, "omni-config-000.json"), `{
  "agents": {"mcp_servers": [{"name":"independent","transport":"stdio","command":"independent-mcp"}]},
  "groups": [{"name":"g","mcp_servers":["independent"]}],
  "hosts": {"h":["g"]}
}`)
	mustWriteBundleFile(t, filepath.Join(snapshot, "paths.json"), `{"omni-config-000.json":"/tmp/settings.json"}`)
	return snapshot
}

func migrationWriteApp(t *testing.T) (*App, string, string, *executor.MockExecutor) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	template, err := AgentsTemplatePath()
	if err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(home, ".apm", "apm.yml")
	if err := os.MkdirAll(filepath.Dir(live), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(live, []byte("live: unchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mock := &executor.MockExecutor{}
	a := &App{StateDir: filepath.Join(t.TempDir(), "state"), fallbackExec: mock}
	return a, template, live, mock
}

func TestAgentsMigrateWriteCreatesOnlyMarkedTemplate(t *testing.T) {
	a, template, live, mock := migrationWriteApp(t)
	snapshot := migrationWriteFixture(t)
	preview, err := a.AgentsMigrate(t.Context(), "h", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(preview, agentsMigrationMarker+"\n") {
		t.Fatalf("preview = %q", preview)
	}
	if _, err := os.Stat(template); !os.IsNotExist(err) {
		t.Fatalf("preview wrote template: %v", err)
	}
	out, err := a.AgentsMigrateWrite(t.Context(), "h", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(template)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != out || !strings.HasPrefix(string(got), agentsMigrationMarker+"\n") {
		t.Fatalf("template = %q, output = %q", got, out)
	}
	if got, _ := os.ReadFile(live); string(got) != "live: unchanged\n" {
		t.Fatalf("live manifest changed: %q", got)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("migrate ran subprocesses: %+v", mock.Calls)
	}
}

func TestAgentsMigrateWriteRefusesUnmarkedTemplate(t *testing.T) {
	a, template, live, _ := migrationWriteApp(t)
	if err := os.MkdirAll(filepath.Dir(template), 0o755); err != nil {
		t.Fatal(err)
	}
	old := []byte("name: hand-written\n")
	if err := os.WriteFile(template, old, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.AgentsMigrateWrite(t.Context(), "h", migrationWriteFixture(t)); err == nil || !strings.Contains(err.Error(), "not migration-owned") {
		t.Fatalf("unmarked template error = %v", err)
	}
	if got, _ := os.ReadFile(template); string(got) != string(old) {
		t.Fatalf("unmarked template changed: %q", got)
	}
	if got, _ := os.ReadFile(live); string(got) != "live: unchanged\n" {
		t.Fatalf("live manifest changed: %q", got)
	}
}

func TestAgentsMigrateWriteRegeneratesMarkedTemplate(t *testing.T) {
	a, template, _, _ := migrationWriteApp(t)
	if err := os.MkdirAll(filepath.Dir(template), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(template, []byte("\n"+agentsMigrationMarker+"\nold: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := a.AgentsMigrateWrite(t.Context(), "h", migrationWriteFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(template); string(got) != out || strings.Contains(string(got), "old: true") {
		t.Fatalf("marked template was not regenerated: %q", got)
	}
}

func TestAgentsMigrateTemplateWriteFailurePreservesPreviousBytes(t *testing.T) {
	a, template, _, _ := migrationWriteApp(t)
	if err := os.MkdirAll(filepath.Dir(template), 0o755); err != nil {
		t.Fatal(err)
	}
	old := []byte(agentsMigrationMarker + "\nold: true\n")
	if err := os.WriteFile(template, old, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := a.agentsMigrate(t.Context(), "h", migrationWriteFixture(t), true, func(string, []byte) (string, error) {
		return "", errors.New("injected template write failure")
	})
	if err == nil || !strings.Contains(err.Error(), "injected template write failure") {
		t.Fatalf("write error = %v", err)
	}
	if got, _ := os.ReadFile(template); string(got) != string(old) {
		t.Fatalf("failed write changed template: %q", got)
	}
}

func TestAgentsMigrateWriteRejectsTemplateSymlink(t *testing.T) {
	a, template, _, _ := migrationWriteApp(t)
	if err := os.MkdirAll(filepath.Dir(template), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "apm.yml")
	if err := os.WriteFile(target, []byte(agentsMigrationMarker+"\nold: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, template); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := a.AgentsMigrateWrite(t.Context(), "h", migrationWriteFixture(t)); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink error = %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != agentsMigrationMarker+"\nold: true\n" {
		t.Fatalf("symlink target changed: %q", got)
	}
}

func TestCommitAgentMigrationRejectsTemplateChangedBeforeLock(t *testing.T) {
	_, template, _, _ := migrationWriteApp(t)
	if err := os.MkdirAll(filepath.Dir(template), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(template, []byte(agentsMigrationMarker+"\nold: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	identity, err := inspectAgentsMigrationTemplate(template)
	if err != nil {
		t.Fatal(err)
	}
	changed := []byte(agentsMigrationMarker + "\nexternal: true\n")
	if err := os.WriteFile(template, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	called := false
	_, err = commitAgentMigration(template, t.TempDir(), agentBundlePlan{}, nil, identity, "new", func(string, []byte) (string, error) {
		called = true
		return "", nil
	})
	if err == nil || !strings.Contains(err.Error(), "changed during migration") || called {
		t.Fatalf("commit error = %v, writer called = %v", err, called)
	}
	if got, _ := os.ReadFile(template); string(got) != string(changed) {
		t.Fatalf("external change overwritten: %q", got)
	}
}

func TestCommitAgentMigrationSerializesPublishTemplateAndCleanup(t *testing.T) {
	_, template, _, _ := migrationWriteApp(t)
	if err := os.MkdirAll(filepath.Dir(template), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(template, []byte(agentsMigrationMarker+"\nold: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	identity, err := inspectAgentsMigrationTemplate(template)
	if err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	plans := []agentBundlePlan{
		{Wrappers: []agentBundleWrapper{testMigrationWrapper(state, "a", "one")}},
		{Wrappers: []agentBundleWrapper{testMigrationWrapper(state, "b", "two")}},
	}
	prepared := make([][]preparedAgentBundleWrapper, len(plans))
	for i := range plans {
		prepared[i], err = prepareAgentBundleWrappers(plans[i])
		if err != nil {
			t.Fatal(err)
		}
		defer discardPreparedAgentBundleWrappers(prepared[i])
	}

	type result struct {
		index int
		err   error
	}
	results := make(chan result, len(plans))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range plans {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := commitAgentMigration(template, state, plans[i], prepared[i], identity, agentsMigrationMarker+"\nplan: "+string(rune('1'+i))+"\n", writeAgentsMigrationTemplate)
			results <- result{i, err}
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	winner := -1
	for result := range results {
		if result.err == nil {
			if winner != -1 {
				t.Fatal("both concurrent commits succeeded")
			}
			winner = result.index
		} else if !strings.Contains(result.err.Error(), "changed during migration") {
			t.Fatalf("loser error = %v", result.err)
		}
	}
	if winner == -1 {
		t.Fatal("no concurrent commit succeeded")
	}
	if _, err := os.Stat(plans[winner].Wrappers[0].Path); err != nil {
		t.Fatalf("winning referenced wrapper missing: %v", err)
	}
}

func TestWriteAgentsMigrationTemplateTreatsRenameAsCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "apm.yml")
	warning, err := writeAgentsMigrationTemplateWithSync(path, []byte("new\n"), func(string) error {
		return errors.New("injected directory sync failure")
	})
	if err != nil || !strings.Contains(warning, "directory sync") {
		t.Fatalf("warning = %q, error = %v", warning, err)
	}
	if got, _ := os.ReadFile(path); string(got) != "new\n" {
		t.Fatalf("committed template = %q", got)
	}
}

func testMigrationWrapper(state, digit, name string) agentBundleWrapper {
	hash := strings.Repeat(digit, 64)
	return agentBundleWrapper{
		Hash:     hash,
		Path:     filepath.Join(state, "agents-migration", "bundles", hash),
		Manifest: []byte("name: " + name + "\nversion: 1.0.0\ndependencies: {}\n"),
	}
}

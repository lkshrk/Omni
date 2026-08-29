package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/executor"
)

func writeTUIFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readTUIFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestAgentsSyncAllGoesThroughTheTemplateAwareSync(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	writeTUIFile(t, filepath.Join(home, ".apm", "apm.yml"), "name: live\n")
	writeTUIFile(t, filepath.Join(home, ".config", "omni", "apm.yml"), "name: template\n")

	mock := &executor.MockExecutor{Responses: []executor.MockCall{
		{Stdout: "APM CLI version 0.29.0\n"},
		{Stdout: "installed\n"},
	}}
	a := app.New(filepath.Join(home, "settings.json"))
	a.SetFallbackExecutor(mock)
	a.StateDir = filepath.Join(home, "state")

	m := baseModel(nil)
	m.app = a
	m.ctx = context.Background()

	cmds := m.doAgentsSyncAll()
	if !m.apmRunning {
		t.Fatal("sync did not mark APM as running")
	}
	var done apmCommandDoneMsg
	for _, msg := range runBatchCmd(tea.Batch(cmds...)) {
		if got, ok := msg.(apmCommandDoneMsg); ok {
			done = got
		}
	}

	if done.err != nil {
		t.Fatalf("sync failed: %v", done.err)
	}
	if !strings.Contains(done.stdout, "installed") {
		t.Fatalf("stdout = %q", done.stdout)
	}
	if len(done.notices) != 1 || !strings.Contains(done.notices[0], "force-template") {
		t.Fatalf("template warning not surfaced: %q", done.notices)
	}
	// The live manifest must survive: the divergence guard blocks the template on a first sync.
	if data := readTUIFile(t, filepath.Join(home, ".apm", "apm.yml")); data != "name: live\n" {
		t.Fatalf("live manifest overwritten: %q", data)
	}
	if len(mock.Calls) != 2 || strings.Join(mock.Calls[1].Args, " ") != "install -g" {
		t.Fatalf("calls = %#v", mock.Calls)
	}
}

package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/executor"
)

func TestAgentsTabSyncDelegatesToGlobalAPM(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".apm"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".apm", "apm.yml"), []byte("name: test\nversion: 1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "settings.json")
	if err := config.Save(configPath, &config.RootConfig{Version: config.CurrentVersion}); err != nil {
		t.Fatal(err)
	}
	a := app.New(configPath)
	if err := a.InitTestMode(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	m := modelForCmds(a)
	m.mode = viewSkills
	mock := &executor.MockExecutor{}
	m.app.SetFallbackExecutor(mock)
	cmds := m.doAgentsSyncAll()
	if len(cmds) < 2 {
		t.Fatalf("sync commands = %d", len(cmds))
	}
	msg := cmds[1]()
	if done, ok := msg.(agentsProgressDoneMsg); !ok || done.skillsErr != nil {
		t.Fatalf("sync result = %#v", msg)
	}
	want := []string{"install", "--global"}
	if len(mock.Calls) != 1 || mock.Calls[0].Name != "apm" || !reflect.DeepEqual(mock.Calls[0].Args, want) {
		t.Fatalf("calls = %#v, want apm %v", mock.Calls, want)
	}
}

func TestAgentsTabUpdateDelegatesToGlobalAPM(t *testing.T) {
	a := app.New(filepath.Join(t.TempDir(), "settings.json"))
	if err := a.InitTestMode(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	m := modelForCmds(a)
	mock := &executor.MockExecutor{}
	m.app.SetFallbackExecutor(mock)
	cmds := m.doAgentsUpdateAll()
	if len(cmds) < 2 {
		t.Fatalf("update commands = %d", len(cmds))
	}
	if done, ok := cmds[1]().(agentsProgressDoneMsg); !ok || done.skillsErr != nil {
		t.Fatalf("update result = %#v", done)
	}
	want := []string{"update", "--yes", "--global"}
	if len(mock.Calls) != 1 || mock.Calls[0].Name != "apm" || !reflect.DeepEqual(mock.Calls[0].Args, want) {
		t.Fatalf("calls = %#v, want apm %v", mock.Calls, want)
	}
}

func TestSetupAgentsImportMigratesLegacyPackageToAPM(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(t.TempDir(), "settings.json")
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex"}},
		Agents:   config.AgentsConfig{Packages: []config.SkillPackage{{Source: "acme/demo", Agents: []string{"codex"}}}},
	}); err != nil {
		t.Fatal(err)
	}
	a := app.New(configPath)
	m := modelForCmds(a)
	msg := m.doSetupAgentsImportAll()()
	done, ok := msg.(setupAgentsImportDoneMsg)
	if !ok || done.err != nil || done.skills != 1 {
		t.Fatalf("setup migration = %#v", msg)
	}
	if _, err := os.Stat(filepath.Join(home, ".apm", "apm.yml")); err != nil {
		t.Fatalf("manifest: %v", err)
	}
}

package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/executor"
)

func newSyncApp(t *testing.T, cfg *config.RootConfig, withManifest bool) (*App, *executor.MockExecutor, string) {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "settings.json")
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if withManifest {
		if err := os.MkdirAll(filepath.Join(home, ".apm"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, ".apm", "apm.yml"), []byte("name: test\nversion: 1.0.0\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mock := &executor.MockExecutor{}
	a := New(configPath, WithEnvLookup(func(name string) string {
		if name == "HOME" {
			return home
		}
		return ""
	}))
	a.SetFallbackExecutor(mock)
	return a, mock, configPath
}

func TestAgentsSyncAllFrozenReplaysManifest(t *testing.T) {
	a, mock, _ := newSyncApp(t, &config.RootConfig{Version: config.CurrentVersion}, true)
	mock.Responses = []executor.MockCall{{Stdout: "installed\n", Stderr: "warning\n"}}

	result, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{Frozen: true, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "installed\n" || result.Stderr != "warning\n" {
		t.Fatalf("result = %#v", result)
	}
	want := []string{"install", "--global", "--frozen", "--dry-run"}
	if len(mock.Calls) != 1 || mock.Calls[0].Name != "apm" || !reflect.DeepEqual(mock.Calls[0].Args, want) {
		t.Fatalf("calls = %#v, want apm %v", mock.Calls, want)
	}
}

func TestAgentsSyncAllInstallsConfiguredPackagesDirectly(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex"}},
		Agents: config.AgentsConfig{Packages: []config.SkillPackage{
			{Source: "acme/shared", Agents: []string{"codex"}, Ref: "v1"},
		}},
	}
	a, mock, configPath := newSyncApp(t, cfg, false)

	result, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"install", "--global", "--target", "codex", "acme/shared#v1"}
	if len(mock.Calls) != 1 || !reflect.DeepEqual(mock.Calls[0].Args, want) {
		t.Fatalf("calls = %#v, want apm %v", mock.Calls, want)
	}
	if result.InstalledPackages != 1 {
		t.Fatalf("result = %#v", result)
	}
	got, err := config.Load(configPath)
	if err != nil || len(got.Agents.Packages) != 1 {
		t.Fatalf("config mutated: %+v, %v", got.Agents, err)
	}
}

func TestAgentsSyncAllHonorsGroupHostScoping(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "workhost")
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex"}},
		Agents: config.AgentsConfig{Packages: []config.SkillPackage{
			{Source: "acme/everywhere", Agents: []string{"codex"}},
			{Source: "acme/grouped", Agents: []string{"codex"}},
		}},
		Groups: []*config.GroupConfig{{Name: "ai-plugins", Skills: []string{"acme/grouped"}}},
		Hosts:  map[string][]string{"otherhost": {"ai-plugins"}, "workhost": {}},
	}
	a, mock, configPath := newSyncApp(t, cfg, false)

	if _, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{}); err != nil {
		t.Fatal(err)
	}
	want := []string{"install", "--global", "--target", "codex", "acme/everywhere"}
	if len(mock.Calls) != 1 || !reflect.DeepEqual(mock.Calls[0].Args, want) {
		t.Fatalf("calls = %#v, want apm %v", mock.Calls, want)
	}
	got, err := config.Load(configPath)
	if err != nil || len(got.Agents.Packages) != 2 || len(got.Groups) != 1 || len(got.Groups[0].Skills) != 1 {
		t.Fatalf("config mutated: %+v, %v", got.Agents, err)
	}
}

func TestAgentsSyncAllInstallsGroupPackagesOnMemberHost(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "workhost")
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex"}},
		Agents: config.AgentsConfig{Packages: []config.SkillPackage{
			{Source: "acme/grouped", Agents: []string{"codex"}},
		}},
		Groups: []*config.GroupConfig{{Name: "ai-plugins", Skills: []string{"acme/grouped"}}},
		Hosts:  map[string][]string{"workhost": {"ai-plugins"}},
	}
	a, mock, _ := newSyncApp(t, cfg, false)

	if _, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{}); err != nil {
		t.Fatal(err)
	}
	want := []string{"install", "--global", "--target", "codex", "acme/grouped"}
	if len(mock.Calls) != 1 || !reflect.DeepEqual(mock.Calls[0].Args, want) {
		t.Fatalf("calls = %#v, want apm %v", mock.Calls, want)
	}
}

func TestAgentsSyncAllBatchesPackagesByTargetSet(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex", "claude-code"}},
		Agents: config.AgentsConfig{Packages: []config.SkillPackage{
			{Source: "acme/one", Agents: []string{"codex"}},
			{Source: "acme/two", Agents: []string{"claude-code"}},
			{Source: "acme/three", Agents: []string{"codex"}},
		}},
	}
	a, mock, _ := newSyncApp(t, cfg, false)

	if _, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(mock.Calls) != 2 {
		t.Fatalf("calls = %#v, want two target batches", mock.Calls)
	}
	wantFirst := []string{"install", "--global", "--target", "codex", "acme/one", "acme/three"}
	wantSecond := []string{"install", "--global", "--target", "claude", "acme/two"}
	if !reflect.DeepEqual(mock.Calls[0].Args, wantFirst) || !reflect.DeepEqual(mock.Calls[1].Args, wantSecond) {
		t.Fatalf("calls = %#v, want %v then %v", mock.Calls, wantFirst, wantSecond)
	}
}

func TestAgentsSyncAllSkipsPackagesDisabledByAgentsUse(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex"}},
		Agents: config.AgentsConfig{Packages: []config.SkillPackage{
			{Source: "acme/kept", Agents: []string{"codex"}},
			{Source: "acme/disabled", Agents: []string{"claude-code"}},
		}},
	}
	a, mock, _ := newSyncApp(t, cfg, false)

	result, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"install", "--global", "--target", "codex", "acme/kept"}
	if len(mock.Calls) != 1 || !reflect.DeepEqual(mock.Calls[0].Args, want) {
		t.Fatalf("calls = %#v, want apm %v", mock.Calls, want)
	}
	if !strings.Contains(strings.Join(result.Warnings, " "), `"acme/disabled" skipped`) {
		t.Fatalf("warnings = %v", result.Warnings)
	}
}

func TestAgentsSyncAllNoPackagesNoManifestIsQuietNoop(t *testing.T) {
	a, mock, _ := newSyncApp(t, &config.RootConfig{Version: config.CurrentVersion}, false)
	result, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("apm invoked with nothing to do: %#v", mock.Calls)
	}
	if !strings.Contains(strings.Join(result.Warnings, " "), "nothing to install") {
		t.Fatalf("warnings = %v", result.Warnings)
	}
}

func TestAgentsSyncAllReplaysManifestWhenNoPackagesConfigured(t *testing.T) {
	a, mock, _ := newSyncApp(t, &config.RootConfig{Version: config.CurrentVersion}, true)
	if _, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{}); err != nil {
		t.Fatal(err)
	}
	want := []string{"install", "--global"}
	if len(mock.Calls) != 1 || !reflect.DeepEqual(mock.Calls[0].Args, want) {
		t.Fatalf("calls = %#v, want apm %v", mock.Calls, want)
	}
}

func TestAgentsUpdateAllWithoutManifestReturnsGuidance(t *testing.T) {
	a, mock, _ := newSyncApp(t, &config.RootConfig{Version: config.CurrentVersion}, false)
	_, err := a.AgentsUpdateAll(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "omni agents sync") {
		t.Fatalf("error lacks guidance: %v", err)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("apm invoked despite missing manifest: %#v", mock.Calls)
	}
}

func TestAgentsUpdateAllDelegatesToAPM(t *testing.T) {
	a, mock, _ := newSyncApp(t, &config.RootConfig{Version: config.CurrentVersion}, true)
	if _, err := a.AgentsUpdateAll(context.Background(), false, "acme/shared"); err != nil {
		t.Fatal(err)
	}
	want := []string{"update", "acme/shared", "--yes", "--global"}
	if len(mock.Calls) != 1 || !reflect.DeepEqual(mock.Calls[0].Args, want) {
		t.Fatalf("calls = %#v, want apm %v", mock.Calls, want)
	}
}

package app

import (
	"context"
	"reflect"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestAddAgentPackagesDeclaresInConfigAndInstallsTheSurface(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex"}},
	}
	a, mock, configPath := newSyncApp(t, cfg, "")

	if _, warnings, err := a.AddAgentPackages(context.Background(), []string{"acme/demo#v2"}); err != nil {
		t.Fatalf("add: %v (%v)", err, warnings)
	}
	want := []string{"install", "-g", "--only", "apm", "--target", "codex"}
	if calls := apmCalls(mock); len(calls) != 1 || !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("calls = %#v, want apm %v", calls, want)
	}
	got, err := config.Load(configPath)
	if err != nil || len(got.Agents.Packages) != 1 || got.Agents.Packages[0].Source != "acme/demo" {
		t.Fatalf("packages = %+v, %v", got.Agents.Packages, err)
	}
	deps := syncManifestDeps(t, a, "apm")
	wantDeps := []any{map[string]any{"git": "acme/demo", "ref": "v2", "targets": []any{"codex"}}}
	if !reflect.DeepEqual(deps, wantDeps) {
		t.Fatalf("apm deps = %#v, want %#v", deps, wantDeps)
	}
}

func TestAddAgentPackagesRefusesACredentialBearingSource(t *testing.T) {
	a, mock, configPath := newSyncApp(t, &config.RootConfig{Version: config.CurrentVersion}, "")

	_, _, err := a.AddAgentPackages(context.Background(), []string{"https://user:token@git.example.com/acme/skills.git"})
	if err == nil {
		t.Fatal("expected a refusal for a credential-bearing source")
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("calls = %#v, want none", mock.Calls)
	}
	got, loadErr := config.Load(configPath)
	if loadErr != nil || len(got.Agents.Packages) != 0 {
		t.Fatalf("packages = %+v, %v; want nothing written", got.Agents.Packages, loadErr)
	}
}

// Undeclaring without uninstalling leaves the package deployed; uninstalling without undeclaring has the
// next sync put it straight back.
func TestRemoveAgentPackagesUndeclaresAndUninstalls(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex"}},
		Agents: config.AgentsConfig{Packages: []config.SkillPackage{
			{Source: "acme/dropped", Agents: []string{"codex"}},
			{Source: "acme/kept", Agents: []string{"codex"}},
		}},
	}
	a, mock, configPath := newSyncApp(t, cfg, emptySyncManifest)

	if _, err := a.RemoveAgentPackages(context.Background(), []string{"https://github.com/acme/dropped.git"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"uninstall", "--global", "https://github.com/acme/dropped.git"}
	if calls := apmCalls(mock); len(calls) != 1 || !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("calls = %#v, want apm %v", calls, want)
	}
	got, err := config.Load(configPath)
	if err != nil || len(got.Agents.Packages) != 1 || got.Agents.Packages[0].Source != "acme/kept" {
		t.Fatalf("packages = %+v, %v; want the URL-form argument matched to its declaration", got.Agents.Packages, err)
	}
}

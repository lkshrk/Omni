package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/executor"
)

func TestAgentsSyncAllDelegatesToGlobalAPM(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "settings.json")
	if err := config.Save(configPath, &config.RootConfig{Version: config.CurrentVersion}); err != nil {
		t.Fatal(err)
	}
	mock := &executor.MockExecutor{Responses: []executor.MockCall{{Stdout: "installed\n", Stderr: "warning\n"}}}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".apm"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".apm", "apm.yml"), []byte("name: test\nversion: 1.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(configPath, WithEnvLookup(func(name string) string {
		if name == "HOME" {
			return home
		}
		return ""
	}))
	a.SetFallbackExecutor(mock)

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

func TestAgentsSyncAllDryRunDoesNotMigrateLegacyConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "settings.json")
	home := filepath.Join(dir, "home")
	want := &config.RootConfig{Version: config.CurrentVersion, Agents: config.AgentsConfig{
		Packages: []config.SkillPackage{{Source: "acme/shared"}},
	}}
	if err := config.Save(configPath, want); err != nil {
		t.Fatal(err)
	}
	mock := &executor.MockExecutor{}
	a := New(configPath, WithEnvLookup(func(name string) string {
		if name == "HOME" {
			return home
		}
		return ""
	}))
	a.SetFallbackExecutor(mock)

	result, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("dry-run invoked APM without a manifest: %#v", mock.Calls)
	}
	if _, err := os.Stat(filepath.Join(home, ".apm", "apm.yml")); !os.IsNotExist(err) {
		t.Fatalf("dry-run created manifest: %v", err)
	}
	got, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Agents.Packages, want.Agents.Packages) {
		t.Fatalf("legacy config changed: %#v", got.Agents.Packages)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("dry-run without a manifest should explain that migration is deferred")
	}
}

func TestAgentsSyncAllFrozenDoesNotMigrateLegacyConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "settings.json")
	home := filepath.Join(dir, "home")
	if err := config.Save(configPath, &config.RootConfig{Version: config.CurrentVersion, Agents: config.AgentsConfig{
		Packages: []config.SkillPackage{{Source: "acme/shared"}},
	}}); err != nil {
		t.Fatal(err)
	}
	a := New(configPath, WithEnvLookup(func(name string) string {
		if name == "HOME" {
			return home
		}
		return ""
	}))
	a.SetFallbackExecutor(&executor.MockExecutor{})
	_, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{Frozen: true})
	if err == nil {
		t.Fatal("frozen sync without a manifest should fail")
	}
	if _, err := os.Stat(filepath.Join(home, ".apm", "apm.yml")); !os.IsNotExist(err) {
		t.Fatalf("frozen sync created manifest: %v", err)
	}
}

package app

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setupTemplateEnv(t *testing.T) (workspace, stateDir, template string) {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	return t.TempDir(), t.TempDir(), filepath.Join(cfg, "omni", "apm.yml")
}

func TestMaterializeNoTemplateIsNoop(t *testing.T) {
	ws, st, _ := setupTemplateEnv(t)
	copied, warn, err := materializeAgentsTemplate(ws, st, false)
	if err != nil || copied || warn != "" {
		t.Fatalf("copied=%v warn=%q err=%v", copied, warn, err)
	}
}

func TestMaterializeFreshWorkspaceCopies(t *testing.T) {
	ws, st, tmpl := setupTemplateEnv(t)
	writeFile(t, tmpl, "name: h\ndependencies: {}\n")
	copied, warn, err := materializeAgentsTemplate(ws, st, false)
	if err != nil || !copied || warn != "" {
		t.Fatalf("copied=%v warn=%q err=%v", copied, warn, err)
	}
	got, _ := os.ReadFile(filepath.Join(ws, "apm.yml"))
	if string(got) != "name: h\ndependencies: {}\n" {
		t.Fatalf("live = %q", got)
	}
}

func TestMaterializeFirstRunWithLiveRefusesWithoutForce(t *testing.T) {
	ws, st, tmpl := setupTemplateEnv(t)
	writeFile(t, tmpl, "v2\n")
	live := filepath.Join(ws, "apm.yml")
	writeFile(t, live, "pre-existing\n")
	copied, warn, err := materializeAgentsTemplate(ws, st, false)
	if err != nil || copied || !strings.Contains(warn, "first sync with a template") {
		t.Fatalf("copied=%v warn=%q err=%v", copied, warn, err)
	}
	copied, _, err = materializeAgentsTemplate(ws, st, true)
	if err != nil || !copied {
		t.Fatalf("force adopt: copied=%v err=%v", copied, err)
	}
}

func TestMaterializeLiveEqualToTemplateNeedsNoCopy(t *testing.T) {
	ws, st, tmpl := setupTemplateEnv(t)
	writeFile(t, tmpl, "identical\n")
	writeFile(t, filepath.Join(ws, "apm.yml"), "identical\n")
	synced, warn, err := materializeAgentsTemplate(ws, st, false)
	if err != nil || !synced || warn != "" {
		t.Fatalf("synced=%v warn=%q err=%v", synced, warn, err)
	}
}

func TestMaterializeMatchingSnapshotCopies(t *testing.T) {
	ws, st, tmpl := setupTemplateEnv(t)
	writeFile(t, tmpl, "v2\n")
	live := filepath.Join(ws, "apm.yml")
	writeFile(t, live, "v1-normalized\n")
	if err := snapshotLiveManifest(ws, st); err != nil {
		t.Fatal(err)
	}
	copied, warn, err := materializeAgentsTemplate(ws, st, false)
	if err != nil || !copied || warn != "" {
		t.Fatalf("copied=%v warn=%q err=%v", copied, warn, err)
	}
	got, _ := os.ReadFile(live)
	if string(got) != "v2\n" {
		t.Fatalf("live = %q", got)
	}
}

func TestMaterializeDivergedLiveRefusesWithoutForce(t *testing.T) {
	ws, st, tmpl := setupTemplateEnv(t)
	writeFile(t, tmpl, "v2\n")
	live := filepath.Join(ws, "apm.yml")
	writeFile(t, live, "v1-normalized\n")
	if err := snapshotLiveManifest(ws, st); err != nil {
		t.Fatal(err)
	}
	writeFile(t, live, "v1-hand-edited\n")
	copied, warn, err := materializeAgentsTemplate(ws, st, false)
	if err != nil || copied || !strings.Contains(warn, "diverged") {
		t.Fatalf("copied=%v warn=%q err=%v", copied, warn, err)
	}
	got, _ := os.ReadFile(live)
	if string(got) != "v1-hand-edited\n" {
		t.Fatalf("live overwritten: %q", got)
	}
	copied, warn, err = materializeAgentsTemplate(ws, st, true)
	if err != nil || !copied {
		t.Fatalf("force: copied=%v warn=%q err=%v", copied, warn, err)
	}
}

func TestAgentsSyncAllDryRunLeavesLiveManifestAndStateUntouched(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	writeFile(t, filepath.Join(cfg, "omni", "apm.yml"), "name: from-template\n")
	live := filepath.Join(home, ".apm", "apm.yml")
	writeFile(t, live, "name: live-normalized\n")
	stateDir := t.TempDir()
	// Snapshot first so materialization would copy here, leaving the dry-run guard as the only thing stopping it.
	if err := snapshotLiveManifest(filepath.Join(home, ".apm"), stateDir); err != nil {
		t.Fatal(err)
	}
	stateBefore, err := os.ReadFile(filepath.Join(stateDir, templateStateName))
	if err != nil {
		t.Fatal(err)
	}
	mock := &executor.MockExecutor{Responses: []executor.MockCall{
		{Stdout: "APM CLI version " + apmVersionPin + "\n"},
		{Stdout: "planned\n"},
	}}
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	a.StateDir = stateDir
	a.SetFallbackExecutor(mock)

	if _, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(live)
	if string(got) != "name: live-normalized\n" {
		t.Fatalf("live = %q, want a dry run to leave it untouched", got)
	}
	stateAfter, _ := os.ReadFile(filepath.Join(stateDir, templateStateName))
	if string(stateAfter) != string(stateBefore) {
		t.Fatalf("state = %q, want %q unchanged by a dry run", stateAfter, stateBefore)
	}
}

func TestAgentsSyncAllSnapshotsWhenLiveAlreadyMatchesTemplate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	writeFile(t, filepath.Join(cfg, "omni", "apm.yml"), "name: identical\n")
	writeFile(t, filepath.Join(home, ".apm", "apm.yml"), "name: identical\n")
	stateDir := t.TempDir()
	mock := &executor.MockExecutor{Responses: []executor.MockCall{
		{Stdout: "APM CLI version " + apmVersionPin + "\n"},
		{Stdout: "installed\n"},
	}}
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	a.StateDir = stateDir
	a.SetFallbackExecutor(mock)

	res, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Warning != "" {
		t.Fatalf("warning = %q, want none when the live manifest already equals the template", res.Warning)
	}
	if _, err := os.Stat(filepath.Join(stateDir, templateStateName)); err != nil {
		t.Fatalf("post-install snapshot missing: %v", err)
	}
}

func TestSnapshotLiveManifestWritesHash(t *testing.T) {
	ws, st := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(ws, "apm.yml"), "abc")
	if err := snapshotLiveManifest(ws, st); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("abc"))
	got, _ := os.ReadFile(filepath.Join(st, templateStateName))
	if string(got) != hex.EncodeToString(sum[:])+"\n" {
		t.Fatalf("state = %q", got)
	}
}

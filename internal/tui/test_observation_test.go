package tui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/app"
)

func TestObservationMergesAcceptedSemanticMessages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "observation.json")
	t.Setenv(testObservationEnv, path)
	observeTestTools([]*app.ToolView{{Name: "zeta", Provider: "brew", Package: "zeta", Installed: true, Version: "1.0.0", Tracked: true}})
	observeTestDots([]app.DotStatus{{Name: "nvim", Health: app.HealthConflict}}, " M dotfiles/nvim")
	observeTestDoctor(&app.DoctorResult{Checks: []app.DoctorCheck{{ID: "config", Status: app.DoctorStatusWarn, Message: "fixture"}}, Summary: app.DoctorSummary{Warn: 1}})

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got testObservation
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Tools) != 1 || got.Tools[0].Name != "zeta" || got.Dots == nil || len(got.Dots.Entries) != 1 || got.Doctor == nil || got.Doctor.Summary.Warn != 1 {
		t.Fatalf("observation = %#v", got)
	}
}

func TestObservationRejectsPathOutsideSandbox(t *testing.T) {
	root := os.Getenv("OMNI_TEST_ROOT")
	if root == "" {
		t.Skip("testguard sandbox root unavailable")
	}
	path := filepath.Join(filepath.Dir(root), "escaped-observation.json")
	t.Setenv(testObservationEnv, path)
	assertObservationPanics(t, func() { observeTestTools([]*app.ToolView{{Name: "must-not-write"}}) })
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("outside observation was written: %v", err)
	}
}

func TestObservationRejectsFinalSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "outside.json")
	if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "observation.json")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	t.Setenv(testObservationEnv, path)
	assertObservationPanics(t, func() { observeTestTools([]*app.ToolView{{Name: "must-not-write"}}) })
	raw, err := os.ReadFile(target)
	if err != nil || string(raw) != "outside" {
		t.Fatalf("symlink target changed: %q, %v", raw, err)
	}
}

func TestObservationFailsFastOnCorruptPriorJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "observation.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(testObservationEnv, path)
	assertObservationPanics(t, func() { observeTestTools([]*app.ToolView{{Name: "fresh"}}) })
}

func TestObservationFailsFastWhenAtomicRenameCannotReplaceTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "observation.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(testObservationEnv, path)
	original := renameTestObservation
	renameTestObservation = func(string, string) error { return errors.New("rename failed") }
	t.Cleanup(func() { renameTestObservation = original })
	assertObservationPanics(t, func() { observeTestTools([]*app.ToolView{{Name: "fresh"}}) })
}

func TestObservationFailsFastWhenAtomicWriteCannotStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "observation.json")
	t.Setenv(testObservationEnv, path)
	original := createTestObservationTemp
	createTestObservationTemp = func(string, string) (*os.File, error) { return nil, errors.New("write failed") }
	t.Cleanup(func() { createTestObservationTemp = original })
	assertObservationPanics(t, func() { observeTestTools([]*app.ToolView{{Name: "fresh"}}) })
}

func TestObservationIgnoresStaleDotsGeneration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "observation.json")
	t.Setenv(testObservationEnv, path)
	model := Model{dotsOpGen: 2, dotsEntries: []app.DotStatus{{Name: "fresh"}}, dotsGitStatus: "fresh"}
	observeTestDots(model.dotsEntries, model.dotsGitStatus)
	model.Update(dotsLoadedMsg{gen: 1, entries: []app.DotStatus{{Name: "stale"}}, gitStatus: "stale"})
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got testObservation
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Dots == nil || len(got.Dots.Entries) != 1 || got.Dots.Entries[0].Name != "fresh" || got.Dots.GitStatus != "fresh" {
		t.Fatalf("stale dots message was observed: %#v", got.Dots)
	}
}

func TestObservationKeepsAcceptedToolsAndDoctorAfterRejectedMessages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "observation.json")
	t.Setenv(testObservationEnv, path)
	model := Model{
		allTools:     []*app.ToolView{{Name: "fresh"}},
		doctorResult: &app.DoctorResult{Checks: []app.DoctorCheck{{ID: "fresh"}}},
	}
	observeTestTools(model.allTools)
	observeTestDoctor(model.doctorResult)
	model.Update(toolsLoadedMsg{tools: []*app.ToolView{{Name: "stale"}}, err: errors.New("stale tools")})
	model.Update(doctorDoneMsg{result: &app.DoctorResult{Checks: []app.DoctorCheck{{ID: "stale"}}}, err: errors.New("stale doctor")})
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got testObservation
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Tools) != 1 || got.Tools[0].Name != "fresh" || got.Doctor == nil || len(got.Doctor.Checks) != 1 || got.Doctor.Checks[0].ID != "fresh" {
		t.Fatalf("rejected messages were observed: %#v", got)
	}
}

func assertObservationPanics(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("observation write did not fail fast")
		}
	}()
	fn()
}

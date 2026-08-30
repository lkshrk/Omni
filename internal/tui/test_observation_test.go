package tui

import (
	"encoding/json"
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
	observeTestTools([]*app.ToolView{{Name: "must-not-write"}})
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("outside observation was written: %v", err)
	}
}

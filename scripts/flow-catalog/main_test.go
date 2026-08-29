package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/actions"
	"github.com/lkshrk/omni/internal/testflow"
	_ "github.com/lkshrk/omni/internal/testguard"
)

func TestActionSurfacesProjectRegistryMetadata(t *testing.T) {
	byID := map[string]testflow.ActionSurface{}
	for _, surface := range actionSurfaces() {
		byID[surface.ID] = surface
	}
	sync := byID[string(actions.ToolSync)]
	if !sync.CLI || !sync.TUI || !sync.Mutates || len(sync.CLICommands) != 1 || !slices.Equal(sync.CLICommands[0].Command, []string{"tools", "sync"}) {
		t.Fatalf("tools.sync surface = %+v, want complete registry projection", sync)
	}
	syncAll := byID[string(actions.ToolSyncAll)]
	if len(syncAll.CLICommands) != 1 || !slices.Equal(syncAll.CLICommands[0].RequiredFlags, []string{"--all"}) {
		t.Fatalf("tools.sync_all surface = %+v, want required --all flag", syncAll)
	}
}

func TestUpdateDetectsAndRepairsDrift(t *testing.T) {
	path := filepath.Join(t.TempDir(), "matrix.md")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := update(path, []byte("old"), []byte("new"), false); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("update(check) error = %v, want drift", err)
	}
	if err := update(path, []byte("old"), []byte("new"), true); err != nil {
		t.Fatalf("update(write): %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "new" {
		t.Fatalf("written body = %q", body)
	}
}

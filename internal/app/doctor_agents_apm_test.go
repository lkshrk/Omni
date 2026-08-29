package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestDoctorAgentsReportsWorkspaceState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	mock := &availExecutor{available: map[string]bool{}}
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	a.SetFallbackExecutor(mock)

	result := &DoctorResult{}
	a.doctorAgents(context.Background(), result, &config.RootConfig{})
	if got := result.Checks[0]; got.Status != DoctorStatusWarn || !strings.Contains(got.Message, "executable not found") {
		t.Fatalf("missing executable check = %+v", got)
	}

	mock.available["apm"] = true
	// Availability is memoized per executor, so flipping it mid-test re-announces the executor.
	a.SetFallbackExecutor(mock)
	result = &DoctorResult{}
	a.doctorAgents(context.Background(), result, &config.RootConfig{})
	if got := result.Checks[0]; got.Status != DoctorStatusWarn || !strings.Contains(got.Message, "manifest not found") {
		t.Fatalf("missing manifest check = %+v", got)
	}

	dir := filepath.Join(home, ".apm")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "apm.yml"), []byte("name: test\nversion: 1.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result = &DoctorResult{}
	a.doctorAgents(context.Background(), result, &config.RootConfig{})
	if got := result.Checks[0]; got.Status != DoctorStatusWarn || !strings.Contains(got.Message, "lockfile not found") {
		t.Fatalf("missing lockfile check = %+v", got)
	}

	if err := os.WriteFile(filepath.Join(dir, "apm.lock.yaml"), []byte("dependencies: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result = &DoctorResult{}
	a.doctorAgents(context.Background(), result, &config.RootConfig{})
	if len(result.Checks) != 2 || result.Checks[0].Status != DoctorStatusOK || result.Checks[1].Status != DoctorStatusOK {
		t.Fatalf("healthy checks = %+v", result.Checks)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("apm invoked = %+v; doctor must not run apm", mock.Calls)
	}
}

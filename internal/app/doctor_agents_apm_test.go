package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/executor"
)

func TestDoctorAgentsReportsWorkspaceAndAudit(t *testing.T) {
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
	result = &DoctorResult{}
	a.doctorAgents(context.Background(), result, &config.RootConfig{})
	if got := result.Checks[0]; got.Status != DoctorStatusWarn || !strings.Contains(got.Message, "manifest not found") {
		t.Fatalf("missing manifest check = %+v", got)
	}

	dir := filepath.Join(home, ".apm")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "apm.yml")
	if err := os.WriteFile(manifest, []byte("name: test\nversion: 1.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result = &DoctorResult{}
	a.doctorAgents(context.Background(), result, &config.RootConfig{})
	if got := result.Checks[0]; got.Status != DoctorStatusWarn || !strings.Contains(got.Message, "lockfile not found") {
		t.Fatalf("missing lockfile check = %+v", got)
	}

	lockfile := filepath.Join(dir, "apm.lock.yaml")
	if err := os.WriteFile(lockfile, []byte("dependencies: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("vulnerability found")
	mock.Responses = []executor.MockCall{
		{Stdout: "Agent Package Manager (APM) CLI version " + apmVersionPin + "\n"},
		{Stdout: "no vulnerabilities\n"},
		{Stdout: "Agent Package Manager (APM) CLI version " + apmVersionPin + "\n"},
		{Stderr: "critical package\n", Err: sentinel},
	}
	result = &DoctorResult{}
	a.doctorAgents(context.Background(), result, &config.RootConfig{})
	if len(result.Checks) != 2 || result.Checks[0].Status != DoctorStatusOK || result.Checks[1].Status != DoctorStatusOK {
		t.Fatalf("healthy checks = %+v", result.Checks)
	}
	if len(mock.Calls) != 2 || !strings.Contains(strings.Join(mock.Calls[1].Args, " "), "audit --ci") {
		t.Fatalf("audit call = %+v, want apm audit --ci", mock.Calls)
	}

	result = &DoctorResult{}
	a.doctorAgents(context.Background(), result, &config.RootConfig{})
	if got := result.Checks[1]; got.Status != DoctorStatusFail || !strings.Contains(strings.Join(got.Details, " "), "critical package") {
		t.Fatalf("failed audit check = %+v", got)
	}
}

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestDoctorAgentsReportsAPMState(t *testing.T) {
	home := t.TempDir()
	a := New(filepath.Join(t.TempDir(), "settings.json"), WithEnvLookup(func(name string) string {
		if name == "HOME" {
			return home
		}
		return ""
	}))
	original := lookPath
	t.Cleanup(func() { lookPath = original })

	lookPath = func(string) (string, error) { return "", errors.New("missing") }
	result := &DoctorResult{}
	a.doctorAgents(context.Background(), result, &config.RootConfig{})
	if got := result.Checks[0]; got.Status != DoctorStatusWarn || !strings.Contains(got.Message, "executable not found") {
		t.Fatalf("missing executable check = %+v", got)
	}

	lookPath = func(string) (string, error) { return "/usr/bin/apm", nil }
	result = &DoctorResult{}
	a.doctorAgents(context.Background(), result, &config.RootConfig{})
	if got := result.Checks[0]; got.Status != DoctorStatusWarn || !strings.Contains(got.Message, "manifest not found") {
		t.Fatalf("missing manifest check = %+v", got)
	}

	manifest := filepath.Join(home, ".apm", "apm.yml")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte("name: test\nversion: 1.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result = &DoctorResult{}
	a.doctorAgents(context.Background(), result, &config.RootConfig{})
	if got := result.Checks[0]; got.Status != DoctorStatusOK || !strings.Contains(got.Message, manifest) {
		t.Fatalf("healthy check = %+v", got)
	}
}

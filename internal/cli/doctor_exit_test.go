package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --dry-run previews the fixers; it does not suppress the health verdict.
func TestDoctorFixDryRun_ExitsNonZeroOnFailingCheck(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	missingRepo := filepath.Join(dir, "no-such-dots-repo")
	settings := `{
  "settings": {"dots_repo": "` + missingRepo + `"},
  "hosts": {"testhost": ["dev"]},
  "groups": [{"name": "testhost", "special": "host"}, {"name": "dev"}]
}`
	if err := os.WriteFile(cfgPath, []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}

	output, err := runRootCommand(t, "--config", cfgPath, "--cache-dir", t.TempDir(), "doctor", "--fix", "--dry-run")
	if err == nil {
		t.Fatalf("doctor --fix --dry-run exited 0 with a failing check:\n%s", output)
	}
	if !strings.Contains(err.Error(), "failing check") {
		t.Fatalf("error = %v, want the failing-check verdict", err)
	}
	if !strings.Contains(output, "dry run") {
		t.Fatalf("output missing the dry-run notice:\n%s", output)
	}
}

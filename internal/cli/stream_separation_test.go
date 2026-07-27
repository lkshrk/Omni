package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runRootStreams(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

func writeDoctorFixtureWithDuplicates(t *testing.T) (cfgPath, cacheDir string) {
	t.Helper()
	cfgDir := t.TempDir()
	cacheDir = t.TempDir()
	cfgPath = filepath.Join(cfgDir, "settings.json")
	write := func(rel, content string) {
		p := filepath.Join(cfgDir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("settings.json", `{
  "$include": ["settings.d/dots.json"],
  "hosts": {"testhost": ["dev"]},
  "groups": [
    {"name": "testhost", "special": "host"},
    {"name": "dev", "dots": [{"name": "git", "path": "~/.gitconfig"}]}
  ]
}`)
	write("settings.d/dots.json", `{
  "groups": [{"name": "dev", "dots": [{"name": "git", "path": "~/.gitconfig"}]}]
}`)
	return cfgPath, cacheDir
}

func TestDoctorFixJSON_StdoutIsOnlyTheDocument(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	for _, args := range [][]string{
		{"doctor", "--fix", "--format", "json"},
		{"doctor", "--fix", "--dry-run", "--format", "json"},
	} {
		cfgPath, cacheDir := writeDoctorFixtureWithDuplicates(t)
		full := append([]string{"--config", cfgPath, "--cache-dir", cacheDir}, args...)
		stdout, stderr, err := runRootStreams(t, full...)
		if err != nil {
			t.Fatalf("%v: %v\nstdout=%s\nstderr=%s", args, err, stdout, stderr)
		}
		var decoded map[string]any
		if jsonErr := json.Unmarshal([]byte(stdout), &decoded); jsonErr != nil {
			t.Fatalf("%v: stdout is not a bare JSON document: %v\nstdout=%q", args, jsonErr, stdout)
		}
		if _, ok := decoded["checks"]; !ok {
			t.Fatalf("%v: decoded document has no checks: %v", args, decoded)
		}
		if !strings.Contains(stderr, "git") {
			t.Fatalf("%v: fix progress missing from stderr: %q", args, stderr)
		}
	}
}

func TestDoctorFixText_ProgressStaysOnStdout(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgPath, cacheDir := writeDoctorFixtureWithDuplicates(t)
	stdout, _, err := runRootStreams(t, "--config", cfgPath, "--cache-dir", cacheDir, "doctor", "--fix", "--dry-run")
	if err != nil {
		t.Fatalf("doctor --fix --dry-run: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "would remove") || !strings.Contains(stdout, "Omni doctor") {
		t.Fatalf("text mode lost fix progress from stdout: %q", stdout)
	}
}

func TestToolsDelete_RenameNoticeNamesThePurgingReplacement(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	withHost(t, cfgPath)
	_, stderr, _ := runRootStreams(t, "--config", cfgPath, "--cache-dir", t.TempDir(), "tools", "delete", "ripgrep", "-y")
	if !strings.Contains(stderr, `"tools remove --purge"`) {
		t.Fatalf("notice must name the purging replacement, got %q", stderr)
	}
}

func TestToolsDelete_ExplicitPurgeFalseIsHonoured(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	withHost(t, cfgPath)
	_, stderr, err := runRootStreams(t, "--config", cfgPath, "--cache-dir", t.TempDir(),
		"tools", "delete", "ripgrep", "--purge=false", "-y")
	if err != nil && strings.Contains(err.Error(), "--provider is required") {
		t.Fatalf("--purge=false still took the purge path: %v\nstderr=%s", err, stderr)
	}
}

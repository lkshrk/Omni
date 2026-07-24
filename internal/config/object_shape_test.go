package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func writeObjectShapeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func requireJSONObjectError(t *testing.T, err error) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "JSON object") {
		t.Fatalf("error = %v, want JSON object error", err)
	}
}

func TestLoadRejectsTopLevelNull(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	writeObjectShapeFixture(t, path, "null\n")

	_, err := config.Load(path)
	requireJSONObjectError(t, err)
}

func TestPatchRawRejectsTopLevelNullAndPreservesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	before := []byte("null\n")
	writeObjectShapeFixture(t, path, string(before))

	err := config.PatchRaw(path, map[string]json.RawMessage{
		"settings": json.RawMessage(`{"auto_import":true}`),
	})
	requireJSONObjectError(t, err)
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != string(before) {
		t.Fatalf("rejected patch changed file: got %q, want %q", after, before)
	}
}

func TestLoadRejectsNullInclude(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "settings.json")
	writeObjectShapeFixture(t, mainPath, `{"version":19,"$include":["fragment.json"],"settings":{}}`)
	writeObjectShapeFixture(t, filepath.Join(dir, "fragment.json"), "null\n")

	_, err := config.Load(mainPath)
	requireJSONObjectError(t, err)
}

func TestPatchRawRoutedRejectsNullFragmentAndPreservesFiles(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "settings.json")
	fragmentPath := filepath.Join(dir, "fragment.json")
	mainBefore := []byte(`{"version":19,"$include":["fragment.json"],"settings":{}}`)
	fragmentBefore := []byte("null\n")
	writeObjectShapeFixture(t, mainPath, string(mainBefore))
	writeObjectShapeFixture(t, fragmentPath, string(fragmentBefore))

	err := config.PatchRawRouted(mainPath, map[string]json.RawMessage{
		"settings": json.RawMessage(`{"auto_import":true}`),
	})
	requireJSONObjectError(t, err)
	for path, before := range map[string][]byte{mainPath: mainBefore, fragmentPath: fragmentBefore} {
		after, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(after) != string(before) {
			t.Fatalf("rejected routed patch changed %s: got %q, want %q", path, after, before)
		}
	}
}

func TestToolSourceRejectsTopLevelNull(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	writeObjectShapeFixture(t, path, "null\n")

	_, err := config.ToolSource(path, "jq")
	requireJSONObjectError(t, err)
}

func TestExtractIncludeFragmentsRejectsNullExistingFragment(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "settings.json")
	fragmentPath := filepath.Join(dir, "settings.d", "tools.json")
	mainBefore := []byte(`{"version":19,"settings":{},"tools":{"jq":{"providers":[{"provider":"brew"}]}}}`)
	fragmentBefore := []byte("null\n")
	writeObjectShapeFixture(t, mainPath, string(mainBefore))
	writeObjectShapeFixture(t, fragmentPath, string(fragmentBefore))

	_, err := config.ExtractIncludeFragments(mainPath)
	requireJSONObjectError(t, err)
	for path, before := range map[string][]byte{mainPath: mainBefore, fragmentPath: fragmentBefore} {
		after, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(after) != string(before) {
			t.Fatalf("rejected extract changed %s: got %q, want %q", path, after, before)
		}
	}
}

func TestLoadAcceptsEmptyJSONObject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	writeObjectShapeFixture(t, path, "{}\n")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load empty object: %v", err)
	}
	if cfg.Version != config.CurrentVersion {
		t.Fatalf("Version = %d, want %d", cfg.Version, config.CurrentVersion)
	}
}

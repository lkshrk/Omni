package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestApplyLegacyAgentCleanupRollsBackEarlierWrites(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.json")
	second := filepath.Join(dir, "second.json")
	for path, raw := range map[string][]byte{first: []byte("first-old\n"), second: []byte("second-old\n")} {
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	changes := []legacyConfigChange{
		{legacyConfigFile: legacyConfigFile{path: first, raw: []byte("first-old\n")}, rendered: []byte("first-new\n")},
		{legacyConfigFile: legacyConfigFile{path: second, raw: []byte("second-old\n")}, rendered: []byte("second-new\n")},
	}
	writes := 0
	err := applyLegacyAgentCleanup(changes, func(path string, raw []byte) error {
		writes++
		if path == second {
			return errors.New("forced write failure")
		}
		return atomicWriteUnlocked(path, raw)
	})
	if err == nil {
		t.Fatal("cleanup succeeded despite forced failure")
	}
	if raw, readErr := os.ReadFile(first); readErr != nil || string(raw) != "first-old\n" {
		t.Fatalf("first file was not rolled back: %q, %v", raw, readErr)
	}
	if writes != 3 {
		t.Fatalf("writes = %d, want cleanup, failure, rollback", writes)
	}
}

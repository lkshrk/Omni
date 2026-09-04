package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestHasRemovedAgentConfigDetectsEmptyAgentsObject(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"agents":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	found, err := config.HasRemovedAgentConfig(path)
	if err != nil || !found {
		t.Fatalf("HasRemovedAgentConfig = %v, %v; want true, nil", found, err)
	}
}

func TestHasRemovedAgentConfigFollowsIncludes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	root := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(root, []byte(`{"$include":["fragment.json"],"version":22}`), 0o600); err != nil {
		t.Fatal(err)
	}
	clean := filepath.Join(dir, "fragment.json")
	if err := os.WriteFile(clean, []byte(`{"groups":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	found, err := config.HasRemovedAgentConfig(root)
	if err != nil || found {
		t.Fatalf("clean include = %v, %v; want false, nil", found, err)
	}
	if err := os.WriteFile(clean, []byte(`{"agents":{"mcp_servers":[]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	found, err = config.HasRemovedAgentConfig(root)
	if err != nil || !found {
		t.Fatalf("legacy include = %v, %v; want true, nil", found, err)
	}
}

package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPluginManifestEntry(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "marketplace.json")

	if got := pluginManifestEntry(manifestPath, "caveman"); got != (marketplaceManifestEntry{}) {
		t.Errorf("missing file: got %+v, want zero value", got)
	}

	if err := os.WriteFile(manifestPath, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := pluginManifestEntry(manifestPath, "caveman"); got != (marketplaceManifestEntry{}) {
		t.Errorf("malformed json: got %+v, want zero value", got)
	}

	content := `{"plugins":[{"name":"caveman","description":"Caveman commit messages.","version":"25d22f864ad6"},{"name":"other","description":"Other plugin."}]}`
	if err := os.WriteFile(manifestPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got := pluginManifestEntry(manifestPath, "caveman")
	if got.Description != "Caveman commit messages." || got.Version != "25d22f864ad6" {
		t.Errorf("found entry: got %+v", got)
	}
	other := pluginManifestEntry(manifestPath, "other")
	if other.Description != "Other plugin." || other.Version != "" {
		t.Errorf("entry without version: got %+v, want empty version", other)
	}
	if got := pluginManifestEntry(manifestPath, "nonexistent"); got != (marketplaceManifestEntry{}) {
		t.Errorf("name not listed: got %+v, want zero value", got)
	}
}

func TestPluginMarketplaceManifestPath(t *testing.T) {
	got := pluginMarketplaceManifestPath("/home/user", "caveman")
	want := filepath.Join("/home/user", ".claude", "plugins", "marketplaces", "caveman", ".claude-plugin", "marketplace.json")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

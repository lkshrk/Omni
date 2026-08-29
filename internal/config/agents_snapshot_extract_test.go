package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyAgentsFromSnapshotJoinsNamesToDefinitions(t *testing.T) {
	decls, _, err := LegacyAgentsFromSnapshot("testdata/apm-snapshot", "h1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := decls.MCPServers["gh"]; !ok {
		t.Fatalf("expected joined definition for gh, got %+v", decls.MCPServers)
	}
	if len(decls.Packages) != 1 {
		t.Fatalf("skills selection: %+v", decls.Packages)
	}
	if _, ok := decls.Packages["acme/skills-a"]; !ok {
		t.Fatalf("packages keyed by source: %+v", decls.Packages)
	}
	if len(decls.Plugins) != 2 || len(decls.Marketplaces) != 1 {
		t.Fatalf("plugins=%+v marketplaces=%+v", decls.Plugins, decls.Marketplaces)
	}
	if got := string(decls.MCPServers["docs"]); !strings.Contains(got, "${LITELLM_API}") {
		t.Fatalf("definition should be the verbatim snapshot payload: %s", got)
	}
}

func TestLegacyAgentsFromSnapshotRejectsAmbiguousPathEvidence(t *testing.T) {
	tests := []struct {
		name  string
		paths map[string]string
		want  string
	}{
		{
			name: "duplicate canonical original",
			paths: map[string]string{
				"omni-config-000.json": filepath.Join(t.TempDir(), "config", "..", "settings.json"),
				"omni-config-001.json": filepath.Join(t.TempDir(), "settings.json"),
			},
			want: "canonical original",
		},
		{
			name: "multiple marketplaces copies",
			paths: map[string]string{
				"omni-config-000.json":    filepath.Join(t.TempDir(), "settings.json"),
				"marketplaces.json":       filepath.Join(t.TempDir(), "a", "marketplaces.json"),
				"cache/marketplaces.json": filepath.Join(t.TempDir(), "b", "marketplaces.json"),
			},
			want: "multiple marketplaces.json",
		},
	}
	// Use one shared canonical original only in the duplicate case.
	base := filepath.Join(t.TempDir(), "settings.json")
	tests[0].paths["omni-config-000.json"] = filepath.Join(filepath.Dir(base), "x", "..", "settings.json")
	tests[0].paths["omni-config-001.json"] = base
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			raw, err := json.Marshal(test.paths)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "paths.json"), raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := LegacyAgentsFromSnapshot(dir, "h"); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q, got %v", test.want, err)
			}
		})
	}
}

func TestLegacyAgentsFromSnapshotRejectsUnsafeSnapshotFiles(t *testing.T) {
	t.Run("malicious key", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "paths.json"), []byte(`{"../evil":"/tmp/settings"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LegacyAgentsFromSnapshot(dir, "h"); err == nil || !strings.Contains(err.Error(), "invalid copied path") {
			t.Fatalf("malicious key accepted: %v", err)
		}
	})
	t.Run("symlink config", func(t *testing.T) {
		dir := t.TempDir()
		outside := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(outside, []byte(`{"hosts":{"h":[]}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(dir, "omni-config-000.json")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "paths.json"), []byte(`{"omni-config-000.json":"/tmp/settings"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LegacyAgentsFromSnapshot(dir, "h"); err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("symlink config accepted: %v", err)
		}
	})
	t.Run("oversized config", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "omni-config-000.json"), []byte(strings.Repeat(" ", maxSnapshotFileBytes+1)), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "paths.json"), []byte(`{"omni-config-000.json":"/tmp/settings"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LegacyAgentsFromSnapshot(dir, "h"); err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("oversized config accepted: %v", err)
		}
	})
}

func TestLegacyAgentsFromSnapshotRejectsDuplicateDefinitions(t *testing.T) {
	dir := t.TempDir()
	first := `{"agents":{"mcp_servers":[{"name":"dup","command":"one"}]},"groups":[],"hosts":{"h":[]}}`
	second := `{"agents":{"mcp_servers":[{"name":"dup","command":"two"}]}}`
	for name, body := range map[string]string{"omni-config-000.json": first, "omni-config-001.json": second} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "paths.json"), []byte(`{"omni-config-000.json":"/tmp/a","omni-config-001.json":"/tmp/b"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LegacyAgentsFromSnapshot(dir, "h"); err == nil || !strings.Contains(err.Error(), "duplicate agents.mcp_servers") {
		t.Fatalf("duplicate definition accepted: %v", err)
	}
}

func TestLegacyAgentsFromSnapshotIncludesImplicitHostGroup(t *testing.T) {
	decls, _, err := LegacyAgentsFromSnapshot("testdata/apm-snapshot", "h1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := decls.MCPServers["implicit-only"]; !ok {
		t.Fatal("implicit host group not collected")
	}
}

func TestLegacyAgentsFromSnapshotUnknownNameErrors(t *testing.T) {
	_, _, err := LegacyAgentsFromSnapshot("testdata/apm-snapshot", "broken-host")
	if err == nil {
		t.Fatal("expected join error")
	}
	if !strings.Contains(err.Error(), "ghost") || !strings.Contains(err.Error(), "broken") {
		t.Fatalf("error must name the missing entry and its group: %v", err)
	}
}

func TestLegacyAgentsFromSnapshotUnknownHostErrors(t *testing.T) {
	_, _, err := LegacyAgentsFromSnapshot("testdata/apm-snapshot", "h1-typo")
	if err == nil {
		t.Fatal("expected an error for a host the snapshot does not know")
	}
	if !strings.Contains(err.Error(), "h1-typo") || !strings.Contains(err.Error(), "bare, broken-host, h1") {
		t.Fatalf("error must name the host and the known hosts: %v", err)
	}
}

func TestLegacyAgentsFromSnapshotBareHostIsEmpty(t *testing.T) {
	decls, _, err := LegacyAgentsFromSnapshot("testdata/apm-snapshot", "bare")
	if err != nil || len(decls.MCPServers)+len(decls.Plugins)+len(decls.Marketplaces)+len(decls.Packages) != 0 {
		t.Fatalf("decls=%+v err=%v", decls, err)
	}
}

func TestLegacyAgentsFromSnapshotMissingDirErrors(t *testing.T) {
	if _, _, err := LegacyAgentsFromSnapshot("testdata/does-not-exist", "h1"); err == nil {
		t.Fatal("expected error for missing snapshot dir")
	}
}

func TestLegacyAgentsFromSnapshotPreservesOwnershipEvidence(t *testing.T) {
	_, evidence, err := LegacyAgentsFromSnapshot("testdata/apm-snapshot", "h1")
	if err != nil {
		t.Fatal(err)
	}
	wantRoot := "/home/example/.config/omni/settings.d/agents.json"
	if got := evidence.Paths[wantRoot]; got != "omni-config-000.json" {
		t.Fatalf("path evidence[%q] = %q", wantRoot, got)
	}
	if evidence.MarketplacesJSON != "marketplaces.json" {
		t.Fatalf("marketplaces evidence = %q", evidence.MarketplacesJSON)
	}
}

package config_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestCaptureAndCleanupLegacyAgentsConfig(t *testing.T) {
	t.Parallel()
	outer := t.TempDir()
	realDir := filepath.Join(outer, "dotfiles", "omni")
	if err := os.MkdirAll(filepath.Join(realDir, "settings.d", "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(realDir, "settings.json")
	agents := filepath.Join(realDir, "settings.d", "agents.json")
	groups := filepath.Join(realDir, "settings.d", "nested", "groups.json")
	rootRaw := []byte(`{
  "version": 24,
  "$include": ["settings.d/agents.json"],
  "hosts": {"testhost": ["work"]},
  "settings": {"dots_repo": "keep", "agents_use": ["claude"]},
  "unknown": {"bytes": "stay"}
}
`)
	agentsRaw := []byte(`{
  "$include": ["nested/groups.json"],
  "agents": {
    "packages": [{"source": "acme/skill"}],
    "mcp_servers": [{"name": "docs", "command": "docs serve"}],
    "plugins": [{"name": "acme-plugin", "source": "acme/plugin"}]
  },
  "host_settings": {"testhost": {"dots_disabled": false, "plugins_disabled": true}}
}
`)
	groupsRaw := []byte(`{
  "groups": [{"name": "work", "description": "keep", "skills": ["acme/skill"], "mcp_servers": ["docs"], "plugins": ["acme-plugin"]}]
}
`)
	for path, data := range map[string][]byte{root: rootRaw, agents: agentsRaw, groups: groupsRaw} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	linkDir := filepath.Join(outer, "config", "omni")
	if err := os.MkdirAll(linkDir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(linkDir, "settings.json")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}

	snapshot, err := config.CaptureLegacyAgentsSnapshot(link)
	if err != nil {
		t.Fatalf("CaptureLegacyAgentsSnapshot: %v", err)
	}
	defer func() { _ = os.Chmod(snapshot, 0o700) }()
	if filepath.Dir(snapshot) != realDir || !strings.HasPrefix(filepath.Base(snapshot), ".omni-apm-migration-backup-") {
		t.Fatalf("snapshot = %q, want unique directory beside resolved config", snapshot)
	}
	second, err := config.CaptureLegacyAgentsSnapshot(link)
	if err != nil || second == snapshot {
		t.Fatalf("second snapshot = %q, %v; want distinct directory", second, err)
	}
	defer func() { _ = os.Chmod(second, 0o700) }()
	manifestRaw, err := os.ReadFile(filepath.Join(snapshot, "paths.json"))
	if err != nil {
		t.Fatal(err)
	}
	var paths map[string]string
	if err := json.Unmarshal(manifestRaw, &paths); err != nil {
		t.Fatal(err)
	}
	wants := map[string][]byte{root: rootRaw, agents: agentsRaw, groups: groupsRaw}
	for copied, original := range paths {
		got, err := os.ReadFile(filepath.Join(snapshot, copied))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, wants[original]) {
			t.Fatalf("snapshot %s did not preserve exact bytes for %s", copied, original)
		}
	}
	decls, _, err := config.LegacyAgentsFromSnapshot(snapshot, "testhost")
	if err != nil {
		t.Fatalf("LegacyAgentsFromSnapshot: %v", err)
	}
	if len(decls.Packages) != 1 || len(decls.MCPServers) != 1 || len(decls.Plugins) != 1 {
		t.Fatalf("snapshot declarations = %#v", decls)
	}
	if err := config.CleanupLegacyAgentsConfigFromSnapshot(link, snapshot); err != nil {
		t.Fatalf("CleanupLegacyAgentsConfigFromSnapshot: %v", err)
	}
	if _, err := os.Stat(snapshot); err != nil {
		t.Fatalf("snapshot removed during cleanup: %v", err)
	}
	for _, path := range []string{root, agents, groups} {
		found, err := config.HasRemovedAgentConfig(path)
		if err != nil || found {
			t.Fatalf("HasRemovedAgentConfig(%s) = %v, %v; want false, nil", path, found, err)
		}
	}
	rootAfter, _ := os.ReadFile(root)
	if !bytes.Contains(rootAfter, []byte(`"dots_repo": "keep"`)) || !bytes.Contains(rootAfter, []byte(`"unknown"`)) {
		t.Fatalf("root cleanup lost unrelated config: %s", rootAfter)
	}
	agentsAfter, _ := os.ReadFile(agents)
	if bytes.Contains(agentsAfter, []byte(`"agents"`)) || bytes.Contains(agentsAfter, []byte(`plugins_disabled`)) || !bytes.Contains(agentsAfter, []byte(`"dots_disabled": false`)) {
		t.Fatalf("agents cleanup = %s", agentsAfter)
	}
	groupsAfter, _ := os.ReadFile(groups)
	if bytes.Contains(groupsAfter, []byte(`"skills"`)) || bytes.Contains(groupsAfter, []byte(`"mcp_servers"`)) || bytes.Contains(groupsAfter, []byte(`"plugins"`)) || !bytes.Contains(groupsAfter, []byte(`"description": "keep"`)) {
		t.Fatalf("groups cleanup = %s", groupsAfter)
	}
}

func TestLegacyAgentsMigrationRejectsUnsafeIncludes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		include string
		setup   func(t *testing.T, dir string)
		want    string
	}{
		{name: "traversal", include: "../outside.json", want: "traversal"},
		{
			name:    "symlink",
			include: "linked.json",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				target := filepath.Join(dir, "target.json")
				if err := os.WriteFile(target, []byte(`{"agents":{}}`), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(dir, "linked.json")); err != nil {
					t.Fatal(err)
				}
			},
			want: "symlink",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.setup != nil {
				tc.setup(t, dir)
			}
			root := filepath.Join(dir, "settings.json")
			if err := os.WriteFile(root, []byte(`{"$include":["`+tc.include+`"],"agents":{}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := config.CaptureLegacyAgentsSnapshot(root); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("CaptureLegacyAgentsSnapshot error = %v, want %q", err, tc.want)
			}
			if err := config.CleanupLegacyAgentsConfig(root); err == nil || !strings.Contains(err.Error(), "requires the snapshot") {
				t.Fatalf("CleanupLegacyAgentsConfig error = %v, want snapshot requirement", err)
			}
			body, _ := os.ReadFile(root)
			if !bytes.Contains(body, []byte(`"agents"`)) {
				t.Fatalf("unsafe cleanup mutated root: %s", body)
			}
		})
	}
}

func TestCleanupLegacyAgentsConfigRejectsChangeAfterCapture(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	root := filepath.Join(dir, "settings.json")
	original := []byte(`{"agents":{"mcp_servers":[{"name":"old"}]},"unknown":"keep"}`)
	if err := os.WriteFile(root, original, 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := config.CaptureLegacyAgentsSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(snapshot, 0o700) }()
	changed := []byte(`{"agents":{"mcp_servers":[{"name":"new"}]},"unknown":"user edit"}`)
	if err := os.WriteFile(root, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := config.CleanupLegacyAgentsConfigFromSnapshot(root, snapshot); err == nil || !strings.Contains(err.Error(), "changed after snapshot") {
		t.Fatalf("CleanupLegacyAgentsConfigFromSnapshot error = %v, want changed-after-snapshot refusal", err)
	}
	got, err := os.ReadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, changed) {
		t.Fatalf("refused cleanup changed live config: %s", got)
	}
}

func TestCaptureLegacyAgentsSnapshotCopiesExplicitLocalBundle(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bundle := filepath.Join(dir, "local-plugin")
	if err := os.MkdirAll(filepath.Join(bundle, "skills", "demo"), 0o700); err != nil {
		t.Fatal(err)
	}
	bundleFile := filepath.Join(bundle, "skills", "demo", "SKILL.md")
	bundleRaw := []byte("# exact local bundle\n")
	if err := os.WriteFile(bundleFile, bundleRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "settings.json")
	body := fmt.Sprintf(`{"hosts":{"testhost":["work"]},"groups":[{"name":"work","plugins":["local"]}],"agents":{"plugins":[{"name":"local","path":%q}]}}`, bundle)
	if err := os.WriteFile(root, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := config.CaptureLegacyAgentsSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = filepath.WalkDir(snapshot, func(path string, entry os.DirEntry, err error) error {
			if err == nil && entry.IsDir() {
				_ = os.Chmod(path, 0o700)
			}
			return nil
		})
	}()
	manifestRaw, err := os.ReadFile(filepath.Join(snapshot, "paths.json"))
	if err != nil {
		t.Fatal(err)
	}
	var paths map[string]string
	if err := json.Unmarshal(manifestRaw, &paths); err != nil {
		t.Fatal(err)
	}
	var copied string
	for rel, original := range paths {
		if original == bundle {
			copied = rel
		}
	}
	if copied == "" {
		t.Fatalf("paths.json did not map local bundle %q: %#v", bundle, paths)
	}
	got, err := os.ReadFile(filepath.Join(snapshot, copied, "skills", "demo", "SKILL.md"))
	if err != nil || !bytes.Equal(got, bundleRaw) {
		t.Fatalf("copied local bundle = %q, %v", got, err)
	}
	decls, evidence, err := config.LegacyAgentsFromSnapshot(snapshot, "testhost")
	if err != nil || len(decls.Plugins) != 1 || evidence.Paths[bundle] != copied {
		t.Fatalf("LegacyAgentsFromSnapshot local evidence = %#v, %#v, %v", decls, evidence, err)
	}
}

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

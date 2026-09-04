package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func renderFixtureDecls() config.LegacyAgentDecls {
	return config.LegacyAgentDecls{
		Packages: map[string]json.RawMessage{
			"acme/skills-a": json.RawMessage(`{"source":"acme/skills-a","ref":"v1.2.0","agents":["claude-code"]}`),
		},
		Plugins: map[string]json.RawMessage{
			"acme-plugin":   json.RawMessage(`{"name":"acme-plugin","marketplace":"acme-market","agents":["claude-code"]}`),
			"direct-plugin": json.RawMessage(`{"name":"direct-plugin","source":"acme/direct-plugin"}`),
		},
		Marketplaces: map[string]json.RawMessage{
			"acme-market": json.RawMessage(`{"name":"acme-market","source":"https://github.com/acme/market.git","agents":["claude-code"]}`),
		},
		MCPServers: map[string]json.RawMessage{
			"gh":   json.RawMessage(`{"name":"gh","transport":"stdio","command":"gh-mcp serve --stdio","env":["GH_TOKEN"],"agents":["claude-code"]}`),
			"docs": json.RawMessage(`{"name":"docs","transport":"http","url":"https://docs.example.test/mcp/","headers":{"x-api-key":"${LITELLM_API}"},"env_literal":{"DOCS_MODE":"readonly"},"agents":["claude-code","codex"]}`),
		},
	}
}

const goldenAPMTemplate = `name: omni-migrated
version: 1.0.0
dependencies:
  apm:
    - git: acme/skills-a
      ref: v1.2.0
      targets:
        - claude
    - name: acme-plugin
      marketplace: acme-market
      targets:
        - claude
    - git: acme/direct-plugin
  mcp:
    - name: docs
      registry: false
      transport: http
      url: https://docs.example.test/mcp/
      headers:
        x-api-key: ${env:LITELLM_API}
      env:
        DOCS_MODE: readonly
    - name: gh
      registry: false
      transport: stdio
      command: gh-mcp
      args:
        - serve
        - --stdio
      env:
        GH_TOKEN: ${env:GH_TOKEN}
targets:
  - claude
  - codex
`

func TestRenderAPMTemplateGolden(t *testing.T) {
	got, cmds, err := RenderAPMTemplate(renderFixtureDecls())
	if err != nil {
		t.Fatal(err)
	}
	if got != goldenAPMTemplate {
		t.Fatalf("apm.yml mismatch\n--- got ---\n%s\n--- want ---\n%s", got, goldenAPMTemplate)
	}
	want := []string{"apm marketplace add https://github.com/acme/market.git --name acme-market"}
	if len(cmds) != len(want) || cmds[0] != want[0] {
		t.Fatalf("marketplace commands: %q", cmds)
	}
}

func TestRenderAPMTemplateIsDeterministic(t *testing.T) {
	first, _, err := RenderAPMTemplate(renderFixtureDecls())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		next, _, err := RenderAPMTemplate(renderFixtureDecls())
		if err != nil {
			t.Fatal(err)
		}
		if next != first {
			t.Fatalf("render %d differs:\n%s\n---\n%s", i, next, first)
		}
	}
}

func TestRenderAPMTemplateEmptyDecls(t *testing.T) {
	got, cmds, err := RenderAPMTemplate(config.LegacyAgentDecls{})
	if err != nil {
		t.Fatal(err)
	}
	const want = "name: omni-migrated\nversion: 1.0.0\ndependencies: {}\n"
	if got != want {
		t.Fatalf("empty decls render:\n%q", got)
	}
	if len(cmds) != 0 {
		t.Fatalf("expected no marketplace commands, got %q", cmds)
	}
}

// Discovery must resolve the symlink first: the snapshot sits next to the target, not the link.
func TestDefaultSnapshotDirResolvesThroughSymlink(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "dotfiles")
	linkDir := filepath.Join(root, "config")
	for _, dir := range []string{realDir, linkDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	realSettings := filepath.Join(realDir, "settings.json")
	if err := os.WriteFile(realSettings, []byte(`{"version":22}`), 0o644); err != nil {
		t.Fatal(err)
	}
	linkSettings := filepath.Join(linkDir, "settings.json")
	if err := os.Symlink(realSettings, linkSettings); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	snapshot := filepath.Join(realDir, ".omni-apm-migration-backup-20260822T170211Z-abc")
	if err := os.MkdirAll(snapshot, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := (&App{ConfigPath: linkSettings}).defaultSnapshotDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != snapshot {
		t.Fatalf("got %q want %q", got, snapshot)
	}

	if err := os.MkdirAll(snapshot+"-second", 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := (&App{ConfigPath: linkSettings}).defaultSnapshotDir(); err == nil || !strings.Contains(err.Error(), "--snapshot") {
		t.Fatalf("ambiguous snapshots must ask for --snapshot, got %v", err)
	}

	if _, err := (&App{ConfigPath: filepath.Join(linkDir, "absent.json")}).defaultSnapshotDir(); err == nil {
		t.Fatal("expected error for unresolvable config path")
	}
}

func TestAgentsMigrateRendersOwnerWithoutOwnedStandaloneChildren(t *testing.T) {
	snapshot := t.TempDir()
	ownerRoot := filepath.Join(snapshot, "owner")
	mustWriteBundleFile(t, filepath.Join(ownerRoot, ".codex-plugin", "plugin.json"), `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"bundle-a","version":"1.0.0"}`)
	mustWriteBundleFile(t, filepath.Join(ownerRoot, "mcp.json"), `{"mcpServers":{"owned":{"type":"stdio","command":"node","args":["${PLUGIN_ROOT}/bin/server.js"],"cwd":"${PLUGIN_ROOT}"}}}`)
	mustWriteBundleFile(t, filepath.Join(ownerRoot, "bin", "server.js"), "process.exit(0)\n")
	mustWriteBundleFile(t, filepath.Join(ownerRoot, "skills", "review", "SKILL.md"), "---\nname: review\ndescription: test\n---\n")

	original := filepath.Join(t.TempDir(), "bundle-a")
	skillOriginal := filepath.Join(original, "skills", "review")
	configRaw := `{
  "agents": {
    "plugins": [{"name":"bundle-a","path":"` + original + `"}],
    "packages": [{"source":"` + skillOriginal + `"}],
    "mcp_servers": [
      {"name":"owned","transport":"stdio","command":"node ` + original + `/bin/server.js","cwd":"` + original + `"},
      {"name":"independent","transport":"stdio","command":"independent-mcp"}
    ]
  },
  "groups": [{"name":"g","plugins":["bundle-a"],"skills":["` + skillOriginal + `"],"mcp_servers":["owned","independent"]}],
  "hosts": {"h":["g"]}
}`
	mustWriteBundleFile(t, filepath.Join(snapshot, "omni-config-000.json"), configRaw)
	paths, err := json.Marshal(map[string]string{
		"omni-config-000.json": filepath.Join(t.TempDir(), "settings.json"),
		"owner":                original,
		"owner/skills/review":  skillOriginal,
	})
	if err != nil {
		t.Fatal(err)
	}
	mustWriteBundleFile(t, filepath.Join(snapshot, "paths.json"), string(paths))

	state := filepath.Join(t.TempDir(), "state")
	got, err := (&App{StateDir: state}).AgentsMigrate(t.Context(), "h", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"name: independent", "# suppressed: mcp owned owned by bundle-a", "# suppressed: package " + skillOriginal + " owned by bundle-a"} {
		if !strings.Contains(got, want) {
			t.Fatalf("migration output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "name: owned") || strings.Count(got, "path:") != 1 {
		t.Fatalf("owned children were emitted independently:\n%s", got)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("preview wrote wrapper state: %v", err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	root := filepath.Join(state, "agents-migration", "bundles")
	stale := filepath.Join(root, strings.Repeat("f", 64))
	unknown := filepath.Join(root, "unknown")
	temp := filepath.Join(root, ".omni-bundle-keep")
	for _, dir := range []string{stale, unknown, temp} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := (&App{StateDir: state}).AgentsMigrateWrite(t.Context(), "h", snapshot); err != nil {
		t.Fatal(err)
	}
	wrappers, err := filepath.Glob(filepath.Join(state, "agents-migration", "bundles", "*", "apm.yml"))
	if err != nil || len(wrappers) != 1 {
		t.Fatalf("published wrappers = %v, err = %v", wrappers, err)
	}
	for _, dir := range []string{stale, unknown, temp} {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("existing migration entry was removed: %s: %v", dir, err)
		}
	}
}

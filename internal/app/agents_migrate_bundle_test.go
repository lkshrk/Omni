package app

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestPlanAgentBundlesCollapsesOwnedMCPAndPreservesIndependent(t *testing.T) {
	snapshot := t.TempDir()
	root := filepath.Join(snapshot, "owner")
	mustWriteBundleFile(t, filepath.Join(root, ".claude-plugin", "plugin.json"), `{"name":"bundle-a","version":"1.0.0"}`)
	mustWriteBundleFile(t, filepath.Join(root, ".mcp.json"), `{"mcpServers":{"owned":{"type":"stdio","command":"node","args":["${CLAUDE_PLUGIN_ROOT}/bin/server.js"],"cwd":"${CLAUDE_PLUGIN_ROOT}","env":{"MODE":"${MODE}"}}}}`)
	mustWriteBundleFile(t, filepath.Join(root, "bin", "server.js"), "process.exit(0)\n")
	mustWriteBundleFile(t, filepath.Join(root, "skills", "review", "SKILL.md"), "---\nname: review\ndescription: test\n---\n")

	original := filepath.Join(t.TempDir(), "bundle-a")
	decls := config.LegacyAgentDecls{
		Plugins: map[string]json.RawMessage{
			"bundle-a": json.RawMessage(`{"name":"bundle-a","path":"` + original + `","agents":["claude-code"]}`),
		},
		MCPServers: map[string]json.RawMessage{
			"owned":       json.RawMessage(`{"name":"owned","transport":"stdio","command":"node ` + original + `/bin/server.js","cwd":"` + original + `","env_literal":{"MODE":"${MODE}"},"agents":["claude-code"]}`),
			"independent": json.RawMessage(`{"name":"independent","transport":"stdio","command":"independent-mcp"}`),
		},
	}
	evidence := config.LegacyAgentEvidence{SnapshotDir: snapshot, Paths: map[string]string{original: "owner"}}

	plan, err := planAgentBundles(decls, evidence, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Owners) != 1 || len(plan.Suppressed) != 1 {
		t.Fatalf("owners=%+v suppressed=%+v", plan.Owners, plan.Suppressed)
	}
	if _, ok := plan.Decls.MCPServers["owned"]; ok {
		t.Fatal("owned MCP remained standalone")
	}
	if _, ok := plan.Decls.MCPServers["independent"]; !ok {
		t.Fatal("independent MCP was removed")
	}

	first, _, err := renderAPMTemplatePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := renderAPMTemplatePlan(plan)
	if err != nil || second != first {
		t.Fatalf("render not deterministic: err=%v", err)
	}
	if strings.Count(first, "name: owned") != 0 || !strings.Contains(first, "name: independent") || strings.Count(first, "path:") != 1 {
		t.Fatalf("unexpected manifest:\n%s", first)
	}
}

func TestPlanAgentBundlesBlocksConflictsAndUnsafeRoots(t *testing.T) {
	tests := []struct {
		name string
		make func(*testing.T) (config.LegacyAgentDecls, config.LegacyAgentEvidence)
		want []string
	}{
		{
			name: "name only collision is not suppressed",
			make: func(t *testing.T) (config.LegacyAgentDecls, config.LegacyAgentEvidence) {
				decls, evidence, original := ownedBundleFixture(t, "bundle-a", "owned")
				decls.MCPServers["owned"] = json.RawMessage(`{"name":"owned","transport":"stdio","command":"unrelated"}`)
				_ = original
				return decls, evidence
			},
			want: []string{"collides", "without path evidence"},
		},
		{
			name: "explicit override",
			make: func(t *testing.T) (config.LegacyAgentDecls, config.LegacyAgentEvidence) {
				decls, evidence, original := ownedBundleFixture(t, "bundle-a", "owned")
				decls.MCPServers["owned"] = json.RawMessage(`{"name":"owned","transport":"stdio","command":"node ` + original + `/bin/other.js","cwd":"` + original + `"}`)
				return decls, evidence
			},
			want: []string{"explicitly overrides", "different definition"},
		},
		{
			name: "path traversal",
			make: func(t *testing.T) (config.LegacyAgentDecls, config.LegacyAgentEvidence) {
				original := filepath.Join(t.TempDir(), "bundle-a")
				return config.LegacyAgentDecls{Plugins: map[string]json.RawMessage{"bundle-a": json.RawMessage(`{"name":"bundle-a","path":"` + original + `"}`)}}, config.LegacyAgentEvidence{SnapshotDir: t.TempDir(), Paths: map[string]string{original: "../escape"}}
			},
			want: []string{"traversal"},
		},
		{
			name: "escaping symlink",
			make: func(t *testing.T) (config.LegacyAgentDecls, config.LegacyAgentEvidence) {
				decls, evidence, _ := ownedBundleFixture(t, "bundle-a", "owned")
				outside := filepath.Join(t.TempDir(), "outside")
				mustWriteBundleFile(t, outside, "outside")
				if err := os.Symlink(outside, filepath.Join(evidence.SnapshotDir, "bundle-a", "escape")); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
				return decls, evidence
			},
			want: []string{"symlink runtime entry"},
		},
		{
			name: "internal runtime symlink",
			make: func(t *testing.T) (config.LegacyAgentDecls, config.LegacyAgentEvidence) {
				decls, evidence, _ := ownedBundleFixture(t, "bundle-a", "owned")
				root := filepath.Join(evidence.SnapshotDir, "bundle-a")
				if err := os.Symlink(filepath.Join(root, "bin", "server.js"), filepath.Join(root, "bin", "server-link.js")); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
				return decls, evidence
			},
			want: []string{"symlink runtime entry"},
		},
		{
			name: "symlinked snapshot ancestor",
			make: func(t *testing.T) (config.LegacyAgentDecls, config.LegacyAgentEvidence) {
				snapshot := t.TempDir()
				realParent := filepath.Join(snapshot, "real")
				mustWriteBundleFile(t, filepath.Join(realParent, "owner", ".claude-plugin", "plugin.json"), `{"name":"bundle-a"}`)
				if err := os.Symlink(realParent, filepath.Join(snapshot, "alias")); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
				original := filepath.Join(t.TempDir(), "bundle-a")
				decls := config.LegacyAgentDecls{Plugins: map[string]json.RawMessage{"bundle-a": json.RawMessage(`{"name":"bundle-a","path":"` + original + `"}`)}}
				return decls, config.LegacyAgentEvidence{SnapshotDir: snapshot, Paths: map[string]string{original: "alias/owner"}}
			},
			want: []string{"symlinked ancestor"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decls, evidence := test.make(t)
			plan, err := planAgentBundles(decls, evidence, filepath.Join(t.TempDir(), "state"))
			if err == nil {
				t.Fatalf("expected blocker; plan=%+v", plan)
			}
			for _, want := range test.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q missing %q", err, want)
				}
			}
		})
	}
}

func TestPlanAgentBundlesBlocksTwoOwnersClaimingChild(t *testing.T) {
	snapshot := t.TempDir()
	decls := config.LegacyAgentDecls{Plugins: map[string]json.RawMessage{}}
	evidence := config.LegacyAgentEvidence{SnapshotDir: snapshot, Paths: map[string]string{}}
	for _, name := range []string{"bundle-a", "bundle-b"} {
		root := filepath.Join(snapshot, name)
		mustWriteBundleFile(t, filepath.Join(root, ".claude-plugin", "plugin.json"), `{"name":"`+name+`"}`)
		mustWriteBundleFile(t, filepath.Join(root, ".mcp.json"), `{"mcpServers":{"shared":{"type":"stdio","command":"node","args":["${CLAUDE_PLUGIN_ROOT}/bin/server.js"]}}}`)
		mustWriteBundleFile(t, filepath.Join(root, "bin", "server.js"), "process.exit(0)\n")
		original := filepath.Join(t.TempDir(), name)
		decls.Plugins[name] = json.RawMessage(`{"name":"` + name + `","path":"` + original + `"}`)
		evidence.Paths[original] = name
	}

	_, err := planAgentBundles(decls, evidence, filepath.Join(t.TempDir(), "state"))
	if err == nil || !strings.Contains(err.Error(), "bundle-a, bundle-b") {
		t.Fatalf("expected both owners in conflict: %v", err)
	}
}

func TestSnapshotFileRejectsSymlinkedAncestor(t *testing.T) {
	snapshot := t.TempDir()
	realDir := filepath.Join(snapshot, "real")
	mustWriteBundleFile(t, filepath.Join(realDir, "marketplaces.json"), `{}`)
	if err := os.Symlink(realDir, filepath.Join(snapshot, "alias")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := secureSnapshotFile(snapshot, "alias/marketplaces.json"); err == nil || !strings.Contains(err.Error(), "symlinked ancestor") {
		t.Fatalf("symlinked file ancestor accepted: %v", err)
	}
}

func TestClaudeRelativeRuntimeRequiresWrapper(t *testing.T) {
	decls, evidence, _ := ownedBundleFixture(t, "bundle-a", "owned")
	root := filepath.Join(evidence.SnapshotDir, "bundle-a")
	mustWriteBundleFile(t, filepath.Join(root, ".mcp.json"), `{"mcpServers":{"owned":{"type":"stdio","command":"node","args":["bin/server.js"],"cwd":"."}}}`)
	plan, err := planAgentBundles(decls, evidence, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Wrappers) != 1 || plan.Owners[0].Wrapper == nil {
		t.Fatalf("plain relative runtime was treated as lossless direct layout: %+v", plan.Owners)
	}
}

func TestPlanAgentBundlesRedactsLiteralSecretsAndWritesNothing(t *testing.T) {
	decls, evidence, _ := ownedBundleFixture(t, "bundle-a", "owned")
	root := filepath.Join(evidence.SnapshotDir, "bundle-a")
	mustWriteBundleFile(t, filepath.Join(root, ".mcp.json"), `{"mcpServers":{"owned":{"type":"stdio","command":"node","args":["${CLAUDE_PLUGIN_ROOT}/bin/server.js"],"env":{"API_TOKEN":"do-not-print-this"}}}}`)
	state := filepath.Join(t.TempDir(), "state")
	plan, err := planAgentBundles(decls, evidence, state)
	if err == nil || !strings.Contains(err.Error(), "API_TOKEN") || strings.Contains(err.Error(), "do-not-print-this") {
		t.Fatalf("redacted blocker = %v", err)
	}
	if err := materializeAgentBundleWrappers(plan); err == nil {
		t.Fatal("blocked plan materialized wrappers")
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("blocked plan wrote state: %v", err)
	}
}

func TestMaterializeAgentBundleWrapperIsDeterministicAndRunnable(t *testing.T) {
	snapshot := t.TempDir()
	root := filepath.Join(snapshot, "codex-owner")
	mustWriteBundleFile(t, filepath.Join(root, ".codex-plugin", "plugin.json"), `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"codex-owner","version":"1.0.0"}`)
	mustWriteBundleFile(t, filepath.Join(root, "mcp.json"), `{"mcpServers":{"handshake":{"type":"stdio","command":"sh","args":["bin/server.sh","--config=bin/config.json"],"cwd":".","env":{"MODE":"${MODE}"}}}}`)
	mustWriteBundleFile(t, filepath.Join(root, "bin", "config.json"), `{}`)
	mustWriteBundleFile(t, filepath.Join(root, "bin", "server.sh"), "#!/bin/sh\n[ \"$1\" = \"--config=runtime/bin/config.json\" ] && [ -f \"${1#*=}\" ] || exit 7\nprintf '{\"jsonrpc\":\"2.0\"}\\n'\n")
	mustWriteBundleFile(t, filepath.Join(root, "bin", "hook.js"), "process.exit(0)\n")
	mustWriteBundleFile(t, filepath.Join(root, "bin", "hook.json"), `{}`)
	mustWriteBundleFile(t, filepath.Join(root, "hooks", "hooks.json"), `{"hooks":{"Start":[{"hooks":[{"type":"command","command":"node ./bin/hook.js"},{"type":"command","command":"node --config=bin/hook.json"}]}]}}`)
	original := filepath.Join(t.TempDir(), "codex-owner")
	decls := config.LegacyAgentDecls{Plugins: map[string]json.RawMessage{"codex-owner": json.RawMessage(`{"name":"codex-owner","path":"` + original + `"}`)}}
	evidence := config.LegacyAgentEvidence{SnapshotDir: snapshot, Paths: map[string]string{original: "codex-owner"}}
	state := filepath.Join(t.TempDir(), "state")

	first, err := planAgentBundles(decls, evidence, state)
	if err != nil {
		t.Fatal(err)
	}
	second, err := planAgentBundles(decls, evidence, state)
	if err != nil || len(first.Wrappers) != 1 || first.Wrappers[0].Path != second.Wrappers[0].Path {
		t.Fatalf("wrapper plan not deterministic: err=%v first=%+v second=%+v", err, first.Wrappers, second.Wrappers)
	}
	if err := materializeAgentBundleWrappers(first); err != nil {
		t.Fatal(err)
	}
	if err := materializeAgentBundleWrappers(first); err != nil {
		t.Fatalf("second materialization: %v", err)
	}
	wrapper := first.Wrappers[0].Path
	manifest, err := os.ReadFile(filepath.Join(wrapper, "apm.yml"))
	if err != nil || !strings.Contains(string(manifest), "runtime/bin/server.sh") {
		t.Fatalf("wrapper manifest: %v\n%s", err, manifest)
	}
	command := exec.Command("sh", filepath.Join(wrapper, "runtime", "bin", "server.sh"), "--config=runtime/bin/config.json")
	command.Dir = wrapper
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), `"jsonrpc":"2.0"`) {
		t.Fatalf("handshake: %v %s", err, output)
	}
	hooks, err := os.ReadFile(filepath.Join(wrapper, "hooks", "hooks.json"))
	if err != nil || !strings.Contains(string(hooks), "node runtime/bin/hook.js") || !strings.Contains(string(hooks), "node --config=runtime/bin/hook.json") {
		t.Fatalf("rewritten hook: %v %s", err, hooks)
	}
	if err := os.Chmod(filepath.Join(wrapper, "runtime", "bin", "server.sh"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := materializeAgentBundleWrappers(first); err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("mode-only corruption was accepted: %v", err)
	}
}

func TestPrepareAgentBundleWrappersPreparesAllBeforePublishing(t *testing.T) {
	state := t.TempDir()
	first := testMigrationWrapper(state, "a", "one")
	second := testMigrationWrapper(state, "b", "two")
	second.Files = []agentBundleFile{{Source: filepath.Join(t.TempDir(), "missing"), Dest: "runtime/missing", Size: 1, Hash: strings.Repeat("0", 64)}}
	_, err := prepareAgentBundleWrappers(agentBundlePlan{Wrappers: []agentBundleWrapper{first, second}})
	if err == nil {
		t.Fatal("prepare accepted missing second wrapper source")
	}
	root := filepath.Join(state, "agents-migration", "bundles")
	entries, readErr := os.ReadDir(root)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("failed preparation left wrapper state: %+v", entries)
	}
}

func TestBundleWrapperRebasesArgsAgainstDeclaredCwd(t *testing.T) {
	snapshot := t.TempDir()
	root := filepath.Join(snapshot, "owner")
	mustWriteBundleFile(t, filepath.Join(root, ".codex-plugin", "plugin.json"), `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"owner"}`)
	mustWriteBundleFile(t, filepath.Join(root, "mcp.json"), `{"mcpServers":{"server":{"type":"stdio","command":"sh","args":["server.sh"],"cwd":"subdir"}}}`)
	mustWriteBundleFile(t, filepath.Join(root, ".lsp.json"), `{"language":{"command":"sh","args":["--config=subdir/lsp.json"],"cwd":".","extensionToLanguage":{".x":"x"}}}`)
	mustWriteBundleFile(t, filepath.Join(root, "subdir", "server.sh"), "printf cwd-ok\\n\n")
	mustWriteBundleFile(t, filepath.Join(root, "subdir", "lsp.json"), `{}`)
	original := filepath.Join(t.TempDir(), "owner")
	decls := config.LegacyAgentDecls{Plugins: map[string]json.RawMessage{"owner": json.RawMessage(`{"name":"owner","path":"` + original + `"}`)}}
	plan, err := planAgentBundles(decls, config.LegacyAgentEvidence{SnapshotDir: snapshot, Paths: map[string]string{original: "owner"}}, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	if err := materializeAgentBundleWrappers(plan); err != nil {
		t.Fatal(err)
	}
	wrapper := plan.Wrappers[0].Path
	manifest, err := os.ReadFile(filepath.Join(wrapper, "apm.yml"))
	if err != nil || !strings.Contains(string(manifest), "cwd: runtime/subdir") || !strings.Contains(string(manifest), "- server.sh") || !strings.Contains(string(manifest), "--config=runtime/subdir/lsp.json") {
		t.Fatalf("cwd-relative manifest: %v\n%s", err, manifest)
	}
	command := exec.Command("sh", "server.sh")
	command.Dir = filepath.Join(wrapper, "runtime", "subdir")
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "cwd-ok") {
		t.Fatalf("cwd-relative runtime failed: %v %s", err, output)
	}
}

func TestPlanAgentBundlesOwnerLimitReturnsBeforeResolution(t *testing.T) {
	decls := config.LegacyAgentDecls{Plugins: map[string]json.RawMessage{}}
	for i := 0; i <= maxBundleOwners; i++ {
		name := fmt.Sprintf("owner-%03d", i)
		decls.Plugins[name] = json.RawMessage(`{"name":"` + name + `","path":"/missing/` + name + `"}`)
	}
	_, err := planAgentBundles(decls, config.LegacyAgentEvidence{}, filepath.Join(t.TempDir(), "state"))
	if err == nil || err.Error() != fmt.Sprintf("selected owners exceed limit %d", maxBundleOwners) {
		t.Fatalf("owner cap did not return before resolution: %v", err)
	}
}

func TestMarketplaceEvidenceUsesExactRegistrationCatalogAndRoot(t *testing.T) {
	snapshot := t.TempDir()
	marketOriginal := filepath.Join(t.TempDir(), "market")
	pluginOriginal := filepath.Join(marketOriginal, "plugins", "bundle-a")
	mustWriteBundleFile(t, filepath.Join(snapshot, "marketplaces.json"), `{"marketplaces":[{"name":"acme","owner":"acme","repo":"market","ref":"v1"}]}`)
	mustWriteBundleFile(t, filepath.Join(snapshot, "catalog.json"), `{"name":"acme","owner":{"name":"acme"},"plugins":[{"name":"bundle-a","source":"plugins/bundle-a","ref":"v1"}]}`)
	mustWriteBundleFile(t, filepath.Join(snapshot, "wrong-catalog.json"), `{"name":"other","owner":{"name":"acme"},"plugins":[{"name":"bundle-a","source":"/wrong"}]}`)
	mustWriteBundleFile(t, filepath.Join(snapshot, "owner", ".claude-plugin", "plugin.json"), `{"name":"bundle-a"}`)
	evidence := config.LegacyAgentEvidence{
		SnapshotDir:      snapshot,
		MarketplacesJSON: "marketplaces.json",
		Paths: map[string]string{
			pluginOriginal: "owner",
			filepath.Join(t.TempDir(), ".apm", "cache", "marketplace", "acme.json"):  "catalog.json",
			filepath.Join(t.TempDir(), ".apm", "cache", "marketplace", "other.json"): "wrong-catalog.json",
		},
	}
	decls := config.LegacyAgentDecls{
		Plugins:      map[string]json.RawMessage{"bundle-a": json.RawMessage(`{"name":"bundle-a","marketplace":"acme","owner":"acme","ref":"v1"}`)},
		Marketplaces: map[string]json.RawMessage{"acme": json.RawMessage(`{"name":"acme","source":"` + marketOriginal + `","ref":"v1"}`)},
	}
	plan, err := planAgentBundles(decls, evidence, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Owners) != 1 || plan.Owners[0].Original != pluginOriginal {
		t.Fatalf("exact marketplace root not selected: %+v", plan.Owners)
	}
}

func TestMarketplaceHTTPSIdentityResolvesCopiedRelativePluginRoot(t *testing.T) {
	snapshot := t.TempDir()
	marketCopy := filepath.Join(snapshot, "market-root")
	mustWriteBundleFile(t, filepath.Join(marketCopy, "plugins", "bundle-a", ".claude-plugin", "plugin.json"), `{"name":"bundle-a"}`)
	mustWriteBundleFile(t, filepath.Join(marketCopy, "marketplace.json"), `{"name":"acme","owner":{"name":"acme"},"plugins":[{"name":"bundle-a","source":"plugins/bundle-a","ref":"v1"}]}`)
	mustWriteBundleFile(t, filepath.Join(snapshot, "marketplaces.json"), `{"marketplaces":[{"name":"acme","owner":"acme","repo":"market","ref":"v1"}]}`)
	mustWriteBundleFile(t, filepath.Join(snapshot, "catalog.json"), `{"name":"acme","owner":{"name":"acme"},"plugins":[{"name":"bundle-a","source":"plugins/bundle-a","ref":"v1"}]}`)
	marketOriginal := filepath.Join(t.TempDir(), "cached-market")
	catalogOriginal := filepath.Join(t.TempDir(), ".apm", "cache", "marketplace", "acme.json")
	evidence := config.LegacyAgentEvidence{
		SnapshotDir:      snapshot,
		MarketplacesJSON: "marketplaces.json",
		Paths: map[string]string{
			marketOriginal:  "market-root",
			catalogOriginal: "catalog.json",
		},
	}
	decls := config.LegacyAgentDecls{
		Plugins:      map[string]json.RawMessage{"bundle-a": json.RawMessage(`{"name":"bundle-a","marketplace":"acme","owner":"acme","ref":"v1"}`)},
		Marketplaces: map[string]json.RawMessage{"acme": json.RawMessage(`{"name":"acme","source":"https://github.com/acme/market.git","ref":"v1"}`)},
	}
	plan, err := planAgentBundles(decls, evidence, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	wantRoot := filepath.Join(marketCopy, "plugins", "bundle-a")
	if len(plan.Owners) != 1 || plan.Owners[0].Root != wantRoot {
		t.Fatalf("HTTPS marketplace identity did not resolve copied relative root: %+v", plan.Owners)
	}
}

func TestMarketplaceRelativeSourceRejectsUnrelatedSameNamePlugin(t *testing.T) {
	snapshot := t.TempDir()
	mustWriteBundleFile(t, filepath.Join(snapshot, "unrelated", ".claude-plugin", "plugin.json"), `{"name":"bundle-a"}`)
	mustWriteBundleFile(t, filepath.Join(snapshot, "marketplaces.json"), `{"marketplaces":[{"name":"acme","owner":"acme","repo":"market","ref":"v1"}]}`)
	mustWriteBundleFile(t, filepath.Join(snapshot, "catalog.json"), `{"name":"acme","owner":{"name":"acme"},"plugins":[{"name":"bundle-a","source":"plugins/bundle-a","ref":"v1"}]}`)
	evidence := config.LegacyAgentEvidence{
		SnapshotDir:      snapshot,
		MarketplacesJSON: "marketplaces.json",
		Paths: map[string]string{
			filepath.Join(t.TempDir(), "unrelated-plugin"):                          "unrelated",
			filepath.Join(t.TempDir(), ".apm", "cache", "marketplace", "acme.json"): "catalog.json",
		},
	}
	decls := config.LegacyAgentDecls{
		Plugins:      map[string]json.RawMessage{"bundle-a": json.RawMessage(`{"name":"bundle-a","marketplace":"acme","owner":"acme","ref":"v1"}`)},
		Marketplaces: map[string]json.RawMessage{"acme": json.RawMessage(`{"name":"acme","source":"https://github.com/acme/market.git","ref":"v1"}`)},
	}
	_, err := planAgentBundles(decls, evidence, filepath.Join(t.TempDir(), "state"))
	if err == nil || !strings.Contains(err.Error(), "0 copied marketplace/plugin roots") {
		t.Fatalf("unrelated same-name plugin was accepted: %v", err)
	}
}

func TestMarketplaceRootProofRejectsForgedSymlinkManifest(t *testing.T) {
	snapshot := t.TempDir()
	marketCopy := filepath.Join(snapshot, "market-root")
	mustWriteBundleFile(t, filepath.Join(marketCopy, "plugins", "bundle-a", ".claude-plugin", "plugin.json"), `{"name":"bundle-a"}`)
	outside := filepath.Join(t.TempDir(), "marketplace.json")
	mustWriteBundleFile(t, outside, `{"name":"acme","owner":{"name":"acme"},"plugins":[{"name":"bundle-a","source":"plugins/bundle-a"}]}`)
	if err := os.MkdirAll(marketCopy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(marketCopy, "marketplace.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	mustWriteBundleFile(t, filepath.Join(snapshot, "marketplaces.json"), `{"marketplaces":[{"name":"acme","owner":"acme","repo":"market"}]}`)
	mustWriteBundleFile(t, filepath.Join(snapshot, "catalog.json"), `{"name":"acme","owner":{"name":"acme"},"plugins":[{"name":"bundle-a","source":"plugins/bundle-a"}]}`)
	evidence := config.LegacyAgentEvidence{
		SnapshotDir:      snapshot,
		MarketplacesJSON: "marketplaces.json",
		Paths: map[string]string{
			filepath.Join(t.TempDir(), "cached-market"):                             "market-root",
			filepath.Join(t.TempDir(), ".apm", "cache", "marketplace", "acme.json"): "catalog.json",
		},
	}
	decls := config.LegacyAgentDecls{
		Plugins:      map[string]json.RawMessage{"bundle-a": json.RawMessage(`{"name":"bundle-a","marketplace":"acme","owner":"acme"}`)},
		Marketplaces: map[string]json.RawMessage{"acme": json.RawMessage(`{"name":"acme","source":"https://github.com/acme/market.git"}`)},
	}
	_, err := planAgentBundles(decls, evidence, filepath.Join(t.TempDir(), "state"))
	if err == nil || !strings.Contains(err.Error(), "symlinked ancestor") {
		t.Fatalf("forged marketplace symlink was accepted: %v", err)
	}
}

func TestMarketplaceRepoIdentityNormalizesGitSpellings(t *testing.T) {
	for _, source := range []string{
		"https://github.com/acme/market.git",
		"git://github.com/acme/market.git",
		"ssh://git@github.com/acme/market.git",
		"git@github.com:acme/market.git",
		"acme/market",
	} {
		if !sameSourceIdentity(source, "acme/market") {
			t.Errorf("source identity did not normalize: %q", source)
		}
	}
}

func TestOwnerPathIdentityUsesWholeSegments(t *testing.T) {
	owner := agentBundleOwner{Original: filepath.Join(t.TempDir(), "owner"), Root: filepath.Join(t.TempDir(), "owner")}
	sibling := owner.Original + "2/bin/server"
	if pathWithin(sibling, owner.Original) || ownerValueReferencesRoot(sibling, owner) {
		t.Fatalf("prefix sibling treated as owned: %q", sibling)
	}
	if got := normalizeOwnerString(sibling, owner); got != filepath.ToSlash(sibling) {
		t.Fatalf("prefix sibling rewritten: %q", got)
	}
	blockers := validateBundleRuntime(owner, "mcp", "sibling", sibling, "", nil)
	if len(blockers) == 0 || !strings.Contains(blockers[0], "absolute child path") {
		t.Fatalf("prefix sibling absolute path accepted: %v", blockers)
	}
}

func TestMarketplaceCatalogDiscoveryBoundsAndChargesReads(t *testing.T) {
	index := `{"marketplaces":[{"name":"acme","owner":"acme","repo":"market"}]}`
	entry := legacyEntry{Name: "bundle", Marketplace: "acme", Owner: "acme"}
	marketplace := legacyEntry{Name: "acme", Source: "acme/market"}

	t.Run("candidate cap before reads", func(t *testing.T) {
		snapshot := t.TempDir()
		mustWriteBundleFile(t, filepath.Join(snapshot, "marketplaces.json"), index)
		evidence := config.LegacyAgentEvidence{SnapshotDir: snapshot, MarketplacesJSON: "marketplaces.json", Paths: map[string]string{}}
		for i := 0; i <= maxMarketplaceCatalogs; i++ {
			original := filepath.Join(snapshot, "original", "cache", "marketplace", fmt.Sprintf("%04d.json", i))
			evidence.Paths[original] = fmt.Sprintf("missing-%04d.json", i)
		}
		_, err := marketplaceEvidenceRoots("bundle", entry, marketplace, evidence, &bundleScanBudget{})
		if err == nil || !strings.Contains(err.Error(), "candidate limit") {
			t.Fatalf("candidate cap: %v", err)
		}
	})

	t.Run("manifest byte budget", func(t *testing.T) {
		snapshot := t.TempDir()
		mustWriteBundleFile(t, filepath.Join(snapshot, "marketplaces.json"), index)
		catalog := `{"name":"acme","owner":{"name":"acme"},"plugins":[]}`
		mustWriteBundleFile(t, filepath.Join(snapshot, "catalog.json"), catalog)
		original := filepath.Join(t.TempDir(), ".apm", "cache", "marketplace", "acme.json")
		evidence := config.LegacyAgentEvidence{SnapshotDir: snapshot, MarketplacesJSON: "marketplaces.json", Paths: map[string]string{original: "catalog.json"}}
		budget := &bundleScanBudget{manifestBytes: maxAllManifestBytes - int64(len(index)) - 1}
		_, err := marketplaceEvidenceRoots("bundle", entry, marketplace, evidence, budget)
		if err == nil || !strings.Contains(err.Error(), "byte limit") {
			t.Fatalf("catalog read was not charged: %v", err)
		}
	})

	t.Run("oversized catalog", func(t *testing.T) {
		snapshot := t.TempDir()
		mustWriteBundleFile(t, filepath.Join(snapshot, "marketplaces.json"), index)
		mustWriteBundleFile(t, filepath.Join(snapshot, "catalog.json"), strings.Repeat(" ", maxBundleManifestBytes+1))
		original := filepath.Join(t.TempDir(), ".apm", "cache", "marketplace", "acme.json")
		evidence := config.LegacyAgentEvidence{SnapshotDir: snapshot, MarketplacesJSON: "marketplaces.json", Paths: map[string]string{original: "catalog.json"}}
		_, err := marketplaceEvidenceRoots("bundle", entry, marketplace, evidence, &bundleScanBudget{})
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("oversized catalog skipped: %v", err)
		}
	})
}

func TestPlanAgentBundlesBlocksMissingRelativeRuntimeAndServiceSchema(t *testing.T) {
	snapshot := t.TempDir()
	root := filepath.Join(snapshot, "owner")
	mustWriteBundleFile(t, filepath.Join(root, "apm.yml"), `name: owner
version: 1.0.0
includes: [missing-runtime]
unsupportedTop: true
dependencies:
  mcp:
    - name: unsafe-mcp
      registry: false
      transport: stdio
      command: node
      args: [bin/missing.js]
      env:
        API_TOKEN: literal-mcp-secret
  lsp:
    - name: unsafe-lsp
      command: bin/missing-lsp
      env:
        PASSWORD: literal-lsp-secret
      extensionToLanguage:
        .go: go
      unsupportedNative: true
`)
	original := filepath.Join(t.TempDir(), "owner")
	decls := config.LegacyAgentDecls{Plugins: map[string]json.RawMessage{"owner": json.RawMessage(`{"name":"owner","path":"` + original + `"}`)}}
	evidence := config.LegacyAgentEvidence{SnapshotDir: snapshot, Paths: map[string]string{original: "owner"}}
	_, err := planAgentBundles(decls, evidence, filepath.Join(t.TempDir(), "state"))
	if err == nil {
		t.Fatal("expected service blockers")
	}
	for _, want := range []string{"API_TOKEN", "PASSWORD", "unsupportedNative", "unsupportedTop", "missing-runtime", "missing or escaping runtime path"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
	}
	for _, secret := range []string{"literal-mcp-secret", "literal-lsp-secret"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("secret leaked in diagnostic: %q", err)
		}
	}
}

func TestBundleWrapperHashIncludesExecutableMode(t *testing.T) {
	makePlan := func(t *testing.T, mode os.FileMode) agentBundlePlan {
		t.Helper()
		snapshot := t.TempDir()
		root := filepath.Join(snapshot, "owner")
		mustWriteBundleFile(t, filepath.Join(root, ".codex-plugin", "plugin.json"), `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"owner"}`)
		mustWriteBundleFile(t, filepath.Join(root, "mcp.json"), `{"mcpServers":{"server":{"type":"stdio","command":"bin/server"}}}`)
		mustWriteBundleFile(t, filepath.Join(root, "bin", "server"), "same bytes\n")
		if err := os.Chmod(filepath.Join(root, "bin", "server"), mode); err != nil {
			t.Fatal(err)
		}
		original := filepath.Join(t.TempDir(), "owner")
		decls := config.LegacyAgentDecls{Plugins: map[string]json.RawMessage{"owner": json.RawMessage(`{"name":"owner","path":"` + original + `"}`)}}
		plan, err := planAgentBundles(decls, config.LegacyAgentEvidence{SnapshotDir: snapshot, Paths: map[string]string{original: "owner"}}, filepath.Join(t.TempDir(), "state"))
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	executable := makePlan(t, 0o755)
	regular := makePlan(t, 0o644)
	if executable.Wrappers[0].Hash == regular.Wrappers[0].Hash {
		t.Fatal("wrapper hash ignored executable mode")
	}
}

func TestBundlePlannerFinalHardeningContracts(t *testing.T) {
	t.Run("direct apm absolute runtime becomes wrapper", func(t *testing.T) {
		snapshot := t.TempDir()
		root := filepath.Join(snapshot, "owner")
		original := filepath.Join(t.TempDir(), "owner")
		mustWriteBundleFile(t, filepath.Join(root, "apm.yml"), "name: owner\nversion: 1.0.0\ndependencies:\n  mcp:\n    - name: server\n      registry: false\n      transport: stdio\n      command: node\n      args: ["+original+"/bin/server.js]\n")
		mustWriteBundleFile(t, filepath.Join(root, "bin", "server.js"), "process.exit(0)\n")
		decls := config.LegacyAgentDecls{Plugins: map[string]json.RawMessage{"owner": json.RawMessage(`{"name":"owner","path":"` + original + `"}`)}}
		plan, err := planAgentBundles(decls, config.LegacyAgentEvidence{SnapshotDir: snapshot, Paths: map[string]string{original: "owner"}}, filepath.Join(t.TempDir(), "state"))
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Wrappers) != 1 || !strings.Contains(string(plan.Wrappers[0].Manifest), "runtime/bin/server.js") {
			t.Fatalf("apm.yml owner was not wrapped: %+v", plan.Wrappers)
		}
	})
	t.Run("HOME placeholder and malformed fields block", func(t *testing.T) {
		decls, evidence, _ := ownedBundleFixture(t, "owner", "server")
		root := filepath.Join(evidence.SnapshotDir, "owner")
		mustWriteBundleFile(t, filepath.Join(root, ".mcp.json"), `{"mcpServers":{"server":{"type":"stdio","command":"node","args":["${HOME}/server.js",7],"env":{"API_TOKEN":"prefix ${TOKEN}"}}}}`)
		_, err := planAgentBundles(decls, evidence, filepath.Join(t.TempDir(), "state"))
		if err == nil || !strings.Contains(err.Error(), "string") || !strings.Contains(err.Error(), "API_TOKEN") {
			t.Fatalf("malformed or mixed secret accepted: %v", err)
		}
	})
	t.Run("HOME path placeholder blocks", func(t *testing.T) {
		decls, evidence, _ := ownedBundleFixture(t, "owner", "server")
		root := filepath.Join(evidence.SnapshotDir, "owner")
		mustWriteBundleFile(t, filepath.Join(root, ".mcp.json"), `{"mcpServers":{"server":{"type":"stdio","command":"node","args":["${HOME}/server.js"]}}}`)
		_, err := planAgentBundles(decls, evidence, filepath.Join(t.TempDir(), "state"))
		if err == nil || !strings.Contains(err.Error(), "unsupported environment path placeholder") {
			t.Fatalf("HOME path accepted: %v", err)
		}
	})
	t.Run("comment injection blocks", func(t *testing.T) {
		if _, err := decodeLegacyEntry(json.RawMessage(`{"name":"bad\n# injected"}`), "plugin", "bad"); err == nil {
			t.Fatal("comment injection accepted")
		}
	})
	t.Run("duplicate normalized names block", func(t *testing.T) {
		snapshot := t.TempDir()
		decls := config.LegacyAgentDecls{Plugins: map[string]json.RawMessage{}}
		evidence := config.LegacyAgentEvidence{SnapshotDir: snapshot, Paths: map[string]string{}}
		for i, name := range []string{"a/b", "a-b"} {
			copyName := fmt.Sprintf("owner-%d", i)
			mustWriteBundleFile(t, filepath.Join(snapshot, copyName, ".claude-plugin", "plugin.json"), `{"name":"`+name+`"}`)
			original := filepath.Join(t.TempDir(), copyName)
			decls.Plugins[name] = json.RawMessage(`{"name":"` + name + `","path":"` + original + `"}`)
			evidence.Paths[original] = copyName
		}
		_, err := planAgentBundles(decls, evidence, filepath.Join(t.TempDir(), "state"))
		if err == nil || !strings.Contains(err.Error(), "normalized package name") {
			t.Fatalf("duplicate normalized names accepted: %v", err)
		}
	})
}

func TestWrapperPreservesSourceAPMManifestSemantics(t *testing.T) {
	snapshot := t.TempDir()
	root := filepath.Join(snapshot, "owner")
	original := filepath.Join(t.TempDir(), "owner")
	manifest := `name: source-owner
version: 2.0.0
type: hybrid
includes: [assets]
allowExecutables:
  source-owner@2.0.0:
    hooks: true
scripts:
  build: node ./bin/build.js
dependencies:
  apm:
    - remote/pkg
    - path: ./child
devDependencies:
  apm:
    - dev/pkg
`
	mustWriteBundleFile(t, filepath.Join(root, "apm.yml"), manifest)
	mustWriteBundleFile(t, filepath.Join(root, "assets", "a.txt"), "a")
	mustWriteBundleFile(t, filepath.Join(root, "bin", "build.js"), "process.exit(0)\n")
	mustWriteBundleFile(t, filepath.Join(root, "child", "apm.yml"), "name: child\nversion: 1.0.0\n")
	decls := config.LegacyAgentDecls{Plugins: map[string]json.RawMessage{"owner": json.RawMessage(`{"name":"owner","path":"` + original + `"}`)}}
	plan, err := planAgentBundles(decls, config.LegacyAgentEvidence{SnapshotDir: snapshot, Paths: map[string]string{original: "owner"}}, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(plan.Wrappers[0].Manifest)
	for _, want := range []string{"name: source-owner", "version: 2.0.0", "type: hybrid", "runtime/assets", "node runtime/bin/build.js", "remote/pkg", "runtime/child", "dev/pkg", "allowExecutables"} {
		if !strings.Contains(got, want) {
			t.Fatalf("wrapper manifest lost %q:\n%s", want, got)
		}
	}
}

func TestNativeMetadataAndUnsafeChildIDsBlock(t *testing.T) {
	decls, evidence, _ := ownedBundleFixture(t, "owner", "safe")
	root := filepath.Join(evidence.SnapshotDir, "owner")
	mustWriteBundleFile(t, filepath.Join(root, ".claude-plugin", "plugin.json"), `{"name":"owner","extensions":{"native":true}}`)
	mustWriteBundleFile(t, filepath.Join(root, ".mcp.json"), `{"mcpServers":{"bad\ncomment":{"type":"stdio","command":"node"}}}`)
	_, err := planAgentBundles(decls, evidence, filepath.Join(t.TempDir(), "state"))
	if err == nil || !strings.Contains(err.Error(), "extensions") || !strings.Contains(err.Error(), "unsafe mcp identifier") {
		t.Fatalf("unsafe native metadata/child accepted: %v", err)
	}
}

func TestRewrittenHookConfigCapBlocksExpansion(t *testing.T) {
	decls, evidence, _ := ownedBundleFixture(t, "owner", "safe")
	root := filepath.Join(evidence.SnapshotDir, "owner")
	mustWriteBundleFile(t, filepath.Join(root, "bin", "h"), "#!/bin/sh\n")
	fragment := `{"command":"bin/h"},`
	body := `{"items":[` + strings.Repeat(fragment, 40000)
	body = strings.TrimSuffix(body, ",") + `]}`
	if len(body) >= maxBundleManifestBytes {
		t.Fatalf("fixture unexpectedly too large: %d", len(body))
	}
	mustWriteBundleFile(t, filepath.Join(root, "hooks", "hooks.json"), body)
	_, err := planAgentBundles(decls, evidence, filepath.Join(t.TempDir(), "state"))
	if err == nil || !strings.Contains(err.Error(), "rewritten hook config exceeds") {
		t.Fatalf("expanded hook config accepted: %v", err)
	}
}

func TestHookRewriteDeltaRespectsRuntimeCaps(t *testing.T) {
	root := t.TempDir()
	mustWriteBundleFile(t, filepath.Join(root, "bin", "h"), "#!/bin/sh\n")
	hook := filepath.Join(root, "hooks", "hooks.json")
	mustWriteBundleFile(t, hook, `{"items":[{"command":"bin/h"}]}`)
	owner := agentBundleOwner{Name: "owner", Root: root, Original: filepath.Join(t.TempDir(), "owner")}
	tests := []struct {
		name      string
		ownerUsed int64
		allUsed   int64
		want      string
	}{
		{"owner cap", maxBundleRuntimeBytes - 1, 0, "owner runtime byte limit"},
		{"migration cap", 0, maxMigrationRuntimeByte - 1, "migration runtime byte limit"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ownerUsed := test.ownerUsed
			budget := &bundleScanBudget{runtimeBytes: test.allUsed}
			_, _, blockers := rewriteHookCommands(owner, hook, budget, &ownerUsed)
			if !strings.Contains(strings.Join(blockers, "\n"), test.want) {
				t.Fatalf("missing %q blocker: %v", test.want, blockers)
			}
			if ownerUsed != test.ownerUsed || budget.runtimeBytes != test.allUsed {
				t.Fatalf("failed rewrite mutated counters: owner=%d migration=%d", ownerUsed, budget.runtimeBytes)
			}
		})
	}
}

func TestWrapperPreservesDotAPMAndScalarIncludes(t *testing.T) {
	snapshot := t.TempDir()
	root := filepath.Join(snapshot, "owner")
	original := filepath.Join(t.TempDir(), "owner")
	mustWriteBundleFile(t, filepath.Join(root, "apm.yml"), "name: owner\nversion: 1.0.0\nincludes: assets\n")
	mustWriteBundleFile(t, filepath.Join(root, "assets", "a.txt"), "a")
	mustWriteBundleFile(t, filepath.Join(root, ".apm", "skills", "review", "SKILL.md"), "---\nname: review\ndescription: test\n---\n")
	decls := config.LegacyAgentDecls{Plugins: map[string]json.RawMessage{"owner": json.RawMessage(`{"name":"owner","path":"` + original + `"}`)}}
	plan, err := planAgentBundles(decls, config.LegacyAgentEvidence{SnapshotDir: snapshot, Paths: map[string]string{original: "owner"}}, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plan.Wrappers[0].Manifest), "includes:\n    - runtime/assets") {
		t.Fatalf("scalar includes not rebased:\n%s", plan.Wrappers[0].Manifest)
	}
	found := false
	for _, file := range plan.Wrappers[0].Files {
		found = found || file.Dest == ".apm/skills/review/SKILL.md"
	}
	if !found {
		t.Fatalf(".apm primitive moved under runtime: %+v", plan.Wrappers[0].Files)
	}
}

func TestWrapperPreservesAutoIncludesScalar(t *testing.T) {
	root := t.TempDir()
	mustWriteBundleFile(t, filepath.Join(root, "apm.yml"), "name: owner\nversion: 1.0.0\nincludes: auto\n")
	owner, _ := inspectBundleOwner(selectedBundleOwner{kind: "plugin", key: "owner", entry: legacyEntry{Name: "owner"}, original: filepath.Join(t.TempDir(), "owner"), root: root}, &bundleScanBudget{})
	manifest, _, err := wrapperManifestBase(owner)
	if err != nil {
		t.Fatal(err)
	}
	if manifest["includes"] != "auto" {
		t.Fatalf("auto includes changed shape: %#v", manifest["includes"])
	}
}

func TestWrapperManifestMalformedFieldsReturnErrorWithoutPanic(t *testing.T) {
	root := t.TempDir()
	mustWriteBundleFile(t, filepath.Join(root, "apm.yml"), "name: owner\nversion: 1.0.0\nincludes: [7]\nscripts:\n  build: 7\n")
	selected := selectedBundleOwner{kind: "plugin", key: "owner", entry: legacyEntry{Name: "owner"}, original: filepath.Join(t.TempDir(), "owner"), root: root}
	owner, _ := inspectBundleOwner(selected, &bundleScanBudget{})
	if _, _, err := wrapperManifestBase(owner); err == nil {
		t.Fatal("malformed wrapper manifest fields were accepted")
	}
}

func ownedBundleFixture(t *testing.T, owner, mcp string) (config.LegacyAgentDecls, config.LegacyAgentEvidence, string) {
	t.Helper()
	snapshot := t.TempDir()
	root := filepath.Join(snapshot, owner)
	mustWriteBundleFile(t, filepath.Join(root, ".claude-plugin", "plugin.json"), `{"name":"`+owner+`","version":"1.0.0"}`)
	mustWriteBundleFile(t, filepath.Join(root, ".mcp.json"), `{"mcpServers":{"`+mcp+`":{"type":"stdio","command":"node","args":["${CLAUDE_PLUGIN_ROOT}/bin/server.js"],"cwd":"${CLAUDE_PLUGIN_ROOT}"}}}`)
	mustWriteBundleFile(t, filepath.Join(root, "bin", "server.js"), "process.exit(0)\n")
	original := filepath.Join(t.TempDir(), owner)
	decls := config.LegacyAgentDecls{
		Plugins:    map[string]json.RawMessage{owner: json.RawMessage(`{"name":"` + owner + `","path":"` + original + `"}`)},
		MCPServers: map[string]json.RawMessage{},
	}
	return decls, config.LegacyAgentEvidence{SnapshotDir: snapshot, Paths: map[string]string{original: owner}}, original
}

func mustWriteBundleFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

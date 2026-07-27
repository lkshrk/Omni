package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func writeRoutedFixture(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "settings.json")
}

func rawKeys(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return raw
}

func TestPatchRawRouted_FragmentOwnedKeyWritesToFragment(t *testing.T) {
	dir := t.TempDir()
	mainPath := writeRoutedFixture(t, dir, map[string]string{
		"settings.json": `{
  "version": 17,
  "$include": ["settings.d/tools.json"],
  "settings": { "auto_import": true }
}`,
		"settings.d/tools.json": `{
  "tools": { "jq": { "providers": [{ "provider": "brew", "package": "jq" }] } }
}`,
	})

	patch := map[string]json.RawMessage{
		"tools": json.RawMessage(`{"rg":{"providers":[{"provider":"brew","package":"ripgrep"}]}}`),
	}
	if err := config.PatchRawRouted(mainPath, patch); err != nil {
		t.Fatalf("PatchRawRouted: %v", err)
	}

	mainRaw := rawKeys(t, mainPath)
	if _, ok := mainRaw["tools"]; ok {
		t.Fatal("tools must not be re-inlined into main settings.json")
	}
	fragRaw := rawKeys(t, filepath.Join(dir, "settings.d", "tools.json"))
	if !strings.Contains(string(fragRaw["tools"]), "ripgrep") {
		t.Fatalf("fragment tools = %s, want patched value", fragRaw["tools"])
	}
}

func TestPatchRawRouted_DuplicateKeyConsolidatesIntoLastOwner(t *testing.T) {
	dir := t.TempDir()
	mainPath := writeRoutedFixture(t, dir, map[string]string{
		"settings.json": `{
  "version": 17,
  "$include": ["settings.d/agents.json", "settings.d/tools.json"],
  "settings": {}
}`,
		"settings.d/agents.json": `{
  "agents": { "mcp_servers": [{ "name": "node_repl", "transport": "stdio", "command": "node-repl" }] }
}`,
		"settings.d/tools.json": `{
  "agents": { "mcp_servers": [{ "name": "stale", "transport": "stdio", "command": "stale" }] },
  "tools": {}
}`,
	})

	patch := map[string]json.RawMessage{
		"agents": json.RawMessage(`{"mcp_servers":[{"name":"keeper","transport":"stdio","command":"keeper"}]}`),
	}
	if err := config.PatchRawRouted(mainPath, patch); err != nil {
		t.Fatalf("PatchRawRouted: %v", err)
	}

	agentsRaw := rawKeys(t, filepath.Join(dir, "settings.d", "agents.json"))
	if _, ok := agentsRaw["agents"]; ok {
		t.Fatalf("stale agents copy must be cleared from agents.json, got %s", agentsRaw["agents"])
	}
	toolsRaw := rawKeys(t, filepath.Join(dir, "settings.d", "tools.json"))
	if !strings.Contains(string(toolsRaw["agents"]), "keeper") || strings.Contains(string(toolsRaw["agents"]), "stale") {
		t.Fatalf("owner fragment agents = %s, want only patched value", toolsRaw["agents"])
	}
	if _, ok := rawKeys(t, mainPath)["agents"]; ok {
		t.Fatal("agents must not appear in main settings.json")
	}
}

func TestPatchRawRouted_DuplicateKeyAcrossThreeFragmentsClearsAllNonOwners(t *testing.T) {
	dir := t.TempDir()
	mainPath := writeRoutedFixture(t, dir, map[string]string{
		"settings.json": `{
  "version": 17,
  "$include": ["settings.d/a.json", "settings.d/b.json", "settings.d/c.json"],
  "settings": {}
}`,
		"settings.d/a.json": `{
  "agents": { "mcp_servers": [{ "name": "from-a", "transport": "stdio", "command": "a" }] }
}`,
		"settings.d/b.json": `{
  "agents": { "mcp_servers": [{ "name": "from-b", "transport": "stdio", "command": "b" }] },
  "tools": {}
}`,
		"settings.d/c.json": `{
  "agents": { "mcp_servers": [{ "name": "from-c", "transport": "stdio", "command": "c" }] }
}`,
	})

	patch := map[string]json.RawMessage{
		"agents": json.RawMessage(`{"mcp_servers":[{"name":"keeper","transport":"stdio","command":"keeper"}]}`),
	}
	if err := config.PatchRawRouted(mainPath, patch); err != nil {
		t.Fatalf("PatchRawRouted: %v", err)
	}

	for _, name := range []string{"a.json", "b.json"} {
		raw := rawKeys(t, filepath.Join(dir, "settings.d", name))
		if _, ok := raw["agents"]; ok {
			t.Fatalf("non-owner %s must have its agents copy cleared, got %s", name, raw["agents"])
		}
	}
	ownerRaw := rawKeys(t, filepath.Join(dir, "settings.d", "c.json"))
	agents := string(ownerRaw["agents"])
	if !strings.Contains(agents, "keeper") || strings.Contains(agents, "from-") {
		t.Fatalf("owner c.json agents = %s, want only patched value", agents)
	}
	if _, ok := rawKeys(t, filepath.Join(dir, "settings.d", "b.json"))["tools"]; !ok {
		t.Fatal("unrelated tools key must survive in b.json")
	}
}

func TestPatchRawRouted_NestedIncludeChainRoutesToDeepOwner(t *testing.T) {
	dir := t.TempDir()
	mainPath := writeRoutedFixture(t, dir, map[string]string{
		"settings.json": `{
  "version": 17,
  "$include": ["settings.d/agents.json"],
  "settings": {}
}`,
		"settings.d/agents.json": `{
  "$include": ["agents/mcp.json"],
  "agents": { "plugins": [{ "name": "plug", "marketplace": "m" }] }
}`,
		"settings.d/agents/mcp.json": `{
  "agents": { "mcp_servers": [{ "name": "node_repl", "transport": "stdio", "command": "node-repl" }] }
}`,
	})

	patch := map[string]json.RawMessage{
		"agents": json.RawMessage(`{"mcp_servers":[{"name":"keeper","transport":"stdio","command":"keeper"}]}`),
	}
	if err := config.PatchRawRouted(mainPath, patch); err != nil {
		t.Fatalf("PatchRawRouted: %v", err)
	}

	deepRaw := rawKeys(t, filepath.Join(dir, "settings.d", "agents", "mcp.json"))
	deep := string(deepRaw["agents"])
	if !strings.Contains(deep, "keeper") || strings.Contains(deep, "node_repl") {
		t.Fatalf("deep owner agents = %s, want only patched value", deep)
	}
	midRaw := rawKeys(t, filepath.Join(dir, "settings.d", "agents.json"))
	if _, ok := midRaw["agents"]; ok {
		t.Fatalf("intermediate fragment agents copy must be cleared, got %s", midRaw["agents"])
	}
	if _, ok := midRaw["$include"]; !ok {
		t.Fatal("intermediate fragment must keep its $include key")
	}
}

func TestPatchRawRouted_DuplicateKeyInMainClearedOnRoutedWrite(t *testing.T) {
	dir := t.TempDir()
	mainPath := writeRoutedFixture(t, dir, map[string]string{
		"settings.json": `{
  "version": 17,
  "$include": ["settings.d/agents.json"],
  "agents": { "mcp_servers": [{ "name": "stale-main", "transport": "stdio", "command": "x" }] },
  "settings": {}
}`,
		"settings.d/agents.json": `{
  "agents": { "mcp_servers": [{ "name": "node_repl", "transport": "stdio", "command": "node-repl" }] }
}`,
	})

	patch := map[string]json.RawMessage{
		"agents": json.RawMessage(`{"mcp_servers":[{"name":"keeper","transport":"stdio","command":"keeper"}]}`),
	}
	if err := config.PatchRawRouted(mainPath, patch); err != nil {
		t.Fatalf("PatchRawRouted: %v", err)
	}
	if _, ok := rawKeys(t, mainPath)["agents"]; ok {
		t.Fatal("stale agents copy must be cleared from main settings.json")
	}
	fragRaw := rawKeys(t, filepath.Join(dir, "settings.d", "agents.json"))
	if !strings.Contains(string(fragRaw["agents"]), "keeper") {
		t.Fatalf("fragment agents = %s, want patched value", fragRaw["agents"])
	}
}

func TestPatchRawRouted_MainOwnedKeyStaysInMain(t *testing.T) {
	dir := t.TempDir()
	mainPath := writeRoutedFixture(t, dir, map[string]string{
		"settings.json": `{
  "version": 17,
  "$include": ["settings.d/tools.json"],
  "settings": { "auto_import": true }
}`,
		"settings.d/tools.json": `{ "tools": {} }`,
	})

	patch := map[string]json.RawMessage{
		"settings": json.RawMessage(`{"auto_import":false,"dots_repo":"/tmp/repo"}`),
	}
	if err := config.PatchRawRouted(mainPath, patch); err != nil {
		t.Fatalf("PatchRawRouted: %v", err)
	}
	mainRaw := rawKeys(t, mainPath)
	if !strings.Contains(string(mainRaw["settings"]), "dots_repo") {
		t.Fatalf("main settings = %s, want patched in main", mainRaw["settings"])
	}
	fragRaw := rawKeys(t, filepath.Join(dir, "settings.d", "tools.json"))
	if _, ok := fragRaw["settings"]; ok {
		t.Fatal("settings must not leak into tools fragment")
	}
}

const routedGroupsMain = `{
  "version": 17,
  "$include": ["settings.d/groups.json", "settings.d/dots.json"],
  "settings": { "auto_import": true }
}`

const routedGroupsFragment = `{
  "groups": [
    { "name": "core", "tools": ["jq"], "description": "core stuff" },
    { "name": "empty-group" }
  ]
}`

const routedDotsFragment = `{
  "groups": [
    { "name": "core", "dots": [
      { "name": "vim", "path": "~/.vim" }
    ] }
  ]
}`

func routedGroupsFixture(t *testing.T) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	mainPath := writeRoutedFixture(t, dir, map[string]string{
		"settings.json":          routedGroupsMain,
		"settings.d/groups.json": routedGroupsFragment,
		"settings.d/dots.json":   routedDotsFragment,
	})
	return mainPath, filepath.Join(dir, "settings.d", "groups.json"), filepath.Join(dir, "settings.d", "dots.json")
}

func mergedGroupsPatch(t *testing.T, mainPath string, mutate func(cfg *config.RootConfig)) map[string]json.RawMessage {
	t.Helper()
	cfg, err := config.Load(mainPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	mutate(cfg)
	data, err := json.Marshal(cfg.Groups)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]json.RawMessage{"groups": data}
}

func TestPatchRawRouted_DotChangeWritesToDotsFragmentOnly(t *testing.T) {
	mainPath, groupsPath, dotsPath := routedGroupsFixture(t)

	patch := mergedGroupsPatch(t, mainPath, func(cfg *config.RootConfig) {
		for _, group := range cfg.Groups {
			if group.Name != "core" {
				continue
			}
			group.Dots[0].Ignore = []string{".netrwhist"}
			group.Dots = append(group.Dots, config.DotEntry{Name: "zshrc", Path: "~/.zshrc"})
		}
	})
	if err := config.PatchRawRouted(mainPath, patch); err != nil {
		t.Fatalf("PatchRawRouted: %v", err)
	}

	if _, ok := rawKeys(t, mainPath)["groups"]; ok {
		t.Fatal("groups must not be re-inlined into main settings.json")
	}
	groupsRaw := string(rawKeys(t, groupsPath)["groups"])
	if strings.Contains(groupsRaw, "zshrc") || strings.Contains(groupsRaw, ".netrwhist") {
		t.Fatalf("groups fragment received dot data: %s", groupsRaw)
	}
	if !strings.Contains(groupsRaw, "jq") || !strings.Contains(groupsRaw, "empty-group") {
		t.Fatalf("groups fragment lost existing data: %s", groupsRaw)
	}
	dotsRaw := string(rawKeys(t, dotsPath)["groups"])
	for _, want := range []string{"vim", "zshrc", ".netrwhist"} {
		if !strings.Contains(dotsRaw, want) {
			t.Fatalf("dots fragment = %s, want %s", dotsRaw, want)
		}
	}
	if strings.Contains(dotsRaw, "jq") {
		t.Fatalf("dots fragment received tool data: %s", dotsRaw)
	}

	cfg, err := config.Load(mainPath)
	if err != nil {
		t.Fatalf("Load after patch: %v", err)
	}
	for _, group := range cfg.Groups {
		if group.Name == "core" && len(group.Dots) != 2 {
			t.Fatalf("core dots = %+v, want vim and zshrc", group.Dots)
		}
	}
}

func TestPatchRawRouted_NewGroupGoesToGroupsFragment(t *testing.T) {
	mainPath, groupsPath, dotsPath := routedGroupsFixture(t)

	patch := mergedGroupsPatch(t, mainPath, func(cfg *config.RootConfig) {
		cfg.Groups = append(cfg.Groups, &config.GroupConfig{
			Name:  "newgrp",
			Tools: []config.ToolEntry{{Name: "fd"}},
		})
	})
	if err := config.PatchRawRouted(mainPath, patch); err != nil {
		t.Fatalf("PatchRawRouted: %v", err)
	}
	// dots.json is listed last in $include, so new groups land there — not in main, and exactly once.
	if _, ok := rawKeys(t, mainPath)["groups"]; ok {
		t.Fatal("groups must not land in main")
	}
	groupsRaw := string(rawKeys(t, groupsPath)["groups"])
	dotsRaw := string(rawKeys(t, dotsPath)["groups"])
	inGroups := strings.Contains(groupsRaw, "newgrp")
	inDots := strings.Contains(dotsRaw, "newgrp")
	if inGroups == inDots {
		t.Fatalf("newgrp must live in exactly one fragment (groups=%v dots=%v)", inGroups, inDots)
	}
	cfg, err := config.Load(mainPath)
	if err != nil {
		t.Fatalf("Load after patch: %v", err)
	}
	found := false
	for _, group := range cfg.Groups {
		if group.Name == "newgrp" && len(group.Tools) == 1 {
			found = true
		}
	}
	if !found {
		t.Fatal("newgrp missing from effective config")
	}
}

func TestPatchRawRouted_DeletedGroupRemovedEverywhere(t *testing.T) {
	mainPath, groupsPath, dotsPath := routedGroupsFixture(t)

	patch := mergedGroupsPatch(t, mainPath, func(cfg *config.RootConfig) {
		kept := cfg.Groups[:0]
		for _, group := range cfg.Groups {
			if group.Name != "core" {
				kept = append(kept, group)
			}
		}
		cfg.Groups = kept
	})
	if err := config.PatchRawRouted(mainPath, patch); err != nil {
		t.Fatalf("PatchRawRouted: %v", err)
	}
	if raw := string(rawKeys(t, groupsPath)["groups"]); strings.Contains(raw, `"core"`) {
		t.Fatalf("groups fragment still has deleted group: %s", raw)
	}
	if raw, ok := rawKeys(t, dotsPath)["groups"]; ok && strings.Contains(string(raw), `"core"`) {
		t.Fatalf("dots fragment still has deleted group: %s", raw)
	}
}

func TestPatchRawRouted_NoIncludesFallsBackToMain(t *testing.T) {
	dir := t.TempDir()
	mainPath := writeRoutedFixture(t, dir, map[string]string{
		"settings.json": `{ "version": 17, "settings": { "auto_import": true } }`,
	})
	patch := map[string]json.RawMessage{
		"groups": json.RawMessage(`[{"name":"solo"}]`),
	}
	if err := config.PatchRawRouted(mainPath, patch); err != nil {
		t.Fatalf("PatchRawRouted: %v", err)
	}
	if _, ok := rawKeys(t, mainPath)["groups"]; !ok {
		t.Fatal("groups must be written to main when no fragments exist")
	}
}

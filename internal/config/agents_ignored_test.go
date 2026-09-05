package config_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

const validAgentsIgnoredBlock = `"agents":{"ignored":[{"host":"work","target":"claude","kind":"plugin","id":"acme/tool","reason":"local pin"}]}`

func writeSettings(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadAcceptsAgentsIgnoredBlock(t *testing.T) {
	t.Parallel()
	path := writeSettings(t, t.TempDir(), "settings.json",
		fmt.Sprintf(`{"version":%d,%s}`, config.CurrentVersion, validAgentsIgnoredBlock))
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load valid agents block: %v", err)
	}
	if cfg.Agents == nil || len(cfg.Agents.Ignored) != 1 {
		t.Fatalf("cfg.Agents = %#v, want one ignored entry", cfg.Agents)
	}
	entry := cfg.Agents.Ignored[0]
	want := config.AgentIgnoreEntry{Host: "work", Target: "claude", Kind: "plugin", ID: "acme/tool", Reason: "local pin"}
	if entry != want {
		t.Fatalf("entry = %#v, want %#v", entry, want)
	}
	found, err := config.HasRemovedAgentConfig(path)
	if err != nil || found {
		t.Fatalf("HasRemovedAgentConfig = %v, %v; want false, nil", found, err)
	}
}

func TestLoadAcceptsEmptyAgentsIgnoredList(t *testing.T) {
	t.Parallel()
	path := writeSettings(t, t.TempDir(), "settings.json",
		fmt.Sprintf(`{"version":%d,"agents":{"ignored":[]}}`, config.CurrentVersion))
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load empty ignored list: %v", err)
	}
	if cfg.Agents == nil || len(cfg.Agents.Ignored) != 0 {
		t.Fatalf("cfg.Agents = %#v, want an empty ignored list", cfg.Agents)
	}
	found, err := config.HasRemovedAgentConfig(path)
	if err != nil || found {
		t.Fatalf("HasRemovedAgentConfig = %v, %v; want false, nil", found, err)
	}
}

func TestLoadRejectsRetiredAgentsBlockShapes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, block, path string
	}{
		{"empty object", `{}`, `$.agents`},
		{"null", `null`, `$.agents`},
		{"stale subkey", `{"ai":{"skills":[]}}`, `$.agents.ai`},
		{"extra subkey", `{"ignored":[],"skills":[]}`, `$.agents.skills`},
		{"ignored null", `{"ignored":null}`, `$.agents.ignored`},
		{"ignored object", `{"ignored":{}}`, `$.agents.ignored`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := writeSettings(t, t.TempDir(), "settings.json",
				fmt.Sprintf(`{"version":%d,"agents":%s}`, config.CurrentVersion, tc.block))
			_, err := config.Load(path)
			if err == nil || !strings.Contains(err.Error(), `"`+tc.path+`"`) {
				t.Fatalf("Load error = %v, want removed field %s", err, tc.path)
			}
			found, err := config.HasRemovedAgentConfig(path)
			if err != nil || !found {
				t.Fatalf("HasRemovedAgentConfig = %v, %v; want true, nil", found, err)
			}
		})
	}
}

func TestLoadRejectsInvalidAgentIgnoreEntries(t *testing.T) {
	t.Parallel()
	entries := map[string]string{
		"bad target":    `{"host":"work","target":"gemini","kind":"plugin","id":"acme/tool"}`,
		"bad kind":      `{"host":"work","target":"claude","kind":"skill","id":"acme/tool"}`,
		"missing id":    `{"host":"work","target":"claude","kind":"plugin"}`,
		"empty id":      `{"host":"work","target":"claude","kind":"plugin","id":"  "}`,
		"empty host":    `{"host":"","target":"claude","kind":"plugin","id":"acme/tool"}`,
		"unknown field": `{"host":"work","target":"claude","kind":"plugin","id":"acme/tool","scope":"user"}`,
		"non-string id": `{"host":"work","target":"claude","kind":"plugin","id":7}`,
		"not an object": `"acme/tool"`,
	}
	for name, entry := range entries {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := writeSettings(t, t.TempDir(), "settings.json",
				fmt.Sprintf(`{"version":%d,"agents":{"ignored":[%s]}}`, config.CurrentVersion, entry))
			if _, err := config.Load(path); err == nil || !strings.Contains(err.Error(), `"$.agents.ignored"`) {
				t.Fatalf("Load error = %v, want rejection of %s", err, name)
			}
			found, err := config.HasRemovedAgentConfig(path)
			if err != nil || !found {
				t.Fatalf("HasRemovedAgentConfig = %v, %v; want true, nil", found, err)
			}
		})
	}
}

func TestAgentsBlockClassificationAcrossIncludes(t *testing.T) {
	t.Parallel()
	t.Run("valid", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		root := writeSettings(t, dir, "settings.json",
			fmt.Sprintf(`{"version":%d,"$include":["agents.json"]}`, config.CurrentVersion))
		writeSettings(t, dir, "agents.json", fmt.Sprintf(`{%s}`, validAgentsIgnoredBlock))
		cfg, err := config.Load(root)
		if err != nil {
			t.Fatalf("Load included agents block: %v", err)
		}
		if cfg.Agents == nil || len(cfg.Agents.Ignored) != 1 || cfg.Agents.Ignored[0].ID != "acme/tool" {
			t.Fatalf("cfg.Agents = %#v, want the included entry merged", cfg.Agents)
		}
		found, err := config.HasRemovedAgentConfig(root)
		if err != nil || found {
			t.Fatalf("HasRemovedAgentConfig = %v, %v; want false, nil", found, err)
		}
	})
	t.Run("stale", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		root := writeSettings(t, dir, "settings.json",
			fmt.Sprintf(`{"version":%d,"$include":["agents.json"]}`, config.CurrentVersion))
		writeSettings(t, dir, "agents.json", `{"agents":{"ai":{"skills":[]}}}`)
		if _, err := config.Load(root); err == nil || !strings.Contains(err.Error(), `"$.agents.ai"`) {
			t.Fatalf("Load error = %v, want removed field $.agents.ai", err)
		}
		found, err := config.HasRemovedAgentConfig(root)
		if err != nil || !found {
			t.Fatalf("HasRemovedAgentConfig = %v, %v; want true, nil", found, err)
		}
	})
}

func TestSaveRoundTripsAgentsIgnored(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeSettings(t, dir, "settings.json",
		fmt.Sprintf(`{"version":%d,%s}`, config.CurrentVersion, validAgentsIgnoredBlock))
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	saved := filepath.Join(dir, "saved.json")
	if err := config.Save(saved, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := config.Load(saved)
	if err != nil {
		t.Fatalf("Load saved: %v", err)
	}
	if reloaded.Agents == nil || len(reloaded.Agents.Ignored) != 1 || reloaded.Agents.Ignored[0] != cfg.Agents.Ignored[0] {
		t.Fatalf("reloaded.Agents = %#v, want %#v", reloaded.Agents, cfg.Agents)
	}
	found, err := config.HasRemovedAgentConfig(saved)
	if err != nil || found {
		t.Fatalf("HasRemovedAgentConfig = %v, %v; want false, nil", found, err)
	}
}

func TestExtractPreservesAgentsIgnored(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeSettings(t, dir, "settings.json", fmt.Sprintf(`{
  "version": %d,
  "tools": {"jq": {"providers": [{"provider": "brew", "package": "jq"}]}},
  "groups": [{"name": "core", "tools": ["jq"]}],
  %s
}`, config.CurrentVersion, validAgentsIgnoredBlock))
	if _, err := config.ExtractIncludeFragments(path); err != nil {
		t.Fatalf("ExtractIncludeFragments: %v", err)
	}
	if _, ok := rawKeys(t, path)["agents"]; !ok {
		t.Fatal("main config lost its agents block after extract")
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load after extract: %v", err)
	}
	want := config.AgentIgnoreEntry{Host: "work", Target: "claude", Kind: "plugin", ID: "acme/tool", Reason: "local pin"}
	if cfg.Agents == nil || len(cfg.Agents.Ignored) != 1 || cfg.Agents.Ignored[0] != want {
		t.Fatalf("cfg.Agents = %#v, want the ignored entry preserved", cfg.Agents)
	}
}

func TestExtractRejectsRetiredAgentsBlock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := fmt.Sprintf(`{"version":%d,"groups":[{"name":"core"}],"agents":{"ai":{"skills":[]}}}`, config.CurrentVersion)
	path := writeSettings(t, dir, "settings.json", body)
	if _, err := config.ExtractIncludeFragments(path); err == nil || !strings.Contains(err.Error(), `"$.agents.ai"`) {
		t.Fatalf("ExtractIncludeFragments error = %v, want removed field $.agents.ai", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != body {
		t.Fatalf("main config = %s, want it left untouched", data)
	}
}

package config_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestMigrateV24ToV25KeepsAgentsIgnored(t *testing.T) {
	t.Parallel()
	path := writeSettings(t, t.TempDir(), "settings.json",
		`{"version":24,`+validAgentsIgnoredBlock+`}`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load v24 config with agents.ignored: %v", err)
	}
	if cfg.Version != config.CurrentVersion {
		t.Fatalf("version = %d, want %d", cfg.Version, config.CurrentVersion)
	}
	want := config.AgentIgnoreEntry{Host: "work", Target: "claude", Kind: "plugin", ID: "acme/tool", Reason: "local pin"}
	if cfg.Agents == nil || len(cfg.Agents.Ignored) != 1 || cfg.Agents.Ignored[0] != want {
		t.Fatalf("cfg.Agents = %#v, want the ignored entry preserved", cfg.Agents)
	}
}

func TestMigrateV24ToV25RejectsRetiredAgentsBlock(t *testing.T) {
	t.Parallel()
	path := writeSettings(t, t.TempDir(), "settings.json",
		`{"version":24,"agents":{"ai":{"skills":[]}}}`)
	if _, err := config.Load(path); err == nil || !strings.Contains(err.Error(), `"$.agents.ai"`) {
		t.Fatalf("Load error = %v, want removed field $.agents.ai", err)
	}
}

func TestV25ConfigRoundTripsThroughSave(t *testing.T) {
	t.Parallel()
	path := writeSettings(t, t.TempDir(), "settings.json",
		`{"version":25,`+validAgentsIgnoredBlock+`}`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load v25 config: %v", err)
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Schema  string `json:"$schema"`
		Version int    `json:"version"`
		Agents  struct {
			Ignored []config.AgentIgnoreEntry `json:"ignored"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("parse saved config: %v", err)
	}
	if saved.Version != 25 {
		t.Fatalf("saved version = %d, want 25", saved.Version)
	}
	if saved.Schema != config.SchemaURL || !strings.Contains(saved.Schema, ".v25.") {
		t.Fatalf("saved $schema = %q, want %q", saved.Schema, config.SchemaURL)
	}
	want := config.AgentIgnoreEntry{Host: "work", Target: "claude", Kind: "plugin", ID: "acme/tool", Reason: "local pin"}
	if len(saved.Agents.Ignored) != 1 || saved.Agents.Ignored[0] != want {
		t.Fatalf("saved agents.ignored = %#v, want %#v", saved.Agents.Ignored, want)
	}
	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("reload saved config: %v", err)
	}
	if reloaded.Version != config.CurrentVersion || reloaded.Agents == nil || len(reloaded.Agents.Ignored) != 1 {
		t.Fatalf("reloaded = %#v, want a v%d config with one ignored entry", reloaded, config.CurrentVersion)
	}
}

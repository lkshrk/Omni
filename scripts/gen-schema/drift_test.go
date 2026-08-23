package main

import (
	"encoding/json"
	"os"
	"strconv"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestGeneratedSchemaHasNoRemovedAgentDefinitions(t *testing.T) {
	data, err := os.ReadFile("../../spec/omni.settings.v" + strconv.Itoa(config.CurrentVersion) + ".schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"AgentsConfig", "AgentsIgnore", "ManifestSkill", "Marketplace", "McpServer", "Plugin", "SkillPackage"} {
		if _, ok := doc.Defs[name]; ok {
			t.Fatalf("removed definition %q remains", name)
		}
	}
}

package config_test

import (
	"encoding/json"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	_ "github.com/lkshrk/omni/internal/testguard"
)

func TestRootConfigAgentsSkillsRoundTrip(t *testing.T) {
	in := `{"version":1,"agents":{"skills":[{"name":"frontend-design","source":"vercel-labs/agent-skills","ref":"main","agents":["claude-code"]}]}}`
	var cfg config.RootConfig
	if err := json.Unmarshal([]byte(in), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(cfg.Agents.Skills) != 1 {
		t.Fatalf("want 1 skill, got %d", len(cfg.Agents.Skills))
	}
	s := cfg.Agents.Skills[0]
	if s.Name != "frontend-design" || s.Source != "vercel-labs/agent-skills" || s.Ref != "main" {
		t.Fatalf("unexpected skill: %+v", s)
	}
	if len(s.Agents) != 1 || s.Agents[0] != "claude-code" {
		t.Fatalf("unexpected agents: %+v", s.Agents)
	}
}

package config_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestLoadRejectsRemovedAgentFields(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, body, path string
	}{
		{"agents", `"agents":{}`, `$.agents`},
		{"settings agents_use", `"settings":{"agents_use":[]}`, `$.settings.agents_use`},
		{"settings agents_disabled", `"settings":{"agents_disabled":false}`, `$.settings.agents_disabled`},
		{"settings skills_disabled", `"settings":{"skills_disabled":false}`, `$.settings.skills_disabled`},
		{"settings mcp_disabled", `"settings":{"mcp_disabled":false}`, `$.settings.mcp_disabled`},
		{"settings plugins_disabled", `"settings":{"plugins_disabled":false}`, `$.settings.plugins_disabled`},
		{"host agents_use", `"host_settings":{"work":{"agents_use":[]}}`, `$.host_settings."work".agents_use`},
		{"host agents_disabled", `"host_settings":{"work":{"agents_disabled":false}}`, `$.host_settings."work".agents_disabled`},
		{"host skills_disabled", `"host_settings":{"work":{"skills_disabled":false}}`, `$.host_settings."work".skills_disabled`},
		{"host mcp_disabled", `"host_settings":{"work":{"mcp_disabled":false}}`, `$.host_settings."work".mcp_disabled`},
		{"host plugins_disabled", `"host_settings":{"work":{"plugins_disabled":false}}`, `$.host_settings."work".plugins_disabled`},
		{"group skills", `"groups":[{"name":"work","skills":[]}]`, `$.groups[0].skills`},
		{"group mcp_servers", `"groups":[{"name":"work","mcp_servers":[]}]`, `$.groups[0].mcp_servers`},
		{"group plugins", `"groups":[{"name":"work","plugins":[]}]`, `$.groups[0].plugins`},
		{"group marketplaces", `"groups":[{"name":"work","marketplaces":[]}]`, `$.groups[0].marketplaces`},
	}
	for _, version := range []int{23, config.CurrentVersion} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("v%d/%s", version, tc.name), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "settings.json")
				data := []byte(fmt.Sprintf(`{"version":%d,%s}`, version, tc.body))
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
				_, err := config.Load(path)
				if err == nil || !strings.Contains(err.Error(), strconv.Quote(tc.path)) || !strings.Contains(err.Error(), "~/.apm/apm.yml") {
					t.Fatalf("Load error = %v, want removed field %s with APM guidance", err, tc.path)
				}
			})
		}
	}
}

func TestLoadStillIgnoresUnrelatedUnknownNestedFields(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "settings.json")
	data := []byte(`{
		"version": 24,
		"settings": {"future_setting": true},
		"host_settings": {"work": {"future_host_setting": true}},
		"groups": [{"name": "work", "future_group_field": []}]
	}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load unrelated unknown fields: %v", err)
	}
	if len(cfg.Groups) != 1 || cfg.Groups[0].Name != "work" {
		t.Fatalf("groups = %#v, want known fields decoded normally", cfg.Groups)
	}
}

func TestV14MigrationDropsOnlyAPMManagedAgentsSkills(t *testing.T) {
	t.Parallel()
	cfg := &config.RootConfig{
		Version: 13,
		Groups: []*config.GroupConfig{{
			Name: "dots",
			Dots: []config.DotEntry{
				{Name: "skills", Path: "~/.agents/skills"},
				{Name: "agents-config", Path: "~/.agents/config.json"},
				{Name: "nvim", Path: "~/.config/nvim"},
			},
		}},
	}
	if _, err := config.Migrate(cfg); err != nil {
		t.Fatal(err)
	}
	got := cfg.Groups[0].Dots
	if len(got) != 2 || got[0].Name != "agents-config" || got[1].Name != "nvim" {
		t.Fatalf("dots = %#v, want user-owned ~/.agents sibling and normal dot", got)
	}
}

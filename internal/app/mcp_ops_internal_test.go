package app

import (
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func resolveNames(cfg *config.RootConfig, group string) []string {
	servers := resolveMcpServers(cfg, group)
	names := make([]string, len(servers))
	for i, s := range servers {
		names[i] = s.Name
	}
	return names
}

func containsName(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

func TestResolveMcpServers_UngroupedServer_AppearsOnAllHosts(t *testing.T) {
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{
			McpServers: []config.McpServer{
				{Name: "global", Transport: "http", URL: "https://example.com"},
			},
		},
	}
	for _, group := range []string{"box", "work", ""} {
		got := resolveNames(cfg, group)
		if !containsName(got, "global") {
			t.Errorf("group=%q: ungrouped server must appear; got %v", group, got)
		}
	}
}

func TestResolveMcpServers_GroupedServer_MatchingGroup_Included(t *testing.T) {
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{
			McpServers: []config.McpServer{
				{Name: "work-only", Transport: "http", URL: "https://work.example.com"},
			},
		},
		Groups: []*config.GroupConfig{
			{Name: "work", McpServers: []string{"work-only"}},
		},
	}
	got := resolveNames(cfg, "work")
	if !containsName(got, "work-only") {
		t.Fatalf("grouped server must appear when its group is active; got %v", got)
	}
}

func TestResolveMcpServers_GroupedServer_NonMatchingGroup_Excluded(t *testing.T) {
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{
			McpServers: []config.McpServer{
				{Name: "work-only", Transport: "http", URL: "https://work.example.com"},
			},
		},
		Groups: []*config.GroupConfig{
			{Name: "work", McpServers: []string{"work-only"}},
		},
	}
	got := resolveNames(cfg, "personal")
	if containsName(got, "work-only") {
		t.Fatalf("grouped server must not appear when its group is not active; got %v", got)
	}
}

func TestResolveMcpServers_HostAssignedGroup_Included(t *testing.T) {
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{
			McpServers: []config.McpServer{
				{Name: "litellm-tools", Transport: "http", URL: "https://litellm.example.com"},
			},
		},
		Groups: []*config.GroupConfig{
			{Name: "ai", McpServers: []string{"litellm-tools"}},
		},
		Hosts: map[string][]string{
			"topaz": {"ai"},
		},
	}
	got := resolveNames(cfg, "topaz")
	if !containsName(got, "litellm-tools") {
		t.Fatalf("server in a group assigned to the host via cfg.Hosts must appear; got %v", got)
	}
}

func TestResolveMcpServers_ServerInMultipleGroups_AppearsOncePerActiveGroup(t *testing.T) {
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{
			McpServers: []config.McpServer{
				{Name: "shared", Transport: "http", URL: "https://example.com"},
			},
		},
		Groups: []*config.GroupConfig{
			{Name: "work", McpServers: []string{"shared"}},
			{Name: "home", McpServers: []string{"shared"}},
		},
	}
	// active group "work" → shared appears
	got := resolveNames(cfg, "work")
	var count int
	for _, n := range got {
		if n == "shared" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("shared server must appear exactly once; got %d in %v", count, got)
	}
	// inactive group "other" → shared is excluded
	got2 := resolveNames(cfg, "other")
	if containsName(got2, "shared") {
		t.Fatalf("shared server must not appear when neither group is active; got %v", got2)
	}
}

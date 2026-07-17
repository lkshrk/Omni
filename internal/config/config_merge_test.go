package config

import (
	"reflect"
	"testing"
)

// TestMergeRootConfig_AgentsAndHosts pins the include-fragment merge rules:
// later fragments fill blanks and union identity-keyed arrays, never
// duplicate or clobber non-empty destination fields.
func TestMergeRootConfig_AgentsAndHosts(t *testing.T) {
	dst := &RootConfig{
		Version: 3,
		Hosts:   map[string][]string{"mac": {"dev"}},
		Ignore:  GlobalIgnore{Tools: []string{"jq"}},
		Agents: AgentsConfig{
			Packages: []SkillPackage{{Source: "acme/skills", Agents: []string{"claude-code"}}},
			McpServers: []McpServer{{
				Name: "srv", Transport: "stdio",
				EnvLiteral: map[string]string{"A": "1"},
			}},
			Marketplaces: []Marketplace{{Name: "caveman"}},
			Plugins:      []Plugin{{Name: "plug"}},
		},
	}
	src := &RootConfig{
		Version: 5,
		Hosts:   map[string][]string{"mac": {"dev", "ops", " ", ""}, "linux": {"base"}},
		Ignore:  GlobalIgnore{Tools: []string{"jq", "fzf"}, Dots: []string{".vimrc"}},
		Agents: AgentsConfig{
			Packages: []SkillPackage{
				{Source: "acme/skills", Ref: "v2", Agents: []string{"codex"}},
				{Source: "other/pkg"},
			},
			McpServers: []McpServer{
				{Name: "srv", Command: "run", URL: "http://x", Env: []string{"B"}, EnvLiteral: map[string]string{"C": "2"}, Agents: []string{"codex"}},
				{Name: "new-srv", Transport: "http"},
			},
			Marketplaces: []Marketplace{{Name: "caveman", Source: "a/b", Agents: []string{"codex"}}},
			Plugins:      []Plugin{{Name: "plug", Marketplace: "caveman", Agents: []string{"codex"}}},
			Ignore:       AgentsIgnore{Skills: []string{"s1"}},
		},
	}

	MergeRootConfig(dst, src)

	if dst.Version != 5 {
		t.Fatalf("Version = %d, want later fragment's 5", dst.Version)
	}
	if got := dst.Hosts["mac"]; !reflect.DeepEqual(got, []string{"dev", "ops"}) {
		t.Fatalf("Hosts[mac] = %v, want deduped trimmed union [dev ops]", got)
	}
	if got := dst.Hosts["linux"]; !reflect.DeepEqual(got, []string{"base"}) {
		t.Fatalf("Hosts[linux] = %v", got)
	}
	if got := dst.Ignore.Tools; !reflect.DeepEqual(got, []string{"jq", "fzf"}) {
		t.Fatalf("Ignore.Tools = %v", got)
	}

	if len(dst.Agents.Packages) != 2 {
		t.Fatalf("Packages = %v, want merged-by-source pair", dst.Agents.Packages)
	}
	pkg := dst.Agents.Packages[0]
	if pkg.Ref != "v2" || !reflect.DeepEqual(pkg.Agents, []string{"claude-code", "codex"}) {
		t.Fatalf("merged package = %+v, want ref filled + agents union", pkg)
	}

	if len(dst.Agents.McpServers) != 2 {
		t.Fatalf("McpServers = %v, want merged-by-name pair", dst.Agents.McpServers)
	}
	srv := dst.Agents.McpServers[0]
	if srv.Transport != "stdio" || srv.Command != "run" || srv.URL != "http://x" {
		t.Fatalf("merged server = %+v: blanks filled, non-empty transport kept", srv)
	}
	if srv.EnvLiteral["A"] != "1" || srv.EnvLiteral["C"] != "2" {
		t.Fatalf("merged EnvLiteral = %v, want union", srv.EnvLiteral)
	}
	if !reflect.DeepEqual(srv.Env, []string{"B"}) || !reflect.DeepEqual(srv.Agents, []string{"codex"}) {
		t.Fatalf("merged env/agents = %v/%v", srv.Env, srv.Agents)
	}

	mkt := dst.Agents.Marketplaces[0]
	if mkt.Source != "a/b" || !reflect.DeepEqual(mkt.Agents, []string{"codex"}) {
		t.Fatalf("merged marketplace = %+v, want source filled", mkt)
	}
	plug := dst.Agents.Plugins[0]
	if plug.Marketplace != "caveman" || !reflect.DeepEqual(plug.Agents, []string{"codex"}) {
		t.Fatalf("merged plugin = %+v, want marketplace filled", plug)
	}
	if !reflect.DeepEqual(dst.Agents.Ignore.Skills, []string{"s1"}) {
		t.Fatalf("Agents.Ignore.Skills = %v", dst.Agents.Ignore.Skills)
	}
}

func TestMergeRootConfig_NonEmptyDestinationFieldsWin(t *testing.T) {
	dst := &RootConfig{
		Agents: AgentsConfig{
			McpServers:   []McpServer{{Name: "srv", Transport: "http", Command: "keep"}},
			Marketplaces: []Marketplace{{Name: "caveman", Source: "keep/me"}},
			Plugins:      []Plugin{{Name: "plug", Marketplace: "keep-market"}},
		},
	}
	src := &RootConfig{
		Agents: AgentsConfig{
			McpServers:   []McpServer{{Name: "srv", Transport: "stdio", Command: "clobber"}},
			Marketplaces: []Marketplace{{Name: "caveman", Source: "clobber/me"}},
			Plugins:      []Plugin{{Name: "plug", Marketplace: "clobber-market"}},
		},
	}
	MergeRootConfig(dst, src)
	if srv := dst.Agents.McpServers[0]; srv.Transport != "http" || srv.Command != "keep" {
		t.Fatalf("non-empty mcp fields clobbered: %+v", srv)
	}
	if dst.Agents.Marketplaces[0].Source != "keep/me" {
		t.Fatalf("non-empty marketplace source clobbered: %+v", dst.Agents.Marketplaces[0])
	}
	if dst.Agents.Plugins[0].Marketplace != "keep-market" {
		t.Fatalf("non-empty plugin marketplace clobbered: %+v", dst.Agents.Plugins[0])
	}
}

func TestMergeRootConfig_SettingsAndHostSettings(t *testing.T) {
	no := false
	dst := &RootConfig{Settings: Settings{UpdateQuarantine: "7d"}}
	src := &RootConfig{
		Settings: Settings{
			AutoImport:               true,
			ProviderUpdateQuarantine: map[string]string{"brew": "3d"},
			DotsRepo:                 "~/dots",
			SkillsDisabled:           &no,
			AgentsUse:                []string{"claude-code"},
			DotsGit:                  DotsGitConfig{AutoCommit: true, AutoPush: true},
		},
		HostSettings: map[string]Settings{"mac": {FallbackBinDir: "~/bin"}},
	}
	MergeRootConfig(dst, src)
	s := dst.Settings
	if !s.AutoImport || s.UpdateQuarantine != "7d" || s.ProviderUpdateQuarantine["brew"] != "3d" {
		t.Fatalf("merged settings = %+v", s)
	}
	if s.DotsRepo != "~/dots" || s.SkillsDisabled == nil || *s.SkillsDisabled {
		t.Fatalf("merged settings pointers = %+v", s)
	}
	if !reflect.DeepEqual(s.AgentsUse, []string{"claude-code"}) || !s.DotsGit.AutoCommit || !s.DotsGit.AutoPush {
		t.Fatalf("merged settings arrays/flags = %+v", s)
	}
	if dst.HostSettings["mac"].FallbackBinDir != "~/bin" {
		t.Fatalf("HostSettings = %+v", dst.HostSettings)
	}
}

func TestAppendUniqueToolEntries(t *testing.T) {
	dst := []ToolEntry{{Name: "jq"}}
	got := appendUniqueToolEntries(dst, []ToolEntry{{Name: "jq"}, {Name: "fzf"}, {Name: ""}})
	names := make([]string, 0, len(got))
	for _, e := range got {
		names = append(names, e.Name)
	}
	want := []string{"jq", "fzf"}
	if len(got) > 2 || !reflect.DeepEqual(names[:2], want) {
		t.Fatalf("appendUniqueToolEntries = %v, want %v", names, want)
	}
}

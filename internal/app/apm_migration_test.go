package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lkshrk/omni/internal/config"
)

func TestMigrateAgentsToAPMWritesGlobalManifestAndRemovesOnlyMigratedEntries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "settings.json")
	home := filepath.Join(dir, "home")
	cfg := &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"claude-code", "codex", "cursor"}},
		Agents: config.AgentsConfig{
			Packages: []config.SkillPackage{
				{Source: "acme/shared", Ref: "v1.2.3", Skills: []string{"review"}, Agents: []string{"claude-code", "codex"}},
				{Source: "acme/cursor", Agents: []string{"cursor"}},
			},
			McpServers: []config.McpServer{{
				Name: "grafana", Transport: "http", URL: "https://mcp.example.test",
				Env: []string{"GRAFANA_TOKEN"}, Headers: map[string]string{"X-Team": "agents"},
			}},
		},
	}
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	a := New(configPath, WithEnvLookup(func(name string) string {
		if name == "HOME" {
			return home
		}
		return ""
	}))

	result, err := a.MigrateAgentsToAPM()
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != filepath.Join(home, ".apm", "apm.yml") || result.MigratedPackages != 2 || result.MigratedMCPServers != 1 {
		t.Fatalf("result = %+v", result)
	}

	data, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Name         string   `yaml:"name"`
		Version      string   `yaml:"version"`
		Targets      []string `yaml:"targets"`
		Dependencies struct {
			APM []struct {
				Git     string   `yaml:"git"`
				Ref     string   `yaml:"ref"`
				Skills  []string `yaml:"skills"`
				Targets []string `yaml:"targets"`
			} `yaml:"apm"`
			MCP []struct {
				Name      string            `yaml:"name"`
				Registry  bool              `yaml:"registry"`
				Transport string            `yaml:"transport"`
				URL       string            `yaml:"url"`
				Env       map[string]string `yaml:"env"`
				Headers   map[string]string `yaml:"headers"`
			} `yaml:"mcp"`
		} `yaml:"dependencies"`
	}
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Name != "omni-agents" || manifest.Version != "0.1.0" {
		t.Fatalf("manifest identity = %q %q", manifest.Name, manifest.Version)
	}
	if !slices.Equal(manifest.Targets, []string{"claude", "codex", "cursor"}) {
		t.Fatalf("manifest targets = %v", manifest.Targets)
	}
	if len(manifest.Dependencies.APM) != 2 || manifest.Dependencies.APM[0].Git != "acme/shared" ||
		!slices.Equal(manifest.Dependencies.APM[0].Targets, []string{"claude", "codex"}) {
		t.Fatalf("apm dependencies = %+v", manifest.Dependencies.APM)
	}
	if len(manifest.Dependencies.MCP) != 1 || manifest.Dependencies.MCP[0].Registry ||
		manifest.Dependencies.MCP[0].Env["GRAFANA_TOKEN"] != "${env:GRAFANA_TOKEN}" {
		t.Fatalf("mcp dependencies = %+v", manifest.Dependencies.MCP)
	}

	updated, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Agents.Packages) != 0 {
		t.Fatalf("remaining packages = %+v", updated.Agents.Packages)
	}
	if len(updated.Agents.McpServers) != 0 || len(updated.Agents.Plugins) != 0 || len(updated.Agents.Marketplaces) != 0 {
		t.Fatalf("remaining agents config = %+v", updated.Agents)
	}
}

func TestMigrateAgentsToAPMRejectsGroupScopedAgentState(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "settings.json")
	home := filepath.Join(dir, "home")
	cfg := &config.RootConfig{
		Version: config.CurrentVersion,
		Agents:  config.AgentsConfig{Packages: []config.SkillPackage{{Source: "acme/shared"}}},
		Groups:  []*config.GroupConfig{{Name: "work", Skills: []string{"acme/shared"}}},
	}
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	a := New(configPath, WithEnvLookup(func(name string) string {
		if name == "HOME" {
			return home
		}
		return ""
	}))
	if _, err := a.MigrateAgentsToAPM(); err == nil || !strings.Contains(err.Error(), "group-scoped") {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".apm", "apm.yml")); !os.IsNotExist(err) {
		t.Fatalf("manifest created: %v", err)
	}
}

func TestMigrateAgentsToAPMRejectsExistingSkillDeployments(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "settings.json")
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(home, ".agents", "skills", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(configPath, &config.RootConfig{Version: config.CurrentVersion, Agents: config.AgentsConfig{
		Packages: []config.SkillPackage{{Source: "acme/shared", Agents: []string{"codex"}}},
	}}); err != nil {
		t.Fatal(err)
	}
	a := New(configPath, WithEnvLookup(func(name string) string {
		if name == "HOME" {
			return home
		}
		return ""
	}))
	if _, err := a.MigrateAgentsToAPM(); err == nil || !strings.Contains(err.Error(), "legacy skill deployments") {
		t.Fatalf("error = %v", err)
	}
	got, err := config.Load(configPath)
	if err != nil || len(got.Agents.Packages) != 1 {
		t.Fatalf("legacy config changed: %+v, %v", got, err)
	}
}

func TestMigrateAgentsToAPMRejectsTargetSpecificSkillDeployment(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "settings.json")
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(home, ".claude", "skills", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(configPath, &config.RootConfig{Version: config.CurrentVersion, Agents: config.AgentsConfig{
		Packages: []config.SkillPackage{{Source: "acme/shared", Agents: []string{"claude-code"}}},
	}}); err != nil {
		t.Fatal(err)
	}
	a := New(configPath, WithEnvLookup(func(name string) string {
		if name == "HOME" {
			return home
		}
		return ""
	}))
	if _, err := a.MigrateAgentsToAPM(); err == nil || !strings.Contains(err.Error(), filepath.Join(".claude", "skills")) {
		t.Fatalf("error = %v", err)
	}
}

func TestMigrateAgentsToAPMRejectsExplicitlyDisabledTargets(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(configPath, []byte(`{"version":22,"settings":{"agents_use":[]},"agents":{"packages":[{"source":"acme/shared"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(configPath, WithEnvLookup(func(name string) string {
		if name == "HOME" {
			return filepath.Join(dir, "home")
		}
		return ""
	}))
	if _, err := a.MigrateAgentsToAPM(); err == nil || !strings.Contains(err.Error(), "explicitly disables every target") {
		t.Fatalf("error = %v", err)
	}
}

func TestMigrateAgentsToAPMIntersectsPackageTargetsWithAgentsUse(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "settings.json")
	home := filepath.Join(dir, "home")
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex"}},
		Agents: config.AgentsConfig{Packages: []config.SkillPackage{{
			Source: "acme/shared", Agents: []string{"claude-code", "codex"},
		}}},
	}); err != nil {
		t.Fatal(err)
	}
	a := New(configPath, WithEnvLookup(func(name string) string {
		if name == "HOME" {
			return home
		}
		return ""
	}))
	result, err := a.MigrateAgentsToAPM()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest apmMigrationManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if got := manifest.Dependencies.APM[0].Targets; !slices.Equal(got, []string{"codex"}) {
		t.Fatalf("package targets = %v", got)
	}
}

func TestMigrateAgentsToAPMIncludesExplicitUndetectedPackageTargetAtRoot(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "settings.json")
	home := filepath.Join(dir, "home")
	if err := config.Save(configPath, &config.RootConfig{
		Version: config.CurrentVersion,
		Agents: config.AgentsConfig{Packages: []config.SkillPackage{{
			Source: "acme/shared", Agents: []string{"codex"},
		}}},
	}); err != nil {
		t.Fatal(err)
	}
	a := New(configPath, WithEnvLookup(func(name string) string {
		if name == "HOME" {
			return home
		}
		return ""
	}))
	result, err := a.MigrateAgentsToAPM()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest apmMigrationManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(manifest.Targets, []string{"codex"}) {
		t.Fatalf("root targets = %v", manifest.Targets)
	}
}

func TestMigrateAgentsToAPMScopesUnrestrictedPackageToDetectedTargets(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	configPath := filepath.Join(dir, "settings.json")
	if err := config.Save(configPath, &config.RootConfig{
		Version: config.CurrentVersion,
		Agents: config.AgentsConfig{Packages: []config.SkillPackage{
			{Source: "acme/codex", Agents: []string{"codex"}},
			{Source: "acme/detected"},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	a := New(configPath, WithEnvLookup(func(name string) string {
		if name == "HOME" {
			return home
		}
		return ""
	}))
	result, err := a.MigrateAgentsToAPM()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest apmMigrationManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if got := manifest.Dependencies.APM[1].Targets; !slices.Equal(got, []string{"claude"}) {
		t.Fatalf("unrestricted package targets = %v", got)
	}
}

func TestMigrateAgentsToAPMRejectsMixedPackageOnlyAndMCPTargets(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "settings.json")
	if err := config.Save(configPath, &config.RootConfig{
		Version: config.CurrentVersion,
		Agents: config.AgentsConfig{
			Packages:   []config.SkillPackage{{Source: "acme/shared", Agents: []string{"codex"}}},
			McpServers: []config.McpServer{{Name: "demo", Transport: "http", URL: "https://mcp.example.test"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	a := New(configPath, WithEnvLookup(func(name string) string {
		if name == "HOME" {
			return filepath.Join(dir, "home")
		}
		return ""
	}))
	if _, err := a.MigrateAgentsToAPM(); err == nil || !strings.Contains(err.Error(), "package-only targets") {
		t.Fatalf("error = %v", err)
	}
}

func TestMigrateAgentsToAPMRejectsInvalidAPMEntries(t *testing.T) {
	tests := []struct {
		name   string
		agents config.AgentsConfig
		want   string
	}{
		{name: "local ref", agents: config.AgentsConfig{Packages: []config.SkillPackage{{Source: "./local", Ref: "main"}}}, want: "local paths cannot preserve a Git ref"},
		{name: "mcp name", agents: config.AgentsConfig{McpServers: []config.McpServer{{Name: "my server", Transport: "http", URL: "https://example.test"}}}, want: "name is not valid"},
		{name: "mcp url", agents: config.AgentsConfig{McpServers: []config.McpServer{{Name: "demo", Transport: "http", URL: "example.test"}}}, want: "absolute HTTPS URL"},
		{name: "long mcp name", agents: config.AgentsConfig{McpServers: []config.McpServer{{Name: strings.Repeat("a", 129), Transport: "http", URL: "https://example.test"}}}, want: "name is not valid"},
		{name: "mcp header newline", agents: config.AgentsConfig{McpServers: []config.McpServer{{Name: "demo", Transport: "http", URL: "https://example.test", Headers: map[string]string{"X-Test": "bad\nvalue"}}}}, want: "contains a newline"},
		{name: "mcp command traversal", agents: config.AgentsConfig{McpServers: []config.McpServer{{Name: "demo", Transport: "stdio", Command: "../server"}}}, want: "stdio command"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			configPath := filepath.Join(dir, "settings.json")
			if err := config.Save(configPath, &config.RootConfig{Version: config.CurrentVersion, Agents: tc.agents}); err != nil {
				t.Fatal(err)
			}
			a := New(configPath, WithEnvLookup(func(name string) string {
				if name == "HOME" {
					return filepath.Join(dir, "home")
				}
				return ""
			}))
			result, err := a.MigrateAgentsToAPM()
			if err == nil || !slices.ContainsFunc(result.Warnings, func(w string) bool { return strings.Contains(w, tc.want) }) {
				t.Fatalf("result = %+v, error = %v", result, err)
			}
		})
	}
}

func TestMigrateAgentsToAPMIsAtomicWhenLegacyEntriesAreUnsupported(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "settings.json")
	home := filepath.Join(dir, "home")
	want := &config.RootConfig{Version: config.CurrentVersion, Agents: config.AgentsConfig{
		Packages: []config.SkillPackage{{Source: "acme/shared"}},
		Plugins:  []config.Plugin{{Name: "weather", Marketplace: "private"}},
	}}
	if err := config.Save(configPath, want); err != nil {
		t.Fatal(err)
	}
	a := New(configPath, WithEnvLookup(func(name string) string {
		if name == "HOME" {
			return home
		}
		return ""
	}))
	result, err := a.MigrateAgentsToAPM()
	if err == nil || !strings.Contains(err.Error(), "cannot be migrated losslessly") {
		t.Fatalf("error = %v", err)
	}
	if !slices.ContainsFunc(result.Warnings, func(w string) bool { return strings.Contains(w, "plugin") }) {
		t.Fatalf("warnings = %v", result.Warnings)
	}
	if _, err := os.Stat(filepath.Join(home, ".apm", "apm.yml")); !os.IsNotExist(err) {
		t.Fatalf("partial manifest created: %v", err)
	}
	got, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Agents.Packages) != 1 || len(got.Agents.Plugins) != 1 {
		t.Fatalf("legacy config changed: %+v", got.Agents)
	}
}

func TestMigrateAPMTargets(t *testing.T) {
	legacy := []string{"antigravity", "claude-code", "codex", "cursor", "gemini-cli", "github-copilot", "grok", "kiro-cli", "opencode", "windsurf", "codex", "hermes-agent", "unknown"}
	want := []string{"antigravity", "claude", "codex", "cursor", "gemini", "copilot", "grok-build", "kiro", "opencode", "windsurf"}
	targets, unsupported := migrateAPMTargets(legacy)
	if !slices.Equal(targets, want) {
		t.Fatalf("targets = %v, want %v", targets, want)
	}
	if !slices.Equal(unsupported, []string{"hermes-agent", "unknown"}) {
		t.Fatalf("unsupported = %v", unsupported)
	}
}

func TestMigrateAgentsToAPMDoesNotOverwriteExistingManifest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "settings.json")
	home := filepath.Join(dir, "home")
	apmHome := filepath.Join(home, ".apm")
	if err := os.MkdirAll(apmHome, 0o700); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(apmHome, "apm.yml")
	const existing = "name: existing\nversion: 1.0.0\n"
	if err := os.WriteFile(manifestPath, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(configPath, &config.RootConfig{
		Version: config.CurrentVersion,
		Agents:  config.AgentsConfig{Packages: []config.SkillPackage{{Source: "acme/shared"}}},
	}); err != nil {
		t.Fatal(err)
	}
	a := New(configPath, WithEnvLookup(func(name string) string {
		if name == "HOME" {
			return home
		}
		return ""
	}))

	result, err := a.MigrateAgentsToAPM()
	if err == nil || !strings.Contains(err.Error(), "merge legacy Omni agent entries") {
		t.Fatalf("error = %v, want explicit merge requirement", err)
	}
	if !result.AlreadyExists || result.MigratedPackages != 0 {
		t.Fatalf("result = %+v", result)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != existing {
		t.Fatalf("existing manifest changed: %q", data)
	}
	updated, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Agents.Packages) != 1 {
		t.Fatalf("legacy config changed: %+v", updated.Agents)
	}
}

func TestMigrateAgentsToAPMPreservesConfigWhenManifestWriteFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "settings.json")
	home := filepath.Join(dir, "home")
	if err := os.WriteFile(home, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(configPath, &config.RootConfig{
		Version: config.CurrentVersion,
		Agents:  config.AgentsConfig{Packages: []config.SkillPackage{{Source: "acme/shared"}}},
	}); err != nil {
		t.Fatal(err)
	}
	a := New(configPath, WithEnvLookup(func(name string) string {
		if name == "HOME" {
			return home
		}
		return ""
	}))

	if _, err := a.MigrateAgentsToAPM(); err == nil {
		t.Fatal("expected migration error")
	}
	updated, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Agents.Packages) != 1 {
		t.Fatalf("legacy config changed: %+v", updated.Agents)
	}
}

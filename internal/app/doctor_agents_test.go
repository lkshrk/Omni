package app_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func newDoctorAgentsApp(t *testing.T, agents config.AgentsConfig, opts ...func(*app.App)) *app.App {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	root := config.RootConfig{Version: config.CurrentVersion, Agents: agents}
	if err := config.Save(cfgPath, &root); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath, opts...)
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func doctorAgentsGroup(check *app.DoctorCheck, header string) *app.DoctorDetailGroup {
	for i := range check.Groups {
		if check.Groups[i].Header == header {
			return &check.Groups[i]
		}
	}
	return nil
}

func TestDoctorAgents_MasterDisabled(t *testing.T) {
	t.Parallel()
	a := newFeatureGateApp(t, config.Settings{AgentsDisabled: config.BoolPtr(true)})
	result, err := a.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check := doctorCheck(result, "agents")
	if check == nil {
		t.Fatal("agents check missing")
	}
	if check.Status != app.DoctorStatusOK || !strings.Contains(check.Message, "agents_disabled") {
		t.Errorf("check = %+v, want ok + disabled message", check)
	}
	if len(check.Groups) != 0 {
		t.Errorf("disabled master must not probe features, got groups %+v", check.Groups)
	}
}

func TestDoctorAgents_FeatureDisabledSingleLine(t *testing.T) {
	t.Parallel()
	a := newFeatureGateApp(t, config.Settings{McpDisabled: config.BoolPtr(true)})
	result, err := a.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check := doctorCheck(result, "agents")
	var mcpGroup *app.DoctorDetailGroup
	for i := range check.Groups {
		if check.Groups[i].Header == "mcp servers" {
			mcpGroup = &check.Groups[i]
		}
	}
	if mcpGroup == nil || len(mcpGroup.Items) != 1 || !strings.Contains(mcpGroup.Items[0], "disabled (mcp_disabled)") {
		t.Errorf("mcp group = %+v, want single disabled line", mcpGroup)
	}
}

func TestDoctorAgents_AdapterUnavailableWarns(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{id: "codex", available: false}
	a := newFeatureGateApp(t, config.Settings{}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	result, err := a.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check := doctorCheck(result, "agents")
	if check.Status != app.DoctorStatusWarn {
		t.Errorf("status = %s, want warn for unavailable adapter", check.Status)
	}
	found := false
	for _, g := range check.Groups {
		for _, item := range g.Items {
			if strings.Contains(item, "codex") && strings.Contains(item, "not found") {
				found = true
			}
		}
	}
	if !found {
		t.Error("missing 'codex ... not found' item")
	}
}

func TestDoctorAgents_NeverCallsAdapterList(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{id: "codex", available: true}
	a := newFeatureGateApp(t, config.Settings{}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	if _, err := a.Doctor(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stub.listCalls != 0 {
		t.Errorf("listCalls = %d, want 0 (doctor must not probe List)", stub.listCalls)
	}
}

func TestDoctorAgents_SkillsMissingWarn(t *testing.T) {
	t.Parallel()
	mcpStub := &stubMcpAdapter{id: "claude-code", available: true}
	pluginStub := &stubPluginAdapter{id: "claude-code", available: true}
	agents := config.AgentsConfig{
		Packages: []config.SkillPackage{{Source: "o/r"}},
	}
	a := newDoctorAgentsApp(t, agents, app.WithMcpAdapters([]app.McpAdapter{mcpStub}), app.WithPluginAdapters([]app.PluginAdapter{pluginStub}))
	result, err := a.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check := doctorCheck(result, "agents")
	if check.Status != app.DoctorStatusWarn {
		t.Errorf("status = %s, want warn for missing skill package", check.Status)
	}
	g := doctorAgentsGroup(check, "skills")
	if g == nil {
		t.Fatal("skills group missing")
	}
	found := false
	for _, item := range g.Items {
		if strings.Contains(item, "1 in manifest, 0 installed, 1 missing") {
			found = true
		}
	}
	if !found {
		t.Errorf("skills items = %+v, want '1 in manifest, 0 installed, 1 missing'", g.Items)
	}
}

func TestDoctorAgents_SkillsRunnerNotFoundWarn(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	mcpStub := &stubMcpAdapter{id: "claude-code", available: true}
	pluginStub := &stubPluginAdapter{id: "claude-code", available: true}
	a := newDoctorAgentsApp(t, config.AgentsConfig{}, app.WithMcpAdapters([]app.McpAdapter{mcpStub}), app.WithPluginAdapters([]app.PluginAdapter{pluginStub}))
	result, err := a.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check := doctorCheck(result, "agents")
	if check.Status != app.DoctorStatusWarn {
		t.Errorf("status = %s, want warn for missing runner", check.Status)
	}
	g := doctorAgentsGroup(check, "skills")
	if g == nil {
		t.Fatal("skills group missing")
	}
	found := false
	for _, item := range g.Items {
		if strings.Contains(item, "runner npx: not found on PATH") {
			found = true
		}
	}
	if !found {
		t.Errorf("skills items = %+v, want 'runner npx: not found on PATH'", g.Items)
	}
}

func TestDoctorAgents_PluginsUnavailableWarn(t *testing.T) {
	t.Parallel()
	stub := &stubPluginAdapter{id: "codex", available: false}
	a := newDoctorAgentsApp(t, config.AgentsConfig{}, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	result, err := a.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check := doctorCheck(result, "agents")
	if check.Status != app.DoctorStatusWarn {
		t.Errorf("status = %s, want warn for unavailable plugin adapter", check.Status)
	}
	found := false
	for _, g := range check.Groups {
		for _, item := range g.Items {
			if strings.Contains(item, "codex") && strings.Contains(item, "not found") {
				found = true
			}
		}
	}
	if !found {
		t.Error("missing 'codex ... not found' item")
	}
}

func TestDoctorAgents_SkillsDisabledSingleLine(t *testing.T) {
	t.Parallel()
	a := newFeatureGateApp(t, config.Settings{SkillsDisabled: config.BoolPtr(true)})
	result, err := a.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check := doctorCheck(result, "agents")
	g := doctorAgentsGroup(check, "skills")
	if g == nil || len(g.Items) != 1 || !strings.Contains(g.Items[0], "disabled (skills_disabled)") {
		t.Errorf("skills group = %+v, want single disabled line", g)
	}
}

func TestDoctorAgents_PluginsDisabledSingleLine(t *testing.T) {
	t.Parallel()
	a := newFeatureGateApp(t, config.Settings{PluginsDisabled: config.BoolPtr(true)})
	result, err := a.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check := doctorCheck(result, "agents")
	g := doctorAgentsGroup(check, "plugins")
	if g == nil || len(g.Items) != 1 || !strings.Contains(g.Items[0], "disabled (plugins_disabled)") {
		t.Errorf("plugins group = %+v, want single disabled line", g)
	}
}

func writeFakeClaudeOnPath(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "claude")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}

func TestDoctorAgents_PluginShaSourceOK(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFakeClaudeOnPath(t)
	pluginDir := filepath.Join(home, ".claude", "plugins")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{"plugins":{"foo@bar":[{"gitCommitSha":"abc123"}]}}`
	if err := os.WriteFile(filepath.Join(pluginDir, "installed_plugins.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	mcpStub := &stubMcpAdapter{id: "claude-code", available: true}
	pluginStub := &stubPluginAdapter{id: "claude-code", available: true}
	a := newDoctorAgentsApp(t, config.AgentsConfig{}, app.WithMcpAdapters([]app.McpAdapter{mcpStub}), app.WithPluginAdapters([]app.PluginAdapter{pluginStub}))
	result, err := a.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check := doctorCheck(result, "agents")
	g := doctorAgentsGroup(check, "plugins")
	if g == nil {
		t.Fatal("plugins group missing")
	}
	found := false
	for _, item := range g.Items {
		if strings.Contains(item, "update detection: sha source ok") {
			found = true
		}
	}
	if !found {
		t.Errorf("plugins items = %+v, want 'update detection: sha source ok'", g.Items)
	}
}

func TestDoctorAgents_PluginShaSourceMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFakeClaudeOnPath(t)
	mcpStub := &stubMcpAdapter{id: "claude-code", available: true}
	pluginStub := &stubPluginAdapter{id: "claude-code", available: true}
	a := newDoctorAgentsApp(t, config.AgentsConfig{}, app.WithMcpAdapters([]app.McpAdapter{mcpStub}), app.WithPluginAdapters([]app.PluginAdapter{pluginStub}))
	result, err := a.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check := doctorCheck(result, "agents")
	if check.Status != app.DoctorStatusOK {
		t.Errorf("status = %s, want ok (missing sha source is not a warn)", check.Status)
	}
	g := doctorAgentsGroup(check, "plugins")
	if g == nil {
		t.Fatal("plugins group missing")
	}
	found := false
	for _, item := range g.Items {
		if strings.Contains(item, "update detection: sha source unavailable (plugin update checks limited to declared versions)") {
			found = true
		}
	}
	if !found {
		t.Errorf("plugins items = %+v, want 'update detection: sha source unavailable...'", g.Items)
	}
}

func TestDoctorAgents_PluginShaSourceMalformed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFakeClaudeOnPath(t)
	pluginDir := filepath.Join(home, ".claude", "plugins")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "installed_plugins.json"), []byte("not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	mcpStub := &stubMcpAdapter{id: "claude-code", available: true}
	pluginStub := &stubPluginAdapter{id: "claude-code", available: true}
	a := newDoctorAgentsApp(t, config.AgentsConfig{}, app.WithMcpAdapters([]app.McpAdapter{mcpStub}), app.WithPluginAdapters([]app.PluginAdapter{pluginStub}))
	result, err := a.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check := doctorCheck(result, "agents")
	g := doctorAgentsGroup(check, "plugins")
	if g == nil {
		t.Fatal("plugins group missing")
	}
	found := false
	for _, item := range g.Items {
		if strings.Contains(item, "update detection: sha source unavailable (plugin update checks limited to declared versions)") {
			found = true
		}
	}
	if !found {
		t.Errorf("plugins items = %+v, want 'update detection: sha source unavailable...'", g.Items)
	}
}

func TestDoctorAgents_PluginShaSourceSkippedWithoutClaude(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	pluginStub := &stubPluginAdapter{id: "claude-code", available: false}
	a := newDoctorAgentsApp(t, config.AgentsConfig{}, app.WithPluginAdapters([]app.PluginAdapter{pluginStub}))
	result, err := a.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check := doctorCheck(result, "agents")
	g := doctorAgentsGroup(check, "plugins")
	if g == nil {
		t.Fatal("plugins group missing")
	}
	for _, item := range g.Items {
		if strings.Contains(item, "update detection:") {
			t.Errorf("plugins items = %+v, want no 'update detection:' line when claude not on PATH", g.Items)
		}
	}
}

func TestDoctorAgents_SkillPackageRowsErrorWarns(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	agentsDir := filepath.Join(home, ".agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, ".skill-lock.json"), []byte("not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	mcpStub := &stubMcpAdapter{id: "claude-code", available: true}
	pluginStub := &stubPluginAdapter{id: "claude-code", available: true}
	a := newDoctorAgentsApp(t, config.AgentsConfig{}, app.WithMcpAdapters([]app.McpAdapter{mcpStub}), app.WithPluginAdapters([]app.PluginAdapter{pluginStub}))
	result, err := a.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check := doctorCheck(result, "agents")
	if check.Status != app.DoctorStatusWarn {
		t.Errorf("status = %s, want warn for SkillPackageRows error", check.Status)
	}
	g := doctorAgentsGroup(check, "skills")
	if g == nil {
		t.Fatal("skills group missing")
	}
	found := false
	for _, item := range g.Items {
		if strings.Contains(item, "packages:") && !strings.Contains(item, "in manifest") {
			found = true
		}
	}
	if !found {
		t.Errorf("skills items = %+v, want a 'packages: <err>' line", g.Items)
	}
}

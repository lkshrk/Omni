package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestSkillPackageRowsPerAgentStatus(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("OMNI_HOSTNAME", "skills-row-host")
	stubBinariesOnPath(t, "claude", "cursor")
	for _, dir := range []string{".claude", ".cursor"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	source := filepath.Join(t.TempDir(), "row-skills")
	writeAppSkill(t, filepath.Join(source, "skills", "demo"), "demo")

	brew := &availabilityCountingProvider{name: "brew", available: true}
	path := filepath.Join(t.TempDir(), "settings.json")
	a := New(path)
	if err := a.InitTestMode(t.Context(), brew); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer a.Close() //nolint:errcheck

	service, err := a.skillService()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Install(context.Background(), config.SkillPackage{Source: source}, []string{"claude-code"}); err != nil {
		t.Fatal(err)
	}

	t.Run("targeted agents from package Agents", func(t *testing.T) {
		if err := a.withConfig(func(cfg *config.RootConfig) error {
			cfg.Agents.Packages = []config.SkillPackage{{Source: source, Agents: []string{"claude-code", "cursor"}}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		rows, err := a.SkillPackageRows(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 {
			t.Fatalf("rows = %v, want 1", rows)
		}
		status := rows[0].PerAgentStatus
		if status["claude-code"] != SkillStatusInstalled {
			t.Error("claude-code should be installed on disk")
		}
		if status["cursor"] == SkillStatusInstalled {
			t.Error("cursor should not be installed on disk")
		}
		if _, ok := status["codex"]; ok {
			t.Error("codex was not targeted, should not appear")
		}
	})

	t.Run("falls back to enabled agents when package Agents empty", func(t *testing.T) {
		if err := a.withConfig(func(cfg *config.RootConfig) error {
			cfg.Agents.Packages = []config.SkillPackage{{Source: source}}
			if cfg.HostSettings == nil {
				cfg.HostSettings = make(map[string]config.Settings)
			}
			settings := cfg.HostSettings["skills-row-host"]
			settings.AgentsUse = []string{"claude-code", "cursor"}
			cfg.HostSettings["skills-row-host"] = settings
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		rows, err := a.SkillPackageRows(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		status := rows[0].PerAgentStatus
		if _, ok := status["claude-code"]; !ok {
			t.Error("claude-code should be in the enabled-agents fallback set")
		}
		if _, ok := status["cursor"]; !ok {
			t.Errorf("cursor should be in the enabled-agents fallback set: %v", status)
		}
	})
}

func TestSkillPackageRows_ShadowedByPlugin_NotHidden(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	stubBinariesOnPath(t, "claude")
	source := filepath.Join(t.TempDir(), "owner", "academic-research-skills")
	writeAppSkill(t, filepath.Join(source, "skills", "demo"), "demo")

	brew := &availabilityCountingProvider{name: "brew", available: true}
	pluginStub := &shadowTestPluginAdapter{
		id:            "claude-code",
		listedPlugins: []InstalledPlugin{{Name: "academic-research-skills", Marketplace: "some-marketplace"}},
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	a := New(path, WithPluginAdapters([]PluginAdapter{pluginStub}))
	if err := a.InitTestMode(t.Context(), brew); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer a.Close() //nolint:errcheck

	service, err := a.skillService()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Install(context.Background(), config.SkillPackage{Source: source}, []string{"claude-code"}); err != nil {
		t.Fatal(err)
	}
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Agents.Packages = []config.SkillPackage{{Source: source, Agents: []string{"claude-code"}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	rows, err := a.SkillPackageRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %v, want 1 (kept, not hidden)", rows)
	}
	if !rows[0].ShadowedByPlugin {
		t.Fatalf("expected ShadowedByPlugin=true, got %+v", rows[0])
	}
}

func TestSkillPackageRows_UnknownAgentTargetDegradesToRowWarning(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	stubBinariesOnPath(t, "claude")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "typo-skills")
	writeAppSkill(t, filepath.Join(source, "skills", "demo"), "demo")

	a := newSkillsTestApp(t, config.AgentsConfig{
		Packages: []config.SkillPackage{{Source: source, Agents: []string{"claude-code", "clode-code"}}},
	})
	service, err := a.skillService()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Install(context.Background(), config.SkillPackage{Source: source}, []string{"claude-code"}); err != nil {
		t.Fatal(err)
	}

	rows, err := a.SkillPackageRows(context.Background())
	if err != nil {
		t.Fatalf("rows should degrade, not fail: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want 1", rows)
	}
	if len(rows[0].UnknownAgents) != 1 || rows[0].UnknownAgents[0] != "clode-code" {
		t.Fatalf("UnknownAgents = %v, want [clode-code]", rows[0].UnknownAgents)
	}
	if rows[0].PerAgentStatus["claude-code"] != SkillStatusInstalled {
		t.Error("the known target's status must still be reported")
	}
	if w := rows[0].UnknownAgentsWarning(); !strings.Contains(w, "clode-code") || !strings.Contains(w, "agents.packages") {
		t.Errorf("warning = %q, want the unknown ID and a fix hint", w)
	}

}

func TestSkillPackageRows_AgentsUseMatchesRestoreTargets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("OMNI_HOSTNAME", "agents-use-host")
	stubBinariesOnPath(t, "claude", "cursor")
	for _, dir := range []string{".claude", ".cursor"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	source := filepath.Join(t.TempDir(), "use-skills")
	writeAppSkill(t, filepath.Join(source, "skills", "demo"), "demo")

	a := New(filepath.Join(t.TempDir(), "settings.json"),
		WithMcpAdapters([]McpAdapter{&unavailableMcpAdapter{id: "claude-code"}}),
		WithPluginAdapters([]PluginAdapter{&unavailablePluginAdapter{id: "claude-code"}}))
	if err := a.InitTestMode(t.Context(), &availabilityCountingProvider{name: "brew", available: true}); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer a.Close() //nolint:errcheck

	service, err := a.skillService()
	if err != nil {
		t.Fatal(err)
	}
	pkg := config.SkillPackage{Source: source, Agents: []string{"claude-code", "cursor"}}
	if _, err := service.Install(context.Background(), pkg, []string{"claude-code", "cursor"}); err != nil {
		t.Fatal(err)
	}
	makeForeign(t, home, "cursor", "demo")
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Agents.Packages = []config.SkillPackage{pkg}
		if cfg.HostSettings == nil {
			cfg.HostSettings = make(map[string]config.Settings)
		}
		settings := cfg.HostSettings["agents-use-host"]
		settings.AgentsUse = []string{"claude-code"}
		cfg.HostSettings["agents-use-host"] = settings
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	resolved := a.resolveSkillPackages(cfg, currentMachineGroupName())
	if len(resolved) != 1 {
		t.Fatalf("resolved = %+v, want 1 package", resolved)
	}
	want := a.restoreTargetsFor(cfg, resolved[0])
	if !slices.Equal(want, []string{"claude-code"}) {
		t.Fatalf("restoreTargetsFor = %v, want [claude-code]", want)
	}

	rows, err := a.SkillPackageRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(rows[0].PerAgentStatus))
	for id := range rows[0].PerAgentStatus {
		got = append(got, id)
	}
	sort.Strings(got)
	if !slices.Equal(got, want) {
		t.Fatalf("PerAgentStatus targets = %v, want the restore targets %v", got, want)
	}

	counts := classifySkillRows(rows)
	if len(counts.Drifted) != 0 {
		t.Fatalf("Drifted = %v, want none: cursor is not managed on this host", counts.Drifted)
	}

	result, err := a.ResolveAllDrift(context.Background(), ResolveAllDriftOptions{UseManaged: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Resolved() != 0 || len(result.Errors) != 0 {
		t.Fatalf("result = %+v, want nothing to resolve and no unresolvable drift", result)
	}
}

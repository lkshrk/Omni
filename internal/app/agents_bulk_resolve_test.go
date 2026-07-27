package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func bulkDriftedSkillsApp(t *testing.T) (*App, string, []config.SkillPackage) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("OMNI_HOSTNAME", "bulk-resolve-host")
	stubBinariesOnPath(t, "claude", "cursor")
	for _, dir := range []string{".claude", ".cursor"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	a := New(filepath.Join(t.TempDir(), "settings.json"),
		WithMcpAdapters([]McpAdapter{&unavailableMcpAdapter{id: "claude-code"}}),
		WithPluginAdapters([]PluginAdapter{&unavailablePluginAdapter{id: "claude-code"}}))
	if err := a.InitTestMode(t.Context(), &availabilityCountingProvider{name: "brew", available: true}); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	service, err := a.skillService()
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"alpha", "beta"}
	agents := []string{"claude-code", "cursor"}
	var packages []config.SkillPackage
	for _, name := range names {
		source := filepath.Join(t.TempDir(), name+"-src")
		writeAppSkill(t, filepath.Join(source, "skills", name), name)
		pkg := config.SkillPackage{Source: source, Agents: agents}
		if _, err := service.Install(context.Background(), pkg, agents); err != nil {
			t.Fatalf("install %s: %v", name, err)
		}
		packages = append(packages, pkg)
	}
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Agents.Packages = packages
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		makeForeign(t, home, "claude-code", name)
	}
	return a, home, packages
}

func TestResolveAllDriftUseManagedResolvesEveryDriftedPackage(t *testing.T) {
	a, _, _ := bulkDriftedSkillsApp(t)

	result, err := a.ResolveAllDrift(context.Background(), ResolveAllDriftOptions{UseManaged: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.SkillsResolved != 2 {
		t.Fatalf("SkillsResolved = %d, want 2", result.SkillsResolved)
	}
	if result.McpResolved != 0 || result.PluginsResolved != 0 {
		t.Fatalf("mcp/plugins resolved = %d/%d, want 0/0 with nothing configured there",
			result.McpResolved, result.PluginsResolved)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}

	rows, err := a.SkillPackageRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if got := row.PerAgentStatus["claude-code"]; got != SkillStatusInstalled {
			t.Fatalf("%s PerAgentStatus[claude-code] = %q, want installed", row.Source, got)
		}
	}
}

func TestResolveAllDriftUseLocalNarrowsEveryDriftedPackage(t *testing.T) {
	a, _, packages := bulkDriftedSkillsApp(t)

	result, err := a.ResolveAllDrift(context.Background(), ResolveAllDriftOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.SkillsResolved != 2 {
		t.Fatalf("SkillsResolved = %d, want 2", result.SkillsResolved)
	}

	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.Packages) != len(packages) {
		t.Fatalf("packages = %d, want %d (use-local narrows, it does not remove)", len(cfg.Agents.Packages), len(packages))
	}
	for _, pkg := range cfg.Agents.Packages {
		for _, agent := range pkg.Agents {
			if agent == "claude-code" {
				t.Fatalf("package %s still targets claude-code after use-local narrowed it away", pkg.Source)
			}
		}
	}
}

func TestResolveAllDriftSurfacesPerItemRefusalsWithoutBlockingOthers(t *testing.T) {
	a, _, packages := bulkDriftedSkillsApp(t)
	// Narrowing to claude-code alone leaves no agents, so the per-item resolve refuses.
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		for i := range cfg.Agents.Packages {
			if cfg.Agents.Packages[i].Source == packages[1].Source {
				cfg.Agents.Packages[i].Agents = []string{"claude-code"}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	result, err := a.ResolveAllDrift(context.Background(), ResolveAllDriftOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.SkillsResolved != 1 {
		t.Fatalf("SkillsResolved = %d, want 1 (the other package still resolves)", result.SkillsResolved)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("Errors = %v, want exactly one refusal", result.Errors)
	}
}

type listFailingMcpAdapter struct{ unavailableMcpAdapter }

func (s *listFailingMcpAdapter) Available() bool { return true }
func (s *listFailingMcpAdapter) List(context.Context) ([]InstalledMcpServer, error) {
	return nil, errors.New("mcp cli unreachable")
}

type listFailingPluginAdapter struct{ unavailablePluginAdapter }

func (s *listFailingPluginAdapter) Available() bool { return true }
func (s *listFailingPluginAdapter) ListPlugins(context.Context) ([]InstalledPlugin, error) {
	return nil, errors.New("plugin cli unreachable")
}

func TestResolveAllDriftReportsUninspectedResourceClasses(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("OMNI_HOSTNAME", "bulk-resolve-blind-host")

	a := New(filepath.Join(t.TempDir(), "settings.json"),
		WithMcpAdapters([]McpAdapter{&listFailingMcpAdapter{unavailableMcpAdapter{id: "claude-code"}}}),
		WithPluginAdapters([]PluginAdapter{&listFailingPluginAdapter{unavailablePluginAdapter{id: "claude-code"}}}))
	if err := a.InitTestMode(t.Context(), &availabilityCountingProvider{name: "brew", available: true}); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Agents.McpServers = []config.McpServer{{Name: "srv", Transport: "stdio", Command: "npx srv"}}
		cfg.Agents.Marketplaces = []config.Marketplace{{Name: "declared", Source: "o/declared"}}
		cfg.Agents.Plugins = []config.Plugin{{Name: "helper", Marketplace: "declared"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	result, err := a.ResolveAllDrift(context.Background(), ResolveAllDriftOptions{UseManaged: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Resolved() != 0 {
		t.Fatalf("Resolved = %d, want 0 while nothing could be inspected", result.Resolved())
	}
	if !hasSubstring(result.Errors, "mcp: ") || !hasSubstring(result.Errors, "plugins: ") {
		t.Fatalf("Errors = %v, want a failed enumeration reported per resource class", result.Errors)
	}
}

func TestResolveAllDriftNoDriftIsANoOp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("OMNI_HOSTNAME", "bulk-resolve-clean-host")

	a := New(filepath.Join(t.TempDir(), "settings.json"))
	if err := a.InitTestMode(t.Context(), &availabilityCountingProvider{name: "brew", available: true}); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	result, err := a.ResolveAllDrift(context.Background(), ResolveAllDriftOptions{UseManaged: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Resolved() != 0 || len(result.Errors) != 0 {
		t.Fatalf("result = %+v, want an empty no-op on a clean host", result)
	}
}

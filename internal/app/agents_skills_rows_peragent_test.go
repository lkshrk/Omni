package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestSkillPackageRowsPerAgentStatus(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	stubBinariesOnPath(t, "claude", "cursor")
	if err := os.MkdirAll(filepath.Join(home, ".claude", "skills", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".cursor", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}

	brew := &availabilityCountingProvider{name: "brew", available: true}
	path := filepath.Join(t.TempDir(), "settings.json")
	a := New(path)
	if err := a.InitTestMode(t.Context(), brew); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer a.Close() //nolint:errcheck

	writeSkillLockFixture(t, home, config.SkillLockFile{Skills: map[string]config.SkillLockEntry{
		"demo": {Source: "o/r", UpdatedAt: "2026-06-01T00:00:00Z"},
	}})

	t.Run("targeted agents from package Agents", func(t *testing.T) {
		if err := a.withConfig(func(cfg *config.RootConfig) error {
			cfg.Agents.Packages = []config.SkillPackage{{Source: "o/r", Agents: []string{"claude-code", "cursor"}}}
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
		if !status["claude-code"] {
			t.Error("claude-code should be installed on disk")
		}
		if status["cursor"] {
			t.Error("cursor should not be installed on disk")
		}
		if _, ok := status["codex"]; ok {
			t.Error("codex was not targeted, should not appear")
		}
	})

	t.Run("falls back to enabled agents when package Agents empty", func(t *testing.T) {
		if err := a.withConfig(func(cfg *config.RootConfig) error {
			cfg.Agents.Packages = []config.SkillPackage{{Source: "o/r"}}
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
			t.Error("cursor should be in the enabled-agents fallback set")
		}
	})
}

// TestSkillPackageRows_ShadowedByPlugin_NotHidden mirrors
// TestMcpServerRows_ManifestEntryShadowedByPlugin_NotHidden: a manifest skill
// package is declared intent, so it must stay visible even when an installed
// plugin now provides the same-named skill — flagged via ShadowedByPlugin,
// not hidden or reported as a plain gap.
func TestSkillPackageRows_ShadowedByPlugin_NotHidden(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	stubBinariesOnPath(t, "claude")
	if err := os.MkdirAll(filepath.Join(home, ".claude", "skills", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}

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

	writeSkillLockFixture(t, home, config.SkillLockFile{Skills: map[string]config.SkillLockEntry{
		"demo": {Source: "owner/academic-research-skills", UpdatedAt: "2026-06-01T00:00:00Z"},
	}})
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Agents.Packages = []config.SkillPackage{{Source: "owner/academic-research-skills", Agents: []string{"claude-code"}}}
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

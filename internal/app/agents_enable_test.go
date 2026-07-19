package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestAgentsEnabledDefaultAndDisabled(t *testing.T) {
	t.Parallel()
	a := &App{}

	if !a.AgentsEnabled(&config.RootConfig{}) {
		t.Error("agents must be enabled by default (nil agents_disabled)")
	}

	disabled := &config.RootConfig{Settings: config.Settings{AgentsDisabled: config.BoolPtr(true)}}
	if a.AgentsEnabled(disabled) {
		t.Error("agents must be disabled when agents_disabled=true")
	}
	if err := a.requireAgentsEnabled(disabled); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Errorf("requireAgentsEnabled = %v, want a 'disabled' error", err)
	}

	enabled := &config.RootConfig{Settings: config.Settings{AgentsDisabled: config.BoolPtr(false)}}
	if err := a.requireAgentsEnabled(enabled); err != nil {
		t.Errorf("requireAgentsEnabled(enabled) = %v, want nil", err)
	}
}

func TestSkillsEnabledMatrix(t *testing.T) {
	t.Parallel()
	a := &App{}
	if !a.SkillsEnabled(&config.RootConfig{}) {
		t.Error("skills must be enabled by default")
	}
	flagOff := &config.RootConfig{Settings: config.Settings{SkillsDisabled: config.BoolPtr(true)}}
	if a.SkillsEnabled(flagOff) {
		t.Error("skills must be disabled when skills_disabled=true")
	}
	if err := a.requireSkillsEnabled(flagOff); err == nil || !strings.Contains(err.Error(), "skills are disabled") {
		t.Errorf("requireSkillsEnabled = %v, want skills-disabled error", err)
	}
	masterOff := &config.RootConfig{Settings: config.Settings{AgentsDisabled: config.BoolPtr(true)}}
	if a.SkillsEnabled(masterOff) {
		t.Error("master off must disable skills")
	}
	if err := a.requireSkillsEnabled(masterOff); err == nil || !strings.Contains(err.Error(), "agent skills are disabled") {
		t.Errorf("master-off error = %v, want master message", err)
	}
	explicit := &config.RootConfig{Settings: config.Settings{SkillsDisabled: config.BoolPtr(false)}}
	if err := a.requireSkillsEnabled(explicit); err != nil {
		t.Errorf("explicit false = %v, want nil", err)
	}
}

func TestMcpEnabledMatrix(t *testing.T) {
	t.Parallel()
	a := &App{}
	if !a.McpEnabled(&config.RootConfig{}) {
		t.Error("mcp must be enabled by default")
	}
	flagOff := &config.RootConfig{Settings: config.Settings{McpDisabled: config.BoolPtr(true)}}
	if a.McpEnabled(flagOff) {
		t.Error("mcp must be disabled when mcp_disabled=true")
	}
	if err := a.requireMcpEnabled(flagOff); err == nil || !strings.Contains(err.Error(), "mcp servers are disabled") {
		t.Errorf("requireMcpEnabled = %v, want mcp-disabled error", err)
	}
	masterOff := &config.RootConfig{Settings: config.Settings{AgentsDisabled: config.BoolPtr(true)}}
	if a.McpEnabled(masterOff) {
		t.Error("master off must disable mcp")
	}
	if err := a.requireMcpEnabled(masterOff); err == nil || !strings.Contains(err.Error(), "agent skills are disabled") {
		t.Errorf("master-off error = %v, want master message", err)
	}
	explicit := &config.RootConfig{Settings: config.Settings{McpDisabled: config.BoolPtr(false)}}
	if err := a.requireMcpEnabled(explicit); err != nil {
		t.Errorf("explicit false = %v, want nil", err)
	}
}

func TestPluginsEnabledMatrix(t *testing.T) {
	t.Parallel()
	a := &App{}
	if !a.PluginsEnabled(&config.RootConfig{}) {
		t.Error("plugins must be enabled by default")
	}
	flagOff := &config.RootConfig{Settings: config.Settings{PluginsDisabled: config.BoolPtr(true)}}
	if a.PluginsEnabled(flagOff) {
		t.Error("plugins must be disabled when plugins_disabled=true")
	}
	if err := a.requirePluginsEnabled(flagOff); err == nil || !strings.Contains(err.Error(), "plugins are disabled") {
		t.Errorf("requirePluginsEnabled = %v, want plugins-disabled error", err)
	}
	masterOff := &config.RootConfig{Settings: config.Settings{AgentsDisabled: config.BoolPtr(true)}}
	if a.PluginsEnabled(masterOff) {
		t.Error("master off must disable plugins")
	}
	if err := a.requirePluginsEnabled(masterOff); err == nil || !strings.Contains(err.Error(), "agent skills are disabled") {
		t.Errorf("master-off error = %v, want master message", err)
	}
	explicit := &config.RootConfig{Settings: config.Settings{PluginsDisabled: config.BoolPtr(false)}}
	if err := a.requirePluginsEnabled(explicit); err != nil {
		t.Errorf("explicit false = %v, want nil", err)
	}
}

// newAgentPickerApp builds an App with claude-code and cursor detected on a
// fake HOME. agentsUse == nil leaves the per-host agents_use unset (inherit
// the nil-sentinel default). A non-nil slice (including empty) is written as
// raw host-settings JSON: config.Settings.AgentsUse has `omitempty`, so
// round-tripping an empty slice through the Go struct would silently turn it
// back into nil and defeat the "explicit empty" test case.
func newAgentPickerApp(t *testing.T, agentsUse []string) *App {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	stubBinariesOnPath(t, "claude", "cursor")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OMNI_HOSTNAME", "picker-host")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	raw := fmt.Sprintf(`{"version":%d}`, config.CurrentVersion)
	if agentsUse != nil {
		idsJSON, err := json.Marshal(agentsUse)
		if err != nil {
			t.Fatal(err)
		}
		raw = fmt.Sprintf(`{"version":%d,"host_settings":{"picker-host":{"agents_use":%s}}}`, config.CurrentVersion, idsJSON)
	}
	if err := os.WriteFile(cfgPath, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	a := New(cfgPath)
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func TestAgentPickerRowsEnabledState(t *testing.T) {
	t.Run("nil agents_use enables all detected agents", func(t *testing.T) {
		a := newAgentPickerApp(t, nil)
		rows, err := a.AgentPickerRows()
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 {
			t.Fatal("expected detected agent rows")
		}
		for _, row := range rows {
			if !row.Enabled {
				t.Errorf("row %q: Enabled = false, want true (nil agents_use means all detected agents active)", row.ID)
			}
		}
	})

	t.Run("empty agents_use disables all agents", func(t *testing.T) {
		a := newAgentPickerApp(t, []string{})
		rows, err := a.AgentPickerRows()
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 {
			t.Fatal("expected detected agent rows")
		}
		for _, row := range rows {
			if row.Enabled {
				t.Errorf("row %q: Enabled = true, want false (explicit empty agents_use)", row.ID)
			}
		}
	})

	t.Run("agents_use lists only claude-code enabled", func(t *testing.T) {
		a := newAgentPickerApp(t, []string{"claude-code"})
		rows, err := a.AgentPickerRows()
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 {
			t.Fatal("expected detected agent rows")
		}
		for _, row := range rows {
			want := row.ID == "claude-code"
			if row.Enabled != want {
				t.Errorf("row %q: Enabled = %v, want %v", row.ID, row.Enabled, want)
			}
		}
	})
}

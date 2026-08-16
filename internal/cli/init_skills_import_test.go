package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/executor"
)

// A CLI-era install: recorded in the legacy lockfile, absent from the manifest.
func legacySkillsHome(t *testing.T, source string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))

	skillDir := filepath.Join(home, ".agents", "skills", "demo")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: demo\ndescription: legacy skill\n---\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	lock, err := json.Marshal(config.SkillLockFile{
		Version: 3,
		Skills:  map[string]config.SkillLockEntry{"demo": {Source: source}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".agents", ".skill-lock.json"), lock, 0o644); err != nil {
		t.Fatal(err)
	}
}

func skillsImportTestApp(t *testing.T, settings config.Settings) (*app.App, string, *cobra.Command, *bytes.Buffer) {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	settings.AgentsUse = []string{"codex"}
	withConfig(t, cfgPath, &config.RootConfig{Settings: settings})
	withHost(t, cfgPath)
	a, err := buildTestApp(t, cfgPath)
	if err != nil {
		t.Fatalf("buildTestApp: %v", err)
	}
	out := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cmd.SetOut(out)
	cmd.SetErr(out)
	return a, cfgPath, cmd, out
}

func manifestSkillSources(t *testing.T, cfgPath string) []string {
	t.Helper()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	sources := make([]string, 0, len(cfg.Agents.Packages))
	for _, p := range cfg.Agents.Packages {
		sources = append(sources, p.Source)
	}
	return sources
}

func TestRunSkillsImportSection_NoImportFlagSkipsSilently(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	legacySkillsHome(t, "o/demo")
	a, cfgPath, cmd, out := skillsImportTestApp(t, config.Settings{})

	runSkillsImportSection(cmd, nil, a, skillsImportChoice{skip: true})

	if out.String() != "" {
		t.Fatalf("output = %q, want nothing", out.String())
	}
	if got := manifestSkillSources(t, cfgPath); len(got) != 0 {
		t.Fatalf("manifest packages = %v, want none", got)
	}
}

func TestRunSkillsImportSectionInstallsConfiguredPackages(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex"}},
		Agents:   config.AgentsConfig{Packages: []config.SkillPackage{{Source: "acme/demo", Agents: []string{"codex"}}}},
	})
	a := app.New(cfgPath)
	mock := &executor.MockExecutor{}
	a.SetFallbackExecutor(mock)
	out := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(out)
	cmd.SetErr(out)

	runSkillsImportSection(cmd, nil, a, skillsImportChoice{})

	if !bytes.Contains(out.Bytes(), []byte("Installed 1 agent packages")) {
		t.Fatalf("output = %q", out.String())
	}
	want := []string{"install", "--global", "--target", "codex", "acme/demo"}
	if len(mock.Calls) != 1 || !reflect.DeepEqual(mock.Calls[0].Args, want) {
		t.Fatalf("calls = %#v, want apm %v", mock.Calls, want)
	}
	if got := manifestSkillSources(t, cfgPath); len(got) != 1 {
		t.Fatalf("config mutated: %v", got)
	}
}

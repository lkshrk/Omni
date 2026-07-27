package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
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

func TestRunSkillsImportSection_ImportFlagSkipsPromptAndAdopts(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	legacySkillsHome(t, "o/demo")
	a, cfgPath, cmd, out := skillsImportTestApp(t, config.Settings{})

	runSkillsImportSection(cmd, nil, a, skillsImportChoice{force: true})

	if !strings.Contains(out.String(), "1 added") {
		t.Fatalf("output = %q, want an import summary", out.String())
	}
	if got := manifestSkillSources(t, cfgPath); len(got) != 1 || got[0] != "o/demo" {
		t.Fatalf("manifest packages = %v, want [o/demo]", got)
	}
	link := filepath.Join(os.Getenv("HOME"), ".agents", "skills", "demo")
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("legacy skill dir: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("%s is still a real directory, want an adopted link", link)
	}
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

func TestRunSkillsImportSection_PromptDefaultsToImport(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	legacySkillsHome(t, "o/demo")
	a, cfgPath, cmd, out := skillsImportTestApp(t, config.Settings{})

	withMockStdin(t, "\n", func() {
		runSkillsImportSection(cmd, nil, a, skillsImportChoice{})
	})

	if !strings.Contains(out.String(), "1 added") {
		t.Fatalf("output = %q, want an import summary", out.String())
	}
	if got := manifestSkillSources(t, cfgPath); len(got) != 1 || got[0] != "o/demo" {
		t.Fatalf("manifest packages = %v, want [o/demo]", got)
	}
}

func TestRunSkillsImportSection_PromptDeclinedLeavesLegacyInstall(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	legacySkillsHome(t, "o/demo")
	a, cfgPath, cmd, _ := skillsImportTestApp(t, config.Settings{})

	withMockStdin(t, "n\n", func() {
		runSkillsImportSection(cmd, nil, a, skillsImportChoice{})
	})

	if got := manifestSkillSources(t, cfgPath); len(got) != 0 {
		t.Fatalf("manifest packages = %v, want none", got)
	}
	dir := filepath.Join(os.Getenv("HOME"), ".agents", "skills", "demo")
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatalf("legacy skill dir: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Errorf("%s was adopted without consent", dir)
	}
}

func TestRunSkillsImportSection_NonTerminalStdinLeavesLegacyInstall(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	legacySkillsHome(t, "o/demo")
	a, cfgPath, cmd, out := skillsImportTestApp(t, config.Settings{})

	withMockStdin(t, "", func() {
		withMockTerminal(t, false, func() {
			runSkillsImportSection(cmd, nil, a, skillsImportChoice{})
		})
	})

	if got := manifestSkillSources(t, cfgPath); len(got) != 0 {
		t.Fatalf("manifest packages = %v, want none", got)
	}
	dir := filepath.Join(os.Getenv("HOME"), ".agents", "skills", "demo")
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatalf("legacy skill dir: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Errorf("%s was adopted without consent on non-terminal stdin", dir)
	}
	// The prompt itself goes to the process stdout rather than this writer; what matters here is that nothing was adopted, which the manifest and symlink checks above already establish.
	if strings.Contains(out.String(), "added") {
		t.Errorf("output = %q, want no import report when the prompt goes unanswered", out.String())
	}
}

func TestRunSkillsImportSection_NoCandidatesIsSilent(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	a, _, cmd, out := skillsImportTestApp(t, config.Settings{})

	runSkillsImportSection(cmd, nil, a, skillsImportChoice{})

	if out.String() != "" {
		t.Fatalf("output = %q, want nothing without candidates", out.String())
	}
}

func TestRunSkillsImportSection_SkillsDisabledIsSilent(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	legacySkillsHome(t, "o/demo")
	a, cfgPath, cmd, out := skillsImportTestApp(t, config.Settings{SkillsDisabled: config.BoolPtr(true)})

	runSkillsImportSection(cmd, nil, a, skillsImportChoice{force: true})

	if out.String() != "" {
		t.Fatalf("output = %q, want nothing when skills are disabled", out.String())
	}
	if got := manifestSkillSources(t, cfgPath); len(got) != 0 {
		t.Fatalf("manifest packages = %v, want none", got)
	}
}

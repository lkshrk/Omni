package app

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

// stubBinariesOnPath points PATH at a tmp dir containing executable stub
// files for each given name, so binaryOnPath sees them via exec.LookPath
// without touching the real PATH.
func stubBinariesOnPath(t *testing.T, names ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		p := filepath.Join(dir, name)
		if runtime.GOOS == "windows" {
			p += ".exe"
		}
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

func TestInstalledAgentsDetectsGrok(t *testing.T) {
	home := t.TempDir()
	stubBinariesOnPath(t, "grok")
	if err := os.MkdirAll(filepath.Join(home, ".grok", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := InstalledAgents(home)
	for _, a := range got {
		if a.ID == "grok" {
			return
		}
	}
	t.Fatalf("InstalledAgents = %+v, want grok detected", got)
}

func TestInstalledAgentsDetectsConfigDirs(t *testing.T) {
	home := t.TempDir()
	stubBinariesOnPath(t, "claude", "cursor")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := InstalledAgents(home)
	ids := make([]string, len(got))
	for i, a := range got {
		ids[i] = a.ID
	}
	want := map[string]bool{"claude-code": true, "cursor": true}
	if len(ids) != 2 {
		t.Fatalf("InstalledAgents = %v, want exactly claude-code+cursor", ids)
	}
	for _, id := range ids {
		if !want[id] {
			t.Errorf("unexpected agent %q detected", id)
		}
	}
}

func TestInstalledAgentsSharedDirNoBinaryNeverDetected(t *testing.T) {
	home := t.TempDir()
	stubBinariesOnPath(t) // empty PATH dir, nothing resolvable
	if err := os.MkdirAll(filepath.Join(home, ".agents", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := InstalledAgents(home)
	for _, a := range got {
		if a.configDir == ".agents" {
			t.Errorf("agent %q on shared dir with no binary must never be detected, got %+v", a.ID, got)
		}
	}
}

func TestInstalledAgentsSharedDirWithBinaryOnPathDetected(t *testing.T) {
	home := t.TempDir()
	stubBinariesOnPath(t, "cline")
	if err := os.MkdirAll(filepath.Join(home, ".agents", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := InstalledAgents(home)
	found := false
	for _, a := range got {
		if a.ID == "cline" {
			found = true
		}
	}
	if !found {
		t.Errorf("cline (shared dir, binary on PATH) should be detected, got %+v", got)
	}
}

func TestInstalledAgentsDedicatedDirEmptyNotDetected(t *testing.T) {
	home := t.TempDir()
	stubBinariesOnPath(t)
	// openclaw: dedicated dir, no binary configured.
	if err := os.MkdirAll(filepath.Join(home, ".openclaw"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := InstalledAgents(home)
	for _, a := range got {
		if a.ID == "openclaw" {
			t.Errorf("openclaw with empty dedicated dir must not be detected, got %+v", got)
		}
	}
}

func TestInstalledAgentsDedicatedDirNonEmptyNoBinaryDetected(t *testing.T) {
	home := t.TempDir()
	stubBinariesOnPath(t)
	if err := os.MkdirAll(filepath.Join(home, ".openclaw", "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := InstalledAgents(home)
	found := false
	for _, a := range got {
		if a.ID == "openclaw" {
			found = true
		}
	}
	if !found {
		t.Errorf("openclaw with non-empty dedicated dir (no binary set) should be detected, got %+v", got)
	}
}

func TestInstalledAgentsDedicatedDirBinarySetButMissingNotDetected(t *testing.T) {
	home := t.TempDir()
	stubBinariesOnPath(t) // cursor binary absent
	if err := os.MkdirAll(filepath.Join(home, ".cursor", "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := InstalledAgents(home)
	for _, a := range got {
		if a.ID == "cursor" {
			t.Errorf("cursor (dedicated dir, binary set but missing from PATH) must not be detected, got %+v", got)
		}
	}
}

func TestInstalledAgentsHonorsConfigEnvOverride(t *testing.T) {
	home := t.TempDir()
	stubBinariesOnPath(t, "codex")
	override := t.TempDir() // exists, but is not under home/.codex
	t.Setenv("CODEX_HOME", override)
	got := InstalledAgents(home)
	found := false
	for _, a := range got {
		if a.ID == "codex" {
			found = true
		}
	}
	if !found {
		t.Errorf("codex not detected via CODEX_HOME override; got %+v", got)
	}
}

func TestAgentHasAnySkill(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	codex := AgentInfo{ID: "codex", configDir: ".codex", extraSkillsDirs: []string{".agents/skills"}}
	// primary dir hit
	if err := os.MkdirAll(filepath.Join(home, ".codex", "skills", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !agentHasAnySkill(home, codex, []string{"demo"}) {
		t.Error("codex should have demo via ~/.codex/skills")
	}
	if agentHasAnySkill(home, codex, []string{"absent"}) {
		t.Error("absent skill should not match")
	}
	// shared ~/.agents/skills hit
	if err := os.MkdirAll(filepath.Join(home, ".agents", "skills", "shared"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !agentHasAnySkill(home, codex, []string{"shared"}) {
		t.Error("codex should have shared via ~/.agents/skills extra dir")
	}
}

func TestEffectiveSkillAgents(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		use  []string
		pkg  config.SkillPackage
		want []string
	}{
		{name: "nil use, no skill agents -> nil (auto)", use: nil, pkg: config.SkillPackage{}, want: nil},
		{name: "nil use, skill agents -> skill agents", use: nil, pkg: config.SkillPackage{Agents: []string{"codex"}}, want: []string{"codex"}},
		{name: "use set, no skill agents -> use", use: []string{"claude-code", "codex"}, pkg: config.SkillPackage{}, want: []string{"claude-code", "codex"}},
		{name: "intersection", use: []string{"claude-code", "codex"}, pkg: config.SkillPackage{Agents: []string{"codex", "cursor"}}, want: []string{"codex"}},
		{name: "empty intersection", use: []string{"claude-code"}, pkg: config.SkillPackage{Agents: []string{"codex"}}, want: []string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := effectiveSkillAgents(tc.use, tc.pkg)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("got %v, want nil", got)
				}
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// TestAgentsCatalog_ConfigDirsMatchConfigList guards against supportedAgents
// and config.AgentConfigDirs() (the duplicate list config/migrate_v14.go and
// dots discovery rely on, since internal/config cannot import internal/app)
// silently drifting apart.
func TestAgentsCatalog_ConfigDirsMatchConfigList(t *testing.T) {
	t.Parallel()
	catalog := make(map[string]struct{}, len(supportedAgents))
	for _, a := range supportedAgents {
		catalog[a.configDir] = struct{}{}
	}
	fromConfig := make(map[string]struct{}, len(config.AgentConfigDirs()))
	for _, dir := range config.AgentConfigDirs() {
		fromConfig[dir] = struct{}{}
	}
	for dir := range catalog {
		if _, ok := fromConfig[dir]; !ok {
			t.Errorf("supportedAgents configDir %q missing from config.AgentConfigDirs()", dir)
		}
	}
	for dir := range fromConfig {
		if _, ok := catalog[dir]; !ok {
			t.Errorf("config.AgentConfigDirs() has %q not present in any supportedAgents configDir", dir)
		}
	}
}

// TestInstalledAgentsSharedDirRequiresAgentSpecificSignal pins the deliberate
// trade-off for shared configDirs (.zencoder is shared by zencoder+zenflow,
// .config/agents by amp/replit/universal): detection requires the agent's
// binary, because InstalledAgents feeds restore targets and a false positive
// would write skills/config for a product that is not installed. Binary-less
// agents on shared dirs stay undetected; users target them via agents_use.
func TestInstalledAgentsSharedDirRequiresAgentSpecificSignal(t *testing.T) {
	home := t.TempDir()
	stubBinariesOnPath(t) // empty PATH, no binaries resolvable
	for _, dir := range [][]string{{".zencoder", "skills"}, {".config", "agents", "skills"}} {
		if err := os.MkdirAll(filepath.Join(append([]string{home}, dir...)...), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got := InstalledAgents(home); len(got) != 0 {
		t.Fatalf("InstalledAgents = %+v, want none detected from shared dirs without binaries", got)
	}
}

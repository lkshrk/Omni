package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestInstalledAgentsDetectsConfigDirs(t *testing.T) {
	home := t.TempDir()
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

func TestInstalledAgentsHonorsConfigEnvOverride(t *testing.T) {
	home := t.TempDir()
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

func TestEffectiveSkillAgents(t *testing.T) {
	tests := []struct {
		name  string
		use   []string
		skill config.ManifestSkill
		want  []string
	}{
		{name: "nil use, no skill agents -> nil (auto)", use: nil, skill: config.ManifestSkill{}, want: nil},
		{name: "nil use, skill agents -> skill agents", use: nil, skill: config.ManifestSkill{Agents: []string{"codex"}}, want: []string{"codex"}},
		{name: "use set, no skill agents -> use", use: []string{"claude-code", "codex"}, skill: config.ManifestSkill{}, want: []string{"claude-code", "codex"}},
		{name: "intersection", use: []string{"claude-code", "codex"}, skill: config.ManifestSkill{Agents: []string{"codex", "cursor"}}, want: []string{"codex"}},
		{name: "empty intersection", use: []string{"claude-code"}, skill: config.ManifestSkill{Agents: []string{"codex"}}, want: []string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := effectiveSkillAgents(tc.use, tc.skill)
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

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestEnabledAgentIDs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	stubBinariesOnPath(t, "claude", "cursor")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}

	a := &App{}
	tests := []struct {
		name string
		cfg  *config.RootConfig
		want []string
	}{
		{name: "nil use -> all installed agents", cfg: &config.RootConfig{}, want: []string{"claude-code", "cursor"}},
		{name: "explicit empty -> empty", cfg: &config.RootConfig{Settings: config.Settings{AgentsUse: []string{}}}, want: []string{}},
		{name: "explicit subset of installed -> sorted subset", cfg: &config.RootConfig{Settings: config.Settings{AgentsUse: []string{"claude-code"}}}, want: []string{"claude-code"}},
		{name: "explicit list with uninstalled agent -> excluded", cfg: &config.RootConfig{Settings: config.Settings{AgentsUse: []string{"codex", "claude-code"}}}, want: []string{"claude-code"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := a.EnabledAgentIDs(tc.cfg)
			if len(got) != len(tc.want) {
				t.Fatalf("EnabledAgentIDs = %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("EnabledAgentIDs = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

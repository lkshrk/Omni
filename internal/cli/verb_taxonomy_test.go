package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

// Returns stdout and stderr separately so the deprecation notice can be told apart from the operation's own output.
func runAgentsSubcommand(t *testing.T, a *app.App, args ...string) (string, string, error) {
	t.Helper()
	cmd := newAgentsCmd(&rootState{app: a})
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return out.String(), errOut.String(), err
}

// A renamed verb's old spelling must reach the same operation and say so once.

func TestToolsRemove_UndeclaresWithoutProvider(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Tools:  map[string]config.ToolSpec{"ripgrep": {Provider: "system"}},
		Groups: []*config.GroupConfig{cliTestHostGroup("ripgrep")},
	})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"-y", "--config", cfgPath, "--cache-dir", t.TempDir(), "tools", "remove", "ripgrep"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("tools remove: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, ok := cfg.Tools["ripgrep"]; ok {
		t.Error("tools remove left the logical tool spec in config")
	}
	for _, group := range cfg.Groups {
		for _, tool := range group.Tools {
			if tool.Name == "ripgrep" {
				t.Errorf("tools remove left a membership in group %q", group.Name)
			}
		}
	}
}

// dots remove has no manifest-only mode: the repo package is the entry, and --purge also drops the local target.
func TestDotsRemove_PurgeRejectsContradictoryKeepLocal(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{Settings: config.Settings{DotsRepo: t.TempDir()}})

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"-y", "--config", cfgPath, "--cache-dir", t.TempDir(), "dots", "remove", "nvim", "--purge", "--keep-local"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--purge") {
		t.Fatalf("err = %v, want a --purge/--keep-local conflict error", err)
	}
}

// Every remove verb must name --purge or explain why that flag does not exist on its surface.
func TestRemoveHelpNamesItsCounterpart(t *testing.T) {
	root := NewRootCmd()
	for _, path := range [][]string{
		{"tools", "remove"},
		{"dots", "remove"},
		{"agents", "remove"},
		{"hosts", "remove"},
		{"groups", "remove-tool"},
	} {
		cmd := findCommand(root, path)
		if cmd == nil {
			t.Fatalf("missing command %q", strings.Join(path, " "))
		}
		help := cmd.Short + "\n" + cmd.Long
		if !mentionsPurgeOrItsAbsence(help) {
			t.Errorf("%q help does not say what happens to the live side:\n%s",
				strings.Join(path, " "), help)
		}
	}
}

func mentionsPurgeOrItsAbsence(help string) bool {
	if strings.Contains(help, "--purge") {
		return true
	}
	// Surfaces without a managed copy explain why they cannot keep one.
	for _, phrase := range []string{"no --purge", "Nothing installed", "nothing installed", "agents keep", "keep it"} {
		if strings.Contains(help, phrase) {
			return true
		}
	}
	return false
}

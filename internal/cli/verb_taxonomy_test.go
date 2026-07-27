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
func TestDeprecatedAliasRunsCanonicalOperation(t *testing.T) {
	for _, spec := range []struct {
		name      string
		canonical []string
		alias     []string
		notice    string
	}{
		{
			name:      "agents sync",
			canonical: []string{"sync", "--dry-run"},
			alias:     []string{"restore", "--dry-run"},
			notice:    `note: "agents restore" renamed to "agents sync"`,
		},
		{
			name:      "agents skills sync",
			canonical: []string{"skills", "sync", "--dry-run"},
			alias:     []string{"skills", "restore", "--dry-run"},
			notice:    `note: "agents skills restore" renamed to "agents skills sync"`,
		},
		{
			name:      "agents skills upgrade",
			canonical: []string{"skills", "upgrade", "--dry-run"},
			alias:     []string{"skills", "update", "--dry-run"},
			notice:    `note: "agents skills update" renamed to "agents skills upgrade"`,
		},
		{
			name:      "agents mcp sync",
			canonical: []string{"mcp", "sync", "--dry-run"},
			alias:     []string{"mcp", "restore", "--dry-run"},
			notice:    `note: "agents mcp restore" renamed to "agents mcp sync"`,
		},
		{
			name:      "agents plugins sync",
			canonical: []string{"plugins", "sync", "--dry-run"},
			alias:     []string{"plugins", "restore", "--dry-run"},
			notice:    `note: "agents plugins restore" renamed to "agents plugins sync"`,
		},
	} {
		t.Run(spec.name, func(t *testing.T) {
			want, wantErrOut, wantErr := runAgentsSubcommand(t, newAgentsSyncTestApp(t, config.Settings{}), spec.canonical...)
			if wantErr != nil {
				t.Fatalf("%v: %v", spec.canonical, wantErr)
			}
			if strings.Contains(wantErrOut, "note:") {
				t.Errorf("canonical spelling printed a rename notice: %q", wantErrOut)
			}

			got, gotErrOut, err := runAgentsSubcommand(t, newAgentsSyncTestApp(t, config.Settings{}), spec.alias...)
			if err != nil {
				t.Fatalf("%v: %v", spec.alias, err)
			}
			if got != want {
				t.Errorf("alias output = %q, want the canonical %q", got, want)
			}
			if strings.Count(gotErrOut, "note:") != 1 {
				t.Errorf("alias stderr = %q, want exactly one rename notice", gotErrOut)
			}
			if !strings.Contains(gotErrOut, spec.notice) {
				t.Errorf("alias stderr = %q, want %q", gotErrOut, spec.notice)
			}
		})
	}
}

// Without --purge nothing is uninstalled, so no provider is needed.
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
		{"agents", "skills", "remove"},
		{"agents", "mcp", "remove"},
		{"agents", "plugins", "remove"},
		{"agents", "plugins", "marketplace", "remove"},
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

package cli

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/executor"
)

const cliTestPinnedAPMVersion = "0.29.0"

func TestAgentsCommandsDelegateToAPM(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"sync", []string{"sync", "--frozen", "--dry-run"}, []string{"install", "-g", "--trust-transitive-mcp", "--frozen", "--dry-run"}},
		{"add skill subset", []string{"add", "owner/pkg", "--skill", "one", "--skill", "two"}, []string{"install", "-g", "owner/pkg", "--skill", "one", "--skill", "two"}},
		{"remove", []string{"remove", "owner/one", "owner/two"}, []string{"uninstall", "-g", "owner/one", "owner/two"}},
		{"update", []string{"update"}, []string{"update", "-g", "--yes"}},
		{"search", []string{"search", "security"}, []string{"search", "security"}},
		{"audit", []string{"audit"}, []string{"audit", "--ci"}},
		{"targets", []string{"targets"}, []string{"targets", "--json"}},
		{"prune", []string{"prune"}, []string{"prune"}},
		{"deps list", []string{"deps", "list"}, []string{"deps", "list", "-g"}},
		{"deps why", []string{"deps", "why", "owner/pkg"}, []string{"deps", "why", "-g", "owner/pkg"}},
		{"deps info", []string{"deps", "info", "owner/pkg"}, []string{"view", "--global", "owner/pkg"}},
		{"marketplace add", []string{"marketplace", "add", "owner/catalog", "--name", "catalog", "--ref", "v1"}, []string{"marketplace", "add", "owner/catalog", "--name", "catalog", "--ref", "v1"}},
		{"marketplace list", []string{"marketplace", "list"}, []string{"marketplace", "list"}},
		{"marketplace browse", []string{"marketplace", "browse", "catalog"}, []string{"marketplace", "browse", "catalog"}},
		{"marketplace update", []string{"marketplace", "update", "catalog"}, []string{"marketplace", "update", "catalog"}},
		{"marketplace validate", []string{"marketplace", "validate", "catalog"}, []string{"marketplace", "validate", "catalog"}},
		{"marketplace remove confirms", []string{"marketplace", "remove", "catalog"}, []string{"marketplace", "remove", "catalog", "--yes"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			writeGlobalAPMManifest(t, home)
			responses := []executor.MockCall{{Stdout: "APM CLI version " + cliTestPinnedAPMVersion + "\n"}}
			responses = append(responses, executor.MockCall{Stdout: "stdout\n", Stderr: "stderr\n"})
			mock := &executor.MockExecutor{Responses: responses}
			a := app.New(filepath.Join(home, "settings.json"))
			a.SetFallbackExecutor(mock)
			cmd := newAgentsCmd(&rootState{app: a})
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			cmd.SetArgs(tt.args)

			if err := cmd.ExecuteContext(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(mock.Calls) != 2 || !reflect.DeepEqual(apmCallArgs(mock.Calls[0]), []string{"--version"}) || !reflect.DeepEqual(apmCallArgs(mock.Calls[1]), tt.want) {
				t.Fatalf("calls = %#v, want apm %v", mock.Calls, tt.want)
			}
			// `add` appends a host-template hint after the passthrough.
			if stdout.String() != "stdout\n" || !strings.HasPrefix(stderr.String(), "stderr\n") {
				t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
		})
	}
}

func TestAgentsCommandPreservesAPMFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mock := &executor.MockExecutor{Responses: []executor.MockCall{
		{Stdout: "APM CLI version " + cliTestPinnedAPMVersion + "\n"},
		{Stdout: "partial output\n", Stderr: "bad package\n", Err: errors.New("exit status 2")},
	}}
	a := app.New(filepath.Join(home, "settings.json"))
	a.SetFallbackExecutor(mock)
	cmd := newAgentsCmd(&rootState{app: a})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"add", "broken/pkg"})

	err := cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "bad package") {
		t.Fatalf("error = %v, want APM failure detail", err)
	}
	if !strings.Contains(stdout.String(), "partial output\n") || !strings.Contains(stderr.String(), "bad package\n") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func apmCallArgs(call executor.MockCall) []string {
	if call.Name == "apm" {
		return call.Args
	}
	for i, arg := range call.Args {
		if arg == "apm" {
			return call.Args[i+1:]
		}
	}
	return nil
}

func TestAgentsLegacyCommandsAndBootstrapSkillImportAreGone(t *testing.T) {
	agents := newAgentsCmd(&rootState{})
	for _, name := range []string{"skills", "plugins", "mcp", "import", "find", "restore"} {
		if findCommand(agents, []string{name}) != nil {
			t.Errorf("legacy command %q is still registered", name)
		}
	}
	bootstrap := newBootstrapCmd(&rootState{})
	for _, name := range []string{"import-skills", "no-import-skills"} {
		if bootstrap.Flags().Lookup(name) != nil {
			t.Errorf("legacy --%s flag is still registered", name)
		}
	}
}

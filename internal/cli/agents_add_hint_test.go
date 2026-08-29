package cli

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/executor"
)

func runAgentsAdd(t *testing.T, spec string, addResponse executor.MockCall) (string, string, error) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	writeGlobalAPMManifest(t, home)
	mock := &executor.MockExecutor{Responses: []executor.MockCall{
		{Stdout: "APM CLI version " + cliTestPinnedAPMVersion + "\n"},
		addResponse,
	}}
	a := app.New(filepath.Join(home, "settings.json"))
	a.SetFallbackExecutor(mock)
	cmd := newAgentsCmd(&rootState{app: a})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"add", spec})
	err := cmd.ExecuteContext(context.Background())
	return stdout.String(), stderr.String(), err
}

func TestAgentsAddHintsAtTheHostTemplate(t *testing.T) {
	_, stderr, err := runAgentsAdd(t, "owner/pkg", executor.MockCall{Stdout: "installed\n"})
	if err != nil {
		t.Fatal(err)
	}
	template, err := app.AgentsTemplatePath()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{template, "- git: owner/pkg", "path:", "targets:"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("hint %q missing from stderr:\n%s", want, stderr)
		}
	}
}

func TestAgentsAddHintCarriesAnExplicitRef(t *testing.T) {
	_, stderr, err := runAgentsAdd(t, "https://github.com/owner/pkg.git@v1.2.3", executor.MockCall{Stdout: "installed\n"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "- git: owner/pkg") || !strings.Contains(stderr, "ref: v1.2.3") {
		t.Fatalf("hint did not normalise the package spec:\n%s", stderr)
	}
}

func TestAgentsAddSkipsHintWhenInstallFails(t *testing.T) {
	_, stderr, err := runAgentsAdd(t, "broken/pkg", executor.MockCall{Stderr: "boom\n", Err: errors.New("exit status 2")})
	if err == nil {
		t.Fatal("expected the APM failure to propagate")
	}
	if strings.Contains(stderr, "- git:") {
		t.Fatalf("hint printed after a failed add:\n%s", stderr)
	}
}

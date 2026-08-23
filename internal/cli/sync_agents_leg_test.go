package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/executor"
)

func TestSyncAllAPMLegRunsOneGlobalInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".apm"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".apm", "apm.yml"), []byte("name: test\nversion: 1.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mock := &executor.MockExecutor{Responses: []executor.MockCall{
		{Stdout: "APM CLI version " + cliTestPinnedAPMVersion + "\n"},
		{Stdout: "installed\n"},
	}}
	a := app.New(filepath.Join(home, "settings.json"))
	a.SetFallbackExecutor(mock)
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := runSyncAllAPMLeg(cmd, &rootState{app: a}, true); err != nil {
		t.Fatal(err)
	}
	want := []string{"install", "-g", "--dry-run"}
	if len(mock.Calls) != 2 || !reflect.DeepEqual(apmCallArgs(mock.Calls[0]), []string{"--version"}) || !reflect.DeepEqual(apmCallArgs(mock.Calls[1]), want) {
		t.Fatalf("calls = %#v, want one apm %v", mock.Calls, want)
	}
}

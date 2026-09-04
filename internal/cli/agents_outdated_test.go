package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/executor"
)

func newOutdatedCLI(t *testing.T, home string, responses ...executor.MockCall) (*executor.MockExecutor, *bytes.Buffer, *bytes.Buffer, func() error) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	writeGlobalAPMManifest(t, home)
	mock := &executor.MockExecutor{Responses: responses}
	a := app.New(filepath.Join(home, "settings.json"))
	a.SetFallbackExecutor(mock)
	cmd := newAgentsCmd(&rootState{app: a})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"outdated"})
	return mock, &stdout, &stderr, func() error { return cmd.ExecuteContext(context.Background()) }
}

func TestAgentsOutdatedWithoutLockfileIsNeutral(t *testing.T) {
	home := t.TempDir()
	mock, stdout, stderr, run := newOutdatedCLI(t, home)

	if err := run(); err != nil {
		t.Fatalf("outdated = %v, want nil", err)
	}
	want := "nothing managed yet: no lockfile in " + filepath.Join(home, ".apm") + "\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	if stderr.String() != "" {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("calls = %#v, want no APM invocation", mock.Calls)
	}
}

func TestAgentsOutdatedWithLockfileDelegatesToAPM(t *testing.T) {
	home := t.TempDir()
	mock, stdout, stderr, run := newOutdatedCLI(t, home,
		executor.MockCall{Stdout: "APM CLI version " + cliTestPinnedAPMVersion + "\n"},
		executor.MockCall{Stdout: "stdout\n", Stderr: "stderr\n"},
	)
	if err := os.WriteFile(filepath.Join(home, ".apm", "apm.lock.yaml"), []byte("packages: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := run(); err != nil {
		t.Fatalf("outdated = %v, want nil", err)
	}
	want := []string{"outdated", "-g", "--parallel-checks", "4"}
	if len(mock.Calls) != 2 || !reflect.DeepEqual(apmCallArgs(mock.Calls[1]), want) {
		t.Fatalf("calls = %#v, want apm %v", mock.Calls, want)
	}
	if stdout.String() != "stdout\n" || !strings.Contains(stderr.String(), "stderr\n") {
		t.Fatalf("stdout = %q stderr = %q", stdout.String(), stderr.String())
	}
}

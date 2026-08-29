package cli

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func TestSettingsResetRejectsUnexpectedArgsWithoutMutation(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	cacheDir := t.TempDir()
	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{ProviderPriority: []string{"npm", "brew"}},
		Groups:   []*config.GroupConfig{cliTestHostGroup()},
	})
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	_, err = runRootCommand(t, "--yes", "--config", cfgPath, "--cache-dir", cacheDir, "settings", "reset", "junk")
	if err == nil {
		t.Fatal("settings reset with an unexpected argument succeeded")
	}
	after, readErr := os.ReadFile(cfgPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("settings changed after rejected argument\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestConsolidateFailuresReturnErrorAfterDetails(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	errInstall := errors.New("install failed")
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Version: config.CurrentVersion,
		Tools: map[string]config.ToolSpec{
			"prettier": {Providers: []config.ToolInstallSpec{{Provider: "npm", Package: "prettier"}}},
		},
		Groups: []*config.GroupConfig{cliTestHostGroup("prettier")},
	})
	a, err := buildTestApp(t, cfgPath,
		&cliStubProvider{name: "brew", installErr: errInstall},
		&cliStubProvider{name: "npm"},
	)
	if err != nil {
		t.Fatal(err)
	}
	cmd := newConsolidateCmd(&rootState{app: a})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--to", "brew"})

	err = cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "1 tool failed") {
		t.Fatalf("error = %v, want 1 tool failed; output:\n%s", err, out.String())
	}
	if output := out.String(); !strings.Contains(output, "prettier") || !strings.Contains(output, errInstall.Error()) {
		t.Fatalf("failure details missing from output:\n%s", output)
	}
}

func TestEcosystemConsolidateFailureLinesReturnError(t *testing.T) {
	errInstall := errors.New("install failed")
	result := &app.ConsolidateResult{Failed: []app.ConsolidateFailure{{
		ConsolidateTool: app.ConsolidateTool{Name: "black", FromProvider: "python"},
		Err:             errInstall,
	}}}
	var out bytes.Buffer

	err := printConsolidateLines(&out, result, "uv")
	if err == nil || !strings.Contains(err.Error(), "1 tool failed") {
		t.Fatalf("error = %v, want 1 tool failed", err)
	}
	if output := out.String(); !strings.Contains(output, "black") || !strings.Contains(output, errInstall.Error()) {
		t.Fatalf("failure details missing from output:\n%s", output)
	}
}

func TestSyncFailuresReturnErrorAfterSummary(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	tests := []struct {
		name       string
		provider   *cliStubProvider
		args       []string
		wantError  string
		wantOutput string
	}{
		{
			name:       "normal install failure",
			provider:   &cliStubProvider{name: "brew", installErr: errors.New("install failed")},
			wantError:  "1 tool failed",
			wantOutput: "install failed",
		},
		{
			name:       "dry-run unavailable provider",
			provider:   &cliStubProvider{name: "brew", unavailable: true},
			args:       []string{"--dry-run"},
			wantError:  "1 tool unavailable",
			wantOutput: "provider unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfgPath := filepath.Join(t.TempDir(), "settings.json")
			withConfig(t, cfgPath, &config.RootConfig{
				Tools: map[string]config.ToolSpec{
					"fd": {Providers: []config.ToolInstallSpec{{Provider: "brew", Package: "fd"}}},
				},
				Groups: []*config.GroupConfig{cliTestHostGroup("fd")},
			})
			a, err := buildTestApp(t, cfgPath, tt.provider)
			if err != nil {
				t.Fatal(err)
			}
			cmd := newSyncCmd(&rootState{app: a})
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetArgs(tt.args)

			err = cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %v, want %q; output:\n%s", err, tt.wantError, out.String())
			}
			if !strings.Contains(out.String(), tt.wantOutput) {
				t.Fatalf("output missing %q:\n%s", tt.wantOutput, out.String())
			}
		})
	}
}

func TestExecuteSIGTERMExitsNonZero(t *testing.T) {
	const helperEnv = "OMNI_TEST_HELPER_EXECUTE_SIGNAL"
	if os.Getenv(helperEnv) == "1" {
		cfgPath := os.Getenv("OMNI_CONFIG")
		withConfig(t, cfgPath, &config.RootConfig{})
		t.Setenv("OMNI_HOSTNAME", "testhost")
		os.Args = []string{
			"omni",
			"--config", cfgPath,
			"--cache-dir", os.Getenv("OMNI_CACHE_DIR"),
			"settings", "reset",
		}
		Execute()
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signal exit codes are not available on Windows")
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestExecuteSIGTERMExitsNonZero$") //nolint:gosec
	cmd.Env = append(os.Environ(),
		helperEnv+"=1",
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }()

	ready := make(chan struct{}, 1)
	output := make(chan string, 1)
	go func() {
		reader := bufio.NewReader(stdout)
		var buf strings.Builder
		seenPrompt := false
		for {
			b, readErr := reader.ReadByte()
			if readErr != nil {
				output <- buf.String()
				return
			}
			buf.WriteByte(b)
			if !seenPrompt && strings.Contains(buf.String(), "Reset settings to defaults?") {
				seenPrompt = true
				ready <- struct{}{}
			}
		}
	}()

	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("helper did not reach blocking prompt; stderr: %s", stderr.String())
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	select {
	case err = <-wait:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		err = <-wait
		t.Fatalf("helper did not exit after SIGTERM: %v", err)
	}
	stdoutText := <-output
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("SIGTERM exit error = %v, want non-zero process exit; stdout: %s; stderr: %s", err, stdoutText, stderr.String())
	}
	if got, want := exitErr.ExitCode(), 128+int(syscall.SIGTERM); got != want {
		t.Fatalf("SIGTERM exit code = %d, want %d", got, want)
	}
}

func TestNoArgumentSettingsLeavesRejectPositionals(t *testing.T) {
	state := &rootState{}
	for _, cmd := range []*cobra.Command{
		newSettingsLintCmd(state),
		newSettingsMigrateHostOverridesCmd(state),
		newSettingsResetCmd(state),
		newSettingsResetCacheCmd(state),
		newSettingsExtractCmd(state),
	} {
		t.Run(cmd.Name(), func(t *testing.T) {
			if cmd.Args == nil {
				t.Fatalf("%s has no positional arguments but no Cobra Args validator", cmd.CommandPath())
			}
			if err := cmd.Args(cmd, []string{"junk"}); err == nil {
				t.Fatalf("%s accepted an unexpected positional argument", cmd.CommandPath())
			}
		})
	}
}

func TestNoArgumentMutatingLeavesRejectPositionals(t *testing.T) {
	root := NewRootCmd()
	for _, path := range discoverMutatingCLICommands(root) {
		cmd := findCommand(root, path)
		if cmd == nil || cmd.Use != cmd.Name() {
			continue
		}
		t.Run(strings.Join(path, "_"), func(t *testing.T) {
			if cmd.Args == nil {
				t.Fatalf("%s has no positional arguments but no Cobra Args validator", cmd.CommandPath())
			}
			if err := cmd.Args(cmd, []string{"junk"}); err == nil {
				t.Fatalf("%s accepted an unexpected positional argument", cmd.CommandPath())
			}
		})
	}
}

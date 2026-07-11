package aptrepo_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/provider/aptrepo"
	_ "github.com/lkshrk/omni/internal/testguard"
)

type recordingExec struct {
	calls []string
}

func (e *recordingExec) Run(_ context.Context, name string, args ...string) (string, string, error) {
	e.calls = append(e.calls, strings.Join(append([]string{name}, args...), " "))
	if name == "apt-get" && len(args) > 0 && args[0] == "--version" {
		return "apt 2.0", "", nil
	}
	return "", "", nil
}

func TestProviderInstallRunsSetupThenPackages(t *testing.T) {
	exec := &recordingExec{}
	p := aptrepo.New(exec)
	tool := provider.Tool{
		Name:     "docker",
		Provider: "apt_repo",
		Options: map[string]string{
			"setup":    "echo setup",
			"packages": "docker-ce, docker-ce-cli",
		},
	}
	if err := p.Install(context.Background(), tool); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(exec.calls) != 2 {
		t.Fatalf("calls = %v, want setup + apt-get install", exec.calls)
	}
	if exec.calls[0] != "sh -c echo setup" {
		t.Fatalf("first call = %q, want setup", exec.calls[0])
	}
	if exec.calls[1] != "apt-get install -y docker-ce docker-ce-cli" {
		t.Fatalf("install call = %q", exec.calls[1])
	}
}

func TestProviderIsInstalledUsesCheckOption(t *testing.T) {
	exec := &recordingExec{}
	p := aptrepo.New(exec)
	installed, _, err := p.IsInstalled(context.Background(), provider.Tool{
		Name:     "docker",
		Provider: "apt_repo",
		Options:  map[string]string{"check": "command -v docker"},
	})
	if err != nil {
		t.Fatalf("IsInstalled: %v", err)
	}
	if !installed {
		t.Fatal("expected installed via check option")
	}
	if exec.calls[0] != "sh -c command -v docker" {
		t.Fatalf("check call = %q", exec.calls[0])
	}
}

var _ executor.Executor = (*recordingExec)(nil)

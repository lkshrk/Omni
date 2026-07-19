package aptrepo_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/provider/aptrepo"
	_ "github.com/lkshrk/omni/internal/testguard"
)

type recordingExec struct {
	calls []string
	// fail maps an exact joined command string to the error it should return.
	fail map[string]error
	// stdoutFor maps an exact joined command string to the stdout it should return.
	stdoutFor map[string]string
}

func (e *recordingExec) Run(_ context.Context, name string, args ...string) (string, string, error) {
	call := strings.Join(append([]string{name}, args...), " ")
	e.calls = append(e.calls, call)
	if err, ok := e.fail[call]; ok {
		return "", "boom", err
	}
	if name == "apt-get" && len(args) > 0 && args[0] == "--version" {
		return "apt 2.0", "", nil
	}
	if stdout, ok := e.stdoutFor[call]; ok {
		return stdout, "", nil
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

func TestProviderInstallMissingSetupOption(t *testing.T) {
	exec := &recordingExec{}
	p := aptrepo.New(exec)
	tool := provider.Tool{Name: "docker", Options: map[string]string{"packages": "docker-ce"}}
	err := p.Install(context.Background(), tool)
	if err == nil || !strings.Contains(err.Error(), `missing "setup" option`) {
		t.Fatalf("err = %v, want missing setup option", err)
	}
}

func TestProviderInstallMissingPackagesOption(t *testing.T) {
	exec := &recordingExec{}
	p := aptrepo.New(exec)
	tool := provider.Tool{Name: "docker", Options: map[string]string{"setup": "echo setup"}}
	err := p.Install(context.Background(), tool)
	if err == nil || !strings.Contains(err.Error(), `missing "packages" option`) {
		t.Fatalf("err = %v, want missing packages option", err)
	}
	if len(exec.calls) != 1 {
		t.Fatalf("calls = %v, want only the setup call before failing", exec.calls)
	}
}

func TestProviderInstallWrapsSetupFailureWithStderr(t *testing.T) {
	exec := &recordingExec{fail: map[string]error{"sh -c echo setup": errors.New("exit status 1")}}
	p := aptrepo.New(exec)
	tool := provider.Tool{
		Name:    "docker",
		Options: map[string]string{"setup": "echo setup", "packages": "docker-ce"},
	}
	err := p.Install(context.Background(), tool)
	if err == nil || !strings.Contains(err.Error(), "exit status 1") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want wrapped exit status and stderr", err)
	}
}

func TestProviderUninstallUsesPackageOrToolPackage(t *testing.T) {
	exec := &recordingExec{}
	p := aptrepo.New(exec)
	tool := provider.Tool{Name: "docker", Package: "docker-ce docker-ce-cli"}
	if err := p.Uninstall(context.Background(), tool); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if len(exec.calls) != 1 || exec.calls[0] != "apt-get remove -y docker-ce docker-ce-cli" {
		t.Fatalf("calls = %v, want apt-get remove", exec.calls)
	}
}

func TestProviderUninstallMissingPackages(t *testing.T) {
	exec := &recordingExec{}
	p := aptrepo.New(exec)
	err := p.Uninstall(context.Background(), provider.Tool{Name: "docker"})
	if err == nil || !strings.Contains(err.Error(), `missing "packages" option`) {
		t.Fatalf("err = %v, want missing packages option", err)
	}
	if len(exec.calls) != 0 {
		t.Fatalf("calls = %v, want no exec calls", exec.calls)
	}
}

func TestProviderUpgradeWithExplicitCommandSkipsSetup(t *testing.T) {
	exec := &recordingExec{}
	p := aptrepo.New(exec)
	tool := provider.Tool{
		Name:    "docker",
		Options: map[string]string{"setup": "echo setup", "packages": "docker-ce", "upgrade": "apt-get upgrade docker-ce"},
	}
	if err := p.Upgrade(context.Background(), tool); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if len(exec.calls) != 1 || exec.calls[0] != "sh -c apt-get upgrade docker-ce" {
		t.Fatalf("calls = %v, want only the explicit upgrade command, no setup", exec.calls)
	}
}

func TestProviderUpgradeWithoutExplicitCommandRunsSetupThenInstallUpgrade(t *testing.T) {
	exec := &recordingExec{}
	p := aptrepo.New(exec)
	tool := provider.Tool{
		Name:    "docker",
		Options: map[string]string{"setup": "echo setup", "packages": "docker-ce"},
	}
	if err := p.Upgrade(context.Background(), tool); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if len(exec.calls) != 2 {
		t.Fatalf("calls = %v, want setup + apt-get upgrade", exec.calls)
	}
	if exec.calls[1] != "apt-get install --only-upgrade -y docker-ce" {
		t.Fatalf("upgrade call = %q", exec.calls[1])
	}
}

func TestProviderUpgradeWithoutExplicitCommandMissingSetup(t *testing.T) {
	exec := &recordingExec{}
	p := aptrepo.New(exec)
	tool := provider.Tool{Name: "docker", Options: map[string]string{"packages": "docker-ce"}}
	err := p.Upgrade(context.Background(), tool)
	if err == nil || !strings.Contains(err.Error(), `missing "setup" option`) {
		t.Fatalf("err = %v, want missing setup option", err)
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

func TestProviderIsInstalledCheckOptionFailureMeansNotInstalled(t *testing.T) {
	exec := &recordingExec{fail: map[string]error{"sh -c command -v docker": errors.New("not found")}}
	p := aptrepo.New(exec)
	installed, _, err := p.IsInstalled(context.Background(), provider.Tool{
		Options: map[string]string{"check": "command -v docker"},
	})
	if err != nil {
		t.Fatalf("IsInstalled: %v", err)
	}
	if installed {
		t.Fatal("expected not installed when check command fails")
	}
}

func TestProviderIsInstalledViaDpkgQueryAllPackagesPresent(t *testing.T) {
	exec := &recordingExec{stdoutFor: map[string]string{
		"dpkg-query --showformat=${Version} --show docker-ce":     "5:24.0.0-1",
		"dpkg-query --showformat=${Version} --show docker-ce-cli": "5:24.0.0-1",
	}}
	p := aptrepo.New(exec)
	installed, version, err := p.IsInstalled(context.Background(), provider.Tool{
		Package: "docker-ce docker-ce-cli",
	})
	if err != nil {
		t.Fatalf("IsInstalled: %v", err)
	}
	if !installed {
		t.Fatal("expected installed when dpkg-query reports all packages present")
	}
	if version != "" {
		t.Fatalf("version = %q, want empty (apt_repo does not report a version)", version)
	}
}

func TestProviderIsInstalledViaDpkgQueryMissingPackage(t *testing.T) {
	exec := &recordingExec{stdoutFor: map[string]string{
		"dpkg-query --showformat=${Version} --show docker-ce": "5:24.0.0-1",
	}}
	p := aptrepo.New(exec)
	installed, _, err := p.IsInstalled(context.Background(), provider.Tool{
		Package: "docker-ce docker-ce-cli",
	})
	if err != nil {
		t.Fatalf("IsInstalled: %v", err)
	}
	if installed {
		t.Fatal("expected not installed when a package is missing from dpkg-query")
	}
}

func TestProviderIsInstalledNoPackagesOrCheck(t *testing.T) {
	exec := &recordingExec{}
	p := aptrepo.New(exec)
	installed, _, err := p.IsInstalled(context.Background(), provider.Tool{Name: "docker"})
	if err != nil {
		t.Fatalf("IsInstalled: %v", err)
	}
	if installed {
		t.Fatal("expected not installed when neither check nor packages is set")
	}
	if len(exec.calls) != 0 {
		t.Fatalf("calls = %v, want no exec calls", exec.calls)
	}
}

func TestProviderAvailable(t *testing.T) {
	exec := &recordingExec{}
	p := aptrepo.New(exec)
	available, err := p.Available(context.Background())
	if err != nil {
		t.Fatalf("Available: %v", err)
	}
	if !available {
		t.Fatal("expected available when apt-get --version succeeds")
	}
}

func TestProviderAvailableFalseWhenAptGetMissing(t *testing.T) {
	exec := &recordingExec{fail: map[string]error{"apt-get --version": errors.New("not found")}}
	p := aptrepo.New(exec)
	available, err := p.Available(context.Background())
	if err != nil {
		t.Fatalf("Available: %v", err)
	}
	if available {
		t.Fatal("expected not available when apt-get --version fails")
	}
}

func TestProviderListInstalledReturnsNil(t *testing.T) {
	p := aptrepo.New(&recordingExec{})
	got, err := p.ListInstalled(context.Background())
	if err != nil || got != nil {
		t.Fatalf("ListInstalled = %v, %v, want nil, nil", got, err)
	}
}

func TestProviderNameAndDescription(t *testing.T) {
	p := aptrepo.New(&recordingExec{})
	if p.Name() != "apt_repo" {
		t.Fatalf("Name = %q", p.Name())
	}
	if p.Description() == "" {
		t.Fatal("Description must not be empty")
	}
}

var _ executor.Executor = (*recordingExec)(nil)

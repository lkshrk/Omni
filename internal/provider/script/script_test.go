package script

import (
	"context"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	_ "github.com/lkshrk/omni/internal/testguard"
)

func tool(name string, opts map[string]string) provider.Tool {
	return provider.Tool{Name: name, Provider: "script", Package: name, Options: opts}
}

func TestName(t *testing.T) {
	p := New(executor.NewMatchMock().WithFallback(executor.MockCall{}))
	if p.Name() != "script" {
		t.Errorf("Name() = %q, want script", p.Name())
	}
}

func TestAvailable_ShellPresent(t *testing.T) {
	mock := executor.NewMatchMock(executor.MatchRule{
		Pattern:  "sh -c exit 0",
		Response: executor.MockCall{},
	}).WithFallback(executor.MockCall{})
	p := New(mock)
	ok, err := p.Available(context.Background())
	if err != nil || !ok {
		t.Errorf("Available() = %v, %v; want true, nil", ok, err)
	}
}

func TestAvailable_ShellAbsent(t *testing.T) {
	mock := executor.NewMatchMock(executor.MatchRule{
		Pattern:  "sh -c exit 0",
		Response: executor.MockCall{Err: context.DeadlineExceeded},
	}).WithFallback(executor.MockCall{})
	p := New(mock)
	ok, _ := p.Available(context.Background())
	if ok {
		t.Error("Available() = true; want false when shell probe errors")
	}
}

func TestInstall_RunsCommand(t *testing.T) {
	mock := executor.NewMatchMock(executor.MatchRule{
		Pattern:  "sh -c curl -fsSL https://bun.sh/install | bash",
		Response: executor.MockCall{},
	}).WithFallback(executor.MockCall{})
	p := New(mock)
	err := p.Install(context.Background(), tool("bun", map[string]string{
		"install": "curl -fsSL https://bun.sh/install | bash",
	}))
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	mock.AssertCalled(t, "sh -c curl -fsSL https://bun.sh/install | bash")
}

func TestInstall_MissingInstallOption(t *testing.T) {
	p := New(executor.NewMatchMock().WithFallback(executor.MockCall{}))
	err := p.Install(context.Background(), tool("bun", map[string]string{}))
	if err == nil {
		t.Fatal("Install() with no install option: want error, got nil")
	}
}

func TestInstall_PropagatesStderr(t *testing.T) {
	mock := executor.NewMatchMock(executor.MatchRule{
		Pattern:  "sh -c false",
		Response: executor.MockCall{Stderr: "boom", Err: context.DeadlineExceeded},
	}).WithFallback(executor.MockCall{})
	p := New(mock)
	err := p.Install(context.Background(), tool("x", map[string]string{"install": "false"}))
	if err == nil {
		t.Fatal("Install() failing command: want error, got nil")
	}
}

func TestIsInstalled_CheckExitZero(t *testing.T) {
	mock := executor.NewMatchMock(executor.MatchRule{
		Pattern:  "sh -c test -x /root/.bun/bin/bun",
		Response: executor.MockCall{},
	}).WithFallback(executor.MockCall{})
	p := New(mock)
	ok, _, err := p.IsInstalled(context.Background(), tool("bun", map[string]string{
		"check": "test -x /root/.bun/bin/bun",
	}))
	if err != nil || !ok {
		t.Errorf("IsInstalled() = %v, %v; want true, nil", ok, err)
	}
}

func TestIsInstalled_CheckNonZero(t *testing.T) {
	mock := executor.NewMatchMock(executor.MatchRule{
		Pattern:  "sh -c test -x /nope",
		Response: executor.MockCall{Err: context.DeadlineExceeded},
	}).WithFallback(executor.MockCall{})
	p := New(mock)
	ok, _, _ := p.IsInstalled(context.Background(), tool("bun", map[string]string{"check": "test -x /nope"}))
	if ok {
		t.Error("IsInstalled() = true; want false on non-zero check")
	}
}

func TestIsInstalled_DetectFound(t *testing.T) {
	mock := executor.NewMatchMock(executor.MatchRule{
		Pattern:  `sh -c command -v "$1" -- bun`,
		Response: executor.MockCall{Stdout: "/root/.bun/bin/bun"},
	}).WithFallback(executor.MockCall{})
	p := New(mock)
	ok, _, err := p.IsInstalled(context.Background(), tool("bun", map[string]string{"detect": "bun"}))
	if err != nil || !ok {
		t.Errorf("IsInstalled() = %v, %v; want true, nil", ok, err)
	}
}

func TestIsInstalled_DetectMissing(t *testing.T) {
	mock := executor.NewMatchMock(executor.MatchRule{
		Pattern:  `sh -c command -v "$1" -- bun`,
		Response: executor.MockCall{Err: context.DeadlineExceeded},
	}).WithFallback(executor.MockCall{})
	p := New(mock)
	ok, _, _ := p.IsInstalled(context.Background(), tool("bun", map[string]string{"detect": "bun"}))
	if ok {
		t.Error("IsInstalled() = true; want false when command -v fails")
	}
}

func TestIsInstalled_DetectInjectionPayloadDoesNotExecute(t *testing.T) {
	// A malicious "detect" value with shell metacharacters must not be
	// interpolated into the shell string. The argv approach passes detect as
	// $1, so the shell never evaluates it as code. The mock verifies that
	// the exact args we expect were passed (payload as a literal argv token,
	// not as part of the -c script).
	payload := "x; touch PWNED"
	mock := executor.NewMatchMock().WithFallback(executor.MockCall{Err: context.DeadlineExceeded})
	p := New(mock)
	ok, _, _ := p.IsInstalled(context.Background(), tool("evil", map[string]string{"detect": payload}))
	if ok {
		t.Error("IsInstalled() = true; want false for unknown binary")
	}
	// The call must carry the payload as a separate argv element, not embedded
	// in the shell script string.
	calls := mock.CallsMatching(`sh -c command -v "$1" --`)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call with argv pattern, got %d", len(calls))
	}
	args := calls[0].Args
	// args: ["-c", `command -v "$1"`, "--", payload]
	if len(args) < 4 || args[3] != payload {
		t.Errorf("detect payload not passed as separate argv arg; args = %q", args)
	}
	// Confirm the -c script string itself does NOT contain the payload.
	if strings.Contains(args[1], payload) {
		t.Errorf("detect payload was interpolated into the shell script string: %q", args[1])
	}
}

func TestIsInstalled_NeitherSet(t *testing.T) {
	p := New(executor.NewMatchMock().WithFallback(executor.MockCall{}))
	ok, _, err := p.IsInstalled(context.Background(), tool("bun", map[string]string{}))
	if err != nil {
		t.Fatalf("IsInstalled() error = %v", err)
	}
	if ok {
		t.Error("IsInstalled() = true; want false when neither check nor detect set")
	}
}

func TestUninstall_RunsWhenSet(t *testing.T) {
	mock := executor.NewMatchMock(executor.MatchRule{
		Pattern:  "sh -c rm -rf /root/.bun",
		Response: executor.MockCall{},
	}).WithFallback(executor.MockCall{})
	p := New(mock)
	if err := p.Uninstall(context.Background(), tool("bun", map[string]string{"uninstall": "rm -rf /root/.bun"})); err != nil {
		t.Fatalf("Uninstall() error = %v", err)
	}
	mock.AssertCalled(t, "sh -c rm -rf /root/.bun")
}

func TestUninstall_NoopWhenUnset(t *testing.T) {
	mock := executor.NewMatchMock().WithFallback(executor.MockCall{})
	p := New(mock)
	if err := p.Uninstall(context.Background(), tool("bun", map[string]string{})); err != nil {
		t.Fatalf("Uninstall() error = %v", err)
	}
	if mock.CallCount() != 0 {
		t.Errorf("Uninstall() ran %d commands; want 0", mock.CallCount())
	}
}

func TestUpgrade_RunsUpgradeWhenSet(t *testing.T) {
	mock := executor.NewMatchMock(executor.MatchRule{
		Pattern:  "sh -c bun upgrade",
		Response: executor.MockCall{},
	}).WithFallback(executor.MockCall{})
	p := New(mock)
	if err := p.Upgrade(context.Background(), tool("bun", map[string]string{"upgrade": "bun upgrade"})); err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	mock.AssertCalled(t, "sh -c bun upgrade")
}

func TestUpgrade_FallsBackToInstall(t *testing.T) {
	mock := executor.NewMatchMock(executor.MatchRule{
		Pattern:  "sh -c install-cmd",
		Response: executor.MockCall{},
	}).WithFallback(executor.MockCall{})
	p := New(mock)
	if err := p.Upgrade(context.Background(), tool("bun", map[string]string{"install": "install-cmd"})); err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	mock.AssertCalled(t, "sh -c install-cmd")
}

func TestListInstalled_Nil(t *testing.T) {
	p := New(executor.NewMatchMock().WithFallback(executor.MockCall{}))
	got, err := p.ListInstalled(context.Background())
	if err != nil || got != nil {
		t.Errorf("ListInstalled() = %v, %v; want nil, nil", got, err)
	}
}

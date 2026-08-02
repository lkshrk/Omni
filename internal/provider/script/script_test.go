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
	if !strings.Contains(err.Error(), "stderr: boom") {
		t.Fatalf("Install() error = %q; want stderr", err)
	}
}

func TestInstall_UsesStdoutWhenStderrIsEmpty(t *testing.T) {
	mock := executor.NewMatchMock(executor.MatchRule{
		Pattern:  "sh -c false",
		Response: executor.MockCall{Stdout: "download failed\n", Err: context.DeadlineExceeded},
	}).WithFallback(executor.MockCall{})
	p := New(mock)
	err := p.Install(context.Background(), tool("x", map[string]string{"install": "false"}))
	if err == nil {
		t.Fatal("Install() failing command: want error, got nil")
	}
	if !strings.Contains(err.Error(), "stdout: download failed") {
		t.Fatalf("Install() error = %q; want stdout fallback", err)
	}
	if strings.Contains(err.Error(), "stderr: )") {
		t.Fatalf("Install() error contains empty stderr: %q", err)
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

func TestIsInstalled_CheckReturnsVersion(t *testing.T) {
	mock := executor.NewMatchMock(
		executor.MatchRule{
			Pattern:  "sh -c test -x /root/.bun/bin/bun",
			Response: executor.MockCall{},
		},
		executor.MatchRule{
			Pattern:  "sh -c bun --version",
			Response: executor.MockCall{Stdout: "  1.2.3\n"},
		},
	).WithFallback(executor.MockCall{})
	p := New(mock)
	ok, version, err := p.IsInstalled(context.Background(), tool("bun", map[string]string{
		"check":   "test -x /root/.bun/bin/bun",
		"version": "bun --version",
	}))
	if err != nil {
		t.Fatalf("IsInstalled() error = %v", err)
	}
	if !ok || version != "1.2.3" {
		t.Errorf("IsInstalled() = %v, %q; want true, %q", ok, version, "1.2.3")
	}
}

func TestIsInstalled_RecordedVersionWithoutVersionCommand(t *testing.T) {
	mock := executor.NewMatchMock().WithFallback(executor.MockCall{})
	p := New(mock)
	ok, version, err := p.IsInstalled(context.Background(), tool("actionlint", map[string]string{
		"check":            "test -x /root/.local/bin/actionlint",
		"recorded_version": "1.7.12",
	}))
	if err != nil {
		t.Fatalf("IsInstalled() error = %v", err)
	}
	if !ok || version != "1.7.12" {
		t.Errorf("IsInstalled() = %v, %q; want true, %q", ok, version, "1.7.12")
	}
	if len(mock.Calls) != 1 {
		t.Errorf("recorded version must not shell out: calls = %v", mock.Calls)
	}
}

func TestIsInstalled_VersionCommandOutranksRecordedVersion(t *testing.T) {
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "sh -c bun-check", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "sh -c bun --version", Response: executor.MockCall{Stdout: "1.2.3\n"}},
	).WithFallback(executor.MockCall{})
	p := New(mock)
	_, version, err := p.IsInstalled(context.Background(), tool("bun", map[string]string{
		"check":            "bun-check",
		"version":          "bun --version",
		"recorded_version": "1.0.0",
	}))
	if err != nil {
		t.Fatalf("IsInstalled() error = %v", err)
	}
	if version != "1.2.3" {
		t.Errorf("version = %q; want %q", version, "1.2.3")
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
	// detect must reach the shell as $1, never interpolated into the -c script where it would be evaluated.
	payload := "x; touch PWNED"
	mock := executor.NewMatchMock().WithFallback(executor.MockCall{Err: context.DeadlineExceeded})
	p := New(mock)
	ok, _, _ := p.IsInstalled(context.Background(), tool("evil", map[string]string{"detect": payload}))
	if ok {
		t.Error("IsInstalled() = true; want false for unknown binary")
	}
	// The payload must be a separate argv element, not part of the script string.
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

func TestLatestCommandMarksDifferentVersionOutdated(t *testing.T) {
	mock := executor.NewMatchMock(executor.MatchRule{
		Pattern:  "sh -c curl -fsSL https://example.com/latest",
		Response: executor.MockCall{Stdout: "1.3.0\n"},
	}).WithFallback(executor.MockCall{})
	p := New(mock)
	checker, ok := any(p).(provider.ToolOutdatedChecker)
	if !ok {
		t.Fatal("script provider does not implement provider.ToolOutdatedChecker")
	}
	latest, outdated, supported, err := checker.CheckOutdated(context.Background(), tool("bun", map[string]string{
		"latest": "curl -fsSL https://example.com/latest",
	}), "1.2.3")
	if err != nil {
		t.Fatalf("CheckOutdated() error = %v", err)
	}
	if !supported || !outdated || latest != "1.3.0" {
		t.Errorf("CheckOutdated() = %q, %v, %v; want %q, true, true", latest, outdated, supported, "1.3.0")
	}
}

func TestLatestCommandRejectsEmptyOutput(t *testing.T) {
	mock := executor.NewMatchMock(executor.MatchRule{
		Pattern:  "sh -c bun-latest",
		Response: executor.MockCall{Stdout: " \n"},
	}).WithFallback(executor.MockCall{})
	checker := any(New(mock)).(provider.ToolOutdatedChecker)
	_, _, supported, err := checker.CheckOutdated(context.Background(), tool("bun", map[string]string{
		"latest": "bun-latest",
	}), "1.2.3")
	if !supported || err == nil {
		t.Fatalf("CheckOutdated() supported=%v error=%v; want true and non-nil error", supported, err)
	}
}

func TestLatestCommandNormalizesVPrefix(t *testing.T) {
	mock := executor.NewMatchMock(executor.MatchRule{
		Pattern:  "sh -c bun-latest",
		Response: executor.MockCall{Stdout: "v1.2.3\n"},
	}).WithFallback(executor.MockCall{})
	checker := any(New(mock)).(provider.ToolOutdatedChecker)
	latest, outdated, supported, err := checker.CheckOutdated(context.Background(), tool("bun", map[string]string{
		"latest": "bun-latest",
	}), "1.2.3")
	if err != nil || !supported || outdated || latest != "v1.2.3" {
		t.Fatalf("CheckOutdated() = %q, %v, %v, %v; want v1.2.3, false, true, nil", latest, outdated, supported, err)
	}
}

func TestLatestCommandDoesNotMarkCurrentAheadOutdated(t *testing.T) {
	mock := executor.NewMatchMock(executor.MatchRule{
		Pattern:  "sh -c bun-latest",
		Response: executor.MockCall{Stdout: "1.2.3\n"},
	}).WithFallback(executor.MockCall{})
	checker := any(New(mock)).(provider.ToolOutdatedChecker)
	_, outdated, supported, err := checker.CheckOutdated(context.Background(), tool("bun", map[string]string{
		"latest": "bun-latest",
	}), "1.3.0")
	if err != nil || !supported || outdated {
		t.Fatalf("CheckOutdated() outdated=%v supported=%v error=%v; want false, true, nil", outdated, supported, err)
	}
}

func TestLatestCommandRejectsIncomparableVersions(t *testing.T) {
	mock := executor.NewMatchMock(executor.MatchRule{
		Pattern:  "sh -c bun-latest",
		Response: executor.MockCall{Stdout: "nightly-b\n"},
	}).WithFallback(executor.MockCall{})
	checker := any(New(mock)).(provider.ToolOutdatedChecker)
	_, _, supported, err := checker.CheckOutdated(context.Background(), tool("bun", map[string]string{
		"latest": "bun-latest",
	}), "nightly-a")
	if !supported || err == nil {
		t.Fatalf("CheckOutdated() supported=%v error=%v; want true and non-nil error", supported, err)
	}
}

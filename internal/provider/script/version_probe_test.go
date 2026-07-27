package script

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
)

const probePattern = `sh -c "$1" --version </dev/null`

func probeMock(response executor.MockCall) *executor.MatchMockExecutor {
	return executor.NewMatchMock(executor.MatchRule{Pattern: probePattern, Response: response}).
		WithFallback(executor.MockCall{})
}

func TestIsInstalled_ProbesTheBinaryWhenNothingElseReportsAVersion(t *testing.T) {
	mock := probeMock(executor.MockCall{Stdout: "just 1.57.0\n"})
	p := New(mock)

	installed, ver, err := p.IsInstalled(context.Background(), tool("just", map[string]string{
		"check": "test -x /usr/bin/just",
		"bin":   "just",
	}))

	if err != nil || !installed {
		t.Fatalf("IsInstalled() = %v, %q, %v; want true", installed, ver, err)
	}
	if ver != "1.57.0" {
		t.Fatalf("version = %q, want %q", ver, "1.57.0")
	}
}

func TestIsInstalled_ProbeFallsBackToTheToolName(t *testing.T) {
	mock := probeMock(executor.MockCall{Stdout: "2.37.1\n"})
	p := New(mock)

	_, ver, _ := p.IsInstalled(context.Background(), tool("direnv", map[string]string{
		"check": "test -x /usr/bin/direnv",
	}))

	if ver != "2.37.1" {
		t.Fatalf("version = %q, want %q", ver, "2.37.1")
	}
	calls := mock.CallsMatching("sh")
	if len(calls) == 0 {
		t.Fatal("expected a probe call")
	}
	last := calls[len(calls)-1]
	if last.Args[len(last.Args)-1] != "direnv" {
		t.Fatalf("probe binary = %q, want the tool name", last.Args[len(last.Args)-1])
	}
}

func TestIsInstalled_ConfiguredVersionCommandOutranksTheProbe(t *testing.T) {
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "sh -c myversion", Response: executor.MockCall{Stdout: "9.9.9\n"}},
		executor.MatchRule{Pattern: probePattern, Response: executor.MockCall{Stdout: "1.0.0\n"}},
	).WithFallback(executor.MockCall{})
	p := New(mock)

	_, ver, _ := p.IsInstalled(context.Background(), tool("thing", map[string]string{
		"check":   "test -x /usr/bin/thing",
		"version": "myversion",
		"bin":     "thing",
	}))

	if ver != "9.9.9" {
		t.Fatalf("version = %q, want the configured command's answer", ver)
	}
}

func TestIsInstalled_RecordedVersionOutranksTheProbe(t *testing.T) {
	mock := probeMock(executor.MockCall{Stdout: "1.0.0\n"})
	p := New(mock)

	_, ver, _ := p.IsInstalled(context.Background(), tool("thing", map[string]string{
		"check":            "test -x /usr/bin/thing",
		"recorded_version": "3.2.1",
		"bin":              "thing",
	}))

	if ver != "3.2.1" {
		t.Fatalf("version = %q, want the recorded version", ver)
	}
}

func TestIsInstalled_ProbeFailureLeavesTheToolInstalledWithNoVersion(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response executor.MockCall
	}{
		{"binary rejects --version", executor.MockCall{Err: errors.New("exit status 2")}},
		{"usage screen carries no version", executor.MockCall{Stdout: "flag provided but not defined: -version\nUsage: gopls\n"}},
		{"probe times out", executor.MockCall{Err: context.DeadlineExceeded}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := New(probeMock(tc.response))
			installed, ver, err := p.IsInstalled(context.Background(), tool("gopls", map[string]string{
				"check": "test -x /usr/bin/gopls",
				"bin":   "gopls",
			}))
			if err != nil {
				t.Fatalf("IsInstalled() error = %v; a failed probe must not fail the check", err)
			}
			if !installed {
				t.Fatal("installed = false; a failed probe must not unmark an installed tool")
			}
			if ver != "" {
				t.Fatalf("version = %q, want empty", ver)
			}
		})
	}
}

// The probe must accept an absolute recipe check_path, not only a bare binary name.
func TestIsInstalled_ProbesAPathShapedBinary(t *testing.T) {
	mock := probeMock(executor.MockCall{Stdout: "claude 2.1.220 (Claude Code)\n"})
	p := New(mock)

	_, ver, _ := p.IsInstalled(context.Background(), tool("claude-code", map[string]string{
		"check": "test -x /home/u/.local/bin/claude",
		"bin":   "/home/u/.local/bin/claude",
	}))

	if ver != "2.1.220" {
		t.Fatalf("version = %q, want %q", ver, "2.1.220")
	}
	calls := mock.CallsMatching("sh")
	last := calls[len(calls)-1]
	if last.Args[len(last.Args)-1] != "/home/u/.local/bin/claude" {
		t.Fatalf("probe binary = %q, want the full path", last.Args[len(last.Args)-1])
	}
}

// A tool that reads stdin must not hold the refresh open until the probe times out.
func TestIsInstalled_ProbeClosesStdin(t *testing.T) {
	mock := probeMock(executor.MockCall{Stdout: "1.0.0\n"})
	p := New(mock)

	p.IsInstalled(context.Background(), tool("widget", map[string]string{ //nolint:errcheck // asserting the command shape
		"check": "test -x /usr/bin/widget",
		"bin":   "widget",
	}))

	calls := mock.CallsMatching("sh")
	last := calls[len(calls)-1]
	joined := strings.Join(last.Args, " ")
	if !strings.Contains(joined, "</dev/null") {
		t.Fatalf("probe command = %q, want stdin closed", joined)
	}
}

// java -version is the canonical shape: exit 0, banner written exclusively to stderr.
func TestIsInstalled_ProbeReadsStderrWhenStdoutIsSilent(t *testing.T) {
	mock := probeMock(executor.MockCall{Stderr: "openjdk version \"21.0.4\" 2024-07-16\n"})
	p := New(mock)

	_, ver, _ := p.IsInstalled(context.Background(), tool("java", map[string]string{
		"check": "test -x /usr/bin/java",
		"bin":   "java",
	}))

	if ver != "21.0.4" {
		t.Fatalf("version = %q, want %q", ver, "21.0.4")
	}
}

func TestIsInstalled_ProbePrefersStdoutOverStderr(t *testing.T) {
	mock := probeMock(executor.MockCall{Stdout: "tool 3.0.0\n", Stderr: "warning: rebuilt against tool 1.0.0\n"})
	p := New(mock)

	_, ver, _ := p.IsInstalled(context.Background(), tool("tool", map[string]string{
		"check": "test -x /usr/bin/tool",
		"bin":   "tool",
	}))

	if ver != "3.0.0" {
		t.Fatalf("version = %q, want stdout's answer", ver)
	}
}

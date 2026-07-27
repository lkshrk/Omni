package executor_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
)

func TestMockExecutor_RecordsCallsAndReturnsResponses(t *testing.T) {
	wantErr := errors.New("fail")
	m := &executor.MockExecutor{
		Responses: []executor.MockCall{
			{Stdout: "hello", Stderr: "", Err: nil},
			{Stdout: "world", Stderr: "oops", Err: wantErr},
		},
	}

	stdout, stderr, err := m.Run(context.Background(), "echo", "hello")
	if stdout != "hello" || stderr != "" || err != nil {
		t.Errorf("first call: got (%q, %q, %v), want (hello, , nil)", stdout, stderr, err)
	}

	stdout, stderr, err = m.Run(context.Background(), "fail", "now")
	if stdout != "world" || stderr != "oops" || !errors.Is(err, wantErr) {
		t.Errorf("second call: got (%q, %q, %v)", stdout, stderr, err)
	}

	if len(m.Calls) != 2 {
		t.Fatalf("Calls = %d, want 2", len(m.Calls))
	}
	if m.Calls[0].Name != "echo" {
		t.Errorf("Calls[0].Name = %q, want echo", m.Calls[0].Name)
	}
	if m.Calls[1].Name != "fail" {
		t.Errorf("Calls[1].Name = %q, want fail", m.Calls[1].Name)
	}
}

func TestMockExecutor_ReturnsEmptyAfterResponsesExhausted(t *testing.T) {
	m := &executor.MockExecutor{}

	stdout, stderr, err := m.Run(context.Background(), "any")
	if stdout != "" || stderr != "" || err != nil {
		t.Errorf("expected empty response after exhausted, got (%q, %q, %v)", stdout, stderr, err)
	}
}

func TestMockExecutor_Reset(t *testing.T) {
	m := &executor.MockExecutor{
		Responses: []executor.MockCall{{Stdout: "first"}},
	}
	m.Run(context.Background(), "cmd")

	m.Reset()
	if len(m.Calls) != 0 {
		t.Errorf("Calls after Reset = %d, want 0", len(m.Calls))
	}
	m.Responses = []executor.MockCall{{Stdout: "second"}}
	stdout, _, _ := m.Run(context.Background(), "cmd2")
	if stdout != "second" {
		t.Errorf("after Reset got %q, want second", stdout)
	}
}

func TestMatchMock_MatchesByPrefix(t *testing.T) {
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "brew install", Response: executor.MockCall{Stdout: "installed"}},
		executor.MatchRule{Pattern: "brew list", Response: executor.MockCall{Stdout: "listed"}},
	)

	stdout, _, _ := m.Run(context.Background(), "brew", "install", "ripgrep")
	if stdout != "installed" {
		t.Errorf("brew install: got %q, want installed", stdout)
	}

	stdout, _, _ = m.Run(context.Background(), "brew", "list", "--versions")
	if stdout != "listed" {
		t.Errorf("brew list: got %q, want listed", stdout)
	}
}

func TestMatchMock_FallbackWhenNoMatch(t *testing.T) {
	m := executor.NewMatchMock().WithFallback(executor.MockCall{Stdout: "fallback"})

	stdout, _, _ := m.Run(context.Background(), "unknown", "cmd")
	if stdout != "fallback" {
		t.Errorf("got %q, want fallback", stdout)
	}
}

func TestMatchMock_CallsMatching(t *testing.T) {
	m := executor.NewMatchMock()
	m.Run(context.Background(), "brew", "install", "git")
	m.Run(context.Background(), "brew", "install", "ripgrep")
	m.Run(context.Background(), "brew", "list")

	calls := m.CallsMatching("brew install")
	if len(calls) != 2 {
		t.Errorf("CallsMatching brew install = %d, want 2", len(calls))
	}
}

func TestMatchMock_CallCount(t *testing.T) {
	m := executor.NewMatchMock()
	m.Run(context.Background(), "a")
	m.Run(context.Background(), "b")

	if m.CallCount() != 2 {
		t.Errorf("CallCount = %d, want 2", m.CallCount())
	}
}

func TestMatchMock_AssertCalled(t *testing.T) {
	m := executor.NewMatchMock()
	m.Run(context.Background(), "brew", "install", "git")

	m.AssertCalled(t, "brew install")
}

func TestMatchMock_MustHaveCalledN(t *testing.T) {
	m := executor.NewMatchMock()
	m.Run(context.Background(), "brew", "install", "git")
	m.Run(context.Background(), "brew", "install", "ripgrep")

	m.MustHaveCalledN(t, "brew install", 2)
}

func TestMatchMock_AssertCalled_FailsWhenNotCalled(t *testing.T) {
	m := executor.NewMatchMock()
	ft := &fakeT{}
	m.AssertCalled(ft, "brew install")
	if len(ft.errs) != 1 {
		t.Errorf("expected 1 error, got %d", len(ft.errs))
	}
}

func TestMatchMock_MustHaveCalledN_FailsOnWrongCount(t *testing.T) {
	m := executor.NewMatchMock()
	m.Run(context.Background(), "brew", "install", "git")

	ft := &fakeT{}
	m.MustHaveCalledN(ft, "brew install", 2)
	if len(ft.errs) != 1 {
		t.Errorf("expected 1 error, got %d", len(ft.errs))
	}
}

// fakeT records Errorf calls without failing the actual test.
type fakeT struct{ errs []string }

func (f *fakeT) Errorf(format string, args ...any) {
	f.errs = append(f.errs, fmt.Sprintf(format, args...))
}

func TestMatchMock_AddRule(t *testing.T) {
	m := executor.NewMatchMock()
	m.AddRule(executor.MatchRule{Pattern: "npm install", Response: executor.MockCall{Stdout: "npm done"}})

	stdout, _, _ := m.Run(context.Background(), "npm", "install", "typescript")
	if stdout != "npm done" {
		t.Errorf("got %q, want npm done", stdout)
	}
}

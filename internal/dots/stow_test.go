package dots_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/lkshrk/omni/internal/dots"
	"github.com/lkshrk/omni/internal/executor"
)

// ─── CheckInstalled ───────────────────────────────────────────────────────────

func TestCheckInstalled_True(t *testing.T) {
	mock := &executor.MockExecutor{} // no error = stow found
	if !dots.CheckInstalled(context.Background(), mock) {
		t.Error("expected CheckInstalled == true when stow exits 0")
	}
	if len(mock.Calls) != 1 || mock.Calls[0].Name != "stow" {
		t.Errorf("expected one stow call, got %v", mock.Calls)
	}
}

func TestCheckInstalled_False(t *testing.T) {
	mock := &executor.MockExecutor{
		Responses: []executor.MockCall{{Err: errors.New("exit 1")}},
	}
	if dots.CheckInstalled(context.Background(), mock) {
		t.Error("expected CheckInstalled == false when stow exits non-zero")
	}
}

// ─── Restow ───────────────────────────────────────────────────────────────────

func TestRestow_CallsStow(t *testing.T) {
	home, _ := os.UserHomeDir()
	mock := &executor.MockExecutor{}
	if err := dots.Restow(context.Background(), mock, "/repo", []string{"nvim", "zsh"}, false); err != nil {
		t.Fatalf("Restow: %v", err)
	}
	if len(mock.Calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(mock.Calls))
	}
	got := mock.Calls[0].Args
	want := []string{"-R", "-d", "/repo", "-t", home, "nvim", "zsh"}
	assertArgs(t, got, want)
}

func TestRestow_DryRun_AddsSimulate(t *testing.T) {
	home, _ := os.UserHomeDir()
	mock := &executor.MockExecutor{}
	if err := dots.Restow(context.Background(), mock, "/repo", []string{"nvim"}, true); err != nil {
		t.Fatalf("Restow dry-run: %v", err)
	}
	want := []string{"-R", "--simulate", "-d", "/repo", "-t", home, "nvim"}
	assertArgs(t, mock.Calls[0].Args, want)
}

func TestRestow_EmptyPackages_IsNoop(t *testing.T) {
	mock := &executor.MockExecutor{}
	if err := dots.Restow(context.Background(), mock, "/repo", nil, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.Calls) != 0 {
		t.Errorf("expected no calls for empty packages, got %d", len(mock.Calls))
	}
}

func TestRestow_PropagatesError(t *testing.T) {
	mock := &executor.MockExecutor{
		Responses: []executor.MockCall{{Err: errors.New("exit 1"), Stderr: "conflict"}},
	}
	err := dots.Restow(context.Background(), mock, "/repo", []string{"nvim"}, false)
	if err == nil {
		t.Fatal("expected error")
	}
}

// ─── Unstow ───────────────────────────────────────────────────────────────────

func TestUnstow_CallsStow(t *testing.T) {
	home, _ := os.UserHomeDir()
	mock := &executor.MockExecutor{}
	if err := dots.Unstow(context.Background(), mock, "/repo", []string{"nvim"}); err != nil {
		t.Fatalf("Unstow: %v", err)
	}
	want := []string{"-D", "-d", "/repo", "-t", home, "nvim"}
	assertArgs(t, mock.Calls[0].Args, want)
}

func TestUnstow_EmptyPackages_IsNoop(t *testing.T) {
	mock := &executor.MockExecutor{}
	if err := dots.Unstow(context.Background(), mock, "/repo", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.Calls) != 0 {
		t.Errorf("expected no calls, got %d", len(mock.Calls))
	}
}

// ─── Adopt ────────────────────────────────────────────────────────────────────

func TestAdopt_CallsStow(t *testing.T) {
	home, _ := os.UserHomeDir()
	mock := &executor.MockExecutor{}
	if err := dots.Adopt(context.Background(), mock, "/repo", "nvim", false); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	want := []string{"--adopt", "-d", "/repo", "-t", home, "nvim"}
	assertArgs(t, mock.Calls[0].Args, want)
}

func TestAdopt_DryRun_AddsSimulate(t *testing.T) {
	home, _ := os.UserHomeDir()
	mock := &executor.MockExecutor{}
	if err := dots.Adopt(context.Background(), mock, "/repo", "nvim", true); err != nil {
		t.Fatalf("Adopt dry-run: %v", err)
	}
	want := []string{"--adopt", "--simulate", "-d", "/repo", "-t", home, "nvim"}
	assertArgs(t, mock.Calls[0].Args, want)
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func assertArgs(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("args length: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("arg[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

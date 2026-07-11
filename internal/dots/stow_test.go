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
	assertStowArgs(t, got, "-R", "/repo", home, false, []string{"nvim", "zsh"})
}

func TestRestow_DryRun_AddsSimulate(t *testing.T) {
	home, _ := os.UserHomeDir()
	mock := &executor.MockExecutor{}
	if err := dots.Restow(context.Background(), mock, "/repo", []string{"nvim"}, true); err != nil {
		t.Fatalf("Restow dry-run: %v", err)
	}
	assertStowArgs(t, mock.Calls[0].Args, "-R", "/repo", home, true, []string{"nvim"})
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

// ─── helpers ──────────────────────────────────────────────────────────────────

func assertStowArgs(t *testing.T, got []string, mode, repo, home string, dryRun bool, packages []string) {
	t.Helper()
	if len(got) == 0 {
		t.Fatal("empty stow args")
	}
	if got[0] != mode {
		t.Fatalf("mode arg = %q, want %q in %v", got[0], mode, got)
	}
	for _, want := range []string{"--no-folding", "--ignore=(?:^|/)\\.DS_Store$", "--ignore=(?:^|/)[^/]*\\.log$"} {
		if !containsArg(got, want) {
			t.Fatalf("args = %v, missing %q", got, want)
		}
	}
	if containsArg(got, "--simulate") != dryRun {
		t.Fatalf("args = %v, simulate presence = %v, want %v", got, containsArg(got, "--simulate"), dryRun)
	}
	wantTail := append([]string{"-d", repo, "-t", home}, packages...)
	if len(got) < len(wantTail) {
		t.Fatalf("args = %v, too short for tail %v", got, wantTail)
	}
	gotTail := got[len(got)-len(wantTail):]
	for i := range wantTail {
		if gotTail[i] != wantTail[i] {
			t.Fatalf("tail arg[%d]: got %q, want %q in args %v", i, gotTail[i], wantTail[i], got)
		}
	}
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

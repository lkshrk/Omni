//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/app"
)

func TestDotsAddIgnorePatternWithStateRefreshesOnce(t *testing.T) {
	a, _, _ := newDotsPerformanceApp(t)
	before := countDotsGitStatusTraces(t, a)
	ctx := app.WithShallowDotsChildren(context.Background())

	if _, err := a.DotsAddIgnorePatternWithState(ctx, "claude", "settings.json"); err != nil {
		t.Fatalf("add ignore pattern with state: %v", err)
	}

	if got := countDotsGitStatusTraces(t, a) - before; got != 1 {
		t.Fatalf("git status calls during ignore = %d, want exactly one state refresh", got)
	}
}

func TestDotsRootFileIgnoreLatency(t *testing.T) {
	a, home, _ := newDotsPerformanceApp(t)
	cache := filepath.Join(home, ".claude", "cache")
	for i := range 3_000 {
		path := filepath.Join(cache, fmt.Sprintf("entry-%05d", i), "data.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	start := time.Now()
	ctx := app.WithShallowDotsChildren(context.Background())
	if _, err := a.DotsAddIgnorePatternWithState(ctx, "claude", "settings.json"); err != nil {
		t.Fatalf("add root-file ignore: %v", err)
	}
	elapsed := time.Since(start)
	t.Logf("root-file ignore with 3,000 unrelated files: %s", elapsed)
	if elapsed > 600*time.Millisecond {
		t.Fatalf("root-file ignore took %s, want <= 600ms", elapsed)
	}
}

func newDotsPerformanceApp(t *testing.T) (*app.App, string, string) {
	t.Helper()
	a, home, repo := newNestedIncludeApp(t, nil)
	writeIntegrationFile(t, filepath.Join(repo, "dotfiles", "claude", ".claude", "settings.json"), "{}\n")
	initDotsRepo(t, repo, os.Environ())
	runCommand(t, repo, os.Environ(), "git", "add", ".")
	runCommand(t, repo, os.Environ(), "git", "commit", "-m", "fixture")
	if _, err := a.DotsSync(app.DotSyncOptions{}); err != nil {
		t.Fatalf("initial dots sync: %v", err)
	}
	return a, home, repo
}

func countDotsGitStatusTraces(t *testing.T, a *app.App) int {
	t.Helper()
	traces, err := a.CommandTraces(context.Background(), 10_000)
	if err != nil {
		t.Fatalf("command traces: %v", err)
	}
	count := 0
	for _, trace := range traces {
		if strings.Contains(trace.Command, "git") && strings.Contains(trace.Command, "status --short") {
			count++
		}
	}
	return count
}

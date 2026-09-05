package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPortableHookPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hook path anchoring is POSIX-only")
	}
	home := t.TempDir()
	settings := filepath.Join(home, ".claude", "settings.json")
	ledger := filepath.Join(home, ".claude", "apm-hooks.json")
	hooks := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"node \"` + home + `/.claude/hooks/caveman/activate.js\""}]}]},"env":{"KEEP":"` + home + `/.claude/other","HOOKDIR":"` + home + `/.claude/hooks/"},"permissions":{"additionalDirectories":["` + home + `/.claude/hooks/x"]}}`
	writeFile(t, settings, hooks)
	writeFile(t, ledger, `{"SessionStart":[{"hooks":[{"command":"node \"`+home+`/.claude/hooks/caveman/activate.js\""}]}]}`)
	if err := os.Chmod(settings, 0o600); err != nil {
		t.Fatal(err)
	}

	changed, err := portableHookPaths(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 2 {
		t.Fatalf("changed = %v, want both files", changed)
	}
	for _, path := range []string{settings, ledger} {
		raw, _ := os.ReadFile(path)
		if !strings.Contains(string(raw), `"command":"node \"$HOME/.claude/hooks/caveman/activate.js\""`) {
			t.Fatalf("%s = %s", path, raw)
		}
	}
	raw, _ := os.ReadFile(settings)
	for _, literal := range []string{`"KEEP":"` + home + `/.claude/other"`, `"HOOKDIR":"` + home + `/.claude/hooks/"`, `["` + home + `/.claude/hooks/x"]`} {
		if !strings.Contains(string(raw), literal) {
			t.Fatalf("non-command value rewritten, missing %s in %s", literal, raw)
		}
	}
	if info, _ := os.Stat(settings); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}

	changed, err = portableHookPaths(home)
	if err != nil || len(changed) != 0 {
		t.Fatalf("second pass changed = %v, %v; want no-op", changed, err)
	}
}

func TestPortableHookPathsWritesThroughSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hook path anchoring is POSIX-only")
	}
	home := t.TempDir()
	repo := filepath.Join(home, "dotfiles", "settings.json")
	writeFile(t, repo, `{"hooks":{"Stop":[{"hooks":[{"command":"`+home+`/.claude/hooks/x/stop.mjs"}]}]}}`)
	link := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	if _, err := portableHookPaths(home); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink replaced: %v, %v", info, err)
	}
	if raw, _ := os.ReadFile(repo); !strings.Contains(string(raw), `$HOME/.claude/hooks/x/stop.mjs`) {
		t.Fatalf("repo file = %s", raw)
	}
}

func TestPortableHookPathsTrailingSlashHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hook path anchoring is POSIX-only")
	}
	home := t.TempDir()
	settings := filepath.Join(home, ".claude", "settings.json")
	writeFile(t, settings, `{"hooks":{"Stop":[{"hooks":[{"command":"`+home+`/.claude/hooks/x/stop.mjs"}]}]}}`)
	if changed, err := portableHookPaths(home + "/"); err != nil || len(changed) != 1 {
		t.Fatalf("changed = %v, %v", changed, err)
	}
	if raw, _ := os.ReadFile(settings); !strings.Contains(string(raw), `$HOME/.claude/hooks/x/stop.mjs`) {
		t.Fatalf("settings = %s", raw)
	}
}

func TestPortableHookPathsSkipsMissingFiles(t *testing.T) {
	changed, err := portableHookPaths(t.TempDir())
	if err != nil || len(changed) != 0 {
		t.Fatalf("changed = %v, %v", changed, err)
	}
}

func TestAPMMutatesHooks(t *testing.T) {
	for _, args := range [][]string{{"install", "-g"}, {"update"}, {"uninstall", "-g", "x"}, {"prune"}} {
		if !apmMutatesHooks(args) {
			t.Errorf("%v should mutate hooks", args)
		}
	}
	for _, args := range [][]string{{}, {"audit"}, {"outdated"}, {"marketplace", "add"}, {"search", "x"}} {
		if apmMutatesHooks(args) {
			t.Errorf("%v should not mutate hooks", args)
		}
	}
}

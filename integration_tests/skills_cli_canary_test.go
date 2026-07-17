//go:build integration

// Canary against the REAL skills CLI, part of the normal integration suite.
// omni invokes the CLI via npx unpinned, so upstream releases change behavior
// under us: the failure markers in internal/app.skillsCLIFailureMarkers were
// verified against vercel-labs/skills source (2026-07), and this test detects
// wording drift in whatever version npx currently resolves. Skips (rather
// than fails) when npx or the npm registry is unavailable — absence of a
// Node toolchain or network is an environment gap, not marker drift.
package integration_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/app"
)

const skillsCLICanarySkillName = "omni-live-canary"

type skillsCLIListCanaryEntry struct {
	Name   string   `json:"name"`
	Path   string   `json:"path"`
	Scope  string   `json:"scope"`
	Agents []string `json:"agents"`
}

func requireSkillsCLINpx(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx not on PATH; canary needs a real Node toolchain")
	}
}

func writeSkillsCLICanarySource(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	content := "---\nname: " + skillsCLICanarySkillName + "\ndescription: Isolated integration canary for Omni.\n---\n\n# Canary\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func skillsCLICanaryEnv(t *testing.T, home, state string) []string {
	t.Helper()
	return append(os.Environ(),
		"HOME="+home,
		"XDG_STATE_HOME="+state,
		"CLAUDE_CONFIG_DIR="+filepath.Join(home, ".claude"),
		"CODEX_HOME="+filepath.Join(home, ".codex"),
		"npm_config_cache="+t.TempDir(),
		"CI=1",
		"DISABLE_TELEMETRY=1",
	)
}

func probeSkillsCLI(t *testing.T, env []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "npx", "-y", "skills", "--version")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("skills CLI unavailable through npx; cannot run live contract canary: %v\noutput:\n%s", err, out)
	}
}

func installSkillsCLICanary(t *testing.T, home string, agents ...string) []string {
	t.Helper()
	for _, agent := range agents {
		var dir string
		switch agent {
		case "claude-code":
			dir = filepath.Join(home, ".claude")
		case "codex":
			dir = filepath.Join(home, ".codex")
		}
		if dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	env := skillsCLICanaryEnv(t, home, t.TempDir())
	probeSkillsCLI(t, env)
	args := []string{"-y", "skills", "add", writeSkillsCLICanarySource(t), "-g"}
	for _, agent := range agents {
		args = append(args, "-a", agent)
	}
	args = append(args, "-y")
	cmd := exec.CommandContext(ctx, "npx", args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	output := string(out)
	if err != nil {
		if ctx.Err() != nil {
			t.Fatalf("real skills CLI local add timed out: %v\noutput:\n%s", ctx.Err(), output)
		}
		t.Fatalf("real skills CLI failed to add isolated local skill: %v\noutput:\n%s", err, output)
	}
	if app.SkillsCLIOutputIndicatesFailure(output, "") {
		t.Fatalf("real skills CLI exited 0 but reported local add failure.\noutput:\n%s", output)
	}
	return cmd.Env
}

func listSkillsCLICanary(t *testing.T, env []string) []skillsCLIListCanaryEntry {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "npx", "-y", "skills", "list", "-g", "--json")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("real skills CLI list failed after confirmed add: %v\noutput:\n%s", err, out)
	}
	var entries []skillsCLIListCanaryEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		t.Fatalf("real skills CLI list no longer emits clean JSON: %v\noutput:\n%s", err, out)
	}
	return entries
}

func skillsCLIListHasAgent(entries []skillsCLIListCanaryEntry, skillName, agentDisplay string) bool {
	for _, entry := range entries {
		if entry.Name != skillName || entry.Scope != "global" {
			continue
		}
		for _, agent := range entry.Agents {
			if agent == agentDisplay {
				return true
			}
		}
	}
	return false
}

func TestSkillsCLICanary_LocalAddAppearsForEveryTargetAgentInGlobalList(t *testing.T) {
	requireSkillsCLINpx(t)
	home := t.TempDir()
	env := installSkillsCLICanary(t, home, "claude-code", "codex")
	entries := listSkillsCLICanary(t, env)
	for _, display := range []string{"Claude Code", "Codex"} {
		if !skillsCLIListHasAgent(entries, skillsCLICanarySkillName, display) {
			t.Fatalf("real skills CLI list omitted %q for %s: %+v", display, skillsCLICanarySkillName, entries)
		}
	}
	for _, entry := range entries {
		if entry.Name == skillsCLICanarySkillName && entry.Scope == "global" {
			if _, err := os.Stat(entry.Path); err != nil {
				t.Fatalf("listed canonical skill path is not materialized: %s: %v", entry.Path, err)
			}
			return
		}
	}
	t.Fatalf("real skills CLI list omitted global %s record: %+v", skillsCLICanarySkillName, entries)
}

func TestSkillsCLICanary_BrokenAgentSymlinkIsNotReportedInstalled(t *testing.T) {
	requireSkillsCLINpx(t)
	home := t.TempDir()
	env := installSkillsCLICanary(t, home, "claude-code", "codex")
	before := listSkillsCLICanary(t, env)
	for _, display := range []string{"Claude Code", "Codex"} {
		if !skillsCLIListHasAgent(before, skillsCLICanarySkillName, display) {
			t.Fatalf("precondition failed: installed canary is not listed for %s: %+v", display, before)
		}
	}

	link := filepath.Join(home, ".claude", "skills", skillsCLICanarySkillName)
	if err := os.RemoveAll(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "missing-canary-target"), link); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("broken symlink was not created: %v", err)
	}
	if _, err := os.Stat(link); !os.IsNotExist(err) {
		t.Fatalf("test symlink is not broken; Stat error = %v", err)
	}

	entries := listSkillsCLICanary(t, env)
	if skillsCLIListHasAgent(entries, skillsCLICanarySkillName, "Claude Code") {
		t.Fatalf("real skills CLI reported a broken Claude Code symlink as installed: %+v", entries)
	}
	if !skillsCLIListHasAgent(entries, skillsCLICanarySkillName, "Codex") {
		t.Fatalf("real skills CLI lost the intact Codex installation after breaking only Claude Code: %+v", entries)
	}
}

// TestSkillsCLICanary_FailureOutputStillMatchesMarkers drives the current
// published skills CLI into a failure (nonexistent repo) and asserts the
// marker detection in internal/app still recognizes its output. If this
// fails, upstream reworded its failure text — update
// skillsCLIFailureMarkers and the txtar fixtures' fake output together.
func TestSkillsCLICanary_FailureOutputStillMatchesMarkers(t *testing.T) {
	requireSkillsCLINpx(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Isolated HOME so the real CLI cannot touch the developer's agent
	// configs or global lockfile; bogus repo forces the failure path.
	cmd := exec.CommandContext(ctx, "npx", "-y", "skills", "add",
		"omni-canary-nonexistent-owner/omni-canary-nonexistent-repo", "-g", "-y")
	cmd.Env = append(cmd.Environ(),
		"HOME="+t.TempDir(),
		"XDG_STATE_HOME="+t.TempDir(),
		"CI=1",
	)
	out, err := cmd.CombinedOutput()
	output := string(out)

	marked := app.SkillsCLIOutputIndicatesFailure(output, "")

	// Generic markers ("Failed to") could occur in pre-CLI noise (npm/npx
	// breakage), so they do not prove the skills CLI ran. Require one of the
	// CLI's own add-failure signatures (src/add.ts: the "Installation
	// failed" outro and the GitCloneError header) before asserting anything;
	// the bogus repo legitimately exits 1 through exactly that path.
	cliRan := strings.Contains(output, "Installation failed") ||
		strings.Contains(output, "Failed to clone repository")

	switch {
	case err == nil && !marked:
		// The one dangerous contract: failure reported as exit 0 with no
		// known marker — omni would treat this install as a success.
		t.Fatalf("real skills CLI exited 0 for a nonexistent repo AND printed no known failure marker (%q) — omni would report success; update skillsCLIFailureMarkers.\noutput:\n%s",
			[]string{"Failed to", "✗", "✘"}, output)
	case !cliRan:
		// npx exited non-zero without any skills-CLI signature: a pre-CLI
		// environment failure (registry outage, node breakage). Nothing
		// about the CLI was observed — skip rather than pass vacuously.
		t.Skipf("skills CLI run not confirmed (no CLI output signature); cannot assert markers.\noutput:\n%s", output)
	case !marked:
		// The CLI demonstrably ran and failed, but no marker matched its
		// output — upstream reworded failure text. Production still catches
		// this run via the non-zero exit, but the exit-0 partial-failure
		// paths rely on these markers, so drift must be fixed, not logged.
		t.Fatalf("skills CLI failure output no longer matches any marker (%q) — update skillsCLIFailureMarkers and the txtar fakes.\noutput:\n%s",
			[]string{"Failed to", "✗", "✘"}, output)
	}
}

package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─── resolveNvmAlias ─────────────────────────────────────────────────────────

func TestResolveNvmAlias_ConcreteVersion(t *testing.T) {
	nvmDir := t.TempDir()
	aliasDir := filepath.Join(nvmDir, "alias")
	if err := os.MkdirAll(aliasDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(aliasDir, "default"), []byte("v22.16.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := resolveNvmAlias(nvmDir, "default", 8)
	if got != "v22.16.0" {
		t.Errorf("got %q, want v22.16.0", got)
	}
}

func TestResolveNvmAlias_FollowsChain(t *testing.T) {
	// default → lts/* → lts/iron → v20.10.0
	nvmDir := t.TempDir()
	aliasDir := filepath.Join(nvmDir, "alias")
	ltsDir := filepath.Join(aliasDir, "lts")
	if err := os.MkdirAll(ltsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAlias := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeAlias(filepath.Join(aliasDir, "default"), "lts/*")
	writeAlias(filepath.Join(ltsDir, "*"), "lts/iron")
	writeAlias(filepath.Join(ltsDir, "iron"), "v20.10.0")

	got := resolveNvmAlias(nvmDir, "default", 8)
	if got != "v20.10.0" {
		t.Errorf("got %q, want v20.10.0", got)
	}
}

func TestResolveNvmAlias_MissingFile(t *testing.T) {
	nvmDir := t.TempDir()
	got := resolveNvmAlias(nvmDir, "default", 8)
	if got != "" {
		t.Errorf("missing alias file: got %q, want empty", got)
	}
}

func TestResolveNvmAlias_HopsExceeded(t *testing.T) {
	nvmDir := t.TempDir()
	aliasDir := filepath.Join(nvmDir, "alias")
	os.MkdirAll(aliasDir, 0o755) //nolint:errcheck
	// Write a circular non-version alias that never resolves.
	os.WriteFile(filepath.Join(aliasDir, "default"), []byte("loop"), 0o644) //nolint:errcheck
	os.WriteFile(filepath.Join(aliasDir, "loop"), []byte("default"), 0o644) //nolint:errcheck

	got := resolveNvmAlias(nvmDir, "default", 4)
	if got != "" {
		t.Errorf("circular alias: got %q, want empty", got)
	}
}

// ─── nvmNewestBinDir ─────────────────────────────────────────────────────────

func TestNvmNewestBinDir_PicksHighestSemver(t *testing.T) {
	versionsDir := t.TempDir()
	for _, ver := range []string{"v18.20.0", "v22.1.0", "v20.15.3"} {
		if err := os.MkdirAll(filepath.Join(versionsDir, ver, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := nvmNewestBinDir(versionsDir)
	want := filepath.Join(versionsDir, "v22.1.0", "bin")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNvmNewestBinDir_IgnoresNonVPrefixDirs(t *testing.T) {
	versionsDir := t.TempDir()
	// Noise dirs without "v" prefix must be skipped; only v16.0.0 is valid.
	os.MkdirAll(filepath.Join(versionsDir, "v16.0.0", "bin"), 0o755) //nolint:errcheck
	os.MkdirAll(filepath.Join(versionsDir, "system"), 0o755)         //nolint:errcheck // no "v" prefix
	os.MkdirAll(filepath.Join(versionsDir, "lts"), 0o755)            //nolint:errcheck // no "v" prefix

	got := nvmNewestBinDir(versionsDir)
	want := filepath.Join(versionsDir, "v16.0.0", "bin")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNvmNewestBinDir_EmptyDir(t *testing.T) {
	versionsDir := t.TempDir()
	got := nvmNewestBinDir(versionsDir)
	if got != "" {
		t.Errorf("empty dir: got %q, want empty", got)
	}
}

// ─── RealExecutor.Run ────────────────────────────────────────────────────────

// TestRealExecutor_Run constructs a RealExecutor directly (bypassing New() to
// avoid mutating the global PATH via augmentOnce) and runs a simple command.
func TestRealExecutor_Run(t *testing.T) {
	r := &RealExecutor{}
	stdout, _, err := r.Run(context.Background(), "echo", "hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.TrimRight(stdout, "\n") != "hello" {
		t.Errorf("stdout = %q, want hello", stdout)
	}
}

// ─── discoverNodeManagerPaths ────────────────────────────────────────────────

// TestDiscoverNodeManagerPaths_NoHome verifies that discoverNodeManagerPaths
// returns nil/empty when HOME points to a directory that does not exist (so
// neither volta nor nvm directories can be found).
func TestDiscoverNodeManagerPaths_NoHome(t *testing.T) {
	t.Setenv("HOME", "/nonexistent-path-xyz-abc")
	got := discoverNodeManagerPaths()
	if len(got) != 0 {
		t.Errorf("expected empty paths with nonexistent HOME, got %v", got)
	}
}

// ─── resolveInEnv ─────────────────────────────────────────────────────────────

// TestResolveInEnv_FindsBinaryInAugmentedPath verifies that resolveInEnv picks
// up a binary that exists only in the augmented PATH entry, not in the current
// process PATH. This is the regression case: exec.CommandContext resolves
// binaries using the process PATH, ignoring cmd.Env. resolveInEnv must be used
// to pre-resolve the binary path so the correct binary is executed.
func TestResolveInEnv_FindsBinaryInAugmentedPath(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "mytool")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + dir + ":/usr/bin:/bin"}
	got := resolveInEnv("mytool", env)
	if got != binary {
		t.Errorf("resolveInEnv = %q, want %q", got, binary)
	}
}

func TestResolveInEnv_ReturnsNameWhenNotFound(t *testing.T) {
	env := []string{"PATH=/nonexistent-dir-xyz"}
	got := resolveInEnv("nosuchbinary", env)
	if got != "nosuchbinary" {
		t.Errorf("resolveInEnv = %q, want original name", got)
	}
}

func TestResolveInEnv_PassesThroughAbsolutePath(t *testing.T) {
	got := resolveInEnv("/usr/bin/env", []string{"PATH=/nonexistent"})
	if got != "/usr/bin/env" {
		t.Errorf("resolveInEnv = %q, want /usr/bin/env", got)
	}
}

func TestResolveInEnv_PrefersFirstMatchInPath(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	for _, d := range []string{dir1, dir2} {
		if err := os.WriteFile(filepath.Join(d, "tool"), []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env := []string{"PATH=" + dir1 + ":" + dir2}
	got := resolveInEnv("tool", env)
	want := filepath.Join(dir1, "tool")
	if got != want {
		t.Errorf("resolveInEnv = %q, want first match %q", got, want)
	}
}

func TestResolveCommand_UsesAugmentedPath(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, ".volta", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(binDir, "node-managed-tool")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")

	got, env := ResolveCommand("node-managed-tool")
	if got != binary {
		t.Fatalf("ResolveCommand path = %q, want %q", got, binary)
	}
	if len(env) == 0 || !strings.Contains(strings.Join(env, "\x00"), "PATH="+binDir) {
		t.Fatalf("ResolveCommand env does not contain augmented PATH entry %q: %v", binDir, env)
	}
}

func TestResolveCommand_UsesActiveNVMBinBeforeDefaultAlias(t *testing.T) {
	home, versionsDir := makeNvmHome(t)
	defaultBin := filepath.Join(versionsDir, "v20.10.0", "bin")
	activeBin := filepath.Join(versionsDir, "v24.15.0", "bin")
	for _, dir := range []string{defaultBin, activeBin} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "npm"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeAlias(t, filepath.Join(home, ".nvm"), "default", "v20.10.0\n")
	t.Setenv("HOME", home)
	t.Setenv("NVM_BIN", activeBin)
	t.Setenv("PATH", "/usr/bin:/bin")

	got, env := ResolveCommand("npm")
	want := filepath.Join(activeBin, "npm")
	if got != want {
		t.Fatalf("ResolveCommand path = %q, want active NVM_BIN %q", got, want)
	}
	pathEntry := strings.Join(env, "\x00")
	activeIdx := strings.Index(pathEntry, "PATH="+activeBin)
	defaultIdx := strings.Index(pathEntry, defaultBin)
	if activeIdx == -1 || defaultIdx == -1 || activeIdx > defaultIdx {
		t.Fatalf("ResolveCommand env should put NVM_BIN before default alias: %v", env)
	}
}

// ─── New ─────────────────────────────────────────────────────────────────────

func TestNew_ReturnsNonNil(t *testing.T) {
	r := New()
	if r == nil {
		t.Fatal("New() returned nil")
	}
}

// ─── nvmDefaultBinDir ────────────────────────────────────────────────────────

// makeNvmHome sets up a minimal fake HOME with a .nvm directory.
// Returns the home path and the versionsDir path.
func makeNvmHome(t *testing.T) (home, versionsDir string) {
	t.Helper()
	home = t.TempDir()
	versionsDir = filepath.Join(home, ".nvm", "versions", "node")
	aliasDir := filepath.Join(home, ".nvm", "alias")
	if err := os.MkdirAll(aliasDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(versionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return home, versionsDir
}

func writeAlias(t *testing.T, nvmDir, name, content string) {
	t.Helper()
	aliasDir := filepath.Join(nvmDir, "alias")
	// handle lts/* by creating the lts subdir
	if strings.Contains(name, "/") {
		if err := os.MkdirAll(filepath.Join(aliasDir, filepath.Dir(name)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(aliasDir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNvmDefaultBinDir_NvmDirAbsent(t *testing.T) {
	home := t.TempDir() // no .nvm subdir
	if got := nvmDefaultBinDir(home); got != "" {
		t.Errorf("expected empty without .nvm, got %q", got)
	}
}

func TestNvmDefaultBinDir_AliasResolvesToVersionWithBin(t *testing.T) {
	home, versionsDir := makeNvmHome(t)
	binDir := filepath.Join(versionsDir, "v22.16.0", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAlias(t, filepath.Join(home, ".nvm"), "default", "v22.16.0\n")

	got := nvmDefaultBinDir(home)
	if got != binDir {
		t.Errorf("got %q, want %q", got, binDir)
	}
}

func TestNvmDefaultBinDir_AliasResolvesButBinAbsent_FallsBackToNewest(t *testing.T) {
	// default alias → v22.16.0, but v22.16.0/bin doesn't exist.
	// Raw default "v22.16.0" → nvmMajorVersionBinDir fails (not a bare integer).
	// Falls back to nvmNewestBinDir → finds v20.1.0.
	home, versionsDir := makeNvmHome(t)
	writeAlias(t, filepath.Join(home, ".nvm"), "default", "v22.16.0\n")
	// Create a different version with a bin dir.
	fallback := filepath.Join(versionsDir, "v20.1.0", "bin")
	if err := os.MkdirAll(fallback, 0o755); err != nil {
		t.Fatal(err)
	}

	got := nvmDefaultBinDir(home)
	if got != fallback {
		t.Errorf("got %q, want fallback %q", got, fallback)
	}
}

func TestNvmDefaultBinDir_BareMajorAlias_ReturvesMajorVersionBin(t *testing.T) {
	// default alias → "22" (bare integer, not resolvable by resolveNvmAlias).
	// Falls through to raw read → "22" → nvmMajorVersionBinDir picks v22.1.0.
	home, versionsDir := makeNvmHome(t)
	writeAlias(t, filepath.Join(home, ".nvm"), "default", "22\n")
	binDir := filepath.Join(versionsDir, "v22.1.0", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	got := nvmDefaultBinDir(home)
	if got != binDir {
		t.Errorf("got %q, want %q", got, binDir)
	}
}

func TestNvmDefaultBinDir_NoAliasFile_FallsBackToNewest(t *testing.T) {
	// No alias/default file at all → resolveNvmAlias returns "".
	// ReadFile of default also fails → falls back to nvmNewestBinDir.
	home, versionsDir := makeNvmHome(t)
	binDir := filepath.Join(versionsDir, "v18.20.0", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	got := nvmDefaultBinDir(home)
	if got != binDir {
		t.Errorf("got %q, want %q", got, binDir)
	}
}

// ─── nvmMajorVersionBinDir ───────────────────────────────────────────────────

func TestNvmMajorVersionBinDir_InvalidMajor(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []string{"", "abc", "0", "-1"} {
		if got := nvmMajorVersionBinDir(dir, bad); got != "" {
			t.Errorf("major=%q: expected empty, got %q", bad, got)
		}
	}
}

func TestNvmMajorVersionBinDir_NoMatchingVersion(t *testing.T) {
	versionsDir := t.TempDir()
	// Only v18 installed, asking for 22.
	os.MkdirAll(filepath.Join(versionsDir, "v18.20.0", "bin"), 0o755) //nolint:errcheck
	if got := nvmMajorVersionBinDir(versionsDir, "22"); got != "" {
		t.Errorf("expected empty for unmatched major, got %q", got)
	}
}

func TestNvmMajorVersionBinDir_MatchPresent_BinDirAbsent(t *testing.T) {
	versionsDir := t.TempDir()
	// v22.1.0 dir exists but without a bin subdir.
	os.MkdirAll(filepath.Join(versionsDir, "v22.1.0"), 0o755) //nolint:errcheck
	if got := nvmMajorVersionBinDir(versionsDir, "22"); got != "" {
		t.Errorf("expected empty when bin dir absent, got %q", got)
	}
}

func TestNvmMajorVersionBinDir_PicksNewestOfMatchingMajor(t *testing.T) {
	versionsDir := t.TempDir()
	for _, ver := range []string{"v22.1.0", "v22.3.5", "v22.2.0"} {
		os.MkdirAll(filepath.Join(versionsDir, ver, "bin"), 0o755) //nolint:errcheck
	}
	got := nvmMajorVersionBinDir(versionsDir, "22")
	want := filepath.Join(versionsDir, "v22.3.5", "bin")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ─── nvmNewestBinDir (additional) ────────────────────────────────────────────

func TestNvmNewestBinDir_VersionDirWithoutBinSubdir(t *testing.T) {
	versionsDir := t.TempDir()
	// v20.1.0 dir exists but has no bin subdir.
	os.MkdirAll(filepath.Join(versionsDir, "v20.1.0"), 0o755) //nolint:errcheck
	if got := nvmNewestBinDir(versionsDir); got != "" {
		t.Errorf("expected empty when bin subdir absent, got %q", got)
	}
}

// ─── discoverNodeManagerPaths (additional) ───────────────────────────────────

// ─── resolveNvmAlias (additional) ────────────────────────────────────────────

func TestResolveNvmAlias_PathTraversalRejected(t *testing.T) {
	// An alias value that contains "/" but doesn't start with "lts/" should be
	// rejected as a path-traversal attempt and return "".
	nvmDir := t.TempDir()
	aliasDir := filepath.Join(nvmDir, "alias")
	os.MkdirAll(aliasDir, 0o755) //nolint:errcheck
	// "../../evil" contains "/" and doesn't start with "lts/"
	os.WriteFile(filepath.Join(aliasDir, "default"), []byte("../../evil\n"), 0o644) //nolint:errcheck

	got := resolveNvmAlias(nvmDir, "default", 8)
	if got != "" {
		t.Errorf("path traversal alias: expected empty, got %q", got)
	}
}

// ─── nvmMajorVersionBinDir (additional) ──────────────────────────────────────

func TestNvmMajorVersionBinDir_NonThreePartVersionSkipped(t *testing.T) {
	versionsDir := t.TempDir()
	// "v22" and "v22.1" are not three-part and must be ignored.
	// Only v22.2.0 is valid and has a bin dir.
	os.MkdirAll(filepath.Join(versionsDir, "v22"), 0o755)   //nolint:errcheck
	os.MkdirAll(filepath.Join(versionsDir, "v22.1"), 0o755) //nolint:errcheck
	binDir := filepath.Join(versionsDir, "v22.2.0", "bin")
	os.MkdirAll(binDir, 0o755) //nolint:errcheck

	got := nvmMajorVersionBinDir(versionsDir, "22")
	if got != binDir {
		t.Errorf("got %q, want %q", got, binDir)
	}
}

// ─── nvmNewestBinDir (additional) ────────────────────────────────────────────

func TestNvmNewestBinDir_NonThreePartVersionsSkipped(t *testing.T) {
	versionsDir := t.TempDir()
	// Malformed dirs must be ignored; only v18.1.0 is valid.
	os.MkdirAll(filepath.Join(versionsDir, "v18"), 0o755)   //nolint:errcheck
	os.MkdirAll(filepath.Join(versionsDir, "v18.0"), 0o755) //nolint:errcheck
	binDir := filepath.Join(versionsDir, "v18.1.0", "bin")
	os.MkdirAll(binDir, 0o755) //nolint:errcheck

	got := nvmNewestBinDir(versionsDir)
	if got != binDir {
		t.Errorf("got %q, want %q", got, binDir)
	}
}

func TestNvmNewestBinDir_NonNumericComponentSkipped(t *testing.T) {
	versionsDir := t.TempDir()
	// "v18.abc.0" has a non-numeric minor → parse error → skipped.
	// "v16.0.0" is the only valid entry.
	os.MkdirAll(filepath.Join(versionsDir, "v18.abc.0"), 0o755) //nolint:errcheck
	binDir := filepath.Join(versionsDir, "v16.0.0", "bin")
	os.MkdirAll(binDir, 0o755) //nolint:errcheck

	got := nvmNewestBinDir(versionsDir)
	if got != binDir {
		t.Errorf("got %q, want %q", got, binDir)
	}
}

func TestNvmMajorVersionBinDir_NonNumericComponentSkipped(t *testing.T) {
	versionsDir := t.TempDir()
	// "v22.abc.0" has a non-numeric minor → parse error → skipped.
	// "v22.1.0" is the only valid match.
	os.MkdirAll(filepath.Join(versionsDir, "v22.abc.0"), 0o755) //nolint:errcheck
	binDir := filepath.Join(versionsDir, "v22.1.0", "bin")
	os.MkdirAll(binDir, 0o755) //nolint:errcheck

	got := nvmMajorVersionBinDir(versionsDir, "22")
	if got != binDir {
		t.Errorf("got %q, want %q", got, binDir)
	}
}

// ─── discoverNodeManagerPaths (additional) ───────────────────────────────────

func TestDiscoverNodeManagerPaths_WithVoltaAndBun(t *testing.T) {
	home := t.TempDir()
	voltaBin := filepath.Join(home, ".volta", "bin")
	bunBin := filepath.Join(home, ".bun", "bin")
	os.MkdirAll(voltaBin, 0o755) //nolint:errcheck
	os.MkdirAll(bunBin, 0o755)   //nolint:errcheck
	t.Setenv("HOME", home)

	paths := discoverNodeManagerPaths()

	hasVolta, hasBun := false, false
	for _, p := range paths {
		if p == voltaBin {
			hasVolta = true
		}
		if p == bunBin {
			hasBun = true
		}
	}
	if !hasVolta {
		t.Errorf("volta bin dir %q not in paths %v", voltaBin, paths)
	}
	if !hasBun {
		t.Errorf("bun bin dir %q not in paths %v", bunBin, paths)
	}
}

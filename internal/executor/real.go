package executor

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// RealExecutor runs commands via os/exec.
// PATH is transparently augmented with node version manager directories
// (nvm, volta) so that npm/pnpm/bun are found even when the process was
// launched outside an initialised shell session.
//
// The augmented PATH is passed per-command via cmd.Env rather than mutating
// the process-global PATH via os.Setenv. This keeps commands hermetic and
// avoids data races in parallel tests.
type RealExecutor struct{}

// New returns a RealExecutor.
func New() *RealExecutor {
	return &RealExecutor{}
}

// Run executes name with args and captures stdout/stderr.
// It returns a non-nil error when the process exits with a non-zero code.
// Each subprocess receives a copy of the current environment with the
// augmented PATH (nvm/volta bin dirs prepended) injected via cmd.Env.
func (r *RealExecutor) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	env := augmentedEnv()

	// exec.CommandContext resolves the binary using the current process PATH,
	// not cmd.Env. For tools like nvm-managed npm that only exist in the augmented
	// PATH, we must resolve the binary explicitly before creating the command.
	resolved := resolveInEnv(name, env)

	cmd := exec.CommandContext(ctx, resolved, args...)
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// resolveInEnv looks up binary in the PATH extracted from env.
// Returns the full path if found, otherwise returns name unchanged
// (letting the OS produce a meaningful "not found" error).
func resolveInEnv(name string, env []string) string {
	if strings.ContainsRune(name, '/') {
		return name // already a path
	}
	for _, e := range env {
		if !strings.HasPrefix(e, "PATH=") {
			continue
		}
		augPath := strings.TrimPrefix(e, "PATH=")
		for _, dir := range filepath.SplitList(augPath) {
			candidate := filepath.Join(dir, name)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
				return candidate
			}
		}
	}
	return name
}

// ── PATH augmentation ────────────────────────────────────────────────────────

// augmentedEnv returns a copy of os.Environ() with nvm/volta bin dirs
// prepended to PATH. The original process environment is never modified.
func augmentedEnv() []string {
	extra := discoverNodeManagerPaths()
	env := os.Environ()
	if len(extra) == 0 {
		return env
	}
	pathSep := string(os.PathListSeparator)
	prefix := strings.Join(extra, pathSep)
	for i, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			current := strings.TrimPrefix(e, "PATH=")
			if current != "" {
				prefix += pathSep + current
			}
			env[i] = "PATH=" + prefix
			return env
		}
	}
	// No PATH entry found — append one.
	return append(env, "PATH="+prefix)
}

// discoverNodeManagerPaths returns bin directories for nvm, volta, and bun,
// in the order they should appear at the front of PATH.
func discoverNodeManagerPaths() []string {
	if runtime.GOOS == "windows" {
		return nil // nvm/volta use different layouts on Windows
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var paths []string
	// Volta: single fixed location, manages shims itself.
	if d := filepath.Join(home, ".volta", "bin"); isDir(d) {
		paths = append(paths, d)
	}
	// nvm: resolve the default alias chain to the active version's bin dir.
	if d := nvmDefaultBinDir(home); d != "" {
		paths = append(paths, d)
	}
	// bun: fixed location ~/.bun/bin.
	if d := filepath.Join(home, ".bun", "bin"); isDir(d) {
		paths = append(paths, d)
	}
	return paths
}

// nvmDefaultBinDir resolves ~/.nvm/alias/default through the alias chain to
// a concrete installed-version bin directory.
// Handles three alias formats:
//   - Concrete version: "v22.16.0" → direct lookup.
//   - LTS alias chain: "lts/*" → "lts/iron" → "v20.x.y".
//   - Bare major version: "22" → newest installed v22.x.y (nvm convention).
//
// Falls back to the newest installed version overall when the chain cannot be
// resolved (e.g. a stale alias pointing to an uninstalled version).
func nvmDefaultBinDir(home string) string {
	nvmDir := filepath.Join(home, ".nvm")
	if !isDir(nvmDir) {
		return ""
	}
	versionsDir := filepath.Join(nvmDir, "versions", "node")

	if version := resolveNvmAlias(nvmDir, "default", 8); version != "" {
		if d := filepath.Join(versionsDir, version, "bin"); isDir(d) {
			return d
		}
	}

	// resolveNvmAlias could not follow the chain (e.g. bare major version "22"
	// has no corresponding alias file). Read the raw default value and try to
	// match it as a major version integer before falling back to newest overall.
	if raw, err := os.ReadFile(filepath.Join(nvmDir, "alias", "default")); err == nil {
		if d := nvmMajorVersionBinDir(versionsDir, strings.TrimSpace(string(raw))); d != "" {
			return d
		}
	}

	// Last resort: newest installed version.
	return nvmNewestBinDir(versionsDir)
}

// resolveNvmAlias follows the nvm alias chain (default → lts/* → lts/krypton →
// v18.x.y). Returns the version string (e.g. "v22.16.0") or "" if unresolvable.
func resolveNvmAlias(nvmDir, alias string, maxHops int) string {
	for i := 0; i < maxHops; i++ {
		raw, err := os.ReadFile(filepath.Join(nvmDir, "alias", alias))
		if err != nil {
			return ""
		}
		val := strings.TrimSpace(string(raw))
		if strings.HasPrefix(val, "v") {
			return val // concrete version found
		}
		// Reject path traversal; allow lts/* which is a valid nvm alias prefix.
		if strings.ContainsAny(val, "/\\") && !strings.HasPrefix(val, "lts/") {
			return ""
		}
		alias = val // follow the next hop (e.g. "lts/*" or "lts/krypton")
	}
	return ""
}

// nvmMajorVersionBinDir returns the bin dir of the newest installed nvm version
// whose major component equals the given string (e.g. "22" → newest v22.x.y).
// Returns "" when major is not a positive integer or no matching version exists.
func nvmMajorVersionBinDir(versionsDir, major string) string {
	maj, err := strconv.Atoi(major)
	if err != nil || maj <= 0 {
		return ""
	}
	entries, err := os.ReadDir(versionsDir)
	if err != nil {
		return ""
	}
	type nv struct {
		name         string
		minor, patch int
	}
	var matches []nv
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "v") {
			continue
		}
		parts := strings.SplitN(strings.TrimPrefix(e.Name(), "v"), ".", 3)
		if len(parts) != 3 {
			continue
		}
		m, e1 := strconv.Atoi(parts[0])
		min, e2 := strconv.Atoi(parts[1])
		pat, e3 := strconv.Atoi(parts[2])
		if e1 != nil || e2 != nil || e3 != nil || m != maj {
			continue
		}
		matches = append(matches, nv{e.Name(), min, pat})
	}
	if len(matches) == 0 {
		return ""
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].minor != matches[j].minor {
			return matches[i].minor > matches[j].minor
		}
		return matches[i].patch > matches[j].patch
	})
	d := filepath.Join(versionsDir, matches[0].name, "bin")
	if isDir(d) {
		return d
	}
	return ""
}

// nvmNewestBinDir picks the bin dir of the highest semver installed under
// nodeVersionsDir (e.g. ~/.nvm/versions/node/).
func nvmNewestBinDir(nodeVersionsDir string) string {
	entries, err := os.ReadDir(nodeVersionsDir)
	if err != nil {
		return ""
	}
	type nv struct {
		name           string
		major, minor, patch int
	}
	var versions []nv
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "v") {
			continue
		}
		parts := strings.SplitN(strings.TrimPrefix(e.Name(), "v"), ".", 3)
		if len(parts) != 3 {
			continue
		}
		maj, e1 := strconv.Atoi(parts[0])
		min, e2 := strconv.Atoi(parts[1])
		pat, e3 := strconv.Atoi(parts[2])
		if e1 != nil || e2 != nil || e3 != nil {
			continue
		}
		versions = append(versions, nv{e.Name(), maj, min, pat})
	}
	if len(versions) == 0 {
		return ""
	}
	sort.Slice(versions, func(i, j int) bool {
		a, b := versions[i], versions[j]
		if a.major != b.major {
			return a.major > b.major
		}
		if a.minor != b.minor {
			return a.minor > b.minor
		}
		return a.patch > b.patch
	})
	d := filepath.Join(nodeVersionsDir, versions[0].name, "bin")
	if isDir(d) {
		return d
	}
	return ""
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

package executor

import (
	"bytes"
	"context"
	"io"
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
	resolved, env := ResolveCommand(name)

	cmd := exec.CommandContext(ctx, resolved, args...)
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	stdoutWriter := &outputWriter{ctx: ctx, dst: io.Writer(&stdout)}
	stderrWriter := &outputWriter{ctx: ctx, dst: io.Writer(&stderr)}
	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter
	err := cmd.Run()
	stdoutWriter.flush()
	stderrWriter.flush()
	return stdout.String(), stderr.String(), err
}

type outputWriter struct {
	ctx     context.Context
	dst     io.Writer
	pending []byte
}

func (w *outputWriter) Write(p []byte) (int, error) {
	n, err := w.dst.Write(p)
	var latest string
	for _, b := range p[:n] {
		if b != '\n' && b != '\r' {
			w.pending = append(w.pending, b)
			continue
		}
		if line := sanitizeOutputLine(w.pending); line != "" {
			latest = line
		}
		w.pending = w.pending[:0]
	}
	// ponytail: the status bar can show one line; keep full logs in dst.
	if observer := outputObserver(w.ctx); observer != nil && latest != "" {
		observer(latest)
	}
	return n, err
}

func (w *outputWriter) flush() {
	line := sanitizeOutputLine(w.pending)
	w.pending = nil
	if observer := outputObserver(w.ctx); observer != nil && line != "" {
		observer(line)
	}
}

// ResolveCommand returns the executable path and environment used by
// RealExecutor. exec.CommandContext resolves binaries using the current process
// PATH, not cmd.Env, so callers that build their own exec.Cmd must pre-resolve
// names through the same augmented PATH.
func ResolveCommand(name string) (string, []string) {
	env := augmentedEnv()
	if resolved, ok := lookupInEnv(name, env); ok {
		return resolved, env
	}
	return name, env
}

// CommandAvailable reports whether name resolves to an executable through the
// same augmented PATH RealExecutor uses for subprocesses.
func CommandAvailable(name string) bool {
	_, ok := lookupInEnv(name, augmentedEnv())
	return ok
}

// CommandAvailable reports whether name resolves to an executable through the
// same augmented PATH this executor uses for subprocesses.
func (r *RealExecutor) CommandAvailable(name string) bool {
	return CommandAvailable(name)
}

func lookupInEnv(name string, env []string) (string, bool) {
	if strings.ContainsRune(name, '/') {
		if isExecutableFile(name) {
			return name, true
		}
		return "", false
	}
	for _, e := range env {
		if !strings.HasPrefix(e, "PATH=") {
			continue
		}
		augPath := strings.TrimPrefix(e, "PATH=")
		for _, dir := range filepath.SplitList(augPath) {
			candidate := filepath.Join(dir, name)
			if isExecutableFile(candidate) {
				return candidate, true
			}
		}
	}
	return "", false
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
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

// discoverNodeManagerPaths returns bin directories for node version managers,
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
	addPath := func(path string) {
		if path == "" || !filepath.IsAbs(path) || !isDir(path) {
			return
		}
		for _, existing := range paths {
			if existing == path {
				return
			}
		}
		paths = append(paths, path)
	}
	// nvm: honour the currently selected shell version first.
	addPath(os.Getenv("NVM_BIN"))
	// Volta: single fixed location, manages shims itself.
	addPath(filepath.Join(home, ".volta", "bin"))
	// nvm: resolve the default alias chain as the non-interactive fallback.
	addPath(nvmDefaultBinDir(home))
	// bun: fixed location ~/.bun/bin.
	addPath(filepath.Join(home, ".bun", "bin"))
	return paths
}

// nvmDefaultBinDir resolves $NVM_DIR/alias/default (or ~/.nvm/alias/default
// when NVM_DIR is unset, matching nvm.sh's own default) through the alias
// chain to a concrete installed-version bin directory.
// Handles three alias formats:
//   - Concrete version: "v22.16.0" → direct lookup.
//   - LTS alias chain: "lts/*" → "lts/iron" → "v20.x.y".
//   - Bare major version: "22" → newest installed v22.x.y (nvm convention).
//
// Falls back to the newest installed version overall when the chain cannot be
// resolved (e.g. a stale alias pointing to an uninstalled version).
func nvmDefaultBinDir(home string) string {
	nvmDir := os.Getenv("NVM_DIR")
	if nvmDir == "" {
		nvmDir = filepath.Join(home, ".nvm")
	}
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
		name                string
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

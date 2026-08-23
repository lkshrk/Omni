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
	"time"
)

// Grace period between killing a cancelled command and abandoning its output pipes.
var waitDelay = 5 * time.Second

// RealExecutor — PATH augmentation is per-command via cmd.Env, never os.Setenv: hermetic, race-free.
type RealExecutor struct{}

func New() *RealExecutor {
	return &RealExecutor{}
}

func (r *RealExecutor) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	return r.run(ctx, nil, "", nil, name, args...)
}

func (r *RealExecutor) RunEnv(ctx context.Context, overlay []string, name string, args ...string) (string, string, error) {
	return r.run(ctx, overlay, "", nil, name, args...)
}

func (r *RealExecutor) RunDir(ctx context.Context, dir, name string, args ...string) (string, string, error) {
	return r.run(ctx, nil, dir, nil, name, args...)
}

func (r *RealExecutor) RunDirEnv(ctx context.Context, dir string, overlay []string, name string, args ...string) (string, string, error) {
	return r.run(ctx, overlay, dir, nil, name, args...)
}

func (r *RealExecutor) RunDirEnvStdin(ctx context.Context, dir string, overlay []string, stdin []byte, name string, args ...string) (string, string, error) {
	return r.run(ctx, overlay, dir, stdin, name, args...)
}

func (r *RealExecutor) run(ctx context.Context, overlay []string, dir string, stdin []byte, name string, args ...string) (string, string, error) {
	resolved, env := resolveCommandWithEnv(name, overlay)

	cmd := exec.CommandContext(ctx, resolved, args...)
	cmd.Dir = dir
	cmd.Env = env
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	// Wait blocks on pipe-copy goroutines, so a grandchild holding a pipe open can outlive cancellation.
	cmd.WaitDelay = waitDelay
	var stdout, stderr bytes.Buffer
	limit := outputLimit(ctx)
	stdoutWriter := &outputWriter{ctx: ctx, dst: io.Writer(&stdout), limit: limit}
	stderrWriter := &outputWriter{ctx: ctx, dst: io.Writer(&stderr), limit: limit}
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
	limit   int
	written int
	pending []byte
}

// Bytes past the limit are dropped because a short write would fail the command.
func (w *outputWriter) Write(p []byte) (int, error) {
	keep := p
	if w.limit > 0 {
		if remaining := w.limit - w.written; remaining < len(keep) {
			keep = keep[:max(remaining, 0)]
		}
	}
	if len(keep) > 0 {
		if _, err := w.dst.Write(keep); err != nil {
			return 0, err
		}
		w.written += len(keep)
	}
	observer := outputObserver(w.ctx)
	for _, b := range p {
		if b != '\n' && b != '\r' {
			if w.limit == 0 || len(w.pending) < w.limit {
				w.pending = append(w.pending, b)
			}
			continue
		}
		if line := sanitizeOutputLine(w.pending); observer != nil && line != "" {
			observer(line)
		}
		w.pending = w.pending[:0]
	}
	return len(p), nil
}

func (w *outputWriter) flush() {
	line := sanitizeOutputLine(w.pending)
	w.pending = nil
	if observer := outputObserver(w.ctx); observer != nil && line != "" {
		observer(line)
	}
}

// ResolveCommand — exec.CommandContext resolves against the process PATH, not cmd.Env — pre-resolve here.
func ResolveCommand(name string) (string, []string) {
	return resolveCommandWithEnv(name, nil)
}

func resolveCommandWithEnv(name string, overlay []string) (string, []string) {
	env := mergeEnv(augmentedEnv(), overlay)
	if resolved, ok := lookupInEnv(name, env); ok {
		return resolved, env
	}
	return name, env
}

func mergeEnv(base, overlay []string) []string {
	merged := append([]string(nil), base...)
	positions := make(map[string]int, len(merged))
	for i, entry := range merged {
		positions[envKey(entry)] = i
	}
	for _, entry := range overlay {
		key := envKey(entry)
		i, exists := positions[key]
		if !strings.Contains(entry, "=") {
			if !exists {
				continue
			}
			merged = append(merged[:i], merged[i+1:]...)
			positions = make(map[string]int, len(merged))
			for j, current := range merged {
				positions[envKey(current)] = j
			}
			continue
		}
		if exists {
			merged[i] = entry
			continue
		}
		positions[key] = len(merged)
		merged = append(merged, entry)
	}
	return merged
}

func envKey(entry string) string {
	key, _, _ := strings.Cut(entry, "=")
	if runtime.GOOS == "windows" {
		return strings.ToUpper(key)
	}
	return key
}

func CommandAvailable(name string) bool {
	_, ok := lookupInEnv(name, augmentedEnv())
	return ok
}

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
	return append(env, "PATH="+prefix)
}

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
	addPath(filepath.Join(home, ".volta", "bin"))
	// nvm: resolve the default alias chain as the non-interactive fallback.
	addPath(nvmDefaultBinDir(home))
	addPath(filepath.Join(home, ".bun", "bin"))
	return paths
}

// Mirrors nvm.sh: $NVM_DIR, else ~/.nvm. Aliases are concrete, an "lts/*" chain, or a bare major.
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

	// A bare major like "22" has no alias file, so match the raw value instead.
	if raw, err := os.ReadFile(filepath.Join(nvmDir, "alias", "default")); err == nil {
		if d := nvmMajorVersionBinDir(versionsDir, strings.TrimSpace(string(raw))); d != "" {
			return d
		}
	}

	return nvmNewestBinDir(versionsDir)
}

func resolveNvmAlias(nvmDir, alias string, maxHops int) string {
	for i := 0; i < maxHops; i++ {
		raw, err := os.ReadFile(filepath.Join(nvmDir, "alias", alias))
		if err != nil {
			return ""
		}
		val := strings.TrimSpace(string(raw))
		if strings.HasPrefix(val, "v") {
			return val
		}
		// Reject path traversal; allow lts/* which is a valid nvm alias prefix.
		if strings.ContainsAny(val, "/\\") && !strings.HasPrefix(val, "lts/") {
			return ""
		}
		alias = val
	}
	return ""
}

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

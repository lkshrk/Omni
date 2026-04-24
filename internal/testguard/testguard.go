// Package testguard centralizes local go test filesystem safety checks.
package testguard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const isolatedEnv = "OMNI_TEST_ISOLATED"

var (
	ensureOnce sync.Once
	ensureErr  error
)

func init() {
	if Active() {
		MustEnsureSafeEnv()
	}
}

// Active reports whether local go test path guards should be enforced.
func Active() bool {
	return runningUnderGoTest() && !Isolated()
}

// Isolated reports whether tests are already running inside an explicit sandbox.
func Isolated() bool {
	return os.Getenv(isolatedEnv) == "1" || os.Getenv("OMNI_PMCONTAINER") == "1"
}

// MustEnsureSafeEnv installs temp HOME/XDG defaults for local unit tests.
func MustEnsureSafeEnv() {
	if err := EnsureSafeEnv(); err != nil {
		panic(err)
	}
}

// EnsureSafeEnv installs temp HOME/XDG defaults for local unit tests.
func EnsureSafeEnv() error {
	if !Active() {
		return nil
	}
	ensureOnce.Do(func() {
		root, err := os.MkdirTemp("", "omni-local-test-*")
		if err != nil {
			ensureErr = fmt.Errorf("creating local test sandbox: %w", err)
			return
		}
		setSafeEnv := func(key, path string) {
			if current := os.Getenv(key); current != "" && PathInTempRoot(current) {
				return
			}
			if err := os.Setenv(key, path); err != nil && ensureErr == nil {
				ensureErr = fmt.Errorf("setting %s for local test sandbox: %w", key, err)
			}
		}
		setSafeEnv("HOME", filepath.Join(root, "home"))
		setSafeEnv("XDG_CONFIG_HOME", filepath.Join(root, "xdg-config"))
		setSafeEnv("XDG_CACHE_HOME", filepath.Join(root, "xdg-cache"))
		setSafeEnv("OMNI_CACHE_DIR", filepath.Join(root, "omni-cache"))
	})
	return ensureErr
}

// RequireHome rejects a live HOME during local go test runs.
func RequireHome(context string) error {
	if !Active() {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("unsafe local test setup: resolve HOME for %s: %w", context, err)
	}
	return RequireTempPath("HOME for "+context, home)
}

// RequireTempPath rejects non-temp filesystem paths during local go test runs.
func RequireTempPath(kind, path string) error {
	if !Active() {
		return nil
	}
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("unsafe local test setup: %s path is empty", kind)
	}
	if !PathInTempRoot(path) {
		return fmt.Errorf("unsafe local test setup: %s=%q is outside a temp directory", kind, path)
	}
	return nil
}

// PathInTempRoot reports whether path is inside the platform temp directory.
func PathInTempRoot(path string) bool {
	for _, root := range tempRoots() {
		if PathInRoot(path, root) {
			return true
		}
	}
	return false
}

// PathInRoot reports whether path is root or a descendant of root.
func PathInRoot(path, root string) bool {
	for _, p := range canonicalPathCandidates(path) {
		for _, r := range canonicalPathCandidates(root) {
			if p == r || strings.HasPrefix(p, r+string(os.PathSeparator)) {
				return true
			}
		}
	}
	return false
}

func runningUnderGoTest() bool {
	base := filepath.Base(os.Args[0])
	return strings.HasSuffix(base, ".test") || strings.Contains(base, ".test.")
}

func tempRoots() []string {
	roots := []string{os.TempDir(), "/tmp"}
	if _, err := os.Stat("/private/tmp"); err == nil {
		roots = append(roots, "/private/tmp")
	}
	return roots
}

func canonicalPathCandidates(path string) []string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = filepath.Clean(path)
	}
	out := []string{filepath.Clean(abs)}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		resolved = filepath.Clean(resolved)
		if resolved != out[0] {
			out = append(out, resolved)
		}
	}
	return out
}

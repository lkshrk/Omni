// Package testguard centralizes local go test filesystem safety checks.
package testguard

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
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

func Active() bool {
	return runningUnderGoTest() && !Isolated()
}

func Isolated() bool {
	return os.Getenv(isolatedEnv) == "1" || os.Getenv("OMNI_PMCONTAINER") == "1"
}

func MustEnsureSafeEnv() {
	if err := EnsureSafeEnv(); err != nil {
		panic(err)
	}
}

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
			if err := os.Setenv(key, path); err != nil && ensureErr == nil {
				ensureErr = fmt.Errorf("setting %s for local test sandbox: %w", key, err)
			}
		}
		setSafeEnv("HOME", filepath.Join(root, "home"))
		setSafeEnv("XDG_CONFIG_HOME", filepath.Join(root, "xdg-config"))
		setSafeEnv("XDG_CACHE_HOME", filepath.Join(root, "xdg-cache"))
		setSafeEnv("OMNI_CACHE_DIR", filepath.Join(root, "omni-cache"))
		setSafeEnv("OMNI_CONFIG", filepath.Join(root, "xdg-config", "omni", "settings.json"))
	})
	return ensureErr
}

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

func PathInTempRoot(path string) bool {
	for _, root := range tempRoots() {
		if PathInRoot(path, root) {
			return true
		}
	}
	return false
}

func PathInRoot(path, root string) bool {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absPath = filepath.Clean(absPath)
	absRoot = filepath.Clean(absRoot)
	if absPath == absRoot {
		return true
	}
	resolvedRoot, err := resolveContainmentRoot(absRoot)
	if err != nil {
		return false
	}
	resolvedPath, err := resolveContainmentParent(absPath)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedPath)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
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

func resolveContainmentRoot(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return filepath.Clean(resolved), nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	return resolveContainmentParent(path)
}

func resolveContainmentParent(path string) (string, error) {
	path = filepath.Clean(path)
	leaf := filepath.Base(path)
	parent := filepath.Dir(path)
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(parent)
		if err == nil {
			resolved = filepath.Clean(resolved)
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Join(resolved, leaf), nil
		}
		if !os.IsNotExist(err) && !errors.Is(err, syscall.ENOTDIR) {
			return "", err
		}
		next := filepath.Dir(parent)
		if next == parent {
			return "", err
		}
		missing = append(missing, filepath.Base(parent))
		parent = next
	}
}

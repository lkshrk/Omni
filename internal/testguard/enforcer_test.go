package testguard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const guardImportPath = "github.com/lkshrk/omni/internal/testguard"

func TestEveryTestPackageImportsGuard(t *testing.T) {
	root := repoRoot(t)
	var missing []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if shouldSkipDir(rel, d.Name()) {
			return filepath.SkipDir
		}
		if rel == filepath.Join("internal", "testguard") {
			return filepath.SkipDir
		}
		hasTests, hasGuard, err := testPackageGuardState(path)
		if err != nil {
			return err
		}
		if hasTests && !hasGuard {
			missing = append(missing, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scanning repo: %v", err)
	}
	if len(missing) > 0 {
		t.Fatalf("test packages missing guard import %q: %s", guardImportPath, strings.Join(missing, ", "))
	}
}

func TestMakeTestTargetsUseSafeRunner(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "Makefile"))
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}
	makefile := string(data)
	for _, want := range []string{
		"TEST_SAFE   := bash scripts/run-test-safe.sh",
		"TEST_UNIT_ROOT := $(TMP_DIR)/test-unit-root",
		"test: test-scripts test-unit",
		"$(TEST_SAFE) bash scripts/test-release.sh",
		"OMNI_TEST_ROOT=\"$(TEST_UNIT_ROOT)\" $(TEST_SAFE) go test -race -trimpath ./...",
	} {
		if !strings.Contains(makefile, want) {
			t.Fatalf("Makefile test target is missing safe runner command %q", want)
		}
	}
	if strings.Contains(makefile, "go clean -testcache") {
		t.Fatal("Makefile test targets should preserve Go's test cache")
	}
}

func TestSafeRunnerDisablesGoTelemetry(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "run-test-safe.sh"))
	if err != nil {
		t.Fatalf("reading safe runner: %v", err)
	}
	if !strings.Contains(string(data), "go telemetry off") {
		t.Fatal("safe runner should disable Go telemetry inside isolated HOME before running tests")
	}
}

func TestSafeRunnerCleansReadOnlyGoModuleCache(t *testing.T) {
	root := repoRoot(t)
	cmd := exec.Command("bash", filepath.Join(root, "scripts", "run-test-safe.sh"), "bash", "-c", `mkdir -p "$HOME/go/pkg/mod/example"; touch "$HOME/go/pkg/mod/example/go.mod"; chmod -R a-w "$HOME/go/pkg/mod"`)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("safe runner should clean read-only Go module cache: %v\n%s", err, out)
	}
}

func TestMakePruneTmpBoundsRepoCaches(t *testing.T) {
	root := repoRoot(t)
	work := t.TempDir()
	makefile, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(work, "Makefile"), makefile, 0o644); err != nil {
		t.Fatalf("writing Makefile fixture: %v", err)
	}
	cacheDir := filepath.Join(work, ".tmp", "go-build")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("creating cache dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "cache-entry"), []byte("x"), 0o644); err != nil {
		t.Fatalf("writing cache entry: %v", err)
	}

	cmd := exec.Command("make", "prune-tmp", "TMP_MAX_MB=0")
	cmd.Dir = work
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("prune-tmp should remove oversized repo caches: %v\n%s", err, out)
	}
	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Fatalf("cache dir should be pruned, stat error = %v", err)
	}
}

func shouldSkipDir(rel, name string) bool {
	if rel == "." {
		return false
	}
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "vendor", "node_modules", "dist", "build", "bin":
		return true
	default:
		return false
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		next := filepath.Dir(dir)
		if next == dir {
			t.Fatal("go.mod not found")
		}
		dir = next
	}
}

func testPackageGuardState(dir string) (bool, bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, false, err
	}
	hasTests := false
	hasGuard := false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		hasTests = true
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return false, false, err
		}
		if strings.Contains(string(data), guardImportPath) {
			hasGuard = true
		}
	}
	return hasTests, hasGuard, nil
}
